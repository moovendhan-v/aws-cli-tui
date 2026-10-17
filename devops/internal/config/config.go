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
	// Density sets default spacing: compact, comfortable or spacious.
	Density string `yaml:"density"`
	// Padding overrides the density's horizontal panel padding (-1 = density default).
	Padding  int  `yaml:"padding"`
	NavWidth int  `yaml:"nav_width"`
	Mouse    bool `yaml:"mouse"`
	Wrap     bool `yaml:"wrap"`
	// OutputFormat is the default result view: table, cards, yaml or json.
	OutputFormat string `yaml:"output_format"`
	// EmbeddedSessions runs SSM/ECS Exec/tunnels inside the layout.
	EmbeddedSessions bool `yaml:"embedded_sessions"`
}

// PanelPadding returns the horizontal padding for panels.
func (u UI) PanelPadding() int {
	if u.Padding >= 0 {
		return min(u.Padding, 6)
	}
	switch u.Density {
	case "compact":
		return 1
	case "spacious":
		return 3
	}
	return 2
}

// PanelVPad returns blank lines above and below panel content.
func (u UI) PanelVPad() (top, bottom int) {
	switch u.Density {
	case "compact":
		return 0, 0
	case "spacious":
		return 1, 1
	}
	return 1, 0
}

// RowGap returns blank lines between grouped blocks (cards, sections).
func (u UI) RowGap() int {
	switch u.Density {
	case "compact":
		return 0
	case "spacious":
		return 2
	}
	return 1
}

type Limits struct {
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	WarnAPICalls      int     `yaml:"warn_api_calls"`
	MaxAPICalls       int     `yaml:"max_api_calls"`
	ConfirmBilled     bool    `yaml:"confirm_billed"`
	MaxPreviewMB      int     `yaml:"max_preview_mb"`
}

type Cache struct {
	Enabled bool                `yaml:"enabled"`
	TTL     Duration            `yaml:"ttl"`
	TTLs    map[string]Duration `yaml:"ttls"`
}

// TTLFor returns the cache lifetime for a resource kind.
func (c Cache) TTLFor(kind string) time.Duration {
	if d, ok := c.TTLs[kind]; ok {
		return d.Duration
	}
	return c.TTL.Duration
}

type Logs struct {
	// TailMode is poll (FilterLogEvents) or live (StartLiveTail, billed per minute).
	TailMode      string   `yaml:"tail_mode"`
	PollInterval  Duration `yaml:"poll_interval"`
	HistoryWindow Duration `yaml:"history_window"`
	PageEvents    int      `yaml:"page_events"`
}

type Overview struct {
	TargetHealth bool `yaml:"target_health"`
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
	Limits    Limits    `yaml:"limits"`
	Cache     Cache     `yaml:"cache"`
	Logs      Logs      `yaml:"logs"`
	Overview  Overview  `yaml:"overview"`

	path string
}

func Default() *Config {
	return &Config{
		UI: UI{Theme: "dark", ShowCommandActivity: true, Density: "comfortable", Padding: -1, NavWidth: 28,
			Mouse: true, Wrap: true, OutputFormat: "table", EmbeddedSessions: true},
		Limits: Limits{RequestsPerSecond: 10, WarnAPICalls: 2000, ConfirmBilled: true, MaxPreviewMB: 20},
		Cache: Cache{Enabled: true, TTL: Duration{2 * time.Minute}, TTLs: map[string]Duration{
			"ec2": {time.Minute}, "ecs": {time.Minute}, "taskdef": {10 * time.Minute}, "logs": {5 * time.Minute},
			"s3": {5 * time.Minute}, "summary": {2 * time.Minute}, "pipelines": {15 * time.Second},
		}},
		Logs:     Logs{TailMode: "poll", PollInterval: Duration{3 * time.Second}, HistoryWindow: Duration{15 * time.Minute}, PageEvents: 500},
		Overview: Overview{TargetHealth: true},
		Search:   Search{Fuzzy: true},
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
  density: comfortable      # compact | comfortable | spacious
  padding: -1               # panel padding override; -1 = density default
  nav_width: 28
  mouse: true
  wrap: true                # wrap long lines in output and logs (w toggles)
  output_format: table      # table | cards | yaml | json (v cycles)
  embedded_sessions: true   # run SSM / ECS Exec / tunnels inside the layout
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

# Cost and API-load guards. Billed operations show a $ badge.
limits:
  requests_per_second: 10   # per AWS service; 0 = unlimited
  warn_api_calls: 2000      # toast once this many calls were made this session
  max_api_calls: 0          # hard cap per session; 0 = none
  confirm_billed: true      # confirm before operations billed per call/data
  max_preview_mb: 20        # S3 previews larger than this ask first

cache:
  enabled: true
  ttl: 2m                   # default; r always refreshes
  ttls: { ec2: 1m, ecs: 1m, taskdef: 10m, logs: 5m, s3: 5m, summary: 2m, pipelines: 15s }

logs:
  tail_mode: poll           # poll (FilterLogEvents) or live (StartLiveTail, billed per minute)
  poll_interval: 3s
  history_window: 15m       # how far back the first history load goes
  page_events: 500          # events per history page

overview:
  target_health: true       # checks up to 40 target groups (cached)
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

// Save writes the current configuration back to config.yaml.
func (c *Config) Save() error {
	if err := c.validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := []byte("# devops CLI preferences (saved from Settings). AWS credentials are read from ~/.aws.\n")
	return os.WriteFile(c.path, append(header, data...), 0o600)
}

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
	switch c.UI.Density {
	case "compact", "comfortable", "spacious":
	case "":
		c.UI.Density = "comfortable"
	default:
		return fmt.Errorf("ui.density must be compact, comfortable or spacious")
	}
	switch c.UI.OutputFormat {
	case "table", "cards", "yaml", "json":
	case "":
		c.UI.OutputFormat = "table"
	default:
		return fmt.Errorf("ui.output_format must be table, cards, yaml or json")
	}
	if c.UI.NavWidth < 20 || c.UI.NavWidth > 40 {
		c.UI.NavWidth = 28
	}
	if c.Logs.TailMode != "poll" && c.Logs.TailMode != "live" {
		return fmt.Errorf("logs.tail_mode must be poll or live")
	}
	if c.Logs.PollInterval.Duration < time.Second {
		c.Logs.PollInterval.Duration = time.Second
	}
	if c.Logs.PageEvents < 50 || c.Logs.PageEvents > 10000 {
		c.Logs.PageEvents = 500
	}
	if c.Logs.HistoryWindow.Duration <= 0 {
		c.Logs.HistoryWindow.Duration = 15 * time.Minute
	}
	if c.Limits.RequestsPerSecond < 0 || c.Limits.MaxAPICalls < 0 || c.Limits.WarnAPICalls < 0 {
		return fmt.Errorf("limits values must be >= 0")
	}
	if c.Limits.MaxPreviewMB <= 0 {
		c.Limits.MaxPreviewMB = 20
	}
	return nil
}
