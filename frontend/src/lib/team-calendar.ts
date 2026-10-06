// Fork (Agenda Maestros 4x4): pure helpers for the Panel's "Calendario del equipo"
// (TeamCalendar.svelte, GET /v1/team/calendar). Owner, 6 Oct 2026: Mes / 15 días / Semana,
// Mes by default - who works when, who is booked, what is still free, and which hours of
// the "horario a cubrir" NOBODY works yet ("Sin cubrir"), to fill them with new people.
//
// The server writes every time as whole minutes since the VIEWER's midnight, already split
// at each of their midnights and keyed by their "YYYY-MM-DD": the panel never runs a date
// through Intl (no day shifts), and prints the 12-hour clock itself ("9:00 a. m.").

import type { Area, TeamCalBusy, TeamCalendar, TeamCalPerson, TeamCalSpan, TeamCoverageTarget, TeamCoverageTargetDay } from './api';
import { addDays, dayNumber, time12 } from './team-availability';

export type CalView = 'month' | 'fortnight' | 'week';
export type TeamArea = Exclude<Area, ''>;
export type AreaFilter = 'all' | TeamArea;

export const VIEW_LABELS: Record<CalView, string> = { month: 'Mes', fortnight: '15 días', week: 'Semana' };
/** The order the switch shows them in; Mes is the default (owner: "por defecto por mes"). */
export const VIEWS: CalView[] = ['month', 'fortnight', 'week'];
export const DEFAULT_VIEW: CalView = 'month';

/** How far ahead the server answers (to <= today + 366). */
export const MAX_DAYS_AHEAD = 366;

const MONTHS = ['enero', 'febrero', 'marzo', 'abril', 'mayo', 'junio', 'julio', 'agosto', 'septiembre', 'octubre', 'noviembre', 'diciembre'];
const DOW_SHORT_MON = ['lun', 'mar', 'mié', 'jue', 'vie', 'sáb', 'dom'];
/** Weekday headers, Monday first (Hispanic custom). The letter form uses X for miércoles. */
export const WEEK_HEADERS: { short: string; letter: string; long: string }[] = [
	{ short: 'lun', letter: 'L', long: 'lunes' },
	{ short: 'mar', letter: 'M', long: 'martes' },
	{ short: 'mié', letter: 'X', long: 'miércoles' },
	{ short: 'jue', letter: 'J', long: 'jueves' },
	{ short: 'vie', letter: 'V', long: 'viernes' },
	{ short: 'sáb', letter: 'S', long: 'sábado' },
	{ short: 'dom', letter: 'D', long: 'domingo' }
];

function ymdParts(ymd: string): [number, number, number] {
	const [y, m, d] = ymd.split('-').map(Number);
	return [y, m, d];
}

function ymd(y: number, m: number, d: number): string {
	return new Date(Date.UTC(y, m - 1, d)).toISOString().slice(0, 10);
}

/** 0 = Monday … 6 = Sunday. */
export function mondayIndex(day: string): number {
	const [y, m, d] = ymdParts(day);
	return (new Date(Date.UTC(y, m - 1, d)).getUTCDay() + 6) % 7;
}

/** Go's weekday (0 = Sunday … 6 = Saturday) of a day: the target's `dow`. */
export function goWeekday(day: string): number {
	const [y, m, d] = ymdParts(day);
	return new Date(Date.UTC(y, m - 1, d)).getUTCDay();
}

/** Last day ("YYYY-MM-DD") of the month of day. */
export function lastOfMonth(day: string): string {
	const [y, m] = ymdParts(day);
	return ymd(y, m + 1, 0);
}

/** The period of view that contains anchor: Mes = 1st to last day; 15 días = the
 *  quincena (1-15 or 16-end: never splits a month, so previous/next is unambiguous);
 *  Semana = Monday to Sunday. */
