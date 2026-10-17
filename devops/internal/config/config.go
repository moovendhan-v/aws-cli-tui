// Package config loads application preferences. AWS credentials and profiles
// are never stored here; they come from the standard AWS shared config.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that (un)marshals as a Go duration string ("30s").
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

type UI struct {
	Theme               string `yaml:"theme"`
	ShowCommandActivity bool   `yaml:"show_command_activity"`
	DefaultRegion       string `yaml:"default_region"`
	DefaultProfile      string `yaml:"default_profile"`
}

type Search struct {
	Fuzzy          bool `yaml:"fuzzy"`
	SemanticSearch bool `yaml:"semantic_search"`
}

type Execution struct {
	MaxConcurrentJobs int      `yaml:"max_concurrent_jobs"`
	DefaultTimeout    Duration `yaml:"default_timeout"`
	ConfirmMutations  bool     `yaml:"confirm_mutations"`
}

type History struct {
	Enabled            bool `yaml:"enabled"`
	StoreCommandOutput bool `yaml:"store_command_output"`
	RetentionDays      int  `yaml:"retention_days"`
}

type Safety struct {
	// ProductionPatterns are case-insensitive substrings matched against the
	// profile name. ProductionAccounts are exact account IDs.
	ProductionPatterns []string `yaml:"production_patterns"`
	ProductionAccounts []string `yaml:"production_accounts"`
}

type Commands struct {
	// Extra directories containing YAML command definitions.
	Paths []string `yaml:"paths"`
}

type Config struct {
	UI        UI        `yaml:"ui"`
	Search    Search    `yaml:"search"`
	Execution Execution `yaml:"execution"`
	History   History   `yaml:"history"`
	Safety    Safety    `yaml:"safety"`
	Commands  Commands  `yaml:"commands"`

	path string
}

func Default() *Config {
	return &Config{
		UI:     UI{Theme: "dark", ShowCommandActivity: true},
		Search: Search{Fuzzy: true},
		Execution: Execution{
			MaxConcurrentJobs: 8,
			DefaultTimeout:    Duration{30 * time.Second},
			ConfirmMutations:  true,
		},
		History: History{Enabled: true, RetentionDays: 7},
		Safety:  Safety{ProductionPatterns: []string{"prod", "prd", "production", "live"}},
	}
}

const template = `# devops CLI preferences. AWS profiles and credentials are read from ~/.aws.
ui:
  theme: dark
  show_command_activity: true
  default_region: ""        # empty = use the profile's region
  default_profile: ""       # empty = AWS_PROFILE or "default"

search:
  fuzzy: true
  semantic_search: false

execution:
  max_concurrent_jobs: 8
  default_timeout: 30s
  confirm_mutations: true

history:
  enabled: true
  store_command_output: false
  retention_days: 7

safety:
  # Profiles whose name contains one of these are treated as production.
  production_patterns: [prod, prd, production, live]
  # Account IDs that are always treated as production.
  production_accounts: []

commands:
  # Extra directories with YAML command definitions (merged with built-ins).
  paths: []
`

// Home returns the application directory ($DEVOPS_HOME or the OS config dir).
func Home() (string, error) {
	if v := os.Getenv("DEVOPS_HOME"); v != "" {
		return v, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "devops"), nil
}

// Load reads config.yaml from Home(), creating it with defaults on first run.
func Load() (*Config, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(home, "config.yaml")
	cfg := Default()
	cfg.path = path

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if werr := os.WriteFile(path, []byte(template), 0o600); werr != nil {
			return nil, werr
		}
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, cfg.validate()
}

func (c *Config) Path() string { return c.path }

func (c *Config) validate() error {
	e := &c.Execution
	if e.MaxConcurrentJobs < 1 || e.MaxConcurrentJobs > 64 {
		return fmt.Errorf("execution.max_concurrent_jobs must be between 1 and 64")
	}
	if e.DefaultTimeout.Duration <= 0 {
		e.DefaultTimeout.Duration = 30 * time.Second
	}
	if c.History.RetentionDays < 0 {
		return fmt.Errorf("history.retention_days must be >= 0")
	}
	return nil
}
