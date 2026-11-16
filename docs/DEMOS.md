# Demos

A demo record ties a prospect, an environment, a seed set, an optional
brand kit and a time slot together. A booked demo is prepared before the
call: the portal resets the environment to the seed set for the prospect,
runs the pre-call check, and refuses other resets while the demo is live.
Each demo keeps a journal of what happened.

```bash
greenroom book acme-crm --at "2026-11-20 15:00" --for 45m --title "Larkspur discovery" \
  --prospect Larkspur --persona cfo@v2 --brand larkspur --reset-after
greenroom demos                                   # upcoming, soonest first
greenroom demo note 20261120-1500-3f9a --kind issue "Export took 40s on the orders page"
greenroom demo set 20261120-1500-3f9a --outcome positive --next-step "Pricing call with finance"
```

## Records

One file per demo, written atomically:

```
state/_demos/<id>.json
```

The id is the start time in UTC plus four hex characters,
`20261120-1500-3f9a`, so ids sort by time. Ids are checked against that
pattern before they touch a path. `_demos` can not collide with an
environment, whose names start with a letter or digit.

| Field | Meaning |
| --- | --- |
| `title`, `prospect` | Free text, 120 characters each. |
| `env` | The environment. Must exist when the demo is booked. |
| `persona`, `version` | The seed set, resolved when booked (default: the pin) and stored as a concrete version, so a later pin change does not move the demo. A set with problems is refused. |
| `brand` | Brand kit slug, optional. Must exist when set. |
| `owner` | Free text; defaults to whoever booked it (`api`, `portal`, or the local user for the CLI). |
| `attendees` | Up to 20 names, 80 characters each. |
| `booked` | `false` for a demo logged after the fact. |
| `start`, `end` | UTC. At most 12 hours apart. |
| `prepLead` | How long before the start the preparation runs. Default `30m`, from `5m` to `12h`. |
| `resetAfter` | Reset to the pin with the house brand when the demo ends. |
| `status` | See below. |
| `stage` | `intro`, `discovery` (default), `deep-dive`, `poc`, `executive`, `other`. |
| `outcome` | Empty, `positive`, `neutral`, `negative`, `no-show`, `rescheduled`. |
| `nextStep` | Free text, 500 characters. |
| `prepRunId`, `checkRunId`, `cleanupRunId` | The runs the scheduler started for the demo. |
| `cleanup` | With `resetAfter`: `pending`, `started`, `skipped` or `failed`. |
| `readiness` | `green`, `amber` or `red`, from the pre-call check after the prep. |
| `error` | Why the preparation failed. |
| `journal` | Entries, oldest first. |
| `createdAt`, `updatedAt`, `createdBy` | Bookkeeping. |

Text is cleaned before it is stored: control characters, bidirectional
overrides and invisible separators are dropped, whitespace is collapsed and
the length is capped. Journal text keeps its line breaks.

## Booking rules

- The start is at least a minute in the future, the end after the start,
  the slot at most 12 hours.
- Two booked demos on the same environment can not overlap unless one of
  them is cancelled or over. A clash is refused with the other demo's id,
  title and slot (`409` from the API). Back to back is fine.
- The environment, the brand kit and the seed set must exist. Unknown
  values are refused, never defaulted.

A demo logged after the fact (`greenroom demo log`, or `"booked": false`)
skips all of that: it starts out `done`, its start defaults to now and may
not be in the future, and it never blocks a reset.

Title, prospect, owner, attendees, stage, outcome and next step change at
any time. Start, end, seed set, brand kit, prep lead and reset after change
only while the demo is `scheduled`; a new slot is checked for overlaps
again. Every change adds a system entry naming the fields.

Cancelling sets `cancelled` and adds a system entry with who cancelled.
Records are never deleted through the CLI or the API.

## Statuses

```
scheduled -> preparing -> ready -------> live -> done
                      \-> prep-failed -/
scheduled -> missed                (the slot ended before anyone prepared it)
any status that is not final -> cancelled
```

`done`, `missed` and `cancelled` are final.

