package handler

// Fork (Agenda Maestros 4x4): áreas and the owner's predefined event types - the reconcile
// engine. The owner picks two of their own event types in Miembros:
//
//   - the Mentoría template T (fork_settings team_mentoria_template_id): every active
//     person whose área is 'mentoria' gets a COPY of T - their personal booking link,
//     owned by T's owner, hosted by that mentor alone, kept in step with T;
//   - the Soporte shared type S (team_soporte_shared_id): one link, round robin among the
//     active people whose área is 'soporte'.
//
// ReconcileTeam is the ONLY writer of copies, holders, and the hosts/routing of T and S.
// It is idempotent: every trigger (boot, team settings, áreas, invites, archive, template
// edits) just runs it again. Rules, in the order they run:
//
//  1. Per user U, one transaction: U's copy of the current T is created when U is
//     eligible (active, área mentoria, not T's owner) and T is not archived, the way
//     DuplicateEventType copies (INSERT ... SELECT, the values never pass through Go).
//     Its slug is "{T.slug}-{name}", transliterated, fixed forever (it is the shared link).
//  2. Every existing copy of T gets T's row by a generic row-value UPDATE over
//     PRAGMA table_info(event_types) minus teamSyncExcluded, plus the fixed overrides
//     (owner, fixed routing, empty location_value, not archived, is_active by rule 3).
//     TestTeamSyncColumns_classified pins the column set, so an upstream column
//     addition fails CI until someone decides whether it syncs.
//  3. is_active = U active AND área mentoria AND T not archived AND T still the setting.
//     T's own is_active is not propagated. Losing eligibility deactivates the copy
//     (never deletes it: bookings are RESTRICT); regaining it reactivates the same one.
//  4. Questions are synced IN PLACE through fork_question_links, so copy question ids -
//     and clients' answers - survive. A copy question whose template question is gone is
//     deleted if nobody answered it, else parked on T's hidden holder type (answers keep
//     their label; the public form and {tema} no longer see it).
//  5. event_type_reminders are copied. Not copied: the owner's type-specific
//     availability rules, hosts, webhook filters, WhatsApp texts (inherited at send time).
//  6. Copies of any other template (or of none, when the setting is unset) are
//     deactivated; their links stay, so their bookings keep the old texts and matching.
//  7. Orphans (a copy whose user was deleted): deleted when it has no bookings and no
//     answer on its questions (a booking reassigned off it may keep one), else
//     deactivated. Área rows of missing users and invite roles granted by missing users
//     are purged. Holders are never deleted.
//
// Then, on every run (a single user's run too - their área or archive changes S's
// rotation): S's hosts are its active soporte staff as 'rotation' (stable priority by
// created_at) with round_robin routing, or its owner as the single required host when
// there is nobody; a previously managed S is released back to its owner; T's hosts are
// locked to [T's owner, required] with fixed routing.
//
// Single-connection pool: every helper takes the transaction, drains each cursor before
// the next statement, and never calls anything that uses h.db or opens its own tx.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/calnode/calnode/internal/uid"
)

// fork_settings keys of the team feature.
const (
	forkKeyTeamTemplate    = "team_mentoria_template_id"
	forkKeyTeamSoporte     = "team_soporte_shared_id"
	forkKeyTeamSoporteLast = "team_soporte_last_managed_id"
)

// Áreas (fork_member_areas.area) and link kinds (fork_event_type_links.kind).
const (
	areaMentoria = "mentoria"
	areaSoporte  = "soporte"

	linkKindCopy   = "copy"
	linkKindHolder = "holder"
)

// teamSyncExcluded are the event_types columns the field sync never copies from T: the
// identity of the row, its lifecycle (is_active follows rule 3, archiving is T's own),
// the routing the reconcile manages (fixed, one required host), the unused team_id, and
// location_value - the owner's own room link, phone or address must never reach a
// mentor's clients (T is constrained to the built-in video room, which needs none).
var teamSyncExcluded = map[string]bool{
	"id": true, "slug": true, "user_id": true, "created_at": true,
	"is_active": true, "archived_at": true,
	"routing_mode": true, "rr_strategy": true,
	"team_id": true, "location_value": true,
}

