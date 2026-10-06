// Fork: the team calendar's periods (Mes / quincena / semana, Monday first), the 12-hour
// texts it prints from the server's minutes, what it says about each person's day, and
// the coverage ("Sin cubrir") it marks.
import { describe, expect, it } from 'vitest';
import type { TeamCalendar, TeamCalPerson } from './api';
import {
	countFreePeople,
	countSessions,
	countWorkingPeople,
	dayCountsText,
	dayCoverage,
	mergeQuickAnswer,
	freeUnknown,
	ownerOnlyText,
	personDot,
	uncoveredText,
	distinctPeople,
	draftToDays,
	durationText,
	freeHourRows,
	freeSpans,
	gridDays,
	mergeSpans,
	minLabel,
	periodLabel,
	periodOf,
	personDayState,
	rowsFor,
	shiftPeriod,
	spanLabel,
	targetDraft,
	targetSummary,
	targetTimeLabel,
	tickLabel,
	timelineTicks,
	timelineWindow,
	wallClock
} from './team-calendar';

function person(over: Partial<TeamCalPerson> = {}): TeamCalPerson {
	return {
		key: 'mentoria:ana',
		user_id: 'ana',
		name: 'Ana Torres',
		area: 'mentoria',
		avatar_url: '',
		color: '#2563eb',
		color_custom: false,
		is_owner: false,
		is_you: false,
		link: { slug: 'm-ana', url: 'https://agenda.example/book/m-ana' },
		duration_min: 60,
		days: {},
		...over
	};
}

function daysOfOct2027(a: number, b: number): string[] {
	return Array.from({ length: b - a + 1 }, (_, i) => `2027-10-${String(a + i).padStart(2, '0')}`);
}

describe('periods', () => {
	it('Mes is the 1st to the last day, leap years included', () => {
		expect(periodOf('month', '2026-10-06')).toEqual({ from: '2026-10-01', to: '2026-10-31' });
		expect(periodOf('month', '2028-02-10')).toEqual({ from: '2028-02-01', to: '2028-02-29' });
		expect(periodOf('month', '2026-02-28')).toEqual({ from: '2026-02-01', to: '2026-02-28' });
	});

	it('15 días is the quincena: 1-15 and 16-end, never splitting a month', () => {
		expect(periodOf('fortnight', '2026-10-15')).toEqual({ from: '2026-10-01', to: '2026-10-15' });
		expect(periodOf('fortnight', '2026-10-16')).toEqual({ from: '2026-10-16', to: '2026-10-31' });
		expect(periodOf('fortnight', '2026-02-20')).toEqual({ from: '2026-02-16', to: '2026-02-28' });
	});

	it('Semana runs Monday to Sunday', () => {
		expect(periodOf('week', '2026-10-06')).toEqual({ from: '2026-10-05', to: '2026-10-11' }); // a Tuesday
		expect(periodOf('week', '2026-10-11')).toEqual({ from: '2026-10-05', to: '2026-10-11' }); // a Sunday
		expect(periodOf('week', '2026-10-05')).toEqual({ from: '2026-10-05', to: '2026-10-11' }); // a Monday
	});

	it('moves one period at a time, across months and years', () => {
		expect(shiftPeriod('month', '2026-12-01', 1)).toBe('2027-01-01');
		expect(shiftPeriod('month', '2026-01-01', -1)).toBe('2025-12-01');
		expect(shiftPeriod('fortnight', '2026-10-01', 1)).toBe('2026-10-16');
		expect(shiftPeriod('fortnight', '2026-10-16', 1)).toBe('2026-11-01');
		expect(shiftPeriod('fortnight', '2026-10-01', -1)).toBe('2026-09-16');
		expect(shiftPeriod('fortnight', '2027-01-01', -1)).toBe('2026-12-16');
		expect(shiftPeriod('fortnight', '2026-12-16', 1)).toBe('2027-01-01');
		expect(shiftPeriod('week', '2026-10-05', 1)).toBe('2026-10-12');
		expect(shiftPeriod('week', '2026-12-28', 1)).toBe('2027-01-04');
	});

	it('names each period in Spanish', () => {
		expect(periodLabel('month', '2026-10-01', '2026-10-31')).toBe('octubre de 2026');
		expect(periodLabel('fortnight', '2026-10-16', '2026-10-31')).toBe('16 – 31 de octubre de 2026');
		expect(periodLabel('week', '2026-10-05', '2026-10-11')).toBe('5 – 11 de octubre de 2026');
		expect(periodLabel('week', '2026-09-28', '2026-10-04')).toBe('28 de septiembre – 4 de octubre de 2026');
		expect(periodLabel('week', '2026-12-28', '2027-01-03')).toBe('28 de diciembre de 2026 – 3 de enero de 2027');
	});

	it('fills whole Monday-first weeks and flags the days outside the period', () => {
		const g = gridDays('2026-10-01', '2026-10-31'); // 1 Oct 2026 is a Thursday
		expect(g.length % 7).toBe(0);
		expect(g[0]).toEqual({ day: '2026-09-28', inRange: false });
		expect(g[3]).toEqual({ day: '2026-10-01', inRange: true });
		expect(g[g.length - 1]).toEqual({ day: '2026-11-01', inRange: false });
		expect(g.filter((c) => c.inRange).length).toBe(31);
		expect(gridDays('2026-10-16', '2026-10-31').filter((c) => c.inRange).length).toBe(16);
	});

	it('days after the furthest day the server answers are out of range, never empty tappable days', () => {
		const g = gridDays('2027-10-01', '2027-10-31', '2027-10-07');
		expect(g.filter((c) => c.inRange).map((c) => c.day)).toEqual(daysOfOct2027(1, 7));
		expect(g.find((c) => c.day === '2027-10-08')).toEqual({ day: '2027-10-08', inRange: false });
		expect(gridDays('2027-10-01', '2027-10-31', '2027-12-01').filter((c) => c.inRange).length).toBe(31);
	});
});

