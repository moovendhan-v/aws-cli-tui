# devops

A keyboard- and mouse-driven AWS DevOps terminal. Browse infrastructure,
investigate failures, open SSM/ECS Exec sessions inside the layout, read and
tail CloudWatch logs, follow pipelines and builds, preview S3 objects, and run
curated diagnostic workflows, all without memorising AWS CLI commands.

```
 DEVOPS   Profile: dev-sso ▾ | Account: <ACCOUNT_ID> | Region: ap-southeast-2 ▾      API 42  ●
╭───────────────────────────────────────────────────────────────────────────────────────────╮
│  Search: > ecs errors                                                                     │
╰───────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────╮ ╭──────────────────────────────────────────────────────────────╮
│   ◎  Overview            │ │  AWS INFRASTRUCTURE SUMMARY                                  │
│   ▣  EC2 Instances       │ │  ╭────────────╮ ╭────────────╮ ╭────────────╮                │
│   ◈  ECS Clusters        │ │  │ EC2  18    │ │ ECS  4     │ │ RDS  3     │                │
│   …                      │ │  ╰────────────╯ ╰────────────╯ ╰────────────╯                │
│  ──────────────────────  │ │  HEALTH  ● 1 alarm firing   ● 0 unhealthy targets            │
│  PROFILE                 │ │  PINNED  1 web-01 ec2   2 /ecs/shop-web logs                 │
│   dev-sso            ▾   │ │                                                              │
│  REGION                  │ │  RECENT ACTIVITY                                             │
│   ap-southeast-2     ▾   │ │  ✓ Describe ECS service (web-api) — 12s ago                  │
╰──────────────────────────╯ ╰──────────────────────────────────────────────────────────────╯
 ↑↓ Navigate   Enter Select   / Search   Ctrl+P Profile   Ctrl+R Region   ? Help
```

## Install

You need Go 1.26+ to build it (pure Go, no CGO). At runtime, SSM sessions, ECS Exec
and tunnels also need the AWS CLI v2 and the
[Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html).
Video frame previews use `ffmpeg` when it's installed.

```bash
make build        # → bin/devops
./bin/devops      # or: ./bin/devops --profile dev-sso --region ap-southeast-2
make test         # unit, PTY and headless render tests (no AWS access needed)
make check        # validate config and all command definitions
make release      # static darwin/linux binaries in dist/
```

Credentials come from your existing AWS config (`~/.aws/config`, SSO, env
vars). The tool stores only preferences, a cache in memory, and redacted history.

## Screens

| Screen | What you can do |
|---|---|
| Overview | Counts, alerts, health strip, pinned resources (`1`–`9`), recent activity; `F` follows the firing alarm |
| What Changed? | CloudTrail write events + alarm state changes for 1h/6h/24h/7d with a timeline (▲ = alarm fired), failed/denied-only filter (`e`), routine agent noise hidden (`n`), `g` jumps to the resource |
| EC2 | Search, details, SSM readiness, SSM shell, health workflow, CPU trends (`C`, billed) |
| ECS | Cluster → service view with tabs: Overview · Deployments (animated progress) · Tasks · Events · Task definition (revision diff) · Metrics. Scale, redeploy, roll back, stop task, exec, port-forward, jump to logs |
| Databases | Enter/`x` opens a SQL shell (psql/mysql) in Terminals, through an SSM bastion tunnel or directly; connectivity workflow, connection metrics |
| Lambda | Function view: configuration (env names only), invocations/errors/duration charts, recent error lines; invoke with a test event (`i`), logs (`l`) |
| Load Balancers | Target groups → target health, a topology map (`m`), `F` on a target group follows the error |
| CloudWatch Logs | Opens on recent history (newest events across streams). Scroll up to load older events; `t` live tail (poll or Live Tail), errors-only, filter patterns, JSON pretty-print, events/min histogram |
| Metrics & Alarms | Alarm view with state history and a braille chart with the threshold line; `F` follows the error |
| Networking | VPCs, subnets, route tables |
| S3 Buckets | Browse buckets/folders, security check (`i`), previews: images (block-art in any terminal, full resolution in Kitty/iTerm2/WezTerm/Ghostty), text/JSON/CSV, video still frames (ffmpeg) |
| Pipelines | Stage dots per pipeline; pipeline view with an animated stage diagram, actions, run history; start/stop/retry/approve/reject |
| CodeBuild | Projects → builds → build view with a phase timeline and live build log |
| Command Library | 56 validated commands and 3 workflows; favourites; `$` marks billed operations |
| Running Commands | Every operation the tool started, with output, cancel and history |
| Terminals | Embedded SSM/ECS Exec/tunnel sessions with tabs (`Ctrl+]` menu) |
| Settings | Editable preferences (density, padding, mouse, wrap, cost limits, cache, logs) and a status/API-usage tab |

