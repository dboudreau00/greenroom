// Package config defines the on-disk shape of a Greenroom workspace: the
// workspace file, one environment file per demo environment, and the target
// and dependency blocks inside them. Everything is plain JSON so the files
// can be diffed, reviewed and versioned next to the seed data they describe.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Duration is a time.Duration that marshals as a Go duration string
// ("30s", "5m", "168h") so config files stay readable.
type Duration time.Duration

// MarshalJSON writes the duration in Go's string form.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts either a duration string or a number of seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("bad duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duration must be a string like \"30s\" or a number of seconds")
	}
	*d = Duration(time.Duration(n * float64(time.Second)))
	return nil
}

// D returns the underlying time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Or returns the duration, or def when it is zero.
func (d Duration) Or(def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return time.Duration(d)
}

// Workspace is the top level greenroom.json file.
type Workspace struct {
	Listen          string   `json:"listen,omitempty"`
	StateDir        string   `json:"stateDir,omitempty"`
	EnvironmentsDir string   `json:"environmentsDir,omitempty"`
	TemplatesDir    string   `json:"templatesDir,omitempty"`
	ProbeInterval   Duration `json:"probeInterval,omitempty"`
	SampleRetention Duration `json:"sampleRetention,omitempty"`
	HealCooldown    Duration `json:"healCooldown,omitempty"`
	// Token protects the portal and API. GREENROOM_TOKEN overrides it.
	Token string `json:"token,omitempty"`
	// PublicURL is used in links and in the login page, optional.
	PublicURL string `json:"publicUrl,omitempty"`
	// LinkFetch tunes the client that fetches brand kits and assets from
	// links people paste.
	LinkFetch LinkFetch `json:"linkFetch,omitempty"`

	// BaseDir is the directory the workspace file lives in (not serialised).
	BaseDir string `json:"-"`
	// Path is where the workspace file was read from (not serialised).
	Path string `json:"-"`
}

// LinkFetch configures fetching from links. By default only public
// addresses on ports 80 and 443 can be reached.
type LinkFetch struct {
	// AllowPrivate permits loopback, private and link-local addresses and
	// any port, for brand sites hosted inside the network.
	AllowPrivate bool     `json:"allowPrivate,omitempty"`
	Timeout      Duration `json:"timeout,omitempty"`
}

// BrandDefaults is the house brand a reset uses when no prospect brand kit
// is chosen.
type BrandDefaults struct {
	Name    string `json:"name,omitempty"`
	Tagline string `json:"tagline,omitempty"`
	Domain  string `json:"domain,omitempty"`
	Primary string `json:"primary,omitempty"`
	Accent  string `json:"accent,omitempty"`
}

// Pin names the persona and seed version an environment is expected to run.
type Pin struct {
	Persona string `json:"persona"`
	Version int    `json:"version,omitempty"`
}

// TemplateRef records the template an environment was created from and the
// product modules it was built with. It is informational: the environment
// file is the source of truth once written.
type TemplateRef struct {
	Name        string   `json:"name"`
	Version     int      `json:"version,omitempty"`
	ProductType string   `json:"productType,omitempty"`
	Modules     []string `json:"modules,omitempty"`
}

// Environment is one environments/<name>/environment.json file.
type Environment struct {
	Name         string            `json:"name"`
	Title        string            `json:"title,omitempty"`
	Owner        string            `json:"owner,omitempty"`
	Description  string            `json:"description,omitempty"`
	StaleAfter   Duration          `json:"staleAfter,omitempty"`
	Pin          Pin               `json:"pin"`
	Targets      map[string]Target `json:"targets,omitempty"`
	Dependencies []Dependency      `json:"dependencies"`
	// Env holds extra environment variables made available to steps and DSNs.
	Env map[string]string `json:"env,omitempty"`
	// Template is set when the environment was created with greenroom new.
	Template *TemplateRef `json:"template,omitempty"`
	// Brand holds the house brand used when a reset has no brand kit.
	Brand *BrandDefaults `json:"brand,omitempty"`

	Dir  string `json:"-"`
	Path string `json:"-"`
}

// Target kinds.
const (
	TargetDocker     = "docker"
	TargetKubernetes = "kubernetes"
	TargetAWS        = "aws"
	TargetSSH        = "ssh"
	TargetLocal      = "local"
)

