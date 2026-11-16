# HTTP API

Base path `/api/v1`. Authenticate with `Authorization: Bearer <token>` or
the portal session cookie. Responses are JSON. Errors are
`{"error": "message"}` with a 4xx or 5xx status.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Liveness, no auth. `{"ok":true,"version":...,"environments":n}` |
| GET | `/api/v1/me` | Actor and whether auth is on. |
| GET | `/api/v1/overview` | Every environment's summary card. |
| POST | `/api/v1/reload` | Re-read environment and seed files. |
| GET | `/api/v1/templates` | Environment templates with their modules, personas and params. |
| POST | `/api/v1/environments` | Create an environment from a template. Body `{"template":"saas-web","name":"acme-crm","title":"Acme CRM","modules":["billing"],"persona":"cfo","params":{"webPort":"8096"},"dryRun":false}`. 201 with the plan, 200 for a dry run, 409 when the name is taken. |
| GET | `/api/v1/environments/{env}?drift=true` | Full detail: dependencies with uptime, seed sets, runs, snapshots, heals, stale report, targets. |
| POST | `/api/v1/environments/{env}/check?record=true&drift=true` | Run the pre-call check now; returns the report. |
| GET | `/api/v1/environments/{env}/stale?drift=true` | Stale report only. |
| POST | `/api/v1/environments/{env}/reset` | Body `{"persona":"cfo","version":2,"force":false,"skipReset":false,"brand":"larkspur","override":false}`. `brand` is a brand kit slug; empty uses the house brand. Returns `{"runId":...}` with 202. 409 when the environment is busy or a booked demo is live on it (`override` goes ahead and notes it in the demo's journal), 400 for an unknown brand kit. |
| POST | `/api/v1/environments/{env}/snapshot` | Body `{"dependency":"postgres","override":false}`. Returns `{"runId":...}`. 409 when busy or a booked demo is live. |
| POST | `/api/v1/environments/{env}/dependencies/{dep}/heal` | Returns `{"runId":...}`. |
| GET | `/api/v1/environments/{env}/seeds/diff?from=cfo@v1&to=cfo@v2` | Compare two seed sets of the environment (personas may differ; a bare persona means its latest): `identical`, `stat`, `files` with status, sizes, hashes and unified `hunks` for changed text files, and a `manifest` summary of title, description, steps and verify. 400 for a missing or bad reference, 404 for an unknown set. |
| POST | `/api/v1/environments/{env}/seeds/{persona}/bump` | Copy a seed set to the next version. Body `{"from":2,"title":"CFO persona: Tidewell close","pin":true}`, all optional; `from` 0 or absent copies the latest. 201 with `{"id":"cfo@v3","from":"cfo@v2","dir":...,"pinned":true}`, plus `warning` when the copy was made but the pin could not be written. 404 for an unknown environment, persona or version, 400 for an invalid persona or title, 409 when the target version exists. |
| GET | `/api/v1/environments/{env}/targets` | Pings every target. |
| GET | `/api/v1/environments/{env}/runs?limit=50` | Run summaries, running ones first. |
| GET | `/api/v1/environments/{env}/runs/{id}` | Full run with steps and log. |
| GET | `/api/v1/environments/{env}/runs/{id}/events` | Server-sent events: `snapshot` (run so far), then `log`, `step`, `done`. |
| GET | `/api/v1/brands` | Brand kits, sorted by name, each with its `hash`. |
| POST | `/api/v1/brands` | Fetch a kit from a link. Body `{"url":"https://larkspur.example","slug":"larkspur","name":"Larkspur","replace":false}`; only `url` is required. 201 with the kit, 409 when the slug exists and `replace` is false, 400 for a blocked or unusable link. |
| GET | `/api/v1/brands/{slug}` | One kit. 404 when unknown. |
| PATCH | `/api/v1/brands/{slug}` | Body `{"name":...,"tagline":...,"primary":"#0e7490","accent":...}`; absent fields are left alone, an empty color clears it. |
| DELETE | `/api/v1/brands/{slug}` | Delete a kit and its files. |
| POST | `/api/v1/brands/{slug}/assets` | Fetch one more image. Body `{"name":"hero","url":"https://larkspur.example/hero.webp"}`. 201 with the kit. |
| GET | `/api/v1/brands/{slug}/assets/{name}` | The image, served with the content type decided when it was stored, `nosniff` and a sandboxing `Content-Security-Policy`. |
| DELETE | `/api/v1/brands/{slug}/assets/{name}` | Remove one asset. |
| GET | `/api/v1/demos?env=&scope=upcoming\|past\|all&limit=` | Demos in short form. `upcoming` is the default. |
| POST | `/api/v1/demos` | Book a demo, or log a past one with `"booked": false`. 201 with the record, 409 for an overlapping booking. |
| GET | `/api/v1/demos/{id}` | One demo with its journal. 404 when unknown. |
| PATCH | `/api/v1/demos/{id}` | Edit; the slot, seed set and brand only while scheduled (409 otherwise). |
| POST | `/api/v1/demos/{id}/cancel` | Cancel. The record stays. |
| POST | `/api/v1/demos/{id}/prepare` | Prepare a scheduled demo now. 202 with `{"runId":...,"demo":...}`. |
| POST | `/api/v1/demos/{id}/journal` | Body `{"kind":"note","text":"..."}`. 201 with the record. |
| DELETE | `/api/v1/demos/{id}/journal/{entryId}` | Remove an entry; 409 for a system entry. |
| GET | `/api/v1/demos.ics?env=` | RFC 5545 calendar of booked demos that are not cancelled. |
| GET | `/api/v1/debriefs?since=90d` | Demos waiting for a debrief, submitted debriefs and insights for the window. `since` takes `30d`, `720h`, `all`, a date or RFC 3339. |
| GET | `/api/v1/demos/{id}/debrief` | The demo's debrief (or `null`) with the gaps it references. |
| PUT | `/api/v1/demos/{id}/debrief` | Write the debrief. Each gap is `{"gapId":...}` or `{"new":{"title","kind","env","module"}}` with `severity`, `detail`, `quote`. 400 for bad input or an unknown gap, 404 for an unknown demo, 409 when the demo is not done. |
| GET | `/api/v1/gaps?status=&kind=&env=&includeMerged=` | The gaps board with derived scores, mentions, prospects and last mention. |
| POST | `/api/v1/gaps` | Create a gap. 201. |
| GET | `/api/v1/gaps/suggest?q=` | Similar active gaps, to link instead of duplicating. |
| GET | `/api/v1/gaps/export?format=md\|csv&status=` | Markdown spec or CSV, as a dated attachment. |
| GET | `/api/v1/gaps/{id}` | One gap with its stats, mentions and the gaps merged into it. |
| PATCH | `/api/v1/gaps/{id}` | Edit title, kind, status, owner, notes, env, module. 409 for a merged gap. |
| POST | `/api/v1/gaps/{id}/merge` | Body `{"into":"gap-..."}`. Rewrites every debrief reference. 409 when either side is merged. |

Mutating requests made with the session cookie must also send
`X-Requested-With: greenroom`. Bearer requests do not.

An optional `X-Actor` header (under 64 characters) is appended to the actor
recorded on runs, so automation can say who asked.

## Examples

Pre-call gate in a calendar hook or CI:

```bash
curl -fsS -H "Authorization: Bearer $TOKEN" -X POST \
  "https://greenroom.example.com/api/v1/environments/acme-demo/check" \
  | jq -e '.readiness == "green"'
```

Reset and follow the log:

```bash
id=$(curl -fsS -H "Authorization: Bearer $TOKEN" -X POST \
  -d '{"persona":"cfo","version":2}' \
  https://greenroom.example.com/api/v1/environments/acme-demo/reset | jq -r .runId)
curl -N -H "Authorization: Bearer $TOKEN" \
  https://greenroom.example.com/api/v1/environments/acme-demo/runs/$id/events
```

Brand kit links go through the guarded link fetcher; see
[PERSONALIZE.md](PERSONALIZE.md). Demo fields, statuses and the scheduler
are in [DEMOS.md](DEMOS.md); debriefs, gaps, scores and the export formats
in [DEBRIEFS.md](DEBRIEFS.md). The overview carries `debriefsPending`, the
demos of the last 90 days waiting for a debrief.

Book a demo and pull the calendar:

```bash
curl -fsS -H "Authorization: Bearer $TOKEN" -X POST \
  -d '{"env":"acme-demo","title":"Tidewell discovery","start":"2026-11-20T15:00:00Z","duration":"45m","brand":"tidewell"}' \
  https://greenroom.example.com/api/v1/demos | jq -r .id
curl -fsS -H "Authorization: Bearer $TOKEN" -o demos.ics https://greenroom.example.com/api/v1/demos.ics
```

Personalize a reset for a prospect:

```bash
curl -fsS -H "Authorization: Bearer $TOKEN" -X POST   -d '{"url":"https://larkspur.example"}' https://greenroom.example.com/api/v1/brands | jq .slug
curl -fsS -H "Authorization: Bearer $TOKEN" -X POST   -d '{"brand":"larkspur"}' https://greenroom.example.com/api/v1/environments/acme-demo/reset
```

## Readiness

| Value | Meaning |
| --- | --- |
| `green` | Every dependency up, nothing stale. |
| `amber` | A non-critical dependency down, a warning, or stale items. |
| `red` | A critical dependency down. |

The CLI maps these to exit codes 0, 1 and 2.
