// Reservas, mounted in a real browser at phone width (owner, 30 Sep 2026: "se ve muy
// cargado en el celular"). Each booking is a short card; e-mail, notices, answers and the
// actions wait behind "Ver detalles"; the countdown is only for the person who attends.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { page, userEvent } from 'vitest/browser';
import type { Booking, User } from '$lib/api';

const gets: string[] = [];
let listResponse: { items: Booking[]; total: number; counts: { upcoming: number; past: number } };

vi.mock('$lib/api', async (importOriginal) => {
	const mod = await importOriginal<typeof import('$lib/api')>();
	return {
		...mod,
		api: {
			...mod.api,
			get: async (path: string) => {
				gets.push(path);
				if (path.startsWith('/v1/bookings?')) return structuredClone(listResponse);
				if (path.endsWith('/answers')) return { items: [{ label: 'Tema', type: 'text', value: 'Finanzas' }] };
				if (path === '/v1/event-types') return { items: [] };
				if (path === '/v1/users') return [];
				if (path === '/v1/teams') return { items: [] };
				return {};
			}
		},
		teamApi: { ...mod.teamApi, getSettings: async () => ({}) }
	};
});

const { currentUser } = await import('$lib/stores');
const { prefs } = await import('$lib/prefs');
const { default: Page } = await import('../routes/bookings/+page.svelte');

const ME = { id: 'me', name: 'Mentor Uno', is_admin: true, timezone: 'America/Lima' } as unknown as User;

function booking(id: string, over: Partial<Booking>): Booking {
	const start = new Date(Date.now() + 5 * 3600_000 + 20 * 60_000 + 30_000);
	return {
		id,
		event_type_slug: 'mentoria-privada',
		event_type_name: 'Mentoría privada',
		start_at: start.toISOString(),
		end_at: new Date(start.getTime() + 3600_000).toISOString(),
		status: 'confirmed',
		attendees: [{ name: `Cliente ${id}`, email: `${id}@ejemplo.com` }],
		created_at: new Date(Date.now() - 86400_000).toISOString(),
		host_id: 'me',
		host_name: 'Mentor Uno',
		whatsapp: [
			{ kind: 'created', status: 'sent', at: new Date().toISOString() },
			{ kind: 'morning', status: 'not_applicable' },
			{ kind: '1h', status: 'pending', at: start.toISOString() },
			{ kind: '5m', status: 'pending', at: start.toISOString() }
		],
		...over
	};
}

beforeEach(() => {
	gets.length = 0;
	try { localStorage.removeItem('agenda.bookings.scope.me'); } catch { /* fine */ }
	currentUser.set(ME);
	prefs.set({ timezone: 'America/Lima', time_format: '12h', week_start: 1, date_format: 'dmy' });
	listResponse = {
		items: [
			booking('a', {}),
			booking('b', { host_id: 'other', host_name: 'Ana Beltrán' })
		],
		total: 2,
		counts: { upcoming: 2, past: 0 }
	};
});

afterEach(() => cleanup());

