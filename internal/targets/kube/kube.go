// Package kube is a small Kubernetes API client. It resolves credentials
// from an in-cluster service account, an explicit server plus token, or a
// kubeconfig in JSON form (including exec credential plugins such as
// aws eks get-token), then reads workload status, scales, restarts
// rollouts, and execs into pods over WebSocket.
package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/targets"
	"github.com/dboudreau00/greenroom/internal/targets/ws"
)

// Options select how the client authenticates.
type Options struct {
	// Kubeconfig: "auto" (run kubectl), "in-cluster", or a path to a JSON
	// kubeconfig. Empty falls back to Server/Token, then in-cluster, then auto.
	Kubeconfig   string
	Context      string
	Namespace    string
	Server       string
	Token        string
	TokenFile    string
	TokenCommand []string
	CAFile       string
	Insecure     bool
}

// Client talks to one cluster.
type Client struct {
	server    string
	namespace string
	http      *http.Client
	tlsCfg    *tls.Config
	authMu    sync.Mutex
	token     string
	tokenExp  time.Time
	tokenFn   func(ctx context.Context) (string, time.Time, error)
	desc      string
}

// New resolves credentials and returns a client.
func New(opt Options) (*Client, error) {
	c := &Client{namespace: opt.Namespace}
	mode := opt.Kubeconfig
	if mode == "" {
		switch {
		case opt.Server != "":
			mode = "explicit"
		case os.Getenv("KUBERNETES_SERVICE_HOST") != "":
			mode = "in-cluster"
		default:
			mode = "auto"
		}
	}
	var err error
	switch mode {
	case "explicit":
		err = c.fromExplicit(opt)
	case "in-cluster":
		err = c.fromInCluster()
	case "auto":
		err = c.fromKubectl(opt.Context)
	default:
		err = c.fromKubeconfigFile(mode, opt.Context)
	}
	if err != nil {
		return nil, err
	}
	if c.namespace == "" {
		c.namespace = "default"
	}
	c.http = &http.Client{Transport: &http.Transport{TLSClientConfig: c.tlsCfg, Proxy: http.ProxyFromEnvironment}}
	return c, nil
}

// Namespace returns the default namespace.
func (c *Client) Namespace() string { return c.namespace }

// Server returns the API server URL.
func (c *Client) Server() string { return c.server }

func (c *Client) fromExplicit(opt Options) error {
	c.server = strings.TrimRight(opt.Server, "/")
	c.tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: opt.Insecure}
	if opt.CAFile != "" {
		pem, err := os.ReadFile(opt.CAFile)
		if err != nil {
			return fmt.Errorf("kubernetes ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("kubernetes ca: no certificates found")
		}
		c.tlsCfg.RootCAs = pool
	}
	switch {
	case opt.Token != "":
		c.token = opt.Token
		c.tokenExp = time.Now().Add(100 * 365 * 24 * time.Hour)
	case opt.TokenFile != "":
		f := opt.TokenFile
		c.tokenFn = func(ctx context.Context) (string, time.Time, error) {
			b, err := os.ReadFile(f)
			return strings.TrimSpace(string(b)), time.Now().Add(time.Minute), err
		}
	case len(opt.TokenCommand) > 0:
		cmd := opt.TokenCommand
		c.tokenFn = func(ctx context.Context) (string, time.Time, error) {
			return runExecCredential(ctx, cmd[0], cmd[1:], nil)
		}
	default:
		return errors.New("kubernetes target needs token, tokenFile or tokenCommand")
	}
	c.desc = "explicit " + c.server
	return nil
}

func (c *Client) fromInCluster() error {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return errors.New("not running in a cluster (KUBERNETES_SERVICE_HOST unset)")
	}
	if port == "" {
		port = "443"
	}
	c.server = "https://" + host + ":" + port
	const dir = "/var/run/secrets/kubernetes.io/serviceaccount"
	pem, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return fmt.Errorf("in-cluster ca: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	c.tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	c.tokenFn = func(ctx context.Context) (string, time.Time, error) {
		b, err := os.ReadFile(filepath.Join(dir, "token"))
		return strings.TrimSpace(string(b)), time.Now().Add(time.Minute), err
	}
	if ns, err := os.ReadFile(filepath.Join(dir, "namespace")); err == nil && c.namespace == "" {
		c.namespace = strings.TrimSpace(string(ns))
	}
	c.desc = "in-cluster " + c.server
	return nil
}

