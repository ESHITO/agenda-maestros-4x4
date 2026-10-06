package handler_test

// Fork (Agenda Maestros 4x4): booking.host_cancelled - the WhatsApp notice to the person
// who was attending a session when it is cancelled (fork_host_notices.go, queued from
// cancelSideEffects). Every cancel path queues exactly one, to the host at cancel time, with
// the reason; none for a host without a number, an archived host, an unpaid Stripe hold or
// a booking already cancelled. cancelSideEffects queues it just BEFORE the client's
// booking.cancelled, so waiting for that delivery settles the host notice's count.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// hostCancelHooks seeds the owner's two webhooks: the host notice, with every field a
// careless setup would tick (the payload must still address the host only), and the
// client's booking.cancelled (its delivery tells the side effects ran).
func hostCancelHooks(t *testing.T, database *sql.DB, ownerID string) (hostHook, clientHook string) {
	t.Helper()
	hostHook = seedReminderWebhook(t, database, ownerID, webhook.EventHostCancelled, []string{
		"id", "host_id", "status", "cancellation_reason", "host_whatsapp", "host_whatsapp_message",
		"attendee_phone", "attendee_whatsapp", "whatsapp_message", "manage_url",
	})
	clientHook = seedReminderWebhook(t, database, ownerID, "booking.cancelled", []string{"id"})
	return hostHook, clientHook
}

// hostCancelNotices waits for the client's booking.cancelled (n of them) and returns the
// host notices queued by then.
func hostCancelNotices(t *testing.T, database *sql.DB, hostHook, clientHook string, n int) []map[string]any {
	t.Helper()
	waitDeliveries(t, database, clientHook, n)
	return deliveryPayloads(t, database, hostHook)
}

// assertOneHostCancel checks exactly one host_cancelled for bookingID, to wantNumber, with
// the reason in the text (or no Motivo line for ""), and no client destination.
func assertOneHostCancel(t *testing.T, got []map[string]any, bookingID, wantHost, wantNumber, reason string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("host_cancelled deliveries = %d; want exactly 1: %v", len(got), got)
	}
	if got[0]["event"] != webhook.EventHostCancelled {
		t.Errorf("event = %v", got[0]["event"])
	}
	data, _ := got[0]["data"].(map[string]any)
	if data["id"] != bookingID || data["host_id"] != wantHost || data["host_whatsapp"] != wantNumber || data["status"] != "cancelled" {
		t.Errorf("data = %v; want booking %s, host %s at %s, cancelled", data, bookingID, wantHost, wantNumber)
	}
	for _, k := range []string{"attendee_phone", "attendee_whatsapp", "whatsapp_message", "manage_url"} {
		if v, ok := data[k]; ok {
			t.Errorf("host_cancelled carries %s = %v", k, v)
		}
	}
	msg, _ := data["host_whatsapp_message"].(string)
	for _, want := range []string{"🔴 *Sesión cancelada: Test Meeting*", "*Nombre:* Ana", "*Correo:* ana@example.com", "📅 ", "Hora de "} {
		if !strings.Contains(msg, want) {
			t.Errorf("host_whatsapp_message lacks %q:\n%s", want, msg)
		}
	}
	if reason == "" {
		if strings.Contains(msg, "Motivo") {
			t.Errorf("no reason, yet a Motivo line: %s", msg)
		}
	} else if !strings.Contains(msg, "*Motivo:* _"+reason+"_") || data["cancellation_reason"] != reason {
		t.Errorf("reason %q missing: %v\n%s", reason, data["cancellation_reason"], msg)
	}
}

// panelCancel cancels id from the panel (POST /v1/bookings/{id}/cancel) as key.
func panelCancel(t *testing.T, h *handler.Handler, key, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/bookings/"+id+"/cancel", body, key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CancelBooking)(rec, req)
	return rec
}

// tokenCancel cancels with a manage token (POST /manage/{token}/cancel).
func tokenCancel(t *testing.T, h *handler.Handler, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/cancel", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.CancelByToken(rec, req)
	return rec
}

