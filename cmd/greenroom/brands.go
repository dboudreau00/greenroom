package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/dboudreau00/greenroom/internal/brand"
)

const brandUsage = `usage:
  greenroom brands                                  list brand kits
  greenroom brand fetch <url> [--slug s] [--name n] [--replace]
  greenroom brand show <slug>
  greenroom brand set <slug> [--name n] [--tagline t] [--primary #rrggbb] [--accent #rrggbb]
  greenroom brand asset <slug> <name> <url>         fetch one more image from a link
  greenroom brand rm <slug> [asset]                 delete a kit, or one of its assets`

func (c *cli) brands(args []string) int {
	fs := newFlags("brands", c.stderr)
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	list, err := c.eng.Brands().List()
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(list)
		return 0
	}
	if len(list) == 0 {
		fmt.Fprintln(c.stdout, "no brand kits yet (greenroom brand fetch <url>)")
		return 0
	}
	tw := tabwriter.NewWriter(c.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tNAME\tDOMAIN\tCOLORS\tASSETS\tUPDATED")
	for _, k := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s %s\t%s\t%s\n", k.Slug, k.Name, k.Domain, k.Primary, k.Accent, assetNames(k), k.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	tw.Flush()
	return 0
}

func assetNames(k *brand.Kit) string {
	var names []string
	for n := range k.Assets {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (c *cli) brand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(c.stderr, brandUsage)
		return 3
	}
	sub, args := args[0], args[1:]
	fs := newFlags("brand "+sub, c.stderr)
	asJSON := fs.Bool("json", false, "print the kit as JSON")
	slug := fs.String("slug", "", "kit name (default: from the domain)")
	name := fs.String("name", "", "brand name")
	replace := fs.Bool("replace", false, "replace an existing kit of the same slug")
	tagline := fs.String("tagline", "", "tagline")
	primary := fs.String("primary", "", "primary color")
	accent := fs.String("accent", "", "accent color")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		return 3
	}
	if err := c.open(true); err != nil {
		return c.fail(err)
	}
	store := c.eng.Brands()
	var k *brand.Kit
	var err error
	switch sub {
	case "fetch":
		if fs.NArg() < 1 {
			return c.fail(errors.New(brandUsage))
		}
		k, err = c.eng.FetchBrand(context.Background(), brand.FetchRequest{URL: fs.Arg(0), Slug: *slug, Name: *name, Replace: *replace}, "cli")
	case "show":
		if fs.NArg() < 1 {
			return c.fail(errors.New(brandUsage))
		}
		k, err = store.Get(fs.Arg(0))
	case "set":
		if fs.NArg() < 1 {
			return c.fail(errors.New(brandUsage))
		}
		var e brand.Edit
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if set["name"] {
			e.Name = name
		}
		if set["tagline"] {
			e.Tagline = tagline
		}
		if set["primary"] {
			e.Primary = primary
		}
		if set["accent"] {
			e.Accent = accent
		}
		k, err = store.Update(fs.Arg(0), e, "cli")
	case "asset":
		if fs.NArg() < 3 {
			return c.fail(errors.New(brandUsage))
		}
		k, err = c.eng.AddBrandAsset(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2), "cli")
	case "rm", "delete":
		if fs.NArg() < 1 {
			return c.fail(errors.New(brandUsage))
		}
		if fs.NArg() > 1 {
			k, err = store.RemoveAsset(fs.Arg(0), fs.Arg(1))
			break
		}
		if err := store.Delete(fs.Arg(0)); err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.stdout, "deleted brand kit %s\n", fs.Arg(0))
		return 0
	default:
		return c.fail(fmt.Errorf("unknown brand command %q\n%s", sub, brandUsage))
	}
	if err != nil {
		return c.fail(err)
	}
	if *asJSON {
		c.printJSON(k)
		return 0
	}
	c.printKit(k)
	return 0
}

func (c *cli) printKit(k *brand.Kit) {
	fmt.Fprintf(c.stdout, "%s: %s\n", k.Slug, k.Name)
	if k.Domain != "" {
		fmt.Fprintf(c.stdout, "  domain   %s (%s)\n", k.Domain, k.URL)
	}
	if k.Tagline != "" {
		fmt.Fprintf(c.stdout, "  tagline  %s\n", k.Tagline)
	}
	fmt.Fprintf(c.stdout, "  colors   primary %s, accent %s\n", firstNonEmpty(k.Primary, "(house)"), firstNonEmpty(k.Accent, "(house)"))
	names := strings.Split(assetNames(k), ",")
	for _, n := range names {
		if n == "" {
			continue
		}
		a := k.Assets[n]
		dim := ""
		if a.Width > 0 {
			dim = fmt.Sprintf(" %dx%d", a.Width, a.Height)
		}
		fmt.Fprintf(c.stdout, "  %-8s %s %s%s, %d bytes\n", n, a.File, a.Type, dim, a.Size)
	}
	for _, n := range k.Notes {
		fmt.Fprintf(c.stdout, "  note     %s\n", n)
	}
	fmt.Fprintf(c.stdout, "  hash     %s\n", k.Hash())
}