func (c *Client) fromKubectl(context_ string) error {
	args := []string{"config", "view", "--raw", "-o", "json"}
	if context_ != "" {
		args = append(args, "--context", context_)
	}
	cmd := exec.Command("kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("kubectl config view: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := c.fromKubeconfigJSON(out, context_); err != nil {
		return err
	}
	c.desc = "kubectl " + c.desc
	return nil
}

func (c *Client) fromKubeconfigFile(path, context_ string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("apiVersion:")) || !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return fmt.Errorf("%s: kubeconfig must be JSON (run: kubectl config view --raw -o json > %s)", path, path)
	}
	if err := c.fromKubeconfigJSON(b, context_); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	c.desc = filepath.Base(path) + " " + c.desc
	return nil
}

type kubeconfig struct {
	CurrentContext string `json:"current-context"`
	Clusters       []struct {
		Name    string `json:"name"`
		Cluster struct {
			Server   string `json:"server"`
			CAData   string `json:"certificate-authority-data"`
			CAFile   string `json:"certificate-authority"`
			Insecure bool   `json:"insecure-skip-tls-verify"`
		} `json:"cluster"`
	} `json:"clusters"`
	Contexts []struct {
		Name    string `json:"name"`
		Context struct {
			Cluster   string `json:"cluster"`
			User      string `json:"user"`
			Namespace string `json:"namespace"`
		} `json:"context"`
	} `json:"contexts"`
	Users []struct {
		Name string `json:"name"`
		User struct {
			Token        string `json:"token"`
			TokenFile    string `json:"tokenFile"`
			CertData     string `json:"client-certificate-data"`
			KeyData      string `json:"client-key-data"`
			CertFile     string `json:"client-certificate"`
			KeyFile      string `json:"client-key"`
			Username     string `json:"username"`
			Password     string `json:"password"`
			AuthProvider *struct {
				Name string `json:"name"`
			} `json:"auth-provider"`
			Exec *struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Env     []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env"`
			} `json:"exec"`
		} `json:"user"`
	} `json:"users"`
}

