package ssh

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dboudreau00/greenroom/internal/targets"
)

// TestHelperSSH stands in for the ssh binary: it prints every argument it
// received after the first "--" and everything on stdin.
func TestHelperSSH(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	for _, a := range args {
		fmt.Println("arg:" + a)
	}
	b, _ := io.ReadAll(os.Stdin)
	fmt.Println("stdin:" + strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", "|"))
	if v := os.Getenv("SSH_HELPER_EXIT"); v != "" {
		code, _ := strconv.Atoi(v)
		fmt.Fprintln(os.Stderr, "connection refused")
		os.Exit(code)
	}
	os.Exit(0)
}

func fakeClient(t *testing.T) *Client {
	t.Helper()
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	c, err := New(Options{Host: "bastion.example.com", User: "ec2-user", Port: 2222, IdentityFile: "/keys/demo.pem", Args: []string{"-J", "jump"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Binary = os.Args[0]
	c.BinaryArgs = []string{"-test.run=TestHelperSSH", "--"}
	return c
}

func argsOf(out string) (args []string, stdin string) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "arg:"):
			args = append(args, strings.TrimPrefix(line, "arg:"))
		case strings.HasPrefix(line, "stdin:"):
			stdin = strings.TrimPrefix(line, "stdin:")
		}
	}
	return
}

func TestRunArgv(t *testing.T) {
	c := fakeClient(t)
	res, err := c.Run(context.Background(), targets.RunSpec{Argv: []string{"psql", "-c", "select 1"}, Env: map[string]string{"PGDATABASE": "demo"}, WorkDir: "/srv"}, nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	args, stdin := argsOf(res.Stdout)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-o BatchMode=yes", "-o StrictHostKeyChecking=accept-new", "-p 2222", "-i /keys/demo.pem", "-J jump", "ec2-user@bastion.example.com --"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	remote := args[len(args)-1]
	if remote != "cd /srv && PGDATABASE=demo psql -c 'select 1'" {
		t.Fatalf("remote command %q", remote)
	}
	if stdin != "" {
		t.Fatalf("unexpected stdin %q", stdin)
	}
}

func TestRunScriptAndCopy(t *testing.T) {
	c := fakeClient(t)
	var lines []string
	res, err := c.Run(context.Background(), targets.RunSpec{Script: "echo hi\nuptime", Shell: "bash"}, func(stream, line string) { lines = append(lines, line) })
	if err != nil {
		t.Fatal(err)
	}
	args, stdin := argsOf(res.Stdout)
	if args[len(args)-1] != "bash -s" || stdin != "echo hi|uptime" {
		t.Fatalf("script: remote %q stdin %q", args[len(args)-1], stdin)
	}
	if len(lines) == 0 {
		t.Fatal("sink received nothing")
	}
	if err := c.CopyTo(context.Background(), targets.RunSpec{}, "/opt/app/seed.sql", []byte("select 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExitCodes(t *testing.T) {
	c := fakeClient(t)
	t.Setenv("SSH_HELPER_EXIT", "3")
	res, err := c.Run(context.Background(), targets.RunSpec{Argv: []string{"false"}}, nil)
	if err != nil || res.ExitCode != 3 {
		t.Fatalf("remote exit 3: %+v %v", res, err)
	}
	t.Setenv("SSH_HELPER_EXIT", "255")
	_, err = c.Run(context.Background(), targets.RunSpec{Argv: []string{"true"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "ssh to bastion.example.com failed") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("ssh failure: %v", err)
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("host required")
	}
}