describe('times', () => {
	it('prints minutes on the 12-hour clock, midnight included', () => {
		expect(minLabel(540)).toBe('9:00 a. m.');
		expect(minLabel(30)).toBe('12:30 a. m.');
		expect(minLabel(750)).toBe('12:30 p. m.');
		expect(minLabel(1200)).toBe('8:00 p. m.');
		expect(minLabel(1440)).toBe('12:00 a. m.');
		expect(spanLabel([480, 780])).toBe('8:00 a. m. – 1:00 p. m.');
		expect(tickLabel(720)).toBe('12 p. m.');
		expect(tickLabel(0)).toBe('12 a. m.');
		expect(targetTimeLabel('24:00')).toBe('12:00 a. m. (medianoche)');
		expect(targetTimeLabel('08:30')).toBe('8:30 a. m.');
	});

	it('says durations shortly', () => {
		expect(durationText(240)).toBe('4 h');
		expect(durationText(30)).toBe('30 min');
		expect(durationText(90)).toBe('1 h 30 min');
	});

	it('reads the wall clock straight from the RFC3339 text', () => {
		expect(wallClock('2026-10-06T13:00:00-05:00')).toEqual({ day: '2026-10-06', min: 780 });
		expect(wallClock('2026-12-05T00:30:00+01:00')).toEqual({ day: '2026-12-05', min: 30 });
	});

	it('merges touching spans', () => {
		expect(
			mergeSpans([
				[600, 660],
				[540, 600],
				[700, 720],
				[710, 800]
			])
		).toEqual([
			[540, 660],
			[700, 800]
		]);
	});

	it('frames the timeline around what there is to draw, at least 4 h', () => {
		const p = person({ days: { d: { hours: [[545, 780]], busy: [{ key: 'b1', start: 1230, end: 1290, type: 'X', area: 'mentoria' }] } } });
		expect(timelineWindow([p], [], 'd')).toEqual({ start: 540, end: 1320 });
		expect(timelineWindow([], [], 'd')).toEqual({ start: 480, end: 1200 });
		expect(timelineWindow([person({ days: { d: { hours: [[600, 660]] } } })], [], 'd')).toEqual({ start: 600, end: 840 });
		expect(timelineWindow([person({ days: { d: { hours: [[1380, 1440]] } } })], [], 'd')).toEqual({ start: 1200, end: 1440 });
		expect(timelineTicks({ start: 480, end: 1200 }, true)).toEqual([480, 720, 960, 1200]);
		expect(timelineTicks({ start: 480, end: 1200 }, false)).toEqual([540, 720, 900, 1080]);
	});
});