// Target is somewhere Greenroom can run commands or ask about resources.
type Target struct {
	Kind string `json:"kind"`

	// Docker: unix:///var/run/docker.sock, npipe:////./pipe/docker_engine,
	// tcp://host:2375 or https://host:2376 (with cert files below).
	Host     string `json:"host,omitempty"`
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
	CAFile   string `json:"caFile,omitempty"`

	// Kubernetes: "auto" shells out to kubectl for a JSON kubeconfig,
	// "in-cluster" uses the mounted service account, otherwise a path to a
	// kubeconfig in JSON form (kubectl config view --raw -o json).
	Kubeconfig   string   `json:"kubeconfig,omitempty"`
	Context      string   `json:"context,omitempty"`
	Namespace    string   `json:"namespace,omitempty"`
	Server       string   `json:"server,omitempty"`
	Token        string   `json:"token,omitempty"`
	TokenFile    string   `json:"tokenFile,omitempty"`
	TokenCommand []string `json:"tokenCommand,omitempty"`
	Insecure     bool     `json:"insecure,omitempty"`

	// AWS
	Region  string `json:"region,omitempty"`
	Profile string `json:"profile,omitempty"`

	// SSH (shells out to the ssh binary)
	User         string   `json:"user,omitempty"`
	Port         int      `json:"port,omitempty"`
	IdentityFile string   `json:"identityFile,omitempty"`
	Args         []string `json:"args,omitempty"`
	Shell        string   `json:"shell,omitempty"`
}

// Dependency kinds.
const (
	KindTCP        = "tcp"
	KindTLS        = "tls"
	KindHTTP       = "http"
	KindPostgres   = "postgres"
	KindMySQL      = "mysql"
	KindDocker     = "docker"
	KindKubernetes = "kubernetes"
	KindEC2        = "ec2"
	KindCommand    = "command"
)

// Via routes SQL through an executor instead of a direct connection, which is
// how a database that only listens behind a bastion is reached.
type Via struct {
	Target     string `json:"target"`
	Container  string `json:"container,omitempty"`
	Pod        string `json:"pod,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	Tool       string `json:"tool,omitempty"`
	DSN        string `json:"dsn,omitempty"`
}

// Heal is the action taken when a dependency is unhealthy.
type Heal struct {
	// start, restart, reboot, scale, rollout-restart, command
	Action   string   `json:"action"`
	Command  []string `json:"command,omitempty"`
	Script   string   `json:"script,omitempty"`
	Target   string   `json:"target,omitempty"`
	Replicas int      `json:"replicas,omitempty"`
}

// Reset is how a dependency is returned to its known-good baseline before a
// seed set is applied.
type Reset struct {
	// postgres: template | schema | script; mysql: schema | script
	// docker: snapshot | restart | recreate; kubernetes: rollout-restart
	// ec2: reboot; command: script
	Strategy string `json:"strategy"`
	Template string `json:"template,omitempty"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	Script   string `json:"script,omitempty"`
	Command  []string `json:"command,omitempty"`
}

// Snapshot is how a known-good baseline is captured.
type Snapshot struct {
	// postgres: template; docker: commit
	Strategy string `json:"strategy"`
	Template string `json:"template,omitempty"`
	Tag      string `json:"tag,omitempty"`
}