// teamReconcileMu serialises reconciles: several triggers can fire at once (two saves in
// Miembros), and while each transaction is safe on its own, two passes creating the same
// copy would make the second one fail on the unique index.
var teamReconcileMu sync.Mutex

// teamQuerier is what the reconcile helpers need: a *sql.Tx (or the pool, for reads made
// before any transaction is open).
type teamQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// teamStats counts what a reconcile changed. Only counts are logged, never names.
type teamStats struct {
	Created, Reactivated, Deactivated, Deleted int
	QuestionsParked, QuestionsDeleted          int
}

func (s teamStats) changed() bool { return s != teamStats{} }

// teamSettings are the saved fork_settings of the team feature ("" = unset).
type teamSettings struct {
	templateID, soporteID, soporteLastID string
}

// teamType is the part of an event type the reconcile needs.
type teamType struct {
	id, userID, slug, name string
	archived               bool
}

// teamUser is a workspace user and their área ("" = attends nothing).
type teamUser struct {
	id, name, email, area string
	archived              bool
}

// teamHost is one event_type_hosts row, without its id.
type teamHost struct {
	userID, role string
	priority     int
}

func teamNow() string { return time.Now().UTC().Format(time.RFC3339) }

// loadTeamSettings reads the three team keys. A missing row is "".
func loadTeamSettings(ctx context.Context, q teamQuerier) (teamSettings, error) {
	var st teamSettings
	rows, err := q.QueryContext(ctx,
		`SELECT key, value FROM fork_settings WHERE key IN (?, ?, ?)`,
		forkKeyTeamTemplate, forkKeyTeamSoporte, forkKeyTeamSoporteLast)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		switch k {
		case forkKeyTeamTemplate:
			st.templateID = v
		case forkKeyTeamSoporte:
			st.soporteID = v
		case forkKeyTeamSoporteLast:
			st.soporteLastID = v
		}
	}
	return st, rows.Err()
}

