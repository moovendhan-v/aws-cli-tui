package safety

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []string{
		"aws_secret_access_key = abcdEFGH1234/xyz",
		`"SecretAccessKey": "abcdEFGH1234/xyz"`,
		"Authorization: Bearer eyJhbGciOi.abc.def",
		"postgres://admin:hunter2@db:5432/app",
		"password=hunter2",
	}
	for _, c := range cases {
		got := Redact(c)
		if !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "hunter2") || strings.Contains(got, "abcdEFGH1234") {
			t.Errorf("Redact(%q) = %q", c, got)
		}
	}
	if got := Redact("instance i-0abc is running"); got != "instance i-0abc is running" {
		t.Errorf("over-redacted: %q", got)
	}
}

func TestProduction(t *testing.T) {
	p := NewProduction([]string{"prod"}, []string{"111111111111"})
	if !p.Is("acme-PROD-admin", "") || !p.Is("dev", "111111111111") || p.Is("dev", "222222222222") {
		t.Fatal("production detection wrong")
	}
}

func TestClassify(t *testing.T) {
	if Classify(context.DeadlineExceeded) != ErrTimeout {
		t.Fatal("timeout")
	}
	if Classify(fmt.Errorf("wrap: %w", errors.New("failed to refresh cached credentials, the SSO session has expired"))) != ErrAuth {
		t.Fatal("auth")
	}
	if Classify(errors.New("dial tcp: lookup sts.amazonaws.com: no such host")) != ErrNetwork {
		t.Fatal("network")
	}
}
