package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
)

func frame(stream byte, s string) []byte {
	var hdr [8]byte
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(s)))
	return append(hdr[:], s...)
}

func TestDemux(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(frame(1, "hello\nwor"))
	buf.Write(frame(2, "oops\n"))
	buf.Write(frame(1, "ld\n"))
	var lines []string
	col := targets.NewCollector(func(stream, line string) { lines = append(lines, stream+":"+line) }, 0)
	if err := demux(&buf, col); err != nil {
		t.Fatal(err)
	}
	col.Flush()
	if col.Stdout() != "hello\nworld\n" || col.Stderr() != "oops\n" {
		t.Fatalf("%q %q", col.Stdout(), col.Stderr())
	}
	if strings.Join(lines, "|") != "stdout:hello|stderr:oops|stdout:world" {
		t.Fatalf("lines %v", lines)
	}
}

type fakeEngine struct {
	mu       sync.Mutex
	execBody map[string]any
	putTar   map[string]string
	commitQ  string
	paths    []string
}

func newFakeEngine(t *testing.T) (*httptest.Server, *fakeEngine) {
	t.Helper()
	fe := &fakeEngine{putTar: map[string]string{}}
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		fe.mu.Lock()
		fe.paths = append(fe.paths, r.Method+" "+r.URL.Path)
		fe.mu.Unlock()
	}
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.47")
		io.WriteString(w, "OK")
	})
	mux.HandleFunc("/v1.47/version", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		json.NewEncoder(w).Encode(map[string]string{"Version": "27.0.1", "ApiVersion": "1.47", "Os": "linux", "Arch": "amd64"})
	})
	mux.HandleFunc("/v1.47/containers/web/json", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		io.WriteString(w, `{"Id":"abcdef1234567890","Name":"/web","Image":"sha256:img1234567890","Created":"2026-09-01T00:00:00Z",
"State":{"Status":"running","Running":true,"StartedAt":"2026-09-01T00:00:01Z","Health":{"Status":"healthy"}},
"Mounts":[{"Type":"volume","Name":"vol1","Destination":"/data","RW":true},{"Type":"bind","Source":"/host","Destination":"/ro","RW":false}],
"Config":{"Image":"nginx:alpine","Hostname":"abcdef123456","Env":["A=1"]},"HostConfig":{"Binds":["vol1:/data"]},
"NetworkSettings":{"Networks":{"demo_default":{"Aliases":["web","abcdef123456"],"IPAddress":"172.18.0.2"}}}}`)
	})
	mux.HandleFunc("/v1.47/containers/missing/json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"message":"No such container: missing"}`)
	})
	mux.HandleFunc("/v1.47/containers/web/start", func(w http.ResponseWriter, r *http.Request) { record(r); w.WriteHeader(304) })
	mux.HandleFunc("/v1.47/containers/web/stop", func(w http.ResponseWriter, r *http.Request) { record(r); w.WriteHeader(204) })
	mux.HandleFunc("/v1.47/containers/web/restart", func(w http.ResponseWriter, r *http.Request) { record(r); w.WriteHeader(204) })
	mux.HandleFunc("/v1.47/containers/web/exec", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		fe.mu.Lock()
		json.NewDecoder(r.Body).Decode(&fe.execBody)
		fe.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"Id": "e1"})
	})
	mux.HandleFunc("/v1.47/exec/e1/start", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		hj := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		rw.Flush()
		// Everything after the JSON body is stdin, until the client half-closes.
		rest, _ := io.ReadAll(rw.Reader)
		stdin := ""
		if i := bytes.IndexByte(rest, '}'); i >= 0 {
			stdin = string(rest[i+1:])
		}
		rw.Write(frame(1, "hi "+stdin))
		rw.Write(frame(2, "warn\n"))
		rw.Flush()
	})
	mux.HandleFunc("/v1.47/exec/e1/json", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		json.NewEncoder(w).Encode(map[string]any{"Running": false, "ExitCode": 3})
	})
	mux.HandleFunc("/v1.47/commit", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		fe.mu.Lock()
		fe.commitQ = r.URL.RawQuery
		fe.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:golden"})
	})
	mux.HandleFunc("/v1.47/containers/web/archive", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method == http.MethodPut {
			tr := tar.NewReader(r.Body)
			for {
				h, err := tr.Next()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(tr)
				fe.mu.Lock()
				fe.putTar[r.URL.Query().Get("path")+"/"+h.Name] = string(b)
				fe.mu.Unlock()
			}
			w.WriteHeader(200)
			return
		}
		w.Header().Set("Content-Type", "application/x-tar")
		tw := tar.NewWriter(w)
		tw.WriteHeader(&tar.Header{Name: "data/file.txt", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg})
		tw.Write([]byte("hello"))
		tw.Close()
	})
	mux.HandleFunc("/v1.47/volumes/vol1", func(w http.ResponseWriter, r *http.Request) { record(r); w.WriteHeader(404) })
	mux.HandleFunc("/v1.47/containers/create", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["Image"] != "greenroom/x:golden" || r.URL.Query().Get("name") != "web" {
			w.WriteHeader(400)
			io.WriteString(w, `{"message":"bad create body"}`)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"Id": "newid", "Warnings": []string{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fe
}

func newClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Options{Host: "tcp://" + strings.TrimPrefix(srv.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientBasics(t *testing.T) {
	srv, fe := newFakeEngine(t)
	c := newClient(t, srv)
	ctx := context.Background()
	if v, err := c.Ping(ctx); err != nil || !strings.Contains(v, "docker 27.0.1 (api 1.47") {
		t.Fatalf("ping %q %v", v, err)
	}
	ct, err := c.Inspect(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if ct.ShortName() != "web" || ct.ImageRef() != "nginx:alpine" || !ct.State.Running || ct.State.Health.Status != "healthy" || len(ct.Mounts) != 2 {
		t.Fatalf("inspect %+v", ct)
	}
	if _, err := c.Inspect(ctx, "missing"); !IsNotFound(err) {
		t.Fatalf("not found: %v", err)
	}
	if err := c.Start(ctx, "web"); err != nil {
		t.Fatalf("start 304 should be nil: %v", err)
	}
	if err := c.Stop(ctx, "web", 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Restart(ctx, "web", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitHealthy(ctx, "web", time.Second); err != nil {
		t.Fatal(err)
	}
	id, err := c.Commit(ctx, "web", "greenroom/x", "golden")
	if err != nil || id != "sha256:golden" || !strings.Contains(fe.commitQ, "repo=greenroom%2Fx") || !strings.Contains(fe.commitQ, "tag=golden") {
		t.Fatalf("commit %q %v %q", id, err, fe.commitQ)
	}
	if err := c.VolumeRemove(ctx, "vol1"); err != nil {
		t.Fatalf("volume 404 should be nil: %v", err)
	}
	newID, err := c.Create(ctx, "web", map[string]any{"Image": "greenroom/x:golden"})
	if err != nil || newID != "newid" {
		t.Fatalf("create %q %v", newID, err)
	}
	fe.mu.Lock()
	joined := strings.Join(fe.paths, "\n")
	fe.mu.Unlock()
	if !strings.Contains(joined, "GET /v1.47/version") {
		t.Fatalf("negotiated version not used:\n%s", joined)
	}
}

func TestExec(t *testing.T) {
	srv, fe := newFakeEngine(t)
	c := newClient(t, srv)
	var lines []string
	res, err := c.Run(context.Background(), targets.RunSpec{Container: "web", Argv: []string{"cat"}, Stdin: strings.NewReader("there\n"), Env: map[string]string{"K": "v"}, WorkDir: "/w"},
		func(stream, line string) { lines = append(lines, stream+":"+line) })
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Stdout != "hi there\n" || res.Stderr != "warn\n" {
		t.Fatalf("result %+v", res)
	}
	if strings.Join(lines, "|") != "stdout:hi there|stderr:warn" {
		t.Fatalf("lines %v", lines)
	}
	fe.mu.Lock()
	body := fe.execBody
	fe.mu.Unlock()
	if body["AttachStdin"] != true || body["WorkingDir"] != "/w" {
		t.Fatalf("exec body %v", body)
	}
	env, _ := body["Env"].([]any)
	if len(env) != 1 || env[0] != "K=v" {
		t.Fatalf("env %v", env)
	}
	// Script form pipes into sh -s.
	res, err = c.Run(context.Background(), targets.RunSpec{Container: "web", Script: "echo x"}, nil)
	if err != nil || !strings.HasPrefix(res.Stdout, "hi echo x") {
		t.Fatalf("script %+v %v", res, err)
	}
	fe.mu.Lock()
	cmd, _ := fe.execBody["Cmd"].([]any)
	fe.mu.Unlock()
	if len(cmd) != 2 || cmd[0] != "sh" || cmd[1] != "-s" {
		t.Fatalf("cmd %v", cmd)
	}
	if _, err := c.Run(context.Background(), targets.RunSpec{Argv: []string{"x"}}, nil); err == nil {
		t.Fatal("missing container accepted")
	}
}

func TestArchiveAndCopy(t *testing.T) {
	srv, fe := newFakeEngine(t)
	c := newClient(t, srv)
	ctx := context.Background()
	body, err := c.Archive(ctx, "web", "/data")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ExtractSingleFile(body)
	body.Close()
	if err != nil || string(b) != "hello" {
		t.Fatalf("archive %q %v", b, err)
	}
	if err := c.CopyTo(ctx, targets.RunSpec{Container: "web"}, "/etc/app/config.yml", []byte("a: 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	fe.mu.Lock()
	got := fe.putTar["/etc/app/config.yml"]
	fe.mu.Unlock()
	if got != "a: 1" {
		t.Fatalf("put tar %v", fe.putTar)
	}
}

func TestNewHosts(t *testing.T) {
	for _, h := range []string{"unix:///var/run/docker.sock", "tcp://1.2.3.4", "https://1.2.3.4:2376"} {
		if _, err := New(Options{Host: h}); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	if _, err := New(Options{Host: "ftp://x"}); err == nil {
		t.Fatal("bad scheme accepted")
	}
}
