// Fork (Agenda Maestros 4x4): pure helpers first written for the Panel's 7-day
// "Disponibilidad del equipo" (GET /v1/team/availability). That panel was replaced by the
// month calendar (TeamCalendar.svelte, team-calendar.ts), which reuses the day, zone and
// 12-hour helpers here; the RFC3339 slot helpers still describe the 7-day endpoint.
//
// The server writes every slot as RFC3339 in the zone the viewer picked
// ("2026-10-02T09:00:00-05:00"), so the wall-clock day and time are read straight from the
// string: no Intl round-trip that could shift a day, and the 12-hour text is the one every
// client-facing time in this fork uses ("9:00 a. m.", "12:30 p. m.").

import type { TeamAvailabilityPerson } from './api';

/** Zones of the countries the clients and mentors come from, offered first. */
export const PRIORITY_ZONES: { country: string; zone: string }[] = [
	{ country: 'Perú', zone: 'America/Lima' },
	{ country: 'México', zone: 'America/Mexico_City' },
	{ country: 'Colombia', zone: 'America/Bogota' },
	{ country: 'Argentina', zone: 'America/Argentina/Buenos_Aires' },
	{ country: 'Chile', zone: 'America/Santiago' },
	{ country: 'Ecuador', zone: 'America/Guayaquil' },
	{ country: 'Bolivia', zone: 'America/La_Paz' },
	{ country: 'Venezuela', zone: 'America/Caracas' },
	{ country: 'España', zone: 'Europe/Madrid' },
	{ country: 'EE. UU. (Este)', zone: 'America/New_York' }
];

/** How far ahead the day strip may go, in days from today. */
export const MAX_DAYS_AHEAD = 60;

/** "YYYY-MM-DD" of a slot's start, in the zone it was written in. */
export function slotDay(iso: string): string {
	return iso.slice(0, 10);
}

/** Wall-clock hour (0-23) of a slot's start. */
export function slotHour(iso: string): number {
	return Number(iso.slice(11, 13));
}

/** Wall-clock minutes since midnight of an instant as written. */
function slotMinutes(iso: string): number {
	return Number(iso.slice(11, 13)) * 60 + Number(iso.slice(14, 16));
}

/** "9:00 a. m." / "12:30 p. m." for hour h and minute m. */
export function time12(h: number, m: number): string {
	const hh = h % 12 || 12;
	return `${hh}:${String(m).padStart(2, '0')} ${h < 12 ? 'a. m.' : 'p. m.'}`;
}

/** The 12-hour text of a slot instant, as written in its zone. */
export function fmtSlotTime(iso: string): string {
	return time12(Number(iso.slice(11, 13)), Number(iso.slice(14, 16)));
}

/** The label of an hour row: "9:00 a. m.". */
export function hourLabel(h: number): string {
	return time12(h, 0);
}

/** An hour row's label in two parts, so a narrow phone column can stack them:
 *  { time: "9:00", period: "a. m." }. */
export function hourParts(h: number): { time: string; period: string } {
	const [time, ...rest] = hourLabel(h).split(' ');
	return { time, period: rest.join(' ') };
}

/** How many different people are free on day. The owner holds both templates (one entry in
 *  Mentoría and one in Soporte), so entries are counted by person, not one by one. */
export function countFreePeople(people: TeamAvailabilityPerson[], day: string): number {
	return new Set(people.filter((p) => !p.error && p.slots.some((s) => slotDay(s.start) === day)).map((p) => p.user_id)).size;
}

/** How long the panel reuses an answer it already fetched: the server's own cache TTL, so
 *  moving between weeks never shows times older than a refresh would. */
export const CLIENT_CACHE_TTL_MS = 60_000;

/** A small per-visit cache of answers whose entries expire after ttlMs. */
export class ExpiringCache<T> {
	#m = new Map<string, { at: number; value: T }>();
	#ttlMs: number;

	constructor(ttlMs: number = CLIENT_CACHE_TTL_MS) {
		this.#ttlMs = ttlMs;
	}