// loadTeamType returns the event type id, or nil when id is "" or the row is gone (a
// setting whose type was deleted counts as unset).
func loadTeamType(ctx context.Context, q teamQuerier, id string) (*teamType, error) {
	if id == "" {
		return nil, nil
	}
	var t teamType
	err := q.QueryRowContext(ctx,
		`SELECT id, user_id, slug, name, archived_at IS NOT NULL FROM event_types WHERE id = ?`, id).
		Scan(&t.id, &t.userID, &t.slug, &t.name, &t.archived)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// loadTeamUsers lists the workspace users with their área; userID "" = every user.
func loadTeamUsers(ctx context.Context, q teamQuerier, userID string) ([]teamUser, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT u.id, u.name, u.email, COALESCE(a.area, ''), u.archived_at IS NOT NULL
		FROM users u LEFT JOIN fork_member_areas a ON a.user_id = u.id
		WHERE (? = '' OR u.id = ?)
		ORDER BY u.created_at, u.id`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []teamUser
	for rows.Next() {
		var u teamUser
		if err := rows.Scan(&u.id, &u.name, &u.email, &u.area, &u.archived); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// teamSyncColumns is the event_types column set the field sync copies from T: every
// column PRAGMA table_info reports, minus teamSyncExcluded, in table order.
func teamSyncColumns(ctx context.Context, q teamQuerier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM pragma_table_info('event_types') ORDER BY cid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !teamSyncExcluded[name] {
			cols = append(cols, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, errors.New("team: event_types has no columns to sync")
	}
	return cols, nil
}

// quoteColumns renders column names for SQL. They come from PRAGMA table_info, never
// from a request; the quoting only guards against a keyword-named column.
func quoteColumns(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
	}
	return strings.Join(q, ", ")
}

// ReconcileTeam brings copies, holders and the hosts of T and S in line with the team
// settings and áreas (see the file comment). userID "" = every user; a user id limits the
// per-user pass to that person (their copies), while the orphan pass and the S/T host
// sync always run. Each user is one transaction. Safe to call from any trigger, at any
// time; concurrent calls queue.
func (h *Handler) ReconcileTeam(ctx context.Context, userID string) (teamStats, error) {
	teamReconcileMu.Lock()
	defer teamReconcileMu.Unlock()

	var stats teamStats
	st, err := loadTeamSettings(ctx, h.db)
	if err != nil {
		return stats, fmt.Errorf("team: load settings: %w", err)
	}
	tmpl, err := loadTeamType(ctx, h.db, st.templateID)
	if err != nil {
		return stats, fmt.Errorf("team: load template: %w", err)
	}
	cols, err := teamSyncColumns(ctx, h.db)
	if err != nil {
		return stats, fmt.Errorf("team: columns: %w", err)
	}
	users, err := loadTeamUsers(ctx, h.db, userID)
	if err != nil {
		return stats, fmt.Errorf("team: load users: %w", err)
	}

	for _, u := range users {
		if err := h.teamTx(ctx, func(tx *sql.Tx) error {
			return reconcileTeamUser(ctx, tx, tmpl, cols, u, &stats)
		}); err != nil {
			return stats, fmt.Errorf("team: user pass: %w", err)
		}
	}
	if err := h.teamTx(ctx, func(tx *sql.Tx) error {
		return reconcileTeamOrphans(ctx, tx, &stats)
	}); err != nil {
		return stats, fmt.Errorf("team: orphans: %w", err)
	}
	if err := h.teamTx(ctx, func(tx *sql.Tx) error {
		return reconcileTeamHosts(ctx, tx, st, tmpl)
	}); err != nil {
		return stats, fmt.Errorf("team: hosts: %w", err)
	}
	if stats.changed() {
		h.logger.InfoContext(ctx, "team reconcile",
			"created", stats.Created, "reactivated", stats.Reactivated,
			"deactivated", stats.Deactivated, "deleted", stats.Deleted,
			"questions_parked", stats.QuestionsParked, "questions_deleted", stats.QuestionsDeleted)
	}
	return stats, nil
}

// reconcileTeamAfter runs ReconcileTeam after a successful request, detached from the
// request's cancellation (the change is already committed; the sync must finish). Errors
// are logged: the next trigger or boot repeats the pass.
func (h *Handler) reconcileTeamAfter(ctx context.Context, userID string) teamStats {
	stats, err := h.ReconcileTeam(context.WithoutCancel(ctx), userID)
	if err != nil {
		h.logger.ErrorContext(ctx, "team reconcile failed", "error", err)
	}
	return stats
}

// teamTx runs fn in one transaction.
func (h *Handler) teamTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// reconcileTeamUser is rules 1-6 for one user, inside tx.
func reconcileTeamUser(ctx context.Context, tx *sql.Tx, tmpl *teamType, cols []string, u teamUser, stats *teamStats) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT copy_id, template_id FROM fork_event_type_links WHERE user_id = ? AND kind = ?`, u.id, linkKindCopy)
	if err != nil {
		return err
	}
	type link struct{ copyID, templateID string }
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.copyID, &l.templateID); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error
			return err
		}
		links = append(links, l)
	}
	rows.Close() // #nosec G104 -- drained
	if err := rows.Err(); err != nil {
		return err
	}

	copyID := ""
	for _, l := range links {
		if tmpl != nil && l.templateID == tmpl.id {
			copyID = l.copyID
			continue
		}
		// Rule 6: a copy of another (or no) template. Deactivated, link kept.
		if err := setTeamTypeActive(ctx, tx, l.copyID, false, stats); err != nil {
			return err
		}
	}
	if tmpl == nil {
		return nil
	}
	eligible := !u.archived && u.area == areaMentoria && u.id != tmpl.userID
	active := eligible && !tmpl.archived
	if copyID == "" {
		if !active {
			return nil
		}
		id, err := createTeamCopy(ctx, tx, tmpl, cols, u)
		if err != nil {
			return err
		}
		copyID = id
		stats.Created++
	}
	return syncTeamCopy(ctx, tx, tmpl, cols, copyID, u.id, active, stats)
}

