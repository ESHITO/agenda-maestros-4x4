package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/handler"
)

// Fork (Agenda Maestros 4x4): reminder webhooks - scheduling on create/reschedule/cancel,
// the "webhook.reminder" job handler, the new events and the settings endpoint.

type reminderJob struct {
	Kind, StartAt, RunAt, Payload string
}

// reminderJobs returns the booking's pending webhook.reminder jobs keyed by kind.
func reminderJobs(t *testing.T, database *sql.DB, bookingID string) map[string]reminderJob {
	t.Helper()
	rows, err := database.Query(`
		SELECT json_extract(payload, '$.kind'), json_extract(payload, '$.start_at'), run_at, payload
		FROM jobs
		WHERE type = 'webhook.reminder' AND status = 'pending'
		  AND json_extract(payload, '$.booking_id') = ?`, bookingID)
	if err != nil {
		t.Fatalf("query reminder jobs: %v", err)
	}
	defer rows.Close()
	out := map[string]reminderJob{}
	for rows.Next() {
		var j reminderJob
		if err := rows.Scan(&j.Kind, &j.StartAt, &j.RunAt, &j.Payload); err != nil {
			t.Fatal(err)
		}
		out[j.Kind] = j
	}
	return out
}

// waitReminderJobs polls until cond holds for the booking's reminder jobs (the side
// effects run in a goroutine after the HTTP response).
func waitReminderJobs(t *testing.T, database *sql.DB, bookingID string, what string, cond func(map[string]reminderJob) bool) map[string]reminderJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		jobs := reminderJobs(t, database, bookingID)
		if cond(jobs) {
			return jobs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: reminder jobs never reached the expected state; last seen %v", what, jobs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// bookInZone creates a booking through POST /v1/bookings for an attendee in tz.
func bookInZone(t *testing.T, h *handler.Handler, slug string, start time.Time, tz, extra string) string {
	t.Helper()
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Ana","email":"ana@example.com","timezone":%q%s}`,
		slug, start.UTC().Format(time.RFC3339), tz, extra)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	return mustString(t, mustCreated(t, rec, "create booking"), "id", "create booking")
}

func TestCreateBooking_schedulesWebhookReminders(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t) // host zone: UTC
	slug, _ := seedEventTypeHTTP(t, h, key)

	// 15:00 UTC = 10:00 in Lima (UTC-5): morning = 08:00 LIMA = 13:00 UTC, not 08:00 UTC.
	start := futureAt(10, 15, 0)
	id := bookInZone(t, h, slug, start, "America/Lima", "")

	jobs := waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	day := start.Format("2006-01-02")
	for kind, want := range map[string]string{
		"morning": day + "T13:00:00Z",
		"1h":      day + "T14:00:00Z",
		"5m":      day + "T14:55:00Z",
	} {
		j, ok := jobs[kind]
		if !ok {
			t.Errorf("no %s job; got %v", kind, jobs)
			continue
		}
		if j.RunAt != want {
			t.Errorf("%s run_at = %s; want %s", kind, j.RunAt, want)
		}
		if j.StartAt != start.Format(time.RFC3339) {
			t.Errorf("%s payload start_at = %s; want %s", kind, j.StartAt, start.Format(time.RFC3339))
		}
	}
}

