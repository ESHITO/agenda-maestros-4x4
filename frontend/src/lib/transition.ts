// Fork (Agenda Maestros 4x4): the transition time between sessions, as the admin UI
// explains it (owner decision, 30 Sep 2026).
//
// "Transition" = buffer_after_minutes: dead time after each session before the next one.
// "Empezar una sesión cada" = slot_interval_minutes: how often a booking may start. For
// Mentoría the owner sets both on the template (the event-type editor); for Soporte each
// person picks their transition in Disponibilidad and their copy then starts a session
// every duration + transition minutes (GET/PUT /v1/users/me/transition).
//
// Example starts are counted from 9:00 and printed the way every client-facing time in
// this fork is ("9:00 a. m.", "12:30 p. m."), never past the end of the day.

import { time12 } from './team-availability';

/** The minutes a support person may choose (the server refuses anything else). */
export const TRANSITION_CHOICES = [0, 5, 10, 15, 20, 25, 30, 45, 60] as const;

/** Where the examples start: 9:00. */
const EXAMPLE_FROM = 9 * 60;
const DAY = 24 * 60;

const valid = (n: number) => Number.isFinite(n) && n >= 1;

/** Up to `count` starts from 9:00, every `interval` minutes, as "9:00 a. m.". Empty when
 *  the interval is not a positive number. */
export function exampleStarts(interval: number, count = 4, from = EXAMPLE_FROM): string[] {
	const step = Math.floor(Number(interval));
	if (!valid(step)) return [];
	const out: string[] = [];
	for (let t = from; t < DAY && out.length < count; t += step) {
		out.push(time12(Math.floor(t / 60), t % 60));
	}
	return out;
}

/** "9:00 a. m., 10:00 a. m., 11:00 a. m., 12:00 p. m.…" (the ellipsis only when the day
 *  goes on). */
export function startsList(interval: number, count = 4): string {
	const s = exampleStarts(interval, count);
	if (s.length === 0) return '';
	const more = EXAMPLE_FROM + Math.floor(Number(interval)) * s.length < DAY;
	return s.join(', ') + (more ? '…' : '');
}

const mins = (n: number) => `${n} min`;

/**
 * The editor's live sentence for any event type:
 * "Sesiones de 40 min + 10 min de transición. Los clientes podrán reservar a las 9:00 a. m., 10:00 a. m., …"
 */
export function transitionSentence(duration: number, bufferAfter: number, interval: number): string {
	const d = Math.max(0, Math.floor(Number(duration) || 0));
	const b = Math.max(0, Math.floor(Number(bufferAfter) || 0));
	const head = b > 0 ? `Sesiones de ${mins(d)} + ${mins(b)} de transición.` : `Sesiones de ${mins(d)} sin tiempo de transición.`;
	const list = startsList(interval);
	return list ? `${head} Los clientes podrán reservar a las ${list}` : head;
}

/**
 * What one session needs before the next start: its duration + the larger of its two
 * margins. The slot engine keeps max(before, after) on each side of a booked session
 * (internal/slots, BufferMargin), so the two margins overlap - they never add up.
 */
export function sessionSpan(duration: number, bufferAfter: number, bufferBefore = 0): number {
	return (Number(duration) || 0) + Math.max(Number(bufferAfter) || 0, Number(bufferBefore) || 0);
}

/**
 * The warning when back-to-back starts cannot all be booked: a session plus its transition
 * is longer than the interval, so a booking hides the next start(s). '' when it fits.
 */
export function cadenceWarning(duration: number, bufferAfter: number, interval: number, bufferBefore = 0): string {
	const need = sessionSpan(duration, bufferAfter, bufferBefore);
	const step = Number(interval);
	if (!valid(step) || step >= need) return '';
	return `Cada sesión ocupa ${mins(need)} con su transición, más que el intervalo de ${mins(step)}: algunos horarios no se podrán reservar uno detrás de otro.`;
}

/** The note for "Una sesión por hora" when a session and its transition do not fit in an
 *  hour ('' when they do). */
export function hourlyWarning(duration: number, bufferAfter: number, bufferBefore = 0): string {
	const need = sessionSpan(duration, bufferAfter, bufferBefore);
	if (need <= 60) return '';
	return `La sesión con su transición dura ${mins(need)}, más de una hora: después de cada reserva se saltará el horario siguiente.`;
}

/** Disponibilidad (Soporte): "Tus sesiones de 40 min empezarán cada 50 min: 9:00 a. m., 9:50 a. m., …".
 *  Miembros says it of someone else: subject "Sus".
 *
 *  `transition` (the minutes in force, the template's when the person chose none) matters
 *  only when the interval is shorter than the session + transition - the template's own
 *  grid can be (production: 40 min, every 30, 15 of transition). Then the starts listed are
 *  not all bookable one after the other, and the sentence says when the next free one is:
 *  "Tus sesiones de 40 min + 15 min de transición se ofrecen cada 30 min: 9:00 a. m., …
 *  Tras cada reserva, el siguiente horario libre es 60 min después de su inicio." */
export function supportCadenceSentence(
	duration: number,
	interval: number,
	subject: 'Tus' | 'Sus' = 'Tus',
	transition: number | null = null
): string {
	const d = Math.floor(Number(duration) || 0);
	const step = Math.floor(Number(interval) || 0);
	const b = Math.max(0, Math.floor(Number(transition) || 0));
	const list = startsList(interval);
	if (transition !== null && valid(step) && step < d + b) {
		const head = `${subject} sesiones de ${mins(d)} + ${mins(b)} de transición se ofrecen cada ${mins(step)}`;
		const next = Math.ceil((d + b) / step) * step;
		const tail = `Tras cada reserva, el siguiente horario libre es ${mins(next)} después de su inicio.`;
		return list ? `${head}: ${list} ${tail}` : `${head}. ${tail}`;
	}
	const head = `${subject} sesiones de ${mins(d)} empezarán cada ${mins(step)}`;
	return list ? `${head}: ${list}` : `${head}.`;
}

/** The interval a choice produces on a support person's copy (null = the template's). */
export function effectiveInterval(duration: number, templateInterval: number, minutes: number | null): number {
	if (minutes === null) return templateInterval;
	return Math.max(1, duration + minutes);
}