// createTeamCopy inserts U's copy of tmpl and its link, and returns the copy's id. The row
// is copied in SQL (INSERT ... SELECT) like DuplicateEventType; syncTeamCopy then does the
// rest (hosts, reminders, questions) exactly as on every later pass.
func createTeamCopy(ctx context.Context, tx *sql.Tx, tmpl *teamType, cols []string, u teamUser) (string, error) {
	slug, err := uniqueTeamSlug(ctx, tx, teamCopySlugBase(tmpl.slug, u))
	if err != nil {
		return "", err
	}
	id := uid.New()
	colList := quoteColumns(cols)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO event_types (id, slug, user_id, is_active, routing_mode, location_value, `+colList+`)
		SELECT ?, ?, user_id, 1, 'fixed', '', `+colList+`
		FROM event_types WHERE id = ?`, id, slug, tmpl.id); err != nil { // #nosec G202 -- colList comes from PRAGMA table_info, quoted; every value is bound
		return "", fmt.Errorf("create copy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO fork_event_type_links (copy_id, template_id, user_id, kind, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, tmpl.id, u.id, linkKindCopy, teamNow()); err != nil {
		return "", fmt.Errorf("link copy: %w", err)
	}
	return id, nil
}

// syncTeamCopy is rules 2-5 for one existing copy of tmpl hosted by userID.
func syncTeamCopy(ctx context.Context, tx *sql.Tx, tmpl *teamType, cols []string, copyID, userID string, active bool, stats *teamStats) error {
	var wasActive bool
	if err := tx.QueryRowContext(ctx, `SELECT is_active FROM event_types WHERE id = ?`, copyID).Scan(&wasActive); err != nil {
		return fmt.Errorf("load copy: %w", err)
	}
	colList := quoteColumns(cols)
	isActive := 0
	if active {
		isActive = 1
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE event_types SET (`+colList+`) = (SELECT `+colList+` FROM event_types WHERE id = ?),
		       user_id = ?, routing_mode = 'fixed', location_value = '', archived_at = NULL, is_active = ?
		WHERE id = ?`, tmpl.id, tmpl.userID, isActive, copyID); err != nil { // #nosec G202 -- colList comes from PRAGMA table_info, quoted; every value is bound
		return fmt.Errorf("sync copy row: %w", err)
	}
	switch {
	case active && !wasActive:
		stats.Reactivated++
	case !active && wasActive:
		stats.Deactivated++
	}
	if err := setTeamTypeHosts(ctx, tx, copyID, []teamHost{{userID: userID, role: "required"}}); err != nil {
		return err
	}
	if err := setTeamTypeRouting(ctx, tx, copyID, "fixed"); err != nil {
		return err
	}
	if err := syncTeamReminders(ctx, tx, tmpl.id, copyID); err != nil {
		return err
	}
	return syncTeamQuestions(ctx, tx, tmpl, cols, copyID, stats)
}

// setTeamTypeActive switches an event type on or off, counting a real transition.
func setTeamTypeActive(ctx context.Context, tx *sql.Tx, id string, active bool, stats *teamStats) error {
	want := 0
	if active {
		want = 1
	}
	res, err := tx.ExecContext(ctx, `UPDATE event_types SET is_active = ? WHERE id = ? AND is_active <> ?`, want, id, want)
	if err != nil {
		return fmt.Errorf("set active: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if active {
			stats.Reactivated++
		} else {
			stats.Deactivated++
		}
	}
	return nil
}