func TestRescheduleBooking_replacesWebhookReminders(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	oldStart := futureAt(10, 15, 0)
	id := bookInZone(t, h, slug, oldStart, "America/Lima", "")
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })

	newStart := futureAt(12, 18, 0) // 13:00 Lima
	rec := patchReschedule(t, h, id, newStart.Format(time.RFC3339), key)
	mustStatus(t, rec, http.StatusOK, "reschedule")

	want := newStart.Format(time.RFC3339)
	jobs := waitReminderJobs(t, database, id, "reschedule", func(j map[string]reminderJob) bool {
		if len(j) != 3 {
			return false
		}
		for _, job := range j {
			if job.StartAt != want {
				return false
			}
		}
		return true
	})
	day := newStart.Format("2006-01-02")
	if jobs["morning"].RunAt != day+"T13:00:00Z" || jobs["1h"].RunAt != day+"T17:00:00Z" || jobs["5m"].RunAt != day+"T17:55:00Z" {
		t.Errorf("rescheduled jobs = %v", jobs)
	}
	// Nothing planned for the old time survives (pending or otherwise).
	var stale int
	database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type = 'webhook.reminder'
		AND json_extract(payload, '$.booking_id') = ? AND json_extract(payload, '$.start_at') = ?`,
		id, oldStart.Format(time.RFC3339)).Scan(&stale)
	if stale != 0 {
		t.Errorf("%d reminder jobs still planned for the old start", stale)
	}
}

func TestCancelBooking_deletesWebhookReminders(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", "")
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })

	req := authReq(http.MethodPost, "/v1/bookings/"+id+"/cancel", `{"reason":"test"}`, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CancelBooking)(rec, req)
	mustStatus(t, rec, http.StatusOK, "cancel")

	waitReminderJobs(t, database, id, "cancel", func(j map[string]reminderJob) bool { return len(j) == 0 })
}

// seedReminderWebhook subscribes a webhook owned by userID to event with the given
// payload fields, straight in the table (no URL/DNS validation needed: nothing is sent).
func seedReminderWebhook(t *testing.T, database *sql.DB, userID, event string, fields []string) string {
	t.Helper()
	id := "wh-" + strings.ReplaceAll(event, ".", "-")
	ev, _ := json.Marshal([]string{event})
	fs, _ := json.Marshal(fields)
	if _, err := database.Exec(`INSERT INTO webhooks (id, user_id, url, events, secret_enc, fields)
		VALUES (?, ?, 'https://hooks.example.com/funnelchat', ?, 'unused', ?)`, id, userID, string(ev), string(fs)); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	return id
}

func deliveryPayloads(t *testing.T, database *sql.DB, webhookID string) []map[string]any {
	t.Helper()
	rows, err := database.Query(`SELECT payload FROM webhook_deliveries WHERE webhook_id = ?`, webhookID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatal(err)
		}
		out = append(out, env)
	}
	return out
}

func TestJobWebhookReminder_enqueuesDelivery(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	h.SetPublicBaseURL("https://citas.example.com")
	slug, etID := seedEventTypeHTTP(t, h, key)
	if _, err := database.Exec(`INSERT INTO event_type_questions (id, event_type_id, label, type, required, position)
		VALUES ('q-wa', ?, 'WhatsApp', 'phone', 1, 0)`, etID); err != nil {
		t.Fatal(err)
	}
	start := futureAt(10, 15, 0)
	id := bookInZone(t, h, slug, start, "America/Lima",
		`,"language":"es","answers":[{"question_id":"q-wa","value":"+51 987-654-321"}]`)
	jobs := waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })

	whID := seedReminderWebhook(t, database, userID, "booking.reminder_1h",
		[]string{"id", "attendee_phone", "attendee_whatsapp", "start_local", "start_local_time", "manage_url"})
	if err := h.JobWebhookReminder(context.Background(), jobs["1h"].Payload); err != nil {
		t.Fatalf("JobWebhookReminder: %v", err)
	}

	got := deliveryPayloads(t, database, whID)
	if len(got) != 1 {
		t.Fatalf("deliveries = %d; want 1", len(got))
	}
	if got[0]["event"] != "booking.reminder_1h" {
		t.Errorf("event = %v", got[0]["event"])
	}
	data, _ := got[0]["data"].(map[string]any)
	if data["id"] != id || data["attendee_phone"] != "+51987654321" || data["attendee_whatsapp"] != "51987654321" {
		t.Errorf("data = %v", data)
	}
	if data["start_local_time"] != "10:00" { // Lima wall clock, 24 h Spanish clock
		t.Errorf("start_local_time = %v; want 10:00 (attendee's zone)", data["start_local_time"])
	}
	if s, _ := data["start_local"].(string); !strings.HasSuffix(s, ", 10:00") {
		t.Errorf("start_local = %v", data["start_local"])
	}
	// manage_url is a working /manage link for THIS booking (a fresh, additive token).
	mu, _ := data["manage_url"].(string)
	const prefix = "https://citas.example.com/manage/"
	if !strings.HasPrefix(mu, prefix) {
		t.Fatalf("manage_url = %q; want %s<token>", mu, prefix)
	}
	b, err := booking.New(database).ValidateManageToken(context.Background(), strings.TrimPrefix(mu, prefix))
	if err != nil || b.ID != id {
		t.Fatalf("manage_url token does not validate for the booking: %v", err)
	}
}

func TestJobWebhookReminder_skipsStaleJobs(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	start := futureAt(10, 15, 0)
	id := bookInZone(t, h, slug, start, "America/Lima", "")
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	whID := seedReminderWebhook(t, database, userID, "booking.reminder_5m", []string{"id"})
	ctx := context.Background()

	run := func(what, payload string) {
		t.Helper()
		if err := h.JobWebhookReminder(ctx, payload); err != nil {
			t.Errorf("%s: JobWebhookReminder returned %v; want nil (no retry for stale data)", what, err)
		}
		if n := len(deliveryPayloads(t, database, whID)); n != 0 {
			t.Fatalf("%s: %d deliveries; want 0", what, n)
		}
	}
	job := func(bookingID, kind string, s time.Time) string {
		return fmt.Sprintf(`{"booking_id":%q,"kind":%q,"start_at":%q}`, bookingID, kind, s.UTC().Format(time.RFC3339))
	}

	run("start_at moved since planning", job(id, "5m", start.Add(time.Hour)))
	run("unknown booking", job("no-such-booking", "5m", start))
	run("unknown kind", job(id, "10m", start))
	run("garbage payload", `{not json`)

	if _, err := database.Exec(`UPDATE bookings SET status = 'cancelled' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	run("cancelled booking", job(id, "5m", start))
}

