// Package targets defines the contract every platform adapter satisfies:
// run a command or script somewhere, stream its output, and report an exit
// code. Docker, Kubernetes, AWS SSM, SSH and the local machine all implement
// Executor so seed steps and heal actions do not care where they run.
package targets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// RunSpec describes one command to run on a target.
type RunSpec struct {
	// Argv runs a program directly. When empty, Script is piped into Shell.
	Argv []string
	// Script is a shell script fed to the shell on stdin (or via a heredoc
	// where a target cannot half-close stdin).
	Script string
	// Shell defaults to sh on Unix targets.
	Shell string
	// Stdin is optional input for Argv commands.
	Stdin io.Reader
	// StdinLen is the size of Stdin when known, used by adapters that must
	// decide between streaming and inlining.
	StdinLen int64
	Env      map[string]string
	WorkDir  string
	Timeout  time.Duration

	// Location inside the target.
	Container  string // docker container, or kubernetes container name
	Pod        string
	Namespace  string
	InstanceID string
}

// Result is the outcome of a run.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// OK reports whether the command exited zero.
func (r Result) OK() bool { return r.ExitCode == 0 }

// Error returns an error describing a non-zero exit.
func (r Result) Error() error {
	if r.ExitCode == 0 {
		return nil
	}
	tail := strings.TrimSpace(r.Stderr)
	if tail == "" {
		tail = strings.TrimSpace(r.Stdout)
	}
	if len(tail) > 400 {
		tail = "..." + tail[len(tail)-400:]
	}
	if tail != "" {
		return fmt.Errorf("exit code %d: %s", r.ExitCode, tail)
	}
	return fmt.Errorf("exit code %d", r.ExitCode)
}

// LineSink receives output lines as they arrive. stream is "stdout" or
// "stderr".
type LineSink func(stream, line string)

// Executor runs commands on a target.
type Executor interface {
	Run(ctx context.Context, spec RunSpec, sink LineSink) (Result, error)
}

// Copier can place a file on a target.
type Copier interface {
	CopyTo(ctx context.Context, spec RunSpec, dest string, content []byte, mode int64) error
}

// Pinger reports whether a target is reachable and what it is.
type Pinger interface {
	Ping(ctx context.Context) (string, error)
}

// ErrUnsupported is returned when an adapter cannot perform an operation.
var ErrUnsupported = errors.New("operation not supported by this target")

// Collector accumulates output for a Result while forwarding to a sink.
type Collector struct {
	mu     sync.Mutex
	sink   LineSink
	stdout strings.Builder
	stderr strings.Builder
	limit  int
	pend   map[string]string
}

// NewCollector returns a collector that keeps up to limit bytes per stream.
func NewCollector(sink LineSink, limit int) *Collector {
	if limit <= 0 {
		limit = 256 * 1024
	}
	return &Collector{sink: sink, limit: limit, pend: map[string]string{}}
}

// Write appends raw bytes for a stream, splitting into lines for the sink.
func (c *Collector) Write(stream string, b []byte) {
	if len(b) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	buf := c.buf(stream)
	if buf.Len() < c.limit {
		room := c.limit - buf.Len()
		if len(b) > room {
			buf.Write(b[:room])
		} else {
			buf.Write(b)
		}
	}
	if c.sink == nil {
		return
	}
	text := c.pend[stream] + string(b)
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		c.sink(stream, strings.TrimRight(text[:i], "\r"))
		text = text[i+1:]
	}
	c.pend[stream] = text
}

func (c *Collector) buf(stream string) *strings.Builder {
	if stream == "stderr" {
		return &c.stderr
	}
	return &c.stdout
}

// Flush emits any trailing partial lines.
func (c *Collector) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sink == nil {
		return
	}
	for stream, text := range c.pend {
		if text != "" {
			c.sink(stream, strings.TrimRight(text, "\r"))
		}
		c.pend[stream] = ""
	}
}

// Stdout returns collected stdout.
func (c *Collector) Stdout() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stdout.String()
}

// Stderr returns collected stderr.
func (c *Collector) Stderr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stderr.String()
}

// Writer returns an io.Writer that feeds one stream.
func (c *Collector) Writer(stream string) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		c.Write(stream, p)
		return len(p), nil
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellJoin joins argv into a single POSIX shell command line.
func ShellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = ShellQuote(a)
	}
	return strings.Join(parts, " ")
}

// Heredoc wraps a script so that it can be passed as a single sh -c argument
// with stdin content attached, for targets that cannot half-close stdin.
func Heredoc(command, stdin string) string {
	marker := "GREENROOM_EOF"
	for strings.Contains(stdin, marker) {
		marker += "_"
	}
	if !strings.HasSuffix(stdin, "\n") {
		stdin += "\n"
	}
	return command + " <<'" + marker + "'\n" + stdin + marker + "\n"
}

// ShellFor returns the shell to use for a spec.
func ShellFor(spec RunSpec) string {
	if spec.Shell != "" {
		return spec.Shell
	}
	return "sh"
}

// EnvPrefix renders env assignments for a POSIX command line.
func EnvPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	var sb strings.Builder
	for k, v := range env {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(ShellQuote(v))
		sb.WriteString(" ")
	}
	return sb.String()
}

// Deadline derives a context with the spec timeout applied.
func Deadline(ctx context.Context, spec RunSpec, def time.Duration) (context.Context, context.CancelFunc) {
	t := spec.Timeout
	if t == 0 {
		t = def
	}
	if t <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, t)
}
