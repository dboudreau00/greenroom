// Package probe answers one question per dependency: is it up right now.
// Each kind has a probe, every probe returns a Result with latency, a
// human message, structured detail and, where the platform exposes one, a
// fingerprint that later reveals when the dependency changed underneath a
// seed set.
package probe

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/registry"
	"github.com/dboudreau00/greenroom/internal/sqlrun"
	"github.com/dboudreau00/greenroom/internal/state"
	"github.com/dboudreau00/greenroom/internal/targets"
	"github.com/dboudreau00/greenroom/internal/targets/kube"
)

// Result is one probe observation.
type Result struct {
	Dep         string             `json:"dep"`
	Kind        string             `json:"kind"`
	OK          bool               `json:"ok"`
	Warn        bool               `json:"warn,omitempty"`
	Critical    bool               `json:"critical"`
	Latency     time.Duration      `json:"latency"`
	Message     string             `json:"message"`
	Detail      map[string]any     `json:"detail,omitempty"`
	Fingerprint *state.Fingerprint `json:"fingerprint,omitempty"`
	CheckedAt   time.Time          `json:"checkedAt"`
}

// Sample converts the result to a state sample.
func (r Result) Sample() state.Sample {
	msg := ""
	if !r.OK {
		msg = r.Message
	}
	return state.Sample{At: r.CheckedAt, Dep: r.Dep, OK: r.OK, MS: r.Latency.Milliseconds(), Msg: msg}
}

// Prober probes the dependencies of one environment.
type Prober struct {
	env *config.Environment
	reg *registry.Registry
	// HTTPClient is replaceable for tests.
	HTTPClient *http.Client
	// Now is replaceable for tests.
	Now func() time.Time
}

// New creates a prober.
func New(env *config.Environment, reg *registry.Registry) *Prober {
	return &Prober{env: env, reg: reg, Now: time.Now}
}

// ProbeAll probes every dependency in parallel, preserving order.
func (p *Prober) ProbeAll(ctx context.Context) []Result {
	out := make([]Result, len(p.env.Dependencies))
	var wg sync.WaitGroup
	for i := range p.env.Dependencies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i] = p.Probe(ctx, p.env.Dependencies[i])
		}(i)
	}
	wg.Wait()
	return out
}

// Probe checks one dependency.
func (p *Prober) Probe(ctx context.Context, dep config.Dependency) Result {
	ctx, cancel := context.WithTimeout(ctx, dep.Timeout.Or(config.DefaultTimeout))
	defer cancel()
	start := p.Now()
	res := Result{Dep: dep.Name, Kind: dep.Kind, Critical: dep.Critical, CheckedAt: start, Detail: map[string]any{}}
	var err error
	switch dep.Kind {
	case config.KindTCP:
		err = p.probeTCP(ctx, dep, &res)
	case config.KindTLS:
		err = p.probeTLS(ctx, dep, &res)
	case config.KindHTTP:
		err = p.probeHTTP(ctx, dep, &res)
	case config.KindPostgres, config.KindMySQL:
		err = p.probeSQL(ctx, dep, &res)
	case config.KindDocker:
		err = p.probeDocker(ctx, dep, &res)
	case config.KindKubernetes:
		err = p.probeKube(ctx, dep, &res)
	case config.KindEC2:
		err = p.probeEC2(ctx, dep, &res)
	case config.KindCommand:
		err = p.probeCommand(ctx, dep, &res)
	default:
		err = fmt.Errorf("unknown kind %q", dep.Kind)
	}
	res.Latency = p.Now().Sub(start)
	if err != nil {
		res.OK = false
		res.Message = err.Error()
		if ctx.Err() != nil && !strings.Contains(res.Message, "timeout") && !strings.Contains(res.Message, "deadline") {
			res.Message = "timed out after " + dep.Timeout.Or(config.DefaultTimeout).String() + ": " + res.Message
		}
	} else {
		res.OK = true
	}
	if len(res.Detail) == 0 {
		res.Detail = nil
	}
	return res
}

func (p *Prober) probeTCP(ctx context.Context, dep config.Dependency, res *Result) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", dep.Address)
	if err != nil {
		return err
	}
	conn.Close()
	res.Message = "port open"
	res.Detail["address"] = dep.Address
	return nil
}