func (c *Client) fromKubeconfigJSON(b []byte, context_ string) error {
	var kc kubeconfig
	if err := json.Unmarshal(b, &kc); err != nil {
		return fmt.Errorf("kubeconfig: %w", err)
	}
	if context_ == "" {
		context_ = kc.CurrentContext
	}
	if context_ == "" {
		return errors.New("kubeconfig has no current-context; set target.context")
	}
	var clusterName, userName string
	found := false
	for _, ctx := range kc.Contexts {
		if ctx.Name == context_ {
			clusterName, userName = ctx.Context.Cluster, ctx.Context.User
			if c.namespace == "" {
				c.namespace = ctx.Context.Namespace
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("kubeconfig: context %q not found", context_)
	}
	c.tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	found = false
	for _, cl := range kc.Clusters {
		if cl.Name != clusterName {
			continue
		}
		found = true
		c.server = strings.TrimRight(cl.Cluster.Server, "/")
		c.tlsCfg.InsecureSkipVerify = cl.Cluster.Insecure
		var pem []byte
		if cl.Cluster.CAData != "" {
			var err error
			pem, err = base64.StdEncoding.DecodeString(cl.Cluster.CAData)
			if err != nil {
				return fmt.Errorf("kubeconfig: cluster %s ca data: %w", clusterName, err)
			}
		} else if cl.Cluster.CAFile != "" {
			var err error
			pem, err = os.ReadFile(cl.Cluster.CAFile)
			if err != nil {
				return fmt.Errorf("kubeconfig: cluster %s ca file: %w", clusterName, err)
			}
		}
		if pem != nil {
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(pem)
			c.tlsCfg.RootCAs = pool
		}
	}
	if !found || c.server == "" {
		return fmt.Errorf("kubeconfig: cluster %q not found", clusterName)
	}
	for _, u := range kc.Users {
		if u.Name != userName {
			continue
		}
		user := u.User
		switch {
		case user.Token != "":
			c.token = user.Token
			c.tokenExp = time.Now().Add(100 * 365 * 24 * time.Hour)
		case user.TokenFile != "":
			f := user.TokenFile
			c.tokenFn = func(ctx context.Context) (string, time.Time, error) {
				b, err := os.ReadFile(f)
				return strings.TrimSpace(string(b)), time.Now().Add(time.Minute), err
			}
		case user.CertData != "" || user.CertFile != "":
			var certPEM, keyPEM []byte
			var err error
			if user.CertData != "" {
				certPEM, err = base64.StdEncoding.DecodeString(user.CertData)
				if err == nil {
					keyPEM, err = base64.StdEncoding.DecodeString(user.KeyData)
				}
			} else {
				certPEM, err = os.ReadFile(user.CertFile)
				if err == nil {
					keyPEM, err = os.ReadFile(user.KeyFile)
				}
			}
			if err != nil {
				return fmt.Errorf("kubeconfig: user %s client cert: %w", userName, err)
			}
			cert, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return fmt.Errorf("kubeconfig: user %s client cert: %w", userName, err)
			}
			c.tlsCfg.Certificates = []tls.Certificate{cert}
		case user.Exec != nil:
			ex := user.Exec
			var env []string
			for _, e := range ex.Env {
				env = append(env, e.Name+"="+e.Value)
			}
			c.tokenFn = func(ctx context.Context) (string, time.Time, error) {
				return runExecCredential(ctx, ex.Command, ex.Args, env)
			}
		case user.AuthProvider != nil:
			return fmt.Errorf("kubeconfig: user %s uses auth-provider %q which is not supported; switch to an exec plugin", userName, user.AuthProvider.Name)
		case user.Username != "":
			basic := base64.StdEncoding.EncodeToString([]byte(user.Username + ":" + user.Password))
			c.token = "basic:" + basic
			c.tokenExp = time.Now().Add(100 * 365 * 24 * time.Hour)
		default:
			return fmt.Errorf("kubeconfig: user %q has no usable credentials", userName)
		}
	}
	c.desc = "context " + context_ + " (" + c.server + ")"
	return nil
}

// runExecCredential runs a client-go credential plugin and returns the token.
// A plugin that returns client certificates instead is reported as an error.
func runExecCredential(ctx context.Context, command string, args, env []string) (string, time.Time, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, `KUBERNETES_EXEC_INFO={"apiVersion":"client.authentication.k8s.io/v1beta1","kind":"ExecCredential","spec":{"interactive":false}}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("credential plugin %s: %v: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	var cred struct {
		Status struct {
			Token               string `json:"token"`
			ExpirationTimestamp string `json:"expirationTimestamp"`
			ClientCertData      string `json:"clientCertificateData"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &cred); err != nil {
		return "", time.Time{}, fmt.Errorf("credential plugin %s: bad output: %w", command, err)
	}
	if cred.Status.Token == "" {
		if cred.Status.ClientCertData != "" {
			return "", time.Time{}, fmt.Errorf("credential plugin %s returned a client certificate, which is not supported", command)
		}
		return "", time.Time{}, fmt.Errorf("credential plugin %s returned no token", command)
	}
	exp := time.Now().Add(10 * time.Minute)
	if cred.Status.ExpirationTimestamp != "" {
		if t, err := time.Parse(time.RFC3339, cred.Status.ExpirationTimestamp); err == nil {
			exp = t.Add(-30 * time.Second)
		}
	}
	return cred.Status.Token, exp, nil
}

func (c *Client) authHeader(ctx context.Context) (string, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return formatAuth(c.token), nil
	}
	if c.tokenFn == nil {
		if len(c.tlsCfg.Certificates) > 0 {
			return "", nil
		}
		return "", errors.New("no kubernetes credentials")
	}
	tok, exp, err := c.tokenFn(ctx)
	if err != nil {
		return "", err
	}
	c.token, c.tokenExp = tok, exp
	return formatAuth(tok), nil
}

