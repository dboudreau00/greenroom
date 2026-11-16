// Package templates turns a pre-rolled environment template into a ready
// environment directory. A template describes one product type (a SaaS web
// app on Docker, a warehouse behind a bastion, microservices on Kubernetes):
// the base dependencies and seed steps every demo of that product needs, and
// a list of product modules. Each module adds its own dependencies, drift
// checks, seed steps and verify checks, so the environment matches exactly
// the modules a call is going to show. Personas pick a subset of modules and
// become one seed set each.
//
// Layout of a template directory:
//
//	template.json        manifest: params, environment, seed, modules, personas
//	files/               copied into the environment directory (compose, web, README)
//	seed/                files referenced by the base seed steps
//	modules/<name>/      files referenced by the steps of one module
package templates

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dboudreau00/greenroom/internal/config"
)

//go:embed all:builtin
var builtin embed.FS

// ManifestFile is the template manifest inside a template directory.
const ManifestFile = "template.json"

// Param is a value the person creating the environment can set.
type Param struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
	// Pattern is the regular expression a value must match. Values end up in
	// YAML, SQL and shell files, so the default only allows a safe charset.
	Pattern  string `json:"pattern,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// Seed is the part of every seed set that does not depend on modules.
type Seed struct {
	// Steps run before the module steps, After runs once they are done.
	Steps        []map[string]any `json:"steps,omitempty"`
	After        []map[string]any `json:"after,omitempty"`
	Verify       []map[string]any `json:"verify,omitempty"`
	Dependencies []string         `json:"dependencies,omitempty"`
}

// Module is one product module a demo can show.
type Module struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     bool     `json:"default,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	// Dependencies are appended to the environment's dependency list.
	Dependencies []map[string]any `json:"dependencies,omitempty"`
	// Drift adds drift checks to existing dependencies, keyed by name.
	Drift  map[string][]map[string]any `json:"drift,omitempty"`
	Steps  []map[string]any            `json:"steps,omitempty"`
	Verify []map[string]any            `json:"verify,omitempty"`
}

// Persona becomes one seed set. Modules lists the modules it walks through;
// "*" means every selected module.
type Persona struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Vertical    string   `json:"vertical,omitempty"`
	Modules     []string `json:"modules"`
}

// Template is a loaded template.
type Template struct {
	Name        string         `json:"name"`
	Version     int            `json:"version,omitempty"`
	Title       string         `json:"title,omitempty"`
	ProductType string         `json:"productType,omitempty"`
	Description string         `json:"description,omitempty"`
	Params      []Param        `json:"params,omitempty"`
	Environment map[string]any `json:"environment"`
	Seed        Seed           `json:"seed"`
	Modules     []Module       `json:"modules"`
	Personas    []Persona      `json:"personas"`

	// Source is "builtin" or "workspace".
	Source   string   `json:"-"`
	Problems []string `json:"-"`
	fsys     fs.FS
}

// DisplayTitle returns the title or the name.
func (t *Template) DisplayTitle() string {
	if t.Title != "" {
		return t.Title
	}
	return t.Name
}

// Module looks a module up by name.
func (t *Template) Module(name string) (*Module, bool) {
	for i := range t.Modules {
		if t.Modules[i].Name == name {
			return &t.Modules[i], true
		}
	}
	return nil, false
}

// Builtin lists the templates shipped inside the binary.
func Builtin() ([]*Template, error) {
	root, err := fs.Sub(builtin, "builtin")
	if err != nil {
		return nil, err
	}
	return loadAll(root, "builtin")
}

