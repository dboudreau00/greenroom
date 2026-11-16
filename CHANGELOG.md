# Changelog

## 0.4.0

Personalization.

- Brand kits: `greenroom brand fetch <url>` reads a prospect's home page
  and stores the name, tagline, primary and accent colors, logo and icon
  under `state/_brands/<slug>/`. `brand set` edits the text and colors,
  `brand asset` adds more images, `brand rm` deletes; `greenroom brands`
  lists them.
- `greenroom reset <env> --brand <slug>` fills `{{brand.*}}` placeholders
  in steps that set `personalize`, after `${VAR}` expansion and escaped
  for where the value lands (SQL literal, JSON, HTML, CSS, URL query).
  Shell steps get `GREENROOM_BRAND_*` variables instead. Without a kit the
  environment's `brand` block, or its title and the house colors, are used.
- New `asset` step copies a kit image to a target, with a seed file as the
  fallback.
- Guarded link fetcher: public addresses on ports 80 and 443 only, checked
  at connect time, no proxy, re-validated redirects, 15s and 2 MiB caps.
  Images are typed by content; unsafe SVG is refused. Workspace
  `linkFetch.allowPrivate` admits internal sites.
- Stale codes `brand-changed` and `brand-missing`; runs and the applied
  record carry the kit.
- Portal: Brands page, Personalize for in the reset dialog, brand on the
  applied stat, run list and run viewer.
- API: `/api/v1/brands` and its assets, `brand` in the reset body.
- `saas-web` writes `brand.css`, `brand.json` and the logo into the web
  container, and its page uses them.

## 0.3.0

Environment templates.

- `greenroom templates` and `greenroom new <env> --template <name> --modules a,b`
  create an environment from a pre-rolled template for a product type, with
  only the product modules the demo will show. Each module adds its
  dependencies, drift checks, seed steps and verify checks; each persona
  becomes a seed set.
- Built-in templates: `saas-web` (Docker Compose, five modules),
  `warehouse-bastion` (EC2 bastion and SSM, three data marts) and
  `k8s-microservices` (Kubernetes or EKS, four services). Workspace templates
  under `templates/` add to or replace them.
- Portal: New environment dialog with template cards, module checkboxes,
  persona and settings; template and modules shown on cards and headers.
- API: `GET /api/v1/templates`, `POST /api/v1/environments`.
- Drift checks take `personas` to apply only to the seed sets that create
  their data.

## 0.2.0

Portal redesign: stage-light visual system, dark and light themes,
CSS-driven motion.

## 0.1.1

Review fixes: shared environment lock across reloads, copy steps create the
target directory, zero-replica workloads count as down, terminating pods
skipped for exec, failed resets and vanished seed data reported as stale.

## 0.1.0

First release: versioned seed sets, resets, pre-call checks, stale report,
portal, Docker, Kubernetes, EC2, SSH and SQL targets.
