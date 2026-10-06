// Fork (Agenda Maestros 4x4): the WhatsApp notices to the HOST - the mentor or support
// person attending the session (internal/webhook/fork_host.go). Three moments, each its own
// event (FunnelChat cannot branch on "event"; the three share one recipient, so they may
// share a webhook - never with a client event):
//
//   booking.host_created      → moment host_created      "nueva sesión agendada"
//   booking.host_reminder_5m  → moment host_reminder_5m  "faltan 5 minutos" + the way in
//   booking.host_cancelled    → moment host_cancelled    "sesión cancelada" + {motivo}
//
// Their texts live next to the client's six in the event type's WhatsApp tab and are saved
// with the same per-moment API; the server renders them (RenderWhatsAppMoment), never the
// panel. What is here is the panel's vocabulary plus a mirror of the server's save check
// (webhook.WhatsAppMarkerMisuse), so the editor can warn before the 400. Pure; tested in
// host-notices.test.ts.

/** The client's six moments, in the panel's order. */
export const CLIENT_MOMENTS = ['created', 'reminder_morning', 'reminder_1h', 'reminder_5m', 'cancelled', 'rescheduled'] as const;
/** The host's three moments. */
export const HOST_MOMENTS = ['host_created', 'host_reminder_5m', 'host_cancelled'] as const;

export type ClientMoment = (typeof CLIENT_MOMENTS)[number];
export type HostMoment = (typeof HOST_MOMENTS)[number];

export function isHostMoment(m: string): m is HostMoment {
	return (HOST_MOMENTS as readonly string[]).includes(m);
}

/** The three webhook events of the host notices (keys of the backend's validWebhookEvents). */
export const HOST_EVENTS = ['booking.host_created', 'booking.host_reminder_5m', 'booking.host_cancelled'] as const;

export function isHostEvent(e: string): boolean {
	return e.startsWith('booking.host_');
}

/** A webhook's events may not mix a host notice with anything else (the server's
 *  TeamWebhookGuard answers 400): a FunnelChat flow has ONE recipient. */
export function mixesHostEvents(events: string[]): boolean {
	return events.some(isHostEvent) && events.some((e) => !isHostEvent(e));
}

/** A marker chip. `moments`, when set, are the only texts of its section it may be used in
 *  (markerMisuse refuses it elsewhere); the help says so in words, and the panel's chip
 *  inserts into the first of them when the box last focused is another one. */
export type MarkerDef = { key: string; help: string; moments?: readonly string[] };

/** The two cancellation texts, the only ones {motivo} fills (webhook IsCancellationMoment). */
export const CANCELLATION_MOMENTS = ['cancelled', 'host_cancelled'] as const;

/** Markers of the client's texts. */
export const CLIENT_MARKERS: MarkerDef[] = [
	{ key: '{nombre}', help: 'Nombre del cliente' },
	{ key: '{mentor}', help: 'Quién atiende la sesión' },
	{ key: '{tipo}', help: 'Nombre de este tipo de atención' },
	{ key: '{tema}', help: 'Lo que el cliente respondió en la primera pregunta de texto (por ejemplo «¿Qué te gustaría hablar en esta sesión?»)' },
	{ key: '{fecha}', help: 'Fecha y hora completas, en la hora del cliente: «martes 30 de septiembre de 2026, 10:00 a. m.»' },
	{ key: '{dia}', help: 'Día en palabras, en la hora del cliente: «martes 30 de septiembre»' },
	{ key: '{hora}', help: 'Solo la hora, en la hora del cliente: «10:00 a. m.»' },
	{ key: '{enlace}', help: 'Enlace para que el cliente entre a la sesión' },
	{ key: '{cancelar}', help: 'Enlace para que el cliente cancele o cambie la fecha' },
	{ key: '{motivo}', help: 'Motivo de la cancelación (solo en «Cancelación»)', moments: ['cancelled'] }
];

