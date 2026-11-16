# Debriefs and gaps

After a demo the sales engineer writes a debrief: a rating, what landed,
what fell flat, what broke in the demo environment, the competitors that
came up, next steps, the outcome, and the gaps the prospect asked about.
Gaps live in a workspace registry. The gaps board rolls up every debrief
that mentions them, so what most prospects asked for, at the highest
severity, comes first; it exports as a Markdown spec or a CSV.

```bash
greenroom debriefs                                # demos waiting for a debrief, ratings, top gaps
greenroom debrief 20261120-1500-3f9a --rating 4 --highlights "Billing flow landed" \
  --gap "feature:high:Scheduled exports" --gap "environment:medium:Orders seed has no EMEA customers"
greenroom gaps --status active
greenroom gaps export --format md --out gaps.md
```

## The debrief

One per demo, stored in the demo file as `debrief`
(`state/_demos/<id>.json`). It opens once the demo is `done`: a booked
demo when the scheduler ends it, a logged demo at once. A debrief for any
other status is refused (`409`).

| Field | Meaning |
| --- | --- |
| `rating` | 1 to 5. Required. |
| `highlights` | What landed. Up to 4000 characters, line breaks kept. |
| `lowlights` | What fell flat. Up to 4000 characters. |
| `environmentNotes` | What broke or was missing in the demo environment itself. Up to 4000 characters. |
| `competitors` | Up to 10 names of 60 characters; repeats in any case are dropped. |
| `nextSteps` | Up to 4000 characters. |
| `outcome` | The demo outcomes: `positive`, `neutral`, `negative`, `no-show`, `rescheduled`. Empty keeps the demo's outcome; set, it becomes the demo's outcome. Changing the demo's outcome later updates the debrief too. |
| `gaps` | Up to 20 gap references, below. |
| `submittedBy`, `submittedAt` | Who wrote it first, and when. Kept on edits. |
| `updatedBy`, `updatedAt` | The last save. |

Text is cleaned like the rest of a demo record (control characters,
bidirectional overrides and invisible separators dropped). Text over a
limit is refused, not cut.

A save writes the whole debrief and adds a system journal entry to the
demo: `debrief submitted by alice: rating 4/5, 2 gaps (1 new)`, or
`debrief updated by bob: ...` on a later save.

### Gap references

| Field | Meaning |
| --- | --- |
| `gapId` | The gap in the registry. |
| `severity` | `low`, `medium`, `high` or `blocker`. Required. |
| `detail` | What exactly they asked. Up to 1000 characters. |
| `quote` | Optional: the prospect's words. Up to 1000 characters. |

In a save, each entry either links an existing gap with `gapId` or
describes a new one with `new: {title, kind, env, module}`. New gaps are
created in the registry first. The save fails closed:

- an unknown gap id fails the whole save, and nothing is created;
- an entry with both `gapId` and `new`, or neither, fails;
- the same gap listed twice fails;
- linking a merged gap links the gap it was merged into;
- gaps created for a save that then fails are removed again.

The portal form is at `#/demos/<id>/debrief`, reached from the **Debrief**
button on the page of a demo that is done and from the queue. Rating is
five buttons, competitors a comma separated field. Each gap row has a
title with suggestions, a kind (with environment and module pickers for
environment gaps), a severity, the detail and the quote. Errors show inside
the form. The demo page shows the submitted debrief: rating, outcome, the
text, competitors and the gaps with links to the board.

## Gaps

The registry is one file per gap, written atomically under the store lock:

```
state/_gaps/<id>.json
```

Ids are `gap-` and eight hex characters and are checked against
`^gap-[0-9a-f]{8}$` before they touch a path. `_gaps` can not collide with
an environment, whose names start with a letter or digit.

| Field | Meaning |
| --- | --- |
| `title` | Up to 120 characters. |
| `kind` | `feature`, `data`, `integration`, `environment`, `content`, `performance`, `security`, `other`. |
| `status` | `open` (default), `planned`, `in-progress`, `done`, `wont-do`. |
| `owner` | Up to 80 characters. |
| `notes` | Up to 4000 characters, line breaks kept. A merge appends a line. |
| `env`, `module` | Environment gaps only: an environment that exists, and optionally one of the template modules it was built with (`template.modules` in its environment file). Anything else is refused. A gap that changes kind drops both. |
| `mergedInto` | Set by a merge. |
| `createdAt`, `updatedAt`, `createdBy` | Bookkeeping. |

Mentions are not stored with the gap. Every read of the board scans the
debriefs in the demo files; the debrief is the only source of truth.

