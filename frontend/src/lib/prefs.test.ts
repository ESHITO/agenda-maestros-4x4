// Booking times in the admin panel follow the signed-in user's PROFILE zone, not the
// device's (audit 29 Sep 2026). Each assertion compares two profile zones for one instant,
// so the result does not depend on the zone of the machine running the tests.
import { describe, expect, it } from 'vitest';
import { dayKeyInZone, displayZone, fmtDateTime, fmtShortWhen, fmtTime, type UserPrefs } from './prefs';

const base: UserPrefs = { timezone: 'America/Lima', time_format: '12h', week_start: 1, date_format: 'dmy' };
const lima = base;
const madrid: UserPrefs = { ...base, timezone: 'Europe/Madrid' };
// Wed 30 Sep 2026, 8:30 p. m. in Lima = Thu 1 Oct 2026, 3:30 a. m. in Madrid.
const ISO = '2026-10-01T01:30:00Z';
const sp = (s: string) => s.replace(/[  ]/g, ' ');

describe('prefs: times in the profile zone', () => {
	it('fmtDateTime uses the profile zone for the date and the time', () => {
		const l = sp(fmtDateTime(ISO, lima));
		const m = sp(fmtDateTime(ISO, madrid));
		expect(l.startsWith('30/09/2026, ')).toBe(true);
		expect(l).toMatch(/08:30/);
		expect(m.startsWith('01/10/2026, ')).toBe(true);
		expect(m).toMatch(/03:30/);
	});

	it('fmtTime uses the profile zone', () => {
		expect(sp(fmtTime(ISO, lima))).toMatch(/08:30/);
		expect(sp(fmtTime(ISO, madrid))).toMatch(/03:30/);
		expect(fmtTime(ISO, { ...lima, time_format: '24h' })).toMatch(/20:30/);
	});

	it('dayKeyInZone and displayZone follow the profile, falling back to the device when unusable', () => {
		expect(dayKeyInZone(ISO, lima)).toBe('2026-09-30');
		expect(dayKeyInZone(ISO, madrid)).toBe('2026-10-01');
		expect(displayZone(madrid)).toBe('Europe/Madrid');
		const device = Intl.DateTimeFormat().resolvedOptions().timeZone;
		expect(displayZone({ ...base, timezone: '' })).toBe(device);
		expect(displayZone({ ...base, timezone: 'Mars/Olympus' })).toBe(device);
	});

	it('fmtShortWhen says hoy / mañana / ayer by the day in the profile zone', () => {
		// 29 Sep 2026 22:00 in Lima = 30 Sep 05:00 in Madrid.
		const now = new Date('2026-09-30T03:00:00Z');
		expect(sp(fmtShortWhen(ISO, lima, now))).toMatch(/^mañana 08:30/);
		expect(sp(fmtShortWhen(ISO, madrid, now))).toMatch(/^mañana 03:30/);
		expect(sp(fmtShortWhen('2026-09-30T02:00:00Z', lima, now))).toMatch(/^hoy /);
		expect(sp(fmtShortWhen('2026-09-30T02:00:00Z', madrid, now))).toMatch(/^hoy /);
		expect(sp(fmtShortWhen('2026-09-29T12:00:00Z', madrid, now))).toMatch(/^ayer /);
		expect(sp(fmtShortWhen('2026-10-05T15:00:00Z', lima, now))).toMatch(/^05\/10 /);
		expect(sp(fmtShortWhen('2026-10-05T15:00:00Z', { ...lima, date_format: 'mdy' }, now))).toMatch(/^10\/05 /);
		expect(fmtShortWhen(undefined, lima, now)).toBe('');
		expect(fmtShortWhen('nope', lima, now)).toBe('');
	});
});