export function periodOf(view: CalView, anchor: string): { from: string; to: string } {
	const [y, m, d] = ymdParts(anchor);
	switch (view) {
		case 'month':
			return { from: ymd(y, m, 1), to: lastOfMonth(anchor) };
		case 'fortnight':
			return d <= 15 ? { from: ymd(y, m, 1), to: ymd(y, m, 15) } : { from: ymd(y, m, 16), to: lastOfMonth(anchor) };
		case 'week': {
			const from = addDays(anchor, -mondayIndex(anchor));
			return { from, to: addDays(from, 6) };
		}
	}
}

/** The first day of the period delta periods away from the one starting at from. */
export function shiftPeriod(view: CalView, from: string, delta: number): string {
	const [y, m, d] = ymdParts(from);
	switch (view) {
		case 'month':
			return ymd(y, m + delta, 1);
		case 'fortnight': {
			// Quincenas counted from year 0: two per month.
			const idx = (y * 12 + (m - 1)) * 2 + (d >= 16 ? 1 : 0) + delta;
			const monthIdx = Math.floor(idx / 2);
			return ymd(Math.floor(monthIdx / 12), (monthIdx % 12) + 1, idx % 2 === 0 ? 1 : 16);
		}
		case 'week':
			return addDays(periodOf('week', from).from, 7 * delta);
	}
}

function monthName(day: string): string {
	return MONTHS[ymdParts(day)[1] - 1];
}

/** The navigation title: "octubre de 2026", "16 – 31 de octubre de 2026",
 *  "28 de septiembre – 4 de octubre de 2026". */
export function periodLabel(view: CalView, from: string, to: string): string {
	const [fy] = ymdParts(from);
	const [ty] = ymdParts(to);
	if (view === 'month') return `${monthName(from)} de ${fy}`;
	if (from.slice(0, 7) === to.slice(0, 7)) return `${dayNumber(from)} – ${dayNumber(to)} de ${monthName(from)} de ${fy}`;
	if (fy === ty) return `${dayNumber(from)} de ${monthName(from)} – ${dayNumber(to)} de ${monthName(to)} de ${fy}`;
	return `${dayNumber(from)} de ${monthName(from)} de ${fy} – ${dayNumber(to)} de ${monthName(to)} de ${ty}`;
}

/** The cells of a Monday-first grid that holds [from, to]: whole weeks, the days outside
 *  the period flagged (they are drawn grey and carry no data). A day after `until` (the
 *  furthest day the server answers, today + 366) is out of range too: it cannot be asked,
 *  so it is never a tappable empty day. */
export function gridDays(from: string, to: string, until?: string): { day: string; inRange: boolean }[] {
	const start = addDays(from, -mondayIndex(from));
	const end = addDays(to, 6 - mondayIndex(to));
	const out: { day: string; inRange: boolean }[] = [];
	for (let d = start; d <= end; d = addDays(d, 1)) out.push({ day: d, inRange: d >= from && d <= to && (!until || d <= until) });
	return out;
}

/** Every day of [from, to]. */
export function daysOf(from: string, to: string): string[] {
	const out: string[] = [];
	for (let d = from; d <= to; d = addDays(d, 1)) out.push(d);
	return out;
}

/** "lun 5". */
export function shortDay(day: string): string {
	return `${DOW_SHORT_MON[mondayIndex(day)]} ${dayNumber(day)}`;
}

// ── Times ────────────────────────────────────────────────────────────────────────────

/** "9:00 a. m." for minutes since midnight; 1440 (the end of the day) is "12:00 a. m.". */
export function minLabel(m: number): string {
	return time12(Math.floor(m / 60) % 24, m % 60);
}

/** A short axis tick: "9 a. m.", "12 p. m.". */
export function tickLabel(m: number): string {
	const h = Math.floor(m / 60) % 24;
	return `${h % 12 || 12} ${h < 12 ? 'a. m.' : 'p. m.'}`;
}

/** "9:00 a. m. – 1:00 p. m.". */
export function spanLabel(s: TeamCalSpan): string {
	return `${minLabel(s[0])} – ${minLabel(s[1])}`;
}

export function totalMinutes(spans: TeamCalSpan[] | undefined): number {
	return (spans ?? []).reduce((n, s) => n + Math.max(0, s[1] - s[0]), 0);
}