// hostCancelSetup: the owner with a WhatsApp number, an event type, both webhooks and one
// booking whose confirmation side effects have finished.
func hostCancelSetup(t *testing.T) (h *handler.Handler, database *sql.DB, key, ownerID, slug, id, hostHook, clientHook string) {
	t.Helper()
	h, database, key, ownerID = setupWorkspaceWithDB(t)
	mustExec(t, database, `UPDATE users SET iana_timezone = 'America/Lima' WHERE id = ?`, ownerID)
	slug, _ = seedEventTypeHTTP(t, h, key)
	mustStatus(t, putWhatsApp(t, h, key, "", `{"phone":"+51 987 111 222"}`), http.StatusOK, "owner's number")
	hostHook, clientHook = hostCancelHooks(t, database, ownerID)
	id = bookAndSettle(t, h, database, slug)
	return
}

// The panel, as the owner (an admin: CancelByID), with a reason; a second cancel is a 409
// and queues nothing more.
func TestHostCancelled_panelAdmin(t *testing.T) {
	h, database, key, ownerID, _, id, hostHook, clientHook := hostCancelSetup(t)
	mustStatus(t, panelCancel(t, h, key, id, `{"reason":"El mentor tuvo un imprevisto"}`), http.StatusOK, "panel cancel")
	got := hostCancelNotices(t, database, hostHook, clientHook, 1)
	assertOneHostCancel(t, got, id, ownerID, "51987111222", "El mentor tuvo un imprevisto")

	mustStatus(t, panelCancel(t, h, key, id, `{"reason":"otra vez"}`), http.StatusConflict, "second cancel")
	if n := len(deliveryPayloads(t, database, hostHook)); n != 1 {
		t.Errorf("after an already-cancelled cancel: %d host notices; want still 1", n)
	}
	if n := len(deliveryPayloads(t, database, clientHook)); n != 1 {
		t.Errorf("client notices = %d; want 1", n)
	}
}

