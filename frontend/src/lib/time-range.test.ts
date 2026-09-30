import { describe, expect, it } from 'vitest';
import { fixRange } from './time-range';

const r = (s: string, e: string) => ({ start_time: s, end_time: e });

describe('fixRange: a start not before the end is fixed, never refused', () => {
	it('keeps a valid block as it is', () => {
		expect(fixRange(r('09:00', '17:00'), 'start')).toEqual(r('09:00', '17:00'));
		expect(fixRange(r('09:00', '17:00'), 'end')).toEqual(r('09:00', '17:00'));
	});

	it('moves the end to start + 1 h when the start reaches it', () => {
		expect(fixRange(r('17:00', '17:00'), 'start')).toEqual(r('17:00', '18:00'));
		expect(fixRange(r('18:30', '17:00'), 'start')).toEqual(r('18:30', '19:30'));
	});

	it('caps the end at 23:30', () => {
		expect(fixRange(r('23:00', '10:00'), 'start')).toEqual(r('23:00', '23:30'));
	});

	it('a start of 23:30 becomes 23:00 - 23:30', () => {
		expect(fixRange(r('23:30', '17:00'), 'start')).toEqual(r('23:00', '23:30'));
	});

	it('moves the start to end - 1 h when the end is set before it', () => {
		expect(fixRange(r('09:00', '08:00'), 'end')).toEqual(r('07:00', '08:00'));
		expect(fixRange(r('09:00', '00:30'), 'end')).toEqual(r('00:00', '00:30'));
		expect(fixRange(r('09:00', '00:00'), 'end')).toEqual(r('00:00', '00:30'));
	});
});