// List returns the built-in templates plus the ones under dir. A workspace
// template replaces a built-in one with the same name. dir may not exist.
func List(dir string) ([]*Template, error) {
	out, err := Builtin()
	if err != nil {
		return nil, err
	}
	var errs []error
	if dir != "" {
		if _, serr := os.Stat(dir); serr == nil {
			local, lerr := loadAll(os.DirFS(dir), "workspace")
			if lerr != nil {
				errs = append(errs, lerr)
			}
			byName := map[string]int{}
			for i, t := range out {
				byName[t.Name] = i
			}
			for _, t := range local {
				if i, ok := byName[t.Name]; ok {
					out[i] = t
				} else {
					out = append(out, t)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errors.Join(errs...)
}

// Find returns the template with the given name.
func Find(list []*Template, name string) (*Template, bool) {
	for _, t := range list {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

func loadAll(root fs.FS, source string) ([]*Template, error) {
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil, err
	}
	var out []*Template
	var errs []error
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		sub, err := fs.Sub(root, ent.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := fs.Stat(sub, ManifestFile); err != nil {
			continue
		}
		t, err := Load(sub, source)
		if err != nil {
			errs = append(errs, fmt.Errorf("template %s: %w", ent.Name(), err))
			continue
		}
		if t.Name == "" {
			t.Name = ent.Name()
		}
		if t.Name != ent.Name() {
			t.Problems = append(t.Problems, fmt.Sprintf("manifest name %q does not match directory %q", t.Name, ent.Name()))
		}
		out = append(out, t)
	}
	return out, errors.Join(errs...)
}

// Load reads one template rooted at fsys.
func Load(fsys fs.FS, source string) (*Template, error) {
	b, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return nil, err
	}
	t := &Template{}
	if err := json.Unmarshal(config.StripComments(b), t); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	t.Source, t.fsys = source, fsys
	t.Problems = append(t.Problems, t.validate()...)
	return t, nil
}

var paramNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,40}$`)

// DefaultPattern is applied to params that do not declare one.
const DefaultPattern = `^[A-Za-z0-9._:/@-]{0,200}$`

// TitlePattern limits the environment title, which reaches HTML and README
// files as well as the environment file.
var TitlePattern = regexp.MustCompile(`^[A-Za-z0-9 .,&()_+-]{1,80}$`)

// builtinParams are always available to templates.
var builtinParams = map[string]bool{"env": true, "envSlug": true, "title": true, "owner": true, "modules": true, "template": true}

func (t *Template) validate() []string {
	var problems []string
	if t.Name != "" && !config.ValidName(t.Name) {
		problems = append(problems, fmt.Sprintf("name %q is not a valid name", t.Name))
	}
	if t.Environment == nil {
		problems = append(problems, "environment block missing")
	}
	seen := map[string]bool{}
	for _, p := range t.Params {
		if !paramNameRe.MatchString(p.Name) || builtinParams[p.Name] {
			problems = append(problems, fmt.Sprintf("param %q: invalid or reserved name", p.Name))
		}
		if seen["p:"+p.Name] {
			problems = append(problems, fmt.Sprintf("param %q declared twice", p.Name))
		}
		seen["p:"+p.Name] = true
		if p.Pattern != "" {
			if _, err := regexp.Compile(p.Pattern); err != nil {
				problems = append(problems, fmt.Sprintf("param %q: bad pattern: %v", p.Name, err))
			}
		}
	}
	if len(t.Modules) == 0 {
		problems = append(problems, "no modules")
	}
	for _, m := range t.Modules {
		if !config.ValidName(m.Name) {
			problems = append(problems, fmt.Sprintf("module %q is not a valid name", m.Name))
		}
		if seen["m:"+m.Name] {
			problems = append(problems, fmt.Sprintf("module %q declared twice", m.Name))
		}
		seen["m:"+m.Name] = true
	}
	for _, m := range t.Modules {
		for _, r := range m.Requires {
			if !seen["m:"+r] {
				problems = append(problems, fmt.Sprintf("module %s requires unknown module %q", m.Name, r))
			}
		}
	}
	if len(t.Personas) == 0 {
		problems = append(problems, "no personas")
	}
	for _, p := range t.Personas {
		if !config.ValidName(p.Name) {
			problems = append(problems, fmt.Sprintf("persona %q is not a valid name", p.Name))
		}
		if seen["s:"+p.Name] {
			problems = append(problems, fmt.Sprintf("persona %q declared twice", p.Name))
		}
		seen["s:"+p.Name] = true
		if len(p.Modules) == 0 {
			problems = append(problems, fmt.Sprintf("persona %s lists no modules", p.Name))
		}
		for _, m := range p.Modules {
			if m != "*" && !seen["m:"+m] {
				problems = append(problems, fmt.Sprintf("persona %s uses unknown module %q", p.Name, m))
			}
		}
	}
	return problems
}

// Request is what the person creating an environment chose.
type Request struct {
	Name        string            `json:"name"`
	Title       string            `json:"title,omitempty"`
	Owner       string            `json:"owner,omitempty"`
	Description string            `json:"description,omitempty"`
	Modules     []string          `json:"modules,omitempty"`
	Persona     string            `json:"persona,omitempty"`
	Params      map[string]string `json:"params,omitempty"`
}

// Plan is a rendered template: the files to write and what they contain.
type Plan struct {
	Template     string            `json:"template"`
	Name         string            `json:"name"`
	Modules      []string          `json:"modules"`
	Personas     []string          `json:"personas"`
	Pin          string            `json:"pin"`
	Dependencies int               `json:"dependencies"`
	Steps        map[string]int    `json:"steps"`
	Params       map[string]string `json:"params"`
	Files        map[string][]byte `json:"-"`
}

// Paths lists the plan's files in order.
func (p *Plan) Paths() []string {
	out := make([]string, 0, len(p.Files))
	for k := range p.Files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResolveModules returns the modules a request ends up with: the required
// ones, the requested ones (or the defaults when none are requested) and
// everything they require, in template order.
func (t *Template) ResolveModules(requested []string) ([]string, error) {
	want := map[string]bool{}
	for _, m := range t.Modules {
		if m.Required || (len(requested) == 0 && m.Default) {
			want[m.Name] = true
		}
	}
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := t.Module(name); !ok {
			return nil, fmt.Errorf("template %s has no module %q", t.Name, name)
		}
		want[name] = true
	}
	for changed := true; changed; {
		changed = false
		for name := range want {
			m, _ := t.Module(name)
			for _, r := range m.Requires {
				if !want[r] {
					want[r] = true
					changed = true
				}
			}
		}
	}
	var out []string
	for _, m := range t.Modules {
		if want[m.Name] {
			out = append(out, m.Name)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("pick at least one module")
	}
	return out, nil
}

// personaModules returns the selected modules a persona walks through, in
// template order, or nil when it shares none with the selection. Required
// modules and everything its modules require come along, since their
// tables are what the persona's data refers to.
func (t *Template) personaModules(p Persona, selected []string) []string {
	all := false
	in := map[string]bool{}
	for _, m := range p.Modules {
		if m == "*" {
			all = true
		}
		in[m] = true
	}
	want := map[string]bool{}
	for _, m := range selected {
		if all || in[m] {
			want[m] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	for _, m := range t.Modules {
		if m.Required {
			want[m.Name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for name := range want {
			m, _ := t.Module(name)
			for _, r := range m.Requires {
				if !want[r] {
					want[r] = true
					changed = true
				}
			}
		}
	}
	var out []string
	for _, m := range selected {
		if want[m] {
			out = append(out, m)
		}
	}
	return out
}

// Render builds the files of a new environment without touching disk.
func (t *Template) Render(req Request) (*Plan, error) {
	if len(t.Problems) > 0 {
		return nil, fmt.Errorf("template %s cannot be used: %s", t.Name, strings.Join(t.Problems, "; "))
	}
	if !config.ValidName(req.Name) {
		return nil, fmt.Errorf("environment name %q must be lower case letters, digits, '.', '_' or '-'", req.Name)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = req.Name
	}
	if !TitlePattern.MatchString(title) {
		return nil, fmt.Errorf("title %q may only use letters, digits, spaces and . , & ( ) _ + -", title)
	}
	owner := strings.TrimSpace(req.Owner)
	if owner != "" && !TitlePattern.MatchString(owner) {
		return nil, fmt.Errorf("owner %q may only use letters, digits, spaces and . , & ( ) _ + -", owner)
	}
	modules, err := t.ResolveModules(req.Modules)
	if err != nil {
		return nil, err
	}
	var moduleTitles []string
	for _, name := range modules {
		m, _ := t.Module(name)
		moduleTitles = append(moduleTitles, firstNonEmpty(m.Title, m.Name))
	}
	values := map[string]string{
		"env": req.Name, "envSlug": strings.ReplaceAll(req.Name, ".", "-"), "title": title, "owner": owner,
		"modules": strings.Join(moduleTitles, ", "), "template": t.Name,
	}
	chosen := map[string]string{}
	declared := map[string]bool{}
	for _, p := range t.Params {
		declared[p.Name] = true
		v, given := req.Params[p.Name]
		v = strings.TrimSpace(v)
		if !given || v == "" {
			v = p.Default
		}
		if v == "" && p.Required {
			return nil, fmt.Errorf("param %s is required", p.Name)
		}
		pattern := p.Pattern
		if pattern == "" {
			pattern = DefaultPattern
		}
		if !regexp.MustCompile(pattern).MatchString(v) {
			return nil, fmt.Errorf("param %s: value %q does not match %s", p.Name, v, pattern)
		}
		if metadataHost(v) {
			return nil, fmt.Errorf("param %s: %q is a link-local or cloud metadata address; a demo environment can not point there", p.Name, v)
		}
		values[p.Name] = v
		chosen[p.Name] = v
	}
	for k := range req.Params {
		if !declared[k] {
			return nil, fmt.Errorf("template %s has no param %q", t.Name, k)
		}
	}
	r := &renderer{values: values}

	// Personas that share at least one module with the selection.
	type personaSet struct {
		p       Persona
		modules []string
	}
	var personas []personaSet
	for _, p := range t.Personas {
		if mods := t.personaModules(p, modules); len(mods) > 0 {
			personas = append(personas, personaSet{p, mods})
		}
	}
	if len(personas) == 0 {
		return nil, errors.New("no persona of this template covers the selected modules")
	}
	pin := personas[0].p.Name
	if req.Persona != "" {
		found := false
		for _, ps := range personas {
			if ps.p.Name == req.Persona {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("persona %q is not available with the selected modules", req.Persona)
		}
		pin = req.Persona
	}

	plan := &Plan{Template: t.Name, Name: req.Name, Modules: modules, Pin: pin + "@v1", Steps: map[string]int{}, Params: chosen, Files: map[string][]byte{}}

	// Environment file.
	env, err := r.tree(t.Environment)
	if err != nil {
		return nil, fmt.Errorf("environment: %w", err)
	}
	envMap, _ := env.(map[string]any)
	if envMap == nil {
		envMap = map[string]any{}
	}
	deps, _ := envMap["dependencies"].([]any)
	for _, name := range modules {
		m, _ := t.Module(name)
		for _, d := range m.Dependencies {
			rd, err := r.tree(d)
			if err != nil {
				return nil, fmt.Errorf("module %s: %w", name, err)
			}
			deps = append(deps, rd)
		}
	}
	for _, name := range modules {
		m, _ := t.Module(name)
		// A module's drift checks only apply to seed sets that create its
		// data; scope them when some personas leave the module out.
		var covering []any
		for _, ps := range personas {
			for _, pm := range ps.modules {
				if pm == name {
					covering = append(covering, ps.p.Name)
				}
			}
		}
		for depName, checks := range m.Drift {
			dep := findDep(deps, depName)
			if dep == nil {
				return nil, fmt.Errorf("module %s adds drift checks to unknown dependency %q", name, depName)
			}
			list, _ := dep["drift"].([]any)
			for _, c := range checks {
				rc, err := r.tree(c)
				if err != nil {
					return nil, fmt.Errorf("module %s: %w", name, err)
				}
				if len(covering) < len(personas) {
					rc.(map[string]any)["personas"] = covering
				}
				list = append(list, rc)
			}
			dep["drift"] = list
		}
	}
	envMap["dependencies"] = deps
	envMap["name"] = req.Name
	envMap["title"] = title
	if owner != "" {
		envMap["owner"] = owner
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		desc = fmt.Sprintf("%s with %s.", t.DisplayTitle(), strings.Join(moduleTitles, ", "))
	}
	envMap["description"] = desc
	envMap["pin"] = map[string]any{"persona": pin, "version": 1}
	envMap["template"] = config.TemplateRef{Name: t.Name, Version: t.Version, ProductType: t.ProductType, Modules: modules}
	b, err := json.MarshalIndent(envMap, "", "  ")
	if err != nil {
		return nil, err
	}
	plan.Files[config.EnvironmentFile] = append(b, '\n')
	plan.Dependencies = len(deps)

	// One seed set per persona.
	for _, ps := range personas {
		dir := path.Join(config.SeedsDirName, ps.p.Name, "v1")
		manifest := map[string]any{
			"persona": ps.p.Name, "version": 1,
			"title":       firstNonEmpty(ps.p.Title, ps.p.Name),
			"description": ps.p.Description,
			"author":      "greenroom template " + t.Name,
			"tags":        ps.modules,
		}
		if ps.p.Vertical != "" {
			manifest["vertical"] = ps.p.Vertical
		}
		if len(t.Seed.Dependencies) > 0 {
			manifest["dependencies"] = t.Seed.Dependencies
		}
		var steps, verify []any
		names := map[string]bool{}
		add := func(list []map[string]any, prefix, rename string, into *[]any, isStep bool) error {
			for _, raw := range list {
				item, err := r.tree(raw)
				if err != nil {
					return err
				}
				m := item.(map[string]any)
				for _, key := range []string{"file", "bodyFile"} {
					ref, _ := m[key].(string)
					if ref == "" {
						continue
					}
					dest, err := t.copyFile(r, plan, dir, prefix, rename, ref)
					if err != nil {
						return err
					}
					m[key] = dest
				}
				if isStep {
					n, _ := m["name"].(string)
					if names[n] {
						return fmt.Errorf("persona %s: step name %q used twice", ps.p.Name, n)
					}
					names[n] = true
				}
				*into = append(*into, m)
			}
			return nil
		}
		if err := add(t.Seed.Steps, "seed/", "", &steps, true); err != nil {
			return nil, err
		}
		for _, name := range ps.modules {
			m, _ := t.Module(name)
			if err := add(m.Steps, "modules/"+name+"/", name+"/", &steps, true); err != nil {
				return nil, fmt.Errorf("module %s: %w", name, err)
			}
			if err := add(m.Verify, "modules/"+name+"/", name+"/", &verify, false); err != nil {
				return nil, fmt.Errorf("module %s: %w", name, err)
			}
		}
		if err := add(t.Seed.After, "seed/", "", &steps, true); err != nil {
			return nil, err
		}
		if err := add(t.Seed.Verify, "seed/", "", &verify, false); err != nil {
			return nil, err
		}
		manifest["steps"] = steps
		if len(verify) > 0 {
			manifest["verify"] = verify
		}
		mb, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return nil, err
		}
		plan.Files[path.Join(dir, config.SeedFile)] = append(mb, '\n')
		plan.Personas = append(plan.Personas, ps.p.Name)
		plan.Steps[ps.p.Name] = len(steps)
	}

	// Plain files.
	if _, err := fs.Stat(t.fsys, "files"); err == nil {
		err := fs.WalkDir(t.fsys, "files", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel := strings.TrimPrefix(p, "files/")
			if rel == config.EnvironmentFile || strings.HasPrefix(rel, config.SeedsDirName+"/") {
				return fmt.Errorf("files/%s would overwrite generated content", rel)
			}
			b, err := fs.ReadFile(t.fsys, p)
			if err != nil {
				return err
			}
			out, err := r.file(rel, b)
			if err != nil {
				return fmt.Errorf("files/%s: %w", rel, err)
			}
			plan.Files[rel] = out
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// copyFile copies a step's file reference into the seed set directory and
// returns the path the manifest should use.
func (t *Template) copyFile(r *renderer, plan *Plan, setDir, prefix, rename, ref string) (string, error) {
	clean := path.Clean(ref)
	if !strings.HasPrefix(clean, prefix) || !fs.ValidPath(clean) {
		return "", fmt.Errorf("file %q must live under %s in the template", ref, prefix)
	}
	b, err := fs.ReadFile(t.fsys, clean)
	if err != nil {
		return "", fmt.Errorf("file %q: %w", ref, err)
	}
	dest := rename + strings.TrimPrefix(clean, prefix)
	out, err := r.file(dest, b)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ref, err)
	}
	plan.Files[path.Join(setDir, dest)] = out
	return dest, nil
}

func findDep(deps []any, name string) map[string]any {
	for _, d := range deps {
		if m, ok := d.(map[string]any); ok && m["name"] == name {
			return m
		}
	}
	return nil
}

// renderer substitutes {{param}} placeholders. Dotted names such as
// {{brand.name}} are left alone: they are filled at reset time.
type renderer struct {
	values map[string]string
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_.]*)\s*\}\}`)

func (r *renderer) text(s string, escape func(string) string) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	var bad []string
	out := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := placeholderRe.FindStringSubmatch(m)[1]
		if strings.Contains(name, ".") {
			return m
		}
		v, ok := r.values[name]
		if !ok {
			bad = append(bad, name)
			return m
		}
		if escape != nil {
			return escape(v)
		}
		return v
	})
	if len(bad) > 0 {
		return "", fmt.Errorf("unknown placeholder {{%s}}", strings.Join(bad, "}}, {{"))
	}
	return out, nil
}

