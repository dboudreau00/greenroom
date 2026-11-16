package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dboudreau00/greenroom/internal/engine"
	"github.com/dboudreau00/greenroom/internal/seeds"
)

// seedsDiff exits 0 when the sets are identical and 1 when they differ.
func (c *cli) seedsDiff(args []string) int {
	fs := newFlags("seeds diff", c.stderr)
	asJSON := fs.Bool("json", false, "print as JSON")
	stat := fs.Bool("stat", false, "summary and file list only, no line diffs")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() != 3 {
		return c.fail(errors.New("usage: greenroom seeds diff <env> <persona@vN> <persona@vN> [--json] [--stat]"))
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	v, err := c.eng.Env(fs.Arg(0))
	if err != nil {
		return c.fail(err)
	}
	d, err := c.eng.DiffSets(v, fs.Arg(1), fs.Arg(2))
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		if *stat {
			for i := range d.Files {
				d.Files[i].Hunks = nil
			}
		}
		c.printJSON(d)
	} else {
		c.printDiff(d, *stat)
	}
	if d.Identical {
		return 0
	}
	return 1
}

func (c *cli) printDiff(d *seeds.SetDiff, stat bool) {
	w := c.stdout
	fmt.Fprintf(w, "%s -> %s\n", d.From, d.To)
	if d.Identical {
		fmt.Fprintln(w, "identical")
		return
	}
	m := d.Manifest
	if !m.Empty() {
		fmt.Fprintln(w, "\nmanifest:")
		if m.Title != nil {
			fmt.Fprintf(w, "  title: %q -> %q\n", m.Title.From, m.Title.To)
		}
		if m.Description != nil {
			fmt.Fprintln(w, "  description changed")
		}
		if len(m.Fields) > 0 {
			fmt.Fprintf(w, "  changed: %s\n", strings.Join(m.Fields, ", "))
		}
		printItems(c, "step", m.Steps)
		printItems(c, "verify", m.Verify)
	}
	s := d.Stat
	fmt.Fprintf(w, "\nfiles: %d added, %d removed, %d changed, %d unchanged\n", s.Added, s.Removed, s.Changed, s.Unchanged)
	for _, f := range d.Files {
		switch f.Status {
		case seeds.FileAdded:
			fmt.Fprintf(w, "  A %s (%d B)\n", f.Path, f.NewSize)
		case seeds.FileRemoved:
			fmt.Fprintf(w, "  D %s (%d B)\n", f.Path, f.OldSize)
		case seeds.FileChanged:
			note := ""
			if f.Binary {
				note = ", binary"
			} else if f.Note != "" {
				note = ", " + f.Note
			}
			fmt.Fprintf(w, "  M %s (%d B -> %d B%s)\n", f.Path, f.OldSize, f.NewSize, note)
		}
	}
	if stat {
		return
	}
	for _, f := range d.Files {
		if f.Status != seeds.FileChanged {
			continue
		}
		fmt.Fprintln(w)
		if f.Binary {
			fmt.Fprintf(w, "Binary files a/%s and b/%s differ (%s -> %s)\n", f.Path, f.Path, short(f.OldHash), short(f.NewHash))
			continue
		}
		if len(f.Hunks) == 0 {
			fmt.Fprintf(w, "%s: %s\n", f.Path, f.Note)
			continue
		}
		fmt.Fprint(w, f.Unified())
		if f.Truncated {
			fmt.Fprintf(w, "... diff of %s truncated (%d lines or %d KiB of input at most)\n", f.Path, seeds.MaxDiffLines, seeds.MaxDiffInput/1024)
		}
	}
}

func printItems(c *cli, label string, d seeds.ItemsDiff) {
	if len(d.Added) > 0 {
		fmt.Fprintf(c.stdout, "  %s added: %s\n", label, strings.Join(d.Added, ", "))
	}
	if len(d.Removed) > 0 {
		fmt.Fprintf(c.stdout, "  %s removed: %s\n", label, strings.Join(d.Removed, ", "))
	}
	for _, ch := range d.Changed {
		fmt.Fprintf(c.stdout, "  %s changed: %s (%s)\n", label, ch.Name, strings.Join(ch.Fields, ", "))
	}
	if d.Reordered {
		fmt.Fprintf(c.stdout, "  %s order changed\n", label)
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func (c *cli) seedsBump(args []string) int {
	fs := newFlags("seeds bump", c.stderr)
	from := fs.String("from", "", "version to copy, v2 or 2 (default: the latest)")
	title := fs.String("title", "", "title of the new version (default: the source title)")
	pin := fs.Bool("pin", false, "pin the environment to the new version")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if fs.NArg() != 2 {
		return c.fail(errors.New("usage: greenroom seeds bump <env> <persona> [--from vN] [--title t] [--pin]"))
	}
	n := 0
	if *from != "" {
		var err error
		n, err = strconv.Atoi(strings.TrimPrefix(*from, "v"))
		if err != nil || n <= 0 {
			return c.fail(fmt.Errorf("bad --from %q (want v2)", *from))
		}
	}
	if err := c.load(true); err != nil {
		return c.fail(err)
	}
	res, err := c.eng.BumpSet(fs.Arg(0), engine.BumpRequest{Persona: fs.Arg(1), From: n, Title: *title, Pin: *pin, Actor: "cli"})
	if res == nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.stdout, "created %s from %s in %s\n", res.ID, res.From, res.Dir)
	if res.Pinned {
		fmt.Fprintf(c.stdout, "pinned %s to %s\n", fs.Arg(0), res.ID)
	}
	if err != nil {
		return c.fail(err)
	}
	return 0
}
