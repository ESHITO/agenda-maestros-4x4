// Fork (owner, 30 Sep 2026): the Reservas cards were "too loaded" on a phone. Each booking
// now opens as a short card - client, when, type, a countdown for the person who attends and
// four tiny WhatsApp dots - and everything else waits behind "Ver detalles". The pure parts
// of that short card live here so they are tested: the dots summary and the compact date.

import type { Booking, WhatsAppNotice } from './api';
import { displayZone, dayKeyInZone, type UserPrefs } from './prefs';

export const NOTICE_KIND_LABELS: Record<WhatsAppNotice['kind'], string> = {
	created: 'Confirmación',
	morning: 'Mañana',
	'1h': '1 hora',
	'5m': '5 min'
};

export const NOTICE_STATUS_LABELS: Record<WhatsAppNotice['status'], string> = {
	sent: 'Enviado',
	pending: 'Pendiente',
	sending: 'Enviando…',
	failed: 'Falló',
	cancelled: 'Cancelado',
	missed: 'No salió',
	unknown: 'Sin registro',
	not_applicable: 'No aplica'
};

/** ok = sent; wait = pending or sending; bad = failed or missed; off = everything else. */
export type DotTone = 'ok' | 'wait' | 'bad' | 'off';

export function noticeTone(status: WhatsAppNotice['status'] | string): DotTone {
	switch (status) {
		case 'sent':
			return 'ok';
		case 'pending':
		case 'sending':
			return 'wait';
		case 'failed':
		case 'missed':
			return 'bad';
		default:
			return 'off';
	}
}

export type NoticeDots = {
	dots: { kind: WhatsAppNotice['kind']; tone: DotTone }[];
	/** For aria-label and title: the four notices and their state, in Spanish. */
	label: string;
};

/**
 * The compact WhatsApp summary of a booking, or null when there is nothing to tell: no
 * notices at all, or all of them "No aplica" (no webhook for this type).
 */
export function noticeDots(notices: WhatsAppNotice[] | undefined | null): NoticeDots | null {
	if (!notices || notices.length === 0) return null;
	if (notices.every((n) => n.status === 'not_applicable')) return null;
	const parts = notices.map(
		(n) =>
			`${NOTICE_KIND_LABELS[n.kind] ?? n.kind}: ${(NOTICE_STATUS_LABELS[n.status] ?? NOTICE_STATUS_LABELS.not_applicable).toLowerCase()}`
	);
	return {
		dots: notices.map((n) => ({ kind: n.kind, tone: noticeTone(n.status) })),
		label: `Avisos de WhatsApp. ${parts.join('; ')}.`
	};
}

function shiftDay(key: string, n: number): string {
	const [y, m, d] = key.split('-').map(Number);
	return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10);
}

/**
 * The card's short "when", in the profile's zone: "Hoy · 3:30 p. m.", "Mañana · 9:00 a. m.",
 * "Vie 2 oct · 3:30 p. m." (and the year when it is not this year's). The time follows the
 * profile's 12/24 h choice, written with hourCycle (Intl's hour12 may print "0:30 a. m.").
 */
export function fmtCardWhen(iso: string, p: UserPrefs, now: Date = new Date()): string {
	const d = new Date(iso);
	if (isNaN(d.getTime())) return '';
	const timeZone = displayZone(p);
	const time = new Intl.DateTimeFormat('es', {
		hour: 'numeric',
		minute: '2-digit',
		hourCycle: p.time_format === '24h' ? 'h23' : 'h12',
		timeZone
	}).format(d);

	const key = dayKeyInZone(d, p);
	const today = dayKeyInZone(now, p);
	let day: string;
	if (key === today) day = 'Hoy';
	else if (key === shiftDay(today, 1)) day = 'Mañana';
	else if (key === shiftDay(today, -1)) day = 'Ayer';
	else {
		const parts = new Intl.DateTimeFormat('es', {
			weekday: 'short',
			day: 'numeric',
			month: 'short',
			timeZone
		}).formatToParts(d);
		const get = (t: string) => (parts.find((x) => x.type === t)?.value ?? '').replace(/\.$/, '');
		const wd = get('weekday');
		day = `${wd.charAt(0).toUpperCase()}${wd.slice(1)} ${get('day')} ${get('month')}`;
		if (key.slice(0, 4) !== today.slice(0, 4)) day += ` ${key.slice(0, 4)}`;
	}
	return `${day} · ${time}`;
}

/**
 * The chips beside the client's name (owner, 30 Sep 2026: "al costado de su nombre diga
 * confirmado (color verde)", and the history tagged as cancelled, concluded or rescheduled).
 * One state chip - Cancelada, Concluida (a confirmed session whose end has passed) or
 * Confirmada - plus "Reprogramada" when the booking was ever moved, whatever its state.
 */
export type ChipKind = 'confirmed' | 'cancelled' | 'concluded' | 'rescheduled';
export type BookingChip = { kind: ChipKind; label: string; title?: string };

export function bookingChips(
	b: Pick<Booking, 'status' | 'end_at' | 'rescheduled'>,
	now: Date | number = Date.now()
): BookingChip[] {
	const t = typeof now === 'number' ? now : now.getTime();
	const chips: BookingChip[] = [];
	if (b.status === 'cancelled') chips.push({ kind: 'cancelled', label: 'Cancelada' });
	else if (new Date(b.end_at).getTime() < t) chips.push({ kind: 'concluded', label: 'Concluida' });
	else chips.push({ kind: 'confirmed', label: 'Confirmada' });
	const n = b.rescheduled?.count ?? 0;
	if (n > 0) {
		chips.push({ kind: 'rescheduled', label: 'Reprogramada', title: `Reprogramada ${n} ${n === 1 ? 'vez' : 'veces'}` });
	}
	return chips;
}

/** The "Pasadas" filter: '' = Todas. Each maps to the server's history sub-filter. */
export type HistoryFilter = '' | 'concluded' | 'cancelled' | 'rescheduled';

export const HISTORY_FILTER_LABELS: Record<HistoryFilter, string> = {
	'': 'Todas',
	concluded: 'Concluidas',
	cancelled: 'Canceladas',
	rescheduled: 'Reprogramadas'
};

/** The query parameters of a history filter (when=history is set by the caller). */
export function historyFilterParams(f: HistoryFilter): Record<string, string> {
	switch (f) {
		case 'concluded':
			return { status: 'confirmed' };
		case 'cancelled':
			return { status: 'cancelled' };
		case 'rescheduled':
			return { rescheduled: '1' };
		default:
			return {};
	}
}
