package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

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

// errDetailBudget caps the raw command output included in wrapped errors.
const errDetailBudget = 2_000

// rateLimitRE matches GitHub primary and secondary rate-limit errors. These
// are not retried by runRetry: a seconds-scale backoff is too short to help,
// so the agent is told to wait instead (see wrap).
var rateLimitRE = regexp.MustCompile(`(?i)rate limit|HTTP 429|too many requests`)

// transientRE matches GitHub-side failures that usually succeed on retry:
// gateway errors from the API or the log storage backend, and dropped
// connections. Large-log downloads (gh run view --log-failed) hit these often.
var transientRE = regexp.MustCompile(`(?i)HTTP 50[234]|bad gateway|service unavailable|gateway timeout|server error|connection reset|unexpected EOF|TLS handshake timeout`)

// transient reports whether a failure looks like a temporary GitHub-side
// error rather than something wrong with the request. Rate limits are
// excluded: retrying them within seconds only burns more quota.
func (r runResult) transient() bool {
	return r.err != nil && transientRE.MatchString(r.combined()) && !r.rateLimited()
}

// rateLimited reports whether a failure is a GitHub rate-limit rejection.
func (r runResult) rateLimited() bool {
	return r.err != nil && rateLimitRE.MatchString(r.combined())
}

// runRetry executes a read-only command, retrying with a short backoff when
// the failure looks transient. Mutating commands must use run() directly so
// they never execute twice.
func runRetry(ctx context.Context, dir, name string, args ...string) runResult {
	const attempts = 3
	var res runResult
	for i := range attempts {
		if i > 0 {
			logrus.Debugf("transient failure, retry %d/%d: %s", i, attempts-1, name)
			select {
			case <-ctx.Done():
				return res
			case <-time.After(time.Duration(i) * 2 * time.Second):
			}
		}
		res = runEnv(ctx, dir, nil, name, args...)
		if !res.transient() {
			return res
		}
	}
	return res
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

	// In debug mode also log the command's result. Guarded by the level check
	// so the (capped) output strings are only built when debug is enabled.
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		logrus.Debugf("exec result: %s exit=%d stdout=%q stderr=%q",
			name, res.exitCode, clipForLog(res.stdout), clipForLog(res.stderr))
	}
	return res
}

// clipForLog bounds command output included in debug logs so a large payload
// (e.g. a multi-MB log download) does not flood the log.
func clipForLog(s string) string {
	const logOutputCap = 2_000
	if len(s) <= logOutputCap {
		return s
	}
	return s[:logOutputCap] + "…[truncated]"
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
	// Rate limits must be checked before the HTTP 403 case below: GitHub
	// rejects secondary-rate-limited requests with a 403.
	case r.rateLimited():
		return fmt.Errorf("%s: GitHub rate limit reached. "+
			"Do not retry immediately — wait at least a minute, then retry the same call. "+
			"Details:\n%s", action, tail(r.stderr, errDetailBudget))

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

	case r.transient():
		return fmt.Errorf("%s: GitHub returned a temporary server error. "+
			"This is not a problem with the request — retry the same call in a few seconds. "+
			"Details:\n%s", action, tail(r.stderr, errDetailBudget))

	default:
		// Cap the details: a command that fails mid-download can leave
		// megabytes of partial output in stdout.
		return fmt.Errorf("%s (exit %d): %s", action, r.exitCode, tail(r.combined(), errDetailBudget))
	}
}
