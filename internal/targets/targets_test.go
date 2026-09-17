package targets

import (
	"strings"
	"testing"
)

func TestCollectorLines(t *testing.T) {
	var got []string
	c := NewCollector(func(stream, line string) { got = append(got, stream+":"+line) }, 0)
	c.Write("stdout", []byte("hel"))
	c.Write("stdout", []byte("lo\nwor"))
	c.Write("stderr", []byte("err1\r\n"))
	c.Write("stdout", []byte("ld\n"))
	c.Write("stdout", []byte("tail"))
	c.Flush()
	want := []string{"stdout:hello", "stderr:err1", "stdout:world", "stdout:tail"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v", got)
	}
	if c.Stdout() != "hello\nworld\ntail" || c.Stderr() != "err1\r\n" {
		t.Fatalf("buffers %q %q", c.Stdout(), c.Stderr())
	}
}

func TestCollectorLimit(t *testing.T) {
	c := NewCollector(nil, 10)
	c.Writer("stdout").Write([]byte("0123456789abcdef"))
	if c.Stdout() != "0123456789" {
		t.Fatalf("limit %q", c.Stdout())
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":            "''",
		"plain":       "plain",
		"a b":         "'a b'",
		"it's":        `'it'\''s'`,
		"/usr/bin:x=1": "/usr/bin:x=1",
		"$HOME":       "'$HOME'",
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
	if got := ShellJoin([]string{"psql", "-c", "select 1"}); got != "psql -c 'select 1'" {
		t.Fatalf("join %q", got)
	}
}

func TestHeredoc(t *testing.T) {
	h := Heredoc("cat", "line1\nGREENROOM_EOF\n")
	if !strings.Contains(h, "<<'GREENROOM_EOF_'") || !strings.HasSuffix(h, "GREENROOM_EOF_\n") {
		t.Fatalf("marker escalation failed:\n%s", h)
	}
	h = Heredoc("cat", "x")
	if h != "cat <<'GREENROOM_EOF'\nx\nGREENROOM_EOF\n" {
		t.Fatalf("heredoc %q", h)
	}
}

func TestResultError(t *testing.T) {
	if (Result{}).Error() != nil {
		t.Fatal("ok result has error")
	}
	err := Result{ExitCode: 2, Stderr: "boom\n"}.Error()
	if err == nil || !strings.Contains(err.Error(), "exit code 2: boom") {
		t.Fatalf("err %v", err)
	}
	err = Result{ExitCode: 1, Stdout: strings.Repeat("x", 500)}.Error()
	if err == nil || !strings.HasPrefix(err.Error(), "exit code 1: ...") {
		t.Fatalf("truncation %v", err)
	}
}

func TestEnvPrefix(t *testing.T) {
	if EnvPrefix(nil) != "" {
		t.Fatal("empty")
	}
	if got := EnvPrefix(map[string]string{"A": "x y"}); got != "A='x y' " {
		t.Fatalf("prefix %q", got)
	}
	if ShellFor(RunSpec{}) != "sh" || ShellFor(RunSpec{Shell: "bash"}) != "bash" {
		t.Fatal("shell")
	}
}
