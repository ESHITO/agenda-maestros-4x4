import { describe, expect, it } from 'vitest';
import { countdown } from './countdown';

const START = '2026-10-02T20:00:00Z';
const END = '2026-10-02T21:00:00Z';
const S = new Date(START).getTime();
const sec = 1000;
const min = 60 * sec;
const hour = 60 * min;
const day = 24 * hour;

// The countdown seen `ms` before the start.
const before = (ms: number) => countdown(START, END, S - ms);

describe('countdown: minutes', () => {
	it('under a minute', () => {
		expect(before(1)).toEqual({ text: 'Empieza en menos de 1 min', tone: 'soon' });
		expect(before(59 * sec)).toEqual({ text: 'Empieza en menos de 1 min', tone: 'soon' });
	});

	it('exactly one minute', () => {
		expect(before(min)?.text).toBe('Empieza en 1 min');
	});

	it('rounds down, never up', () => {
		expect(before(12 * min + 59 * sec)?.text).toBe('Empieza en 12 min');
		expect(before(59 * min + 59 * sec)).toEqual({ text: 'Empieza en 59 min', tone: 'soon' });
	});
});

describe('countdown: hours', () => {
	it('exactly one hour drops the minutes', () => {
		expect(before(hour)).toEqual({ text: 'Empieza en 1 h', tone: 'later' });
	});

	it('hours and minutes, rounded down', () => {
		expect(before(5 * hour + 20 * min)?.text).toBe('Empieza en 5 h 20 min');
		expect(before(5 * hour + 20 * min + 59 * sec)?.text).toBe('Empieza en 5 h 20 min');
		expect(before(day - 1)?.text).toBe('Empieza en 23 h 59 min');
	});
});

describe('countdown: days', () => {
	it('one day, singular, with and without hours', () => {
		expect(before(day)?.text).toBe('Empieza en 1 día');
		expect(before(day + 4 * hour)?.text).toBe('Empieza en 1 día 4 h');
		expect(before(day + 4 * hour + 59 * min)?.text).toBe('Empieza en 1 día 4 h');
	});

	it('two days, plural', () => {
		expect(before(2 * day + 23 * hour + 59 * min)?.text).toBe('Empieza en 2 días 23 h');
		expect(before(2 * day)?.text).toBe('Empieza en 2 días');
	});

	it('from three days on, days only', () => {
		expect(before(3 * day)?.text).toBe('Empieza en 3 días');
		expect(before(3 * day + 22 * hour)?.text).toBe('Empieza en 3 días');
		expect(before(40 * day)).toEqual({ text: 'Empieza en 40 días', tone: 'later' });
	});
});

describe('countdown: started and ended', () => {
	it('at the start and until the end: En curso', () => {
		expect(countdown(START, END, S)).toEqual({ text: 'En curso', tone: 'live' });
		expect(countdown(START, END, S + 30 * min)).toEqual({ text: 'En curso', tone: 'live' });
		expect(countdown(START, END, new Date(END).getTime() - 1)?.tone).toBe('live');
	});

	it('nothing at or after the end', () => {
		expect(countdown(START, END, new Date(END).getTime())).toBeNull();
		expect(countdown(START, END, new Date(END).getTime() + day)).toBeNull();
	});

	it('accepts Date objects for every argument', () => {
		expect(countdown(new Date(START), new Date(END), new Date(S - 12 * min))?.text).toBe('Empieza en 12 min');
	});

	it('an unreadable date shows nothing', () => {
		expect(countdown('not a date', END, S)).toBeNull();
		expect(countdown(START, 'not a date', S + min)).toBeNull();
		// Before the start the end is not needed.
		expect(countdown(START, 'not a date', S - 12 * min)?.text).toBe('Empieza en 12 min');
	});
});
