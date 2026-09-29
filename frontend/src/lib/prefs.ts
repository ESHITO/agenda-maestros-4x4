import { writable, get } from 'svelte/store';
import type { User } from './api';

export interface UserPrefs {
	timezone: string;
	time_format: '12h' | '24h';
	week_start: number;
	date_format: 'dmy' | 'mdy' | 'ymd';
}

const defaults: UserPrefs = {
	timezone: 'UTC',
	time_format: '12h',
	week_start: 1,
	date_format: 'dmy'
};

export const prefs = writable<UserPrefs>(defaults);

export function prefsFromUser(u: User): UserPrefs {
	return {
		timezone: u.timezone,
		time_format: u.time_format ?? '12h',
		week_start: u.week_start ?? 1,
		date_format: u.date_format ?? 'dmy'
	};
}

// The zone every booking time in the panel is shown in: the signed-in user's profile zone
// (Configuración > Perfil), not the device's. A mentor whose profile says America/Lima on a
// laptop set to Madrid must still read a Lima session as 8:30 p. m., and the admin reschedule
// picker asks /slots for days in this same zone. Falls back to the device's zone only when
// the profile has none or this browser cannot load it.
export function displayZone(p: UserPrefs = get(prefs)): string {
	const tz = p.timezone;
	if (tz) {
		try {
			new Intl.DateTimeFormat('en-US', { timeZone: tz });
			return tz;
		} catch {
			// Unknown to this browser: use the device's zone below.
		}
	}
	return Intl.DateTimeFormat().resolvedOptions().timeZone;
}

// dayKeyInZone — "YYYY-MM-DD" of an instant in the display zone.
export function dayKeyInZone(iso: string | Date, p: UserPrefs = get(prefs)): string {
	return new Intl.DateTimeFormat('en-CA', {
		timeZone: displayZone(p),
		year: 'numeric',
		month: '2-digit',
		day: '2-digit'
	}).format(typeof iso === 'string' ? new Date(iso) : iso);
}

export function fmtDateTime(iso: string, p: UserPrefs = get(prefs)): string {
	return `${fmtDate(dayKeyInZone(iso, p), p)}, ${fmtTime(iso, p)}`;
}

export function fmtDate(ymd: string, p: UserPrefs = get(prefs)): string {
	// ymd is always YYYY-MM-DD from the API — parse directly to avoid TZ shift from new Date()
	const [y, m, d] = ymd.split('-');
	if (p.date_format === 'mdy') return `${m}/${d}/${y}`;
	if (p.date_format === 'ymd') return `${y}-${m}-${d}`;
	return `${d}/${m}/${y}`;
}

export function fmtTime(iso: string, p: UserPrefs = get(prefs)): string {
	return new Date(iso).toLocaleTimeString(undefined, {
		hour: '2-digit',
		minute: '2-digit',
		hour12: p.time_format === '12h',
		timeZone: displayZone(p)
	});
}

// fmtShortWhen — a short "when" in the display zone: "hoy 07:00", "mañana 07:00",
// "ayer 07:00", or "30/09 07:00" (day/month order follows the date preference). "Today" is
// today in that zone too, so the label and the time never disagree about the day.
export function fmtShortWhen(iso: string | undefined, p: UserPrefs = get(prefs), now: Date = new Date()): string {
	if (!iso) return '';
	const d = new Date(iso);
	if (isNaN(d.getTime())) return '';
	const time = fmtTime(iso, p);
	const key = dayKeyInZone(d, p);
	const todayKey = dayKeyInZone(now, p);
	const shift = (k: string, n: number) => {
		const [y, m, dd] = k.split('-').map(Number);
		return new Date(Date.UTC(y, m - 1, dd + n)).toISOString().slice(0, 10);
	};
	if (key === todayKey) return `hoy ${time}`;
	if (key === shift(todayKey, 1)) return `mañana ${time}`;
	if (key === shift(todayKey, -1)) return `ayer ${time}`;
	const [, mm, dd] = key.split('-');
	return `${p.date_format === 'mdy' || p.date_format === 'ymd' ? `${mm}/${dd}` : `${dd}/${mm}`} ${time}`;
}

export const WEEK_DAYS = ['Domingo', 'Lunes', 'Martes', 'Miércoles', 'Jueves', 'Viernes', 'Sábado'];

// Shown only when the browser can't list its zones (Intl.supportedValuesOf is missing).
const FALLBACK_TIMEZONES = [
	'Pacific/Auckland',
	'Australia/Sydney',
	'Australia/Melbourne',
	'Asia/Tokyo',
	'Asia/Singapore',
	'Asia/Dubai',
	'Europe/London',
	'Europe/Paris',
	'Europe/Berlin',
	'Europe/Amsterdam',
	'America/New_York',
	'America/Chicago',
	'America/Denver',
	'America/Los_Angeles',
	'UTC'
];

// Every zone the browser knows. Members book from many countries (Lima, Ciudad de
// México, Madrid, Buenos Aires…); a short hand-picked list left most of them unable
// to pick their own zone, and hid the one they already had.
export const TIMEZONES: string[] = (() => {
	try {
		const all = Intl.supportedValuesOf('timeZone');
		return all.includes('UTC') ? all : [...all, 'UTC'];
	} catch {
		return FALLBACK_TIMEZONES;
	}
})();

const labelCache = new Map<string, string>();

// "America/Mexico_City" → "America/Mexico City · GMT-6", so a search for "mexico city"
// or "lima" finds it and the offset tells two nearby zones apart.
export function timezoneLabel(tz: string): string {
	const cached = labelCache.get(tz);
	if (cached) return cached;
	const name = tz.replaceAll('_', ' ');
	let label = name;
	try {
		const offset = new Intl.DateTimeFormat('en-US', { timeZone: tz, timeZoneName: 'shortOffset' })
			.formatToParts(new Date())
			.find((p) => p.type === 'timeZoneName')?.value;
		if (offset) label = `${name} · ${offset}`;
	} catch {
		// Unknown to this browser: the plain name is still searchable.
	}
	labelCache.set(tz, label);
	return label;
}

// Picker items, keeping the saved zone even when this browser doesn't list it
// (an alias such as Asia/Calcutta), so the field never shows empty.
export function timezoneItems(current = ''): { value: string; label: string }[] {
	const zones = current && !TIMEZONES.includes(current) ? [current, ...TIMEZONES] : TIMEZONES;
	return zones.map((tz) => ({ value: tz, label: timezoneLabel(tz) }));
}
