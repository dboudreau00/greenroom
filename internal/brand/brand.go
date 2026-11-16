// Package brand keeps brand kits: the name, colors, logo, icon and any
// other assets of a prospect, fetched from a link and used to personalize a
// demo when it is reset. Kits live in the state directory as plain files:
//
//	state/_brands/<slug>/brand.json
//	state/_brands/<slug>/<asset>.<ext>
package brand

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoders for dimension and color reads
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dboudreau00/greenroom/internal/config"
	"github.com/dboudreau00/greenroom/internal/webfetch"
)

// Asset is one stored file of a kit.
type Asset struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Source string `json:"source,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Kit is a prospect's brand.
type Kit struct {
	Slug      string           `json:"slug"`
	Name      string           `json:"name"`
	Domain    string           `json:"domain,omitempty"`
	URL       string           `json:"url,omitempty"`
	Tagline   string           `json:"tagline,omitempty"`
	Primary   string           `json:"primary,omitempty"`
	Accent    string           `json:"accent,omitempty"`
	Assets    map[string]Asset `json:"assets"`
	Notes     []string         `json:"notes,omitempty"`
	FetchedAt time.Time        `json:"fetchedAt,omitempty"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Actor     string           `json:"actor,omitempty"`
}

// Hash identifies the kit's content: text fields and asset digests. A reset
// records it so an edit or re-fetch afterwards shows up as stale.
func (k *Kit) Hash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\n", k.Slug, k.Name, k.Domain, k.URL, k.Tagline, k.Primary, k.Accent)
	names := make([]string, 0, len(k.Assets))
	for n := range k.Assets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := k.Assets[n]
		fmt.Fprintf(h, "%s\x00%s\x00%s\n", n, a.Type, a.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Store is the brands directory.
type Store struct {
	dir string
	mu  sync.Mutex
}

// DirName is the brands directory inside the state directory. It does not
// match config.ValidName, so it can never collide with an environment.
const DirName = "_brands"

// Open returns the store under stateDir.
func Open(stateDir string) *Store { return &Store{dir: filepath.Join(stateDir, DirName)} }

func (s *Store) kitDir(slug string) (string, error) {
	if !config.ValidName(slug) {
		return "", fmt.Errorf("brand %q: names are lower case letters, digits, '.', '_' or '-'", slug)
	}
	return filepath.Join(s.dir, slug), nil
}

// ErrNotFound is returned for an unknown kit or asset.
var ErrNotFound = errors.New("brand kit not found")

// ErrExists is returned when a kit of that slug already exists.
var ErrExists = errors.New("brand kit already exists")

// List returns every kit, sorted by name.
func (s *Store) List() ([]*Kit, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*Kit{}, nil
		}
		return nil, err
	}
	out := []*Kit{}
	for _, e := range entries {
		if !e.IsDir() || !config.ValidName(e.Name()) {
			continue
		}
		k, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Get loads one kit.
func (s *Store) Get(slug string) (*Kit, error) {
	dir, err := s.kitDir(slug)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "brand.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, slug)
		}
		return nil, err
	}
	k := &Kit{}
	if err := json.Unmarshal(b, k); err != nil {
		return nil, err
	}
	if k.Assets == nil {
		k.Assets = map[string]Asset{}
	}
	return k, nil
}

// Asset returns an asset's metadata and bytes.
func (s *Store) Asset(slug, name string) (*Asset, []byte, error) {
	k, err := s.Get(slug)
	if err != nil {
		return nil, nil, err
	}
	a, ok := k.Assets[name]
	if !ok {
		return nil, nil, fmt.Errorf("%w: asset %s of %s", ErrNotFound, name, slug)
	}
	dir, _ := s.kitDir(slug)
	b, err := os.ReadFile(filepath.Join(dir, a.File))
	if err != nil {
		return nil, nil, err
	}
	return &a, b, nil
}

// Save writes a kit's metadata.
func (s *Store) Save(k *Kit) error {
	dir, err := s.kitDir(k.Slug)
	if err != nil {
		return err
	}
	if k.Assets == nil {
		k.Assets = map[string]Asset{}
	}
	return config.WriteJSON(filepath.Join(dir, "brand.json"), k)
}

// Delete removes a kit and its files.
func (s *Store) Delete(slug string) error {
	dir, err := s.kitDir(slug)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "brand.json")); err != nil {
		return fmt.Errorf("%w: %s", ErrNotFound, slug)
	}
	return os.RemoveAll(dir)
}

var assetNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidAssetName reports whether s can name an asset.
func ValidAssetName(s string) bool { return assetNameRe.MatchString(s) }

// prepareAsset validates bytes as an image and describes them.
func prepareAsset(name, source string, b []byte) (Asset, error) {
	if !ValidAssetName(name) {
		return Asset{}, fmt.Errorf("asset name %q: lower case letters, digits, '_' or '-'", name)
	}
	typ, ext, err := ImageType(b)
	if err != nil {
		return Asset{}, err
	}
	a := Asset{Name: name, File: name + ext, Type: typ, Size: int64(len(b)), Source: source}
	sum := sha256.Sum256(b)
	a.SHA256 = hex.EncodeToString(sum[:])
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err == nil {
		a.Width, a.Height = cfg.Width, cfg.Height
	}
	return a, nil
}

// writeAsset stores an asset's bytes in the kit directory and records it in
// the kit (without saving the kit's metadata).
func (s *Store) writeAsset(k *Kit, a Asset, b []byte) error {
	dir, err := s.kitDir(k.Slug)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if old, ok := k.Assets[a.Name]; ok && old.File != a.File {
		os.Remove(filepath.Join(dir, old.File))
	}
	tmp := filepath.Join(dir, a.File+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, a.File)); err != nil {
		return err
	}
	k.Assets[a.Name] = a
	return nil
}

// RemoveAsset deletes one asset from a kit.
func (s *Store) RemoveAsset(slug, name string) (*Kit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := s.Get(slug)
	if err != nil {
		return nil, err
	}
	a, ok := k.Assets[name]
	if !ok {
		return nil, fmt.Errorf("%w: asset %s of %s", ErrNotFound, name, slug)
	}
	dir, _ := s.kitDir(slug)
	os.Remove(filepath.Join(dir, a.File))
	delete(k.Assets, name)
	k.UpdatedAt = time.Now().UTC()
	return k, s.Save(k)
}

// Edit changes the text fields of a kit. Empty values leave a field alone;
// colors must be #rrggbb, #rgb or rgb().
type Edit struct {
	Name    *string `json:"name,omitempty"`
	Tagline *string `json:"tagline,omitempty"`
	Primary *string `json:"primary,omitempty"`
	Accent  *string `json:"accent,omitempty"`
}

// Update applies an edit.
func (s *Store) Update(slug string, e Edit, actor string) (*Kit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := s.Get(slug)
	if err != nil {
		return nil, err
	}
	if e.Name != nil {
		n := Clean(*e.Name, 80)
		if n == "" {
			return nil, errors.New("name can not be empty")
		}
		k.Name = n
	}
	if e.Tagline != nil {
		k.Tagline = Clean(*e.Tagline, 200)
	}
	for _, c := range []struct {
		in  *string
		out *string
	}{{e.Primary, &k.Primary}, {e.Accent, &k.Accent}} {
		if c.in == nil {
			continue
		}
		if strings.TrimSpace(*c.in) == "" {
			*c.out = ""
			continue
		}
		v, ok := NormalizeColor(*c.in)
		if !ok {
			return nil, fmt.Errorf("color %q: use #rrggbb", *c.in)
		}
		*c.out = v
	}
	k.UpdatedAt = time.Now().UTC()
	k.Actor = actor
	return k, s.Save(k)
}

// FetchRequest asks for a kit built from a link.
type FetchRequest struct {
	URL     string `json:"url"`
	Slug    string `json:"slug,omitempty"`
	Name    string `json:"name,omitempty"`
	Replace bool   `json:"replace,omitempty"`
}