func (p *Prober) probeTLS(ctx context.Context, dep config.Dependency, res *Result) error {
	host, _, err := net.SplitHostPort(dep.Address)
	if err != nil {
		host = dep.Address
	}
	d := tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: dep.Insecure, MinVersion: tls.VersionTLS12}}
	conn, err := d.DialContext(ctx, "tcp", dep.Address)
	if err != nil {
		return err
	}
	defer conn.Close()
	cs := conn.(*tls.Conn).ConnectionState()
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("no certificate presented")
	}
	cert := cs.PeerCertificates[0]
	now := p.Now()
	left := cert.NotAfter.Sub(now)
	days := int(left.Hours() / 24)
	res.Detail["subject"] = cert.Subject.CommonName
	res.Detail["issuer"] = cert.Issuer.CommonName
	res.Detail["notAfter"] = cert.NotAfter.UTC().Format(time.RFC3339)
	res.Detail["daysLeft"] = days
	res.Fingerprint = &state.Fingerprint{Kind: "tls-serial", Value: cert.SerialNumber.String(), Detail: cert.NotAfter.UTC().Format("2006-01-02"), At: now}
	if left <= 0 {
		return fmt.Errorf("certificate expired %s ago", (-left).Round(time.Hour))
	}
	warn := dep.WarnDays
	if warn == 0 {
		warn = 14
	}
	if days < warn {
		res.Warn = true
		res.Message = fmt.Sprintf("certificate expires in %d days", days)
	} else {
		res.Message = fmt.Sprintf("certificate valid, %d days left", days)
	}
	return nil
}

func (p *Prober) httpClient(insecure bool) *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure, MinVersion: tls.VersionTLS12}}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (p *Prober) probeHTTP(ctx context.Context, dep config.Dependency, res *Result) error {
	method := dep.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, dep.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "greenroom-probe")
	for k, v := range dep.Headers {
		req.Header.Set(k, v)
	}
	resp, err := p.httpClient(dep.Insecure).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	res.Detail["status"] = resp.StatusCode
	if s := resp.Header.Get("Server"); s != "" {
		res.Detail["server"] = s
	}
	if v := resp.Header.Get("X-Version"); v != "" {
		res.Detail["version"] = v
		res.Fingerprint = &state.Fingerprint{Kind: "http-version", Value: v, At: p.Now()}
	} else if etag := resp.Header.Get("ETag"); etag != "" {
		res.Fingerprint = &state.Fingerprint{Kind: "http-etag", Value: etag, At: p.Now()}
	}
	if dep.ExpectStatus != 0 {
		if resp.StatusCode != dep.ExpectStatus {
			return fmt.Errorf("status %d, expected %d", resp.StatusCode, dep.ExpectStatus)
		}
	} else if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if dep.ExpectBody != "" && !strings.Contains(string(body), dep.ExpectBody) {
		return fmt.Errorf("status %d but body does not contain %q", resp.StatusCode, dep.ExpectBody)
	}
	res.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	return nil
}

// SQLRunner opens the runner for a database dependency.
func (p *Prober) SQLRunner(dep config.Dependency) (sqlrun.Runner, error) {
	var ex targets.Executor
	if dep.Via != nil {
		var err error
		ex, err = p.reg.Executor(dep.Via.Target)
		if err != nil {
			return nil, err
		}
	}
	return sqlrun.Open(&dep, ex)
}

func (p *Prober) probeSQL(ctx context.Context, dep config.Dependency, res *Result) error {
	r, err := p.SQLRunner(dep)
	if err != nil {
		return err
	}
	defer r.Close()
	version, err := r.Ping(ctx)
	if err != nil {
		return err
	}
	res.Detail["version"] = version
	res.Detail["route"] = r.Describe()
	res.Fingerprint = &state.Fingerprint{Kind: "sql-version", Value: version, At: p.Now()}
	res.Message = "connected: " + version
	return nil
}

func (p *Prober) probeDocker(ctx context.Context, dep config.Dependency, res *Result) error {
	cli, err := p.reg.Docker(dep.Target)
	if err != nil {
		return err
	}
	ct, err := cli.Inspect(ctx, dep.Container)
	if err != nil {
		return err
	}
	res.Detail["status"] = ct.State.Status
	res.Detail["image"] = ct.ImageRef()
	res.Detail["imageId"] = short(ct.Image)
	res.Detail["containerId"] = short(ct.ID)
	res.Detail["startedAt"] = ct.State.StartedAt
	res.Fingerprint = &state.Fingerprint{Kind: "docker-container", Value: short(ct.ID), Detail: "image " + short(ct.Image), At: p.Now()}
	if ct.State.Health != nil {
		res.Detail["health"] = ct.State.Health.Status
	}
	if !ct.State.Running {
		return fmt.Errorf("container is %s", ct.State.Status)
	}
	if ct.State.Health != nil && ct.State.Health.Status != "healthy" {
		if ct.State.Health.Status == "starting" {
			res.Warn = true
			res.Message = "running, healthcheck still starting"
			return nil
		}
		return fmt.Errorf("container running but %s (%d failing checks)", ct.State.Health.Status, ct.State.Health.FailingStreak)
	}
	res.Message = "running"
	if ct.State.Health != nil {
		res.Message += ", healthy"
	}
	return nil
}

