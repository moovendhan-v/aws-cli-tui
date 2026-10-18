// Package app wires configuration, AWS profiles, the command registry, the
// executor and storage together. The TUI receives these as dependencies and
// never constructs AWS clients itself.
package app

import (
	"fmt"
	"path/filepath"
	"time"

	library "github.com/moovendhan-v/aws-cli-tui/devops/commands"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/meter"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/ops"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/profiles"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/cache"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/commands"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/config"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/executor"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/safety"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/storage"
)

type Deps struct {
	Config     *config.Config
	Home       string
	DBPath     string
	Profiles   []profiles.Profile
	Registry   *commands.Registry
	Runner     *commands.Runner
	Exec       *executor.Manager
	Store      *storage.Store // nil when history is unavailable
	Cache      *cache.Store
	Meter      *meter.Meter
	Production safety.Production
	SessionID  string
	Warnings   []string
}

// New loads everything the UI needs. Non-fatal problems become Warnings.
func New() (*Deps, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	home, _ := config.Home()
	d := &Deps{
		Config:     cfg,
		Home:       home,
		DBPath:     filepath.Join(home, "devops.db"),
		Production: safety.NewProduction(cfg.Safety.ProductionPatterns, cfg.Safety.ProductionAccounts),
		SessionID:  time.Now().Format("20060102-150405.000"),
	}

	d.Meter = meter.Default
	d.ApplyLimits()
	d.Cache = cache.New(cfg.Cache.Enabled, func(kind string) time.Duration { return cfg.Cache.TTLFor(kind) })

	if d.Profiles, err = profiles.Discover(); err != nil {
		d.Warnings = append(d.Warnings, "reading AWS profiles: "+err.Error())
	}

	if st, err := storage.Open(d.DBPath); err != nil {
		d.Warnings = append(d.Warnings, "history disabled: "+err.Error())
	} else {
		d.Store = st
		if err := st.Prune(cfg.History.RetentionDays); err != nil {
			d.Warnings = append(d.Warnings, "pruning history: "+err.Error())
		}
	}

	var rec executor.Recorder
	if d.Store != nil && cfg.History.Enabled {
		rec = &recorder{store: d.Store, session: d.SessionID, keepOutput: cfg.History.StoreCommandOutput}
	}
	d.Exec = executor.NewManager(cfg.Execution.MaxConcurrentJobs, rec)

	d.Registry = commands.NewRegistry(ops.Default())
	if err := d.Registry.LoadFS(library.FS, "builtin"); err != nil {
		return nil, fmt.Errorf("loading built-in commands: %w", err)
	}
	userDirs := append([]string{filepath.Join(home, "commands")}, cfg.Commands.Paths...)
	for _, dir := range userDirs {
		if err := d.Registry.LoadDir(dir); err != nil {
			d.Warnings = append(d.Warnings, "loading "+dir+": "+err.Error())
		}
	}
	d.Registry.Finalize()
	if d.Store != nil {
		if err := d.Registry.UseIndex(d.Store); err != nil {
			d.Warnings = append(d.Warnings, "search index: "+err.Error())
		}
	}
	d.Runner = &commands.Runner{Registry: d.Registry, Exec: d.Exec, DefaultTimeout: cfg.Execution.DefaultTimeout.Duration}
	return d, nil
}

// ApplyLimits pushes the configured API limits into the meter.
func (d *Deps) ApplyLimits() {
	l := d.Config.Limits
	d.Meter.SetLimits(meter.Limits{RequestsPerSecond: l.RequestsPerSecond, WarnCalls: l.WarnAPICalls, MaxCalls: l.MaxAPICalls})
}

func (d *Deps) Close() {
	d.Exec.CancelAll()
	if d.Store != nil {
		d.Store.Close()
	}
}

type recorder struct {
	store      *storage.Store
	session    string
	keepOutput bool
}

func (r *recorder) RecordJob(v executor.View, output string) {
	rec := storage.JobRecord{
		Session: r.session, JobID: v.ID, Name: v.Name, CommandID: v.CommandID, Args: v.Args,
		Profile: v.Ctx.Profile, Account: v.Ctx.AccountID, Region: v.Ctx.Region,
		Kind: string(v.Kind), Risk: string(v.Risk), Status: string(v.Status),
		Started: v.Started, Ended: v.Ended, ExitCode: v.ExitCode, Resources: v.Resources,
	}
	if v.Err != nil {
		rec.Error = safety.Describe(v.Err)
	}
	if r.keepOutput {
		rec.Output = safety.Redact(output)
	}
	_ = r.store.RecordJob(rec)
}
