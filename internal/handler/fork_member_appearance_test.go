package handler_test

// Fork (Agenda Maestros 4x4): the owner and the admins set another person's booking accent
// and photo (fork_member_appearance.go), and a team copy's booking surfaces show the look of
// the person who attends it.

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Hex ids: the avatar file is named after the id, so the endpoints (like ServeAvatar)
// accept only [0-9a-f-].
const (
	apAdmin    = "ad000001"
	apAdmin2   = "ad000002"
	apMentor   = "ae000001"
	apSupport  = "ae000002"
	apArchived = "ae000003"
)

type appearanceFixture struct {
	*teamFixture
	adminKey, admin2Key, mentorKey, supportKey string
	dataDir                                    string
}

func newAppearanceFixture(t *testing.T) *appearanceFixture {
	t.Helper()
	f := &appearanceFixture{teamFixture: newTeamFixture(t)}
	f.dataDir = t.TempDir()
	f.h.SetDataDir(f.dataDir)
	f.adminKey = addMember(t, f.db, apAdmin, "UTC")
	f.admin2Key = addMember(t, f.db, apAdmin2, "UTC")
	f.mentorKey = addMember(t, f.db, apMentor, "UTC")
	f.supportKey = addMember(t, f.db, apSupport, "UTC")
	addMember(t, f.db, apArchived, "UTC")
	mustExec(t, f.db, `UPDATE users SET is_admin = 1 WHERE id IN (?, ?)`, apAdmin, apAdmin2)
	mustExec(t, f.db, `UPDATE users SET name = 'Daniel Pérez' WHERE id = ?`, apMentor)
	mustExec(t, f.db, `UPDATE users SET archived_at = '2026-01-01T00:00:00Z' WHERE id = ?`, apArchived)
	return f
}

func (f *appearanceFixture) patchAppearance(key, id, body string) *httptest.ResponseRecorder {
	return f.call(f.h.PatchMemberAppearance, http.MethodPatch, "/v1/users/"+id+"/appearance", body, key, "id", id)
}

func (f *appearanceFixture) accentOf(id string) string {
	return f.scalar(`SELECT booking_accent FROM users WHERE id = ?`, id)
}

// multipartReq builds an authenticated multipart POST with an "avatar" file.
func multipartReq(t *testing.T, path, key string, file []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("avatar", "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(file)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-API-Key", key)
	return req
}

func (f *appearanceFixture) uploadMember(key, id string, file []byte) *httptest.ResponseRecorder {
	req := multipartReq(f.t, "/v1/users/"+id+"/avatar", key, file)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	f.h.RequireAuth(f.h.UploadMemberAvatar)(rec, req)
	return rec
}

