// Fork: the event type's WhatsApp tab with the host notices ("Avisos al mentor o soporte")
// at phone width. The host section has its own chips (they insert into ITS boxes), a host
// text using the client's link is flagged before saving, all eight texts go in one PUT, and
// the server-rendered example shows both audiences.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const MOMENTS = ['created', 'reminder_morning', 'reminder_1h', 'reminder_5m', 'cancelled', 'rescheduled', 'host_created', 'host_reminder_5m'];
const blank = () => Object.fromEntries(MOMENTS.map((m) => [m, '']));
const defaults = { ...blank(), host_created: '*Nueva sesión agendada: {tipo}*', host_reminder_5m: '*FALTAN 5 MINUTOS:* {enlace_mentor}' };

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

	test('the example shows the client and the host messages', async () => {
		const { container } = await render(Panel, { slug: 'mentoria-privada' });
		await settle(() => expect(button(container, 'Ver ejemplo')).toBeDefined());
		button(container, 'Ver ejemplo')!.click();
		await settle(() => expect(container.textContent).toContain('texto de host_reminder_5m'));
		expect(container.textContent).toContain('texto de created');
		expect(container.textContent).toContain('Así lo recibiría quien atiende la sesión');
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);
	});
});
