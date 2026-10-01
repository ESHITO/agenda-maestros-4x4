// Disponibilidad, mounted in a real browser (owner report 30 Sep 2026: a mentor had to
// reload after every change to see it). The API is faked with promises the test settles
// by hand, so "while the first save is still in flight" is exact, not a race.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { page, userEvent } from 'vitest/browser';

type Call = { method: string; path: string; body: any; resolve: (v?: any) => void; reject: (e: unknown) => void };
const calls: Call[] = [];

function fake(method: string) {
	return (path: string, body?: unknown) =>
		new Promise((resolve, reject) => {
			calls.push({ method, path, body, resolve, reject });
		});
}

// Fork: the Soporte "Tiempo entre sesiones" card (teamApi, not the faked api object). By
// default it does not apply, so the page shows no extra select.
const { NO_TRANSITION, transitionApi } = vi.hoisted(() => {
	const NO_TRANSITION = {
		user_id: 'me', applies: false, minutes: null as number | null, template_minutes: 15,
		template_interval: 30, duration: 40, interval_effective: 30
	};
	const transitionApi = {
		myTransition: vi.fn(async () => ({ ...NO_TRANSITION })),
		putMyTransition: vi.fn(async (minutes: number | null) => ({
			...NO_TRANSITION, applies: true, minutes, interval_effective: minutes === null ? 30 : 40 + minutes
		}))
	};
	return { NO_TRANSITION, transitionApi };
});

vi.mock('$lib/api', async (importOriginal) => {
	const mod = await importOriginal<typeof import('$lib/api')>();
	return {
		...mod,
		api: { ...mod.api, get: fake('GET'), post: fake('POST'), patch: fake('PATCH'), del: fake('DELETE') },
		teamApi: { ...mod.teamApi, ...transitionApi }
	};
});

const { default: Page } = await import('../routes/availability/+page.svelte');

const of = (method: string, path?: string) => calls.filter((c) => c.method === method && (!path || c.path.startsWith(path)));
const triggers = () => [...document.querySelectorAll<HTMLButtonElement>('[data-slot="select-trigger"]')];

async function pick(trigger: HTMLElement, label: string) {
	await userEvent.click(trigger);
	await page.getByRole('option', { name: label, exact: true }).click();
}

// Mounts the page with one Monday block 09:00-17:00 and no overrides.
async function mountWithMondayBlock() {
	render(Page);
	// Rules and overrides are asked for at once.
	await vi.waitFor(() => expect(of('GET')).toHaveLength(2));
	of('GET', '/v1/availability-rules')[0].resolve({
		items: [{ id: 'r1', event_type_id: null, day_of_week: 1, start_time: '09:00', end_time: '17:00' }]
	});
	of('GET', '/v1/availability-overrides')[0].resolve({ items: [] });
	await vi.waitFor(() => expect(triggers().length).toBeGreaterThanOrEqual(2));
}

beforeEach(() => {
	calls.length = 0;
	transitionApi.myTransition.mockImplementation(async () => ({ ...NO_TRANSITION }));
	transitionApi.putMyTransition.mockClear();
});

afterEach(() => {
	cleanup();
});

