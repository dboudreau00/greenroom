package probe

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/registry"
)

// TestHelperProcess is re-executed as a fake command by the command probe.
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
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "echo":
		fmt.Println(strings.Join(args[1:], " "))
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(7)
	}
	os.Exit(1)
}

func helper(cmd ...string) []string {
	return append([]string{os.Args[0], "-test.run=TestHelperProcess", "--"}, cmd...)
}

func newProber(deps ...config.Dependency) *Prober {
	env := &config.Environment{Name: "t", Targets: map[string]config.Target{"local": {Kind: config.TargetLocal}}, Dependencies: deps,
		Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"}}
	return New(env, registry.New(env))
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := newProber()
	r := p.Probe(context.Background(), config.Dependency{Name: "port", Kind: config.KindTCP, Address: ln.Addr().String()})
	if !r.OK || r.Message != "port open" {
		t.Fatalf("%+v", r)
	}
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := closed.Addr().String()
	closed.Close()
	r = p.Probe(context.Background(), config.Dependency{Name: "port", Kind: config.KindTCP, Address: addr, Timeout: config.Duration(2 * time.Second)})
	if r.OK {
		t.Fatalf("closed port reported up: %+v", r)
	}
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Version", "1.2.3")
		switch r.URL.Path {
		case "/ok":
			io := "hello world"
			w.Write([]byte(io))
		case "/teapot":
			w.WriteHeader(418)
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	p := newProber()
	r := p.Probe(context.Background(), config.Dependency{Name: "api", Kind: config.KindHTTP, URL: srv.URL + "/ok", ExpectBody: "world", Critical: true})
	if !r.OK || r.Message != "HTTP 200" || r.Fingerprint == nil || r.Fingerprint.Value != "1.2.3" || !r.Critical {
		t.Fatalf("%+v", r)
	}
	r = p.Probe(context.Background(), config.Dependency{Name: "api", Kind: config.KindHTTP, URL: srv.URL + "/ok", ExpectBody: "missing"})
	if r.OK || !strings.Contains(r.Message, "does not contain") {
		t.Fatalf("%+v", r)
	}
	r = p.Probe(context.Background(), config.Dependency{Name: "api", Kind: config.KindHTTP, URL: srv.URL + "/teapot", ExpectStatus: 418})
	if !r.OK {
		t.Fatalf("expected status honoured: %+v", r)
	}
	r = p.Probe(context.Background(), config.Dependency{Name: "api", Kind: config.KindHTTP, URL: srv.URL + "/err"})
	if r.OK || r.Message != "status 500" {
		t.Fatalf("%+v", r)
	}
}

func TestTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	p := newProber()
	addr := strings.TrimPrefix(srv.URL, "https://")
	r := p.Probe(context.Background(), config.Dependency{Name: "cert", Kind: config.KindTLS, Address: addr, Insecure: true})
	if !r.OK || r.Fingerprint == nil || r.Fingerprint.Kind != "tls-serial" || r.Detail["daysLeft"] == nil {
		t.Fatalf("%+v", r)
	}
	r = p.Probe(context.Background(), config.Dependency{Name: "cert", Kind: config.KindTLS, Address: addr, Insecure: true, WarnDays: 100000})
	if !r.OK || !r.Warn || !strings.Contains(r.Message, "expires in") {
		t.Fatalf("warn expected: %+v", r)
	}
	r = p.Probe(context.Background(), config.Dependency{Name: "cert", Kind: config.KindTLS, Address: addr})
	if r.OK {
		t.Fatalf("self-signed cert should fail verification: %+v", r)
	}
}

func TestCommandAndProbeAll(t *testing.T) {
	ok := config.Dependency{Name: "good", Kind: config.KindCommand, Target: "local", Command: helper("echo", "version 9")}
	bad := config.Dependency{Name: "bad", Kind: config.KindCommand, Target: "local", Command: helper("fail"), Critical: true}
	p := newProber(ok, bad)
	results := p.ProbeAll(context.Background())
	if len(results) != 2 || results[0].Dep != "good" || results[1].Dep != "bad" {
		t.Fatalf("order %+v", results)
	}
	if !results[0].OK || results[0].Message != "version 9" || results[0].Fingerprint == nil || results[0].Fingerprint.Value != "version 9" {
		t.Fatalf("good %+v", results[0])
	}
	if results[1].OK || !strings.Contains(results[1].Message, "exit code 7") || !strings.Contains(results[1].Message, "boom") {
		t.Fatalf("bad %+v", results[1])
	}
	s := results[1].Sample()
	if s.OK || s.Dep != "bad" || s.Msg == "" {
		t.Fatalf("sample %+v", s)
	}
	r := p.Probe(context.Background(), config.Dependency{Name: "x", Kind: "nope"})
	if r.OK || !strings.Contains(r.Message, "unknown kind") {
		t.Fatalf("%+v", r)
	}
}

func TestDockerUnreachable(t *testing.T) {
	env := &config.Environment{Name: "t", Targets: map[string]config.Target{"docker": {Kind: config.TargetDocker, Host: "tcp://127.0.0.1:1"}},
		Dependencies: []config.Dependency{{Name: "c", Kind: config.KindDocker, Target: "docker", Container: "x", Timeout: config.Duration(2 * time.Second)}}}
	p := New(env, registry.New(env))
	r := p.Probe(context.Background(), env.Dependencies[0])
	if r.OK || r.Message == "" {
		t.Fatalf("%+v", r)
	}
}
