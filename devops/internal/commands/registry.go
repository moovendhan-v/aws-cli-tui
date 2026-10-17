// Package commands loads, validates, searches and runs YAML command
// definitions. Only validated definitions are registered.
package commands

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"devops/internal/aws/ops"
	"devops/internal/safety"
)

const (
	TypeSDK         = "aws_sdk"
	TypeProcess     = "process"
	TypeInteractive = "interactive"
	TypeWorkflow    = "workflow"
)

type Param struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"` // string | resource | enum | int | duration | bool
	Resource    string   `yaml:"resource"`
	Required    bool     `yaml:"required"`
	Default     string   `yaml:"default"`
	Options     []string `yaml:"options"`
	Pattern     string   `yaml:"pattern"`
	Description string   `yaml:"description"`

	re *regexp.Regexp
}

type Step struct {
	Command         string            `yaml:"command"`
	With            map[string]string `yaml:"with"`
	ContinueOnError bool              `yaml:"continue_on_error"`
}

type Execution struct {
	Type      string   `yaml:"type"`
	Service   string   `yaml:"service"`
	Operation string   `yaml:"operation"`
	Program   string   `yaml:"program"`
	Args      []string `yaml:"args"`
	Steps     []Step   `yaml:"steps"`
}

type Command struct {
	ID          string    `yaml:"id"`
	Name        string    `yaml:"name"`
	Category    string    `yaml:"category"`
	Description string    `yaml:"description"`
	Tags        []string  `yaml:"tags"`
	Execution   Execution `yaml:"execution"`
	Parameters  []Param   `yaml:"parameters"`
	RiskName    string    `yaml:"risk"`
	TimeoutStr  string    `yaml:"timeout"`

	Risk    safety.Risk   `yaml:"-"`
	Timeout time.Duration `yaml:"-"`
	Source  string        `yaml:"-"`
}

func (c *Command) Param(name string) *Param {
	for i := range c.Parameters {
		if c.Parameters[i].Name == name {
			return &c.Parameters[i]
		}
	}
	return nil
}

// Rejected is a definition that failed validation.
type Rejected struct {
	Source string
	ID     string
	Err    error
}

func (r Rejected) Error() string {
	if r.ID != "" {
		return fmt.Sprintf("%s (%s): %v", r.Source, r.ID, r.Err)
	}
	return fmt.Sprintf("%s: %v", r.Source, r.Err)
}

type Registry struct {
	ops      *ops.Table
	byID     map[string]*Command
	ordered  []*Command
	Rejected []Rejected
	fts      func(q string, limit int) ([]string, error)
	usage    map[string]int
}

func NewRegistry(t *ops.Table) *Registry {
	return &Registry{ops: t, byID: map[string]*Command{}, usage: map[string]int{}}
}

type file struct {
	Commands []Command `yaml:"commands"`
}

// LoadFS loads every *.yaml/*.yml file in fsys.
func (r *Registry) LoadFS(fsys fs.FS, label string) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isYAML(path) {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		r.loadBytes(data, label+":"+path)
		return nil
	})
}

// LoadDir loads user definitions; a missing directory is not an error.
func (r *Registry) LoadDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isYAML(path) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		r.loadBytes(data, path)
		return nil
	})
}

func (r *Registry) loadBytes(data []byte, source string) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		r.Rejected = append(r.Rejected, Rejected{Source: source, Err: err})
		return
	}
	if len(f.Commands) == 0 {
		var single Command
		if err := yaml.Unmarshal(data, &single); err == nil && single.ID != "" {
			f.Commands = []Command{single}
		}
	}
	for i := range f.Commands {
		c := f.Commands[i]
		c.Source = source
		if err := r.validate(&c); err != nil {
			r.Rejected = append(r.Rejected, Rejected{Source: source, ID: c.ID, Err: err})
			continue
		}
		if prev, dup := r.byID[c.ID]; dup {
			// Later sources (user dirs) override built-ins deliberately.
			*prev = c
			continue
		}
		cp := c
		r.byID[c.ID] = &cp
		r.ordered = append(r.ordered, &cp)
	}
}

// Finalize checks cross-references (workflow steps) after all sources load.
func (r *Registry) Finalize() {
	var keep []*Command
	for _, c := range r.ordered {
		if err := r.validateWorkflow(c); err != nil {
			r.Rejected = append(r.Rejected, Rejected{Source: c.Source, ID: c.ID, Err: err})
			delete(r.byID, c.ID)
			continue
		}
		keep = append(keep, c)
	}
	r.ordered = keep
	sort.SliceStable(r.ordered, func(i, j int) bool {
		if r.ordered[i].Category != r.ordered[j].Category {
			return r.ordered[i].Category < r.ordered[j].Category
		}
		return r.ordered[i].Name < r.ordered[j].Name
	})
}

func (r *Registry) Get(id string) *Command { return r.byID[id] }

// BilledNote explains why a command costs money to run, or "" if it calls
// only operations that are not billed per request.
func (r *Registry) BilledNote(c *Command) string {
	switch c.Execution.Type {
	case TypeSDK:
		if op, ok := r.ops.Get(c.Execution.Service, c.Execution.Operation); ok {
			return op.Billed
		}
	case TypeWorkflow:
		for _, st := range c.Execution.Steps {
			if sub := r.byID[st.Command]; sub != nil {
				if n := r.BilledNote(sub); n != "" {
					return n
				}
			}
		}
	}
	return ""
}

func (r *Registry) All() []*Command { return r.ordered }

func (r *Registry) Ops() *ops.Table { return r.ops }

// Categories returns category → count.
func (r *Registry) Categories() map[string]int {
	out := map[string]int{}
	for _, c := range r.ordered {
		out[c.Category]++
	}
	return out
}

func isYAML(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".yaml" || ext == ".yml"
}