// Fetch builds a kit from a prospect's site: it reads the home page, picks
// the name, colors, logo and icon, downloads the images through the guarded
// client and stores everything under a slug.
func (s *Store) Fetch(ctx context.Context, c *webfetch.Client, req FetchRequest, actor string) (*Kit, error) {
	page, err := c.Get(ctx, req.URL, "text/html", "application/xhtml+xml")
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", req.URL, err)
	}
	p := ParsePage(page.URL, page.Body)
	host := strings.TrimPrefix(strings.ToLower(page.URL.Hostname()), "www.")
	slug := req.Slug
	if slug == "" {
		slug = SlugFor(host)
	}
	if !config.ValidName(slug) {
		return nil, fmt.Errorf("brand %q: names are lower case letters, digits, '.', '_' or '-'", slug)
	}
	if _, err := s.Get(slug); err == nil && !req.Replace {
		return nil, fmt.Errorf("%w: %s (fetch again with replace to refresh it)", ErrExists, slug)
	}
	k := &Kit{Slug: slug, Domain: host, URL: page.URL.String(), Assets: map[string]Asset{}, FetchedAt: time.Now().UTC(), Actor: actor}
	k.UpdatedAt = k.FetchedAt
	k.Name = Clean(firstNonEmpty(req.Name, p.Name, titleCase(strings.Split(host, ".")[0])), 80)
	k.Tagline = p.Description
	if len(p.Colors) > 0 {
		k.Primary = p.Colors[0]
	}
	if len(p.Colors) > 1 && p.Colors[1] != k.Primary {
		k.Accent = p.Colors[1]
	}
	type fetched struct {
		a Asset
		b []byte
	}
	got := map[string]fetched{}
	tryAssets := func(name string, candidates []string, max int) {
		tried := 0
		for _, u := range candidates {
			if tried >= max {
				break
			}
			tried++
			img, err := c.Get(ctx, u, "image/")
			if err != nil {
				k.Notes = append(k.Notes, fmt.Sprintf("%s candidate %s skipped: %v", name, shortURL(u), err))
				continue
			}
			a, err := prepareAsset(name, img.URL.String(), img.Body)
			if err != nil {
				k.Notes = append(k.Notes, fmt.Sprintf("%s candidate %s skipped: %v", name, shortURL(u), err))
				continue
			}
			got[name] = fetched{a, img.Body}
			return
		}
		k.Notes = append(k.Notes, "no usable "+name+" found")
	}
	tryAssets("logo", p.Logos, 5)
	tryAssets("icon", p.Icons, 3)
	if k.Primary == "" {
		for _, n := range []string{"logo", "icon"} {
			f, ok := got[n]
			if !ok {
				continue
			}
			if colors := DominantColors(f.b, 2); len(colors) > 0 {
				k.Primary = colors[0]
				if len(colors) > 1 && k.Accent == "" {
					k.Accent = colors[1]
				}
				k.Notes = append(k.Notes, "no theme color on the page; colors taken from the "+n)
				break
			}
		}
	}
	if k.Accent == "" && k.Primary != "" {
		k.Accent = Shade(k.Primary, 0.72)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Get(slug); err == nil && !req.Replace {
		return nil, fmt.Errorf("%w: %s (fetch again with replace to refresh it)", ErrExists, slug)
	}
	dir, _ := s.kitDir(slug)
	os.RemoveAll(dir)
	for _, n := range []string{"logo", "icon"} {
		if f, ok := got[n]; ok {
			if err := s.writeAsset(k, f.a, f.b); err != nil {
				return nil, err
			}
		}
	}
	return k, s.Save(k)
}

// AddAsset fetches an image from a link into an existing kit, for assets
// beyond the logo and icon: a hero image, a product shot, a partner badge.
func (s *Store) AddAsset(ctx context.Context, c *webfetch.Client, slug, name, link, actor string) (*Kit, error) {
	if !ValidAssetName(name) {
		return nil, fmt.Errorf("asset name %q: lower case letters, digits, '_' or '-'", name)
	}
	if _, err := s.Get(slug); err != nil {
		return nil, err
	}
	img, err := c.Get(ctx, link, "image/")
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", shortURL(link), err)
	}
	a, err := prepareAsset(name, img.URL.String(), img.Body)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := s.Get(slug)
	if err != nil {
		return nil, err
	}
	if err := s.writeAsset(k, a, img.Body); err != nil {
		return nil, err
	}
	k.UpdatedAt = time.Now().UTC()
	k.Actor = actor
	return k, s.Save(k)
}

// SlugFor turns a host name into a kit name: tidewell.com becomes tidewell,
// shop.tidewell.co.uk becomes shop-tidewell.
func SlugFor(host string) string {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	parts := strings.Split(host, ".")
	if len(parts) > 1 {
		parts = parts[:len(parts)-1]
		if n := len(parts); n > 1 && len(parts[n-1]) <= 3 && (parts[n-1] == "co" || parts[n-1] == "com" || parts[n-1] == "org" || parts[n-1] == "net" || parts[n-1] == "gov" || parts[n-1] == "ac") {
			parts = parts[:n-1]
		}
	}
	s := strings.Join(parts, "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = out[:63]
	}
	if out == "" {
		out = "brand"
	}
	return out
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func shortURL(s string) string {
	if u, err := url.Parse(s); err == nil {
		p := u.Path
		if len(p) > 40 {
			p = "..." + p[len(p)-37:]
		}
		return u.Host + p
	}
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