// setTeamTypeHosts makes the event type's host list exactly want, writing only when it
// differs (host rows carry ids; rewriting them on every pass would be churn).
func setTeamTypeHosts(ctx context.Context, tx *sql.Tx, etID string, want []teamHost) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT user_id, role, priority FROM event_type_hosts WHERE event_type_id = ? ORDER BY priority, user_id`, etID)
	if err != nil {
		return err
	}
	have := map[string]teamHost{}
	for rows.Next() {
		var hh teamHost
		if err := rows.Scan(&hh.userID, &hh.role, &hh.priority); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error
			return err
		}
		have[hh.userID] = hh
	}
	rows.Close() // #nosec G104 -- drained
	if err := rows.Err(); err != nil {
		return err
	}
	same := len(have) == len(want)
	for _, w := range want {
		if !same {
			break
		}
		if got, ok := have[w.userID]; !ok || got != w {
			same = false
		}
	}
	if same {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM event_type_hosts WHERE event_type_id = ?`, etID); err != nil {
		return fmt.Errorf("clear hosts: %w", err)
	}
	for _, w := range want {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
			VALUES (?, ?, ?, ?, ?)`, uid.New(), etID, w.userID, w.role, w.priority); err != nil {
			return fmt.Errorf("insert host: %w", err)
		}
	}
	return nil
}

// setTeamTypeRouting sets routing_mode when it differs.
func setTeamTypeRouting(ctx context.Context, tx *sql.Tx, etID, mode string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE event_types SET routing_mode = ? WHERE id = ? AND routing_mode <> ?`, mode, etID, mode); err != nil {
		return fmt.Errorf("set routing: %w", err)
	}
	return nil
}

// syncTeamReminders makes the copy's reminder schedule the template's, writing only on a
// difference. Direct SQL in tx: replaceReminders opens its own transaction.
func syncTeamReminders(ctx context.Context, tx *sql.Tx, fromID, toID string) error {
	load := func(id string) ([]int, error) {
		rows, err := tx.QueryContext(ctx,
			`SELECT hours_before FROM event_type_reminders WHERE event_type_id = ? ORDER BY hours_before`, id)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []int
		for rows.Next() {
			var hb int
			if err := rows.Scan(&hb); err != nil {
				return nil, err
			}
			out = append(out, hb)
		}
		return out, rows.Err()
	}
	want, err := load(fromID)
	if err != nil {
		return err
	}
	have, err := load(toID)
	if err != nil {
		return err
	}
	if slices.Equal(want, have) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM event_type_reminders WHERE event_type_id = ?`, toID); err != nil {
		return fmt.Errorf("clear reminders: %w", err)
	}
	for _, hb := range want {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO event_type_reminders (id, event_type_id, hours_before) VALUES (?, ?, ?)`,
			uid.New(), toID, hb); err != nil {
			return fmt.Errorf("copy reminders: %w", err)
		}
	}
	return nil
}

// teamQuestion is one event_type_questions row.
type teamQuestion struct {
	id, label, qType string
	options          sql.NullString
	required         int
	position         int
}

