// Package safety holds risk levels, production detection, secret redaction
// and error classification shared by the executor and the UI.
package safety

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Risk string

const (
	ReadOnly    Risk = "read_only"
	Mutating    Risk = "mutating"
	Destructive Risk = "destructive"
)

func ParseRisk(s string) (Risk, error) {
	switch r := Risk(s); r {
	case ReadOnly, Mutating, Destructive:
		return r, nil
	}
	return "", fmt.Errorf("unknown risk %q (want read_only, mutating or destructive)", s)
}

// Level orders risks so a definition can never declare less than an operation needs.
func (r Risk) Level() int {
	switch r {
	case Mutating:
		return 1
	case Destructive:
		return 2
	}
	return 0
}

func (r Risk) Label() string {
	switch r {
	case Mutating:
		return "mutating"
	case Destructive:
		return "destructive"
	}
	return "read-only"
}

// Production identifies production targets from profile names and account IDs.
type Production struct {
	patterns []string
	accounts []string
}

func NewProduction(patterns, accounts []string) Production {
	p := Production{accounts: accounts}
	for _, s := range patterns {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			p.patterns = append(p.patterns, s)
		}
	}
	return p
}

func (p Production) Is(profile, account string) bool {
	if account != "" && slices.Contains(p.accounts, account) {
		return true
	}
	name := strings.ToLower(profile)
	for _, pat := range p.patterns {
		if strings.Contains(name, pat) {
			return true
		}
	}
	return false
}

var redactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)("?(?:aws_)?(?:secret_access_key|secretaccesskey|session_token|sessiontoken|password|passwd|secret|api[_-]?key)"?\s*[:=]\s*"?)([^\s",}]+)`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-._~+/]+=*`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)(://[^:/\s]+:)[^@\s]+@`), "${1}[REDACTED]@"},
}

// Redact removes credentials and obvious secrets from text before it is shown or stored.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// RedactMap returns a copy of m with sensitive-looking keys and values redacted.
func RedactMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "password") || strings.Contains(lk, "secret") || strings.Contains(lk, "token") {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = Redact(v)
	}
	return out
}