/** "4 h", "30 min", "1 h 30 min". */
export function durationText(min: number): string {
	const h = Math.floor(min / 60);
	const m = min % 60;
	if (h && m) return `${h} h ${m} min`;
	if (h) return `${h} h`;
	return `${m} min`;
}

/** Sorts and merges spans that touch or overlap. */
export function mergeSpans(spans: TeamCalSpan[]): TeamCalSpan[] {
	const sorted = spans.map((s) => [s[0], s[1]] as TeamCalSpan).sort((a, b) => a[0] - b[0] || a[1] - b[1]);
	const out: TeamCalSpan[] = [];
	for (const s of sorted) {
		const last = out[out.length - 1];
		if (last && s[0] <= last[1]) last[1] = Math.max(last[1], s[1]);
		else out.push(s);
	}
	return out;
}

/** The wall-clock day and minute of an RFC3339 instant written on the viewer's clock
 *  (notice_until, bookable_until): read from the text, never through Intl. */
export function wallClock(iso: string): { day: string; min: number } {
	return { day: iso.slice(0, 10), min: Number(iso.slice(11, 13)) * 60 + Number(iso.slice(14, 16)) };
}

// ── People and days ──────────────────────────────────────────────────────────────────

export function rowsFor(people: TeamCalPerson[], area: AreaFilter, onlyUsers?: Set<string>): TeamCalPerson[] {
	return people.filter((p) => (area === 'all' || p.area === area) && (!onlyUsers || onlyUsers.size === 0 || onlyUsers.has(p.user_id)));
}

/** One entry per person (the owner has a Mentoría and a Soporte row), in answer order. */
export function distinctPeople(people: TeamCalPerson[]): TeamCalPerson[] {
	const seen = new Set<string>();
	return people.filter((p) => (seen.has(p.user_id) ? false : (seen.add(p.user_id), true)));
}

/** The person's free time on day: each start plus the session's length, merged. */
export function freeSpans(p: TeamCalPerson, day: string): TeamCalSpan[] {
	const dur = Math.max(1, p.duration_min || 0);
	return mergeSpans((p.days[day]?.free ?? []).map((f) => [f, Math.min(1440, f + dur)] as TeamCalSpan));
}

/** The person's sessions on day (one row's copy; every row of a person carries them all). */
export function busyOn(p: TeamCalPerson, day: string): TeamCalBusy[] {
	return p.days[day]?.busy ?? [];
}

/** How many different sessions the rows hold on day: a session shows on every row of
 *  its person (and of each of its hosts) under one key. */
export function countSessions(people: TeamCalPerson[], day: string): number {
	const keys = new Set<string>();
	for (const p of people) for (const b of busyOn(p, day)) keys.add(b.key);
	return keys.size;
}

/** Different people (not rows) with a free start on day. */
export function countFreePeople(people: TeamCalPerson[], day: string): number {
	return new Set(people.filter((p) => !p.error && (p.days[day]?.free?.length ?? 0) > 0).map((p) => p.user_id)).size;
}

