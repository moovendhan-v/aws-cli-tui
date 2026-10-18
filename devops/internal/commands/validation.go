package commands

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/executor"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/safety"
)

var (
	idRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	paramRe    = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	templateRe = regexp.MustCompile(`\{\{\s*([a-z_][a-z0-9_]*)\s*\}\}`)
	paramTypes = []string{"string", "resource", "enum", "int", "duration", "bool"}
	builtins   = []string{"profile", "region", "account"}
)

func (r *Registry) validate(c *Command) error {
	if !idRe.MatchString(c.ID) {
		return fmt.Errorf("invalid id %q", c.ID)
	}
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Category) == "" {
		return errors.New("name and category are required")
	}
	risk, err := safety.ParseRisk(c.RiskName)
	if err != nil {
		return err
	}
	c.Risk = risk
	if c.TimeoutStr != "" {
		d, err := time.ParseDuration(c.TimeoutStr)
		if err != nil || d <= 0 || d > 30*time.Minute {
			return fmt.Errorf("timeout must be a duration between 0 and 30m")
		}
		c.Timeout = d
	}

	seen := map[string]bool{}
	for i := range c.Parameters {
		p := &c.Parameters[i]
		if !paramRe.MatchString(p.Name) || slices.Contains(builtins, p.Name) {
			return fmt.Errorf("invalid parameter name %q", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate parameter %q", p.Name)
		}
		seen[p.Name] = true
		if p.Type == "" {
			p.Type = "string"
		}
		if !slices.Contains(paramTypes, p.Type) {
			return fmt.Errorf("parameter %q: unknown type %q", p.Name, p.Type)
		}
		if p.Type == "enum" && len(p.Options) == 0 {
			return fmt.Errorf("parameter %q: enum needs options", p.Name)
		}
		if p.Pattern != "" {
			re, err := regexp.Compile("^(?:" + p.Pattern + ")$")
			if err != nil {
				return fmt.Errorf("parameter %q: bad pattern: %w", p.Name, err)
			}
			p.re = re
		}
		if p.Default != "" {
			if err := p.check(p.Default); err != nil {
				return fmt.Errorf("parameter %q default: %w", p.Name, err)
			}
		}
	}

	e := c.Execution
	switch e.Type {
	case TypeSDK:
		op, ok := r.ops.Get(e.Service, e.Operation)
		if !ok {
			return fmt.Errorf("unknown SDK operation %s:%s", e.Service, e.Operation)
		}
		if c.Risk.Level() < op.Risk.Level() {
			return fmt.Errorf("risk %s is lower than operation risk %s", c.Risk, op.Risk)
		}
		for _, p := range c.Parameters {
			if !op.Accepts(p.Name) {
				return fmt.Errorf("operation %s does not accept parameter %q", op.Key(), p.Name)
			}
		}
		for _, req := range op.Required {
			if p := c.Param(req); p == nil || (!p.Required && p.Default == "") {
				return fmt.Errorf("operation %s requires parameter %q", op.Key(), req)
			}
		}
	case TypeProcess, TypeInteractive:
		if !executor.Allowed(e.Program) {
			return fmt.Errorf("program %q is not allowlisted", e.Program)
		}
		for _, a := range e.Args {
			if strings.ContainsAny(a, "|;&$`<>") && !templateRe.MatchString(a) {
				return fmt.Errorf("argument %q contains shell metacharacters", a)
			}
			for _, m := range templateRe.FindAllStringSubmatch(a, -1) {
				if c.Param(m[1]) == nil && !slices.Contains(builtins, m[1]) {
					return fmt.Errorf("argument references undeclared parameter %q", m[1])
				}
			}
			if a == "--profile" || a == "--region" {
				return errors.New("--profile/--region are added automatically from the active context")
			}
		}
	case TypeWorkflow:
		if len(e.Steps) == 0 {
			return errors.New("workflow has no steps")
		}
	default:
		return fmt.Errorf("unknown execution type %q", e.Type)
	}
	return nil
}