describe('Reservas: short cards on a phone', () => {
	test('asks the server for soonest first, and most recent first for past', async () => {
		await page.viewport(375, 812);
		render(Page);
		await vi.waitFor(() => expect(gets.some((g) => g.startsWith('/v1/bookings?'))).toBe(true));
		const first = new URLSearchParams(gets.find((g) => g.startsWith('/v1/bookings?'))!.split('?')[1]);
		expect(first.get('when')).toBe('upcoming');
		expect(first.get('order')).toBe('asc');

		await page.getByRole('button', { name: /Pasadas/ }).click();
		await vi.waitFor(() => {
			const last = new URLSearchParams(gets.filter((g) => g.startsWith('/v1/bookings?')).at(-1)!.split('?')[1]);
			// Pasadas is the history: ended + cancelled at any date (when=history).
			expect(last.get('when')).toBe('history');
			expect(last.get('order')).toBe('desc');
		});
	});

	test('collapsed: name, when, type, host; no e-mail and no actions', async () => {
		await page.viewport(375, 812);
		render(Page);
		await expect.element(page.getByText('Cliente a')).toBeVisible();
		expect(document.body.textContent).toContain('Mentoría privada');
		// "Todas las reservas" is the admin default: who attends is on the card.
		expect(document.body.textContent).toContain('con Ana Beltrán');
		// The viewer's own sessions read 'Contigo', not 'con Mentor Uno'.
		expect(document.body.textContent).toContain('Contigo');
		expect(document.body.textContent).not.toContain('con Mentor Uno');
		expect(document.body.textContent).not.toContain('a@ejemplo.com');
		expect(page.getByRole('button', { name: 'Cancelar reunión' }).elements()).toHaveLength(0);
		// Owner, 30 Sep 2026: beside the name, "Confirmada" in green.
		const chip = page.getByText('Confirmada').first();
		await expect.element(chip).toBeVisible();
		expect(chip.element().className).toContain('bg-green-100');
	});

	test('the countdown is only for the person who attends', async () => {
		await page.viewport(375, 812);
		render(Page);
		await expect.element(page.getByText('Cliente b')).toBeVisible();
		const pills = [...document.querySelectorAll('li')].map((li) => li.textContent ?? '');
		const a = pills.find((t) => t.includes('Cliente a'))!;
		const b = pills.find((t) => t.includes('Cliente b'))!;
		expect(a).toContain('Empieza en 5 h 20 min');
		expect(b).not.toContain('Empieza en');
	});

	test('WhatsApp dots carry a Spanish summary of the four notices', async () => {
		await page.viewport(375, 812);
		render(Page);
		await expect.element(page.getByText('Cliente a')).toBeVisible();
		const dots = document.querySelector('[role="img"][aria-label^="Avisos de WhatsApp"]');
		expect(dots?.getAttribute('aria-label')).toBe(
			'Avisos de WhatsApp. Confirmación: enviado; Mañana: no aplica; 1 hora: pendiente; 5 min: pendiente.'
		);
	});

	test('"Ver detalles" shows everything, and tapping the card works too', async () => {
		await page.viewport(375, 812);
		render(Page);
		await expect.element(page.getByText('Cliente a')).toBeVisible();
		const toggle = page.getByRole('button', { name: 'Ver detalles' }).first();
		await toggle.click();
		await expect.element(page.getByText('a@ejemplo.com')).toBeVisible();
		await expect.element(page.getByText('Finanzas')).toBeVisible();
		await expect.element(page.getByRole('button', { name: 'Cancelar reunión' })).toBeVisible();
		await expect.element(page.getByRole('button', { name: 'Reprogramar' })).toBeVisible();
		await expect.element(page.getByRole('button', { name: 'Pasar a otra persona' })).toBeVisible();
		expect(document.querySelector('[aria-label="Avisos de WhatsApp"]')).not.toBeNull();

		// Closing it again from the same button.
		await page.getByRole('button', { name: 'Ocultar detalles' }).click();
		expect(document.body.textContent).not.toContain('a@ejemplo.com');

		// A tap on the card's text opens it as well.
		await userEvent.click(page.getByText('Cliente b'));
		await expect.element(page.getByText('b@ejemplo.com')).toBeVisible();
		// Not the host: no Reprogramar (the server would 404), but pass and cancel stay.
		expect(page.getByRole('button', { name: 'Reprogramar' }).elements()).toHaveLength(0);
		await expect.element(page.getByRole('button', { name: 'Pasar a otra persona' })).toBeVisible();
	});

	test('filters fold behind "Filtros" on a phone and sit inline from md', async () => {
		await page.viewport(375, 812);
		render(Page);
		await expect.element(page.getByText('Cliente a')).toBeVisible();
		const panel = document.getElementById('booking-filters')!;
		expect(getComputedStyle(panel).display).toBe('none');
		const btn = page.getByRole('button', { name: 'Filtros' });
		await btn.click();
		expect(getComputedStyle(panel).display).toBe('grid');
		await expect.element(btn).toHaveAttribute('aria-expanded', 'true');

		await page.viewport(1280, 900);
		await vi.waitFor(() => expect(getComputedStyle(panel).display).toBe('flex'));
		// Hidden from md, so it is no longer reachable by role: ask the DOM.
		const btnEl = document.querySelector('[aria-controls="booking-filters"]')!;
		await vi.waitFor(() => expect(getComputedStyle(btnEl).display).toBe('none'));
	});
});