func loadTeamQuestions(ctx context.Context, tx *sql.Tx, etID string) ([]teamQuestion, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, label, type, options, required, position
		FROM event_type_questions WHERE event_type_id = ? ORDER BY position, id`, etID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []teamQuestion
	for rows.Next() {
		var q teamQuestion
		if err := rows.Scan(&q.id, &q.label, &q.qType, &q.options, &q.required, &q.position); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// syncTeamQuestions is rule 4: the copy's questions follow the template's in place.
func syncTeamQuestions(ctx context.Context, tx *sql.Tx, tmpl *teamType, cols []string, copyID string, stats *teamStats) error {
	tqs, err := loadTeamQuestions(ctx, tx, tmpl.id)
	if err != nil {
		return err
	}
	cqs, err := loadTeamQuestions(ctx, tx, copyID)
	if err != nil {
		return err
	}
	byID := make(map[string]teamQuestion, len(cqs))
	for _, q := range cqs {
		byID[q.id] = q
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT copy_question_id, template_question_id FROM fork_question_links WHERE copy_id = ?`, copyID)
	if err != nil {
		return err
	}
	linked := map[string]string{} // template question id -> copy question id
	var stale []string            // links whose copy question left the copy
	for rows.Next() {
		var cq, tq string
		if err := rows.Scan(&cq, &tq); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error
			return err
		}
		if _, ok := byID[cq]; ok {
			linked[tq] = cq
		} else {
			stale = append(stale, cq)
		}
	}
	rows.Close() // #nosec G104 -- drained
	if err := rows.Err(); err != nil {
		return err
	}
	for _, cq := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM fork_question_links WHERE copy_question_id = ?`, cq); err != nil {
			return err
		}
	}

	kept := map[string]bool{}
	for _, tq := range tqs {
		if cqID, ok := linked[tq.id]; ok {
			kept[cqID] = true
			cur := byID[cqID]
			if cur.label == tq.label && cur.qType == tq.qType && cur.options == tq.options &&
				cur.required == tq.required && cur.position == tq.position {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE event_type_questions SET label = ?, type = ?, options = ?, required = ?, position = ?
				WHERE id = ?`, tq.label, tq.qType, tq.options, tq.required, tq.position, cqID); err != nil {
				return fmt.Errorf("update copy question: %w", err)
			}
			continue
		}
		newID := uid.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_type_questions (id, event_type_id, label, type, options, required, position)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, newID, copyID, tq.label, tq.qType, tq.options, tq.required, tq.position); err != nil {
			return fmt.Errorf("insert copy question: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fork_question_links (copy_question_id, template_question_id, copy_id)
			VALUES (?, ?, ?)`, newID, tq.id, copyID); err != nil {
			return fmt.Errorf("link copy question: %w", err)
		}
	}

	// What is left is retired (its template question is gone) or was never linked.
	for _, cq := range cqs {
		if kept[cq.id] {
			continue
		}
		var answers int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM booking_answers WHERE question_id = ?`, cq.id).Scan(&answers); err != nil {
			return err
		}
		if answers == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM event_type_questions WHERE id = ?`, cq.id); err != nil {
				return fmt.Errorf("delete retired question: %w", err)
			}
			stats.QuestionsDeleted++
			continue
		}
		// Answered: deleting would CASCADE the clients' answers away. Park it on the
		// template's holder type, where nothing public reads it.
		holderID, err := ensureTeamHolder(ctx, tx, tmpl, cols)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE event_type_questions SET event_type_id = ? WHERE id = ?`, holderID, cq.id); err != nil {
			return fmt.Errorf("park question: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM fork_question_links WHERE copy_question_id = ?`, cq.id); err != nil {
			return err
		}
		stats.QuestionsParked++
	}
	return nil
}