	get(key: string, now: number = Date.now()): T | undefined {
		const e = this.#m.get(key);
		if (!e) return undefined;
		if (now - e.at >= this.#ttlMs) {
			this.#m.delete(key);
			return undefined;
		}
		return e.value;
	}

	set(key: string, value: T, now: number = Date.now()): void {
		this.#m.set(key, { at: now, value });
	}

	clear(): void {
		this.#m.clear();
	}
}

/** Today ("YYYY-MM-DD") in zone tz. */
export function todayIn(tz: string, now: Date = new Date()): string {
	try {
		return new Intl.DateTimeFormat('en-CA', { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit' }).format(now);
	} catch {
		return now.toISOString().slice(0, 10);
	}
}

/** ymd shifted by n days. */
export function addDays(ymd: string, n: number): string {
	const [y, m, d] = ymd.split('-').map(Number);
	return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10);
}

/** Whole days from a to b (both "YYYY-MM-DD"). */
export function daysBetween(a: string, b: string): number {
	const [ay, am, ad] = a.split('-').map(Number);
	const [by, bm, bd] = b.split('-').map(Number);
	return Math.round((Date.UTC(by, bm - 1, bd) - Date.UTC(ay, am - 1, ad)) / 86_400_000);
}

const DOW_SHORT = ['dom', 'lun', 'mar', 'mié', 'jue', 'vie', 'sáb'];
const DOW_LONG = ['domingo', 'lunes', 'martes', 'miércoles', 'jueves', 'viernes', 'sábado'];
const MONTHS = ['enero', 'febrero', 'marzo', 'abril', 'mayo', 'junio', 'julio', 'agosto', 'septiembre', 'octubre', 'noviembre', 'diciembre'];

function weekday(ymd: string): number {
	const [y, m, d] = ymd.split('-').map(Number);
	return new Date(Date.UTC(y, m - 1, d)).getUTCDay();
}

/** Short weekday of a day: "lun". */
export function dowShort(ymd: string): string {
	return DOW_SHORT[weekday(ymd)];
}

/** Day of the month of a day: 2. */
export function dayNumber(ymd: string): number {
	return Number(ymd.slice(8, 10));
}

/** "jueves 2 de octubre". */
export function longDay(ymd: string): string {
	return `${DOW_LONG[weekday(ymd)]} ${dayNumber(ymd)} de ${MONTHS[Number(ymd.slice(5, 7)) - 1]}`;
}

/** The person's starts on day, in order. */
export function startsOn(p: TeamAvailabilityPerson, day: string): string[] {
	return p.slots.filter((s) => slotDay(s.start) === day).map((s) => s.start);
}

/** The person's free time on day as merged ranges: consecutive slots that touch or overlap
 *  become one "9:00 a. m. – 12:00 p. m." range (a range ends when its last session ends). */
export function mergedRanges(p: TeamAvailabilityPerson, day: string): string[] {
	const out: { from: string; to: string; toMin: number }[] = [];
	for (const s of p.slots) {
		if (slotDay(s.start) !== day) continue;
		const startMin = slotMinutes(s.start);
		// An end on the next day (a late session) counts as past midnight.
		const endMin = slotMinutes(s.end) + (slotDay(s.end) !== day ? 24 * 60 : 0);
		const last = out[out.length - 1];
		if (last && startMin <= last.toMin) {
			if (endMin > last.toMin) {
				last.to = s.end;
				last.toMin = endMin;
			}
		} else {
			out.push({ from: s.start, to: s.end, toMin: endMin });
		}
	}
	return out.map((r) => `${fmtSlotTime(r.from)} – ${fmtSlotTime(r.to)}`);
}

/** First name for chips and toasts: "Daniel" from "Daniel Pérez". */
export function firstName(name: string): string {
	return name.trim().split(/\s+/)[0] || name;
}

/** Up to two initials for an avatar fallback. */
export function initials(name: string): string {
	return (
		name
			.trim()
			.split(/\s+/)
			.map((p) => p[0] ?? '')
			.join('')
			.toUpperCase()
			.slice(0, 2) || '?'
	);
}
