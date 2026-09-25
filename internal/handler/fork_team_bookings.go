package handler

// Fork (Agenda Maestros 4x4): the bookings list and the team's áreas. GET /v1/bookings
// gains two filters and one item field, all derived from the booking's TYPE (never from
// the host's current área, which would carry a mentor's past sessions into Soporte when
// they change área):
//   - ?area=mentoria: bookings of the template T or of any mentor's copy; ?area=soporte:
//     bookings of the Soporte type S;
//   - ?event_type=<slug of a template>: the template AND every copy of it (the copies'
//     bookings are its sessions too), where upstream resolves exactly one type;
//   - items gain "area" ("mentoria" | "soporte" | "") in bookingListItem only - never in
//     bookingJSON, which the public GET /v1/bookings/{id} and MCP also serve.
//
// Visibility is untouched: these narrow whatever parseBookingListFilter allowed.

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/calnode/calnode/internal/booking"
)

// forkBookingListFilter is parseBookingListFilter's hook, run after upstream resolved
// ?event_type= to f.EventTypeID. Returns errNoMatches when the filters can match nothing,
// or an error (a 400) for a bad ?area=.
func (h *Handler) forkBookingListFilter(ctx context.Context, q url.Values, f *booking.ListFilter) error {
	var set []string
	constrained := false
	if f.EventTypeID != "" {
		family, err := h.templateFamily(ctx, f.EventTypeID)
		if err != nil {
			return err
		}
		if len(family) > 1 {
			set, constrained = family, true
			f.EventTypeID = ""
		}
	}
	if area := q.Get("area"); area != "" {
		if area != areaMentoria && area != areaSoporte {
			return fmt.Errorf("area must be mentoria or soporte")
		}
		ids, err := h.teamFamily(ctx, area)
		if err != nil {
			return err
		}
		switch {
		case constrained:
			set = slices.DeleteFunc(set, func(id string) bool { return !slices.Contains(ids, id) })
		case f.EventTypeID != "":
			if !slices.Contains(ids, f.EventTypeID) {
				return errNoMatches
			}
			return nil
		default:
			set, constrained = ids, true
		}
	}
	if !constrained {
		return nil
	}
	if len(set) == 0 {
		return errNoMatches
	}
	f.EventTypeIDs = set
	return nil
}

// templateFamily is etID plus every mentor's copy of it (just etID for any other type).
func (h *Handler) templateFamily(ctx context.Context, etID string) ([]string, error) {
	out := []string{etID}
	rows, err := h.db.QueryContext(ctx,
		`SELECT copy_id FROM fork_event_type_links WHERE template_id = ? AND kind = ?`, etID, linkKindCopy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// fillBookingAreas sets each list item's área from its booking's type. One query for the
// page; best effort (withWhatsAppNotices logs an error and serves the list without it).
func (h *Handler) fillBookingAreas(ctx context.Context, idsJSON string, idx map[string]int, out []bookingListItem) error {
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		return err
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT b.id,
		       CASE WHEN b.event_type_id = ?
		              OR EXISTS (SELECT 1 FROM fork_event_type_links l
		                         WHERE l.copy_id = b.event_type_id AND l.kind = ?) THEN ?
		            WHEN b.event_type_id = ? THEN ?
		            ELSE '' END
		FROM bookings b WHERE b.id IN (SELECT value FROM json_each(?))`,
		st.templateID, linkKindCopy, areaMentoria, st.soporteID, areaSoporte, idsJSON)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, area string
		if err := rows.Scan(&id, &area); err != nil {
			return err
		}
		if i, ok := idx[id]; ok {
			out[i].Area = area
		}
	}
	return rows.Err()
}
