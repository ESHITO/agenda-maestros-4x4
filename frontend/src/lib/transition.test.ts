import { describe, expect, it } from 'vitest';
import {
	TRANSITION_CHOICES,
	cadenceWarning,
	sessionSpan,
	effectiveInterval,
	exampleStarts,
	hourlyWarning,
	startsList,
	supportCadenceSentence,
	transitionSentence
} from './transition';

describe('exampleStarts: from 9:00, every interval, 12-hour', () => {
	it('one session per hour', () => {
		expect(exampleStarts(60)).toEqual(['9:00 a. m.', '10:00 a. m.', '11:00 a. m.', '12:00 p. m.']);
	});

	it('40 + 10 of transition = every 50', () => {
		expect(exampleStarts(50)).toEqual(['9:00 a. m.', '9:50 a. m.', '10:40 a. m.', '11:30 a. m.']);
	});

	it('an odd interval keeps its minutes', () => {
		expect(exampleStarts(55, 3)).toEqual(['9:00 a. m.', '9:55 a. m.', '10:50 a. m.']);
	});

	it('stops at the end of the day', () => {
		expect(exampleStarts(600)).toEqual(['9:00 a. m.', '7:00 p. m.']);
	});

	it('nothing for a non-positive or missing interval', () => {
		expect(exampleStarts(0)).toEqual([]);
		expect(exampleStarts(-5)).toEqual([]);
		expect(exampleStarts(Number.NaN)).toEqual([]);
	});
});

describe('startsList', () => {
	it('ends with an ellipsis while the day goes on', () => {
		expect(startsList(60)).toBe('9:00 a. m., 10:00 a. m., 11:00 a. m., 12:00 p. m.…');
	});

	it('no ellipsis when the day is over', () => {
		expect(startsList(600)).toBe('9:00 a. m., 7:00 p. m.');
	});
});

describe('transitionSentence', () => {
	it('the owner example', () => {
		expect(transitionSentence(40, 10, 60)).toBe(
			'Sesiones de 40 min + 10 min de transición. Los clientes podrán reservar a las 9:00 a. m., 10:00 a. m., 11:00 a. m., 12:00 p. m.…'
		);
	});

	it('without a transition', () => {
		expect(transitionSentence(30, 0, 30)).toBe(
			'Sesiones de 30 min sin tiempo de transición. Los clientes podrán reservar a las 9:00 a. m., 9:30 a. m., 10:00 a. m., 10:30 a. m.…'
		);
	});

	it('an empty interval while typing still explains the session', () => {
		expect(transitionSentence(40, 10, 0)).toBe('Sesiones de 40 min + 10 min de transición.');
	});
});

describe('warnings (never a block)', () => {
	it('interval shorter than session + transition', () => {
		expect(cadenceWarning(40, 15, 30)).toContain('algunos horarios no se podrán reservar uno detrás de otro');
		expect(cadenceWarning(40, 15, 30)).toContain('55 min');
	});

	it('fits exactly: no warning', () => {
		expect(cadenceWarning(40, 10, 50)).toBe('');
		expect(cadenceWarning(40, 10, 60)).toBe('');
	});

	it('the margins overlap, they do not add up: the larger one counts', () => {
		// The engine keeps max(before, after) on each side of a booked session.
		expect(sessionSpan(40, 10, 5)).toBe(50);
		expect(sessionSpan(40, 10, 15)).toBe(55);
		expect(cadenceWarning(40, 10, 50, 5)).toBe('');
		expect(cadenceWarning(40, 10, 50, 15)).toContain('55 min');
	});

	it('one per hour that does not fit in the hour', () => {
		expect(hourlyWarning(40, 10)).toBe('');
		expect(hourlyWarning(40, 15, 10)).toBe('');
		expect(hourlyWarning(50, 15)).toContain('65 min');
	});
});

describe('Soporte: the person chooses', () => {
	it('the choices', () => {
		expect([...TRANSITION_CHOICES]).toEqual([0, 5, 10, 15, 20, 25, 30, 45, 60]);
	});

	it('effective interval = duration + minutes, or the template', () => {
		expect(effectiveInterval(40, 30, 10)).toBe(50);
		expect(effectiveInterval(40, 30, 0)).toBe(40);
		expect(effectiveInterval(40, 30, null)).toBe(30);
	});

	it('the Disponibilidad helper', () => {
		expect(supportCadenceSentence(40, 50)).toBe(
			'Tus sesiones de 40 min empezarán cada 50 min: 9:00 a. m., 9:50 a. m., 10:40 a. m., 11:30 a. m.…'
		);
		expect(supportCadenceSentence(40, 40, 'Sus')).toBe(
			'Sus sesiones de 40 min empezarán cada 40 min: 9:00 a. m., 9:40 a. m., 10:20 a. m., 11:00 a. m.…'
		);
	});

	it('a chosen transition that fits the interval reads as before', () => {
		expect(supportCadenceSentence(40, 50, 'Tus', 10)).toBe(
			'Tus sesiones de 40 min empezarán cada 50 min: 9:00 a. m., 9:50 a. m., 10:40 a. m., 11:30 a. m.…'
		);
	});

	it('the template grid shorter than session + transition says when the next free start is', () => {
		// Production template: 40 min, every 30, 15 of transition → after a 9:00 booking, 10:00.
		expect(supportCadenceSentence(40, 30, 'Tus', 15)).toBe(
			'Tus sesiones de 40 min + 15 min de transición se ofrecen cada 30 min: 9:00 a. m., 9:30 a. m., 10:00 a. m., 10:30 a. m.… Tras cada reserva, el siguiente horario libre es 60 min después de su inicio.'
		);
		expect(supportCadenceSentence(40, 25, 'Sus', 15)).toContain('el siguiente horario libre es 75 min después');
	});
});
