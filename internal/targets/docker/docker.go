// Package docker is a small Docker Engine API client built on net/http. It
// speaks to a Unix socket, a Windows named pipe, or a TCP endpoint with or
// without TLS, and implements the pieces Greenroom needs: inspect, start,
// stop, restart, exec with streamed output, commit, create, archive in and
// out, and volume removal.
package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
)

// fallbackAPIVersion is used when the engine does not advertise a version.
const fallbackAPIVersion = "1.44"

// Client talks to one Docker Engine.
type Client struct {
	host    string
	http    *http.Client
	dial    func(ctx context.Context) (net.Conn, error)
	base    string
	scheme  string
	verMu   sync.Mutex
	version string
}

// apiVersion negotiates the API version once by reading the API-Version
// header of the unversioned ping endpoint, so old and new engines both work.
func (c *Client) apiVersion(ctx context.Context) string {
	c.verMu.Lock()
	defer c.verMu.Unlock()
	if c.version != "" {
		return c.version
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/_ping", nil)
	if err != nil {
		return fallbackAPIVersion
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fallbackAPIVersion
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	v := strings.TrimPrefix(resp.Header.Get("API-Version"), "v")
	if v == "" {
		v = fallbackAPIVersion
	}
	c.version = v
	return v
}

// Options configure the client.
type Options struct {
	Host     string
	CertFile string
	KeyFile  string
	CAFile   string
}

// New creates a client for host. Empty host uses DOCKER_HOST or the platform
// default socket.
func New(opt Options) (*Client, error) {
	host := opt.Host
	if host == "" {
		host = os.Getenv("DOCKER_HOST")
	}
	if host == "" {
		if isWindows() {
			host = "npipe:////./pipe/docker_engine"
		} else {
			host = "unix:///var/run/docker.sock"
		}
	}
	c := &Client{host: host}
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("docker host %q: %w", host, err)
	}
	var tlsCfg *tls.Config
	if opt.CertFile != "" || opt.CAFile != "" || u.Scheme == "https" {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		if opt.CertFile != "" {
			cert, err := tls.LoadX509KeyPair(opt.CertFile, opt.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("docker client cert: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
		if opt.CAFile != "" {
			pem, err := os.ReadFile(opt.CAFile)
			if err != nil {
				return nil, fmt.Errorf("docker ca: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("docker ca: no certificates found")
			}
			tlsCfg.RootCAs = pool
		}
	}
	switch u.Scheme {
	case "unix":
		p := u.Path
		if p == "" {
			p = u.Host
		}
		c.dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", p)
		}
		c.base = "http://docker"
	case "npipe":
		p := strings.TrimPrefix(host, "npipe://")
		p = strings.ReplaceAll(p, "/", `\`)
		c.dial = func(ctx context.Context) (net.Conn, error) { return dialPipe(ctx, p) }
		c.base = "http://docker"
	case "tcp", "http", "https":
		addr := u.Host
		if _, _, err := net.SplitHostPort(addr); err != nil {
			if tlsCfg != nil {
				addr += ":2376"
			} else {
				addr += ":2375"
			}
		}
		c.dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			if tlsCfg != nil {
				cfg := tlsCfg.Clone()
				h, _, _ := net.SplitHostPort(addr)
				cfg.ServerName = h
				tc := tls.Client(conn, cfg)
				if err := tc.HandshakeContext(ctx); err != nil {
					conn.Close()
					return nil, err
				}
				return tc, nil
			}
			return conn, nil
		}
		c.base = "http://" + addr
		if tlsCfg != nil {
			c.base = "https://" + addr
		}
	default:
		return nil, fmt.Errorf("docker host %q: unsupported scheme %q", host, u.Scheme)
	}
	tr := &http.Transport{
		DialContext:     func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dial(ctx) },
		TLSClientConfig: tlsCfg,
	}
	if tlsCfg != nil {
		tr.DialTLSContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dial(ctx) }
	}
	c.http = &http.Client{Transport: tr}
	return c, nil
}

// Host returns the configured endpoint.
func (c *Client) Host() string { return c.host }

func (c *Client) url(ctx context.Context, p string, q url.Values) string {
	u := c.base + "/v" + c.apiVersion(ctx) + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// APIError is a non-2xx response from the engine.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker: %d %s", e.Status, e.Message) }

// IsNotFound reports whether err is a 404 from the engine.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == 404
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(ctx, p, q), rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("docker %s %s: %w", method, p, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readAPIError(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func readAPIError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var m struct {
		Message string `json:"message"`
	}
	json.Unmarshal(b, &m)
	if m.Message == "" {
		m.Message = strings.TrimSpace(string(b))
	}
	return &APIError{Status: resp.StatusCode, Message: m.Message}
}

// Ping returns the engine version.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var v struct {
		Version    string `json:"Version"`
		APIVersion string `json:"ApiVersion"`
		Os         string `json:"Os"`
		Arch       string `json:"Arch"`
	}
	if err := c.do(ctx, http.MethodGet, "/version", nil, nil, &v); err != nil {
		return "", err
	}
	return fmt.Sprintf("docker %s (api %s, %s/%s)", v.Version, v.APIVersion, v.Os, v.Arch), nil
}

// Health is the container healthcheck state.
type Health struct {
	Status        string `json:"Status"`
	FailingStreak int    `json:"FailingStreak"`
}

// State is the container runtime state.
type State struct {
	Status     string  `json:"Status"`
	Running    bool    `json:"Running"`
	Paused     bool    `json:"Paused"`
	Restarting bool    `json:"Restarting"`
	ExitCode   int     `json:"ExitCode"`
	Error      string  `json:"Error"`
	StartedAt  string  `json:"StartedAt"`
	FinishedAt string  `json:"FinishedAt"`
	Health     *Health `json:"Health"`
}

// Mount is one container mount.
type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

// Container is the subset of inspect output Greenroom uses.
type Container struct {
	ID              string                     `json:"Id"`
	Name            string                     `json:"Name"`
	Image           string                     `json:"Image"`
	Created         string                     `json:"Created"`
	State           State                      `json:"State"`
	Mounts          []Mount                    `json:"Mounts"`
	Config          map[string]json.RawMessage `json:"Config"`
	HostConfig      map[string]json.RawMessage `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

// ShortName strips the leading slash from the container name.
func (c *Container) ShortName() string { return strings.TrimPrefix(c.Name, "/") }

// ImageRef returns the image the container was created from.
func (c *Container) ImageRef() string {
	var s string
	if raw, ok := c.Config["Image"]; ok {
		json.Unmarshal(raw, &s)
	}
	return s
}

// Inspect returns a container by name or id.
func (c *Client) Inspect(ctx context.Context, name string) (*Container, error) {
	var out Container
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Start starts a container.
func (c *Client) Start(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil, nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 304 {
		return nil
	}
	return err
}

// Stop stops a container, waiting up to timeout seconds.
func (c *Client) Stop(ctx context.Context, name string, timeout int) error {
	q := url.Values{}
	if timeout > 0 {
		q.Set("t", fmt.Sprint(timeout))
	}
	err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/stop", q, nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 304 {
		return nil
	}
	return err
}

// Restart restarts a container.
func (c *Client) Restart(ctx context.Context, name string, timeout int) error {
	q := url.Values{}
	if timeout > 0 {
		q.Set("t", fmt.Sprint(timeout))
	}
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/restart", q, nil, nil)
}

// Remove deletes a container. removeVolumes also removes anonymous volumes.
func (c *Client) Remove(ctx context.Context, name string, force, removeVolumes bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	if removeVolumes {
		q.Set("v", "true")
	}
	return c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name), q, nil, nil)
}

// Create creates a container from a full create body and returns its id.
func (c *Client) Create(ctx context.Context, name string, body map[string]any) (string, error) {
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	var out struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/create", q, body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Commit snapshots a container's filesystem into an image.
func (c *Client) Commit(ctx context.Context, name, repo, tag string) (string, error) {
	q := url.Values{"container": {name}, "repo": {repo}, "tag": {tag}, "pause": {"true"}, "comment": {"greenroom snapshot"}}
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/commit", q, map[string]any{}, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Image is the subset of image inspect output used for fingerprints.
type Image struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     string   `json:"Created"`
}

// ImageInspect returns image metadata.
func (c *Client) ImageInspect(ctx context.Context, ref string) (*Image, error) {
	var out Image
	if err := c.do(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VolumeRemove deletes a named volume.
func (c *Client) VolumeRemove(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), url.Values{"force": {"true"}}, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Archive streams a tar of a path inside the container.
func (c *Client) Archive(ctx context.Context, name, p string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(ctx, "/containers/"+url.PathEscape(name)+"/archive", url.Values{"path": {p}}), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, readAPIError(resp)
	}
	return resp.Body, nil
}

// PutArchive extracts a tar stream into a path inside the container.
func (c *Client) PutArchive(ctx context.Context, name, p string, r io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.url(ctx, "/containers/"+url.PathEscape(name)+"/archive", url.Values{"path": {p}}), r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readAPIError(resp)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// CopyTo writes a single file into the container.
func (c *Client) CopyTo(ctx context.Context, spec targets.RunSpec, dest string, content []byte, mode int64) error {
	if spec.Container == "" {
		return errors.New("docker copy needs a container")
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if mode == 0 {
		mode = 0o644
	}
	hdr := &tar.Header{Name: path.Base(dest), Mode: mode, Size: int64(len(content)), ModTime: time.Now()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return c.PutArchive(ctx, spec.Container, path.Dir(dest), &buf)
}

// WaitHealthy polls until the container is running and, when it declares a
// healthcheck, healthy.
func (c *Client) WaitHealthy(ctx context.Context, name string, timeout time.Duration) (*Container, error) {
	deadline := time.Now().Add(timeout)
	for {
		ct, err := c.Inspect(ctx, name)
		if err != nil {
			return nil, err
		}
		if ct.State.Running && (ct.State.Health == nil || ct.State.Health.Status == "healthy") {
			return ct, nil
		}
		if !ct.State.Running && !ct.State.Restarting && ct.State.Status == "exited" {
			return ct, fmt.Errorf("container %s exited with code %d", name, ct.State.ExitCode)
		}
		if time.Now().After(deadline) {
			status := ct.State.Status
			if ct.State.Health != nil {
				status += "/" + ct.State.Health.Status
			}
			return ct, fmt.Errorf("container %s not healthy after %s (state %s)", name, timeout, status)
		}
		select {
		case <-ctx.Done():
			return ct, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Run executes a command inside a container and streams its output.
func (c *Client) Run(ctx context.Context, spec targets.RunSpec, sink targets.LineSink) (targets.Result, error) {
	if spec.Container == "" {
		return targets.Result{}, errors.New("docker exec needs a container")
	}
	ctx, cancel := targets.Deadline(ctx, spec, 10*time.Minute)
	defer cancel()
	start := time.Now()
	argv := spec.Argv
	var stdin io.Reader = spec.Stdin
	if len(argv) == 0 {
		argv = []string{targets.ShellFor(spec), "-s"}
		stdin = strings.NewReader(spec.Script)
	}
	var env []string
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	body := map[string]any{
		"AttachStdin":  stdin != nil,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          false,
		"Cmd":          argv,
		"Env":          env,
	}
	if spec.WorkDir != "" {
		body["WorkingDir"] = spec.WorkDir
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(spec.Container)+"/exec", nil, body, &created); err != nil {
		return targets.Result{}, err
	}
	col := targets.NewCollector(sink, 0)
	if err := c.execStart(ctx, created.ID, stdin, col); err != nil {
		return targets.Result{Stdout: col.Stdout(), Stderr: col.Stderr(), Duration: time.Since(start), ExitCode: -1}, err
	}
	col.Flush()
	code, err := c.execExitCode(ctx, created.ID)
	res := targets.Result{ExitCode: code, Stdout: col.Stdout(), Stderr: col.Stderr(), Duration: time.Since(start)}
	return res, err
}

func (c *Client) execExitCode(ctx context.Context, id string) (int, error) {
	for i := 0; i < 50; i++ {
		var out struct {
			ExitCode int  `json:"ExitCode"`
			Running  bool `json:"Running"`
		}
		if err := c.do(ctx, http.MethodGet, "/exec/"+id+"/json", nil, nil, &out); err != nil {
			return -1, err
		}
		if !out.Running {
			return out.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return -1, errors.New("exec still running after stream closed")
}

// execStart performs the hijacked exec/start exchange and demultiplexes the
// stdout/stderr stream into the collector.
func (c *Client) execStart(ctx context.Context, id string, stdin io.Reader, col *targets.Collector) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()
	body, _ := json.Marshal(map[string]any{"Detach": false, "Tty": false})
	req, err := http.NewRequest(http.MethodPost, c.url(ctx, "/exec/"+id+"/start", nil), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if err := req.Write(conn); err != nil {
		return err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		return readAPIError(resp)
	}
	if resp.StatusCode == http.StatusOK && resp.Header.Get("Content-Type") != "application/vnd.docker.raw-stream" && resp.Header.Get("Content-Type") != "application/vnd.docker.multiplexed-stream" {
		// Not upgraded: engine returned a plain body (older engines).
		defer resp.Body.Close()
		return demux(resp.Body, col)
	}
	writeErr := make(chan error, 1)
	go func() {
		if stdin != nil {
			if _, err := io.Copy(conn, stdin); err != nil {
				writeErr <- err
				return
			}
		}
		closeWrite(conn)
		writeErr <- nil
	}()
	err = demux(br, col)
	if werr := <-writeErr; werr != nil && err == nil {
		err = fmt.Errorf("writing stdin: %w", werr)
	}
	if err != nil && errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func closeWrite(conn net.Conn) {
	type cw interface{ CloseWrite() error }
	if c, ok := conn.(cw); ok {
		c.CloseWrite()
	}
}

// demux reads the Docker multiplexed stream format.
func demux(r io.Reader, col *targets.Collector) error {
	var hdr [8]byte
	buf := make([]byte, 32*1024)
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		stream := "stdout"
		if hdr[0] == 2 {
			stream = "stderr"
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		for n > 0 {
			chunk := buf
			if uint32(len(chunk)) > n {
				chunk = chunk[:n]
			}
			m, err := io.ReadFull(r, chunk)
			if m > 0 {
				col.Write(stream, chunk[:m])
			}
			if err != nil {
				return err
			}
			n -= uint32(m)
		}
	}
}

// ExtractSingleFile reads the first regular file out of a tar stream.
func ExtractSingleFile(r io.Reader) ([]byte, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}
