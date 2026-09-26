# Calnode — agent notes

Go backend (SQLite, `go:embed`s the built SvelteKit SPA) + SvelteKit 5 admin UI
served under `/admin/`. Public booking pages are server-rendered Go templates
(`internal/handler/templates/*.html`), distinct from the Svelte admin app.

## Frontend toolchain

- Vite 8 (Rolldown) · SvelteKit 2 · Svelte 5 · Tailwind v4 (`@tailwindcss/vite`)
  · bits-ui / shadcn-svelte · Vitest 4 browser mode.
- The frontend is embedded at Go compile time (`frontend/embed.go` →
  `//go:embed all:build`). To see frontend changes in the running app: rebuild
  the frontend (`pnpm build` in `frontend/`) **and** rebuild/restart the Go
  binary — restarting Go alone won't pick up new assets.

## UI styling — required check

**Default to shadcn-svelte in the admin UI.** In the SvelteKit admin app (`frontend/`),
build from the existing shadcn-svelte components — `Button`/`buttonVariants`,
`ConfirmDialog` (**never** `window.confirm`/`alert`), `Dialog`, `Input`, `Switch`,
`Tooltip`, etc. Don't hand-roll buttons, modals, or browser-native dialogs. Destructive
actions use `ConfirmDialog` with `destructive`; row actions use a ghost icon button +
`Tooltip` (see `event-types`, `members`, `recordings`). **If shadcn genuinely doesn't fit,
flag it (and the reason) before deviating — don't silently hand-roll.** This does NOT apply
to the public booking templates (`internal/handler/templates/*.html`), `embed.js`, or the
LiveKit room — those are intentionally framework-free (Go templates / vanilla JS, own CSS).