// tree substitutes placeholders in every string of a decoded JSON value,
// returning a deep copy. Structure is never built from text, so a value can
// not break the JSON.
func (r *renderer) tree(v any) (any, error) {
	switch x := v.(type) {
	case string:
		return r.text(x, nil)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			rv, err := r.tree(val)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			rv, err := r.tree(val)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case []map[string]any:
		out := make([]any, len(x))
		for i, val := range x {
			rv, err := r.tree(val)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	}
	return v, nil
}

var textExt = map[string]bool{".sql": true, ".json": true, ".yml": true, ".yaml": true, ".md": true, ".txt": true, ".sh": true,
	".conf": true, ".css": true, ".js": true, ".env": true, ".csv": true, ".toml": true, ".ini": true}
var markupExt = map[string]bool{".html": true, ".htm": true, ".xml": true, ".svg": true}

// file renders a file's content by type: markup gets HTML-escaped values,
// other text files get the raw (pattern-checked) values, anything else is
// copied byte for byte.
func (r *renderer) file(name string, b []byte) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case markupExt[ext]:
		out, err := r.text(string(b), html.EscapeString)
		return []byte(out), err
	case textExt[ext], ext == "":
		s := string(b)
		if ext == ".sh" {
			s = strings.ReplaceAll(s, "\r\n", "\n")
		}
		out, err := r.text(s, nil)
		return []byte(out), err
	}
	return b, nil
}