// DriftCheck compares a live value with the value the seed set left behind.
type DriftCheck struct {
	Name      string   `json:"name"`
	Query     string   `json:"query,omitempty"`
	Command   []string `json:"command,omitempty"`
	Expect    *float64 `json:"expect,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Tolerance float64  `json:"tolerance,omitempty"`
	// Personas limits the check to seed sets of these personas, for data
	// that only some seed sets create. Empty means every seed set.
	Personas []string `json:"personas,omitempty"`
}

// Applies reports whether the check is evaluated when persona's seed set is
// the one applied. An empty persona (never reset) only runs unscoped checks.
func (dc DriftCheck) Applies(persona string) bool {
	if len(dc.Personas) == 0 {
		return true
	}
	for _, p := range dc.Personas {
		if p == persona {
			return true
		}
	}
	return false
}

// Dependency is one thing a demo needs to be up and correct.
type Dependency struct {
	Name     string   `json:"name"`
	Title    string   `json:"title,omitempty"`
	Kind     string   `json:"kind"`
	Target   string   `json:"target,omitempty"`
	Critical bool     `json:"critical,omitempty"`
	Timeout  Duration `json:"timeout,omitempty"`
	Heal     *Heal    `json:"heal,omitempty"`
	AutoHeal bool     `json:"autoHeal,omitempty"`
	Reset    *Reset   `json:"reset,omitempty"`
	Snapshot *Snapshot `json:"snapshot,omitempty"`
	Drift    []DriftCheck `json:"drift,omitempty"`
	Tags     []string `json:"tags,omitempty"`

	// tcp, tls
	Address  string `json:"address,omitempty"`
	WarnDays int    `json:"warnDays,omitempty"`

	// http
	URL          string            `json:"url,omitempty"`
	Method       string            `json:"method,omitempty"`
	ExpectStatus int               `json:"expectStatus,omitempty"`
	ExpectBody   string            `json:"expectBody,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Insecure     bool              `json:"insecure,omitempty"`

	// postgres, mysql
	DSN    string `json:"dsn,omitempty"`
	DSNEnv string `json:"dsnEnv,omitempty"`
	Via    *Via   `json:"via,omitempty"`

	// docker
	Container string `json:"container,omitempty"`

	// kubernetes: "deployment/web", "statefulset/db", "pod/foo"
	Resource  string `json:"resource,omitempty"`
	Namespace string `json:"namespace,omitempty"`

	// ec2
	InstanceID string `json:"instanceId,omitempty"`

	// command
	Command []string `json:"command,omitempty"`
	Script  string   `json:"script,omitempty"`
}

// DisplayTitle returns the title or the name.
func (d Dependency) DisplayTitle() string {
	if d.Title != "" {
		return d.Title
	}
	return d.Name
}

// DisplayTitle returns the title or the name.
func (e Environment) DisplayTitle() string {
	if e.Title != "" {
		return e.Title
	}
	return e.Name
}

// Dependency looks a dependency up by name.
func (e *Environment) Dependency(name string) (*Dependency, bool) {
	for i := range e.Dependencies {
		if e.Dependencies[i].Name == name {
			return &e.Dependencies[i], true
		}
	}
	return nil, false
}

// Target looks a target up by name.
func (e *Environment) Target(name string) (Target, bool) {
	t, ok := e.Targets[name]
	return t, ok
}

// Defaults applied when the workspace file leaves a field empty.
const (
	DefaultListen          = "127.0.0.1:8470"
	DefaultProbeInterval   = 60 * time.Second
	DefaultSampleRetention = 30 * 24 * time.Hour
	DefaultHealCooldown    = 5 * time.Minute
	DefaultStaleAfter      = 7 * 24 * time.Hour
	DefaultTimeout         = 15 * time.Second
	WorkspaceFile          = "greenroom.json"
	EnvironmentFile        = "environment.json"
	SeedsDirName           = "seeds"
	SeedFile               = "seed.json"
)

// LoadWorkspace reads greenroom.json from dir. A missing file yields defaults.
func LoadWorkspace(dir string) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	ws := &Workspace{BaseDir: abs}
	path := filepath.Join(abs, WorkspaceFile)
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(stripComments(b), ws); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		ws.Path = path
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ws.BaseDir = abs
	ws.applyDefaults()
	return ws, nil
}

func (ws *Workspace) applyDefaults() {
	if ws.Listen == "" {
		ws.Listen = DefaultListen
	}
	if ws.StateDir == "" {
		ws.StateDir = "state"
	}
	if ws.EnvironmentsDir == "" {
		ws.EnvironmentsDir = "environments"
	}
	if ws.TemplatesDir == "" {
		ws.TemplatesDir = "templates"
	}
	if !filepath.IsAbs(ws.StateDir) {
		ws.StateDir = filepath.Join(ws.BaseDir, ws.StateDir)
	}
	if !filepath.IsAbs(ws.EnvironmentsDir) {
		ws.EnvironmentsDir = filepath.Join(ws.BaseDir, ws.EnvironmentsDir)
	}
	if !filepath.IsAbs(ws.TemplatesDir) {
		ws.TemplatesDir = filepath.Join(ws.BaseDir, ws.TemplatesDir)
	}
	if ws.ProbeInterval == 0 {
		ws.ProbeInterval = Duration(DefaultProbeInterval)
	}
	if ws.SampleRetention == 0 {
		ws.SampleRetention = Duration(DefaultSampleRetention)
	}
	if ws.HealCooldown == 0 {
		ws.HealCooldown = Duration(DefaultHealCooldown)
	}
	if v := os.Getenv("GREENROOM_TOKEN"); v != "" {
		ws.Token = v
	}
	if v := os.Getenv("GREENROOM_LISTEN"); v != "" {
		ws.Listen = v
	}
}