describe('people and days', () => {
	const ownerT = person({ key: 'mentoria:own', user_id: 'own', name: 'Daniel Pérez', is_owner: true, days: { d: { hours: [[480, 720]], free: [480, 540] } } });
	const ownerS = { ...ownerT, key: 'soporte:own', area: 'soporte' as const };
	const ana = person({
		days: {
			d: {
				hours: [[540, 780]],
				free: [720],
				busy: [
					{ key: 'b1', start: 600, end: 660, type: 'Mentoría privada', area: 'mentoria' },
					{ key: 'b2', start: 660, end: 690, type: 'Otra reunión', area: '' }
				]
			}
		}
	});
	const bart = person({ key: 'soporte:bart', user_id: 'bart', name: 'Bartolomé Ríos', area: 'soporte', error: true, error_kind: 'calendar', days: { d: { hours: [[600, 780]] } } });

	it('counts people, not rows, and sessions once', () => {
		const all = [ownerT, ana, bart, ownerS];
		expect(countFreePeople(all, 'd')).toBe(2); // the owner (two rows) and Ana; Bart failed
		expect(countWorkingPeople(all, 'd')).toBe(3);
		const shared = { key: 'b9', start: 900, end: 960, type: 'Mentoría privada', area: 'mentoria' as const };
		const t = { ...ownerT, days: { d: { busy: [shared] } } };
		const s = { ...ownerS, days: { d: { busy: [shared] } } };
		expect(countSessions([t, s, ana], 'd')).toBe(3);
		expect(distinctPeople(all).map((p) => p.user_id)).toEqual(['own', 'ana', 'bart']);
	});

	it('filters by área and by the people picked', () => {
		const all = [ownerT, ana, bart, ownerS];
		expect(rowsFor(all, 'soporte').map((p) => p.key)).toEqual(['soporte:bart', 'soporte:own']);
		expect(rowsFor(all, 'all', new Set(['own'])).map((p) => p.key)).toEqual(['mentoria:own', 'soporte:own']);
		expect(rowsFor(all, 'all', new Set()).length).toBe(4);
	});

	it('turns free starts into spans of one session each, merged', () => {
		expect(freeSpans(ownerT, 'd')).toEqual([[480, 600]]);
		expect(freeSpans(ana, 'd')).toEqual([[720, 780]]);
		expect(freeSpans(ana, 'other')).toEqual([]);
		expect(freeSpans(person({ duration_min: 90, days: { d: { free: [1380] } } }), 'd')).toEqual([[1380, 1440]]);
	});

	it('lists the hours where someone can start, without failed people', () => {
		const rows = freeHourRows([ownerT, ana, bart], 'd');
		expect(rows.map((r) => [r.hour, r.people.map((p) => p.user_id)])).toEqual([
			[8, ['own']],
			[9, ['own']],
			[12, ['ana']]
		]);
	});

	it('says what each day is, and only the causes it knows', () => {
		const today = '2026-10-06';
		const base = { hours: [[540, 720]] as [number, number][] };
		expect(personDayState(person({ days: { '2026-10-05': { busy: [] } } }), '2026-10-05', today, true).kind).toBe('past');
		expect(personDayState(person(), today, today, true)).toEqual({ kind: 'off', note: 'No trabaja este día.' });
		expect(personDayState(person({ days: { [today]: { ...base, free: [540] } } }), today, today, true).kind).toBe('free');
		expect(personDayState(person({ days: { [today]: base } }), today, today, false).kind).toBe('checking');
		expect(personDayState(person({ days: { [today]: base } }), today, today, false, true).note).toBe('Revisando su calendario…');
		// The quick answer arrived but the complete one failed: never "Revisando…" forever.
		expect(personDayState(person({ days: { [today]: base } }), today, today, false, false)).toEqual({
			kind: 'unknown',
			note: 'No se pudo revisar su calendario; pulsa Actualizar.'
		});
		// A day off is still a day off, whatever happened to the calendars.
		expect(personDayState(person(), today, today, false, false).kind).toBe('off');
		expect(personDayState(person({ error: true, error_kind: 'timeout', days: { [today]: base } }), today, today, true).note).toContain('pulsa Actualizar');
		expect(personDayState(person({ error: true, error_kind: 'internal' }), today, today, true).kind).toBe('error');
		// Works 9-12 today but the notice runs until 1 p. m.: certain.
		expect(personDayState(person({ notice_until: `${today}T13:00:00-05:00`, days: { [today]: base } }), today, today, true).note).toBe(
			'Sin cupos: ya no se puede reservar con tan poca anticipación.'
		);
		// The notice ends at 10 a. m.: the hours after it may be taken for other reasons.
		expect(personDayState(person({ notice_until: `${today}T10:00:00-05:00`, days: { [today]: base } }), today, today, true).note).toBe('Sin cupos libres.');
		// Past the furthest bookable day.
		expect(
			personDayState(person({ bookable_until: '2026-11-05T09:00:00-05:00', days: { '2026-11-06': base } }), '2026-11-06', today, true).note
		).toBe('Sin cupos: es más allá de los días que su enlace deja reservar.');
		expect(
			personDayState(person({ bookable_until: '2026-11-05T09:00:00-05:00', days: { '2026-11-05': base } }), '2026-11-05', today, true).note
		).toBe('Sin cupos: es más allá de los días que su enlace deja reservar.');
		expect(
			personDayState(person({ bookable_until: '2026-11-05T10:00:00-05:00', days: { '2026-11-05': base } }), '2026-11-05', today, true).note
		).toBe('Sin cupos libres.');
	});
});

