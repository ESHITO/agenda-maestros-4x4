import { describe, expect, it } from 'vitest';
import type { WhatsAppNotice } from './api';
import type { UserPrefs } from './prefs';
import { fmtCardWhen, noticeDots, noticeTone } from './booking-card';

const four = (s: WhatsAppNotice['status'][]): WhatsAppNotice[] =>
	(['created', 'morning', '1h', '5m'] as const).map((kind, i) => ({ kind, status: s[i] }));

describe('noticeTone', () => {
	it('maps every state to one of four colours', () => {
		expect(noticeTone('sent')).toBe('ok');
		expect(noticeTone('pending')).toBe('wait');
		expect(noticeTone('sending')).toBe('wait');
		expect(noticeTone('failed')).toBe('bad');
		expect(noticeTone('missed')).toBe('bad');
		expect(noticeTone('cancelled')).toBe('off');
		expect(noticeTone('unknown')).toBe('off');
		expect(noticeTone('not_applicable')).toBe('off');
		expect(noticeTone('something new')).toBe('off');
	});
});

describe('noticeDots', () => {
	it('nothing to show without notices or when none applies', () => {
		expect(noticeDots(undefined)).toBeNull();
		expect(noticeDots([])).toBeNull();
		expect(noticeDots(four(['not_applicable', 'not_applicable', 'not_applicable', 'not_applicable']))).toBeNull();
	});

	it('one dot per notice, in order, and a Spanish label of all four', () => {
		const d = noticeDots(four(['sent', 'pending', 'failed', 'not_applicable']))!;
		expect(d.dots).toEqual([
			{ kind: 'created', tone: 'ok' },
			{ kind: 'morning', tone: 'wait' },
			{ kind: '1h', tone: 'bad' },
			{ kind: '5m', tone: 'off' }
		]);
		expect(d.label).toBe(
			'Avisos de WhatsApp. Confirmación: enviado; Mañana: pendiente; 1 hora: falló; 5 min: no aplica.'
		);
	});

	it('a single applicable notice is enough to show the dots', () => {
		expect(noticeDots(four(['not_applicable', 'not_applicable', 'not_applicable', 'missed']))?.dots[3].tone).toBe('bad');
	});
});

describe('fmtCardWhen', () => {
	const lima: UserPrefs = { timezone: 'America/Lima', time_format: '12h', week_start: 1, date_format: 'dmy' };
	// Normalise Intl's no-break spaces so the expectations stay readable.
	const norm = (s: string) => s.replace(/\s/g, ' ');
	const now = new Date('2026-09-30T15:00:00Z'); // Wed 30 Sep, 10:00 a. m. in Lima

	it('weekday, day and month in the profile zone, 12-hour time', () => {
		// 20:30 UTC = 3:30 p. m. in Lima, Friday 2 October.
		expect(norm(fmtCardWhen('2026-10-02T20:30:00Z', lima, now))).toBe('Vie 2 oct · 3:30 p. m.');
	});

	it('today, tomorrow and yesterday by name, as seen in the profile zone', () => {
		expect(norm(fmtCardWhen('2026-09-30T23:00:00Z', lima, now))).toBe('Hoy · 6:00 p. m.');
		// 03:00 UTC on 1 Oct is still 30 Sep, 10 p. m., in Lima.
		expect(norm(fmtCardWhen('2026-10-01T03:00:00Z', lima, now))).toBe('Hoy · 10:00 p. m.');
		expect(norm(fmtCardWhen('2026-10-01T14:00:00Z', lima, now))).toBe('Mañana · 9:00 a. m.');
		expect(norm(fmtCardWhen('2026-09-29T14:00:00Z', lima, now))).toBe('Ayer · 9:00 a. m.');
	});

	it('noon and midnight on the 12-hour clock', () => {
		expect(norm(fmtCardWhen('2026-10-05T17:00:00Z', lima, now))).toBe('Lun 5 oct · 12:00 p. m.');
		expect(norm(fmtCardWhen('2026-10-06T05:30:00Z', lima, now))).toBe('Mar 6 oct · 12:30 a. m.');
	});

	it('follows a 24-hour profile', () => {
		expect(norm(fmtCardWhen('2026-10-02T20:30:00Z', { ...lima, time_format: '24h' }, now))).toBe('Vie 2 oct · 15:30');
	});

	it('adds the year when it is not this one', () => {
		expect(norm(fmtCardWhen('2025-12-12T20:30:00Z', lima, now))).toBe('Vie 12 dic 2025 · 3:30 p. m.');
	});

	it('an unreadable date is empty', () => {
		expect(fmtCardWhen('nope', lima, now)).toBe('');
	});
});