## The scheduler

`greenroom serve` advances booked demos on its own ticker, every
`probeInterval` or 30 seconds, whichever is shorter. Each pass reads the
demo files fresh, so a booking written by the CLI a moment ago is picked
up, and every transition is written back before the next one, so a restart
continues where it stopped. The CLI writes bookings but never runs the
scheduler; preparation only happens while the portal runs.

For a demo booked at 15:00 for 45 minutes with a 30 minute prep lead:

| When | What happens |
| --- | --- |
| 14:30 | `scheduled` and inside the prep lead: a reset to the demo's seed set and brand kit starts, actor `demo:<id>`. Status `preparing`, `prepRunId` stored. A busy environment is tried again on the next pass. |
| when the reset ends | Success: a recorded pre-call check runs (actor `demo:<id>`); `checkRunId` and `readiness` are stored and the status is `ready`. Failure: `prep-failed` with the run's error. A prep run that was cut off by a portal restart counts as failed. |
| 15:00 | `ready` or `prep-failed` becomes `live`. |
| 15:45 | `live` becomes `done`. With `resetAfter`, a reset to the pin with the house brand starts (`cleanupRunId`); a busy environment is tried again on the next pass. |
| after 15:45 | A demo still `scheduled` (the portal was down through the whole slot) becomes `missed`. |

A portal that comes back late catches up in one pass: a demo whose start
has passed but whose slot has not ended is prepared at once; one whose slot
is over is marked `missed`.

**Back to back demos.** When the next booking on the same environment is
already inside its prep lead while the earlier demo is live, its
preparation waits until the earlier one ends (one system entry says so).
The earlier demo's cleanup is then skipped, because the next preparation
is about to reset the environment anyway.

**Prepare now** (`POST /api/v1/demos/{id}/prepare`, or the button on the
demo page) starts the preparation of a `scheduled` demo at once, ignoring
the prep lead.

Every step adds a system entry to the journal: booked, preparing, ready
with the check summary, live, done, cleanup, missed, cancelled.

## Live protection

From the start until the end of a booked demo that is `live`, `ready` or
`prep-failed`, a reset or a snapshot of its environment is refused:

```
environment acme-crm is booked for demo 20261120-1500-3f9a (Larkspur discovery) until 2026-11-20 15:45 UTC; use override to go ahead anyway
```

The API answers `409`. `override` in the reset or snapshot body, or
`--override` on `greenroom reset` and `greenroom snapshot`, goes ahead and
adds a system entry with who did it and the run id to the live demo's
journal. Heals are never blocked: they keep the demo up.

The window is the slot itself, not the status alone, so a demo left `live`
by a portal that stopped never blocks resets after its end time. The demo's
own preparation and cleanup run outside its window.

## Journal

Entries have a kind: `note`, `issue`, `question`, `follow-up`, `feedback`,
or `system` for the scheduler's own lines. Text is up to 4000 characters
and keeps line breaks; a demo holds up to 500 entries. People can delete
their entries; system entries stay.

```bash
greenroom demo note <id> "Asked whether exports can be scheduled"
greenroom demo note <id> --kind follow-up "Send the SSO setup guide"
greenroom demo show <id>                          # details and the journal, newest first
```

## Tracker

**Demos** in the portal's top navigation lists upcoming, past or all demos,
filtered by environment: when, title, prospect with the brand kit's logo,
environment, seed set, status, readiness, outcome and journal entries.
**Book a demo** and **Log a past demo** open the dialogs; **.ics export**
downloads the calendar.

A demo's page shows the status, the slot with a countdown, the environment,
seed set and brand kit, links to the prep, check and cleanup runs, **Prepare
now** and **Cancel demo**, the editable stage, outcome, next step, prospect
and attendees, and the journal with a form to add entries.

The overview shows the next five demos above the environment grid, and each
environment card shows its next demo. An environment's page shows a notice
while a demo is live there, a **Book a demo** button, and a **Demos** tab.
The reset and snapshot dialogs warn about a live demo and offer the
override.