describe('coverage', () => {
	const data = {
		today: '2026-10-06',
		coverage: {
			mentoria: { days: { '2026-10-06': { target: [[480, 1200]] as [number, number][], uncovered: [[480, 540]] as [number, number][] } } },
			soporte: {
				days: {
					'2026-10-06': { target: [[480, 1200]] as [number, number][], owner_only: [[780, 1200]] as [number, number][] },
					'2026-10-07': { target: [[480, 1200]] as [number, number][] }
				}
			}
		}
	};

	it('marks gaps, owner-only hours and covered days per área shown', () => {
		const all = dayCoverage(data, '2026-10-06', 'all');
		expect(all.state).toBe('gaps');
		expect(all.uncoveredMin).toBe(60);
		expect(all.ownerOnlyMin).toBe(420);
		expect(all.areas.map((a) => a.area)).toEqual(['mentoria', 'soporte']);
		expect(dayCoverage(data, '2026-10-06', 'soporte').state).toBe('owner_only');
		expect(dayCoverage(data, '2026-10-07', 'soporte').state).toBe('covered');
		expect(dayCoverage(data, '2026-10-07', 'mentoria').state).toBe('none'); // no target sent that day
		expect(dayCoverage(data, '2026-10-05', 'all').state).toBe('past');
		expect(dayCoverage({ today: '2026-10-06', coverage: {} }, '2026-10-06', 'all').state).toBe('none'); // no template set
	});
});

describe('the target', () => {
	it('summarises consecutive days with the same hours', () => {
		const t = {
			tz: 'America/Lima',
			days: [1, 2, 3, 4, 5, 6].map((dow) => ({ dow, start: '08:00', end: '20:00' }))
		};
		expect(targetSummary(t)).toBe('lun–sáb 8:00 a. m. – 8:00 p. m. · hora de Lima');
		expect(
			targetSummary({
				tz: 'America/Argentina/Buenos_Aires',
				days: [
					{ dow: 1, start: '09:00', end: '18:00' },
					{ dow: 2, start: '09:00', end: '18:00' },
					{ dow: 6, start: '09:00', end: '13:00' },
					{ dow: 0, start: '10:00', end: '24:00' }
				]
			})
		).toBe('lun–mar 9:00 a. m. – 6:00 p. m. · sáb 9:00 a. m. – 1:00 p. m. · dom 10:00 a. m. – 12:00 a. m. · hora de Buenos Aires');
		expect(targetSummary({ tz: 'America/Lima', days: [] })).toBe('Sin horario a cubrir');
	});

	it('round-trips through the editor rows, Monday first', () => {
		const rows = targetDraft({ days: [{ dow: 0, start: '10:00', end: '14:00' }, { dow: 1, start: '08:00', end: '20:00' }] });
		expect(rows.map((r) => r.dow)).toEqual([1, 2, 3, 4, 5, 6, 0]);
		expect(rows[0]).toEqual({ dow: 1, on: true, start: '08:00', end: '20:00' });
		expect(rows[1].on).toBe(false);
		expect(draftToDays(rows)).toEqual({
			days: [
				{ dow: 0, start: '10:00', end: '14:00' },
				{ dow: 1, start: '08:00', end: '20:00' }
			],
			error: ''
		});
		rows[2] = { dow: 3, on: true, start: '12:00', end: '12:00' };
		expect(draftToDays(rows).error).toBe('El miércoles: la hora de inicio debe ser anterior a la de fin.');
		expect(draftToDays(targetDraft({ days: [] }))).toEqual({ days: [], error: '' });
	});
});

