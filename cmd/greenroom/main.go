// Command greenroom is the demo environment reset and seed tool: a CLI for
// pre-call checks, resets, snapshots and heals, and a portal (serve) that
// watches every environment and drives the same operations from a browser.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/example"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/server"
	"github.com/dboudreau00/greenroom/internal/state"
)

// Version is set by the build.
var Version = "dev"

const usage = `greenroom: demo environment reset and seed tool

Usage:
  greenroom [-C dir] <command> [flags]

Commands:
  serve       run the portal and the background prober
  check       pre-call health check of an environment (exit 0 green, 1 amber, 2 red)
  stale       report what is stale (exit 0 fresh, 1 stale)
  reset       reset an environment to a seed set (known-good snapshot + seed files)
  snapshot    capture a known-good baseline for a dependency
  heal        run a dependency's heal action
  envs        list environments
  seeds       list seed sets of an environment
  targets     check connectivity of an environment's targets
  validate    load every environment and seed set and report problems
  templates   list the environment templates and their product modules
  new         create an environment from a template with the modules to show
  init        scaffold a workspace with the bundled example environment
  version     print the version

Global flags:
  -C dir      workspace directory holding greenroom.json (default: current directory)

Run "greenroom <command> -h" for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	dir := "."
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-C" || args[i] == "--dir":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "-C needs a directory")
				return 3
			}
			dir = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-C="):
			dir = strings.TrimPrefix(args[i], "-C=")
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	cmd, cargs := rest[0], rest[1:]
	if cmd == "version" || cmd == "--version" {
		fmt.Fprintln(stdout, "greenroom", Version)
		return 0
	}
	if cmd == "init" {
		return cmdInit(dir, cargs, stdout, stderr)
	}
	c := &cli{dir: dir, stdout: stdout, stderr: stderr}
	switch cmd {
	case "serve":
		return c.serve(cargs)
	case "check":
		return c.check(cargs)
	case "stale":
		return c.stale(cargs)
	case "reset":
		return c.reset(cargs)
	case "snapshot":
		return c.snapshot(cargs)
	case "heal":
		return c.heal(cargs)
	case "envs", "environments", "ls":
		return c.envs(cargs)
	case "seeds":
		return c.seeds(cargs)
	case "targets":
		return c.targets(cargs)
	case "validate":
		return c.validate(cargs)
	case "templates":
		return c.templates(cargs)
	case "new":
		return c.newEnv(cargs)
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
	return 3
}

type cli struct {
	dir    string
	stdout io.Writer
	stderr io.Writer
	eng    *engine.Engine
	ws     *config.Workspace
}

func (c *cli) load(quiet bool) error {
	if err := c.open(quiet); err != nil {
		return err
	}
	if len(c.eng.Envs()) == 0 {
		return fmt.Errorf("no environments found under %s (run greenroom init, or pass -C to the workspace directory)", c.ws.EnvironmentsDir)
	}
	return nil
}

// open loads the workspace and engine without requiring any environment.
func (c *cli) open(quiet bool) error {
	ws, err := config.LoadWorkspace(c.dir)
	if err != nil {
		return err
	}
	c.ws = ws
	st, err := state.Open(ws.StateDir)
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if quiet {
		level = slog.LevelWarn
	}
	logger := slog.New(slog.NewTextHandler(c.stderr, &slog.HandlerOptions{Level: level}))
	c.eng = engine.New(ws, st, logger)
	c.eng.Version = Version
	if err := c.eng.Reload(); err != nil {
		fmt.Fprintf(c.stderr, "warning: %v\n", err)
	}
	return nil
}

func (c *cli) fail(err error) int {
	fmt.Fprintln(c.stderr, "error:", err)
	return 3
}

func (c *cli) printJSON(v any) {
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func newFlags(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}

// reorder moves flags ahead of positional arguments so that
// "greenroom check acme-demo --json" works as well as the reverse.
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			pos = append(pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			flags = append(flags, a)
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			flags = append(flags, a)
			continue
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			flags = append(flags, a)
			continue
		}
		flags = append(flags, a)
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, pos...)
}

// ---- serve ----

func (c *cli) serve(args []string) int {
	fs := newFlags("serve", c.stderr)
	listen := fs.String("listen", "", "address to listen on (default from greenroom.json or 127.0.0.1:8470)")
	token := fs.String("token", "", "portal token (default GREENROOM_TOKEN or greenroom.json)")
	insecure := fs.Bool("insecure", false, "allow listening on a non-loopback address without a token")
	noSched := fs.Bool("no-scheduler", false, "do not run the background prober")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.load(false); err != nil {
		return c.fail(err)
	}
	if *listen == "" {
		*listen = c.ws.Listen
	}
	if *token == "" {
		*token = c.ws.Token
	}
	if os.Getenv("GREENROOM_INSECURE") == "1" {
		*insecure = true
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(c.stderr, nil))
	srv := server.New(c.eng, server.Options{Token: *token, Insecure: *insecure, Version: Version}, logger)
	if !*noSched {
		go c.eng.RunScheduler(ctx)
	}
	fmt.Fprintf(c.stderr, "greenroom %s: %d environments from %s, state in %s\n", Version, len(c.eng.Envs()), c.ws.EnvironmentsDir, c.ws.StateDir)
	if *token == "" {
		fmt.Fprintln(c.stderr, "no token configured: the portal is open to anyone who can reach it (loopback only unless --insecure)")
	}
	if err := srv.ListenAndServe(ctx, *listen); err != nil {
		return c.fail(err)
	}
	return 0
}

// ---- check ----

func (c *cli) check(args []string) int {
	fs := newFlags("check", c.stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	noDrift := fs.Bool("no-drift", false, "skip drift queries")
	noRecord := fs.Bool("no-record", false, "do not record the check as a run")
	all := fs.Bool("all", false, "check every environment")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	var names []string
	if *all {
		for _, v := range c.eng.Envs() {
			names = append(names, v.Config.Name)
		}
	} else if fs.NArg() < 1 {
		return c.fail(errors.New("usage: greenroom check <env> [--json] [--no-drift] or --all"))
	} else {
		names = fs.Args()
	}
	worst := 0
	var reports []*engine.CheckReport
	for _, name := range names {
		rep, err := c.eng.Check(context.Background(), name, engine.CheckOptions{Record: !*noRecord, Drift: !*noDrift, Actor: "cli"})
		if err != nil {
			return c.fail(err)
		}
		reports = append(reports, rep)
		if !*asJSON {
			c.printCheck(rep)
		}
		code := map[string]int{engine.Green: 0, engine.Amber: 1, engine.Red: 2}[rep.Readiness]
		if code > worst {
			worst = code
		}
	}
	if *asJSON {
		if len(reports) == 1 {
			c.printJSON(reports[0])
		} else {
			c.printJSON(reports)
		}
	}
	return worst
}

func (c *cli) printCheck(rep *engine.CheckReport) {
	fmt.Fprintf(c.stdout, "%s  %s  %s (%s)\n", strings.ToUpper(rep.Readiness), rep.Env, rep.Summary, rep.Duration.Round(time.Millisecond))
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	for _, r := range rep.Results {
		st := "up"
		switch {
		case !r.OK:
			st = "DOWN"
		case r.Warn:
			st = "warn"
		}
		crit := ""
		if r.Critical {
			crit = " (critical)"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s%s\n", st, r.Dep, r.Latency.Round(time.Millisecond), r.Message, crit)
	}
	tw.Flush()
	if rep.Stale != nil && len(rep.Stale.Items) > 0 {
		fmt.Fprintln(c.stdout, "  stale:")
		for _, it := range rep.Stale.Items {
			dep := ""
			if it.Dep != "" {
				dep = it.Dep + ": "
			}
			fmt.Fprintf(c.stdout, "    [%s] %s%s\n", it.Severity, dep, it.Message)
		}
	} else if rep.Stale != nil && rep.Stale.Fresh {
		fmt.Fprintln(c.stdout, "  seed fresh:", rep.Stale.Pinned)
	}
	fmt.Fprintln(c.stdout)
}

// ---- stale ----

func (c *cli) stale(args []string) int {
	fs := newFlags("stale", c.stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	noDrift := fs.Bool("no-drift", false, "skip drift queries")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 {
		return c.fail(errors.New("usage: greenroom stale <env> [--json] [--no-drift]"))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	v, err := c.eng.Env(fs.Arg(0))
	if err != nil {
		return c.fail(err)
	}
	// Probe first so fingerprints are current.
	results := v.Prober.ProbeAll(context.Background())
	rep, err := c.eng.StaleReport(context.Background(), v, results, !*noDrift)
	if err != nil && rep == nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(rep)
	} else {
		fmt.Fprintf(c.stdout, "%s: pinned %s", rep.Env, rep.Pinned)
		if rep.Applied != nil {
			fmt.Fprintf(c.stdout, ", applied %s at %s", rep.Applied.SetID, rep.Applied.At.Local().Format("2006-01-02 15:04"))
		}
		fmt.Fprintln(c.stdout)
		if len(rep.Items) == 0 {
			fmt.Fprintln(c.stdout, "  nothing stale")
		}
		for _, it := range rep.Items {
			dep := ""
			if it.Dep != "" {
				dep = it.Dep + ": "
			}
			fmt.Fprintf(c.stdout, "  [%s] %s%s\n", it.Severity, dep, it.Message)
			if it.Fix != "" {
				fmt.Fprintf(c.stdout, "          fix: %s\n", it.Fix)
			}
		}
	}
	if rep.Stale > 0 {
		return 1
	}
	return 0
}

// ---- reset ----

func (c *cli) reset(args []string) int {
	fs := newFlags("reset", c.stderr)
	persona := fs.String("persona", "", "persona (default: the environment pin)")
	version := fs.Int("version", 0, "seed version (default: pinned or latest)")
	force := fs.Bool("force", false, "continue even if the preflight finds dependencies down")
	skip := fs.Bool("skip-reset", false, "apply seed steps without running reset strategies")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	asJSON := fs.Bool("json", false, "print the run as JSON when done")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 {
		return c.fail(errors.New("usage: greenroom reset <env> [--persona p] [--version n] [--force] [--yes]"))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	name := fs.Arg(0)
	if fs.NArg() > 1 && *persona == "" {
		p, v, err := seeds.ParseRef(fs.Arg(1))
		if err != nil {
			return c.fail(err)
		}
		*persona, *version = p, v
	}
	v, err := c.eng.Env(name)
	if err != nil {
		return c.fail(err)
	}
	set, err := c.eng.ResolveSet(v, *persona, *version)
	if err != nil {
		return c.fail(err)
	}
	if !*yes {
		fmt.Fprintf(c.stdout, "About to reset %s to %s (%s). Everything demoed since the last reset is discarded.\nContinue? [y/N] ", v.Config.DisplayTitle(), set.ID(), set.DisplayTitle())
		var answer string
		fmt.Fscanln(os.Stdin, &answer)
		if !strings.HasPrefix(strings.ToLower(answer), "y") {
			fmt.Fprintln(c.stdout, "aborted")
			return 1
		}
	}
	id, err := c.eng.StartReset(name, engine.ResetOptions{Persona: set.Persona, Version: set.Version, Force: *force, SkipReset: *skip, Actor: "cli"})
	if err != nil {
		return c.fail(err)
	}
	run := c.follow(name, id, !*asJSON)
	if *asJSON {
		c.printJSON(run)
	}
	if run == nil || run.Status != state.StatusOK {
		return 1
	}
	return 0
}

// follow streams a run's events to stdout until it ends.
func (c *cli) follow(env, id string, print bool) *state.Run {
	snap, ch, cancel, live := c.eng.Subscribe(id)
	if !live {
		run, _ := c.eng.Run(env, id)
		if print && run != nil {
			for _, l := range run.Log {
				c.printLine(l)
			}
		}
		return run
	}
	defer cancel()
	if print {
		for _, l := range snap.Log {
			c.printLine(l)
		}
	}
	for ev := range ch {
		if print && ev.Line != nil {
			c.printLine(*ev.Line)
		}
	}
	run, _ := c.eng.Run(env, id)
	if print && run != nil {
		fmt.Fprintf(c.stdout, "%s: %s\n", strings.ToUpper(run.Status), firstNonEmpty(run.Summary, run.Error))
	}
	return run
}

func (c *cli) printLine(l state.LogLine) {
	tag := ""
	if l.Step != "" {
		tag = "[" + l.Step + "] "
	}
	prefix := ""
	switch l.Level {
	case "warn":
		prefix = "warn: "
	case "error":
		prefix = "ERROR: "
	case "err":
		prefix = "  | "
	case "out":
		prefix = "  | "
	}
	fmt.Fprintf(c.stdout, "%s %s%s%s\n", l.At.Local().Format("15:04:05"), tag, prefix, l.Text)
}

// ---- snapshot / heal ----

func (c *cli) snapshot(args []string) int {
	fs := newFlags("snapshot", c.stderr)
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 2 {
		return c.fail(errors.New("usage: greenroom snapshot <env> <dependency>"))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	id, err := c.eng.StartSnapshot(fs.Arg(0), fs.Arg(1), "cli")
	if err != nil {
		return c.fail(err)
	}
	run := c.follow(fs.Arg(0), id, true)
	if run == nil || run.Status != state.StatusOK {
		return 1
	}
	return 0
}

func (c *cli) heal(args []string) int {
	fs := newFlags("heal", c.stderr)
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 2 {
		return c.fail(errors.New("usage: greenroom heal <env> <dependency>"))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	id, err := c.eng.StartHeal(fs.Arg(0), fs.Arg(1), "cli")
	if err != nil {
		return c.fail(err)
	}
	run := c.follow(fs.Arg(0), id, true)
	if run == nil || run.Status != state.StatusOK {
		return 1
	}
	return 0
}

// ---- listings ----

func (c *cli) envs(args []string) int {
	fs := newFlags("envs", c.stderr)
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	var list []engine.EnvSummary
	for _, v := range c.eng.Envs() {
		list = append(list, c.eng.Summary(context.Background(), v))
	}
	if *asJSON {
		c.printJSON(list)
		return 0
	}
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTITLE\tDEPS\tPINNED\tAPPLIED\tLAST RESET\tSEED SETS")
	for _, s := range list {
		applied, last := "never", ""
		if s.Applied != nil {
			applied = s.Applied.SetID
			last = s.Applied.At.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%d\n", s.Name, s.Title, s.Deps, s.Pinned, applied, last, s.SeedSets)
	}
	tw.Flush()
	return 0
}

func (c *cli) seeds(args []string) int {
	fs := newFlags("seeds", c.stderr)
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 {
		return c.fail(errors.New("usage: greenroom seeds <env>"))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	v, err := c.eng.Env(fs.Arg(0))
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(v.Seeds)
		return 0
	}
	applied, _ := c.eng.Store().LoadApplied(v.Config.Name)
	pinned, _ := c.eng.PinnedSet(v)
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SEED SET\tTITLE\tSTEPS\tFILES\tHASH\tFLAGS")
	for _, s := range v.Seeds {
		var flags []string
		if pinned != nil && pinned.ID() == s.ID() {
			flags = append(flags, "pinned")
		}
		if applied != nil && applied.SetID == s.ID() {
			if applied.SetHash == s.Hash {
				flags = append(flags, "applied")
			} else {
				flags = append(flags, "applied (files changed)")
			}
		}
		if len(s.Problems) > 0 {
			flags = append(flags, "PROBLEMS: "+strings.Join(s.Problems, "; "))
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", s.ID(), s.Title, len(s.Steps), len(s.Files), s.Hash[:12], strings.Join(flags, ", "))
	}
	tw.Flush()
	return 0
}

func (c *cli) targets(args []string) int {
	fs := newFlags("targets", c.stderr)
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() < 1 {
		return c.fail(errors.New("usage: greenroom targets <env>"))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	v, err := c.eng.Env(fs.Arg(0))
	if err != nil {
		return c.fail(err)
	}
	list := c.eng.Targets(context.Background(), v)
	if *asJSON {
		c.printJSON(list)
		return 0
	}
	code := 0
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TARGET\tKIND\tSTATUS\tDETAIL")
	for _, t := range list {
		st := "ok"
		if !t.OK {
			st = "FAIL"
			code = 1
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Name, t.Kind, st, t.Message)
	}
	tw.Flush()
	return code
}

func (c *cli) validate(args []string) int {
	fs := newFlags("validate", c.stderr)
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	ws, err := config.LoadWorkspace(c.dir)
	if err != nil {
		return c.fail(err)
	}
	code := 0
	envs, err := config.LoadEnvironments(ws.EnvironmentsDir)
	if err != nil {
		fmt.Fprintln(c.stdout, "environment errors:")
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Fprintln(c.stdout, "  ", line)
		}
		code = 1
	}
	for _, env := range envs {
		sets, serr := seeds.Discover(env.Dir)
		problems := 0
		for _, s := range sets {
			problems += len(s.Problems)
		}
		status := "ok"
		if serr != nil || problems > 0 {
			status = "PROBLEMS"
			code = 1
		}
		fmt.Fprintf(c.stdout, "%s: %d dependencies, %d targets, %d seed sets, %s\n", env.Name, len(env.Dependencies), len(env.Targets), len(sets), status)
		if serr != nil {
			fmt.Fprintln(c.stdout, "   ", serr)
		}
		for _, s := range sets {
			for _, p := range s.Problems {
				fmt.Fprintf(c.stdout, "    %s: %s\n", s.ID(), p)
			}
		}
		if env.Pin.Persona != "" {
			if _, ok := seeds.Find(sets, env.Pin.Persona, env.Pin.Version); !ok {
				fmt.Fprintf(c.stdout, "    pin %s@v%d does not match any seed set\n", env.Pin.Persona, env.Pin.Version)
				code = 1
			}
		}
	}
	if len(envs) == 0 && err == nil {
		fmt.Fprintf(c.stdout, "no environments under %s\n", ws.EnvironmentsDir)
	}
	return code
}

// ---- init ----

func cmdInit(dir string, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("init", stderr)
	force := fs.Bool("force", false, "overwrite existing files")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 3
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 3
	}
	wsPath := filepath.Join(abs, config.WorkspaceFile)
	if _, err := os.Stat(wsPath); err == nil && !*force {
		fmt.Fprintf(stderr, "%s already exists (use --force to overwrite)\n", wsPath)
		return 1
	}
	ws := map[string]any{
		"listen":          config.DefaultListen,
		"environmentsDir": "environments",
		"stateDir":        "state",
		"probeInterval":   "60s",
		"sampleRetention": "720h",
		"healCooldown":    "5m",
	}
	if err := config.WriteJSON(wsPath, ws); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 3
	}
	envDir := filepath.Join(abs, "environments", example.Name)
	written, err := example.Extract(envDir, *force)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 3
	}
	sort.Strings(written)
	fmt.Fprintf(stdout, "workspace created in %s\n", abs)
	fmt.Fprintf(stdout, "  %s\n", wsPath)
	for _, w := range written {
		fmt.Fprintf(stdout, "  %s\n", w)
	}
	fmt.Fprintf(stdout, "\nNext:\n  cd %s\n  docker compose -f environments/%s/docker-compose.yml up -d\n  greenroom check %s\n  greenroom reset %s\n  greenroom serve\n", abs, example.Name, example.Name, example.Name)
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
