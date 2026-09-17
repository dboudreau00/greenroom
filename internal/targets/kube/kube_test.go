package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
	"github.com/dboudreau00/greenroom/internal/targets/ws"
)

const deploymentJSON = `{"metadata":{"name":"web","namespace":"demo","uid":"u1","generation":5},
"spec":{"replicas":2,"selector":{"matchLabels":{"app":"web"}},"template":{"spec":{"containers":[{"name":"web","image":"nginx:1.27"}]}}},
"status":{"observedGeneration":5,"readyReplicas":2,"updatedReplicas":2,"availableReplicas":2}}`

type fakeAPI struct {
	mu      sync.Mutex
	patches []string
	execIn  string
	execCmd []string
}

func newFakeAPI(t *testing.T) (*httptest.Server, *fakeAPI) {
	t.Helper()
	fa := &fakeAPI{}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			io.WriteString(w, `{"reason":"Unauthorized","message":"no token"}`)
			return false
		}
		return true
	}
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		io.WriteString(w, `{"gitVersion":"v1.31.0","platform":"linux/amd64"}`)
	})
	mux.HandleFunc("/apis/apps/v1/namespaces/demo/deployments/web", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			fa.mu.Lock()
			fa.patches = append(fa.patches, r.Header.Get("Content-Type")+" "+string(b))
			fa.mu.Unlock()
			io.WriteString(w, deploymentJSON)
			return
		}
		io.WriteString(w, deploymentJSON)
	})
	mux.HandleFunc("/apis/apps/v1/namespaces/demo/deployments/gone", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"reason":"NotFound","message":"deployments.apps \"gone\" not found"}`)
	})
	mux.HandleFunc("/api/v1/namespaces/demo/pods", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("labelSelector") != "app=web" {
			w.WriteHeader(400)
			return
		}
		io.WriteString(w, `{"items":[{"metadata":{"name":"web-1","namespace":"demo","uid":"p1","creationTimestamp":"2026-09-01T00:00:00Z"},
"spec":{"containers":[{"name":"web"}]},"status":{"phase":"Running","containerStatuses":[{"ready":true,"image":"nginx:1.27"}]}}]}`)
	})
	mux.HandleFunc("/api/v1/namespaces/demo/pods/web-1/exec", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		fa.mu.Lock()
		fa.execCmd = r.URL.Query()["command"]
		fa.mu.Unlock()
		hj := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: v5.channel.k8s.io\r\n\r\n", ws.ServerAccept(r.Header.Get("Sec-WebSocket-Key")))
		sc := ws.ServerConn(conn, rw.Reader)
		var stdin []byte
		if r.URL.Query().Get("stdin") == "true" {
			for {
				_, msg, err := sc.ReadMessage()
				if err != nil {
					return
				}
				if len(msg) > 0 && msg[0] == 255 {
					break
				}
				if len(msg) > 0 && msg[0] == 0 {
					stdin = append(stdin, msg[1:]...)
				}
			}
		}
		fa.mu.Lock()
		fa.execIn = string(stdin)
		fa.mu.Unlock()
		sc.WriteServerMessage(ws.OpBinary, append([]byte{1}, []byte("out:"+string(stdin))...))
		sc.WriteServerMessage(ws.OpBinary, append([]byte{2}, []byte("err\n")...))
		status := `{"status":"Success","metadata":{}}`
		if len(fa.execCmd) > 0 && fa.execCmd[0] == "false" {
			status = `{"status":"Failure","message":"command terminated with non-zero exit code","reason":"NonZeroExitCode","details":{"causes":[{"reason":"ExitCode","message":"3"}]}}`
		}
		sc.WriteServerMessage(ws.OpBinary, append([]byte{3}, []byte(status)...))
		sc.WriteServerMessage(ws.OpClose, []byte{0x03, 0xE8})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fa
}

func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()
	kc := map[string]any{
		"current-context": "c1",
		"clusters":        []any{map[string]any{"name": "cl", "cluster": map[string]any{"server": server, "insecure-skip-tls-verify": true}}},
		"contexts":        []any{map[string]any{"name": "c1", "context": map[string]any{"cluster": "cl", "user": "u", "namespace": "demo"}}},
		"users":           []any{map[string]any{"name": "u", "user": map[string]any{"token": "tok"}}},
	}
	b, _ := json.Marshal(kc)
	p := filepath.Join(t.TempDir(), "kube.json")
	os.WriteFile(p, b, 0o600)
	return p
}

func TestClientWithKubeconfig(t *testing.T) {
	srv, fa := newFakeAPI(t)
	c, err := New(Options{Kubeconfig: writeKubeconfig(t, srv.URL)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if c.Namespace() != "demo" {
		t.Fatalf("namespace %q", c.Namespace())
	}
	if v, err := c.Ping(ctx); err != nil || !strings.Contains(v, "v1.31.0") {
		t.Fatalf("ping %q %v", v, err)
	}
	w, err := c.GetWorkload(ctx, "", "deploy/web")
	if err != nil {
		t.Fatal(err)
	}
	if !w.Healthy() || w.Desired != 2 || w.Ready != 2 || w.Generation != 5 || w.Images[0] != "nginx:1.27" || w.Selector["app"] != "web" {
		t.Fatalf("workload %+v", w)
	}
	if _, err := c.GetWorkload(ctx, "", "deployment/gone"); !IsNotFound(err) {
		t.Fatalf("not found: %v", err)
	}
	if err := c.Scale(ctx, "", "deployment/web", 3); err != nil {
		t.Fatal(err)
	}
	if err := c.RolloutRestart(ctx, "", "deployment/web"); err != nil {
		t.Fatal(err)
	}
	fa.mu.Lock()
	patches := strings.Join(fa.patches, "\n")
	fa.mu.Unlock()
	if !strings.Contains(patches, `application/merge-patch+json {"spec":{"replicas":3}}`) || !strings.Contains(patches, "greenroom.dev/restartedAt") {
		t.Fatalf("patches:\n%s", patches)
	}
	pod, err := c.FirstReadyPod(ctx, "", "deployment/web")
	if err != nil || pod != "web-1" {
		t.Fatalf("first pod %q %v", pod, err)
	}
	if _, err := c.WaitReady(ctx, "", "deployment/web", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestExec(t *testing.T) {
	srv, fa := newFakeAPI(t)
	c, err := New(Options{Kubeconfig: writeKubeconfig(t, srv.URL)})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	res, err := c.Run(context.Background(), targets.RunSpec{Pod: "deployment/web", Argv: []string{"cat"}, Stdin: strings.NewReader("hello")},
		func(stream, line string) { lines = append(lines, stream+":"+line) })
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Stdout != "out:hello" || res.Stderr != "err\n" {
		t.Fatalf("result %+v", res)
	}
	fa.mu.Lock()
	in, cmd := fa.execIn, fa.execCmd
	fa.mu.Unlock()
	if in != "hello" || len(cmd) != 1 || cmd[0] != "cat" {
		t.Fatalf("server saw stdin %q cmd %v", in, cmd)
	}
	if strings.Join(lines, "|") != "stderr:err|stdout:out:hello" && strings.Join(lines, "|") != "stdout:out:hello|stderr:err" {
		t.Fatalf("lines %v", lines)
	}
	res, err = c.Run(context.Background(), targets.RunSpec{Pod: "web-1", Argv: []string{"false"}}, nil)
	if err != nil || res.ExitCode != 3 {
		t.Fatalf("non-zero exit: %+v %v", res, err)
	}
	if err := c.CopyTo(context.Background(), targets.RunSpec{Pod: "web-1"}, "/tmp/x.txt", []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	fa.mu.Lock()
	cmd = fa.execCmd
	fa.mu.Unlock()
	if strings.Join(cmd, " ") != "tar xf - -C /tmp" {
		t.Fatalf("copy command %v", cmd)
	}
}

func TestParseWorkloadKinds(t *testing.T) {
	var obj map[string]any
	json.Unmarshal([]byte(`{"metadata":{"name":"ds","generation":2},"spec":{},"status":{"observedGeneration":2,"desiredNumberScheduled":3,"numberReady":2}}`), &obj)
	w := parseWorkload("daemonset", obj)
	if w.Healthy() || w.Desired != 3 || w.Ready != 2 {
		t.Fatalf("daemonset %+v", w)
	}
	json.Unmarshal([]byte(`{"metadata":{"name":"p","uid":"x"},"spec":{"containers":[{"name":"a"},{"name":"b"}]},"status":{"phase":"Running","containerStatuses":[{"ready":true},{"ready":false}]}}`), &obj)
	w = parseWorkload("pod", obj)
	if w.Healthy() || w.Desired != 2 || w.Ready != 1 || w.Phase != "Running" {
		t.Fatalf("pod %+v", w)
	}
	if k, n, err := SplitResource("sts/db"); err != nil || k != "statefulset" || n != "db" {
		t.Fatal("split")
	}
	if _, _, err := SplitResource("nope"); err == nil {
		t.Fatal("bad resource accepted")
	}
	if _, err := resourcePath("job", "ns", "x"); err == nil {
		t.Fatal("unsupported kind accepted")
	}
}

func TestExecCredentialPlugin(t *testing.T) {
	tok, exp, err := runExecCredential(context.Background(), os.Args[0], []string{"-test.run=TestHelperExecCred", "--"}, []string{"GO_WANT_HELPER_PROCESS=1"})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "plugin-token" || exp.Before(time.Now()) {
		t.Fatalf("token %q exp %v", tok, exp)
	}
}

func TestHelperExecCred(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if !strings.Contains(os.Getenv("KUBERNETES_EXEC_INFO"), "ExecCredential") {
		fmt.Println(`{"kind":"ExecCredential","status":{}}`)
		os.Exit(0)
	}
	fmt.Printf(`{"apiVersion":"client.authentication.k8s.io/v1beta1","kind":"ExecCredential","status":{"token":"plugin-token","expirationTimestamp":"%s"}}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	os.Exit(0)
}

func TestKubeconfigErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kube.yaml")
	os.WriteFile(p, []byte("apiVersion: v1\nkind: Config\n"), 0o600)
	if _, err := New(Options{Kubeconfig: p}); err == nil || !strings.Contains(err.Error(), "must be JSON") {
		t.Fatalf("yaml should be rejected with a hint: %v", err)
	}
	if _, err := New(Options{Kubeconfig: writeKubeconfig(t, "http://x"), Context: "missing"}); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("missing context: %v", err)
	}
	if _, err := New(Options{Server: "http://x"}); err == nil {
		t.Fatal("explicit server without credentials accepted")
	}
}
