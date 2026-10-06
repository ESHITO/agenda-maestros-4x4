// Fork: the event type's WhatsApp tab with the host notices ("Avisos al mentor o soporte")
// at phone width. The host section has its own chips (they insert into ITS boxes), a host
// text using the client's link is flagged before saving, all nine texts go in one PUT, and
// the server-rendered example shows both audiences. The third host text, «Sesión cancelada»
// (host_cancelled), takes {motivo} - which no other host text may use.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const MOMENTS = ['created', 'reminder_morning', 'reminder_1h', 'reminder_5m', 'cancelled', 'rescheduled', 'host_created', 'host_reminder_5m', 'host_cancelled'];
const blank = () => Object.fromEntries(MOMENTS.map((m) => [m, '']));
const defaults = {
	...blank(),
	host_created: '*Nueva sesión agendada: {tipo}*',
	host_reminder_5m: '*FALTAN 5 MINUTOS:* {enlace_mentor}',
	host_cancelled: '🔴 *Sesión cancelada: {tipo}*\n*Motivo:* _{motivo}_'
};

const get = vi.fn(async (_path: string) => ({ ...blank(), defaults }));
const put = vi.fn(async (_path: string, body: Record<string, string>) => ({ ...blank(), ...body, defaults }));
const post = vi.fn(async (_path: string, _body: unknown) => ({
	...Object.fromEntries(MOMENTS.map((m) => [m, `texto de ${m}`])),
	timezone: 'America/Lima',
	has_text_question: true
}));

vi.mock('$lib/api', async (importOriginal) => {
	const mod = await importOriginal<typeof import('$lib/api')>();
	return { ...mod, api: { ...mod.api, get: (p: string) => get(p), put: (p: string, b: any) => put(p, b), post: (p: string, b: any) => post(p, b) } };
});

const { default: Panel } = await import('./WhatsAppMessagesPanel.svelte');

async function settle(check: () => void) {
	await expect.poll(() => {
		try {
			check();
			return true;
		} catch {
			return false;
		}
	}).toBe(true);
	check();
}

const button = (root: HTMLElement, text: string) =>
	[...root.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === text);

function setValue(el: HTMLTextAreaElement, value: string) {
	el.value = value;
	el.dispatchEvent(new Event('input', { bubbles: true }));
}

beforeEach(async () => {
	await page.viewport(375, 812);
	document.body.style.margin = '0';
	document.body.style.padding = '0 16px';
	get.mockClear();
	put.mockClear();
	post.mockClear();
});

afterEach(() => {
	cleanup();
	document.body.style.padding = '';
});

