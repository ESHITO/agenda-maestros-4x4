// Fork: the Panel's team calendar reads day and time straight from the server's RFC3339
// (already in the chosen zone) and writes them on the 12-hour clock like every client time.
import { describe, expect, it } from 'vitest';
import type { TeamAvailabilityPerson } from './api';
import {
	ExpiringCache,
	addDays,
	countFreePeople,
	daysBetween,
	fmtSlotTime,
	hourLabel,
	hourParts,
	longDay,
	mergedRanges,
	slotDay,
	slotHour,
	startsOn
} from './team-availability';

const person = (slots: [string, string][]): TeamAvailabilityPerson => ({
	user_id: 'u',
	name: 'Daniel Pérez',
	area: 'mentoria',
	avatar_url: '',
	color: null,
	is_you: false,
	link: { slug: 'x', url: 'https://example.com/book/x' },
	slots: slots.map(([start, end]) => ({ start, end }))
});

describe('team availability helpers', () => {
	it('formats wall-clock times on the 12-hour clock', () => {
		expect(fmtSlotTime('2026-10-02T09:00:00-05:00')).toBe('9:00 a. m.');
		expect(fmtSlotTime('2026-10-02T00:30:00-05:00')).toBe('12:30 a. m.');
		expect(fmtSlotTime('2026-10-02T12:30:00+02:00')).toBe('12:30 p. m.');
		expect(fmtSlotTime('2026-10-02T16:00:00Z')).toBe('4:00 p. m.');
		expect(hourLabel(13)).toBe('1:00 p. m.');
	});

	it('reads the day and hour as written, never shifting them', () => {
		expect(slotDay('2026-10-02T23:30:00-05:00')).toBe('2026-10-02');
		expect(slotHour('2026-10-02T23:30:00-05:00')).toBe(23);
		expect(addDays('2026-12-30', 3)).toBe('2027-01-02');
		expect(daysBetween('2026-09-30', '2026-11-29')).toBe(60);
		expect(longDay('2026-10-02')).toBe('viernes 2 de octubre');
	});

	it('merges touching slots into ranges per day', () => {
		const p = person([
			['2026-10-02T09:00:00-05:00', '2026-10-02T09:30:00-05:00'],
			['2026-10-02T09:30:00-05:00', '2026-10-02T10:00:00-05:00'],
			['2026-10-02T15:00:00-05:00', '2026-10-02T15:30:00-05:00'],
			['2026-10-03T09:00:00-05:00', '2026-10-03T09:30:00-05:00']
		]);
		expect(mergedRanges(p, '2026-10-02')).toEqual(['9:00 a. m. – 10:00 a. m.', '3:00 p. m. – 3:30 p. m.']);
		expect(startsOn(p, '2026-10-03')).toEqual(['2026-10-03T09:00:00-05:00']);
		expect(mergedRanges(p, '2026-10-04')).toEqual([]);
	});

	it('splits an hour label for the narrow phone column', () => {
		expect(hourParts(9)).toEqual({ time: '9:00', period: 'a. m.' });
		expect(hourParts(12)).toEqual({ time: '12:00', period: 'p. m.' });
		expect(hourParts(0)).toEqual({ time: '12:00', period: 'a. m.' });
	});

	it('counts people, not entries: the owner in both áreas is one person', () => {
		const slot: [string, string] = ['2026-10-01T09:00:00-05:00', '2026-10-01T09:30:00-05:00'];
		const owner = person([slot]);
		const ownerT = { ...owner, user_id: 'owner', area: 'mentoria' as const };
		const ownerS = { ...owner, user_id: 'owner', area: 'soporte' as const };
		const ana = { ...owner, user_id: 'ana' };
		const failed = { ...owner, user_id: 'bea', error: true, slots: [] };
		expect(countFreePeople([ownerT, ownerS, ana, failed], '2026-10-01')).toBe(2);
		expect(countFreePeople([ownerT, ownerS, ana], '2026-10-02')).toBe(0);
	});

	it('forgets cached answers after the TTL and on clear', () => {
		const c = new ExpiringCache<number>(60_000);
		c.set('a', 1, 1_000);
		expect(c.get('a', 60_999)).toBe(1);
		expect(c.get('a', 61_000)).toBeUndefined();
		expect(c.get('a', 1_001)).toBeUndefined(); // an expired entry is dropped, not revived
		c.set('b', 2, 0);
		c.clear();
		expect(c.get('b', 1)).toBeUndefined();
	});
});