/** Different people with working hours on day. */
export function countWorkingPeople(people: TeamCalPerson[], day: string): number {
	return new Set(people.filter((p) => (p.days[day]?.hours?.length ?? 0) > 0).map((p) => p.user_id)).size;
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** A day's counts in words, never abbreviated (the users are older people):
 *  - 'long' (the day detail): "3 personas libres · 4 trabajan · 0 sesiones";
 *  - 'wide' (a week row from sm): "3 personas libres · 4 trabajan · 1 sesión" (sessions only
 *    when there are some);
 *  - 'phone' (a week row at 375 px): "3 personas libres · 1 sesión" - "trabajan" only when the
 *    free count is unknown (then it is the only figure).
 *  "trabaja(n)" takes the noun when it opens the text: "4 personas trabajan". */
export function dayCountsText(
	c: { free: number; working: number; sessions: number },
	freeIncluded: boolean,
	form: 'long' | 'wide' | 'phone'
): string {
	const parts: string[] = [];
	if (freeIncluded) parts.push(plural(c.free, 'persona libre', 'personas libres'));
	if (!freeIncluded || form !== 'phone') {
		parts.push(parts.length ? plural(c.working, 'trabaja', 'trabajan') : plural(c.working, 'persona trabaja', 'personas trabajan'));
	}
	if (c.sessions || form === 'long') parts.push(plural(c.sessions, 'sesión', 'sesiones'));
	return parts.join(' · ');
}

/** After the owner saves a new "horario a cubrir": the quick answer (free=0, fresh) brings
 *  the new coverage in a moment, the complete one up to ~22 s later. The quick answer's
 *  target, coverage and each row's hours and sessions are laid over the answer shown, KEEPING
 *  its free starts (and its errors) until the complete answer replaces everything. Rows only
 *  in the quick answer wait for the complete one (their free time would read as "sin cupos");
 *  a quick answer of another range or zone changes nothing. When what is shown has no free
 *  starts either, the quick answer simply replaces it. */
export function mergeQuickAnswer(shown: TeamCalendar, quick: TeamCalendar): TeamCalendar {
	if (quick.from !== shown.from || quick.to !== shown.to || quick.tz !== shown.tz) return shown;
	if (!shown.free_included) return quick;
	const byKey = new Map(quick.people.map((p) => [p.key, p]));
	const people = shown.people.map((p) => {
		const q = byKey.get(p.key);
		if (!q || q.error) return p;
		const days: TeamCalPerson['days'] = {};
		for (const [day, fresh] of Object.entries(q.days)) {
			const free = p.days[day]?.free;
			// Free starts only where the person still works (never free time on a day off).
			days[day] = free && (fresh.hours?.length ?? 0) > 0 ? { ...fresh, free } : { ...fresh };
		}
		return { ...p, days };
	});
	return { ...shown, target: quick.target, coverage: quick.coverage, people };
}

export type PersonDayKind ='past' | 'off' | 'free' | 'full' | 'checking' | 'unknown' | 'error';
export type PersonDayState = { kind: PersonDayKind; note: string };

/** What a person's day is, and the one sentence the panel says about it. Only two causes
 *  of "works but nothing free" are known for certain (the minimum notice, the furthest
 *  bookable day); for anything else it says "Sin cupos libres" and never guesses.
 *  checking = the complete answer (free starts) is still on its way; without free starts
 *  and with nothing on its way, that answer failed: "Revisando…" would be a lie forever. */
export function personDayState(p: TeamCalPerson, day: string, today: string, freeIncluded: boolean, checking = true): PersonDayState {
	const d = p.days[day];
	if (day < today) {
		const n = d?.busy?.length ?? 0;
		return { kind: 'past', note: n ? '' : 'Sin sesiones este día.' };
	}
	if (p.error && p.error_kind === 'internal') {
		return { kind: 'error', note: 'Una regla de su horario no se puede leer; revisa su disponibilidad.' };
	}
	const hours = d?.hours ?? [];
	if (hours.length === 0) return { kind: 'off', note: 'No trabaja este día.' };
	if (p.error) {
		return {
			kind: 'unknown',
			note:
				p.error_kind === 'timeout'
					? 'No hubo tiempo de revisar su calendario; pulsa Actualizar.'
					: 'No se pudo revisar su calendario ahora; no se sabe qué horas tiene libres.'
		};
	}
	if (!freeIncluded) {
		return checking
			? { kind: 'checking', note: 'Revisando su calendario…' }
			: { kind: 'unknown', note: 'No se pudo revisar su calendario; pulsa Actualizar.' };
	}
	if ((d?.free?.length ?? 0) > 0) return { kind: 'free', note: '' };
	if (p.bookable_until) {
		const bu = wallClock(p.bookable_until);
		if (day > bu.day || (day === bu.day && hours.every((h) => h[0] >= bu.min))) {
			return { kind: 'full', note: 'Sin cupos: es más allá de los días que su enlace deja reservar.' };
		}
	}
	if (p.notice_until) {
		const nu = wallClock(p.notice_until);
		if (day < nu.day || (day === nu.day && hours.every((h) => h[1] <= nu.min))) {
			return { kind: 'full', note: 'Sin cupos: ya no se puede reservar con tan poca anticipación.' };
		}
	}
	return { kind: 'full', note: 'Sin cupos libres.' };
}

/** Hour rows of a day: the hours (0-23) where someone can start, with who. */
export function freeHourRows(people: TeamCalPerson[], day: string): { hour: number; people: TeamCalPerson[] }[] {
	const rows = new Map<number, TeamCalPerson[]>();
	for (const p of people) {
		if (p.error) continue;
		const hours = new Set((p.days[day]?.free ?? []).map((m) => Math.floor(m / 60)));
		for (const h of hours) {
			if (!rows.has(h)) rows.set(h, []);
			rows.get(h)!.push(p);
		}
	}
	return [...rows.entries()].sort((a, b) => a[0] - b[0]).map(([hour, list]) => ({ hour, people: list }));
}

/** The free starts of the person on day as "9:00 a. m." texts. */
export function freeStartLabels(p: TeamCalPerson, day: string): string[] {
	return (p.days[day]?.free ?? []).map(minLabel);
}

// ── Coverage ─────────────────────────────────────────────────────────────────────────

export type AreaCoverage = {
	area: TeamArea;
	target: TeamCalSpan[];
	uncovered: TeamCalSpan[];
	ownerOnly: TeamCalSpan[];
	uncoveredMin: number;
	ownerOnlyMin: number;
};
export type CoverageState = 'past' | 'none' | 'covered' | 'owner_only' | 'gaps';
/** uncoveredMin / ownerOnlyMin are CLOCK minutes (the áreas' spans merged), never a sum of
 *  áreas: under "Todos" one hour nobody covers in either área is 1 h, not 2. */
export type DayCoverage = { state: CoverageState; areas: AreaCoverage[]; uncoveredMin: number; ownerOnlyMin: number };

const AREA_NAME: Record<TeamArea, string> = { mentoria: 'Mentoría', soporte: 'Soporte' };
/** One letter per área, as on the avatars (M / S). */
export const AREA_LETTER: Record<TeamArea, string> = { mentoria: 'M', soporte: 'S' };

/** The áreas whose coverage the filter shows (only those the answer measured). */
export function coverageAreas(data: Pick<TeamCalendar, 'coverage'> | null, area: AreaFilter): TeamArea[] {
	const all: TeamArea[] = area === 'all' ? ['mentoria', 'soporte'] : [area];
	return all.filter((a) => !!data?.coverage?.[a]);
}

/** A day's coverage over the shown áreas. "gaps" when some hour of the target has nobody;
 *  "owner_only" when every hour has someone but some only the owner (whose hours count for
 *  both áreas and would hide the gaps); "none" when there is no target that day. */
export function dayCoverage(data: Pick<TeamCalendar, 'coverage' | 'today'> | null, day: string, area: AreaFilter): DayCoverage {
	const out: DayCoverage = { state: 'none', areas: [], uncoveredMin: 0, ownerOnlyMin: 0 };
	if (!data) return out;
	if (day < data.today) return { ...out, state: 'past' };
	for (const a of coverageAreas(data, area)) {
		const c = data.coverage[a]?.days?.[day];
		if (!c || c.target.length === 0) continue;
		const uncovered = c.uncovered ?? [];
		const ownerOnly = c.owner_only ?? [];
		out.areas.push({
			area: a,
			target: c.target,
			uncovered,
			ownerOnly,
			uncoveredMin: totalMinutes(mergeSpans(uncovered)),
			ownerOnlyMin: totalMinutes(mergeSpans(ownerOnly))
		});
	}
	if (out.areas.length === 0) return out;
	out.uncoveredMin = totalMinutes(mergeSpans(out.areas.flatMap((a) => a.uncovered)));
	out.ownerOnlyMin = totalMinutes(mergeSpans(out.areas.flatMap((a) => a.ownerOnly)));
	out.state = out.uncoveredMin > 0 ? 'gaps' : out.ownerOnlyMin > 0 ? 'owner_only' : 'covered';
	return out;
}

/** The hours nobody covers, per área when two are shown (each área has its own people, so
 *  their gaps are never added up): "Sin cubrir 4 h" with one área; "Sin cubrir: M 3 h · S 12 h"
 *  (short) or "sin cubrir: Mentoría 3 h, Soporte 12 h" (long, for screen readers) with two.
 *  "" when nothing is uncovered. */
export function uncoveredText(cov: DayCoverage, short: boolean): string {
	const gaps = cov.areas.filter((a) => a.uncoveredMin > 0);
	if (gaps.length === 0) return '';
	if (cov.areas.length === 1) return `${short ? 'Sin cubrir' : 'sin cubrir'} ${durationText(gaps[0].uncoveredMin)}`;
	return short
		? `Sin cubrir: ${gaps.map((a) => `${AREA_LETTER[a.area]} ${durationText(a.uncoveredMin)}`).join(' · ')}`
		: `sin cubrir: ${gaps.map((a) => `${AREA_NAME[a.area]} ${durationText(a.uncoveredMin)}`).join(', ')}`;
}

/** The hours only the owner covers, the same way: "Solo propietario 2 h" / per área. */
export function ownerOnlyText(cov: DayCoverage, short: boolean): string {
	const parts = cov.areas.filter((a) => a.ownerOnlyMin > 0);
	if (parts.length === 0) return '';
	const head = short ? 'Solo propietario' : 'solo el propietario';
	if (cov.areas.length === 1) return `${head} ${durationText(parts[0].ownerOnlyMin)}`;
	return short
		? `${head}: ${parts.map((a) => `${AREA_LETTER[a.area]} ${durationText(a.ownerOnlyMin)}`).join(' · ')}`
		: `${head}: ${parts.map((a) => `${AREA_NAME[a.area]} ${durationText(a.ownerOnlyMin)}`).join(', ')}`;
}

// ── One person's mark in a month / 15-day cell ─────────────────────────────────────────

/** fill = what is known about their free time: free (solid), full (works, nothing free:
 *  faint), busy (sessions and nothing free - fully booked: stripes), unknown (their calendar
 *  is not checked yet, or could not be: outline only, NEVER drawn as "sin cupos").
 *  booked = they have sessions that day (a ring around the dot). */
export type PersonDotKind = 'free' | 'full' | 'busy' | 'unknown';
export type PersonDot = { kind: PersonDotKind; booked: boolean };

/** Whether a row's free starts are unknown: the quick answer has none at all, and a row with
 *  error (calendar, timeout, internal) has none to trust. */
export function freeUnknown(p: TeamCalPerson, freeIncluded: boolean): boolean {
	return !freeIncluded || !!p.error;
}

/** A person's mark on day from all their rows (the owner has two), or null when there is
 *  nothing to show (does not work and has no session; past days: no session). */
export function personDot(mine: TeamCalPerson[], day: string, today: string, freeIncluded: boolean): PersonDot | null {
	const booked = mine.some((r) => busyOn(r, day).length > 0);
	if (day < today) return booked ? { kind: 'busy', booked } : null;
	const working = mine.filter((r) => (r.days[day]?.hours?.length ?? 0) > 0);
	if (working.length === 0) return booked ? { kind: 'busy', booked } : null;
	if (working.some((r) => !freeUnknown(r, freeIncluded) && (r.days[day]?.free?.length ?? 0) > 0)) return { kind: 'free', booked };
	// Nothing free known: certain only when every working row was checked.
	if (working.some((r) => freeUnknown(r, freeIncluded))) return { kind: 'unknown', booked };
	return { kind: booked ? 'busy' : 'full', booked };
}

/** The minutes a day's timeline shows: whole hours around everything there is to draw
 *  (hours, sessions, target), at least 4 h; 8:00 a. m. - 8:00 p. m. when there is nothing. */
export function timelineWindow(people: TeamCalPerson[], cov: AreaCoverage[], day: string): { start: number; end: number } {
	let lo = Infinity;
	let hi = -Infinity;
	const take = (s: number, e: number) => {
		lo = Math.min(lo, s);
		hi = Math.max(hi, e);
	};
	for (const p of people) {
		const d = p.days[day];
		for (const h of d?.hours ?? []) take(h[0], h[1]);
		for (const b of d?.busy ?? []) take(b.start, b.end);
	}
	for (const c of cov) for (const t of c.target) take(t[0], t[1]);
	if (!isFinite(lo)) return { start: 480, end: 1200 };
	let start = Math.floor(lo / 60) * 60;
	let end = Math.min(1440, Math.ceil(hi / 60) * 60);
	if (end - start < 240) {
		end = Math.min(1440, start + 240);
		start = Math.max(0, end - 240);
	}
	return { start, end };
}

/** The axis ticks of a window: every 2 h on a short day, 3 h on a long one (4 h on a phone),
 *  4 h on a full one - "12 p. m." needs room at 375 px. */
export function timelineTicks(w: { start: number; end: number }, narrow = false): number[] {
	const len = w.end - w.start;
	const hours = len > 16 * 60 ? (narrow ? 6 : 4) : len > 8 * 60 ? (narrow ? 4 : 3) : 2;
	const s = hours * 60;
	const out: number[] = [];
	for (let m = Math.ceil(w.start / s) * s; m <= w.end; m += s) out.push(m);
	return out;
}

/** Percent position of minute m inside window w. */
export function pct(m: number, w: { start: number; end: number }): number {
	return Math.max(0, Math.min(100, ((m - w.start) / (w.end - w.start)) * 100));
}

// ── Colour encodings (never colour alone: free = solid, busy = stripes, no free = faint,
//    not checked = outline only, sin cubrir = red diagonal hatch + label, solo el
//    propietario = amber DOTS, cubierto = plain green) ──────────────────────────────

export const UNCOVERED_COLOR = '#dc2626';
export const OWNER_ONLY_COLOR = '#d97706';

/** The colour mixed with transparency (pct %), for "works, nothing free". */
export function tint(color: string, pctOpaque: number): string {
	return `color-mix(in srgb, ${color} ${pctOpaque}%, transparent)`;
}

/** Diagonal stripes of a colour: sessions (person colour) and "Sin cubrir" (red). */
export function stripes(color: string, width = 3): string {
	return `repeating-linear-gradient(135deg, ${color} 0 ${width}px, transparent ${width}px ${width * 2}px)`;
}

/** A dot grid of a colour: "Solo el propietario" (a different SHAPE from the red hatch, so
 *  the two never differ by hue alone - red vs amber is a colour-blind pair). Use with
 *  background-size `DOTS_SIZE`. */
export function dotsPattern(color: string): string {
	return `radial-gradient(circle, ${color} 1.1px, transparent 1.4px)`;
}
export const DOTS_SIZE = '4px 4px';

/** Inline styles of the two coverage marks, shared by the cells, the lanes and the legend. */
export const UNCOVERED_STYLE = `background-color:${tint(UNCOVERED_COLOR, 22)};background-image:${stripes(UNCOVERED_COLOR, 2)}`;
export const OWNER_ONLY_STYLE = `background-color:${tint(OWNER_ONLY_COLOR, 14)};background-image:${dotsPattern(OWNER_ONLY_COLOR)};background-size:${DOTS_SIZE}`;

/** The inline style of a person's mark (cell dot, legend swatch). */
export function personDotStyle(d: PersonDot, color: string): string {
	const fill =
		d.kind === 'free'
			? `background:${color}`
			: d.kind === 'full'
				? `background:${tint(color, 30)}`
				: d.kind === 'unknown'
					? `background:transparent;box-shadow:inset 0 0 0 1px ${color}`
					: `background-color:${tint(color, 20)};background-image:${stripes(color, 1)};box-shadow:inset 0 0 0 1px ${color}`;
	// The ring says "has sessions" on a dot whose fill does not already say it.
	return d.booked && d.kind !== 'busy' ? `${fill};outline:1px solid ${color};outline-offset:1px` : fill;
}

// ── The target ("Horario a cubrir") ──────────────────────────────────────────────────

const DOW_SHORT_GO = ['dom', 'lun', 'mar', 'mié', 'jue', 'vie', 'sáb'];

export function hhmmToMin(t: string): number {
	const [h, m] = t.split(':').map(Number);
	return (h || 0) * 60 + (m || 0);
}

export function minToHhmm(n: number): string {
	return `${String(Math.floor(n / 60)).padStart(2, '0')}:${String(n % 60).padStart(2, '0')}`;
}

/** Start options of the target editor: 00:00 … 23:30. */
export const TARGET_STARTS: string[] = Array.from({ length: 48 }, (_, i) => minToHhmm(i * 30));
/** End options: 00:30 … 24:00 (until midnight). */
export const TARGET_ENDS: string[] = Array.from({ length: 48 }, (_, i) => minToHhmm((i + 1) * 30));

/** An option's text: "8:00 a. m."; "24:00" is "12:00 a. m. (medianoche)". */
export function targetTimeLabel(t: string): string {
	return t === '24:00' ? '12:00 a. m. (medianoche)' : minLabel(hhmmToMin(t));
}

/** The city of an IANA zone for "hora de Lima". */
export function zoneCity(tz: string): string {
	return (tz.split('/').pop() ?? tz).replaceAll('_', ' ');
}

/** "lun–sáb 8:00 a. m. – 8:00 p. m. · hora de Lima": consecutive days (Monday first) with
 *  the same span are grouped. */
export function targetSummary(t: Pick<TeamCoverageTarget, 'tz' | 'days'>): string {
	if (t.days.length === 0) return 'Sin horario a cubrir';
	const byDow = new Map(t.days.map((d) => [d.dow, d]));
	const order = [1, 2, 3, 4, 5, 6, 0];
	const groups: { first: number; last: number; start: string; end: string }[] = [];
	let prev: TeamCoverageTargetDay | undefined;
	for (const dow of order) {
		const d = byDow.get(dow);
		const g = groups[groups.length - 1];
		if (d && prev && g && g.start === d.start && g.end === d.end) g.last = dow;
		else if (d) groups.push({ first: dow, last: dow, start: d.start, end: d.end });
		prev = d;
	}
	const text = groups
		.map((g) => {
			const days = g.first === g.last ? DOW_SHORT_GO[g.first] : `${DOW_SHORT_GO[g.first]}–${DOW_SHORT_GO[g.last]}`;
			return `${days} ${minLabel(hhmmToMin(g.start))} – ${minLabel(hhmmToMin(g.end))}`;
		})
		.join(' · ');
	return `${text} · hora de ${zoneCity(t.tz)}`;
}

/** One editor row per weekday, Monday first. */
export type TargetDraftDay = { dow: number; on: boolean; start: string; end: string };

export function targetDraft(t: Pick<TeamCoverageTarget, 'days'>): TargetDraftDay[] {
	const byDow = new Map(t.days.map((d) => [d.dow, d]));
	return [1, 2, 3, 4, 5, 6, 0].map((dow) => {
		const d = byDow.get(dow);
		return d ? { dow, on: true, start: d.start, end: d.end } : { dow, on: false, start: '08:00', end: '20:00' };
	});
}

/** The PUT body's days from the editor rows, or the Spanish problem to show. */
export function draftToDays(rows: TargetDraftDay[]): { days: TeamCoverageTargetDay[]; error: string } {
	const days: TeamCoverageTargetDay[] = [];
	for (const r of rows) {
		if (!r.on) continue;
		if (hhmmToMin(r.start) >= hhmmToMin(r.end)) {
			return { days: [], error: `El ${WEEK_HEADERS[(r.dow + 6) % 7].long}: la hora de inicio debe ser anterior a la de fin.` };
		}
		days.push({ dow: r.dow, start: r.start, end: r.end });
	}
	days.sort((a, b) => a.dow - b.dow);
	return { days, error: '' };
}