### Link instead of duplicating

While someone types a gap title, the portal asks
`GET /api/v1/gaps/suggest?q=` (debounced) and offers similar gaps to link.
Both titles are lowercased and split on anything that is not a letter or
digit; stop words and one-letter words are dropped and a plural `s` is
trimmed. The score is the Jaccard overlap of the two word sets. Suggestions
are gaps that are `open`, `planned` or `in-progress` and not merged, with a
score of at least 0.3, best first, at most five.

`Scheduled exports to S3` against `scheduled export`: {scheduled, export,
s3} and {scheduled, export} share two of three words, 0.67.

A new gap whose words match an existing unmerged gap of the same kind
exactly (after the same normalization) is linked to that gap rather than
created again, so the CLI's `--gap` and the API's `new` do not split one ask
across two rows.

## Scores

| Severity | Weight |
| --- | --- |
| `low` | 1 |
| `medium` | 2 |
| `high` | 3 |
| `blocker` | 5 |

A gap's score is the sum, over distinct prospects, of the highest weight
that prospect gave. Prospects match case-insensitively; a demo with no
prospect counts as a prospect of its own. Asking again does not inflate a
score, more prospects asking does.

| Demo | Prospect | Severity |
| --- | --- | --- |
| intro | Larkspur | low |
| deep dive | Larkspur | high |
| discovery | Tidewell | medium |

Score 3 + 2 = 5, from 2 prospects and 3 mentions.

Each board row also has the mention count, distinct prospects, the top
severity, the last mention (the latest demo start), and the environments
and personas of the demos. The board sorts by score, then prospects,
mentions, last mention and title; the portal can sort by score, mentions,
prospects or last mention.

## Merging

`greenroom gaps merge <id> <into>`, `POST /api/v1/gaps/{id}/merge` with
`{"into": "gap-..."}`, or **Merge into...** on the gap page:

1. The source gets `mergedInto`, status `wont-do` and a note.
2. The target gets a note naming the source.
3. Every debrief that names the source is rewritten to name the target.
   Where a debrief names both, they collapse into the one with the higher
   severity, which keeps the other's detail or quote if it has none. Each
   rewritten demo gets a system journal entry.

A source that is already merged, or a target that was merged elsewhere, is
refused (`409`); merging into itself or into an unknown gap is bad input
(`400`). Merged gaps are hidden from the board unless `includeMerged=true`
(`--merged`) and can not be edited. Reads follow `mergedInto`, so a
debrief saved while a merge ran still counts toward the target.

## Insights

`greenroom debriefs` and `GET /api/v1/debriefs` cover demos that started
inside the window: `since` takes `30d`, `720h`, `all`, a date
(`2026-09-01`) or RFC 3339, default 90 days.

| Value | Meaning |
| --- | --- |
| `pending` | Demos that are `done` with no debrief, oldest first. A `no-show` is not pending. |
| `submitted` | Debriefs, newest first, with rating, gap count and who submitted. |
| `averageRating`, `ratingByPersona` | Overall and per seed persona. |
| `outcomes` | Count per debrief outcome. |
| `mentions`, `gapsMentioned` | Gap mentions and distinct gaps in the window. |
| `topGaps` | The ten highest scores, from mentions in the window only. |
| `gapsByKind` | Distinct gaps mentioned, per kind. |
| `environmentGaps` | Environment gaps per environment: the gap's `env`, or the demo's environment when the gap names none. |
| `competitors` | Names and how many debriefs mention them. |

The overview shows `N demos need a debrief` with a link to the queue when
any are pending in the default window.

## Export

`GET /api/v1/gaps/export?format=md|csv&status=` or
`greenroom gaps export [--format md|csv] [--status s] [--out file]`.
Merged gaps are left out; `status` takes a status or `active`. The API
answers with `Content-Disposition: attachment;
filename="greenroom-gaps-2026-11-20.md"` (or `.csv`).

**Markdown** is a spec-ready document: a title, when it was generated, a
summary table (rank, gap, kind, score, prospects, mentions, status), then
one section per kind with one entry per gap: id, status and owner, the
score line, environment and module, the environments demoed on, personas,
notes as a quote, and every mention with prospect, severity, date, demo,
environment, detail and quote. Table cells escape backslashes and pipes and
fold line breaks into spaces; `<` is written as `&lt;` everywhere, so free
text can not open HTML in a renderer.