## Calendar

`GET /api/v1/demos.ics` (optionally `?env=`) is an RFC 5545 calendar of
the booked demos that are not cancelled: CRLF line endings, lines folded at
75 octets, TEXT values escaped (backslash, semicolon, comma, newline), times
in UTC.

| Property | Value |
| --- | --- |
| `UID` | `<id>@greenroom`, stable across updates |
| `DTSTAMP` | the time of the export |
| `DTSTART`, `DTEND` | the slot, UTC |
| `SUMMARY` | the title |
| `DESCRIPTION` | environment, seed set, prospect, brand kit, stage, status, demo id |

The endpoint needs the same token as the rest of the API:

```bash
curl -fsS -H "Authorization: Bearer $TOKEN" -o demos.ics https://greenroom.example.com/api/v1/demos.ics
```

## CLI

| Command | What it does |
| --- | --- |
| `greenroom book <env> --at "2026-11-20 15:00" --for 45m --title t [--prospect p] [--persona cfo@v2] [--brand slug] [--prep 30m] [--reset-after] [--attendee name ...] [--owner o] [--stage s] [--note text]` | Book a demo. `--at` takes RFC 3339 or local `YYYY-MM-DD HH:MM`; `--for` defaults to `1h`. |
| `greenroom demos [--env e] [--past \| --all] [--json]` | Upcoming demos, or past ones, or all. |
| `greenroom demo show <id> [--json]` | Details and the journal. |
| `greenroom demo note <id> [--kind issue] <text>` | Add a journal entry. |
| `greenroom demo set <id> [--outcome o] [--stage s] [--next-step t] [--prospect p] [--title t] [--owner o]` | Edit. |
| `greenroom demo cancel <id>` | Cancel. |
| `greenroom demo log <env> --title t [--at time] [--for 45m] [--persona p] [--brand slug] [--outcome o] [--prospect p] [--stage s] [--note text]` | Record a demo that already happened. |
| `greenroom reset <env> --override`, `greenroom snapshot <env> <dep> --override` | Go ahead during a live demo. |

The CLI owner defaults to `GREENROOM_USER`, then `USER` or `USERNAME`.

## API

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/demos?env=&scope=upcoming\|past\|all&limit=100` | Short records. `upcoming` (default) is not final and not over, soonest first; `past` is final or over, latest first; `all` is latest first. |
| POST | `/api/v1/demos` | Book. Body `{"env":"acme-crm","title":"Larkspur discovery","prospect":"Larkspur","start":"2026-11-20T15:00:00+01:00","duration":"45m","persona":"cfo","version":2,"brand":"larkspur","prepLead":"30m","resetAfter":true,"attendees":["Ana Ruiz"],"stage":"discovery"}`. `end` may replace `duration`. `"booked": false` logs a past demo instead. 201 with the record, 409 for an overlap, 400 for bad input. |
| GET | `/api/v1/demos/{id}` | The full record with its journal. |
| PATCH | `/api/v1/demos/{id}` | Any of `title`, `prospect`, `owner`, `attendees`, `stage`, `outcome`, `nextStep`; while scheduled also `start`, `end`, `duration`, `persona`, `version`, `brand`, `prepLead`, `resetAfter`. 409 when the status does not allow the change or the new slot overlaps. |
| POST | `/api/v1/demos/{id}/cancel` | Cancel. 409 when already final. |
| POST | `/api/v1/demos/{id}/prepare` | Prepare a scheduled demo now. 202 with `{"runId":...,"demo":...}`; 409 when busy, booked by another live demo, or not scheduled. |
| POST | `/api/v1/demos/{id}/journal` | Body `{"kind":"issue","text":"..."}`. 201 with the record. |
| DELETE | `/api/v1/demos/{id}/journal/{entryId}` | Remove an entry; 409 for a system entry. |
| GET | `/api/v1/demos.ics?env=` | The calendar. |

Environment summaries and details carry `nextDemo` and `liveDemo` in the
short form; the detail also has `demos`, the environment's latest 50.
