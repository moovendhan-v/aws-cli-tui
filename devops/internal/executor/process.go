package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// allowlist is the complete set of external programs the tool may launch.
var allowlist = map[string]bool{
	"aws":                    true,
	"session-manager-plugin": true,
	"psql":                   true, // PostgreSQL client (interactive SQL shells)
	"mysql":                  true, // MySQL / MariaDB client
}

// installHint helps when an allowlisted program is missing.
var installHint = map[string]string{
	"psql":  " — install it with: brew install libpq && brew link --force libpq",
	"mysql": " — install it with: brew install mysql-client",
	"aws":   " — install the AWS CLI v2",
}

// Allowed reports whether a program may be launched.
func Allowed(program string) bool { return allowlist[program] }

// Command builds an exec.Cmd for an allowlisted program. Arguments are passed
// directly to the process (never through a shell).
func Command(ctx context.Context, argv []string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	if !allowlist[argv[0]] {
		return nil, fmt.Errorf("program %q is not allowlisted", argv[0])
	}
	for _, a := range argv[1:] {
		if strings.ContainsAny(a, "\x00\n\r") {
			return nil, fmt.Errorf("argument contains control characters")
		}
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("%s is not installed or not on PATH%s", argv[0], installHint[argv[0]])
	}
	return exec.CommandContext(ctx, path, argv[1:]...), nil
}

// RunProcess runs a non-interactive allowlisted process, streaming stdout and
// stderr into the job and recording the exit code.
func RunProcess(ctx context.Context, j *Job, argv []string) error {
	cmd, err := Command(ctx, argv)
	if err != nil {
		return err
	}
	j.Printf("$ %s", strings.Join(argv, " "))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	pipe := func(r io.Reader, prefix string) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			j.Write(prefix + sc.Text())
		}
	}
	wg.Add(2)
	go pipe(stdout, "")
	go pipe(stderr, "stderr: ")
	wg.Wait()
	err = cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	j.setExit(code)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}
