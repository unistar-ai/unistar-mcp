package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

// runResult holds the outcome of an external command execution.
type runResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

// combined returns stdout and stderr merged, useful for error reporting.
func (r runResult) combined() string {
	switch {
	case r.stderr == "":
		return r.stdout
	case r.stdout == "":
		return r.stderr
	default:
		return r.stdout + "\n" + r.stderr
	}
}

// run executes a command with the given arguments without invoking a shell,
// which avoids command injection from model-provided inputs. When dir is
// non-empty it is used as the working directory. extraEnv entries (KEY=VALUE)
// are appended to the inherited process environment, which is how a per-call
// GH_TOKEN can be injected without a shell.
func run(ctx context.Context, dir, name string, args ...string) runResult {
	return runEnv(ctx, dir, nil, name, args...)
}

func runEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) runResult {
	logrus.Debugf("exec: %s %v (dir=%q)", name, args, dir)

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Environ(), extraEnv...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	res := runResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		err:    err,
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.exitCode = exitErr.ExitCode()
	}
	return res
}

// wrap turns a failed command into an error whose message is tailored to the
// most common failure causes (missing binary, auth, not-found) so the agent
// gets actionable guidance instead of a raw stack of gh output.
func (r runResult) wrap(action string) error {
	// Binary missing on PATH.
	var execErr *exec.Error
	if errors.As(r.err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return fmt.Errorf("%s: %q is not installed or not on PATH. "+
			"Install the GitHub CLI (https://cli.github.com/) and git, then retry",
			action, execErr.Name)
	}

	low := strings.ToLower(r.combined())
	switch {
	case strings.Contains(low, "gh auth login"),
		strings.Contains(low, "authentication"),
		strings.Contains(low, "not logged in"),
		strings.Contains(low, "http 401"),
		strings.Contains(low, "bad credentials"):
		return fmt.Errorf("%s: GitHub authentication failed. "+
			"Run `gh auth login`, or set the GH_TOKEN environment variable for the server. "+
			"Details:\n%s", action, r.combined())

	case strings.Contains(low, "could not resolve to a repository"),
		strings.Contains(low, "http 404"),
		strings.Contains(low, "not found"):
		return fmt.Errorf("%s: repository, PR, or run not found (check the owner/repo and IDs). "+
			"Details:\n%s", action, r.combined())

	case strings.Contains(low, "http 403"),
		strings.Contains(low, "permission"),
		strings.Contains(low, "forbidden"):
		return fmt.Errorf("%s: permission denied — the token lacks access to this repository. "+
			"Details:\n%s", action, r.combined())

	default:
		return fmt.Errorf("%s (exit %d): %s", action, r.exitCode, r.combined())
	}
}
