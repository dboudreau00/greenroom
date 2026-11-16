package brand

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Mode is the escaping applied to a value when it is put into content.
type Mode int

// Escaping modes, picked from where the content goes.
const (
	// Raw inserts the cleaned value as is: argv elements, plain text.
	Raw Mode = iota
	// SQL inserts a complete quoted string literal: write {{brand.name}},
	// not '{{brand.name}}'.
	SQL
	// JSON inserts the escaped content of a JSON string: write
	// "{{brand.name}}". Also used for JavaScript files.
	JSON
	// HTML escapes for element content and quoted attributes.
	HTML
	// CSS escapes for the inside of a quoted CSS string.
	CSS
	// URL escapes for a query value.
	URL
	// Path is for destination paths: a value that is not a plain file name
	// (separators, dot segments, a leading dot) fails the render.
	Path
)

// Keys are the placeholders a seed file can use, as {{brand.<key>}}.
var Keys = []string{"name", "slug", "domain", "url", "tagline", "primary", "accent", "logo", "icon"}

// Values are the brand values for one reset.
type Values map[string]string

// Default house colors when neither a kit nor the environment sets any.
const (
	DefaultPrimary = "#22c55e"
	DefaultAccent  = "#15803d"
)

// Context builds the values for a reset: the kit when one was chosen,
// otherwise the house defaults. Every value is cleaned.
func Context(k *Kit, houseName, houseTagline, houseDomain, housePrimary, houseAccent string) Values {
	v := Values{"slug": "default", "url": "", "logo": "", "icon": ""}
	if k != nil {
		v["name"], v["slug"], v["domain"], v["url"], v["tagline"] = k.Name, k.Slug, k.Domain, k.URL, k.Tagline
		v["primary"], v["accent"] = k.Primary, k.Accent
		if a, ok := k.Assets["logo"]; ok {
			v["logo"] = a.File
		}
		if a, ok := k.Assets["icon"]; ok {
			v["icon"] = a.File
		}
	} else {
		v["name"], v["tagline"], v["domain"] = houseName, houseTagline, houseDomain
		v["primary"], v["accent"] = housePrimary, houseAccent
	}
	for _, key := range []string{"primary", "accent"} {
		if c, ok := NormalizeColor(v[key]); ok {
			v[key] = c
		} else {
			v[key] = ""
		}
	}
	ownPrimary := v["primary"] != ""
	if !ownPrimary {
		v["primary"] = DefaultPrimary
	}
	if v["accent"] == "" {
		if ownPrimary {
			v["accent"] = Shade(v["primary"], 0.72)
		} else {
			v["accent"] = DefaultAccent
		}
	}
	for key, val := range v {
		max := 200
		if key == "url" {
			max = 500
		}
		v[key] = Clean(val, max)
	}
	return v
}

// Env returns the values as GREENROOM_BRAND_* variables for scripts.
func (v Values) Env() map[string]string {
	out := map[string]string{}
	for k, val := range v {
		out["GREENROOM_BRAND_"+strings.ToUpper(k)] = val
	}
	return out
}

var brandRe = regexp.MustCompile(`\{\{\s*brand\.([A-Za-z0-9_]+)\s*\}\}`)

// Render fills {{brand.<key>}} placeholders. An unknown key is an error, so
// a typo fails the step instead of shipping a literal placeholder.
func (v Values) Render(content string, mode Mode) (string, error) {
	if !strings.Contains(content, "{{") {
		return content, nil
	}
	var bad, unsafe []string
	out := brandRe.ReplaceAllStringFunc(content, func(m string) string {
		key := brandRe.FindStringSubmatch(m)[1]
		val, ok := v[key]
		if !ok {
			bad = append(bad, key)
			return m
		}
		if mode == Path && !pathSafe(val) {
			unsafe = append(unsafe, key)
			return m
		}
		return escape(val, mode)
	})
	if len(bad) > 0 {
		sort.Strings(bad)
		return "", fmt.Errorf("unknown placeholder {{brand.%s}} (known: %s)", strings.Join(bad, "}}, {{brand."), strings.Join(Keys, ", "))
	}
	if len(unsafe) > 0 {
		return "", fmt.Errorf("{{brand.%s}} is not a plain file name and can not be used in a destination path (use {{brand.slug}})", strings.Join(unsafe, "}}, {{brand."))
	}
	return out, nil
}

// pathSafe reports whether a value can sit inside a path: no separators,
// no dot segments, no leading dot, nothing a shell or tar would treat
// specially.
func pathSafe(s string) bool {
	if s == "" || s == "." || s == ".." || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "-") {
		return false
	}
	return !strings.ContainsAny(s, "/\\:*?\"<>|\x00\n\r\t")
}

func escape(s string, mode Mode) string {
	switch mode {
	case SQL:
		s = strings.NewReplacer("\\", "", "\x00", "").Replace(s)
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case JSON:
		b, _ := json.Marshal(s) // escapes <, > and & as well
		return string(b[1 : len(b)-1])
	case HTML:
		return html.EscapeString(s)
	case CSS:
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(" #.,_+-()&", r) {
				b.WriteRune(r)
			} else {
				fmt.Fprintf(&b, "\\%06x", r)
			}
		}
		return b.String()
	case URL:
		return url.QueryEscape(s)
	}
	return s
}

// ModeForFile picks the escaping for a file by the extension of where it
// is written.
func ModeForFile(name string) Mode {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm", ".xml", ".svg":
		return HTML
	case ".json", ".js", ".mjs":
		return JSON
	case ".css":
		return CSS
	case ".sql":
		return SQL
	}
	return Raw
}