func (f *appearanceFixture) deleteMemberAvatar(key, id string) *httptest.ResponseRecorder {
	return f.call(f.h.DeleteMemberAvatar, http.MethodDelete, "/v1/users/"+id+"/avatar", "", key, "id", id)
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMemberAppearance_permissionMatrix(t *testing.T) {
	f := newAppearanceFixture(t)
	cases := []struct {
		actor, actorKey, target string
		want                    int
	}{
		// The owner: anyone active, themselves included.
		{"owner", f.ownerKey, f.ownerID, http.StatusOK},
		{"owner", f.ownerKey, apAdmin, http.StatusOK},
		{"owner", f.ownerKey, apMentor, http.StatusOK},
		// An admin: non-admin members and themselves; never the owner or another admin.
		{"admin", f.adminKey, apAdmin, http.StatusOK},
		{"admin", f.adminKey, apMentor, http.StatusOK},
		{"admin", f.adminKey, apSupport, http.StatusOK},
		{"admin", f.adminKey, apAdmin2, http.StatusForbidden},
		{"admin", f.adminKey, f.ownerID, http.StatusForbidden},
		// Everybody else: 403, even on themselves (they keep /v1/users/me).
		{"mentor", f.mentorKey, apMentor, http.StatusForbidden},
		{"mentor", f.mentorKey, apSupport, http.StatusForbidden},
		{"support", f.supportKey, f.ownerID, http.StatusForbidden},
		// Archived, unknown and malformed targets: 404.
		{"owner", f.ownerKey, apArchived, http.StatusNotFound},
		{"owner", f.ownerKey, "abcdef99", http.StatusNotFound},
		{"owner", f.ownerKey, "..%2f..%2fetc", http.StatusNotFound},
		{"admin", f.adminKey, apArchived, http.StatusNotFound},
	}
	for _, c := range cases {
		before := f.accentOf(c.target)
		rec := f.patchAppearance(c.actorKey, c.target, `{"booking_accent":"#123abc"}`)
		if rec.Code != c.want {
			t.Errorf("%s → %s accent: status %d; want %d — %s", c.actor, c.target, rec.Code, c.want, rec.Body)
		}
		if c.want != http.StatusOK && f.accentOf(c.target) != before {
			t.Errorf("%s → %s: a refused call changed the accent", c.actor, c.target)
		}
		// The avatar pair runs the same check.
		if up := f.uploadMember(c.actorKey, c.target, testPNG(t, 8, 8)); up.Code != c.want {
			t.Errorf("%s → %s avatar upload: status %d; want %d — %s", c.actor, c.target, up.Code, c.want, up.Body)
		}
		wantDel := c.want
		if wantDel == http.StatusOK {
			wantDel = http.StatusNoContent
		}
		if del := f.deleteMemberAvatar(c.actorKey, c.target); del.Code != wantDel {
			t.Errorf("%s → %s avatar delete: status %d; want %d — %s", c.actor, c.target, del.Code, wantDel, del.Body)
		}
	}
	// Refused uploads wrote no file.
	if _, err := os.Stat(filepath.Join(f.dataDir, "avatars", f.ownerID+".jpg")); err == nil {
		// The owner uploaded their own (allowed) and deleted it again, so nothing may remain.
		t.Error("an avatar file is left for the owner")
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "avatars", apAdmin2+".jpg")); err == nil {
		t.Error("an admin wrote another admin's avatar")
	}
}

func TestMemberAppearance_accentValidationMatchesMe(t *testing.T) {
	f := newAppearanceFixture(t)
	for _, bad := range []string{`"red"`, `"#fff"`, `"#12345g"`, `"#fff;background:red"`, `" #123456"`} {
		rec := f.patchAppearance(f.ownerKey, apMentor, `{"booking_accent":`+bad+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("accent %s: status %d; want 400", bad, rec.Code)
		}
		// PATCH /v1/users/me refuses exactly the same values.
		if me := patchMe(t, f.h, `{"booking_accent":`+bad+`}`, f.ownerKey); me.Code != http.StatusBadRequest {
			t.Errorf("/me accent %s: status %d; want 400", bad, me.Code)
		}
	}
	mustStatus(t, f.patchAppearance(f.ownerKey, apMentor, `{}`), http.StatusBadRequest, "no booking_accent")
	mustStatus(t, f.patchAppearance(f.ownerKey, apMentor, `nope`), http.StatusBadRequest, "bad JSON")

	body := mustJSON(t, f.patchAppearance(f.ownerKey, apMentor, `{"booking_accent":"#4FD7FF"}`), http.StatusOK, "set accent")
	if body["booking_accent"] != "#4fd7ff" || body["accent_custom"] != true {
		t.Errorf("set accent answered %v", body)
	}
	if got := f.accentOf(apMentor); got != "#4fd7ff" {
		t.Errorf("stored accent %q; want the lower-cased value, like /me stores it", got)
	}
	// "" resets to the column default, which the booking pages treat as "not chosen".
	body = mustJSON(t, f.patchAppearance(f.ownerKey, apMentor, `{"booking_accent":""}`), http.StatusOK, "reset accent")
	if body["booking_accent"] != "#111827" || body["accent_custom"] != false {
		t.Errorf("reset answered %v", body)
	}
	if got := f.accentOf(apMentor); got != "#111827" {
		t.Errorf("stored accent after reset %q", got)
	}
}

