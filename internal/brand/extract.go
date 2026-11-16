package brand

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Page is what a prospect's home page says about its brand.
type Page struct {
	Name        string
	Title       string
	Description string
	Colors      []string
	// Logos and Icons are absolute URLs, best candidate first.
	Logos []string
	Icons []string
}

var (
	tagRe   = regexp.MustCompile(`(?is)<(meta|link|img|base)\b([^>]*)>`)
	titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	ldRe    = regexp.MustCompile(`(?is)<script[^>]+type\s*=\s*["']?application/ld\+json["']?[^>]*>(.*?)</script>`)
	attrRe  = regexp.MustCompile(`(?s)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*(?:=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+)))?`)
	sizesRe = regexp.MustCompile(`(\d+)x(\d+)`)
)

func attrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		k := strings.ToLower(m[1])
		if _, seen := out[k]; seen {
			continue
		}
		out[k] = html.UnescapeString(m[2] + m[3] + m[4])
	}
	return out
}

// ParsePage reads the brand signals of an HTML document. base is the URL the
// document was served from, used to resolve relative links. Only the first
// 512 KiB are looked at.
func ParsePage(base *url.URL, doc []byte) Page {
	if len(doc) > 512<<10 {
		doc = doc[:512<<10]
	}
	s := string(doc)
	p := Page{}
	type icon struct {
		href string
		size int
		rank int
	}
	var icons []icon
	var imgLogos, ogImages []string
	meta := map[string]string{}
	resolve := func(ref string) string {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.HasPrefix(strings.ToLower(ref), "data:") || strings.HasPrefix(strings.ToLower(ref), "javascript:") {
			return ""
		}
		r, err := url.Parse(ref)
		if err != nil {
			return ""
		}
		u := base.ResolveReference(r)
		if u.Scheme != "http" && u.Scheme != "https" {
			return ""
		}
		u.Fragment = ""
		return u.String()
	}
	for _, m := range tagRe.FindAllStringSubmatch(s, -1) {
		tag := strings.ToLower(m[1])
		a := attrs(m[2])
		switch tag {
		case "base":
			if h := a["href"]; h != "" {
				if r, err := url.Parse(h); err == nil {
					if nb := base.ResolveReference(r); nb.Scheme == "http" || nb.Scheme == "https" {
						base = nb
					}
				}
			}
		case "meta":
			key := strings.ToLower(firstNonEmpty(a["property"], a["name"]))
			if key != "" && a["content"] != "" {
				if _, seen := meta[key]; !seen {
					meta[key] = a["content"]
				}
			}
		case "link":
			rel := " " + strings.ToLower(a["rel"]) + " "
			href := resolve(a["href"])
			if href == "" {
				continue
			}
			size := 0
			for _, sm := range sizesRe.FindAllStringSubmatch(a["sizes"], -1) {
				if n, _ := strconv.Atoi(sm[1]); n > size {
					size = n
				}
			}
			switch {
			case strings.Contains(rel, " apple-touch-icon"):
				if size == 0 {
					size = 180
				}
				icons = append(icons, icon{href, size, 0})
			case strings.Contains(rel, " mask-icon "):
				if c := a["color"]; c != "" {
					meta["mask-icon-color"] = c
				}
			case strings.Contains(rel, " icon "):
				rank := 1
				if strings.Contains(strings.ToLower(a["type"]), "svg") || strings.HasSuffix(strings.ToLower(href), ".svg") {
					size, rank = 512, 0
				}
				icons = append(icons, icon{href, size, rank})
			}
		case "img":
			hint := strings.ToLower(a["class"] + " " + a["id"] + " " + a["alt"] + " " + a["src"])
			if strings.Contains(hint, "logo") {
				if h := resolve(firstNonEmpty(a["src"], a["data-src"])); h != "" {
					imgLogos = append(imgLogos, h)
				}
			}
		}
	}
	if og := resolve(meta["og:image"]); og != "" {
		ogImages = append(ogImages, og)
	}

	// JSON-LD Organization blocks carry the cleanest name and logo.
	var ldName, ldLogo string
	for _, m := range ldRe.FindAllStringSubmatch(s, 5) {
		n, l := orgFromLD([]byte(strings.TrimSpace(m[1])))
		if ldName == "" {
			ldName = n
		}
		if ldLogo == "" {
			ldLogo = resolve(l)
		}
	}
	if t := titleRe.FindStringSubmatch(s); t != nil {
		p.Title = Clean(html.UnescapeString(t[1]), 120)
	}
	p.Name = Clean(firstNonEmpty(meta["og:site_name"], meta["application-name"], ldName, siteFromTitle(p.Title)), 80)
	p.Description = Clean(firstNonEmpty(meta["og:description"], meta["description"], meta["twitter:description"]), 200)
	for _, k := range []string{"theme-color", "msapplication-tilecolor", "mask-icon-color"} {
		if c, ok := NormalizeColor(meta[k]); ok {
			p.Colors = append(p.Colors, c)
		}
	}
	sort.SliceStable(icons, func(i, j int) bool {
		if icons[i].rank != icons[j].rank {
			return icons[i].rank < icons[j].rank
		}
		return icons[i].size > icons[j].size
	})
	var iconURLs []string
	for _, ic := range icons {
		iconURLs = append(iconURLs, ic.href)
	}
	fav := resolve("/favicon.ico")
	p.Logos = dedupe(append(append(append(append([]string{ldLogo}, imgLogos...), iconURLs...), ogImages...), fav))
	p.Icons = dedupe(append(iconURLs, fav))
	return p
}

