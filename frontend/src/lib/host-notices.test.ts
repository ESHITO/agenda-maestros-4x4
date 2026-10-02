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

	it('a host text may not use the client links nor the reason', () => {
		const why = 'En los avisos al anfitrión usa {enlace_mentor}; {enlace} y {cancelar} son del cliente.';
		expect(markerMisuse('host_reminder_5m', 'ENTRA AHORA: {enlace}')).toBe(why);
		expect(markerMisuse('host_created', 'Cancelar: {cancelar}')).toBe(why);
		// {motivo} is named, not lumped with the links (and {enlace_mentor} is no answer to it).
		expect(markerMisuse('host_created', 'Motivo: {motivo}')).toBe('{motivo} es solo del mensaje de cancelación al cliente.');
		expect(markerMisuse('host_created', '{ Motivo } y {enlace}')).toBe(why);
		expect(markerMisuse('host_reminder_5m', 'ENTRA AHORA: {enlace_mentor}')).toBe('');
	});

	it('every chip offered in a section is allowed in that section', () => {
		for (const mk of CLIENT_MARKERS) for (const m of CLIENT_MOMENTS) expect(markerMisuse(m, mk.key)).toBe('');
		for (const mk of HOST_MARKERS) for (const m of HOST_MOMENTS) expect(markerMisuse(m, mk.key)).toBe('');
	});

	it('the host chips offer {fecha_mentor}, not {fecha}/{dia}/{hora} nor the client links', () => {
		const keys = HOST_MARKERS.map((m) => m.key);
		expect(keys).toContain('{fecha_mentor}');
		expect(keys).toContain('{pais_mentor}');
		for (const k of ['{fecha}', '{dia}', '{hora}', '{enlace}', '{cancelar}', '{motivo}']) expect(keys).not.toContain(k);
	});
});