func formatAuth(tok string) string {
	if strings.HasPrefix(tok, "basic:") {
		return "Basic " + strings.TrimPrefix(tok, "basic:")
	}
	return "Bearer " + tok
}

// APIError is a non-2xx response.
type APIError struct {
	Status  int
	Reason  string
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("kubernetes: %d %s: %s", e.Status, e.Reason, e.Message) }

// IsNotFound reports a 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == 404
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, contentType string, body []byte, out any) error {
	u := c.server + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	auth, err := c.authHeader(ctx)
	if err != nil {
		return err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kubernetes %s %s: %w", method, p, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		var st struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}
		json.Unmarshal(b, &st)
		if st.Message == "" {
			st.Message = strings.TrimSpace(string(b))
		}
		return &APIError{Status: resp.StatusCode, Reason: st.Reason, Message: st.Message}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// Ping returns the server version.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var v struct {
		GitVersion string `json:"gitVersion"`
		Platform   string `json:"platform"`
	}
	if err := c.do(ctx, http.MethodGet, "/version", nil, "", nil, &v); err != nil {
		return "", err
	}
	return fmt.Sprintf("kubernetes %s (%s) via %s", v.GitVersion, v.Platform, c.desc), nil
}

// Workload is the normalised status of a deployment, statefulset, daemonset
// or pod.
type Workload struct {
	Kind               string            `json:"kind"`
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	UID                string            `json:"uid"`
	Generation         int64             `json:"generation"`
	ObservedGeneration int64             `json:"observedGeneration"`
	Desired            int               `json:"desired"`
	Ready              int               `json:"ready"`
	Updated            int               `json:"updated"`
	Available          int               `json:"available"`
	Images             []string          `json:"images"`
	Selector           map[string]string `json:"selector,omitempty"`
	Phase              string            `json:"phase,omitempty"`
	Message            string            `json:"message,omitempty"`
}

// Healthy reports whether the workload has every desired replica ready.
func (w *Workload) Healthy() bool {
	if w.Kind == "pod" {
		return w.Phase == "Running" && w.Ready >= w.Desired
	}
	return w.Desired == w.Ready && (w.Kind == "daemonset" || w.Updated >= w.Desired) && w.ObservedGeneration >= w.Generation
}

func resourcePath(kind, ns, name string) (string, error) {
	switch kind {
	case "deployment", "deploy", "deployments":
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", ns, name), nil
	case "statefulset", "sts", "statefulsets":
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets/%s", ns, name), nil
	case "daemonset", "ds", "daemonsets":
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/daemonsets/%s", ns, name), nil
	case "pod", "po", "pods":
		return fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", ns, name), nil
	}
	return "", fmt.Errorf("unsupported resource kind %q (use deployment, statefulset, daemonset or pod)", kind)
}

func normKind(kind string) string {
	switch kind {
	case "deploy", "deployments":
		return "deployment"
	case "sts", "statefulsets":
		return "statefulset"
	case "ds", "daemonsets":
		return "daemonset"
	case "po", "pods":
		return "pod"
	}
	return kind
}

// SplitResource splits "deployment/web" into kind and name.
func SplitResource(res string) (kind, name string, err error) {
	kind, name, ok := strings.Cut(res, "/")
	if !ok || kind == "" || name == "" {
		return "", "", fmt.Errorf("resource %q must look like deployment/web", res)
	}
	return normKind(kind), name, nil
}

// GetWorkload reads a workload and normalises its status.
func (c *Client) GetWorkload(ctx context.Context, ns, resource string) (*Workload, error) {
	kind, name, err := SplitResource(resource)
	if err != nil {
		return nil, err
	}
	if ns == "" {
		ns = c.namespace
	}
	p, err := resourcePath(kind, ns, name)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := c.do(ctx, http.MethodGet, p, nil, "", nil, &obj); err != nil {
		return nil, err
	}
	return parseWorkload(kind, obj), nil
}

