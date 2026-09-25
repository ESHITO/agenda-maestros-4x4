package handler

import (
	_ "embed"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// The public privacy policy of this fork (Agenda Maestros 4x4). Google requires one on an
// authorized domain before an external OAuth app (Calendar + Sign in with Google) can leave
// "Testing", where every mentor's connection would expire after 7 days. Serving it from the
// instance keeps it on the same domain as the OAuth redirect URIs, and the business name,
// logo and contact address come from the live settings, so a rebrand needs no code change.

//go:embed templates/privacidad.html
var privacyHTML string

var privacyTmpl = template.Must(template.New("privacidad").Parse(privacyHTML))

// privacyUpdated is the "last updated" date shown on the page. Change it with the text.
const privacyUpdated = "24 de septiembre de 2026"

type privacyPageData struct {
	BusinessName string
	LogoURL      string
	LogoHeight   int
	ContactEmail string
	SiteURL      string
	SiteHost     string
	Updated      string
}

// PrivacyPage handles GET /privacidad (public).
func (h *Handler) PrivacyPage(w http.ResponseWriter, r *http.Request) {
	brand := h.loadBranding(r.Context())
	name := strings.TrimSpace(brand.BusinessName)
	if name == "" {
		name = "Club Maestros 4x4"
	}

	// The owner's address is the contact: the one person guaranteed to exist and to answer
	// for the workspace. A missing row only drops the mailto lines, never the page.
	var contact string
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT email FROM users WHERE is_owner = 1 AND archived_at IS NULL LIMIT 1`).Scan(&contact)

	site := h.publicBaseURL
	if site == "" {
		site = h.baseURL
	}
	host := site
	if u, err := url.Parse(site); err == nil && u.Host != "" {
		host = u.Host
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	if err := privacyTmpl.Execute(w, privacyPageData{
		BusinessName: name,
		LogoURL:      brand.LogoURL,
		LogoHeight:   pageLogoHeight(brand.LogoHeight),
		ContactEmail: contact,
		SiteURL:      site,
		SiteHost:     host,
		Updated:      privacyUpdated,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "privacy page: render", "error", err)
	}
}