## Keys and mouse

| Key | Action |
|---|---|
| `↑` `↓` / wheel | Move or scroll |
| `Enter` / double-click | Open a resource or run the selected action |
| click | Select sidebar items, rows, tabs, the profile/region selects |
| `/` or `Ctrl+K` | Search actions, screens and loaded resources |
| `Ctrl+P` / `Ctrl+R` | Profile / region dropdown (bottom-left); running jobs keep their context |
| `Ctrl+J` | Running Commands |
| `f` · `r` · `W` | Filter · refresh (bypasses the cache) · watch (auto-refresh every 10s) |
| `o` · `y` · `Y` | Open in the AWS console · copy ID · copy console link |
| `*` | Pin to the overview |
| `v` · `w` | Output: table → cards → YAML → JSON → raw · wrap |
| `Ctrl+]` | In an embedded session: d detach · n next · x close |
| `Esc` · `?` · `q` | Back · help · quit |

While mouse support is on, hold Option (macOS) or Shift to select text with the mouse. You can turn mouse support off in Settings.

## Follow the error

`F` on a firing alarm (or on the overview, or a target group) walks the chain
for you, step by step:

alarm → target group (unhealthy targets and reasons) → the ECS service using it
(running vs desired, rollout, failing events) → stopped-task reasons → error
lines in its log group over the last 30 minutes. EC2 targets show status
checks; Lambda and RDS alarms go to the function's errors or the database.
Enter on any step opens that screen.

## Forms that find values for you

Parameters that refer to AWS resources (instances, clusters, services, tasks,
log groups, databases, pipelines, buckets…) show a searchable list with names
and states. Typing `web 02` finds `web-02` by name before matching IDs or
details, and recently used values come first. `Ctrl+T` switches a field to manual
typing. Confirmations show names as well as IDs, for example `web-01 (i-0abc…)`.

## Cost and API safety

- Every AWS request goes through a meter and a per-service rate limit
  (default 10 req/s). The header shows the session count (`API 42 · $3`), and
  Settings › Status breaks it down by operation.
- Operations that AWS bills per call or per data unit (CloudWatch metrics,
  Live Tail, Logs Insights, S3 GetObject) are marked `$` and ask before running.
  This tool doesn't list prices; see the AWS pricing pages.
- `limits.warn_api_calls` shows a warning and `limits.max_api_calls` sets a
  hard cap per session.
- List results are cached per profile + region with per-type lifetimes. Stale
  data shows instantly while a refresh runs. `r` always refreshes, and
  changes you make clear the cache.
- Lists load one page at a time as you scroll ("loading more…").
- Polling (pipelines, builds, watch mode, tails) only runs while that view is
  on screen.

## Safety

- Only registered, validated commands run. Programs are allowlisted and get
  arguments directly, never through a shell.
- Mutating commands ask first. In production (profile name or account
  match), you must type the profile name.
- Secrets are redacted in output, logs, history and previews. ECS environment
  variable values and CodeBuild environment variables are never shown.
- Console links are only opened if they point at `*.console.aws.amazon.com`.
  S3 links are 5-minute presigned URLs.

## Configuration

`config.yaml` is created on first run in `$DEVOPS_HOME` (default on macOS:
`~/Library/Application Support/devops`). Most options can be edited live in
Settings. Write your own commands as described in `docs/commands.md`.

## Terminal support

| Feature | Works in |
|---|---|
| Layout, mouse, colours | Any modern terminal (Warp, VS Code, iTerm2, Terminal.app, Kitty, WezTerm, Ghostty) |
| Embedded SSM / ECS Exec sessions | Any terminal (sessions run on an internal PTY) |
| Image block preview | Any truecolor terminal |
| Full-resolution images (`F`) | Kitty, Ghostty, iTerm2, WezTerm. Elsewhere it says it's not available. Set `DEVOPS_IMAGE_PROTOCOL=iterm2` or `kitty` to force it |

## Known limitations

- Full-screen programs (`vim`, `top`) work in embedded sessions but may not
  render perfectly. Choose `f` in the session prompt to run full screen instead.
- Memory/disk metrics for EC2 need the CloudWatch Agent and aren't shown yet.
- The tool only tracks operations it started itself.
- Cancelling an AWS call stops the local request, but AWS-side work that
  already started may still complete.
- Console deep links target the standard `aws` partition (not China/GovCloud).
