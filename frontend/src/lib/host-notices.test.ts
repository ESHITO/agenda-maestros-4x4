import { describe, expect, it } from 'vitest';
import {
	CLIENT_MARKERS,
	CLIENT_MOMENTS,
	HOST_EVENTS,
	HOST_MARKERS,
	HOST_MOMENTS,
	isHostEvent,
	isHostMoment,
	markerMisuse,
	mixesHostEvents,
	usesMarker
} from './host-notices';

describe('moments and events', () => {
	it('host moments are told apart from the client ones', () => {
		for (const m of HOST_MOMENTS) expect(isHostMoment(m)).toBe(true);
		for (const m of CLIENT_MOMENTS) expect(isHostMoment(m)).toBe(false);
	});

	it('host events', () => {
		for (const e of HOST_EVENTS) expect(isHostEvent(e)).toBe(true);
		expect(isHostEvent('booking.created')).toBe(false);
		expect(isHostEvent('booking.reminder_5m')).toBe(false);
	});

	it('a webhook may not mix a host notice with other events', () => {
		expect(mixesHostEvents(['booking.host_created'])).toBe(false);
		expect(mixesHostEvents(['booking.host_created', 'booking.host_reminder_5m'])).toBe(false);
		expect(mixesHostEvents(['booking.host_created', 'booking.host_reminder_5m', 'booking.host_cancelled'])).toBe(false);
		expect(mixesHostEvents(['booking.cancelled', 'booking.host_cancelled'])).toBe(true);
		expect(mixesHostEvents(['booking.created', 'booking.reminder_5m'])).toBe(false);
		expect(mixesHostEvents(['booking.created', 'booking.host_created'])).toBe(true);
		expect(mixesHostEvents([])).toBe(false);
	});
});

describe('usesMarker (markerRE of fork_whatsapp.go)', () => {
	it('case and inner spaces tolerated', () => {
		expect(usesMarker('Entra: { Enlace_Mentor }', 'enlace_mentor')).toBe(true);
		expect(usesMarker('Entra: {enlace}', 'enlace_mentor')).toBe(false);
		expect(usesMarker('', 'enlace')).toBe(false);
	});
});

describe('markerMisuse mirrors webhook.WhatsAppMarkerMisuse', () => {
	it('a client text may not use the host link', () => {
		expect(markerMisuse('reminder_5m', 'Entra: {enlace_mentor}')).toBe('{enlace_mentor} solo sirve en los avisos al anfitrión.');
		expect(markerMisuse('created', 'Entra: {enlace}')).toBe('');
	});

	it('a host text may not use the client links; {motivo} only in a cancellation', () => {
		const why = 'En los avisos al anfitrión usa {enlace_mentor}; {enlace} y {cancelar} son del cliente.';
		const motivo = '{motivo} solo sirve en los mensajes de cancelación.';
		expect(markerMisuse('host_reminder_5m', 'ENTRA AHORA: {enlace}')).toBe(why);
		expect(markerMisuse('host_created', 'Cancelar: {cancelar}')).toBe(why);
		// {motivo} is named, not lumped with the links (and {enlace_mentor} is no answer to it).
		expect(markerMisuse('host_created', 'Motivo: {motivo}')).toBe(motivo);
		expect(markerMisuse('host_created', '{ Motivo } y {enlace}')).toBe(why);
		expect(markerMisuse('host_reminder_5m', 'ENTRA AHORA: {enlace_mentor}')).toBe('');
		// The client's texts: {motivo} only in «Cancelación».
		expect(markerMisuse('created', 'Motivo: {motivo}')).toBe(motivo);
		expect(markerMisuse('reminder_1h', '{ MOTIVO }')).toBe(motivo);
		expect(markerMisuse('cancelled', 'Motivo: {motivo}')).toBe('');
	});

	it('the host cancellation: {motivo} yes, no way in, no client links', () => {
		expect(markerMisuse('host_cancelled', '🔴 Mentoría cancelada\n*Motivo:* _{motivo}_\n{nombre} {telefono}\n{correo}')).toBe('');
		expect(markerMisuse('host_cancelled', '{fecha_mentor} - {pais_mentor} - {tipo}')).toBe('');
		expect(markerMisuse('host_cancelled', 'Entra: {enlace_mentor}')).toBe('{enlace_mentor} no sirve en el aviso de cancelación: esa sesión ya no se hará.');
		// Never points to {enlace_mentor}, which this same text refuses.
		expect(markerMisuse('host_cancelled', 'Cliente: {cancelar}')).toBe('{enlace} y {cancelar} son del cliente; en el aviso de cancelación no hace falta ningún enlace.');
		expect(markerMisuse('host_cancelled', 'Entra: {enlace}')).toBe('{enlace} y {cancelar} son del cliente; en el aviso de cancelación no hace falta ningún enlace.');
		expect(markerMisuse('host_cancelled', '{enlace} {enlace_mentor}')).toBe('{enlace} y {cancelar} son del cliente; en el aviso de cancelación no hace falta ningún enlace.');
	});

	it('every chip offered in a section is allowed where it says, and refused elsewhere', () => {
		const sections: [typeof CLIENT_MARKERS, readonly string[]][] = [
			[CLIENT_MARKERS, CLIENT_MOMENTS],
			[HOST_MARKERS, HOST_MOMENTS]
		];
		for (const [markers, moments] of sections) {
			for (const mk of markers) {
				for (const m of moments) {
					const allowed = !mk.moments || mk.moments.includes(m);
					expect(markerMisuse(m, mk.key) === '', `${mk.key} in ${m}`).toBe(allowed);
				}
			}
		}
	});

	it('the host chips offer {fecha_mentor} and {motivo}, not {fecha}/{dia}/{hora} nor the client links', () => {
		const keys = HOST_MARKERS.map((m) => m.key);
		expect(keys).toContain('{fecha_mentor}');
		expect(keys).toContain('{pais_mentor}');
		expect(keys).toContain('{motivo}');
		expect(HOST_MARKERS.find((m) => m.key === '{motivo}')?.moments).toEqual(['host_cancelled']);
		for (const k of ['{fecha}', '{dia}', '{hora}', '{enlace}', '{cancelar}']) expect(keys).not.toContain(k);
	});
});