**CSV** has one row per gap: `rank, id, title, kind, status, score,
prospects, mentions, top_severity, last_mentioned, environments, personas,
owner, env, module, notes`. RFC 4180 quoting, CRLF line ends. A cell that
starts with `=`, `+`, `-`, `@`, a tab or a carriage return gets a leading
single quote, so a spreadsheet shows it as text instead of running it.

## Portal

**Debriefs** in the top navigation has a window picker and four tabs:

| Tab | What it shows |
| --- | --- |
| Queue | Demos waiting for a debrief, oldest first, with a **Debrief** button each. |
| Debriefs | Submitted debriefs, newest first: demo, prospect with the brand kit's logo, rating, outcome, gap count. |
| Gaps | The board: filters by kind, status and environment, merged gaps on request, sortable score, prospects, mentions and last mention columns, and **Export .md** and **Export .csv**. A row opens the gap. |
| Insights | Stat tiles and bars for ratings, outcomes, top gaps, kinds, environment gaps and competitors. |

The gap page (`#/gaps/<id>`) shows the score, prospects, mentions and top
severity, every mention with a link to its demo, the editable title, kind,
status, owner, notes and environment and module, and **Merge into...**.

## CLI

| Command | What it does |
| --- | --- |
| `greenroom debriefs [--since 30d] [--json]` | The queue and the insights summary. |
| `greenroom debrief <demo-id> --rating 4 [--highlights t] [--lowlights t] [--env-notes t] [--next-steps t] [--outcome o] [--competitor name ...] [--gap "kind:severity:title" ...] [--gap-id "id:severity[:detail]" ...]` | Write or update a debrief. On a demo that has one, flags not given keep what is stored, `--competitor` replaces the list, `--gap` adds a new gap and `--gap-id` links one or replaces its entry. A new environment gap points at the demo's environment. |
| `greenroom gaps [--status s] [--kind k] [--env e] [--merged] [--json]` | The board. |
| `greenroom gaps show <id> [--json]` | One gap and its mentions. |
| `greenroom gaps set <id> [--status s] [--owner o] [--notes t] [--title t] [--kind k] [--env e] [--module m]` | Edit. |
| `greenroom gaps merge <id> <into>` | Merge. |
| `greenroom gaps export [--format md\|csv] [--status s] [--out file]` | Export to stdout or a file. |

## API

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/debriefs?since=90d` | `{"since","pending","submitted","insights"}`. 400 for a bad `since`. |
| GET | `/api/v1/demos/{id}/debrief` | `{"demo": short form, "debrief": ... or null, "gaps": {id: gap}}` with the gaps it references. 404 for an unknown demo. |
| PUT | `/api/v1/demos/{id}/debrief` | Write the debrief. Body `{"rating":4,"highlights":"...","lowlights":"...","environmentNotes":"...","nextSteps":"...","competitors":["Quillmark"],"outcome":"positive","gaps":[{"gapId":"gap-3f9a1c2b","severity":"high","detail":"...","quote":"..."},{"new":{"title":"Seed EMEA customers","kind":"environment","env":"acme-crm","module":"billing"},"severity":"medium"}]}`. 200 with the same shape as the GET; 400 for bad input or an unknown gap, environment or module; 404 for an unknown demo; 409 when the demo is not done. |
| GET | `/api/v1/gaps?status=&kind=&env=&includeMerged=` | Board rows: the gap with `score`, `mentions`, `prospects`, `topSeverity`, `lastMentioned`, `environments`, `personas`. `status=active` is open, planned and in-progress. |
| POST | `/api/v1/gaps` | Create. Body `{"title":"...","kind":"feature","status":"open","owner":"...","notes":"...","env":"...","module":"..."}`. 201. |
| GET | `/api/v1/gaps/suggest?q=` | Up to five similar active gaps with a `score`. |
| GET | `/api/v1/gaps/export?format=md\|csv&status=` | The export, as an attachment. |
| GET | `/api/v1/gaps/{id}` | `{"gap": row, "mentions": [...], "mergedFrom": [ids]}`. 404 when unknown. |
| PATCH | `/api/v1/gaps/{id}` | Any of `title`, `kind`, `status`, `owner`, `notes`, `env`, `module`. 409 for a merged gap. |
| POST | `/api/v1/gaps/{id}/merge` | Body `{"into":"gap-..."}`. 200 with `{"gap": source, "demos": rewritten}`; 404 for an unknown source, 400 for an unknown target or itself, 409 when either side is merged. |

Every route needs the token like the rest of the API, and mutating
requests made with the session cookie send `X-Requested-With: greenroom`.