// The panel, as a plain member cancelling the session they host (booking.Service.Cancel):
// the notice goes to THEM - the host at cancel time - not to whoever booked it first.
func TestHostCancelled_panelMemberHostAtCancelTime(t *testing.T) {
	h, database, _, _, _, id, hostHook, clientHook := hostCancelSetup(t)
	memberKey := addMember(t, database, "m1", "Europe/Madrid")
	mustExec(t, database, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES ('m1','+34612345678','x')`)
	mustExec(t, database, `UPDATE bookings SET host_id = 'm1' WHERE id = ?`, id)
	mustStatus(t, panelCancel(t, h, memberKey, id, `{}`), http.StatusOK, "member cancel")
	got := hostCancelNotices(t, database, hostHook, clientHook, 1)
	assertOneHostCancel(t, got, id, "m1", "34612345678", "")
	if msg, _ := got[0]["data"].(map[string]any)["host_whatsapp_message"].(string); !strings.Contains(msg, "Hora de España") {
		t.Errorf("the date is not in the member's own zone: %s", msg)
	}
}

// The client's /manage page (CancelByToken), with the reason it requires.
func TestHostCancelled_managePage(t *testing.T) {
	h, database, _, ownerID, _, id, hostHook, clientHook := hostCancelSetup(t)
	tok := issueTestToken(t, database, id)
	mustStatus(t, tokenCancel(t, h, tok, `{"reason":"Me surgió un viaje"}`), http.StatusOK, "cancel by token")
	got := hostCancelNotices(t, database, hostHook, clientHook, 1)
	assertOneHostCancel(t, got, id, ownerID, "51987111222", "Me surgió un viaje")
	mustStatus(t, tokenCancel(t, h, tok, `{"reason":"de nuevo"}`), http.StatusConflict, "second cancel by token")
	if n := len(deliveryPayloads(t, database, hostHook)); n != 1 {
		t.Errorf("host notices = %d; want still 1", n)
	}
}

// The WhatsApp {cancelar} short link: /c/{code} → /manage/{token} → the same cancel.
func TestHostCancelled_shortCancelLink(t *testing.T) {
	h, database, key, ownerID, whs, _ := shortLinkWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	mustStatus(t, putWhatsApp(t, h, key, "", `{"phone":"+51 987 111 222"}`), http.StatusOK, "owner's number")
	hostHook, clientHook := hostCancelHooks(t, database, ownerID)
	id := bookAndSettle(t, h, database, slug)
	code := newCode(t, whs, id, webhook.ShortLinkManage)
	loc := followShortLink(t, h, h.ShortManageLink, code)
	tok := strings.TrimPrefix(loc, shortSite+"/manage/")
	mustStatus(t, tokenCancel(t, h, tok, `{"reason":"No llego a tiempo"}`), http.StatusOK, "cancel from /c")
	got := hostCancelNotices(t, database, hostHook, clientHook, 1)
	assertOneHostCancel(t, got, id, ownerID, "51987111222", "No llego a tiempo")
}

// MCP's cancel_booking (the stdio operator: CancelByID).
func TestHostCancelled_mcp(t *testing.T) {
	h, database, _, ownerID, _, id, hostHook, clientHook := hostCancelSetup(t)
	cs := connectMCP(t, h)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cancel_booking", Arguments: map[string]any{
		"booking_id": id, "reason": "Cancelada por el equipo",
	}})
	if err != nil || res.IsError {
		t.Fatalf("cancel_booking: %v %+v", err, res)
	}
	got := hostCancelNotices(t, database, hostHook, clientHook, 1)
	assertOneHostCancel(t, got, id, ownerID, "51987111222", "Cancelada por el equipo")
}

// No notice for a host with no WhatsApp number, an archived host, or an unpaid Stripe hold
// (it never had a host_created) - while the client's booking.cancelled still goes.
func TestHostCancelled_skips(t *testing.T) {
	t.Run("host without a number", func(t *testing.T) {
		h, database, key, ownerID := setupWorkspaceWithDB(t)
		slug, _ := seedEventTypeHTTP(t, h, key)
		hostHook, clientHook := hostCancelHooks(t, database, ownerID)
		id := bookAndSettle(t, h, database, slug)
		mustStatus(t, panelCancel(t, h, key, id, `{"reason":"x"}`), http.StatusOK, "cancel")
		if got := hostCancelNotices(t, database, hostHook, clientHook, 1); len(got) != 0 {
			t.Errorf("host notices = %d; want 0 (no number)", len(got))
		}
	})
	t.Run("archived host", func(t *testing.T) {
		h, database, key, _, _, id, hostHook, clientHook := hostCancelSetup(t)
		addMember(t, database, "m2", "America/Lima")
		mustExec(t, database, `INSERT INTO fork_member_phones (user_id, phone, updated_at) VALUES ('m2','+51987333444','x')`)
		mustExec(t, database, `UPDATE users SET archived_at = ? WHERE id = 'm2'`, time.Now().UTC().Format(time.RFC3339))
		mustExec(t, database, `UPDATE bookings SET host_id = 'm2' WHERE id = ?`, id)
		mustStatus(t, panelCancel(t, h, key, id, `{"reason":"x"}`), http.StatusOK, "cancel")
		if got := hostCancelNotices(t, database, hostHook, clientHook, 1); len(got) != 0 {
			t.Errorf("host notices = %d; want 0 (archived host)", len(got))
		}
	})
	t.Run("unpaid Stripe hold", func(t *testing.T) {
		h, database, key, _, _, id, hostHook, clientHook := hostCancelSetup(t)
		mustExec(t, database, `UPDATE bookings SET payment_status = 'pending' WHERE id = ?`, id)
		mustStatus(t, panelCancel(t, h, key, id, `{"reason":"x"}`), http.StatusOK, "cancel")
		if got := hostCancelNotices(t, database, hostHook, clientHook, 1); len(got) != 0 {
			t.Errorf("host notices = %d; want 0 (unpaid hold)", len(got))
		}
	})
}

// stallMailer blocks every send until its context ends, like an SMTP server that drops
// packets (defaultSMTPTimeout is 30 s; a failed send waits 5 s and retries).
type stallMailer struct{ sends atomic.Int32 }

func (m *stallMailer) Send(ctx context.Context, _ mailer.Message) error {
	m.sends.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// The cancellation e-mails may spend all of cancelSideEffects' context; the host notice and
// the client's booking.cancelled still go, on a budget of their own.
func TestHostCancelled_slowMailDoesNotEatTheNotices(t *testing.T) {
	h, database, key, ownerID, _, id, hostHook, clientHook := hostCancelSetup(t)
	defer handler.SetCancelSideEffectsTimeoutForTest(300 * time.Millisecond)()
	stall := &stallMailer{}
	h.SetMailer(stall, "http://localhost")
	mustStatus(t, panelCancel(t, h, key, id, `{"reason":"Sin correo"}`), http.StatusOK, "panel cancel")
	got := hostCancelNotices(t, database, hostHook, clientHook, 1)
	assertOneHostCancel(t, got, id, ownerID, "51987111222", "Sin correo")
	if stall.sends.Load() == 0 {
		t.Error("no cancellation e-mail was attempted: the test did not exercise a spent context")
	}
}