describe('Disponibilidad: saving never blocks and never drops a change', () => {
	test('a second change while the first is saving is kept and sent after it, the latest wins', async () => {
		await mountWithMondayBlock();
		await pick(triggers()[0], '10am');
		await vi.waitFor(() => expect(of('PATCH')).toHaveLength(1));
		expect(of('PATCH')[0].body).toEqual({ start_time: '10:00', end_time: '17:00' });
		await expect.element(page.getByText('Guardando…')).toBeVisible();

		// Still editable while that PATCH is in flight.
		expect(triggers()[0].disabled).toBe(false);
		await pick(triggers()[0], '11am');
		// A start after the end moves the end one hour later instead of being refused.
		await pick(triggers()[0], '6pm');
		expect(triggers()[0].textContent).toContain('6pm');
		expect(triggers()[1].textContent).toContain('7pm');
		expect(of('PATCH')).toHaveLength(1);

		of('PATCH')[0].resolve({});
		await vi.waitFor(() => expect(of('PATCH')).toHaveLength(2));
		// Only the latest values; the intermediate 11am never goes out.
		expect(of('PATCH')[1].body).toEqual({ start_time: '18:00', end_time: '19:00' });
		of('PATCH')[1].resolve({});
		await expect.element(page.getByText('Guardado ✓')).toBeVisible();
	});

	test('a failed save shows the error with Reintentar and re-syncs from the server', async () => {
		await mountWithMondayBlock();
		await pick(triggers()[1], '8pm');
		await vi.waitFor(() => expect(of('PATCH')).toHaveLength(1));
		of('PATCH')[0].reject(new Error('Sin conexión'));
		await expect.element(page.getByText('Sin conexión')).toBeVisible();
		await vi.waitFor(() => expect(of('GET', '/v1/availability-rules')).toHaveLength(2));
		of('GET', '/v1/availability-rules')[1].resolve({
			items: [{ id: 'r1', event_type_id: null, day_of_week: 1, start_time: '09:00', end_time: '17:00' }]
		});
		await vi.waitFor(() => expect(triggers()[1].textContent).toContain('5pm'));

		await page.getByRole('button', { name: 'Reintentar' }).click();
		await vi.waitFor(() => expect(of('PATCH')).toHaveLength(2));
		expect(of('PATCH')[1].body).toEqual({ start_time: '09:00', end_time: '20:00' });
		expect(triggers()[1].textContent).toContain('8pm');
		of('PATCH')[1].resolve({});
		await expect.element(page.getByText('Guardado ✓')).toBeVisible();
	});

	test('a new block is editable before its POST returns; the edit is sent once the id arrives', async () => {
		await mountWithMondayBlock();
		const before = triggers().length;
		await page.getByRole('button', { name: '+ Agregar horario' }).first().click();
		await vi.waitFor(() => expect(of('POST')).toHaveLength(1));
		await vi.waitFor(() => expect(triggers().length).toBe(before + 2));
		const post = of('POST')[0];
		// The new block's end select, somewhere after Monday's two.
		const fresh = triggers().filter((t) => t.textContent?.includes('5pm') && !t.disabled);
		const newEnd = fresh.find((t) => t !== triggers()[1])!;
		expect(newEnd.disabled).toBe(false);
		await pick(newEnd, '3pm');
		expect(of('PATCH')).toHaveLength(0);

		post.resolve({ id: 'r2', event_type_id: null, day_of_week: post.body.day_of_week, start_time: '09:00', end_time: '17:00' });
		await vi.waitFor(() => expect(of('PATCH')).toHaveLength(1));
		expect(of('PATCH')[0].path).toBe('/v1/availability-rules/r2');
		expect(of('PATCH')[0].body).toEqual({ start_time: '09:00', end_time: '15:00' });
	});

	test('removing a block takes it off the screen at once', async () => {
		await mountWithMondayBlock();
		const before = triggers().length;
		await page.getByRole('button', { name: /^(Quitar|Eliminar horario)$/ }).first().click();
		await vi.waitFor(() => expect(triggers().length).toBe(before - 2));
		await vi.waitFor(() => expect(of('DELETE')).toHaveLength(1));
		expect(of('DELETE')[0].path).toBe('/v1/availability-rules/r1');
	});
});

describe('Disponibilidad: Tiempo entre sesiones (Soporte)', () => {
	test('hidden when it does not apply (Mentoría, no área)', async () => {
		await mountWithMondayBlock();
		expect(page.getByText('Tiempo entre sesiones').query()).toBeNull();
	});

	test('a support person picks 10 min: saved at once, the helper shows the new cadence', async () => {
		transitionApi.myTransition.mockImplementation(async () => ({ ...NO_TRANSITION, applies: true }));
		await mountWithMondayBlock();
		await expect.element(page.getByText('Tiempo entre sesiones')).toBeVisible();
		await expect
			.element(
				page.getByText(
					'Tus sesiones de 40 min + 15 min de transición se ofrecen cada 30 min: 9:00 a. m., 9:30 a. m., 10:00 a. m., 10:30 a. m.… Tras cada reserva, el siguiente horario libre es 60 min después de su inicio.'
				)
			)
			.toBeVisible();
		const trigger = document.getElementById('transition-minutes')!;
		expect(trigger.textContent).toContain('Como la plantilla (15 min)');
		await pick(trigger, '10 min');
		await vi.waitFor(() => expect(transitionApi.putMyTransition).toHaveBeenCalledWith(10));
		await expect
			.element(page.getByText('Tus sesiones de 40 min empezarán cada 50 min: 9:00 a. m., 9:50 a. m., 10:40 a. m., 11:30 a. m.…'))
			.toBeVisible();
		// Back to the template: null.
		await pick(document.getElementById('transition-minutes')!, 'Como la plantilla (15 min)');
		await vi.waitFor(() => expect(transitionApi.putMyTransition).toHaveBeenLastCalledWith(null));
	});
});