// orgFromLD finds an Organization (or similar) name and logo in a JSON-LD
// block, which may be an object, an array or carry an @graph.
func orgFromLD(b []byte) (name, logo string) {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return "", ""
	}
	var walk func(any, int)
	walk = func(v any, depth int) {
		if depth > 4 || (name != "" && logo != "") {
			return
		}
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e, depth+1)
			}
		case map[string]any:
			typ := strings.ToLower(strings.TrimSpace(toString(x["@type"])))
			if strings.Contains(typ, "organization") || typ == "corporation" || typ == "brand" || typ == "localbusiness" {
				if name == "" {
					name = toString(x["name"])
				}
				if logo == "" {
					switch l := x["logo"].(type) {
					case string:
						logo = l
					case map[string]any:
						logo = firstNonEmpty(toString(l["url"]), toString(l["contentUrl"]))
					}
				}
			}
			if g, ok := x["@graph"]; ok {
				walk(g, depth+1)
			}
			if pub, ok := x["publisher"]; ok {
				walk(pub, depth+1)
			}
		}
	}
	walk(v, 0)
	return name, logo
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		if len(x) > 0 {
			return toString(x[0])
		}
	}
	return ""
}

// siteFromTitle keeps the part of "Home | Tidewell" or "Tidewell - Home" that
// looks like the site name: the shortest segment.
func siteFromTitle(title string) string {
	best := ""
	for _, sep := range []string{" | ", " - ", " \u00b7 ", " : ", " \u2013 ", " \u2014 "} {
		if strings.Contains(title, sep) {
			for _, part := range strings.Split(title, sep) {
				part = strings.TrimSpace(part)
				if part != "" && (best == "" || len(part) < len(best)) && !strings.EqualFold(part, "home") {
					best = part
				}
			}
			return best
		}
	}
	return title
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Clean makes a text value safe to carry into seed files: control
// characters and backslashes go, whitespace collapses, length is capped.
// Context-specific escaping still happens where the value is used.
func Clean(s string, max int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == '\\' || r == 0x7f || (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 || r == 0xfeff:
			continue
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if max > 0 && len([]rune(out)) > max {
		out = strings.TrimSpace(string([]rune(out)[:max]))
	}
	return out
}

var hexRe = regexp.MustCompile(`^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
var rgbRe = regexp.MustCompile(`^rgba?\(\s*(\d{1,3})\s*[, ]\s*(\d{1,3})\s*[, ]\s*(\d{1,3})`)

// NormalizeColor turns #abc, #aabbcc or rgb(r, g, b) into #aabbcc.
func NormalizeColor(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if m := hexRe.FindStringSubmatch(s); m != nil {
		h := m[1]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		return "#" + h, true
	}
	if m := rgbRe.FindStringSubmatch(s); m != nil {
		var out [3]int
		for i := 0; i < 3; i++ {
			n, _ := strconv.Atoi(m[i+1])
			if n > 255 {
				return "", false
			}
			out[i] = n
		}
		return hexColor(out[0], out[1], out[2]), true
	}
	return "", false
}

func hexColor(r, g, b int) string {
	const digits = "0123456789abcdef"
	buf := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []int{r, g, b} {
		buf[1+i*2] = digits[v>>4]
		buf[2+i*2] = digits[v&15]
	}
	return string(buf)
}
