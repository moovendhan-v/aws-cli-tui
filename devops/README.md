# devops

A keyboard-driven AWS DevOps terminal. Browse infrastructure, investigate
failures, open SSM/ECS Exec sessions, tail CloudWatch logs and run curated
diagnostic workflows, all from one TUI and without memorising AWS CLI commands.

```
 DEVOPS   Profile: dev-sso | Account: <ACCOUNT_ID> | Region: ap-southeast-2        ●
╭───────────────────────────────────────────────────────────────────────────────────╮
│ Search: > ecs errors                                                              │
╰───────────────────────────────────────────────────────────────────────────────────╯
╭────────────────────────╮ ╭────────────────────────────────────────────────────────╮
│  Overview              │ │ AWS INFRASTRUCTURE SUMMARY                             │
│  EC2 Instances         │ │ ╭────────────╮ ╭────────────╮ ╭────────────╮           │
│  ECS Clusters          │ │ │ EC2  18    │ │ ECS  4     │ │ RDS  3     │           │
│  Databases             │ │ ╰────────────╯ ╰────────────╯ ╰────────────╯           │
│  …                     │ │ RECENT ACTIVITY                                        │
╰────────────────────────╯ ╰────────────────────────────────────────────────────────╯
 ↑↓ Navigate   Enter Select   / Search   Ctrl+P Profile   Ctrl+R Region   ? Help
```

## Install

Requires Go 1.26+ to build (pure Go, no CGO). At runtime, SSM sessions, ECS Exec and tunnels also
need the AWS CLI v2 and the
[Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html).

```bash
make build        # → bin/devops
./bin/devops      # or: ./bin/devops --profile dev-sso --region ap-southeast-2
make test         # unit + headless render tests (no AWS access needed)
make check        # validate config and all command definitions
make release      # static darwin/linux binaries in dist/
```

Credentials come from your existing AWS config (`~/.aws/config`, SSO, env
vars). Nothing is stored by this tool except preferences and redacted history.

## Using it

| Key | Action |
|---|---|
| `↑` `↓` | Move in menus and tables |
| `Enter` | Open a resource / run the selected action |
| `/` or `Ctrl+K` | Search actions, screens and loaded resources |
| `Ctrl+P` / `Ctrl+R` | Switch profile / region (running jobs keep their original context) |
| `Ctrl+J` | Running Commands (activity) |
| `Tab` | Switch between navigation and workspace |
| `f` | Filter the current table |
| `Esc` | Back / close |
| `L` | `aws sso login` for the active profile |
| `?` | Context help |

**Resource-first:** EC2 Instances → `Enter` (details + SSM readiness) → `s` SSM session,
`h` health workflow, `c` CPU, `g` security groups. ECS Clusters → services → tasks
(stopped reasons, exit codes) → `x` ECS Exec. Load Balancers → target groups → target health.

**Problem-first:** press `/` and type the problem: `ecs errors`, `connect to database`,
`unhealthy targets`, `stoped taks` (typos are fine). Search only ever selects registered
commands. It never generates AWS or shell commands.

**Logs:** CloudWatch Logs → `Enter` to tail. `space` pause, `e` errors only, `f` custom
filter pattern, `t` time range, `s` show streams, `b` keep tailing in the background.

## What's implemented

| Area | Status |
|---|---|
| TUI shell, navigation, shortcuts, search palette | ✓ |
| Profiles (config + credentials + SSO), region switch, identity verification, prod badge | ✓ |
| EC2 discovery/search, instance details, SSM managed-node status | ✓ |
| SSM sessions, ECS Exec, SSM port-forward DB tunnel (interactive, via AWS CLI) | ✓ |
| CloudWatch log groups, live tail with pause/filter/range, error search | ✓ |
| ECS cluster → service → task drill-down with stopped reasons | ✓ |
| RDS, Lambda, Load Balancers → target health, Alarms, VPCs | ✓ (browse + actions) |
| Shared executor: bounded concurrency, timeouts, cancellation, live output | ✓ |
| Activity panel + SQLite history (redacted, retention-pruned) | ✓ |
| YAML command registry (41 commands, 3 workflows) with load-time validation | ✓ |
| Search: exact → SQLite FTS5 (porter stemming) → fuzzy, ranked by usage | ✓ |
| Favorites | ✓ |
| Semantic search, parameter presets, shell integration, 100+ commands | Not yet (roadmap phase 5) |

## Layout

```
cmd/devops/            entrypoint (--check, --profile, --region, --version)
commands/              built-in YAML command library (embedded)
internal/app/          dependency wiring, navigation
internal/aws/          auth/session, profiles, and one package per service
internal/aws/ops/      the table of SDK operations YAML may call
internal/commands/     registry, validation, search, runner
internal/executor/     job manager, process runner, output buffers
internal/safety/       risk levels, production detection, redaction, error classes
internal/storage/      SQLite history, favorites, FTS index
internal/tui/          shell (model, layout, palette), core, screens, components, theme
docs/commands.md       how to write your own commands
```

Architecture rules from the plan hold: screens never call AWS directly (all work
goes through `core.Env.Run` → executor), commands go through validation and the
safety layer, and every job captures its profile/account/region at submission.

## Configuration

`config.yaml` is created on first run in `$DEVOPS_HOME` (default: the OS config dir,
`~/Library/Application Support/devops` on macOS). Set `safety.production_patterns`
and `safety.production_accounts` to match your naming.

## Known limitations

- EC2 memory/disk metrics need the CloudWatch Agent and aren't shown yet.
- The tool only tracks operations it launched itself, not commands run in other terminals.
- Cancelling an AWS API call stops the local request, but AWS-side work that already started may still complete. The UI says this when you cancel.
- Overview target-health counts check at most 40 target groups.