func parseWorkload(kind string, obj map[string]any) *Workload {
	w := &Workload{Kind: kind}
	meta, _ := obj["metadata"].(map[string]any)
	spec, _ := obj["spec"].(map[string]any)
	status, _ := obj["status"].(map[string]any)
	w.Name, _ = meta["name"].(string)
	w.Namespace, _ = meta["namespace"].(string)
	w.UID, _ = meta["uid"].(string)
	w.Generation = int64(num(meta["generation"]))
	w.ObservedGeneration = int64(num(status["observedGeneration"]))
	if kind == "pod" {
		w.Phase, _ = status["phase"].(string)
		cs, _ := status["containerStatuses"].([]any)
		w.Desired = len(cs)
		for _, x := range cs {
			m, _ := x.(map[string]any)
			if r, _ := m["ready"].(bool); r {
				w.Ready++
			}
			if img, ok := m["image"].(string); ok {
				w.Images = append(w.Images, img)
			}
		}
		if w.Desired == 0 {
			containers, _ := spec["containers"].([]any)
			w.Desired = len(containers)
		}
		return w
	}
	if kind == "daemonset" {
		w.Desired = int(num(status["desiredNumberScheduled"]))
		w.Ready = int(num(status["numberReady"]))
		w.Updated = int(num(status["updatedNumberScheduled"]))
		w.Available = int(num(status["numberAvailable"]))
	} else {
		if r, ok := spec["replicas"]; ok {
			w.Desired = int(num(r))
		} else {
			w.Desired = 1
		}
		w.Ready = int(num(status["readyReplicas"]))
		w.Updated = int(num(status["updatedReplicas"]))
		w.Available = int(num(status["availableReplicas"]))
	}
	if sel, ok := spec["selector"].(map[string]any); ok {
		if ml, ok := sel["matchLabels"].(map[string]any); ok {
			w.Selector = map[string]string{}
			for k, v := range ml {
				w.Selector[k], _ = v.(string)
			}
		}
	}
	if tpl, ok := spec["template"].(map[string]any); ok {
		if ps, ok := tpl["spec"].(map[string]any); ok {
			containers, _ := ps["containers"].([]any)
			for _, x := range containers {
				m, _ := x.(map[string]any)
				if img, ok := m["image"].(string); ok {
					w.Images = append(w.Images, img)
				}
			}
		}
	}
	if conds, ok := status["conditions"].([]any); ok {
		for _, x := range conds {
			m, _ := x.(map[string]any)
			if t, _ := m["type"].(string); t == "Progressing" || t == "Available" {
				if st, _ := m["status"].(string); st != "True" {
					w.Message, _ = m["message"].(string)
				}
			}
		}
	}
	return w
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

// Scale sets the replica count on a deployment or statefulset.
func (c *Client) Scale(ctx context.Context, ns, resource string, replicas int) error {
	kind, name, err := SplitResource(resource)
	if err != nil {
		return err
	}
	if kind == "pod" || kind == "daemonset" {
		return fmt.Errorf("cannot scale a %s", kind)
	}
	if ns == "" {
		ns = c.namespace
	}
	p, _ := resourcePath(kind, ns, name)
	body, _ := json.Marshal(map[string]any{"spec": map[string]any{"replicas": replicas}})
	return c.do(ctx, http.MethodPatch, p, nil, "application/merge-patch+json", body, nil)
}

// RolloutRestart bumps a pod template annotation so the controller rolls
// new pods, exactly like kubectl rollout restart.
func (c *Client) RolloutRestart(ctx context.Context, ns, resource string) error {
	kind, name, err := SplitResource(resource)
	if err != nil {
		return err
	}
	if ns == "" {
		ns = c.namespace
	}
	if kind == "pod" {
		p, _ := resourcePath(kind, ns, name)
		return c.do(ctx, http.MethodDelete, p, nil, "", nil, nil)
	}
	p, _ := resourcePath(kind, ns, name)
	body, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{
		"greenroom.dev/restartedAt": time.Now().UTC().Format(time.RFC3339),
	}}}}})
	return c.do(ctx, http.MethodPatch, p, nil, "application/merge-patch+json", body, nil)
}

// Pod is a pod summary from a list.
type Pod struct {
	Name      string
	Namespace string
	Phase     string
	Ready     bool
	UID       string
	Created   string
}

