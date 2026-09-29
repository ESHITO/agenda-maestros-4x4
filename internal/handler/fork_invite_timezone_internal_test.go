package handler

import (
	"context"
	"log/slog"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

// claimTimezone's fallback chain: browser, then the inviter, then the owner, then UTC.
func TestClaimTimezone_fallbackChain(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	h := New(database, slog.New(slog.DiscardHandler))
	for _, q := range []string{
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('owner','o@example.com','O','America/Lima',1,1)`,
		`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('adm-utc','a@example.com','A','UTC',1)`,
		`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('adm-mx','m@example.com','M','America/Mexico_City',1)`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	check := func(browser, inviter, want string) {
		t.Helper()
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck
		if got := h.claimTimezone(ctx, tx, browser, inviter); got != want {
			t.Errorf("claimTimezone(%q, inviter %s) = %q; want %q", browser, inviter, got, want)
		}
	}
	check("Asia/Tokyo", "adm-mx", "Asia/Tokyo")            // a real browser zone wins
	check("Mars/Olympus", "adm-mx", "America/Mexico_City") // the inviter's
	check("UTC", "adm-utc", "America/Lima")                // inviter unknown too: the owner's
	check("", "gone", "America/Lima")                      // inviter missing: the owner's
	if _, err := database.Exec(`UPDATE users SET iana_timezone = 'UTC' WHERE id = 'owner'`); err != nil {
		t.Fatal(err)
	}
	check("Etc/Unknown", "adm-utc", "UTC") // nothing known anywhere: UTC, logged
}