shadcn-svelte components style state via Tailwind `data-*` variants. Bits-ui
states exposed as `data-state="…"` (checked/unchecked/open/closed) need an
`@custom-variant` remap in `frontend/src/app.css` or they render **silently
unstyled** (logic works, visuals don't). See `frontend/TESTING.md`.

**After changing `frontend/src/lib/components/ui/**`, `frontend/src/app.css`, or
the theme — run `pnpm test:visual`** (Vitest browser smoke). Unit tests do NOT
catch this class of bug; only the real-browser computed-style assertions do.

## Booking calendar — THREE surfaces, keep them aligned

The date/time-slot booking calendar exists in **three** places. A change to its
behaviour or markup must usually be made in all three, or they drift:

1. **Booking page** — `internal/handler/templates/book.html` (server-rendered Go template + vanilla JS)
2. **Manage page** — `internal/handler/templates/manage.html` (reschedule flow; same calendar/slots)
3. **Embed widget** — `internal/handler/embed.js` (Shadow-DOM Web Component on customer sites)

- **Styling is shared:** all three load `internal/handler/templates/booking.css`
  (served at `GET /booking.css`; the widget injects it into its shadow root). Change
  visuals **there**, once — don't re-style per surface.
- **Markup + JS are NOT shared** (Go template vs web component): the calendar render,
  slot picking, and the **mobile step-flow** (calendar → slots → form, with Back) are
  implemented separately in each. If you change calendar *behaviour*, update all three.
- Verify on **desktop and mobile** for each surface after touching the calendar.
- **Shared slot logic lives in `internal/handler/assets/booking-logic.js`** (tested with
  `node --test`): day grouping, time formatting, and the taken-slot merge. It is inlined
  into **book.html and manage.html only**, which are the two surfaces that load it.
- ⛔ **The embed widget does NOT load `booking-logic.js`, so `BookingLogic` is undefined
  inside it.** `EmbedJS` serves `embed.js` as its own standalone file, unmodified — it is a
  Shadow-DOM web component on a third-party page, with no build step and nothing to
  prepend the module for it. It therefore carries its own copies of the few helpers it
  needs (`dowLabels`, `dayKey`, `timeLabel`, `shortDay`, `ymd`), each commented as
  mirroring the shared one. Calling `BookingLogic.anything` from `embed.js` throws a
  `ReferenceError` on the customer's site, where no test here would see it. So: put logic
  the pages share in `booking-logic.js`, and when the widget needs it too, mirror it there
  deliberately and keep the two in step.
- **Taken/booked slots** (`show_taken_slots`, off by default) are computed by
  `slots.GenerateWithTaken` as the *difference* between a normal pass and one ignoring
  busy, which is what keeps out-of-hours and min-notice starts from being mislabelled as
  booked. Never feed them to MCP or the assistant - `computeSlots(..., includeTaken)`
  makes each caller say. See ARCHITECTURE §8.

## Conversational booking assistant (optional LLM layer)

The "Book by chat" assistant lives on **two** of those surfaces — `book.html` (floating
drawer + inline link) and `embed.js` (inline link only; no floating button, to avoid
colliding with host-site widgets). **Not** on the manage page (reschedule context — a
reschedule chat is deliberately deferred). Server side is one endpoint,
`POST /v1/event-types/{slug}/assistant` (`booking_assistant.go`): an LLM tool-loop that
drives `find_available_slots`/`book` over the **shared deterministic cores** (`computeSlots`,
`createBookingForSlug`) — never re-implement booking logic in the assistant. Invariants:
the LLM does NL→constraints only (never time math), sees only computed availability (never
raw calendar data), and `<think>` reasoning is stripped. Shared `.asst-*` styles are in
`booking.css`; the base prompt (`assistantBaseRules`) is code-owned, admins only append
"Additional instructions". Off by default — `getLLM()` nil → the picker is the fallback.

## Built-in video meetings (LiveKit)

Self-hostable video as a booking location type (`location_type = "livekit"`). **No LiveKit SDK
server-side** — all tokens are hand-signed. The browser room app is **vanilla JS + a vendored
client SDK**, not Svelte.

- **Where it lives:** room UI = `internal/handler/templates/livekit-room.html` +
  `assets/livekit-room.js` (+ vendored `assets/livekit-client.umd.min.js`). Server =
  `internal/livekit/` (`livekit.go` token signing, `admin.go` Twirp/egress) and
  `internal/handler/livekit_room.go` + `livekit_recording.go`. Settings UI =
  `frontend/src/routes/settings/video/`.
- **Three token kinds (don't conflate):** (1) **room token** — opaque HMAC blob in the join URL
  (`{r,e,role}`), carries no LiveKit grant; (2) **access token** — the real LiveKit HS256 JWT the
  SDK joins with (`AccessToken`/`VerifyAccessToken`); (3) **admin token** — short-lived JWT for
  Twirp server APIs.
- **Host authority — `authorizeHost`, NOT just the room token.** A host action is allowed if the
  caller is the **durable host** (`hostRoomOrOwner`: holds a host room token OR is the signed-in
  booking owner) **OR** the **current reassigned host** — proven by verifying their *access token*
  and confirming that identity has `metadata="host"` right now (`ListParticipants`). Clients send
  **both** `t` (room token) and `at` (access token) on every host call. **Reclaim host is
  durable-host-only.** Reassigning only flips metadata, so without the access-token path a temp
  host has the badge but no real power — that gap is exactly what `authorizeHost` closes.
- **Single host:** any host join demotes prior hosts (`demoteOtherHosts` → metadata `"attendee"`);
  the client downgrades only on explicit `"attendee"`, never on a transient/empty metadata event.
- **Recording (Egress):** room-composite → the **Litestream backups bucket** (`LITESTREAM_*` env),
  `recordings/` prefix. **Finalize on stop/end (`finalizeActiveRecording`), do NOT depend on the
  webhook** — `object_key` is set at start so downloads work without it; a startup sweep closes
  orphaned `active` rows. Idempotent guard keys on an `active` row per room.
- **Webhook = single sink** `POST /v1/livekit/webhook` (legacy alias `/v1/livekit/egress-webhook`,
  keep it). LiveKit allows one URL per project, so it receives **all** events; we verify the
  signature and act only on `egress_started/ended/failed` + `room_finished` (everything else is
  200-ACKed and dropped). The **egress lifecycle is the source of truth** for the recording flag.
- **Recording banner** is driven by room **metadata** (`{recording, allowShare}`) + the
  `RoomMetadataChanged` realtime event — it's an in-room overlay, so `showOnly` hides it off the
  room view. **Always `mergeRoomMeta` (read-merge-write), never overwrite** — recording and
  screen-share flags would clobber each other.
- **Attendee screen-share defaults OFF**; host opts in (gear menu). Enforced server-side via
  `canPublishSources` at token mint + live `UpdateParticipant`, not just hidden in the UI.
- Room HTML is served `no-store` and injects `?v=<content-hash>` onto the room JS/SDK assets —
  bump-free cache-busting. After changing the room JS/HTML you still need a frontend-independent
  Go rebuild (these assets are `go:embed`-ed in the handler package, not the SPA).
- **Watch the room JS complexity.** `livekit-room.js` has grown large + stateful (host model,
  single-host, consent, chat, layout, recording) with state in scattered module flags + manual
  DOM updates — several bugs traced to that (stale state, the metadata up/down-grade logic). It's
  fine now, but if it keeps growing the move is NOT "shadcn-ify it" (it's deliberately
  framework-free) — it's tidy-in-place: one state object + a single derive/render, extract the
  pure logic (host/consent state machines) into testable functions. A dedicated tiny Svelte build
  is the last resort, not the first.

## Languages (i18n) - data-driven, adding one touches no code

Public surfaces (book.html, manage.html, embed.js, the four emails, calendar invite
title/description) and the LiveKit room are translated - the room gets its `room_*` keys
through the same resolver and a per-locale slice of the table (`livekit_room.go`), with
`RoomLogic.translate` falling back to English. **NOT translated:** admin-authored content
(event names, descriptions, questions, custom email copy). **This fork's admin SPA is
hardcoded Spanish** (Agenda Maestros 4x4), not run through i18n. Locale is resolved per request from
`Accept-Language` + a `?lang=` override + the operator's fallback setting
(`internal/handler/i18n.go`), and the booker's locale is stored on the booking so later
reminders match. **`FORCE_LOCALE`** (this fork sets `es`) short-circuits that resolver for
every public surface and hides the footer switcher - the audience is Spanish-speaking but
abroad, often on borrowed computers set to other languages. Ships `en es fr fr-CA de it pt nl sv`. Full detail: ARCHITECTURE §23.

**Adding a locale = adding `internal/i18n/locales/<code>.json`.** Nothing else. `init()`
globs the directory; the switcher, the fallback dropdown and the public API payload all read
`SupportedLocales()`. Name the file **BCP-47 canonical** (`pt-BR.json`, never `pt-br.json`).

- Go has no locale date data, so `date_format`, `clock_format`, `dow_short_*` and
  `month_short_*` are **keys in the JSON** (`internal/i18n/datetime.go`), not stdlib calls.
- Three guards police a new file: same-keys, printf-verb parity (uses `fmt` as the oracle),
  and `TestDateTablesMatchCLDR`, which cross-checks the date tables against `Intl` via node.
  Run `go test ./internal/i18n/`.
- Tests use `ja`/`ko` to mean "a language we don't ship". If you add either,
  `assertUnsupported`/`requireUnsupported` fail loudly and tell you what to change.
- **Every non-English locale is an LLM draft with no native review.** Structure is verified;
  wording is not. Say so before anyone markets a language.

### Where a translated sentence gets assembled

**Default: server-side.** Go composes the finished sentence and the page renders it as
given. `durationLabel`, `hostsLabel`, `locationLabel`, `assistantGreeting` and
`noticeLabel` (`book.go`) are the pattern - they return text, not parts. Keeping it there
is what stops plural rules, duration wording and date formats from being reinvented in
three separate front ends, only one of which has tests.

**One exception, and it is the only one:** a booking-surface string whose argument is
chosen by the visitor *after the page loads* may be substituted client-side, via
`BookingLogic.fmt` (book/manage) or its deliberate mirror in `embed.js`. Today that is
exactly the selected date, in `no_available_times`, `no_available_times_host` and
`min_notice_hint`. The alternative is a server round-trip on every calendar click, or
shipping a month of pre-rendered sentences to render one.

The line that still holds inside the exception: **anything locale-dependent is computed
in Go and passed in as a finished fragment.** `MinNoticeLabel` is the model - the server
sends "4 hours" already pluralised and translated, and the page only drops it into a
slot. `fmt` handles `%s` and the indexed `%[n]s` (so a translation can reorder its
arguments) and nothing else; it is not a printf and must not become one.

**If you are building a plural form, a duration or a date format in JavaScript, you have
crossed the line** - move it into `book.go` and send the result.

## Webhooks - WhatsApp confirmations and reminders (fork)

The owner sends WhatsApp through **FunnelChat**: each webhook points at its own FunnelChat
flow, which maps JSON keys (`data.attendee_phone`, ...). FunnelChat **cannot branch on
`event`**, so every moment is a separate event and the operator creates one webhook per
message. No goose migration was added for any of this - keep it that way (this fork's goose
numbers 00066/00067 already collide with upstream's); the fork's tables are made in code by
`webhook.EnsureForkSchema` (below): `webhook_event_type_filters` (+ its trigger),
`event_type_whatsapp_messages`, `fork_settings`, `short_links`, and the index `idx_fork_webhook_deliveries_booking`
on the upstream `webhook_deliveries`. **No new trigger may name another table** (an upstream
`CREATE x_new / DROP x / RENAME` rebuild would fail on it); plain tables and indexes are safe.

- **Events.** `booking.created` is the confirmation. Fork adds `booking.reminder_morning`
  (the meeting's day at the morning hour - in the **attendee's** zone, or in the owner's
  fixed zone, see below), `booking.reminder_1h` and `booking.reminder_5m` (constants in
  `internal/webhook/fork_fields.go`, accepted by `validWebhookEvents`). Payload = the booking.created shape.
- **Job `webhook.reminder`** (`internal/handler/webhook_reminders.go`, registered in
  `server.go`): one `jobs` row per moment, payload `{"booking_id","kind","start_at"}`
  (kind `morning|1h|5m`, start_at = the start it was planned for, RFC3339 UTC), `INSERT OR
  IGNORE` on the live `(type, payload)` index plus a `NOT EXISTS` so a reminder that already
  ran for that start is never planned twice. Written in `dispatchBookingConfirmation`
  (every creation path: page, API, embed, MCP/assistant, Group, paid-after-Stripe), replaced
  in `rescheduleSideEffects` (panel, `/manage`, MCP) and in `ReassignBooking` (same start,
  but the zone can change - see below), deleted in `cancelSideEffects`. The job re-checks at
  run time and does nothing if the booking is gone, not `confirmed`, its start_at no longer
  matches, or it is late enough to be false (`reminderSuperseded`: morning dropped once the
  1 h moment has come, 1 h once the 5 min one has, 5 min once the meeting started - a worker
  back from downtime must not send a burst) - a stale row is harmless.
- **Order is always morning → 1 h → 5 min.** Morning is planned only when it falls strictly
  before the 1 h reminder (`morningReminderAt`; with 08:00, a 08:30 or 09:00 meeting gets
  none, 09:05 does) and after now; 1h/5m only if still future. `planWebhookReminders` is
  pure and carries the tests (zones, DST, same-day bookings, the ordering property).
  `BackfillWebhookReminders` runs at every boot (idempotent) so bookings made before an
  upgrade get their jobs too, and `syncMorningReminder` re-applies the current
  `REMINDER_MORNING_HOUR` and zone to morning jobs still pending (moves or drops them;
  `run_at` is deliberately NOT in the payload, or each change would add a duplicate). The
  worker is in-process: a host that sleeps when idle sends reminders late or drops them -
  keep the instance always on.
- **Morning hour and zone** (`handler/fork_settings.go`). Env defaults: `REMINDER_MORNING_HOUR`
  (`HH:MM`, default `08:00`) and `REMINDER_MORNING_TIMEZONE` (IANA, default `""` = each attendee's
  zone); `config.Validate` refuses a bad value of either at boot. The owner can override both from
  the Webhooks page: saved in `fork_settings` (`reminder_morning_hour`, `reminder_morning_timezone`),
  which wins over the env; a saved value that stopped being valid is ignored (`loadMorningSetting`,
  read from the DB on every planning pass - never cached, so a `calnode mcp` process agrees). With a
  fixed zone the reminder is that hour on the meeting's day **as seen in that zone** (07:00
  America/Lima = 14:00 in Madrid in summer): `morningReminderZone` just swaps the zone fed to the
  unchanged pure planner, so the ordering rules still decide (a Madrid 10:00 meeting gets no morning
  reminder at 07:00 Lima). `GET /v1/webhooks/settings` = `{reminder_morning_hour,
  reminder_morning_timezone, team_scope, can_edit}`; **`PUT` is owner-only** (403 otherwise),
  validates `HH:MM` and IANA (`Local` refused), saves, then runs `BackfillWebhookReminders` right
  away (moves/drops/adds pending morning jobs, never re-plans one that ran) and answers
  `resynced_bookings` / `resync_ok`. The default reminder texts avoid "hoy"/"buenos días" for this
  reason (a fixed-zone reminder can reach someone in the afternoon or the day before).
- **`whatsapp_message`** (`internal/webhook/fork_whatsapp.go`): the finished WhatsApp text, composed
  by the agenda because FunnelChat cannot compute times, branch or drop lines. One text per event
  type and moment (`created`, `reminder_morning`, `reminder_1h`, `reminder_5m`, `cancelled`,
  `rescheduled`; `WhatsAppMomentForEvent` maps the six events, others carry none) in
  `event_type_whatsapp_messages` (CASCADE on the event type; moment validated in Go, no CHECK); no
  row = the Go default (`defaultWhatsAppMessages`). Markers `{nombre} {mentor} {tipo} {tema} {fecha}
  {dia} {hora} {enlace} {cancelar} {motivo}` (case-insensitive, `{día}` too); `{tema}` = the answer
  to the event type's **first** `text` question (not the first answered one); `{fecha}` =
  start_local_long, `{dia}`/`{hora}` in the client's zone; `{motivo}` only in `cancelled`. **A line
  with any known marker that resolves empty is dropped whole**; unknown markers stay as written;
  values are squeezed to one line and never re-scanned (a name typed as `{cancelar}` stays text).
  Rendered only when a receiving webhook explicitly selected the field (it is appended to
  `AllFields`, never in `defaultFields`), in `Enqueue` right after `enrich`. API (owner of the event
  type, `eventTypeIDForOwner` - same rule as editing it): `GET/PUT /v1/event-types/{slug}/whatsapp-messages`
  (six strings, `""` = default; PUT: omitted = unchanged; plus `defaults` in the answer) and
  `POST .../preview` (renders the given texts with sample data **in Go** - `RenderWhatsApp` is the
  only renderer; the panel never fills markers). Panel: event type → tab **WhatsApp**
  (`WhatsAppMessagesPanel.svelte`, saved on its own; kept mounted so unsaved texts survive a tab
  switch). The Webhooks page offers "Añadir mensaje de WhatsApp" on webhooks created before the field.
- **Owner scope.** `webhook.Service.Enqueue` sends to the host's webhooks **and** those of
  the workspace owner (`is_owner = 1`, not archived) - one query with an OR, so no
  duplicates. The owner configures the team's notices once; mentors still get only their own.
  Upstream isolates strictly by host; this is a deliberate divergence. A mentor's panel says
  so, since a mentor webhook repeating the owner's message makes the client get it twice.
- **Fork payload fields** (appended to `AllFields`, never in `defaultFields`, omitted when
  empty): `attendee_phone` (E.164 from the first `phone` question, else a telephone
  booking's `tel:` location; a number with no `+`/`00` is **omitted**, never sent as local
  digits - FunnelChat/wa.me would read its first digits as a country code and message a
  stranger), `attendee_whatsapp` (digits only, wa.me), `start_local` / `start_local_date` /
  `start_local_time` (attendee zone - a stored `UTC` counts as unknown and falls back to the
  host's - in `FORCE_LOCALE`, else the booker's locale, else `es`), `start_local_long`
  ("martes 9 de marzo de 2027, 09:00": the short Spanish forms make "mar" both martes and
  marzo; long names are a Spanish table in `fork_fields.go`, other locales get start_local's
  text), `start_local_timezone` (the zone those texts are really in - `attendee_timezone`
  stays the raw stored value and can say `UTC`), `manage_url` (`/manage/{token}`; the
  caller fills `BookingPayload.ManageURL`, reusing the e-mail's token when there is one, else
  an **additive** `IssueManageToken` - never Rotate - and only when a receiving webhook
  selected the field, via `WantsField`, **or** selected `whatsapp_message` and that text uses
  `{cancelar}` (`WhatsAppNeedsManageURL`); `booking.cancelled` mints only for the latter
  (`whatsAppManageURL`), since its payload never carried manage_url upstream). `location_value`
  is unchanged: it is already the attendee's join link (a Meet/Teams link generated while booking
  is also copied onto `b` in `createHostEventsAndNotify`, as Zoom/LiveKit do, so booking.created and
  its `{enlace}` carry it).
- **`manage_url` is the only credential kept in clear in the database** (manage tokens are
  stored as hashes). It sits in `webhook_deliveries.payload` only while the delivery is in
  flight: the worker calls `webhook.Service.ScrubManageURL` when it succeeds or runs out of
  attempts, which also removes `whatsapp_message` **whole** (its short-link codes are credentials;
  removing beats redacting - no link pattern to keep in step - and nothing reads the text back; the
  stored payload is what gets signed and sent, so both stay until then). Keep it that way if you add a delivery path.
- **Short links in `whatsapp_message`** (`webhook/fork_short_links.go`, `handler/fork_short_links.go`):
  `{enlace}` → `{PUBLIC_BASE_URL}/e/{code}`, `{cancelar}` → `/c/{code}`, 8 chars of `23456789abcdefghjkmnpqrstuvwxyz`, a NEW code per rendered message, only for a web link the short one beats (a Meet link, `tel:` or an address stays); payload `location_value`/`manage_url` stay long. `short_links` (EnsureForkSchema, CASCADE) holds only an **HMAC-SHA256 under a key derived from the instance DEK** (a plain SHA-256 of ~40 bits reverses offline).
  `GET /e|c/{code}`: `RateLimitBy` 20/min per IPv4 address and per IPv6 **/64** (`shortLinkClientKey`: one host usually holds a whole /64); behind a proxy/CDN production needs `TRUSTED_PROXY_CIDRS` with the edge's ranges, or every visitor shares one bucket. Paths redacted in the log, `no-store` + `no-referrer`; malformed → plain 404, no DB; unknown/revoked/expired/wrong route → friendly 404 (manage.html's invalid view, `short_link_invalid_*`). `/e` → 302 to the attendee link built NOW (LiveKit: fresh room token to the CURRENT end + `liveKitJoinGrace`, friendly page after that; cancelled → friendly page); `/c` → additive `IssueManageTokenUntil` (expires with the code's window, not in 60 days) + 302 `/manage/{token}`.
  Validity follows the booking as it is now (room: end + 12 h, manage: start + 12 h). **A reschedule revokes the manage codes** (`DeleteShortLinks` in `rescheduleSideEffects`, before `booking.rescheduled` renders its new `/c`) - the same invariant as `RotateManageToken`; room codes follow the new time with no write. `expires_at` is a 60-day hard cap the worker purges. `ValidShortCode` accepts `MinShortCodeLen`..`ShortCodeLen`: to lengthen codes raise only `ShortCodeLen`. A failed insert falls back to the long link (logged, never the link), which is why the handler still mints the manage token for `{cancelar}`.
- **Cancelling from `{cancelar}`** (`manage.html`): the page requires a reason (≥ 3 letters,
  `textarea`), then shows "Tu sesión fue cancelada · ¿Deseas reprogramar?" with "Sí, elegir otra
  fecha" (`/book/{slug}`: a NEW booking of the same type) and "No, cerrar" (a friendly close); an
  already-cancelled booking offers the same link. Both only when the type is still active and public
  (`managePageData.Rebookable`: `BookPage` 404s otherwise), else the friendly close comes right away.
  Focus moves to the new view's heading (the pressed button is hidden). Its icons use
  `state-icon neutral`, never `info` (booking.css's `.info` is the side panel). `POST /manage/{token}/cancel` itself still
  accepts no reason (upstream's contract and test). The same-booking "Reprogramar" is untouched.
  New strings are keys in all nine locale files.
- **Bookings list** (`handler/booking_whatsapp_status.go`): `GET /v1/bookings` items gain
  `event_type_name`, `host_name` in every view, and `whatsapp` = the four notices (created,
  morning, 1h, 5m) as `sent | sending | pending (+ at = run_at) | failed | cancelled | missed |
  unknown | not_applicable`, from the latest delivery per webhook and the reminder jobs planned for the
  booking's **current** start (a pending job no active webhook would receive = not_applicable; a
  cancelled booking's leftover pending/running jobs are ignored - `cancelSideEffects` deletes them
  only after `CancelBooking` answers; `missed` = the job finished only once `reminderSuperseded`
  held, with no delivery: dropped as late, the sleeping-instance symptom; `unknown` = no record and
  old enough that the worker's 30-day purge of deliveries and jobs may have taken it -
  `noticeRecordRetention` mirrors that purge, change both together).
  Same page and visibility as the list; fixed queries per page (names, one UNION ALL over
  deliveries + jobs, the active webhooks); best effort - a failure drops `whatsapp`, never the
  list. The panel shows cards (stack at 375 px; the notices span the card and take 4 columns only
  from a 42rem-wide card, a `@container` query, since the viewport ignores the desktop sidebar) and
  "Cancelar reunión" (future confirmed only,
  `ConfirmDialog` with an optional reason sent as typed - it used to send the English "cancelled
  by admin"). `ConfirmDialog` takes an optional `children` snippet for that field.
- **Admin shell on phones** (`routes/+layout.svelte`): below `md` the sidebar is a menu opened from
  a top bar (shadcn `Dialog`, closed on navigation and when the screen reaches `md` - its overlay
  is not `md:hidden`); from `md` up it is the old fixed column. In the event-type editor, Ctrl/Cmd+S
  on the WhatsApp tab saves the texts (`saveFromShortcut`), not the event type. The Webhooks page
  shows "No se pudo cargar" + Reintentar when `GET /v1/webhooks/settings` fails, never the defaults.
- **Event-type filter** (`internal/webhook/fork_event_types.go`): `event_type_ids` on POST/PATCH/GET
  `/v1/webhooks` (PATCH: null/omitted = unchanged); **empty = every type**, else only bookings of those
  types, for every event (one `NOT EXISTS`/`EXISTS` in `matchingWebhooks`; no booking id = no match).
  Table `webhook_event_type_filters` is made by `webhook.EnsureForkSchema` (idempotent, NOT goose - see
  above); CASCADE on both FKs, and a trigger switches a webhook off (`is_active = 0`) when its last listed
  type is deleted, since "no rows" would widen it to every type. Boot wraps `db.Migrate` in
  `migrateWithForkSchema` (`cmd/calnode/fork_schema.go`): with migrations pending it drops the trigger first
  (it names `webhooks`, and SQLite's RENAME re-checks every trigger, so an upstream rebuild of `webhooks`
  would fail), then re-creates the schema or stops the boot; `webhook.New` retries it best effort, never failing.
  Only the owner may pick any type; everyone else, admins included (their webhooks get only the bookings
  they host), their own or hosted (`GET /v1/webhooks/event-types`).
- The panel pre-selects no event (one webhook per FunnelChat flow). There is no "send test"
  button; to map a flow in FunnelChat, make a real booking.

## Team áreas, predefined types and supervision (fork)

- **Tier vs área.** Tier = upstream `is_owner` / `is_admin` / member. Área (`mentoria` | `soporte` | none)
  = what the person attends, in `fork_member_areas`, set by `PUT /v1/users/{id}/team-role` (one call, the
  permission matrix is in `SetTeamRole`) or by the role an invite carries (`fork_invite_roles`, applied
  inside `ClaimInvite`'s tx). **The fork's old `is_support` "desk" tier is retired:** the column and the
  positional scans stay, `Role()` never returns `support`, it grants nothing (`support_tier_test.go`),
  `SetUserRole` refuses it with a Spanish 400, and `RetireSupportTier` (boot) turns any leftover flag into
  área soporte. The 00066 migration comment is history.
- **Two templates, one model** (`fork_settings`: `team_mentoria_template_id` = T "Mentoría privada",
  `team_soporte_shared_id` = S "Soporte 1 a 1" - the key keeps its old name, production has it set, and it
  now names the Soporte **template**; owner-only `PUT /v1/team/settings`, body `mentoria_template_id` /
  `soporte_template_id`, `soporte_shared_id` still read as an alias). **Nobody rotates any more.** Every
  active user of an área except that template's owner gets a **copy** of its template (`fork_event_type_links`
  kind `copy`, owned by the template's owner, hosted by that person alone - required, fixed routing - slug
  `{template slug}-{name}` stable forever): mentors get copies of T, support people copies of S. A copy stays
  active only while its template is the CURRENT setting of its person's área. T and S themselves are locked
  to [their owner, required], fixed; they ARE the owner's links (no copy for the owner, and `PUT team-role`
  refuses the owner the área of a template they own, Spanish 400). `ReconcileTeam` (`handler/fork_team.go`)
  is the single idempotent Go engine: boot from `server.New` only (never `BuildHandler`, which `calnode mcp`
  also runs), and after a 2xx of every trigger (route wrappers in `server.go`). Field sync is a row-value
  UPDATE over `PRAGMA table_info(event_types)` minus an exclusion list - **an upstream column addition fails
  `TestTeamSyncColumns_classified` until you classify it.** Questions sync in place through
  `fork_question_links`; a retired question with answers is parked on a hidden **holder** type. Copies are
  deactivated, never deleted (bookings are RESTRICT).
- **A copy's área** is its template's: the current setting, else `fork_template_areas` (EnsureTeamSchema;
  the reconcile records each template's área while it is a setting), else Mentoría (every copy made before S
  became a template). It decides `mentoria_copy` vs `soporte_copy`, the bookings' `area`, `?area=` and the
  reassign family, so an old template's bookings keep their área after the setting changes. Because it is
  one value per template, `PUT /v1/team/settings` refuses (Spanish 400) to make a type that already has
  copies the template of the OTHER área (swapping T and S included): that would relabel every existing
  session and offer it to the other área's people.
- **The retired rotation** (S in round robin among área-soporte staff with hours) is converted by the first
  reconcile after the change: S's hosts back to its owner, fixed, one copy per support person; the leftover
  `team_soporte_last_managed_id` key releases the type it names (if not a template) and is deleted.
  Sessions the rotation booked stay on S with their host; "Pasar a otra persona" moves one onto a copy.
- **`has_availability`** (`GET /v1/users`, `fork_team_hours.go`): whether the person's weekly rules can hold
  one slot of THEIR link (their copy, or the template for its owner) - a global rule or one for that copy,
  aligned like `hostsByStart`; overrides never count; ONE unparseable rule in that scope means false (POST
  accepts it and it makes GetSlots 500). The panel shows false as "Sin horario". POST/PATCH/DELETE
  `/v1/availability-rules` still reconcile the caller (`TeamReconcileAfterCaller`), a no-op today.
- **Guards** (`fork_team_guards.go`, Spanish 409s): copies of either template and holders are read-only
  (edit the template); T and S alike refuse transfer, hosts PUT, routing changes, leaving `livekit` and DELETE
  while they have copies; ownership transfer is refused while either setting is set. Webhook filters list a
  template, never a copy: `matchingWebhooks` also matches a copy's template (`webhook/fork_team.go`, a const
  clause), mirrored in `scopedWebhooks`; `GET /v1/webhooks/event-types` hides copies and counts them on their
  template. A copy sends its template's WhatsApp texts. Only the owner may select `whatsapp_message`.
- **Supervision.** Owner and admins see every booking (`scope=all`), read answers, and "Pasar a otra
  persona" (`TeamReassignGuard`: same-área rule, identical for both áreas - the template's owner or a person
  with an ACTIVE copy of it; `teamReassignHost` moves host, seat, `event_type_id` to the new host's copy (or
  the template, for its owner) and remaps answers in one tx; fresh LiveKit host link, `fork_livekit_host_links`).
  Attendance = our token mints (`fork_livekit_mints`) refined by LiveKit webhook sessions
  (`fork_livekit_sessions`), computed per list page (`fork_attendance.go`).