// Pods lists pods matching a label selector.
func (c *Client) Pods(ctx context.Context, ns string, selector map[string]string) ([]Pod, error) {
	if ns == "" {
		ns = c.namespace
	}
	var parts []string
	for k, v := range selector {
		parts = append(parts, k+"="+v)
	}
	q := url.Values{}
	if len(parts) > 0 {
		q.Set("labelSelector", strings.Join(parts, ","))
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s/pods", ns), q, "", nil, &list); err != nil {
		return nil, err
	}
	var out []Pod
	for _, item := range list.Items {
		w := parseWorkload("pod", item)
		meta, _ := item["metadata"].(map[string]any)
		created, _ := meta["creationTimestamp"].(string)
		out = append(out, Pod{Name: w.Name, Namespace: w.Namespace, Phase: w.Phase, Ready: w.Healthy(), UID: w.UID, Created: created})
	}
	return out, nil
}

// FirstReadyPod resolves a workload to one of its ready pods.
func (c *Client) FirstReadyPod(ctx context.Context, ns, resource string) (string, error) {
	kind, name, err := SplitResource(resource)
	if err != nil {
		return "", err
	}
	if kind == "pod" {
		return name, nil
	}
	w, err := c.GetWorkload(ctx, ns, resource)
	if err != nil {
		return "", err
	}
	pods, err := c.Pods(ctx, ns, w.Selector)
	if err != nil {
		return "", err
	}
	for _, p := range pods {
		if p.Ready {
			return p.Name, nil
		}
	}
	for _, p := range pods {
		if p.Phase == "Running" {
			return p.Name, nil
		}
	}
	return "", fmt.Errorf("no running pod for %s", resource)
}

