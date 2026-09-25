package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
)

// Fork (Agenda Maestros 4x4): the WhatsApp short links' codes are credentials.

func TestRedactTokenPaths_shortLinks(t *testing.T) {
	for in, want := range map[string]string{
		"/e/k3pq9abx":      "/e/[redacted]",
		"/c/k3pq9abx":      "/c/[redacted]",
		"/manage/abc":      "/manage/[redacted]",
		"/embed.js":        "/embed.js",
		"/book/soporte":    "/book/soporte",
		"/cancel/whatever": "/cancel/whatever",
	} {
		if got := redactTokenPaths(in); got != want {
			t.Errorf("redactTokenPaths(%q) = %q; want %q", in, got, want)
		}
	}
}

// Through the real mux: /e and /c are routed, rate-limited per IP on one shared budget
// (20 a minute), and the request log never holds a code.
func TestShortLinkRoutes_rateLimitedAndNotLogged(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	h, drain := New(ctx, &config.Config{BaseURL: "https://citas.example.com", DataDir: t.TempDir(), ForceLocale: "es"}, database, logger)
	defer func() { cancel(); drain() }()

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "198.51.100.7:4242"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	// Well-formed but unknown: the friendly page (a database lookup) - 404.
	if rec := get("/e/zzzzzzzz"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Este enlace ya no es válido") {
		t.Fatalf("/e unknown: %d", rec.Code)
	}
	if rec := get("/c/yyyyyyyy"); rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("/c unknown: %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	for i := 3; i <= 20; i++ {
		if rec := get("/e/k3pq9ab" + string(rune('a'+i%20))); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited; the budget is 20 a minute", i)
		}
	}
	if rec := get("/c/xxxxxxxx"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("request 21: %d; want 429", rec.Code)
	}
	if rec := get("/e/xxxxxxxx"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("request 22: %d; want 429", rec.Code)
	}
	// Another visitor is not affected.
	req := httptest.NewRequest(http.MethodGet, "/e/zzzzzzzz", nil)
	req.RemoteAddr = "203.0.113.9:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("another IP: %d; want 404", rec.Code)
	}

	l := logs.String()
	for _, code := range []string{"zzzzzzzz", "yyyyyyyy", "xxxxxxxx", "k3pq9ab"} {
		if strings.Contains(l, code) {
			t.Errorf("the log holds the code %q", code)
		}
	}
	if !strings.Contains(l, "path=/e/[redacted]") || !strings.Contains(l, "path=/c/[redacted]") {
		t.Errorf("request log lacks the redacted paths")
	}
}

func TestShortLinkClientKey(t *testing.T) {
	for remote, want := range map[string]string{
		"198.51.100.7:4242":                  "198.51.100.7",
		"[2001:db8:1:2::5]:4242":             "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff:ffff:ffff:1]:80": "2001:db8:1:2::/64",
		"[2001:db8:1:3::5]:4242":             "2001:db8:1:3::/64",
		"[::ffff:198.51.100.7]:4242":         "198.51.100.7", // IPv4-mapped: per address
		"[fe80::1%eth0]:4242":                "fe80::/64",
		"not-an-address":                     "not-an-address",
	} {
		req := httptest.NewRequest(http.MethodGet, "/e/k3pq9abx", nil)
		req.RemoteAddr = remote
		if got := shortLinkClientKey(req); got != want {
			t.Errorf("shortLinkClientKey(%q) = %q; want %q", remote, got, want)
		}
	}
}

// Through the real mux: an IPv6 client cannot get a fresh budget by changing its address
// inside its /64 (a single host usually holds the whole /64); another /64 is someone else.
func TestShortLinkRoutes_rateLimitedPerIPv6Slash64(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(t.Context())
	h, drain := New(ctx, &config.Config{BaseURL: "https://citas.example.com", DataDir: t.TempDir(), ForceLocale: "es"}, database, logger)
	defer func() { cancel(); drain() }()

	get := func(remote string) int {
		req := httptest.NewRequest(http.MethodGet, "/e/zzzzzzzz", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 1; i <= 20; i++ {
		if code := get(fmt.Sprintf("[2001:db8:1:2::%x]:4242", i)); code != http.StatusNotFound {
			t.Fatalf("request %d: %d; want 404 (the budget is 20 a minute)", i, code)
		}
	}
	if code := get("[2001:db8:1:2:abcd::99]:4242"); code != http.StatusTooManyRequests {
		t.Errorf("21st request from a new address in the same /64: %d; want 429", code)
	}
	if code := get("[2001:db8:1:3::1]:4242"); code != http.StatusNotFound {
		t.Errorf("another /64: %d; want 404", code)
	}
}
