// Package ssh runs commands on a remote host through the system ssh binary
// (OpenSSH ships with Windows 10+, macOS and every Linux). Scripts are fed
// on stdin so nothing is written to the remote disk unless a step asks.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
)

// Options identify the host.
type Options struct {
	Host         string
	User         string
	Port         int
	IdentityFile string
	Args         []string
	Shell        string
}

// Client is an ssh target.
type Client struct {
	opt Options
	// Binary overrides the ssh executable (tests).
	Binary string
}

// New returns a client.
func New(opt Options) (*Client, error) {
	if opt.Host == "" {
		return nil, errors.New("ssh target needs host")
	}
	return &Client{opt: opt, Binary: "ssh"}, nil
}

func (c *Client) baseArgs() []string {
	args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=15"}
	if c.opt.Port > 0 {
		args = append(args, "-p", strconv.Itoa(c.opt.Port))
	}
	if c.opt.IdentityFile != "" {
		args = append(args, "-i", c.opt.IdentityFile)
	}
	args = append(args, c.opt.Args...)
	dest := c.opt.Host
	if c.opt.User != "" {
		dest = c.opt.User + "@" + dest
	}
	return append(args, dest)
}

// Ping runs a trivial remote command.
func (c *Client) Ping(ctx context.Context) (string, error) {
	res, err := c.Run(ctx, targets.RunSpec{Argv: []string{"uname", "-a"}, Timeout: 20 * time.Second}, nil)
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return "", res.Error()
	}
	return "ssh " + c.opt.Host + ": " + strings.TrimSpace(res.Stdout), nil
}

// Run executes a command or script remotely.
func (c *Client) Run(ctx context.Context, spec targets.RunSpec, sink targets.LineSink) (targets.Result, error) {
	ctx, cancel := targets.Deadline(ctx, spec, 10*time.Minute)
	defer cancel()
	start := time.Now()
	args := c.baseArgs()
	var stdin io.Reader
	shell := spec.Shell
	if shell == "" {
		shell = c.opt.Shell
	}
	if shell == "" {
		shell = "sh"
	}
	remote := ""
	if len(spec.Argv) > 0 {
		remote = targets.EnvPrefix(spec.Env) + targets.ShellJoin(spec.Argv)
		stdin = spec.Stdin
	} else {
		remote = targets.EnvPrefix(spec.Env) + shell + " -s"
		stdin = strings.NewReader(spec.Script)
	}
	if spec.WorkDir != "" {
		remote = "cd " + targets.ShellQuote(spec.WorkDir) + " && " + remote
	}
	args = append(args, "--", remote)
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Stdin = stdin
	col := targets.NewCollector(sink, 0)
	cmd.Stdout = col.Writer("stdout")
	cmd.Stderr = col.Writer("stderr")
	err := cmd.Run()
	col.Flush()
	res := targets.Result{Stdout: col.Stdout(), Stderr: col.Stderr(), Duration: time.Since(start)}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
		if res.ExitCode == 255 {
			return res, fmt.Errorf("ssh to %s failed: %s", c.opt.Host, strings.TrimSpace(res.Stderr))
		}
	default:
		res.ExitCode = -1
		return res, err
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, nil
}

// CopyTo writes a file remotely by streaming it through cat.
func (c *Client) CopyTo(ctx context.Context, spec targets.RunSpec, dest string, content []byte, mode int64) error {
	if mode == 0 {
		mode = 0o644
	}
	script := fmt.Sprintf("mkdir -p %s && cat > %s && chmod %o %s", targets.ShellQuote(dirOf(dest)), targets.ShellQuote(dest), mode, targets.ShellQuote(dest))
	spec.Argv = []string{"sh", "-c", script}
	spec.Stdin = strings.NewReader(string(content))
	res, err := c.Run(ctx, spec, nil)
	if err != nil {
		return err
	}
	return res.Error()
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}
