// Disponibilidad: a weekly block whose start is not before its end used to be refused in
// the browser with a tiny message and NOT saved - to the person it looked like the change
// did nothing. Instead the other end moves to keep a valid block, and that is saved.
//
// Times are "HH:MM" on the editor's half-hour grid (00:00 … 23:30). The server still
// validates (end after start); this only picks a sensible value to send.

export const LAST_SLOT = '23:30';
const DAY_END = 23 * 60 + 30;

export function toMinutes(t: string): number {
	const [h, m] = t.split(':').map(Number);
	return (h || 0) * 60 + (m || 0);
}

export function fromMinutes(n: number): string {
	const c = Math.max(0, Math.min(DAY_END, n));
	return `${String(Math.floor(c / 60)).padStart(2, '0')}:${String(c % 60).padStart(2, '0')}`;
}

export type TimeRange = { start_time: string; end_time: string };

/**
 * The block after the person set one end of it. When `changed` is 'start' and it is not
 * before the end, the end becomes start + 1 h (at most 23:30; a start of 23:30 becomes
 * 23:00 - 23:30). When `changed` is 'end' and it is not after the start, the start becomes
 * end - 1 h (at least 00:00; an end of 00:00 becomes 00:00 - 00:30).
 */
export function fixRange(range: TimeRange, changed: 'start' | 'end'): TimeRange {
	const s = toMinutes(range.start_time);
	const e = toMinutes(range.end_time);
	if (s < e) return { ...range };
	if (changed === 'start') {
		if (s >= DAY_END) return { start_time: '23:00', end_time: LAST_SLOT };
		return { start_time: range.start_time, end_time: fromMinutes(Math.min(s + 60, DAY_END)) };
	}
	if (e <= 0) return { start_time: '00:00', end_time: '00:30' };
	return { start_time: fromMinutes(Math.max(e - 60, 0)), end_time: range.end_time };
}
