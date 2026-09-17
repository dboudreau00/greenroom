package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dboudreau00/greenroom/internal/targets"
)

func TestHelperProcess(t *testing.T) {
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
	switch {
	case len(args) > 0 && args[0] == "echo":
		fmt.Println(strings.Join(args[1:], " "))
		fmt.Fprintln(os.Stderr, "noise")
	case len(args) > 1 && args[0] == "exit":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case len(args) > 0 && args[0] == "env":
		fmt.Println(os.Getenv("GREENROOM_X"))
	}
	os.Exit(0)
}

func helper(cmd ...string) []string {
	return append([]string{os.Args[0], "-test.run=TestHelperProcess", "--"}, cmd...)
}

func TestRunArgv(t *testing.T) {
	c := New("")
	env := map[string]string{"GO_WANT_HELPER_PROCESS": "1", "GREENROOM_X": "set"}
	var lines []string
	res, err := c.Run(context.Background(), targets.RunSpec{Argv: helper("echo", "hello", "world"), Env: env}, func(stream, line string) { lines = append(lines, stream+":"+line) })
	if err != nil || res.ExitCode != 0 || res.Stdout != "hello world\n" || res.Stderr != "noise\n" {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Join(lines, "|") != "stdout:hello world|stderr:noise" && strings.Join(lines, "|") != "stderr:noise|stdout:hello world" {
		t.Fatalf("lines %v", lines)
	}
	res, err = c.Run(context.Background(), targets.RunSpec{Argv: helper("env"), Env: env}, nil)
	if err != nil || strings.TrimSpace(res.Stdout) != "set" {
		t.Fatalf("env not passed: %+v %v", res, err)
	}
	res, err = c.Run(context.Background(), targets.RunSpec{Argv: helper("exit", "4"), Env: env}, nil)
	if err != nil || res.ExitCode != 4 {
		t.Fatalf("exit code: %+v %v", res, err)
	}
	if _, err := c.Run(context.Background(), targets.RunSpec{Argv: []string{"definitely-not-a-program-xyz"}}, nil); err == nil {
		t.Fatal("missing program should error")
	}
}

func TestRunScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	c := New("sh")
	res, err := c.Run(context.Background(), targets.RunSpec{Script: "echo from script\nexit 2"}, nil)
	if err != nil || res.ExitCode != 2 || !strings.Contains(res.Stdout, "from script") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestCopyAndPing(t *testing.T) {
	c := New("")
	dest := filepath.Join(t.TempDir(), "sub", "file.txt")
	if err := c.CopyTo(context.Background(), targets.RunSpec{}, dest, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "data" {
		t.Fatalf("copy %q %v", b, err)
	}
	if msg, err := c.Ping(context.Background()); err != nil || !strings.HasPrefix(msg, "local ") {
		t.Fatalf("ping %q %v", msg, err)
	}
}