/** Markers of the host's texts. {fecha}, {dia} and {hora} are not offered: in a host text the
 *  server fills them in the HOST's zone too (webhook HostDateValues), so a typed one is safe,
 *  but {fecha_mentor} + {pais_mentor} also say whose time it is. */
export const HOST_MARKERS: MarkerDef[] = [
	{ key: '{cliente}', help: 'Nombre completo del cliente y su país: «María Pérez (Perú 🇵🇪)»' },
	{ key: '{nombre}', help: 'Nombre completo del cliente, sin el país' },
	{ key: '{nombre_corto}', help: 'Primer nombre y primer apellido del cliente: «María Pérez»' },
	{ key: '{pais_cliente}', help: 'País del cliente con su bandera: «Perú 🇵🇪» (por su número o su zona horaria)' },
	{ key: '{correo}', help: 'Correo electrónico del cliente' },
	{ key: '{telefono}', help: 'Número del cliente con código de país: «+51987654321»' },
	{ key: '{tema}', help: 'Lo que el cliente respondió en la primera pregunta de texto' },
	{ key: '{tipo}', help: 'Nombre de este tipo de atención' },
	{ key: '{mentor}', help: 'Nombre de quien atiende (quien recibe el aviso)' },
	{ key: '{fecha_mentor}', help: 'Día y hora en la hora LOCAL de quien atiende: «viernes 2 de octubre, 3:30 p. m.»' },
	{ key: '{pais_mentor}', help: 'De qué país es esa hora, con su bandera: «Perú 🇵🇪» (con la ciudad si su país tiene varias horas)' },
	{ key: '{enlace_mentor}', help: 'Enlace para que quien atiende entre a la sesión como anfitrión (no en «Sesión cancelada»)', moments: ['host_created', 'host_reminder_5m'] },
	{ key: '{motivo}', help: 'Motivo de la cancelación, si lo escribieron (solo en «Sesión cancelada»)', moments: ['host_cancelled'] }
];

/** markerRE of fork_whatsapp.go: "{nombre}", tolerating case and inner spaces. */
const MARKER_RE = /\{\s*([\p{L}_]+)\s*\}/gu;

/** Whether text uses the marker `name` ("cancelar" matches "{ Cancelar }"). */
export function usesMarker(text: string, name: string): boolean {
	for (const m of String(text ?? '').matchAll(MARKER_RE)) {
		if (m[1].toLowerCase() === name) return true;
	}
	return false;
}

/**
 * Why `text` cannot be saved as the text of `moment` (the server's message, word for word -
 * webhook.WhatsAppMarkerMisuse), or '': a client text may not use {enlace_mentor} (the host's
 * link), a host text may not use the client's {enlace} or {cancelar}, {motivo} belongs only to
 * the two cancellation texts (cancelled, host_cancelled), and the host's cancellation may not
 * use {enlace_mentor} (no session left to enter). The server renders them empty there anyway
 * (and so drops their line); this says so before saving.
 */
export function markerMisuse(moment: string, text: string): string {
	if (isHostMoment(moment)) {
		if (['enlace', 'cancelar'].some((n) => usesMarker(text, n))) {
			// {enlace_mentor} is refused in host_cancelled too: do not point to it there.
			if (moment === 'host_cancelled') return '{enlace} y {cancelar} son del cliente; en el aviso de cancelación no hace falta ningún enlace.';
			return 'En los avisos al anfitrión usa {enlace_mentor}; {enlace} y {cancelar} son del cliente.';
		}
	} else if (usesMarker(text, 'enlace_mentor')) {
		return '{enlace_mentor} solo sirve en los avisos al anfitrión.';
	}
	if (!(CANCELLATION_MOMENTS as readonly string[]).includes(moment) && usesMarker(text, 'motivo')) {
		return '{motivo} solo sirve en los mensajes de cancelación.';
	}
	if (moment === 'host_cancelled' && usesMarker(text, 'enlace_mentor')) {
		return '{enlace_mentor} no sirve en el aviso de cancelación: esa sesión ya no se hará.';
	}
	return '';
}
