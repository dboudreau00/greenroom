# Changelog

## 0.7.2

Fixes found while capturing the README tour.

- `saas-web`: the demo page now takes the prospect's colors; the default
  colors were declared after the brand stylesheet and always won.
  Environments created earlier need `brand.css` linked after the inline
  style in `web/index.html`.
- Portal: the prospect is not repeated next to a brand kit of the same
  name, and environment names and seed set chips stay on one line in demo
  tables.

## 0.7.1

Fixes to the 0.3.0 to 0.7.0 work.

- The earlier demo on an environment keeps it: a later demo's preparation
  waits while an earlier one is preparing, ready, prep-failed or live and
  not over, with one journal entry naming it, and starts late once that
  slot ends. A later demo that prepared first because of a longer lead goes
  back to scheduled when the earlier demo's preparation runs. Prepare now
  answers 409 while an earlier demo holds the environment. Manual resets
  are unchanged.
- A demo's pre-call check judges the seed set it booked, so a demo on
  another persona than the pin is no longer amber for the pin mismatch.
- SVG assets are checked by an XML token walk: allowlisted SVG elements by
  namespace and local name (a prefixed `script` is caught), editor
  metadata namespaces ignored, no entities, `on*`, `xml:base`, `src`,
  external `href` or `url()`. The string checks stay as a second layer.
- Dominant colors are skipped for images over 4,000,000 pixels or 8192 a
  side, read from the header before anything is decoded.
- Personalized file content (`copy`, `command` stdin `file`) only takes
  `{{brand.*}}` in `.html .htm .xml .svg .json .js .mjs .css .sql`; any
  other file type fails the step, as does an `http` body whose
  Content-Type is not JSON, form, HTML or XML. JSON and JS escaping also
  covers backtick and `$`.
- Templates refuse a param that points at a link-local or cloud metadata
  host (`169.254.0.0/16`, `fe80::/10`, `metadata.google.internal`, ...).
- Verify checks leave a `${NAME}` that is not set and has no default as
  written, so `sh -c` scripts keep their own variables.
- A prep run that ends while the scheduler looks at it is read again
  instead of reported as interrupted.
- Stored JSON files are written through a unique temp file per write,
  removed on failure.
- A debrief with two new gaps of the same title and kind is refused as
  listing the gap twice.
- The bundled example data uses a coined customer name.

## 0.7.0

Seed set authoring: compare two versions, bump to the next one.

- `greenroom seeds diff <env> <from> <to>`: files added, removed, changed
  and unchanged by content hash; a unified diff (Myers, three lines of
  context, CRLF normalized) per changed text file; binaries (NUL in the
  first 8 KiB or not UTF-8) by size and hash; a file diff capped at 2,000
  lines of output or 256 KiB of input, marked truncated. The manifest is
  compared by meaning: title, description, other top-level fields, and
  steps and verify checks added, removed or changed by name with the
  fields that differ. `--stat`, `--json`; exit 0 identical, 1 different.
  Personas may differ, environments may not.
- `greenroom seeds bump <env> <persona> [--from vN] [--title t] [--pin]`
  copies a set to the version after the highest on disk, built in a hidden
  directory and renamed into place once it loads with no new problems.
  `version` and `title` in the copy's `seed.json`, and `pin` in
  `environment.json` with `--pin`, are edited textually at the top level
  only, so comments and formatting survive. Refused when the target
  exists.
- `greenroom seeds <env>` lists as before; `diff` and `bump` as the first
  argument are the subcommands.
- Portal: "Compare with vN" on every seed set after a persona's first and
  "New version" on each persona. The comparison shows the file list with
  badges and sizes, the manifest summary and the line diffs in the dark log
  pane, with From and To pickers across every persona of the environment.
  A new version opens its comparison with the source.
- API: `GET /api/v1/environments/{env}/seeds/diff`,
  `POST /api/v1/environments/{env}/seeds/{persona}/bump`.

## 0.6.0

Demo debriefs and a gaps board.