- **Known, out of scope:** staff creating bookings for clients; per-person summary; the owner
  rescheduling others' sessions (the MCP `reschedule_booking` tool still lets admins do it).

## Email - two transports, and the SMTP trap

`internal/mailer` has **two** real transports behind one `Mailer` interface: `smtp.go` and
`resend.go` (HTTPS to `api.resend.com`). **`handler.BuildMailer` is the ONLY place the
choice is made** - boot (`server.go`) and settings-save both call it, so they cannot drift.
The rule: a Resend API key selects HTTPS, else an SMTP host selects SMTP, else `Noop`.

- **Do not turn this into probe-and-fallback.** It was considered and rejected: a probe
  tests reachability at boot rather than at send time, an open TCP port is not a working
  delivery path, and silent switching masks a broken SMTP config while making "which path
  sent this?" unanswerable. Credentials state intent.
- **Why the HTTPS path exists at all:** several platforms (Railway below Pro) block
  outbound SMTP by *dropping* packets. It presents as a hang, then as a credentials
  problem, on every SMTP port, for every provider. Nothing at the SMTP layer fixes it.
- **Keep the dial bounded.** `defaultSMTPTimeout` must be on the dialers (`newDialers`),
  not only on `conn.SetDeadline`, which runs after the dial. Unbounded, a packet-dropping
  host hangs ~2 min and stalls the job queue, which shares the single SQLite connection.
