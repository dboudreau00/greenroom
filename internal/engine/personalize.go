package engine

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/dboudreau00/greenroom/internal/brand"
	"github.com/dboudreau00/greenroom/internal/seeds"
	"github.com/dboudreau00/greenroom/internal/targets"
	"github.com/dboudreau00/greenroom/internal/webfetch"
)

// Brands returns the brand kit store.
func (e *Engine) Brands() *brand.Store { return e.brands }

// Fetcher returns the guarded client for links people paste.
func (e *Engine) Fetcher() *webfetch.Client { return e.fetcher }

// FetchBrand builds a brand kit from a prospect's link.
func (e *Engine) FetchBrand(ctx context.Context, req brand.FetchRequest, actor string) (*brand.Kit, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return e.brands.Fetch(ctx, e.fetcher, req, actor)
}

// AddBrandAsset fetches one more image from a link into a kit.
func (e *Engine) AddBrandAsset(ctx context.Context, slug, name, link, actor string) (*brand.Kit, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return e.brands.AddAsset(ctx, e.fetcher, slug, name, link, actor)
}

// loadKit returns the kit for a reset, or nil when none was asked for.
func (e *Engine) loadKit(slug string) (*brand.Kit, error) {
	if slug == "" {
		return nil, nil
	}
	return e.brands.Get(slug)
}

// brandValues are the placeholder values of a reset: the kit's, or the
// environment's house brand.
func (e *Engine) brandValues(v *Env, kit *brand.Kit) brand.Values {
	name, tagline, domain, primary, accent := v.Config.DisplayTitle(), "", "", "", ""
	if b := v.Config.Brand; b != nil {
		name = firstNonEmpty(b.Name, name)
		tagline, domain, primary, accent = b.Tagline, b.Domain, b.Primary, b.Accent
	}
	return brand.Context(kit, name, tagline, domain, primary, accent)
}

// personalize renders a step's content when it opted in.
func personalize(st seeds.Step, vals brand.Values, content string, mode brand.Mode) (string, error) {
	if !st.Personalize {
		return content, nil
	}
	return vals.Render(content, mode)
}

// personalizeFile renders file content when the step opted in, escaped by
// the file's extension.
func personalizeFile(st seeds.Step, vals brand.Values, content, name string) (string, error) {
	if !st.Personalize {
		return content, nil
	}
	return vals.RenderFile(content, name)
}

// bodyMode picks the escaping for an http step body from its content type.
// A type with no escaping mode is refused, like file content: a fetched
// value is never put into a body raw.
func bodyMode(headers map[string]string) (brand.Mode, error) {
	for k, v := range headers {
		if strings.EqualFold(k, "Content-Type") {
			lv := strings.ToLower(v)
			switch {
			case strings.Contains(lv, "json"):
				return brand.JSON, nil
			case strings.Contains(lv, "x-www-form-urlencoded"):
				return brand.URL, nil
			case strings.Contains(lv, "html") || strings.Contains(lv, "xml"):
				return brand.HTML, nil
			}
			return brand.Raw, fmt.Errorf("personalize can not escape brand values for a %q body; use JSON, form, HTML or XML", v)
		}
	}
	return brand.JSON, nil
}

// assetStep copies a brand kit asset to a target. Without a kit, or when
// the kit lacks the asset, the step's fallback file is copied instead; with
// neither the step is skipped.
func (e *Engine) assetStep(ctx context.Context, v *Env, set *seeds.Set, st seeds.Step, kit *brand.Kit, expand func(string) string, rec *recorder) (string, error) {
	var data []byte
	var fileName, source string
	if kit != nil {
		if a, b, err := e.brands.Asset(kit.Slug, st.Asset); err == nil {
			data, fileName, source = b, a.File, "brand kit "+kit.Slug
		}
	}
	if data == nil && st.File != "" {
		b, err := set.ReadFile(st.File)
		if err != nil {
			return "", err
		}
		data, fileName, source = b, path.Base(st.File), "fallback "+st.File
	}
	if data == nil {
		rec.Warn(st.Name, "no %s in the brand kit and no fallback file; nothing copied", st.Asset)
		return "skipped: no " + st.Asset, nil
	}
	dest := expand(st.Dest)
	if strings.HasSuffix(dest, "/") {
		dest += fileName
	}
	cp, err := v.Registry.Copier(st.Target)
	if err != nil {
		return "", err
	}
	rec.Info(st.Name, "copying %s (%s, %d bytes) to %s:%s", st.Asset, source, len(data), firstNonEmpty(st.Target, "local"), dest)
	spec := targets.RunSpec{Container: st.Container, Pod: st.Pod, Namespace: st.Namespace, InstanceID: st.InstanceID, Timeout: st.Timeout.D()}
	if err := cp.CopyTo(ctx, spec, dest, data, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s from %s", fileName, source), nil
}