// ensureTeamHolder returns the template's holder type, creating it on first need: owned
// by T's owner, inactive, private, named "Preguntas retiradas — {T.name}", linked with
// kind 'holder'. Holders are omitted from every list, refused every mutation, never
// deleted, and never synced after creation.
func ensureTeamHolder(ctx context.Context, tx *sql.Tx, tmpl *teamType, cols []string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT l.copy_id FROM fork_event_type_links l JOIN event_types et ON et.id = l.copy_id
		WHERE l.template_id = ? AND l.kind = ? ORDER BY l.created_at LIMIT 1`, tmpl.id, linkKindHolder).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	slug, err := uniqueTeamSlug(ctx, tx, tmpl.slug+"-preguntas-retiradas")
	if err != nil {
		return "", err
	}
	id = uid.New()
	colList := quoteColumns(cols)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO event_types (id, slug, user_id, is_active, routing_mode, location_value, `+colList+`)
		SELECT ?, ?, user_id, 0, 'fixed', '', `+colList+`
		FROM event_types WHERE id = ?`, id, slug, tmpl.id); err != nil { // #nosec G202 -- colList comes from PRAGMA table_info, quoted; every value is bound
		return "", fmt.Errorf("create holder: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE event_types SET name = ?, is_public = 0 WHERE id = ?`, "Preguntas retiradas — "+tmpl.name, id); err != nil {
		return "", fmt.Errorf("name holder: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO fork_event_type_links (copy_id, template_id, user_id, kind, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, tmpl.id, tmpl.userID, linkKindHolder, teamNow()); err != nil {
		return "", fmt.Errorf("link holder: %w", err)
	}
	return id, nil
}

// reconcileTeamOrphans is rule 7.
func reconcileTeamOrphans(ctx context.Context, tx *sql.Tx, stats *teamStats) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT l.copy_id, (SELECT COUNT(*) FROM bookings b WHERE b.event_type_id = l.copy_id)
		     + (SELECT COUNT(*) FROM booking_answers a JOIN event_type_questions q ON q.id = a.question_id
		        WHERE q.event_type_id = l.copy_id)
		FROM fork_event_type_links l
		WHERE l.kind = ? AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = l.user_id)`, linkKindCopy)
	if err != nil {
		return err
	}
	type orphan struct {
		id      string
		history int // its bookings plus the answers on its questions
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.history); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error
			return err
		}
		orphans = append(orphans, o)
	}
	rows.Close() // #nosec G104 -- drained
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range orphans {
		if o.history > 0 {
			if err := setTeamTypeActive(ctx, tx, o.id, false, stats); err != nil {
				return err
			}
			continue
		}
		// No bookings and no answer on its questions (a booking reassigned off the copy can
		// keep one): only the link and the unanswered questions CASCADE.
		if _, err := tx.ExecContext(ctx, `DELETE FROM event_types WHERE id = ?`, o.id); err != nil {
			return fmt.Errorf("delete orphan copy: %w", err)
		}
		stats.Deleted++
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM fork_member_areas WHERE user_id NOT IN (SELECT id FROM users)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM fork_invite_roles WHERE created_by NOT IN (SELECT id FROM users)`); err != nil {
		return err
	}
	return nil
}

// reconcileTeamHosts syncs S's rotation, releases a previously managed S, and locks T's
// hosts (see the file comment).
func reconcileTeamHosts(ctx context.Context, tx *sql.Tx, st teamSettings, tmpl *teamType) error {
	sup, err := loadTeamType(ctx, tx, st.soporteID)
	if err != nil {
		return err
	}
	current := ""
	if sup != nil {
		current = sup.id
		rows, err := tx.QueryContext(ctx, `
			SELECT u.id FROM users u JOIN fork_member_areas a ON a.user_id = u.id
			WHERE a.area = ? AND u.archived_at IS NULL
			ORDER BY u.created_at, u.id`, areaSoporte)
		if err != nil {
			return err
		}
		var staff []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close() // #nosec G104 -- already returning the scan error
				return err
			}
			staff = append(staff, id)
		}
		rows.Close() // #nosec G104 -- drained
		if err := rows.Err(); err != nil {
			return err
		}
		hosts := []teamHost{{userID: sup.userID, role: "required"}}
		mode := "fixed"
		if len(staff) > 0 {
			hosts = hosts[:0]
			for i, id := range staff {
				hosts = append(hosts, teamHost{userID: id, role: "rotation", priority: i})
			}
			mode = "round_robin"
		}
		if err := setTeamTypeHosts(ctx, tx, sup.id, hosts); err != nil {
			return err
		}
		if err := setTeamTypeRouting(ctx, tx, sup.id, mode); err != nil {
			return err
		}
	}
	if st.soporteLastID != current {
		if st.soporteLastID != "" {
			prev, err := loadTeamType(ctx, tx, st.soporteLastID)
			if err != nil {
				return err
			}
			if prev != nil {
				if err := setTeamTypeHosts(ctx, tx, prev.id, []teamHost{{userID: prev.userID, role: "required"}}); err != nil {
					return err
				}
				if err := setTeamTypeRouting(ctx, tx, prev.id, "fixed"); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fork_settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, forkKeyTeamSoporteLast, current); err != nil {
			return err
		}
	}
	if tmpl != nil {
		if err := setTeamTypeHosts(ctx, tx, tmpl.id, []teamHost{{userID: tmpl.userID, role: "required"}}); err != nil {
			return err
		}
		if err := setTeamTypeRouting(ctx, tx, tmpl.id, "fixed"); err != nil {
			return err
		}
	}
	return nil
}

