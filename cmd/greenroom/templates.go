package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/dboudreau00/greenroom/internal/templates"
)

// multiFlag collects repeated --set key=value flags.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// ---- templates ----

func (c *cli) templates(args []string) int {
	fs := newFlags("templates", c.stderr)
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	list, err := c.eng.Templates()
	if err != nil {
		fmt.Fprintln(c.stderr, "warning:", err)
	}
	if fs.NArg() > 0 {
		t, ok := templates.Find(list, fs.Arg(0))
		if !ok {
			return c.fail(fmt.Errorf("template %q not found", fs.Arg(0)))
		}
		if *asJSON {
			c.printJSON(t.Describe())
			return 0
		}
		c.printTemplate(t.Describe())
		return 0
	}
	var infos []templates.Info
	for _, t := range list {
		infos = append(infos, t.Describe())
	}
	if *asJSON {
		if infos == nil {
			infos = []templates.Info{}
		}
		c.printJSON(infos)
		return 0
	}
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TEMPLATE\tPRODUCT\tMODULES\tPERSONAS\tSOURCE")
	for _, in := range infos {
		var mods, personas []string
		for _, m := range in.Modules {
			mark := ""
			if m.Required {
				mark = " (required)"
			} else if m.Default {
				mark = "*"
			}
			mods = append(mods, m.Name+mark)
		}
		for _, p := range in.Personas {
			personas = append(personas, p.Name)
		}
		src := in.Source
		if len(in.Problems) > 0 {
			src += ", PROBLEMS"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", in.Name, in.ProductType, strings.Join(mods, ", "), strings.Join(personas, ", "), src)
	}
	tw.Flush()
	fmt.Fprintln(c.stdout, "\n* selected when no --modules are given. Details: greenroom templates <name>")
	return 0
}

func (c *cli) printTemplate(in templates.Info) {
	fmt.Fprintf(c.stdout, "%s (v%d, %s): %s\n", in.Name, in.Version, in.Source, in.Title)
	if in.ProductType != "" {
		fmt.Fprintf(c.stdout, "  product: %s\n", in.ProductType)
	}
	if in.Description != "" {
		fmt.Fprintf(c.stdout, "  %s\n", in.Description)
	}
	for _, p := range in.Problems {
		fmt.Fprintf(c.stdout, "  PROBLEM: %s\n", p)
	}
	fmt.Fprintf(c.stdout, "\nModules (%d base dependencies):\n", in.BaseDeps)
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	for _, m := range in.Modules {
		flags := []string{}
		if m.Required {
			flags = append(flags, "required")
		} else if m.Default {
			flags = append(flags, "default")
		}
		if len(m.Requires) > 0 {
			flags = append(flags, "needs "+strings.Join(m.Requires, ", "))
		}
		if len(m.Dependencies) > 0 {
			flags = append(flags, "adds "+strings.Join(m.Dependencies, ", "))
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d steps, %d checks\t%s\n", m.Name, m.Title, m.Steps, m.Verify, strings.Join(flags, "; "))
	}
	tw.Flush()
	fmt.Fprintln(c.stdout, "\nPersonas:")
	tw = tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	for _, p := range in.Personas {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", p.Name, p.Title, strings.Join(p.Modules, ", "))
	}
	tw.Flush()
	if len(in.Params) > 0 {
		fmt.Fprintln(c.stdout, "\nParams (--set name=value):")
		tw = tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
		for _, p := range in.Params {
			fmt.Fprintf(tw, "  %s\t%s\tdefault %q\n", p.Name, p.Title, p.Default)
		}
		tw.Flush()
	}
}

// ---- new ----

func (c *cli) newEnv(args []string) int {
	fs := newFlags("new", c.stderr)
	tpl := fs.String("template", "", "template name (greenroom templates lists them)")
	modules := fs.String("modules", "", "comma separated modules to include (default: the template's defaults)")
	persona := fs.String("persona", "", "persona to pin (default: the first one that fits the modules)")
	title := fs.String("title", "", "environment title")
	owner := fs.String("owner", "", "owner shown in the portal")
	desc := fs.String("description", "", "description shown in the portal")
	dry := fs.Bool("dry-run", false, "show what would be created without writing")
	asJSON := fs.Bool("json", false, "print the plan as JSON")
	var sets multiFlag
	fs.Var(&sets, "set", "template param as name=value (repeatable)")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 || *tpl == "" {
		return c.fail(errors.New("usage: greenroom new <env> --template <name> [--modules a,b] [--persona p] [--title t] [--set k=v] [--dry-run]"))
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	params := map[string]string{}
	for _, kv := range sets {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return c.fail(fmt.Errorf("--set %q: want name=value", kv))
		}
		params[k] = v
	}
	var mods []string
	for _, m := range strings.Split(*modules, ",") {
		if m = strings.TrimSpace(m); m != "" {
			mods = append(mods, m)
		}
	}
	req := templates.Request{Name: fs.Arg(0), Title: *title, Owner: *owner, Description: *desc, Modules: mods, Persona: *persona, Params: params}
	plan, err := c.eng.CreateEnvironment(*tpl, req, *dry)
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(plan)
		return 0
	}
	dir := filepath.Join(c.ws.EnvironmentsDir, plan.Name)
	verb := "created"
	if *dry {
		verb = "would create"
	}
	fmt.Fprintf(c.stdout, "%s %s from %s with %s\n", verb, plan.Name, plan.Template, strings.Join(plan.Modules, ", "))
	fmt.Fprintf(c.stdout, "  %s: %d dependencies, pinned to %s\n", dir, plan.Dependencies, plan.Pin)
	for _, p := range plan.Personas {
		fmt.Fprintf(c.stdout, "  seed set %s@v1: %d steps\n", p, plan.Steps[p])
	}
	if *dry {
		for _, rel := range plan.Paths() {
			fmt.Fprintf(c.stdout, "    %s\n", filepath.Join(dir, filepath.FromSlash(rel)))
		}
	} else {
		fmt.Fprintf(c.stdout, "  %d files written\n", len(plan.Files))
		if _, ok := plan.Files["README.md"]; ok {
			fmt.Fprintf(c.stdout, "\nNext: %s\n", filepath.Join(dir, "README.md"))
		}
		fmt.Fprintf(c.stdout, "  greenroom check %s\n  greenroom reset %s\n", plan.Name, plan.Name)
	}
	return 0
}