describe('coverage under "Todos" counts clock hours, per área', () => {
	const span = (s: number, e: number) => [s, e] as [number, number];
	const data = {
		today: '2026-10-06',
		coverage: {
			// 8-9 a. m. and 6-8 p. m. nobody in EITHER área: 3 clock hours, not 6.
			mentoria: { days: { '2026-10-06': { target: [span(480, 1200)], uncovered: [span(480, 540), span(1080, 1200)] } } },
			soporte: {
				days: {
					'2026-10-06': { target: [span(480, 1200)], uncovered: [span(480, 540), span(1080, 1200)] },
					'2026-10-07': { target: [span(480, 1200)], uncovered: [span(480, 1200)] }
				}
			}
		}
	};

	it('never adds the áreas up', () => {
		const all = dayCoverage(data, '2026-10-06', 'all');
		expect(all.uncoveredMin).toBe(180);
		expect(all.areas.map((a) => a.uncoveredMin)).toEqual([180, 180]);
		// A 12-hour target day nobody covers in Soporte, Mentoría has no target that day.
		const next = dayCoverage(data, '2026-10-07', 'all');
		expect(next.uncoveredMin).toBe(720);
	});

	it('says the hours per área when two are shown, one figure with one', () => {
		expect(uncoveredText(dayCoverage(data, '2026-10-06', 'all'), true)).toBe('Sin cubrir: M 3 h · S 3 h');
		expect(uncoveredText(dayCoverage(data, '2026-10-06', 'all'), false)).toBe('sin cubrir: Mentoría 3 h, Soporte 3 h');
		expect(uncoveredText(dayCoverage(data, '2026-10-06', 'soporte'), true)).toBe('Sin cubrir 3 h');
		expect(uncoveredText(dayCoverage(data, '2026-10-07', 'all'), true)).toBe('Sin cubrir 12 h'); // only Soporte has a target
		const owner = {
			today: '2026-10-06',
			coverage: { soporte: { days: { '2026-10-06': { target: [span(480, 1200)], owner_only: [span(780, 900)] } } } }
		};
		expect(ownerOnlyText(dayCoverage(owner, '2026-10-06', 'all'), true)).toBe('Solo propietario 2 h');
		expect(uncoveredText(dayCoverage(owner, '2026-10-06', 'all'), true)).toBe('');
	});
});

describe("a person's mark in a cell", () => {
	const today = '2026-10-06';
	const hours = [[540, 720]] as [number, number][];
	const session = { key: 'b1', start: 540, end: 600, type: 'Mentoría privada', area: 'mentoria' as const };

	it('free is solid; sessions add a ring', () => {
		expect(personDot([person({ days: { [today]: { hours, free: [600] } } })], today, today, true)).toEqual({ kind: 'free', booked: false });
		expect(personDot([person({ days: { [today]: { hours, free: [600], busy: [session] } } })], today, today, true)).toEqual({ kind: 'free', booked: true });
	});

	it('fully booked is "busy" (stripes), not "works, nothing free"', () => {
		expect(personDot([person({ days: { [today]: { hours, busy: [session] } } })], today, today, true)).toEqual({ kind: 'busy', booked: true });
		expect(personDot([person({ days: { [today]: { hours } } })], today, today, true)).toEqual({ kind: 'full', booked: false });
	});

	it('free time not checked yet, or not checkable, is "unknown", never "full"', () => {
		const p = person({ days: { [today]: { hours } } });
		expect(personDot([p], today, today, false)).toEqual({ kind: 'unknown', booked: false }); // quick answer
		expect(personDot([{ ...p, error: true, error_kind: 'timeout' }], today, today, true)?.kind).toBe('unknown');
		expect(personDot([{ ...p, error: true, error_kind: 'calendar', days: { [today]: { hours, busy: [session] } } }], today, today, true)).toEqual({
			kind: 'unknown',
			booked: true
		});
		// The owner's two rows: one checked with free starts wins over the other's error.
		const ok = person({ key: 'soporte:ana', area: 'soporte', days: { [today]: { hours, free: [540] } } });
		expect(personDot([{ ...p, error: true, error_kind: 'calendar' }, ok], today, today, true)?.kind).toBe('free');
		expect(freeUnknown(p, false)).toBe(true);
		expect(freeUnknown(p, true)).toBe(false);
	});

	it('past days show only sessions; nothing to show is null', () => {
		expect(personDot([person({ days: { '2026-10-05': { busy: [session] } } })], '2026-10-05', today, true)).toEqual({ kind: 'busy', booked: true });
		expect(personDot([person({ days: { '2026-10-05': { hours } } })], '2026-10-05', today, true)).toBeNull();
		expect(personDot([person()], today, today, true)).toBeNull();
	});
});