// LoadEnvironment reads one environment directory.
func LoadEnvironment(dir string) (*Environment, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(abs, EnvironmentFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := &Environment{}
	if err := json.Unmarshal(stripComments(b), env); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	env.Dir = abs
	env.Path = path
	if env.Name == "" {
		env.Name = filepath.Base(abs)
	}
	env.expand()
	if err := env.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return env, nil
}

// LoadEnvironments reads every environment under dir, sorted by name.
func LoadEnvironments(dir string) ([]*Environment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var envs []*Environment
	var errs []error
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		sub := filepath.Join(dir, ent.Name())
		if _, err := os.Stat(filepath.Join(sub, EnvironmentFile)); err != nil {
			continue
		}
		env, err := LoadEnvironment(sub)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		envs = append(envs, env)
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].Name < envs[j].Name })
	return envs, errors.Join(errs...)
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidName reports whether s is safe to use as an environment, dependency,
// persona or target name (it appears in paths and URLs).
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Validate checks the environment for structural errors.
func (e *Environment) Validate() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !ValidName(e.Name) {
		fail("environment name %q must match %s", e.Name, nameRe)
	}
	if len(e.Dependencies) == 0 {
		fail("environment %q declares no dependencies", e.Name)
	}
	for name, t := range e.Targets {
		if !ValidName(name) {
			fail("target name %q must match %s", name, nameRe)
		}
		switch t.Kind {
		case TargetDocker, TargetKubernetes, TargetAWS, TargetSSH, TargetLocal:
		default:
			fail("target %q: unknown kind %q", name, t.Kind)
		}
		if t.Kind == TargetSSH && t.Host == "" {
			fail("target %q: ssh target needs host", name)
		}
		if t.Kind == TargetAWS && t.Region == "" && os.Getenv("AWS_REGION") == "" && os.Getenv("AWS_DEFAULT_REGION") == "" {
			fail("target %q: aws target needs region (or AWS_REGION)", name)
		}
	}
	seen := map[string]bool{}
	for i, d := range e.Dependencies {
		where := fmt.Sprintf("dependency[%d]", i)
		if d.Name != "" {
			where = "dependency " + d.Name
		}
		if !ValidName(d.Name) {
			fail("%s: name %q must match %s", where, d.Name, nameRe)
		}
		if seen[d.Name] {
			fail("%s: duplicate name", where)
		}
		seen[d.Name] = true
		if d.Target != "" {
			if _, ok := e.Targets[d.Target]; !ok {
				fail("%s: unknown target %q", where, d.Target)
			}
		}
		switch d.Kind {
		case KindTCP:
			if d.Address == "" {
				fail("%s: tcp needs address", where)
			}
		case KindTLS:
			if d.Address == "" {
				fail("%s: tls needs address", where)
			}
		case KindHTTP:
			if d.URL == "" {
				fail("%s: http needs url", where)
			}
		case KindPostgres, KindMySQL:
			if d.DSN == "" && d.DSNEnv == "" && d.Via == nil {
				fail("%s: %s needs dsn, dsnEnv or via", where, d.Kind)
			}
			if d.Via != nil {
				if _, ok := e.Targets[d.Via.Target]; !ok {
					fail("%s: via.target %q is not a declared target", where, d.Via.Target)
				}
			}
		case KindDocker:
			if d.Container == "" {
				fail("%s: docker needs container", where)
			}
			if d.Target == "" {
				fail("%s: docker needs target", where)
			}
		case KindKubernetes:
			if d.Resource == "" || !strings.Contains(d.Resource, "/") {
				fail("%s: kubernetes needs resource like deployment/web", where)
			}
			if d.Target == "" {
				fail("%s: kubernetes needs target", where)
			}
		case KindEC2:
			if d.InstanceID == "" {
				fail("%s: ec2 needs instanceId", where)
			}
			if d.Target == "" {
				fail("%s: ec2 needs target", where)
			}
		case KindCommand:
			if len(d.Command) == 0 && d.Script == "" {
				fail("%s: command needs command or script", where)
			}
		default:
			fail("%s: unknown kind %q", where, d.Kind)
		}
		if d.Heal != nil {
			switch d.Heal.Action {
			case "start", "restart", "reboot", "scale", "rollout-restart", "command":
			default:
				fail("%s: heal.action %q is not one of start, restart, reboot, scale, rollout-restart, command", where, d.Heal.Action)
			}
			if d.Heal.Action == "command" && len(d.Heal.Command) == 0 && d.Heal.Script == "" {
				fail("%s: heal.action command needs command or script", where)
			}
		}
		if d.AutoHeal && d.Heal == nil {
			fail("%s: autoHeal needs a heal block", where)
		}
		for j, dc := range d.Drift {
			if dc.Query == "" && len(dc.Command) == 0 {
				fail("%s: drift[%d] needs query or command", where, j)
			}
			if dc.Name == "" {
				fail("%s: drift[%d] needs a name", where, j)
			}
			for _, p := range dc.Personas {
				if !ValidName(p) {
					fail("%s: drift[%d]: persona %q is not a valid name", where, j, p)
				}
			}
		}
	}
	if e.Brand != nil {
		for _, c := range []string{e.Brand.Primary, e.Brand.Accent} {
			if c != "" && !colorRe.MatchString(c) {
				fail("brand: color %q must be #rrggbb", c)
			}
		}
	}
	if e.Pin.Persona != "" && !ValidName(e.Pin.Persona) {
		fail("pin.persona %q must match %s", e.Pin.Persona, nameRe)
	}
	return errors.Join(errs...)
}