// WaitReady polls until the workload is healthy.
func (c *Client) WaitReady(ctx context.Context, ns, resource string, timeout time.Duration) (*Workload, error) {
	deadline := time.Now().Add(timeout)
	for {
		w, err := c.GetWorkload(ctx, ns, resource)
		if err != nil {
			return nil, err
		}
		if w.Healthy() {
			return w, nil
		}
		if time.Now().After(deadline) {
			return w, fmt.Errorf("%s not ready after %s (%d/%d ready)", resource, timeout, w.Ready, w.Desired)
		}
		select {
		case <-ctx.Done():
			return w, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Run executes a command in a pod over the exec WebSocket endpoint.
func (c *Client) Run(ctx context.Context, spec targets.RunSpec, sink targets.LineSink) (targets.Result, error) {
	pod := spec.Pod
	ns := spec.Namespace
	if ns == "" {
		ns = c.namespace
	}
	if pod == "" {
		return targets.Result{}, errors.New("kubernetes exec needs a pod (or a resource to resolve one)")
	}
	if strings.Contains(pod, "/") {
		var err error
		pod, err = c.FirstReadyPod(ctx, ns, pod)
		if err != nil {
			return targets.Result{}, err
		}
	}
	ctx, cancel := targets.Deadline(ctx, spec, 10*time.Minute)
	defer cancel()
	start := time.Now()
	argv := spec.Argv
	var stdin []byte
	if len(argv) == 0 {
		argv = []string{targets.ShellFor(spec), "-s"}
		stdin = []byte(spec.Script)
	} else if spec.Stdin != nil {
		b, err := io.ReadAll(spec.Stdin)
		if err != nil {
			return targets.Result{}, err
		}
		stdin = b
	}
	if len(spec.Env) > 0 {
		argv = append([]string{"env"}, append(envList(spec.Env), argv...)...)
	}
	col := targets.NewCollector(sink, 0)
	code, err := c.exec(ctx, ns, pod, spec.Container, argv, stdin, col)
	col.Flush()
	return targets.Result{ExitCode: code, Stdout: col.Stdout(), Stderr: col.Stderr(), Duration: time.Since(start)}, err
}

func envList(env map[string]string) []string {
	var out []string
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

const (
	protoV5 = "v5.channel.k8s.io"
	protoV4 = "v4.channel.k8s.io"
)

func (c *Client) exec(ctx context.Context, ns, pod, container string, argv []string, stdin []byte, col *targets.Collector) (int, error) {
	q := url.Values{}
	q.Set("stdout", "true")
	q.Set("stderr", "true")
	if container != "" {
		q.Set("container", container)
	}
	hasStdin := len(stdin) > 0
	// Probe the negotiated protocol first: with v4 there is no way to close
	// stdin, so short inputs are inlined through a heredoc instead.
	if hasStdin {
		q.Set("stdin", "true")
	}
	for _, a := range argv {
		q.Add("command", a)
	}
	u := strings.Replace(c.server, "http", "ws", 1) + fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/exec?", ns, pod) + q.Encode()
	hdr := http.Header{}
	auth, err := c.authHeader(ctx)
	if err != nil {
		return -1, err
	}
	if auth != "" {
		hdr.Set("Authorization", auth)
	}
	conn, _, err := ws.Dial(ctx, u, hdr, c.tlsCfg, []string{protoV5, protoV4})
	if err != nil {
		return -1, fmt.Errorf("pod exec: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if hasStdin && conn.Subprotocol != protoV5 {
		conn.Close()
		if len(stdin) > 60*1024 {
			return -1, fmt.Errorf("pod exec: the API server negotiated %s which cannot close stdin; inputs over 60 KB need Kubernetes 1.30+ (v5.channel.k8s.io)", conn.Subprotocol)
		}
		// Re-run with the input inlined as a heredoc and no stdin channel.
		script := targets.Heredoc(targets.ShellJoin(argv), string(stdin))
		return c.exec(ctx, ns, pod, container, []string{"sh", "-c", script}, nil, col)
	}
	if hasStdin {
		go func() {
			const chunk = 32 * 1024
			for off := 0; off < len(stdin); off += chunk {
				end := off + chunk
				if end > len(stdin) {
					end = len(stdin)
				}
				if err := conn.WriteMessage(ws.OpBinary, append([]byte{0}, stdin[off:end]...)); err != nil {
					return
				}
			}
			// v5 close frame for channel 0 (stdin).
			conn.WriteMessage(ws.OpBinary, []byte{255, 0})
		}()
	}
	exit := 0
	var execErr error
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			return -1, fmt.Errorf("pod exec stream: %w", err)
		}
		if len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case 1:
			col.Write("stdout", msg[1:])
		case 2:
			col.Write("stderr", msg[1:])
		case 3:
			code, err := parseExecStatus(msg[1:])
			exit = code
			execErr = err
		}
	}
	return exit, execErr
}

// parseExecStatus decodes the channel 3 status object. A non-zero exit
// code is reported through the code, anything else through the error.
func parseExecStatus(b []byte) (int, error) {
	var st struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
		Details struct {
			Causes []struct {
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"causes"`
		} `json:"details"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return -1, fmt.Errorf("pod exec status: %s", strings.TrimSpace(string(b)))
	}
	if st.Status == "Success" {
		return 0, nil
	}
	for _, c := range st.Details.Causes {
		if c.Reason == "ExitCode" {
			var code int
			fmt.Sscanf(c.Message, "%d", &code)
			return code, nil
		}
	}
	return -1, fmt.Errorf("pod exec: %s", st.Message)
}

// CopyTo writes a file into a pod by piping a tar through exec.
func (c *Client) CopyTo(ctx context.Context, spec targets.RunSpec, dest string, content []byte, mode int64) error {
	var buf bytes.Buffer
	if err := writeTar(&buf, filepath.Base(dest), content, mode); err != nil {
		return err
	}
	dir := strings.TrimSuffix(dest, "/"+filepath.Base(dest))
	if dir == "" || dir == dest {
		dir = "/"
	}
	spec.Argv = []string{"tar", "xf", "-", "-C", dir}
	spec.Stdin = bytes.NewReader(buf.Bytes())
	spec.StdinLen = int64(buf.Len())
	res, err := c.Run(ctx, spec, nil)
	if err != nil {
		return err
	}
	return res.Error()
}

func writeTar(w io.Writer, name string, content []byte, mode int64) error {
	if mode == 0 {
		mode = 0o644
	}
	tw := newTarWriter(w)
	if err := tw.WriteHeader(name, mode, int64(len(content))); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	return tw.Close()
}