- **`.ics` invites carry `method=REQUEST` in their Content-Type** - that parameter is what
  makes clients show RSVP instead of a file. The Resend path sets `content_type`
  explicitly; if invites ever arrive as plain attachments, look there first
  (`TestICSAttachmentKeepsItsMethodParameter`).
- Adding a secret to email settings: use `storeEmailSecret`, and make the JSON field a
  **pointer** so "omitted" (keep) stays distinguishable from `""` (clear).

## Event types: slot interval is not the duration

`slot_interval_minutes` = how often a booking may **start**. `duration_minutes` = how long it
**runs**. Deliberately independent (a 45-min meeting offered on the hour is valid). New event
types default the interval to the duration; existing ones keep what is stored, so a change of
default is never retroactive. `slots.Generate` refuses a non-positive interval, so create and
update both validate it - an unvalidated `0` yields an event type with no bookable times and
no explanation. Reported as issue #13, where the setting being absent from the admin UI looked
exactly like duration being ignored.

Keep the editor's floor aligned with the API's (`>= 1`). A stricter client-side minimum makes
an event type configured below it via the API unsaveable from the editor, even when the person
is editing an unrelated field.

**The general rule, learned twice:** the admin editor submits the WHOLE form on every save,
so validating a field merely because the request mentions it validates fields the operator
never touched. Any event type holding a stored value the current rules reject then becomes
unsaveable entirely, with an error pointing at something unrelated. Validate on **change**
(effective value vs stored), not on **mention**. Rows reach those states legitimately: a
provider disconnected after the fact, a duplicate that inherited one (#22), a seeder writing
straight to the table, or a create path that defaulted the field before a rule tightened.

The other half of the same rule: **anything written without validation must be valid by
construction.** `CreateEventType` skips `validateLocation` when it defaults the location,
because there is no request field to blame an error on - so every branch of
`smartDefaultLocation` has to return a type the owner can actually host at. It used to end
at an unconditional `"zoom"`.

## Conventions

- `pnpm` (not npm). Use `pnpm exec <tool>` for local binaries.
- Verify changes against the real app, not just builds — this codebase has been
  bitten by CSS that compiles fine but renders wrong.