describe('WhatsAppMessagesPanel with the host notices, 375 px', () => {
	test('host section, its chips, the misuse check and one PUT for all texts', async () => {
		const { container } = await render(Panel, { slug: 'mentoria-privada' });
		await settle(() => expect(container.querySelector('#wa-host_created')).not.toBeNull());
		expect(container.textContent).toContain('Avisos al mentor o soporte');
		expect(container.querySelector<HTMLTextAreaElement>('#wa-host_created')!.placeholder).toContain('Nueva sesión agendada');
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);

		// The host chip goes into the host box focused last, not into a client box.
		const host5 = container.querySelector<HTMLTextAreaElement>('#wa-host_reminder_5m')!;
		host5.dispatchEvent(new Event('focus'));
		host5.focus();
		button(container, '{enlace_mentor}')!.click();
		await settle(() => expect(host5.value).toBe('{enlace_mentor}'));
		expect(container.querySelector<HTMLTextAreaElement>('#wa-created')!.value).toBe('');
		// {fecha_mentor} is offered only in the host section; {cancelar} only in the client one.
		expect([...container.querySelectorAll('button')].filter((b) => b.textContent?.trim() === '{fecha_mentor}')).toHaveLength(1);
		expect([...container.querySelectorAll('button')].filter((b) => b.textContent?.trim() === '{cancelar}')).toHaveLength(1);

		// A host text with the client's link: flagged, save disabled.
		const hostCreated = container.querySelector<HTMLTextAreaElement>('#wa-host_created')!;
		setValue(hostCreated, 'Entra: {enlace}');
		await settle(() => expect(container.textContent).toContain('En los avisos al anfitrión usa {enlace_mentor}'));
		expect(button(container, 'Guardar mensajes')!.disabled).toBe(true);

		setValue(hostCreated, '*Nueva Mentoría agendada*\n*Nombre:* {cliente}');
		await settle(() => expect(button(container, 'Guardar mensajes')!.disabled).toBe(false));
		button(container, 'Guardar mensajes')!.click();
		await settle(() => expect(put).toHaveBeenCalledTimes(1));
		const body = put.mock.calls[0][1];
		expect(Object.keys(body).sort()).toEqual([...MOMENTS].sort());
		expect(body.host_created).toBe('*Nueva Mentoría agendada*\n*Nombre:* {cliente}');
		expect(body.host_reminder_5m).toBe('{enlace_mentor}');
	});

	test('the third host text, «Sesión cancelada»: {motivo} goes in, the misuse check knows it', async () => {
		const { container } = await render(Panel, { slug: 'mentoria-privada' });
		await settle(() => expect(container.querySelector('#wa-host_cancelled')).not.toBeNull());
		expect(container.textContent).toContain('3. Sesión cancelada');
		expect(container.textContent).toContain('Cuando se cancela la sesión, con el motivo si lo escribieron.');
		const cancelled = container.querySelector<HTMLTextAreaElement>('#wa-host_cancelled')!;
		expect(cancelled.placeholder).toContain('Sesión cancelada');
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);

		// {motivo} is a chip in both sections now; the host one goes into the host box focused last.
		const motivoChips = [...container.querySelectorAll<HTMLButtonElement>('button')].filter((b) => b.textContent?.trim() === '{motivo}');
		expect(motivoChips).toHaveLength(2);
		cancelled.dispatchEvent(new Event('focus'));
		cancelled.focus();
		motivoChips[1].click();
		await settle(() => expect(cancelled.value).toBe('{motivo}'));
		expect(container.querySelector<HTMLTextAreaElement>('#wa-cancelled')!.value).toBe('');
		expect(container.textContent).not.toContain('{motivo} solo sirve en los mensajes de cancelación.');

		// {motivo} in another host text, and {enlace_mentor} in the cancellation: flagged.
		const hostCreated = container.querySelector<HTMLTextAreaElement>('#wa-host_created')!;
		setValue(hostCreated, 'Motivo: {motivo}');
		await settle(() => expect(container.textContent).toContain('{motivo} solo sirve en los mensajes de cancelación.'));
		expect(button(container, 'Guardar mensajes')!.disabled).toBe(true);
		setValue(hostCreated, '');
		setValue(cancelled, '🔴 Mentoría cancelada\nEntra: {enlace_mentor}');
		await settle(() => expect(container.textContent).toContain('{enlace_mentor} no sirve en el aviso de cancelación'));
		expect(button(container, 'Guardar mensajes')!.disabled).toBe(true);

		setValue(cancelled, '🔴 Mentoría cancelada\n*Motivo:* _{motivo}_\n{nombre} {telefono}\n{correo}');
		await settle(() => expect(button(container, 'Guardar mensajes')!.disabled).toBe(false));
		button(container, 'Guardar mensajes')!.click();
		await settle(() => expect(put).toHaveBeenCalledTimes(1));
		const body = put.mock.calls[0][1];
		expect(Object.keys(body).sort()).toEqual([...MOMENTS].sort());
		expect(body.host_cancelled).toBe('🔴 Mentoría cancelada\n*Motivo:* _{motivo}_\n{nombre} {telefono}\n{correo}');
	});

	test('a chip limited to some texts goes into its own text, never into a box that refuses it', async () => {
		const { container } = await render(Panel, { slug: 'mentoria-privada' });
		await settle(() => expect(container.querySelector('#wa-host_cancelled')).not.toBeNull());
		const ta = (k: string) => container.querySelector<HTMLTextAreaElement>(`#wa-${k}`)!;
		const chips = (k: string) => [...container.querySelectorAll<HTMLButtonElement>('button')].filter((b) => b.textContent?.trim() === k);

		// Nothing focused yet (the host section's default box is «1. Nueva sesión agendada»):
		// the host {motivo} goes into «Sesión cancelada», with no red error anywhere.
		chips('{motivo}')[1].click();
		await settle(() => expect(ta('host_cancelled').value).toBe('{motivo}'));
		expect(ta('host_created').value).toBe('');
		expect(container.textContent).not.toContain('solo sirve en los mensajes de cancelación');
		expect(button(container, 'Guardar mensajes')!.disabled).toBe(false);

		// With «Sesión cancelada» the host box in use, {enlace_mentor} (not for it) goes to the
		// first text that takes it, at its end.
		setValue(ta('host_created'), 'Hola');
		ta('host_cancelled').dispatchEvent(new Event('focus'));
		ta('host_cancelled').focus();
		chips('{enlace_mentor}')[0].click();
		await settle(() => expect(ta('host_created').value).toBe('Hola{enlace_mentor}'));
		expect(ta('host_cancelled').value).toBe('{motivo}');
		expect(container.textContent).not.toContain('{enlace_mentor} no sirve en el aviso de cancelación');

		// The client {motivo} likewise lands in «Cancelación», not in «Confirmación».
		chips('{motivo}')[0].click();
		await settle(() => expect(ta('cancelled').value).toBe('{motivo}'));
		expect(ta('created').value).toBe('');
		expect(container.textContent).not.toContain('solo sirve en los mensajes de cancelación');
	});

	test('the example shows the client and the host messages', async () => {
		const { container } = await render(Panel, { slug: 'mentoria-privada' });
		await settle(() => expect(button(container, 'Ver ejemplo')).toBeDefined());
		button(container, 'Ver ejemplo')!.click();
		await settle(() => expect(container.textContent).toContain('texto de host_reminder_5m'));
		expect(container.textContent).toContain('texto de host_cancelled');
		expect(container.textContent).toContain('texto de created');
		expect(container.textContent).toContain('Así lo recibiría quien atiende la sesión');
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);
	});
});
