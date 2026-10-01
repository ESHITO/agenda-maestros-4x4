// Fork (owner, 30 Sep 2026): on Reservas, the person who attends a session sees how long is
// left before it starts - "Empieza en 1 día 4 h", "Empieza en 12 min", "En curso".
//
// Pure: the page feeds it the booking's start/end and a clock it ticks itself, so the
// boundaries, plurals and rounding are tested here and nowhere else. Every unit is ROUNDED
// DOWN (59 s left reads "menos de 1 min", 1 h 59 min reads "1 h 59 min", never "2 h"): a
// countdown that rounds up tells a mentor he has more time than he does.
//
// The admin SPA is hardcoded Spanish (see CLAUDE.md), so the words live here.

export type CountdownTone = 'live' | 'soon' | 'later';

export type Countdown = {
	/** What the pill reads, e.g. "Empieza en 5 h 20 min". */
	text: string;
	/** live = between start and end; soon = under an hour to go; later = the rest. */
	tone: CountdownTone;
};

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** From this many whole days left, only the days are shown ("Empieza en 3 días"). */
export const DAYS_ONLY_FROM = 3;

function days(n: number): string {
	return n === 1 ? '1 día' : `${n} días`;
}

/**
 * The countdown for a session from `start` to `end`, seen at `now`; null once it has
 * ended (or when a date cannot be read). The caller decides who sees it.
 */
export function countdown(
	start: string | Date,
	end: string | Date,
	now: number | Date = Date.now()
): Countdown | null {
	const s = new Date(start).getTime();
	const e = new Date(end).getTime();
	const n = typeof now === 'number' ? now : now.getTime();
	if (!Number.isFinite(s) || !Number.isFinite(n)) return null;

	const left = s - n;
	if (left <= 0) {
		// Started. Without a readable end, a started session is treated as over.
		if (!Number.isFinite(e) || n >= e) return null;
		return { text: 'En curso', tone: 'live' };
	}

	const totalMin = Math.floor(left / MINUTE);
	if (totalMin < 1) return { text: 'Empieza en menos de 1 min', tone: 'soon' };
	if (left < HOUR) return { text: `Empieza en ${totalMin} min`, tone: 'soon' };

	if (left < DAY) {
		const h = Math.floor(left / HOUR);
		const m = Math.floor((left % HOUR) / MINUTE);
		return { text: m > 0 ? `Empieza en ${h} h ${m} min` : `Empieza en ${h} h`, tone: 'later' };
	}

	const d = Math.floor(left / DAY);
	if (d >= DAYS_ONLY_FROM) return { text: `Empieza en ${days(d)}`, tone: 'later' };
	const h = Math.floor((left % DAY) / HOUR);
	return { text: h > 0 ? `Empieza en ${days(d)} ${h} h` : `Empieza en ${days(d)}`, tone: 'later' };
}