describe('Reservas: generic picture, state chips and the history', () => {
	test('every card has the same generic picture, never a photo', async () => {
		await page.viewport(375, 812);
		render(Page);
		await expect.element(page.getByText('Cliente a')).toBeVisible();
		const avatars = [...document.querySelectorAll('ul > li [data-slot="avatar"].size-10')];
		expect(avatars).toHaveLength(2);
		for (const a of avatars) {
			expect(a.querySelector('img')).toBeNull();
			expect(a.querySelector('svg')).not.toBeNull();
			const r = a.getBoundingClientRect();
			expect(Math.round(r.width)).toBe(40);
			expect(Math.round(r.height)).toBe(40);
		}
	});

	test('Pasadas: concluded, cancelled and rescheduled chips, and "Reprogramada desde"', async () => {
		await page.viewport(375, 812);
		const past = new Date(Date.now() - 3 * 3600_000);
		listResponse = {
			items: [
				booking('c', {
					start_at: past.toISOString(),
					end_at: new Date(past.getTime() + 3600_000).toISOString(),
					rescheduled: { count: 2, last_previous_start_at: '2026-09-01T15:00:00Z' }
				}),
				booking('x', { status: 'cancelled' })
			],
			total: 2,
			counts: { upcoming: 0, past: 2 }
		};
		render(Page);
		await expect.element(page.getByText('Cliente c')).toBeVisible();
		const cards = [...document.querySelectorAll('ul > li')].map((li) => li.textContent ?? '');
		const c = cards.find((t) => t.includes('Cliente c'))!;
		const x = cards.find((t) => t.includes('Cliente x'))!;
		expect(c).toContain('Concluida');
		expect(c).toContain('Reprogramada');
		expect(c).not.toContain('Confirmada');
		expect(x).toContain('Cancelada');
		const moved = page.getByText('Reprogramada', { exact: true }).first();
		expect(moved.element().getAttribute('title')).toBe('Reprogramada 2 veces');

		await userEvent.click(page.getByText('Cliente c'));
		await expect.element(page.getByText(/^Reprogramada desde /)).toBeVisible();
		expect(document.body.textContent).toContain('2 veces');
	});

	test('the history filter lives on Pasadas only and asks the server', async () => {
		await page.viewport(1280, 900);
		render(Page);
		await expect.element(page.getByText('Cliente a')).toBeVisible();
		expect(document.querySelector('[aria-label="Filtrar el historial"]')).toBeNull();

		await page.getByRole('button', { name: /Pasadas/ }).click();
		const trigger = () => document.querySelector<HTMLElement>('[aria-label="Filtrar el historial"]')!;
		await vi.waitFor(() => expect(trigger()).not.toBeNull());
		expect(trigger().textContent?.trim()).toBe('Todas');
		await userEvent.click(trigger());
		for (const label of ['Todas', 'Concluidas', 'Canceladas', 'Reprogramadas']) {
			await expect.element(page.getByRole('option', { name: label, exact: true })).toBeVisible();
		}
		await page.getByRole('option', { name: 'Canceladas', exact: true }).click();
		await vi.waitFor(() => {
			const last = new URLSearchParams(gets.filter((g) => g.startsWith('/v1/bookings?')).at(-1)!.split('?')[1]);
			expect(last.get('when')).toBe('history');
			expect(last.get('status')).toBe('cancelled');
		});

		await userEvent.click(trigger());
		await page.getByRole('option', { name: 'Reprogramadas', exact: true }).click();
		await vi.waitFor(() => {
			const last = new URLSearchParams(gets.filter((g) => g.startsWith('/v1/bookings?')).at(-1)!.split('?')[1]);
			expect(last.get('rescheduled')).toBe('1');
			expect(last.get('status')).toBeNull();
		});

		// Back on Próximas the filter is gone and nothing of it is sent.
		await page.getByRole('button', { name: /Próximas/ }).click();
		await vi.waitFor(() => {
			const last = new URLSearchParams(gets.filter((g) => g.startsWith('/v1/bookings?')).at(-1)!.split('?')[1]);
			expect(last.get('when')).toBe('upcoming');
			expect(last.get('rescheduled')).toBeNull();
			expect(last.get('status')).toBeNull();
		});
		expect(document.querySelector('[aria-label="Filtrar el historial"]')).toBeNull();
	});
});