// expand substitutes ${VAR} and ${VAR:-default} in the string fields that
// commonly carry secrets or host names.
func (e *Environment) expand() {
	lookup := func(k string) string {
		if v, ok := e.Env[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
	x := func(s string) string { return Expand(s, lookup) }
	for name, t := range e.Targets {
		t.Host = x(t.Host)
		t.Server = x(t.Server)
		t.Token = x(t.Token)
		t.TokenFile = x(t.TokenFile)
		t.Kubeconfig = x(t.Kubeconfig)
		t.IdentityFile = x(t.IdentityFile)
		t.Region = x(t.Region)
		t.Profile = x(t.Profile)
		t.User = x(t.User)
		e.Targets[name] = t
	}
	for i := range e.Dependencies {
		d := &e.Dependencies[i]
		d.Address = x(d.Address)
		d.URL = x(d.URL)
		d.DSN = x(d.DSN)
		d.Container = x(d.Container)
		d.Resource = x(d.Resource)
		d.Namespace = x(d.Namespace)
		d.InstanceID = x(d.InstanceID)
		for k, v := range d.Headers {
			d.Headers[k] = x(v)
		}
		if d.Via != nil {
			d.Via.DSN = x(d.Via.DSN)
			d.Via.Container = x(d.Via.Container)
			d.Via.Pod = x(d.Via.Pod)
			d.Via.InstanceID = x(d.Via.InstanceID)
		}
		if d.DSN == "" && d.DSNEnv != "" {
			d.DSN = lookup(d.DSNEnv)
		}
	}
}

// Expand replaces ${VAR} and ${VAR:-default} using lookup. A missing variable
// with no default expands to the empty string. $$ yields a literal $.
func Expand(s string, lookup func(string) string) string {
	return expandVars(s, func(k string) (string, bool) { return lookup(k), true })
}

// ExpandKnown is Expand for text that may hold the variables of another
// program, such as a sh -c script: ${VAR} is replaced only when lookup
// knows VAR or a default is given; anything else stays as written.
func ExpandKnown(s string, lookup func(string) (string, bool)) string {
	return expandVars(s, lookup)
}

func expandVars(s string, lookup func(string) (string, bool)) string {
	if !strings.Contains(s, "$") {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '$' {
			out.WriteByte(c)
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			out.WriteByte('$')
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' {
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				out.WriteString(s[i:])
				return out.String()
			}
			body := s[i+2 : i+end]
			name, def, hasDef := strings.Cut(body, ":-")
			v, known := lookup(name)
			if !known && !hasDef {
				out.WriteString(s[i : i+end+1])
				i += end
				continue
			}
			if v == "" && hasDef {
				v = def
			}
			out.WriteString(v)
			i += end
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

// stripComments removes // line comments and /* */ block comments that sit
// outside string literals, so config files can carry notes.
func stripComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(b) {
				out = append(out, b[i+1])
				i++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}

// StripComments is exported for the seeds package.
func StripComments(b []byte) []byte { return stripComments(b) }

// WriteJSON writes v to path atomically with a trailing newline. The temp
// file has a unique name in the same directory, so two writers of the same
// path never share one, and it is removed when anything fails.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