func TestMemberAvatar_sameProcessingAsMe(t *testing.T) {
	f := newAppearanceFixture(t)

	// A big, wide PNG is stored as a JPEG that fits 400×400, under the target's id.
	body := mustJSON(t, f.uploadMember(f.adminKey, apMentor, testPNG(t, 1000, 600)), http.StatusOK, "upload")
	firstURL, _ := body["avatar_url"].(string)
	if !strings.HasPrefix(firstURL, "/avatars/"+apMentor+"?v=") {
		t.Errorf("avatar_url = %v; want /avatars/{id}?v=…", body["avatar_url"])
	}
	if got := f.scalar(`SELECT avatar_url FROM users WHERE id = ?`, apMentor); got != firstURL {
		t.Errorf("stored avatar_url = %q; want the answered %q", got, firstURL)
	}
	if got := f.scalar(`SELECT COALESCE(avatar_url,'') FROM users WHERE id = ?`, apAdmin); got != "" {
		t.Errorf("the actor's own avatar_url changed to %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(f.dataDir, "avatars", apMentor+".jpg"))
	if err != nil {
		t.Fatalf("stored file: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("stored file is not a JPEG: %v", err)
	}
	if cfg.Width != 400 || cfg.Height != 240 {
		t.Errorf("stored size %dx%d; want 400x240 (fit within 400×400)", cfg.Width, cfg.Height)
	}

	// Rejections answer exactly what /v1/users/me/avatar answers.
	for name, file := range map[string][]byte{
		"not an image": []byte("hello, this is plain text and not an image at all"),
		"broken png":   append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...),
	} {
		member := f.uploadMember(f.ownerKey, apMentor, file)
		meRec := httptest.NewRecorder()
		f.h.RequireAuth(f.h.UploadAvatar)(meRec, multipartReq(t, "/v1/users/me/avatar", f.ownerKey, file))
		if member.Code != http.StatusBadRequest || member.Code != meRec.Code || member.Body.String() != meRec.Body.String() {
			t.Errorf("%s: member %d %s; /me %d %s", name, member.Code, member.Body, meRec.Code, meRec.Body)
		}
	}
	// Missing field, same answer as /me too.
	req := httptest.NewRequest(http.MethodPost, "/v1/users/"+apMentor+"/avatar", strings.NewReader("x"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=zzz")
	req.Header.Set("X-API-Key", f.ownerKey)
	req.SetPathValue("id", apMentor)
	rec := httptest.NewRecorder()
	f.h.RequireAuth(f.h.UploadMemberAvatar)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed multipart: %d", rec.Code)
	}

	// Delete removes the file and the column.
	mustStatus(t, f.deleteMemberAvatar(f.adminKey, apMentor), http.StatusNoContent, "delete")
	if _, err := os.Stat(filepath.Join(f.dataDir, "avatars", apMentor+".jpg")); !os.IsNotExist(err) {
		t.Errorf("file still there after delete: %v", err)
	}
	if got := f.scalar(`SELECT COALESCE(avatar_url,'') FROM users WHERE id = ?`, apMentor); got != "" {
		t.Errorf("avatar_url after delete = %q", got)
	}
}

func TestListUsers_appearanceFields(t *testing.T) {
	f := newAppearanceFixture(t)
	mustStatus(t, f.patchAppearance(f.ownerKey, apMentor, `{"booking_accent":"#ec4899"}`), http.StatusOK, "set accent")

	list := func(key string) map[string]map[string]any {
		t.Helper()
		rec := f.call(f.h.ListUsers, http.MethodGet, "/v1/users?include_archived=true", "", key)
		mustStatus(t, rec, http.StatusOK, "list users")
		var rows []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]any{}
		for _, r := range rows {
			out[r["id"].(string)] = r
		}
		return out
	}

	byOwner := list(f.ownerKey)
	if r := byOwner[apMentor]; r["booking_accent"] != "#ec4899" || r["accent_custom"] != true {
		t.Errorf("mentor row = %v", r)
	}
	if r := byOwner[apSupport]; r["booking_accent"] != "#111827" || r["accent_custom"] != false {
		t.Errorf("support row (default accent) = %v", r)
	}
	for id, want := range map[string]bool{f.ownerID: true, apAdmin: true, apAdmin2: true, apMentor: true, apArchived: false} {
		if got := byOwner[id]["can_edit_appearance"]; got != want {
			t.Errorf("owner viewing %s: can_edit_appearance = %v; want %v", id, got, want)
		}
	}
	byAdmin := list(f.adminKey)
	for id, want := range map[string]bool{f.ownerID: false, apAdmin: true, apAdmin2: false, apMentor: true, apSupport: true, apArchived: false} {
		if got := byAdmin[id]["can_edit_appearance"]; got != want {
			t.Errorf("admin viewing %s: can_edit_appearance = %v; want %v", id, got, want)
		}
	}
}

// A team copy is owned by the templates' owner and hosted by the mentor: its booking page,
// manage page and embed payload take the colour and the face of the mentor; the template
// keeps the owner's; a mentor with no colour of their own falls back to the owner's.
func TestTeamCopy_bookingSurfacesUseHostLook(t *testing.T) {
	f := newAppearanceFixture(t)
	f.mustRole(apMentor, "member", "mentoria")
	f.mustSettings(`{"mentoria_template_id":"` + f.tID + `"}`)
	c := f.copyOf(f.tID, apMentor)
	if c.id == "" || !c.active {
		t.Fatalf("no active copy for the mentor: %+v", c)
	}
	mustExec(t, f.db, `UPDATE users SET booking_accent = '#123456' WHERE id = ?`, f.ownerID)

	public := func(slug string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/public", nil)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		f.h.PublicEventType(rec, req)
		return mustJSON(t, rec, http.StatusOK, "public "+slug)
	}
	bookPage := func(slug string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		f.h.BookPage(rec, req)
		mustStatus(t, rec, http.StatusOK, "book page "+slug)
		return rec.Body.String()
	}
	mustExec(t, f.db, `INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-look', ?, ?, '2099-03-01T10:00:00Z', '2099-03-01T10:30:00Z', 'confirmed')`, c.id, apMentor)
	mustExec(t, f.db, `INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES ('bh-look', 'bk-look', ?, 1)`, apMentor)
	tok := issueTestToken(t, f.db, "bk-look")
	managePage := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/manage/"+tok, nil)
		req.SetPathValue("token", tok)
		rec := httptest.NewRecorder()
		f.h.ManagePage(rec, req)
		mustStatus(t, rec, http.StatusOK, "manage page")
		return rec.Body.String()
	}
	hostOf := func(p map[string]any) map[string]any {
		hosts, _ := p["hosts"].([]any)
		if len(hosts) != 1 {
			t.Fatalf("hosts = %v; want one", p["hosts"])
		}
		return hosts[0].(map[string]any)
	}

	// 1. The mentor has no colour of their own: the copy keeps the owner's (today's source).
	if got := public(c.slug)["booking_accent"]; got != "#123456" {
		t.Errorf("copy accent with the host on the default = %v; want the owner's #123456", got)
	}
	if !strings.Contains(bookPage(c.slug), "--booking-accent:#123456") {
		t.Error("book page of the copy lost the owner fallback")
	}
	if !strings.Contains(managePage(), "--booking-accent:#123456") {
		t.Error("manage page of the copy lost the owner fallback")
	}

	// 2. The owner gives the mentor a colour and a photo.
	mustStatus(t, f.patchAppearance(f.ownerKey, apMentor, `{"booking_accent":"#FF0066"}`), http.StatusOK, "mentor accent")
	mustStatus(t, f.uploadMember(f.ownerKey, apMentor, testPNG(t, 20, 20)), http.StatusOK, "mentor avatar")

	p := public(c.slug)
	if p["booking_accent"] != "#ff0066" || p["booking_accent_foreground"] != "#000000" {
		t.Errorf("copy payload accent = %v / %v; want the mentor's #ff0066", p["booking_accent"], p["booking_accent_foreground"])
	}
	mentorPhoto := f.scalar(`SELECT avatar_url FROM users WHERE id = ?`, apMentor)
	h := hostOf(p)
	if h["name"] != "Daniel Pérez" || !strings.HasSuffix(h["avatar_url"].(string), mentorPhoto) {
		t.Errorf("copy payload host = %v; want the mentor's name and photo", h)
	}
	page := bookPage(c.slug)
	for _, want := range []string{"--booking-accent:#ff0066", `src="` + mentorPhoto + `"`, "Daniel Pérez"} {
		if !strings.Contains(page, want) {
			t.Errorf("copy book page lacks %q", want)
		}
	}
	if strings.Contains(page, "--booking-accent:#123456") {
		t.Error("copy book page still carries the owner's accent")
	}
	mp := managePage()
	for _, want := range []string{"--booking-accent:#ff0066", `src="` + mentorPhoto + `"`, "Daniel Pérez"} {
		if !strings.Contains(mp, want) {
			t.Errorf("copy manage page lacks %q", want)
		}
	}

	// 3. The template itself (the owner's link) keeps the owner's accent and face.
	tp := public(f.tSlug)
	if tp["booking_accent"] != "#123456" {
		t.Errorf("template accent = %v; want the owner's", tp["booking_accent"])
	}
	if hostOf(tp)["name"] == "Daniel Pérez" {
		t.Error("the template shows the mentor as host")
	}
	if !strings.Contains(bookPage(f.tSlug), "--booking-accent:#123456") {
		t.Error("template book page lost the owner's accent")
	}

	// 4. Reset: back to the owner's colour on the copy.
	mustStatus(t, f.patchAppearance(f.ownerKey, apMentor, `{"booking_accent":""}`), http.StatusOK, "reset")
	if got := public(c.slug)["booking_accent"]; got != "#123456" {
		t.Errorf("after reset copy accent = %v; want the owner's", got)
	}
}

// An ordinary type (not a copy) keeps its owner's accent even when its host has another.
func TestOrdinaryType_accentStaysOwners(t *testing.T) {
	f := newAppearanceFixture(t)
	mustExec(t, f.db, `UPDATE users SET booking_accent = '#123456' WHERE id = ?`, f.ownerID)
	mustExec(t, f.db, `UPDATE users SET booking_accent = '#ff0066' WHERE id = ?`, apMentor)
	mustExec(t, f.db, `UPDATE event_type_hosts SET user_id = ? WHERE event_type_id = ?`, apMentor, f.sID)
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+f.sSlug+"/public", nil)
	req.SetPathValue("slug", f.sSlug)
	rec := httptest.NewRecorder()
	f.h.PublicEventType(rec, req)
	if got := mustJSON(t, rec, http.StatusOK, "public")["booking_accent"]; got != "#123456" {
		t.Errorf("ordinary type accent = %v; want its owner's", got)
	}
}