func TestCreateWebhook_acceptsReminderEvents(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	req := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["booking.reminder_morning","booking.reminder_1h","booking.reminder_5m"],
		  "fields":["attendee_phone","attendee_whatsapp","start_local","start_local_date","start_local_time","manage_url"]}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)
	body := mustCreated(t, rec, "create reminder webhook")
	if ev, _ := body["events"].([]any); len(ev) != 3 {
		t.Errorf("events = %v", body["events"])
	}
	if fs, _ := body["fields"].([]any); len(fs) != 6 {
		t.Errorf("fields = %v; want all six fork fields kept", body["fields"])
	}

	// Still strict: a near-miss is refused.
	req = authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["booking.reminder_10m"]}`, apiKey)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)
	mustStatus(t, rec, http.StatusBadRequest, "unknown reminder event")
}

func TestGetWebhookSettings(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t) // the setup user is the workspace owner
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		h.RequireAuth(h.GetWebhookSettings)(rec, authReq(http.MethodGet, "/v1/webhooks/settings", "", apiKey))
		return mustJSON(t, rec, http.StatusOK, "webhook settings")
	}
	if got := get(); got["reminder_morning_hour"] != "08:00" || got["team_scope"] != true {
		t.Errorf("settings = %v; want 08:00 and team_scope for the owner", got)
	}
	h.SetReminderMorningHour("07:30")
	if got := get(); got["reminder_morning_hour"] != "07:30" {
		t.Errorf("reminder_morning_hour = %v; want 07:30", got["reminder_morning_hour"])
	}
}

// Bookings made before the feature shipped have no reminder jobs; the boot-time
// backfill plans them, skips cancelled/past bookings, and is a no-op when re-run.
func TestBackfillWebhookReminders(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	upcoming := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", "")
	cancelled := bookInZone(t, h, slug, futureAt(11, 15, 0), "America/Lima", "")
	waitReminderJobs(t, database, upcoming, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	waitReminderJobs(t, database, cancelled, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })

	// Simulate "booked before the upgrade": no reminder jobs at all.
	if _, err := database.Exec(`DELETE FROM jobs WHERE type = 'webhook.reminder'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE bookings SET status = 'cancelled' WHERE id = ?`, cancelled); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for run := 1; run <= 2; run++ {
		if _, err := h.BackfillWebhookReminders(ctx); err != nil {
			t.Fatalf("backfill run %d: %v", run, err)
		}
		if got := reminderJobs(t, database, upcoming); len(got) != 3 {
			t.Errorf("run %d: upcoming booking has %d reminder jobs; want 3 (no duplicates on re-run)", run, len(got))
		}
		if got := reminderJobs(t, database, cancelled); len(got) != 0 {
			t.Errorf("run %d: cancelled booking got %d reminder jobs; want 0", run, len(got))
		}
	}
	var total int
	database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type = 'webhook.reminder'`).Scan(&total)
	if total != 3 {
		t.Errorf("total reminder jobs = %d; want 3", total)
	}
}

// The worker was down: when it comes back, the meeting is 3 minutes away and every
// reminder is overdue. Only the 5-minute one is still true; the morning and 1 h ones
// are dropped instead of arriving in a burst.
func TestJobWebhookReminder_dropsLateReminders(t *testing.T) {
	h, database, key, userID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := bookInZone(t, h, slug, futureAt(10, 15, 0), "America/Lima", "")
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })

	soon := time.Now().UTC().Add(3 * time.Minute).Truncate(time.Second)
	if _, err := database.Exec(`UPDATE bookings SET start_at = ?, end_at = ? WHERE id = ?`,
		soon.Format(time.RFC3339), soon.Add(30*time.Minute).Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]string{}
	for kind, event := range map[string]string{"morning": "booking.reminder_morning", "1h": "booking.reminder_1h", "5m": "booking.reminder_5m"} {
		hooks[kind] = seedReminderWebhook(t, database, userID, event, []string{"id"})
	}
	for _, kind := range []string{"morning", "1h", "5m"} {
		payload := fmt.Sprintf(`{"booking_id":%q,"kind":%q,"start_at":%q}`, id, kind, soon.Format(time.RFC3339))
		if err := h.JobWebhookReminder(context.Background(), payload); err != nil {
			t.Fatalf("%s: JobWebhookReminder: %v", kind, err)
		}
	}
	for kind, want := range map[string]int{"morning": 0, "1h": 0, "5m": 1} {
		if got := len(deliveryPayloads(t, database, hooks[kind])); got != want {
			t.Errorf("%s: %d deliveries; want %d", kind, got, want)
		}
	}
}

