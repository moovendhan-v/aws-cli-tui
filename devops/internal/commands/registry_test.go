package commands

import (
	"strings"
	"testing"

	library "github.com/moovendhan-v/aws-cli-tui/devops/commands"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/ops"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/executor"
)

func builtin(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry(ops.Default())
	if err := r.LoadFS(library.FS, "builtin"); err != nil {
		t.Fatal(err)
	}
	r.Finalize()
	return r
}

func TestBuiltinLibraryIsValid(t *testing.T) {
	r := builtin(t)
	for _, rej := range r.Rejected {
		t.Errorf("rejected: %v", rej)
	}
	if len(r.All()) < 40 {
		t.Errorf("expected at least 40 commands, got %d", len(r.All()))
	}
}

func TestValidationRejectsUnsafeDefinitions(t *testing.T) {
	cases := map[string]string{
		"unknown op": `
id: x.a
name: A
category: X
execution: { type: aws_sdk, service: ec2, operation: TerminateInstances }
risk: destructive`,
		"risk lower than op": `
id: x.b
name: B
category: X
execution: { type: aws_sdk, service: ec2, operation: RebootInstances }
parameters: [{ name: instance_id, required: true }]
risk: read_only`,
		"program not allowlisted": `
id: x.c
name: C
category: X
execution: { type: process, program: bash, args: [-c, "rm -rf /"] }
risk: destructive`,
		"shell metacharacters": `
id: x.d
name: D
category: X
execution: { type: process, program: aws, args: ["s3 ls | sh"] }
risk: read_only`,
		"undeclared template": `
id: x.e
name: E
category: X
execution: { type: process, program: aws, args: [ec2, describe-instances, --instance-ids, "{{id}}"] }
risk: read_only`,
		"manual profile flag": `
id: x.f
name: F
category: X
execution: { type: process, program: aws, args: [s3, ls, --profile, prod] }
risk: read_only`,
		"missing required op param": `
id: x.g
name: G
category: X
execution: { type: aws_sdk, service: elbv2, operation: DescribeTargetHealth }
risk: read_only`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewRegistry(ops.Default())
			r.loadBytes([]byte(src), name)
			r.Finalize()
			if len(r.All()) != 0 || len(r.Rejected) != 1 {
				t.Fatalf("expected rejection, got %d commands, %d rejected", len(r.All()), len(r.Rejected))
			}
		})
	}
}

func TestBindRejectsOptionInjection(t *testing.T) {
	r := builtin(t)
	c := r.Get("ssm.session.start")
	if _, err := c.Bind(map[string]string{"instance_id": "--document-name=evil"}); err == nil {
		t.Fatal("expected error for value starting with -")
	}
	if _, err := c.Bind(map[string]string{"instance_id": "i-0123456789abcdef0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Bind(map[string]string{"instance_id": "not-an-instance"}); err == nil {
		t.Fatal("expected pattern error")
	}
}

func TestPrepareBindsContext(t *testing.T) {
	r := builtin(t)
	run := &Runner{Registry: r, Exec: executor.NewManager(2, nil)}
	p, err := run.Prepare(r.Get("ssm.session.start"), map[string]string{"instance_id": "i-0123456789abcdef0"},
		auth.Context{Profile: "dev", Region: "ap-southeast-2"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.Argv, " ")
	want := "aws ssm start-session --target i-0123456789abcdef0 --profile dev --region ap-southeast-2"
	if got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if p.Spec.Kind != executor.KindInteractive || p.Spec.Ctx.Profile != "dev" {
		t.Fatalf("unexpected spec %+v", p.Spec)
	}
}

func TestWorkflowPreparesAllSteps(t *testing.T) {
	r := builtin(t)
	run := &Runner{Registry: r, Exec: executor.NewManager(2, nil)}
	p, err := run.Prepare(r.Get("workflow.ec2.health"), map[string]string{"instance_id": "i-0123456789abcdef0"}, auth.Context{Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 4 {
		t.Fatalf("steps = %d", len(p.Steps))
	}
	if p.Steps[3].Values["dimension_value"] != "i-0123456789abcdef0" {
		t.Fatalf("template not expanded: %v", p.Steps[3].Values)
	}
}

func TestSearchFindsDiagnosticActions(t *testing.T) {
	r := builtin(t)
	ids := func(q string) []string {
		var out []string
		for _, m := range r.Search(q, 8) {
			out = append(out, m.Command.ID)
		}
		return out
	}
	contains := func(list []string, id string) bool {
		for _, v := range list {
			if v == id {
				return true
			}
		}
		return false
	}
	ecs := ids("ecs errors")
	for _, want := range []string{"ecs.tasks.stopped", "workflow.ecs.triage"} {
		if !contains(ecs, want) {
			t.Errorf("ecs errors: missing %s in %v", want, ecs)
		}
	}
	if db := ids("connect to production database"); !contains(db, "ssm.tunnel.database") {
		t.Errorf("database: missing tunnel in %v", db)
	}
	if typo := ids("stoped taks"); !contains(typo, "ecs.tasks.stopped") {
		t.Errorf("fuzzy: missing stopped tasks in %v", typo)
	}
	if got := ids("Show Caller Identity"); len(got) == 0 || got[0] != "identity.caller" {
		t.Errorf("exact match not first: %v", got)
	}
}

func TestDatabaseConnectChain(t *testing.T) {
	r := builtin(t)
	run := &Runner{Registry: r, Exec: executor.NewManager(2, nil)}
	p, err := run.Prepare(r.Get("db.connect.postgres"), map[string]string{"instance_id": "i-0123456789abcdef0",
		"db_host": "orders.abc.ap-southeast-2.rds.amazonaws.com", "db_user": "app", "db_name": "orders"},
		auth.Context{Profile: "dev", Region: "ap-southeast-2"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Then == nil {
		t.Fatal("expected a follow-up client")
	}
	if got := strings.Join(p.Then.Argv, " "); got != "psql -h 127.0.0.1 -p 15432 -U app -d orders" {
		t.Fatalf("client argv = %q", got)
	}
	if !strings.Contains(strings.Join(p.Argv, " "), "host=orders.abc.ap-southeast-2.rds.amazonaws.com,portNumber=5432,localPortNumber=15432") {
		t.Fatalf("tunnel argv = %v", p.Argv)
	}
	if _, err := run.Prepare(r.Get("db.client.mysql"), map[string]string{"host": "db", "user": "-e DROP"}, auth.Context{}); err == nil {
		t.Fatal("option injection via user should be rejected")
	}
}
