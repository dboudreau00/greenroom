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
| GET | `/api/v1/environments/{env}?drift=true` | Full detail: dependencies with uptime, seed sets, runs, snapshots, heals, stale report, targets. |
| POST | `/api/v1/environments/{env}/check?record=true&drift=true` | Run the pre-call check now; returns the report. |
| GET | `/api/v1/environments/{env}/stale?drift=true` | Stale report only. |
| POST | `/api/v1/environments/{env}/reset` | Body `{"persona":"cfo","version":2,"force":false,"skipReset":false}`. Returns `{"runId":...}` with 202. 409 when the environment is busy. |
| POST | `/api/v1/environments/{env}/snapshot` | Body `{"dependency":"postgres"}`. Returns `{"runId":...}`. |
| POST | `/api/v1/environments/{env}/dependencies/{dep}/heal` | Returns `{"runId":...}`. |
| GET | `/api/v1/environments/{env}/targets` | Pings every target. |
| GET | `/api/v1/environments/{env}/runs?limit=50` | Run summaries, running ones first. |
| GET | `/api/v1/environments/{env}/runs/{id}` | Full run with steps and log. |
| GET | `/api/v1/environments/{env}/runs/{id}/events` | Server-sent events: `snapshot` (run so far), then `log`, `step`, `done`. |

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

## Readiness

| Value | Meaning |
| --- | --- |
| `green` | Every dependency up, nothing stale. |
| `amber` | A non-critical dependency down, a warning, or stale items. |
| `red` | A critical dependency down. |

The CLI maps these to exit codes 0, 1 and 2.