// Changing REMINDER_MORNING_HOUR must reach bookings already on the calendar: INSERT OR
// IGNORE alone would keep their morning jobs at the old hour. The boot backfill moves a
// pending one, drops it when the new rule gives the meeting none, and never plans one
// that already ran a second time.
func TestBackfillWebhookReminders_followsMorningHourChange(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	start := futureAt(10, 15, 0) // 10:00 in Lima (UTC-5, no DST)
	id := bookInZone(t, h, slug, start, "America/Lima", "")
	waitReminderJobs(t, database, id, "create", func(j map[string]reminderJob) bool { return len(j) == 3 })
	day := start.Format("2006-01-02")
	ctx := context.Background()

	backfill := func(hour string) map[string]reminderJob {
		t.Helper()
		if !h.SetReminderMorningHour(hour) {
			t.Fatalf("SetReminderMorningHour(%s) refused", hour)
		}
		if _, err := h.BackfillWebhookReminders(ctx); err != nil {
			t.Fatalf("backfill at %s: %v", hour, err)
		}
		return reminderJobs(t, database, id)
	}

	if j := backfill("07:00"); len(j) != 3 || j["morning"].RunAt != day+"T12:00:00Z" {
		t.Errorf("after 07:00: %v; want the pending morning job moved to 12:00Z (07:00 Lima)", j)
	}
	// 09:30 is less than an hour before a 10:00 meeting: no morning reminder under the rule.
	if j := backfill("09:30"); len(j) != 2 || j["morning"] != (reminderJob{}) {
		t.Errorf("after 09:30: %v; want the morning job gone, 1h and 5m kept", j)
	}
	if j := backfill("08:00"); len(j) != 3 || j["morning"].RunAt != day+"T13:00:00Z" {
		t.Errorf("after 08:00: %v; want the morning job back at 13:00Z", j)
	}

	// The morning reminder has run. Moving the hour later must not plan it again.
	if _, err := database.Exec(`UPDATE jobs SET status = 'done', finished_at = ? WHERE type = 'webhook.reminder'
		AND json_extract(payload, '$.booking_id') = ? AND json_extract(payload, '$.kind') = 'morning'`,
		time.Now().UTC().Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
	if j := backfill("08:30"); j["morning"] != (reminderJob{}) {
		t.Errorf("after the morning reminder ran, 08:30 planned another: %v", j)
	}
	var mornings int
	database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type = 'webhook.reminder'
		AND json_extract(payload, '$.booking_id') = ? AND json_extract(payload, '$.kind') = 'morning'`, id).Scan(&mornings)
	if mornings != 1 {
		t.Errorf("morning jobs for the booking = %d; want 1 (the one that ran)", mornings)
	}
}

// An attendee with no stored zone (API/MCP bookings store "UTC") borrows the host's, both
// for the morning moment and for start_local. Reassigning the booking to a host in
// another zone must re-plan the morning reminder in the NEW host's zone.
func TestReassignBooking_replansMorningReminderInNewHostZone(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','h2@example.com','Madrid','Europe/Madrid',0)`)
	mustExec(t, database, `INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','h3@example.com','Lima','America/Lima',0)`)
	mustExec(t, database, `INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u2','et-slug','Intro',30)`)
	start := futureAt(10, 15, 0)
	mustExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b1','et1','u2',?,?,'confirmed')`, start.Format(time.RFC3339), start.Add(30*time.Minute).Format(time.RFC3339))
	mustExec(t, database, `INSERT INTO booking_attendees (id,booking_id,name,email,iana_timezone,is_organizer)
		VALUES ('a1','b1','Alice','alice@example.com','UTC',1)`)

	morningIn := func(zone string) string {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		l := start.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day(), 8, 0, 0, 0, loc).UTC().Format(time.RFC3339)
	}
	if _, err := h.BackfillWebhookReminders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := reminderJobs(t, database, "b1"); j["morning"].RunAt != morningIn("Europe/Madrid") {
		t.Fatalf("before reassign: morning at %q; want %s (08:00 Madrid)", j["morning"].RunAt, morningIn("Europe/Madrid"))
	}

	req := authReq(http.MethodPost, "/v1/bookings/b1/reassign", `{"host_id":"u3"}`, ownerKey)
	req.SetPathValue("id", "b1")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ReassignBooking)(rec, req)
	mustStatus(t, rec, http.StatusOK, "reassign")

	want := morningIn("America/Lima")
	waitReminderJobs(t, database, "b1", "reassign", func(j map[string]reminderJob) bool {
		return len(j) == 3 && j["morning"].RunAt == want
	})
}