// maxTeamSlugName caps the name part of a copy's slug; the cut falls on a hyphen.
const maxTeamSlugName = 40

// teamTransliterations covers the letters NFD does not decompose into a base letter plus
// marks (ø, ł, æ...). Everything else with an accent (á, é, ñ, ü, ç) is handled by NFD.
var teamTransliterations = map[rune]string{
	'ø': "o", 'Ø': "o", 'ł': "l", 'Ł': "l", 'đ': "d", 'Đ': "d",
	'æ': "ae", 'Æ': "ae", 'œ': "oe", 'Œ': "oe", 'ß': "ss",
}

// transliterate strips accents so slugify keeps the letter instead of dropping it
// ("María José" → "Maria Jose", "Núñez" → "Nunez").
func transliterate(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if t, ok := teamTransliterations[r]; ok {
			b.WriteString(t)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// capSlugPart cuts a slug to max characters on a hyphen boundary when it can.
func capSlugPart(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '-'); i > 0 {
		cut = cut[:i]
	}
	return strings.Trim(cut, "-")
}

// teamCopySlugBase is "{template slug}-{name}": the mentor's name, transliterated and
// slugified, capped; else the local part of their email; else a short id.
func teamCopySlugBase(templateSlug string, u teamUser) string {
	part := capSlugPart(slugify(transliterate(u.name)), maxTeamSlugName)
	if part == "" {
		local := u.email
		if i := strings.IndexByte(local, '@'); i >= 0 {
			local = local[:i]
		}
		part = capSlugPart(slugify(transliterate(strings.NewReplacer(".", " ", "+", " ").Replace(local))), maxTeamSlugName)
	}
	if part == "" {
		part = slugify(u.id)
		if len(part) > 8 {
			part = strings.Trim(part[:8], "-")
		}
	}
	return templateSlug + "-" + part
}

// uniqueTeamSlug returns base, else base-2, base-3... - checked inside the caller's
// transaction (event_types.slug is unique across the whole instance).
func uniqueTeamSlug(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	for i := 1; i <= maxCopySlugAttempts; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM event_types WHERE slug = ?`, candidate).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errNoFreeCopySlug
}

// RetireSupportTier is the boot repair of the retired is_support "desk" tier: anyone still
// flagged gets the área soporte (unless they already have an área) and loses the flag. The
// flag grants nothing any more; this keeps the data honest. Idempotent. Returns how many
// users it changed.
func (h *Handler) RetireSupportTier(ctx context.Context) (int, error) {
	var n int64
	err := h.teamTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fork_member_areas (user_id, area, updated_at)
			SELECT id, ?, ? FROM users WHERE is_support = 1
			ON CONFLICT (user_id) DO NOTHING`, areaSoporte, teamNow()); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE users SET is_support = 0 WHERE is_support = 1`)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// StartTeamBoot runs the boot pass of the team feature in the background: the is_support
// repair, then a full ReconcileTeam. HTTP server only (server.New) - never from
// BuildHandler, which the `calnode mcp` process also runs.
func (h *Handler) StartTeamBoot(ctx context.Context) {
	go func() {
		bctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if n, err := h.RetireSupportTier(bctx); err != nil {
			h.logger.ErrorContext(bctx, "support tier repair failed", "error", err)
		} else if n > 0 {
			h.logger.InfoContext(bctx, "support tier retired: users moved to the área soporte", "users", n)
		}
		if _, err := h.ReconcileTeam(bctx, ""); err != nil {
			h.logger.ErrorContext(bctx, "team boot reconcile failed", "error", err)
		}
	}()
}
