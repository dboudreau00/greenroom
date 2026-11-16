# Personalizing a demo

A reset can dress the demo in the prospect's brand: their name in the
header, their colors, their logo. Greenroom fetches a brand kit from the
prospect's website once, stores it, and fills it into the seed steps that
opt in when you reset with `--brand`.

```bash
greenroom brand fetch https://larkspur.example      # kit "larkspur"
greenroom reset acme-crm --brand larkspur
greenroom reset acme-crm                            # back to the house brand
```

## Brand kits

A kit is a name, a tagline, two colors and a set of images, stored as plain
files in the state directory:

```
state/_brands/larkspur/
  brand.json       slug, name, domain, url, tagline, primary, accent, asset list, notes
  logo.png
  icon.ico
```

`greenroom brand fetch <url>` reads the home page and picks:

| Field | Taken from, first match wins |
| --- | --- |
| name | `og:site_name`, `application-name`, JSON-LD `Organization` name, `<title>`, then the domain |
| tagline | `og:description`, `description`, `twitter:description` |
| primary, accent | `theme-color`, `msapplication-TileColor`, `mask-icon` color; with none of them, the dominant colors of the logo or icon; a missing accent is a darker shade of the primary |
| logo | JSON-LD logo, an `<img>` whose class, id, alt or src mentions "logo", the declared icons (SVG first, then largest), `og:image`, `/favicon.ico` |
| icon | the declared icons (SVG first, then largest), then `/favicon.ico` |

Each image candidate is fetched in turn until one is usable. Anything
skipped is recorded in the kit's notes (`greenroom brand show <slug>`), so
you can see why a kit has no logo.

The slug comes from the domain (`shop.tidewell.example` becomes
`shop-tidewell`) unless `--slug` sets it. `--name` overrides the fetched
name. Fetching a slug that exists fails unless `--replace` is given.

### What is stored, and how it is cleaned

- **Images are typed by content**, never by the server's `Content-Type`
  or the file extension: PNG, JPEG, GIF, WebP, ICO or SVG, at most 1 MiB.
  Anything else is refused.
- **SVG is parsed as XML** and refused unless it holds up token by token.
  The root is `svg` in the SVG namespace. Elements must be on an
  allowlist (shapes, `g`, `defs`, `symbol`, `use`, `text`, `tspan`,
  `textPath`, gradients, `clipPath`, `mask`, `pattern`, `marker`,
  `filter` and the `fe*` primitives, `style`, `image`, `switch`, `title`,
  `desc`, `metadata`); `script`, `a`, `foreignObject`, `set`, `animate*`
  and anything in the XHTML, MathML or an unknown namespace are refused.
  Elements are matched by namespace and local name, never by prefix, so
  `<s:script>` with `s` bound to the SVG namespace is caught. Inkscape,
  sodipodi, RDF, Dublin Core, Creative Commons and Adobe editor metadata
  is allowed and ignored. Also refused: entity declarations or a DTD
  internal subset, processing instructions other than `<?xml?>`, event
  handler attributes (`on*`), `xml:base`, `src`, any `href` that is not a
  `#fragment` or a `data:image/(png|jpeg|gif|webp);base64` image, and in
  any attribute or `<style>` a `url()` that is not `url(#...)`,
  `@import`, `expression(` or `javascript:` (a CSS escape in style text
  too). The earlier string checks still run as a second layer. At most
  256 KiB.
- **Dominant colors** are only read from images up to 4,000,000 pixels
  and 8192 pixels a side; the size comes from the header, so a small file
  that would inflate to a huge bitmap is never decoded.
- **Text is cleaned** before it is stored: control characters and
  backslashes are removed, whitespace collapsed, the name capped at 80
  characters and the tagline at 200.
- **Colors** are normalized to `#rrggbb`. `#rgb` and `rgb()` are accepted
  on input.

### Editing

```bash
greenroom brand set larkspur --name "Larkspur Freight" --tagline "Cold chain, on time" --primary "#0e7490"
greenroom brand asset larkspur hero https://larkspur.example/img/hero.webp
greenroom brand rm larkspur hero          # one asset
greenroom brand rm larkspur               # the whole kit
```

Asset names are lower case letters, digits, `_` and `-`, up to 32
characters. `logo` and `icon` are the two a fetch fills; any other name is
yours to use in `asset` steps.

The kit hash covers the text fields and the digest of every asset. A reset
records it, so an edit or re-fetch afterwards shows up as stale.

## The link fetcher