func (r *Registry) validateWorkflow(c *Command) error {
	if t := c.Execution.Then; t != nil {
		if c.Execution.Type != TypeInteractive || c.Execution.Ready == "" {
			return errors.New("then: needs an interactive command with ready: set")
		}
		sub := r.byID[t.Command]
		if sub == nil || sub.Execution.Type != TypeInteractive {
			return fmt.Errorf("then: %q must be a registered interactive command", t.Command)
		}
		if sub.Risk.Level() > c.Risk.Level() {
			return fmt.Errorf("then: %s has a higher risk than this command", sub.ID)
		}
		for k, v := range t.With {
			if sub.Param(k) == nil {
				return fmt.Errorf("then: %s has no parameter %q", sub.ID, k)
			}
			for _, m := range templateRe.FindAllStringSubmatch(v, -1) {
				if c.Param(m[1]) == nil && !slices.Contains(builtins, m[1]) {
					return fmt.Errorf("then: references undeclared parameter %q", m[1])
				}
			}
		}
	}
	if c.Execution.Type != TypeWorkflow {
		return nil
	}
	maxRisk := safety.ReadOnly
	for i, st := range c.Execution.Steps {
		sub := r.byID[st.Command]
		if sub == nil {
			return fmt.Errorf("step %d: unknown command %q", i+1, st.Command)
		}
		if sub.Execution.Type == TypeWorkflow || sub.Execution.Type == TypeInteractive {
			return fmt.Errorf("step %d: %s steps are not allowed in workflows", i+1, sub.Execution.Type)
		}
		if sub.Risk.Level() > maxRisk.Level() {
			maxRisk = sub.Risk
		}
		for k, v := range st.With {
			if sub.Param(k) == nil {
				return fmt.Errorf("step %d: %s has no parameter %q", i+1, sub.ID, k)
			}
			for _, m := range templateRe.FindAllStringSubmatch(v, -1) {
				if c.Param(m[1]) == nil && !slices.Contains(builtins, m[1]) {
					return fmt.Errorf("step %d references undeclared parameter %q", i+1, m[1])
				}
			}
		}
	}
	if c.Risk.Level() < maxRisk.Level() {
		return fmt.Errorf("workflow risk %s is lower than its steps (%s)", c.Risk, maxRisk)
	}
	return nil
}

// check validates one value against the parameter's type and constraints.
func (p *Param) check(v string) error {
	switch p.Type {
	case "int":
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Errorf("%q is not an integer", v)
		}
	case "duration":
		if _, err := time.ParseDuration(v); err != nil {
			return fmt.Errorf("%q is not a duration (e.g. 15m, 1h)", v)
		}
	case "bool":
		if v != "true" && v != "false" {
			return fmt.Errorf("%q must be true or false", v)
		}
	case "enum":
		if !slices.Contains(p.Options, v) {
			return fmt.Errorf("%q must be one of %s", v, strings.Join(p.Options, ", "))
		}
	}
	if p.re != nil && !p.re.MatchString(v) {
		return fmt.Errorf("%q does not match %s", v, p.Pattern)
	}
	return nil
}

// Bind applies defaults and validates user-supplied values.
func (c *Command) Bind(values map[string]string) (map[string]string, error) {
	out := map[string]string{}
	var errs []error
	for _, p := range c.Parameters {
		v := strings.TrimSpace(values[p.Name])
		if v == "" {
			v = p.Default
		}
		if v == "" {
			if p.Required {
				errs = append(errs, fmt.Errorf("%s is required", p.Name))
			}
			continue
		}
		if err := p.check(v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			continue
		}
		// Values for external processes may not start with "-" (option injection).
		if c.Execution.Type == TypeProcess || c.Execution.Type == TypeInteractive {
			if strings.HasPrefix(v, "-") {
				errs = append(errs, fmt.Errorf("%s: value may not start with '-'", p.Name))
				continue
			}
		}
		out[p.Name] = v
	}
	return out, errors.Join(errs...)
}

// expand substitutes {{param}} and builtins in s.
func expand(s string, vals map[string]string) string {
	return templateRe.ReplaceAllStringFunc(s, func(m string) string {
		return vals[templateRe.FindStringSubmatch(m)[1]]
	})
}