- Debriefs: one per demo, stored in the demo file once the demo is
  `done`. Rating 1 to 5, what landed, what fell flat, what broke or was
  missing in the environment, up to 10 competitors, next steps, the
  outcome (kept in sync with the demo's) and up to 20 gaps with a severity,
  the detail and the prospect's quote. Text is cleaned and capped; a save
  adds a system journal entry.
- Gap registry under `state/_gaps/<id>.json`: title, kind, status, owner,
  notes, and for environment gaps the environment and template module,
  which must exist. A debrief links an existing gap or creates a new one;
  an unknown gap id fails the save and creates nothing.
- Mentions are derived from debriefs on every read. A gap's score is the
  sum over distinct prospects of the highest severity each gave (low 1,
  medium 2, high 3, blocker 5), with mention count, prospects, last
  mention, environments and personas.
- Suggestions: typing a gap title lists similar active gaps by word
  overlap (Jaccard, stop words removed) so people link instead of
  duplicating. Merging a gap rewrites every debrief reference, keeps the
  higher severity where a debrief names both, and hides the merged gap.
- Insights for a window (default 90 days): demos waiting for a debrief,
  average rating overall and per persona, outcomes, top gaps, gaps by kind,
  environment gaps by environment, competitors.
- Export: a Markdown spec (summary table, a section per kind, every
  mention's detail and quote, escaped table cells) or a CSV (RFC 4180,
  formula cells defused), as a dated attachment.
- `greenroom debriefs`, `greenroom debrief <id>`, `greenroom gaps`,
  `gaps show`, `gaps set`, `gaps merge`, `gaps export`.
- Portal: Debriefs in the top navigation with Queue, Debriefs, Gaps and
  Insights tabs; a gap page with mentions, edits and Merge into; a debrief
  form with rating buttons, suggestions and environment and module pickers;
  the debrief on the demo page; "N demos need a debrief" on the overview.
  The top navigation scrolls inside the bar and shortens on narrow screens.
- API: `/api/v1/debriefs`, `/api/v1/demos/{id}/debrief`, `/api/v1/gaps`
  with `suggest`, `export`, `merge`; `debriefsPending` on the overview,
  `debriefed` on demo short forms.

## 0.5.0

Demo bookings, a demo tracker and a demo journal.

- Demo records under `state/_demos/<id>.json`: prospect, environment,
  seed set, optional brand kit, time slot, stage, outcome, next step,
  attendees, status and a journal. Text is cleaned and capped.
- `greenroom book <env> --at ... --for 45m --title ...` books a demo.
  Starts at least a minute out, at most 12 hours long, no overlap with
  another booking on the environment; environment, seed set and brand kit
  must exist. `greenroom demo log` records a demo after the fact.
- The portal's scheduler prepares booked demos on its own ticker: a reset
  to the demo's seed set and brand kit `prepLead` before the start (default
  30m), a recorded pre-call check, then `ready`, `live` at the start,
  `done` at the end, and with `--reset-after` a reset to the pin with the
  house brand. Busy environments are retried; demos nobody prepared become
  `missed`. Every step lands in the journal and survives a restart.
- Live protection: while a booked demo is on stage, resets and snapshots
  of its environment are refused (409, "booked") unless `override` or
  `--override` is given; the override is journaled. Heals stay allowed.
- `greenroom demos`, `demo show`, `demo note`, `demo set`, `demo cancel`.
- Portal: Demos page with upcoming, past and all, an environment filter,
  book and log dialogs and an `.ics` export; a demo page with countdown,
  run links, Prepare now, Cancel, editable stage, outcome, next step and
  attendees, and the journal timeline. Upcoming demos on the overview,
  the next demo on environment cards, a live notice, a Book a demo button
  and a Demos tab on the environment page, and the override in the reset
  and snapshot dialogs.
- API: `/api/v1/demos` and its journal, `prepare` and `cancel`,
  `/api/v1/demos.ics` (RFC 5545), `override` in the reset and snapshot
  bodies, `nextDemo` and `liveDemo` on environment summaries.

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