describe('counts in words', () => {
	const c = { free: 5, working: 6, sessions: 1 };
	it('never abbreviates, and gives every number its noun', () => {
		expect(dayCountsText(c, true, 'long')).toBe('5 personas libres · 6 trabajan · 1 sesión');
		expect(dayCountsText({ free: 1, working: 1, sessions: 0 }, true, 'long')).toBe('1 persona libre · 1 trabaja · 0 sesiones');
		expect(dayCountsText({ ...c, sessions: 2 }, true, 'wide')).toBe('5 personas libres · 6 trabajan · 2 sesiones');
		expect(dayCountsText({ ...c, sessions: 0 }, true, 'wide')).toBe('5 personas libres · 6 trabajan');
		for (const form of ['long', 'wide', 'phone'] as const) expect(dayCountsText(c, true, form)).not.toMatch(/ses\.|\d libres?/);
	});
	it('on a phone drops "trabajan" when the free count is there', () => {
		expect(dayCountsText(c, true, 'phone')).toBe('5 personas libres · 1 sesión');
		expect(dayCountsText({ ...c, sessions: 0 }, true, 'phone')).toBe('5 personas libres');
	});
	it('without free starts the working count opens the text, with its noun', () => {
		expect(dayCountsText(c, false, 'phone')).toBe('6 personas trabajan · 1 sesión');
		expect(dayCountsText({ free: 0, working: 1, sessions: 0 }, false, 'wide')).toBe('1 persona trabaja');
		expect(dayCountsText(c, false, 'long')).toBe('6 personas trabajan · 1 sesión');
	});
});

describe('a saved target shows at once', () => {
	const day = '2026-10-06';
	const answer = (over: Partial<TeamCalendar>): TeamCalendar => ({
		tz: 'America/Lima',
		from: '2026-10-01',
		to: '2026-10-31',
		today: day,
		generated_at: '',
		free_included: true,
		target: { v: 1, tz: 'America/Lima', days: [], is_default: false, can_edit: true },
		people: [],
		coverage: {},
		...over
	});
	const shown = answer({
		people: [
			person({ days: { [day]: { hours: [[540, 720]], free: [540, 600] } } }),
			person({ key: 'soporte:bart', user_id: 'bart', error: true, error_kind: 'calendar', days: { [day]: { hours: [[600, 660]] } } })
		],
		coverage: { mentoria: { days: { [day]: { target: [[480, 1200]], uncovered: [[480, 540]] } } } }
	});
	const quick = answer({
		free_included: false,
		target: { v: 1, tz: 'America/Lima', days: [{ dow: 2, start: '09:00', end: '12:00' }], is_default: false, can_edit: true },
		people: [
			person({ days: { [day]: { hours: [[540, 720]], busy: [{ key: 'b1', start: 600, end: 660, type: 'Mentoría privada', area: 'mentoria' }] } } }),
			person({ key: 'soporte:bart', user_id: 'bart', days: { [day]: { hours: [[600, 660]] } } }),
			person({ key: 'mentoria:new', user_id: 'new', days: { [day]: { hours: [[540, 720]] } } })
		],
		coverage: { mentoria: { days: { [day]: { target: [[540, 720]] } } } }
	});

	it("takes the quick answer's target, coverage, hours and sessions, keeping the free starts", () => {
		const m = mergeQuickAnswer(shown, quick);
		expect(m.free_included).toBe(true);
		expect(m.target).toEqual(quick.target);
		expect(m.coverage).toEqual(quick.coverage);
		expect(m.people.map((p) => p.key)).toEqual(['mentoria:ana', 'soporte:bart']); // the new row waits
		expect(m.people[0].days[day]).toEqual({ hours: [[540, 720]], busy: quick.people[0].days[day].busy, free: [540, 600] });
		expect(m.people[1].error).toBe(true); // its free time is still unknown
		expect(shown.coverage.mentoria!.days[day].uncovered).toEqual([[480, 540]]); // never mutated
	});

	it('drops free starts on a day the person no longer works', () => {
		const off = answer({ free_included: false, people: [person({ days: { [day]: { busy: [] } } })] });
		expect(mergeQuickAnswer(shown, off).people[0].days[day].free).toBeUndefined();
	});

	it('another range changes nothing; a quick answer shown is just replaced', () => {
		expect(mergeQuickAnswer(shown, { ...quick, from: '2026-11-01' })).toBe(shown);
		expect(mergeQuickAnswer({ ...shown, free_included: false }, quick)).toBe(quick);
	});
});