// Write puts a plan's files under dir, which must not exist yet.
func (p *Plan) Write(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	for _, rel := range p.Paths() {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, p.Files[rel], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Info is the listing form of a template, without the raw blocks.
type Info struct {
	Name        string       `json:"name"`
	Version     int          `json:"version,omitempty"`
	Title       string       `json:"title"`
	ProductType string       `json:"productType,omitempty"`
	Description string       `json:"description,omitempty"`
	Source      string       `json:"source"`
	Params      []Param      `json:"params"`
	Modules     []ModuleInfo `json:"modules"`
	Personas    []Persona    `json:"personas"`
	BaseDeps    int          `json:"baseDependencies"`
	Problems    []string     `json:"problems"`
}

// ModuleInfo summarises a module.
type ModuleInfo struct {
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Required     bool     `json:"required,omitempty"`
	Default      bool     `json:"default,omitempty"`
	Requires     []string `json:"requires"`
	Dependencies []string `json:"dependencies"`
	Steps        int      `json:"steps"`
	Verify       int      `json:"verify"`
}

// Describe returns the listing form.
func (t *Template) Describe() Info {
	in := Info{Name: t.Name, Version: t.Version, Title: t.DisplayTitle(), ProductType: t.ProductType, Description: t.Description,
		Source: t.Source, Params: t.Params, Personas: t.Personas, Problems: t.Problems}
	if in.Params == nil {
		in.Params = []Param{}
	}
	if in.Personas == nil {
		in.Personas = []Persona{}
	}
	if in.Problems == nil {
		in.Problems = []string{}
	}
	if deps, ok := t.Environment["dependencies"].([]any); ok {
		in.BaseDeps = len(deps)
	}
	for _, m := range t.Modules {
		mi := ModuleInfo{Name: m.Name, Title: firstNonEmpty(m.Title, m.Name), Description: m.Description, Required: m.Required, Default: m.Default,
			Requires: m.Requires, Dependencies: []string{}, Steps: len(m.Steps), Verify: len(m.Verify)}
		if mi.Requires == nil {
			mi.Requires = []string{}
		}
		for _, d := range m.Dependencies {
			if n, ok := d["name"].(string); ok {
				mi.Dependencies = append(mi.Dependencies, n)
			}
		}
		in.Modules = append(in.Modules, mi)
	}
	if in.Modules == nil {
		in.Modules = []ModuleInfo{}
	}
	return in
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