func (p *Prober) probeKube(ctx context.Context, dep config.Dependency, res *Result) error {
	cli, err := p.reg.Kube(dep.Target)
	if err != nil {
		return err
	}
	w, err := cli.GetWorkload(ctx, dep.Namespace, dep.Resource)
	if err != nil {
		if kube.IsNotFound(err) {
			return fmt.Errorf("%s not found", dep.Resource)
		}
		return err
	}
	res.Detail["ready"] = w.Ready
	res.Detail["desired"] = w.Desired
	res.Detail["generation"] = w.Generation
	res.Detail["images"] = w.Images
	if w.Phase != "" {
		res.Detail["phase"] = w.Phase
	}
	res.Fingerprint = &state.Fingerprint{Kind: "k8s-generation", Value: fmt.Sprint(w.Generation), Detail: strings.Join(w.Images, ","), At: p.Now()}
	if w.Kind == "pod" {
		res.Fingerprint = &state.Fingerprint{Kind: "k8s-pod", Value: w.UID, Detail: strings.Join(w.Images, ","), At: p.Now()}
	}
	if !w.Healthy() {
		msg := fmt.Sprintf("%d/%d ready", w.Ready, w.Desired)
		if w.Message != "" {
			msg += ": " + w.Message
		}
		if w.Kind == "pod" {
			msg = w.Phase + ", " + msg
		}
		return fmt.Errorf("%s", msg)
	}
	res.Message = fmt.Sprintf("%d/%d ready", w.Ready, w.Desired)
	return nil
}

func (p *Prober) probeEC2(ctx context.Context, dep config.Dependency, res *Result) error {
	cli, err := p.reg.AWS(dep.Target)
	if err != nil {
		return err
	}
	inst, err := cli.Instance(ctx, dep.InstanceID)
	if err != nil {
		return err
	}
	res.Detail["state"] = inst.State
	res.Detail["type"] = inst.Type
	res.Detail["name"] = inst.Name
	res.Detail["privateIp"] = inst.PrivateIP
	res.Detail["publicIp"] = inst.PublicIP
	res.Detail["launchTime"] = inst.LaunchTime.UTC().Format(time.RFC3339)
	res.Detail["systemStatus"] = inst.SystemStatus
	res.Detail["instanceStatus"] = inst.InstanceStatus
	res.Fingerprint = &state.Fingerprint{Kind: "ec2-launch", Value: inst.LaunchTime.UTC().Format(time.RFC3339), Detail: inst.ImageID, At: p.Now()}
	if !inst.Healthy() {
		if inst.State == "running" {
			return fmt.Errorf("running but status checks %s/%s", inst.SystemStatus, inst.InstanceStatus)
		}
		return fmt.Errorf("instance is %s", inst.State)
	}
	res.Message = "running, status checks passed"
	if inst.SystemStatus == "initializing" || inst.InstanceStatus == "initializing" {
		res.Warn = true
		res.Message = "running, status checks initialising"
	}
	return nil
}

func (p *Prober) probeCommand(ctx context.Context, dep config.Dependency, res *Result) error {
	ex, err := p.reg.Executor(dep.Target)
	if err != nil {
		return err
	}
	spec := targets.RunSpec{Argv: dep.Command, Script: dep.Script, Container: dep.Container, Pod: dep.Resource, Namespace: dep.Namespace, InstanceID: dep.InstanceID, Env: p.env.Env}
	r, err := ex.Run(ctx, spec, nil)
	if err != nil {
		return err
	}
	out := strings.TrimSpace(r.Stdout)
	if len(out) > 200 {
		out = out[:200] + "..."
	}
	res.Detail["exitCode"] = r.ExitCode
	if out != "" {
		res.Detail["output"] = out
		res.Fingerprint = &state.Fingerprint{Kind: "command-output", Value: firstLine(out), At: p.Now()}
	}
	if !r.OK() {
		return r.Error()
	}
	res.Message = "exit 0"
	if out != "" {
		res.Message = firstLine(out)
	}
	return nil
}

func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
