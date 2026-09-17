// Package local runs commands on the machine Greenroom itself runs on.
package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
)

// Client is the local executor.
type Client struct {
	Shell string
}

// New returns a local executor.
func New(shell string) *Client { return &Client{Shell: shell} }

// Ping reports the host.
func (c *Client) Ping(ctx context.Context) (string, error) {
	h, _ := os.Hostname()
	return fmt.Sprintf("local %s (%s/%s)", h, runtime.GOOS, runtime.GOARCH), nil
}

func (c *Client) shell(spec targets.RunSpec) (string, []string) {
	sh := spec.Shell
	if sh == "" {
		sh = c.Shell
	}
	if sh == "" {
		if runtime.GOOS == "windows" {
			if _, err := exec.LookPath("sh"); err == nil {
				sh = "sh"
			} else {
				sh = "powershell"
			}
		} else {
			sh = "sh"
		}
	}
	switch strings.ToLower(filepath.Base(sh)) {
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return sh, []string{"-NoProfile", "-NonInteractive", "-Command", "-"}
	case "cmd", "cmd.exe":
		return sh, []string{"/Q"}
	}
	return sh, []string{"-s"}
}

// Run executes the spec locally.
func (c *Client) Run(ctx context.Context, spec targets.RunSpec, sink targets.LineSink) (targets.Result, error) {
	ctx, cancel := targets.Deadline(ctx, spec, 10*time.Minute)
	defer cancel()
	start := time.Now()
	var cmd *exec.Cmd
	if len(spec.Argv) > 0 {
		cmd = exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
		cmd.Stdin = spec.Stdin
	} else {
		sh, args := c.shell(spec)
		cmd = exec.CommandContext(ctx, sh, args...)
		cmd.Stdin = strings.NewReader(spec.Script)
	}
	cmd.Dir = spec.WorkDir
	if len(spec.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range spec.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	col := targets.NewCollector(sink, 0)
	cmd.Stdout = col.Writer("stdout")
	cmd.Stderr = col.Writer("stderr")
	err := cmd.Run()
	col.Flush()
	res := targets.Result{Stdout: col.Stdout(), Stderr: col.Stderr(), Duration: time.Since(start)}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		res.ExitCode = -1
		return res, err
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, nil
}

// CopyTo writes a local file.
func (c *Client) CopyTo(ctx context.Context, spec targets.RunSpec, dest string, content []byte, mode int64) error {
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, content, os.FileMode(mode))
}