// A replaced photo gets a new avatar_url, from either path, so no browser that cached the
// old one (served with max-age=86400) keeps showing it; the versioned URL still serves the
// current file.
func TestAvatarReplacement_changesURL(t *testing.T) {
	f := newAppearanceFixture(t)

	urlOf := func(rec *httptest.ResponseRecorder, what string) string {
		t.Helper()
		u, _ := mustJSON(t, rec, http.StatusOK, what)["avatar_url"].(string)
		return u
	}
	first := urlOf(f.uploadMember(f.ownerKey, apMentor, testPNG(t, 20, 20)), "first upload")
	time.Sleep(3 * time.Millisecond)
	second := urlOf(f.uploadMember(f.adminKey, apMentor, testPNG(t, 30, 30)), "replacement")
	if first == second || !strings.HasPrefix(second, "/avatars/"+apMentor+"?v=") {
		t.Errorf("replacement URL %q (first %q); want a new /avatars/{id}?v=… URL", second, first)
	}
	if got := f.scalar(`SELECT avatar_url FROM users WHERE id = ?`, apMentor); got != second {
		t.Errorf("stored avatar_url = %q; want %q", got, second)
	}

	// /v1/users/me/avatar shares storeAvatar, so it versions the same way.
	me := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		f.h.RequireAuth(f.h.UploadAvatar)(rec, multipartReq(t, "/v1/users/me/avatar", f.mentorKey, testPNG(t, 20, 20)))
		return urlOf(rec, "/me upload")
	}
	m1 := me()
	time.Sleep(3 * time.Millisecond)
	if m2 := me(); m1 == m2 || !strings.HasPrefix(m2, "/avatars/"+apMentor+"?v=") {
		t.Errorf("/me replacement URL %q (first %q); want a new versioned URL", m2, m1)
	}

	// The query is ignored when serving: the stored URL fetches the current photo.
	stored := f.scalar(`SELECT avatar_url FROM users WHERE id = ?`, apMentor)
	req := httptest.NewRequest(http.MethodGet, stored, nil)
	req.SetPathValue("userID", apMentor)
	rec := httptest.NewRecorder()
	f.h.ServeAvatar(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", stored, rec.Code)
	}
	if _, err := jpeg.DecodeConfig(rec.Body); err != nil {
		t.Errorf("GET %s did not serve the JPEG: %v", stored, err)
	}
}