Every URL a person pastes, for a kit or an asset, goes through one guarded
client (`internal/webfetch`). It exists so a link can not be turned into a
request against the network Greenroom runs in.

- `http` and `https` only, no credentials in the link, ports 80 and 443
  only.
- The address actually dialled is checked at connect time, after DNS, so a
  name that resolves or re-resolves to a non-public address is refused:
  loopback, private, link-local (including `169.254.169.254`), CGNAT
  (`100.64.0.0/10`), NAT64, 6to4, unique-local, documentation, benchmark
  and the other IANA special-purpose ranges.
- No proxy from the environment, which would hide the real destination.
- At most 5 redirects, each validated like the first link.
- 15 second timeout, 2 MiB response cap. Pages must be HTML; assets must
  be images.

A kit for a prospect site hosted inside your network needs the workspace
to allow it in `greenroom.json`:

```json
"linkFetch": { "allowPrivate": true, "timeout": "20s" }
```

`allowPrivate` lifts the address and port checks. The scheme,
credential, redirect and size rules still apply. Leave it off on a portal
other people can reach.

## Placeholders

A step with `"personalize": true` gets `{{brand.<key>}}` filled in its
content when the reset runs:

| Key | Value with a kit | Value without one |
| --- | --- | --- |
| `name` | kit name | `brand.name` of the environment, else its title |
| `slug` | kit slug | `default` |
| `domain` | the site's host, without `www.` | `brand.domain` of the environment |
| `url` | the page the kit was fetched from | empty |
| `tagline` | kit tagline | `brand.tagline` of the environment |
| `primary` | kit primary color | `brand.primary`, else `#22c55e` |
| `accent` | kit accent color | `brand.accent`, else a shade of the primary, else `#15803d` |
| `logo` | file name of the kit's `logo` asset, such as `logo.png` | empty |
| `icon` | file name of the kit's `icon` asset | empty |

An unknown key such as `{{brand.colour}}` fails the step instead of
shipping a literal placeholder.

Placeholders are filled after `${VAR}` expansion, so a value that came from
a prospect's website is never expanded itself: a tagline of `${AWS_SECRET}`
stays that text.

### Escaping

The value is escaped for where it lands. Write the placeholder bare inside
the construct shown, not wrapped in your own quotes where the table says so.

| Step and field | Escaping | Write |
| --- | --- | --- |
| `sql` file or inline `sql` | a complete quoted SQL literal, `'` doubled, backslashes dropped | `INSERT INTO org(name) VALUES ({{brand.name}});` |
| `http` `url` | query-escaped | `?company={{brand.name}}` |
| `http` `body` or `bodyFile` | by `Content-Type`: JSON string content (the default; backtick and `$` as `\u0060` and `\u0024` as well), form-urlencoded, HTML/XML escaped; any other type fails the step | `{"company": "{{brand.name}}"}` |
| `http` `headers` | raw (cleaned text) | `X-Demo-For: {{brand.slug}}` |
| `command` argv | raw: each element is one argument, no shell | `["seed-tool", "--org", "{{brand.name}}"]` |
| `command` `file` (stdin) | by the file's extension, as for `copy` | |
| `copy` content | by the extension of `dest`: `.html` `.htm` `.xml` `.svg` HTML-escaped, `.json` `.js` `.mjs` JSON string content with backtick and `$` escaped as `\u0060` and `\u0024` (safe inside a JS template literal), `.css` CSS string escapes, `.sql` SQL literal | `"name": "{{brand.name}}"` in a `.json` file |
| `copy` `dest` | plain file names only: a value with a separator, a dot segment or a leading dot fails the step | `/srv/brand/{{brand.slug}}.json` |

File content only takes placeholders in those file types. A `copy` to, or
a `command` stdin `file` named, `.sh`, `.env`, `.yml`, `.yaml`, `.conf`,
`.py`, `.txt`, `.md` or anything else with no escaping mode fails the step
when the content holds a `{{brand.*}}` placeholder: a value fetched from a
prospect's website inserted raw there could close a quote and run. Use one
of the supported file types, or read the `GREENROOM_BRAND_*` variables
from a script. The same content without placeholders is copied as is.

`shell` steps are never templated: a script assembled from fetched text is
too easy to get wrong. Every `shell` and `command` step gets the values as
environment variables instead, which a script quotes like any other
variable:

| Variable | Key |
| --- | --- |
| `GREENROOM_BRAND_NAME` | `name` |
| `GREENROOM_BRAND_SLUG` | `slug` |
| `GREENROOM_BRAND_DOMAIN` | `domain` |
| `GREENROOM_BRAND_URL` | `url` |
| `GREENROOM_BRAND_TAGLINE` | `tagline` |
| `GREENROOM_BRAND_PRIMARY` | `primary` |
| `GREENROOM_BRAND_ACCENT` | `accent` |
| `GREENROOM_BRAND_LOGO` | `logo` |
| `GREENROOM_BRAND_ICON` | `icon` |

```sh
psql -v org="$GREENROOM_BRAND_NAME" -c "UPDATE settings SET org_name = :'org'"
```

`personalize` on a `shell`, `wait` or `asset` step is a validation error.

## The `asset` step

Copies an image from the kit to a target:

```json
{ "name": "brand-logo", "kind": "asset", "asset": "logo", "target": "docker",
  "container": "acme-crm-web", "file": "seed/brand/logo.svg", "dest": "/srv/brand/" }
```

| Field | Meaning |
| --- | --- |
| `asset` | Kit asset name: `logo`, `icon` or one added with `brand asset`. |
| `target`, location fields, `dest` | As for `copy`. A `dest` ending in `/` keeps the file name (`logo.png`, `logo.svg`). |
| `file` | Fallback from the seed set, copied when no kit was chosen or the kit lacks the asset. |

With neither a kit asset nor a fallback the step is skipped with a warning.
Pair it with `{{brand.logo}}` in a personalized file so the page knows
which file name to load.

## The house brand

Without `--brand`, placeholders are filled from the environment's `brand`
block, so the same seed set renders your own branding:

```json
"brand": { "name": "Quillmark", "tagline": "Ledgers that close themselves", "domain": "quillmark.example",
           "primary": "#7c3aed", "accent": "#5b21b6" }
```

Every field is optional; colors must be `#rrggbb`. With no block at all the
name is the environment title and the colors are Greenroom green.

## Staleness

The applied record stores the kit's slug, name and hash. `greenroom stale`
adds:

| Code | Severity | Meaning |
| --- | --- | --- |
| `brand-changed` | stale | The kit was edited or re-fetched since the reset. |
| `brand-missing` | stale | The kit used by the reset was deleted. |

A reset without a kit records no brand, so neither code applies to the
house brand.

## Portal and API

**Brands** in the top navigation lists the kits, fetches a new one from a
link, edits name, tagline and colors, adds and removes assets, and deletes
a kit after a confirmation. The reset dialog has a **Personalize for**
select; the brand shows on the applied stat, the run list and the run
viewer.

The API is in [API.md](API.md): `/api/v1/brands` and the `brand` field of
a reset.

## Worked example: `saas-web`

The `saas-web` template ships a personalized web front end. Its base
`after` steps write three files into the web container, outside the
read-only site mount, where nginx serves them at `/brand/`:

```json
{ "name": "brand-css",  "kind": "copy",  "target": "docker", "container": "{{envSlug}}-web",
  "file": "seed/brand/brand.css",  "dest": "/srv/brand/brand.css",  "personalize": true },
{ "name": "brand-json", "kind": "copy",  "target": "docker", "container": "{{envSlug}}-web",
  "file": "seed/brand/brand.json", "dest": "/srv/brand/brand.json", "personalize": true },
{ "name": "brand-logo", "kind": "asset", "asset": "logo", "target": "docker", "container": "{{envSlug}}-web",
  "file": "seed/brand/logo.svg", "dest": "/srv/brand/" }
```

`seed/brand/brand.css` is escaped as CSS, `seed/brand/brand.json` as JSON:

```css
:root {
  --brand: {{brand.primary}};
  --brand-accent: {{brand.accent}};
}
```

```json
{ "name": "{{brand.name}}", "tagline": "{{brand.tagline}}", "logo": "{{brand.logo}}" }
```

The page links `/brand/brand.css`, reads `/brand/brand.json`, sets the
text with `textContent`, and loads `/brand/<logo>` or the fallback
`logo.svg` when `logo` is empty.

```bash
greenroom new tidewell-crm --template saas-web --modules billing,support
docker compose -f environments/tidewell-crm/docker-compose.yml up -d
greenroom brand fetch https://tidewell.example
greenroom reset tidewell-crm --brand tidewell      # "Prepared for Tidewell", their logo and colors
greenroom brand set tidewell --tagline "Harbor logistics"
greenroom stale tidewell-crm                       # brand-changed
greenroom reset tidewell-crm                       # house brand, fallback logo
```
