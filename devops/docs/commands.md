# Writing command definitions

Commands are YAML files. Built-ins live in `commands/` and are embedded in the
binary. Your own go in `<DEVOPS_HOME>/commands/` (on macOS:
`~/Library/Application Support/devops/commands/`) or any directory listed under
`commands.paths` in `config.yaml`. A user definition with the same `id` as a
built-in replaces it.

Run `devops --check` after editing. Invalid definitions are rejected with the
reason and never registered.

## Schema

```yaml
commands:
  - id: ecs.service.describe          # lowercase, dots/dashes allowed, unique
    name: Describe ECS Service
    category: ECS
    description: Inspect the configuration and deployment status of a service
    tags: [service, deployment, errors]  # improves search
    execution:
      type: aws_sdk                   # aws_sdk | process | interactive | workflow
      service: ecs
      operation: DescribeServices
    parameters:
      - name: cluster
        type: resource                # string | resource | enum | int | duration | bool
        resource: ecs_cluster         # hint only
        required: true
      - name: service
        type: resource
        required: true
        pattern: '[A-Za-z0-9_-]+'     # optional, anchored regex
    risk: read_only                   # read_only | mutating | destructive
    timeout: 30s                      # optional, max 30m
```

## Resource pickers

Give a parameter `type: resource` and one of these `resource:` kinds and the
form shows a searchable list of real resources (cached per profile/region):

`ec2_instance` · `ecs_cluster` · `ecs_service` (needs `cluster`) · `ecs_task`
(needs `cluster`, uses `service` if set) · `ecs_taskdef` · `log_group` ·
`rds_instance` · `lambda_function` · `load_balancer` · `target_group` · `vpc` ·
`cfn_stack` · `pipeline` · `build_project` · `s3_bucket`

`enum` and `bool` parameters get a list of their options. Other kinds fall back
to plain typing.

## Execution types

| Type | What it can do |
|---|---|
| `aws_sdk` | Call an operation from the built-in table (`internal/aws/ops`). Parameters must be ones the operation accepts. |
| `process` | Run an allowlisted program (`aws`, `session-manager-plugin`) with templated arguments. Output is captured. |
| `interactive` | Same as `process`, but the program gets the terminal (SSM sessions, ECS Exec, tunnels). |
| `workflow` | Run other `aws_sdk`/`process` commands in sequence. Steps can set `continue_on_error`. |

## Safety rules enforced at load time

- `risk` can never be lower than the SDK operation's risk (e.g. `RebootInstances` is `mutating`).
- A workflow's risk must be at least the highest risk of its steps.
- Programs must be allowlisted. Arguments go straight to the process, never to a shell, and arguments containing `| ; & $ \` < >` are rejected.
- `--profile` and `--region` can't be hard-coded. They're always appended from the active context.
- Templates (`{{name}}`) may only reference declared parameters or `profile`, `region`, `account`.
- At run time, values for `process`/`interactive` commands may not start with `-` (option injection).

## Confirmation

- Interactive commands always ask before opening a session.
- `mutating`/`destructive` commands ask when `execution.confirm_mutations` is on.
- In production (matched by `safety.production_patterns` or `production_accounts`),
  mutating commands require typing the profile name, even if `confirm_mutations` is off.
