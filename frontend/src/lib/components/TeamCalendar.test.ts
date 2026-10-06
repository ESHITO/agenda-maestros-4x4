// Fork: the Panel's team calendar at phone width (375 px, the main device). Owner, 6 Oct
// 2026: Mes / 15 días / Semana, Mes by default; the whole team in their colours; the hours
// nobody covers marked. Carried over from the 7-day panel it replaces: people free in the
// same hour sit side by side, days count people (not rows), and a failed load never reads
// as "nobody is free".
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';
import type { TeamCalendar as Answer, TeamCalPerson, TeamCoverageTarget } from '$lib/api';
import { displayZone } from '$lib/prefs';
import { addDays, todayIn } from '$lib/team-availability';
import { lastOfMonth, periodOf } from '$lib/team-calendar';

type Q = { from: string; to: string; tz: string; free?: boolean; fresh?: boolean };
const calendar = vi.fn<(q: Q) => Promise<Answer>>();
const putCoverageTarget = vi.fn<(b: { tz: string; days: unknown[] }) => Promise<TeamCoverageTarget>>();

vi.mock('$lib/api', async (importOriginal) => {
	const mod = await importOriginal<typeof import('$lib/api')>();
	return {
		...mod,
		teamApi: {
			...mod.teamApi,
			calendar: (q: Q) => calendar(q),
			putCoverageTarget: (b: { tz: string; days: unknown[] }) => putCoverageTarget(b)
		}
	};
});

const { default: TeamCalendar } = await import('./TeamCalendar.svelte');

const TARGET: TeamCoverageTarget = {
	v: 1,
	tz: 'America/Lima',
	days: [1, 2, 3, 4, 5, 6].map((dow) => ({ dow, start: '08:00', end: '20:00' })),
	is_default: true,
	can_edit: true
};

function person(user_id: string, name: string, area: 'mentoria' | 'soporte', color: string, extra: Partial<TeamCalPerson> = {}): TeamCalPerson {
	return {
		key: `${area}:${user_id}`,
		user_id,
		name,
		area,
		avatar_url: '',
		color,
		color_custom: false,
		is_owner: false,
		is_you: false,
		link: { slug: `${area}-${user_id}`, url: `https://agenda.example/book/${area}-${user_id}` },
		duration_min: 30,
		days: {},
		...extra
	};
}

let today = '';

/** Everybody works 9-12 today; with free starts at 9:00 and 9:30 when the answer has them.
 *  Mentoría is uncovered 8-9 a. m. today, Soporte only the owner from 1 p. m. */
function answer(q: Q, over: Partial<Answer> = {}): Answer {
	const withFree = q.free !== false;
	const day = () => ({ hours: [[540, 720]] as [number, number][], ...(withFree ? { free: [540, 570] } : {}) });
	const owner = { is_owner: true, is_you: true };
	return {
		tz: q.tz,
		from: q.from,
		to: q.to,
		today,
		generated_at: new Date().toISOString(),
		free_included: withFree,
		target: TARGET,
		people: [
			person('owner', 'Daniel Pérez', 'mentoria', '#4fd7ff', { ...owner, days: { [today]: day() } }),
			person('ana', 'Ana Torres', 'mentoria', '#2563eb', {
				days: { [today]: { ...day(), busy: [{ key: 'b1', start: 600, end: 630, type: 'Mentoría privada', area: 'mentoria' }] } }
			}),
			person('bart', 'Bartolomé Ríos', 'soporte', '#9333ea', { days: { [today]: day() } }),
			person('owner', 'Daniel Pérez', 'soporte', '#4fd7ff', { ...owner, days: { [today]: day() } })
		],
		coverage: {
			mentoria: { days: { [today]: { target: [[480, 1200]], uncovered: [[480, 540], [720, 1200]] } } },
			soporte: { days: { [today]: { target: [[480, 1200]], uncovered: [[480, 540]], owner_only: [] } } }
		},
		...over
	};
}

function dayCells(container: HTMLElement): HTMLButtonElement[] {
	return [...container.querySelectorAll<HTMLButtonElement>('button[data-day]')];
}

function viewButton(container: HTMLElement, label: string): HTMLButtonElement {
	return [...container.querySelectorAll<HTMLButtonElement>('[aria-label="Vista del calendario"] button')].find(
		(b) => b.textContent?.trim() === label
	)!;
}

async function settle(check: () => void) {
	await expect
		.poll(() => {
			try {
				check();
				return true;
			} catch {
				return false;
			}
		})
		.toBe(true);
	check();
}

beforeEach(async () => {
	await page.viewport(375, 812);
	document.body.style.margin = '0';
	document.body.style.padding = '0 16px'; // the admin shell's px-4 gutter
	calendar.mockReset();
	putCoverageTarget.mockReset();
	today = todayIn(displayZone());
});

afterEach(() => {
	cleanup();
	document.body.style.padding = '';
});

describe('TeamCalendar at 375 px', () => {
	test('opens on Mes: the whole month, the quick answer first, then the complete one', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);

		await settle(() => expect(calendar).toHaveBeenCalledTimes(2));
		expect(viewButton(container, 'Mes').getAttribute('aria-pressed')).toBe('true');
		const first = calendar.mock.calls[0][0];
		expect(first).toMatchObject({ from: `${today.slice(0, 8)}01`, to: lastOfMonth(today), free: false });
		expect(calendar.mock.calls[1][0].free).toBeUndefined();
		await settle(() => expect(dayCells(container).length).toBe(Number(lastOfMonth(today).slice(8))));
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);
	});

	test('people free in the same hour sit side by side, and the day counts people', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);

		await settle(() => expect(container.querySelectorAll('[data-person-chip]').length).toBe(4));
		const chips = [...container.querySelectorAll<HTMLElement>('[data-person-chip]')];
		// All four can start at 9:00: at least the first two share a line.
		const tops = chips.map((c) => Math.round(c.getBoundingClientRect().top));
		expect(tops[1]).toBe(tops[0]);
		expect(new Set(tops).size).toBeLessThanOrEqual(2);
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);

		// Today: 3 people (the owner is in both áreas), not 4 rows.
		const todayCell = container.querySelector<HTMLButtonElement>(`button[data-day="${today}"]`)!;
		expect(todayCell.getAttribute('aria-pressed')).toBe('true');
		expect(todayCell.getAttribute('aria-label')).toContain('3 personas libres');
		expect(container.querySelector('[data-day-counts]')?.textContent).toContain('3 personas libres');
		// One dot per person in the phone cell.
		expect(todayCell.querySelectorAll('.sm\\:hidden > span.rounded-full').length).toBe(3);
	});

	test('a failed load shows no counts next to the error', async () => {
		calendar.mockRejectedValue(new Error('Servidor caído'));
		const { container } = await render(TeamCalendar);

		await settle(() => expect(container.textContent).toContain('No se pudo cargar el calendario del equipo.'));
		const cells = dayCells(container);
		expect(cells.length).toBeGreaterThanOrEqual(28);
		for (const b of cells) {
			expect(b.getAttribute('aria-label')).not.toMatch(/personas? libres?|trabaja|sin cubrir/);
			expect(b.querySelector('[data-cell-uncovered]')).toBeNull();
		}
		expect(container.querySelector('[data-day-detail]')).toBeNull();
	});

	test('marks the hours nobody covers, with text and a pattern, not colour alone', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);

		const todayCell = () => container.querySelector<HTMLButtonElement>(`button[data-day="${today}"]`)!;
		await settle(() => expect(todayCell().querySelector('[data-cell-uncovered]')).not.toBeNull());
		// 1 h + 8 h in Mentoría, 1 h in Soporte: said per área, never added up to "10 h".
		expect(todayCell().getAttribute('aria-label')).toContain('sin cubrir: Mentoría 9 h, Soporte 1 h');
		expect(todayCell().getAttribute('aria-label')).not.toContain('10 h');
		expect(container.querySelector(`button[data-day="${today}"]`)!.textContent).not.toContain('10 h');
		const bar = todayCell().querySelector<HTMLElement>('[data-cell-uncovered]')!;
		expect(getComputedStyle(bar).backgroundImage).toContain('repeating-linear-gradient');

		const cov = container.querySelector('[data-day-coverage]')!.textContent!.replace(/\s+/g, ' ');
		expect(cov).toContain('Mentoría');
		expect(cov).toContain('Sin cubrir: 8:00 a. m. – 9:00 a. m. · 12:00 p. m. – 8:00 p. m.');
		expect(cov).toContain('Soporte');
		expect(container.querySelectorAll('[data-timeline] [data-uncovered]').length).toBe(3);

		// Only Soporte: its 1 h.
		const soporte = [...container.querySelectorAll<HTMLButtonElement>('[aria-label="Filtrar por área"] button')].find(
			(b) => b.textContent?.trim() === 'Soporte'
		)!;
		soporte.click();
		await settle(() => expect(todayCell().getAttribute('aria-label')).toContain('sin cubrir 1 h'));
	});

	test('paints hours and coverage before the calendars answer', async () => {
		calendar.mockImplementation((q) => (q.free === false ? Promise.resolve(answer(q)) : new Promise<Answer>(() => {})));
		const { container } = await render(TeamCalendar);

		await settle(() => expect(container.querySelector(`button[data-day="${today}"] [data-cell-uncovered]`)).not.toBeNull());
		expect(container.textContent).toContain('Revisando los calendarios de cada persona…');
		const label = container.querySelector(`button[data-day="${today}"]`)!.getAttribute('aria-label')!;
		expect(label).toContain('3 trabajan');
		expect(label).not.toContain('libres'); // unknown yet, never "0 libres"
		expect(container.querySelector('[data-person-row="mentoria:ana"]')?.textContent).toContain('Revisando su calendario…');
		expect(container.querySelector('[data-person-row="mentoria:ana"]')?.textContent).toContain('Ocupado: 10:00 a. m. – 10:30 a. m. (Mentoría privada)');
		// Not checked yet is its own mark (outline), never "Trabaja, sin cupos"; Ana's session
		// adds a ring, and the cell counts it.
		const cell = container.querySelector(`button[data-day="${today}"]`)!;
		const dots = [...cell.querySelectorAll<HTMLElement>('.sm\\:hidden > [data-dot]')];
		expect(dots.length).toBe(3);
		expect(dots.every((d) => d.dataset.dot === 'unknown')).toBe(true);
		expect(dots.filter((d) => d.hasAttribute('data-booked')).length).toBe(1);
		expect(cell.querySelector('[data-cell-sessions]')?.textContent).toContain('1');
		expect(container.querySelector('[data-legend-unknown]')).not.toBeNull();
		// The day lanes draw those hours as a dashed outline, not the faint "no openings" fill.
		expect(container.querySelector('[data-day-detail] [data-lane="mentoria:ana"] [data-hours="unknown"]')).not.toBeNull();
		expect(container.querySelector('[data-day-detail] [data-hours="known"]')).toBeNull();
	});

	test('a fully booked person is drawn as busy, and someone with sessions and free time keeps a ring', async () => {
		calendar.mockImplementation(async (q) => {
			const a = answer(q);
			const carla = person('carla', 'Carla Núñez', 'mentoria', '#db2777', {
				days: { [today]: { hours: [[540, 600]], busy: [{ key: 'b9', start: 540, end: 600, type: 'Mentoría privada', area: 'mentoria' }] } }
			});
			return { ...a, people: [...a.people, carla] };
		});
		const { container } = await render(TeamCalendar);
		const cell = () => container.querySelector(`button[data-day="${today}"]`)!;
		await settle(() => expect(cell().querySelectorAll('.sm\\:hidden > [data-dot="free"]').length).toBe(3));
		const dots = [...cell().querySelectorAll<HTMLElement>('.sm\\:hidden > [data-dot]')];
		expect(dots.filter((d) => d.dataset.dot === 'busy').length).toBe(1); // Carla
		expect(dots.some((d) => d.dataset.dot === 'full')).toBe(false);
		expect(dots.filter((d) => d.dataset.dot === 'free' && d.hasAttribute('data-booked')).length).toBe(1); // Ana
		expect(cell().querySelector('[data-cell-sessions]')?.textContent).toContain('2');
	});

	test('the minute-by-minute refresh reaches the server (no client-cache hit), without fresh', async () => {
		const spy = vi.spyOn(window, 'setInterval');
		calendar.mockImplementation(async (q) => answer(q));
		await render(TeamCalendar);
		await settle(() => expect(calendar).toHaveBeenCalledTimes(2));
		const tick = spy.mock.calls.find((c) => c[1] === 60_000)?.[0] as (() => void) | undefined;
		spy.mockRestore();
		expect(tick).toBeTypeOf('function');
		tick!(); // fires right away: the answer in the browser's cache is seconds old
		await settle(() => expect(calendar).toHaveBeenCalledTimes(3));
		const q = calendar.mock.calls[2][0];
		expect(q.fresh).toBeFalsy();
		expect(q.free).toBeUndefined(); // straight to the complete answer, no quick step
	});

	test('Actualizar keeps what is shown while it asks again past the cache', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);
		await settle(() => expect(container.querySelectorAll('[data-person-chip]').length).toBe(4));

		calendar.mockImplementation(() => new Promise<Answer>(() => {}));
		const calls = calendar.mock.calls.length;
		container.querySelector<HTMLButtonElement>('button[aria-label="Actualizar"]')!.click();
		await settle(() => expect(calendar.mock.calls.length).toBe(calls + 1));
		// Straight to the complete answer, fresh; never back to the quick one.
		expect(calendar.mock.calls[calls][0]).toMatchObject({ fresh: true });
		expect(calendar.mock.calls[calls][0].free).toBeUndefined();
		expect(container.querySelectorAll('[data-person-chip]').length).toBe(4);
		expect(container.textContent).toContain('Revisando los calendarios de cada persona…');
	});

	test('15 días asks the quincena and Semana the Monday-to-Sunday week', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);
		await settle(() => expect(calendar).toHaveBeenCalledTimes(2));

		viewButton(container, '15 días').click();
		const q = periodOf('fortnight', today);
		await settle(() => expect(calendar.mock.calls.at(-1)![0]).toMatchObject({ from: q.from, to: q.to }));
		await settle(() => expect(dayCells(container).length).toBe(Number(q.to.slice(8)) - Number(q.from.slice(8)) + 1));
		expect(viewButton(container, '15 días').getAttribute('aria-pressed')).toBe('true');

		viewButton(container, 'Semana').click();
		const w = periodOf('week', today);
		await settle(() => expect(calendar.mock.calls.at(-1)![0]).toMatchObject({ from: w.from, to: w.to }));
		await settle(() => expect(dayCells(container).length).toBe(7));
		expect(addDays(w.from, 6)).toBe(w.to);
		await settle(() => expect(container.querySelector(`button[data-day="${today}"] [data-timeline]`)).not.toBeNull());
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);
	});

	test('picking people shows only them, in their colour', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);
		await settle(() => expect(container.querySelectorAll('[data-person-filter]').length).toBe(3));

		const ana = [...container.querySelectorAll<HTMLButtonElement>('[data-person-filter]')].find((b) => b.textContent?.includes('Ana'))!;
		ana.click();
		await settle(() => expect(container.querySelector(`button[data-day="${today}"]`)!.getAttribute('aria-label')).toContain('1 persona libre'));
		expect(container.querySelectorAll('[data-person-row]').length).toBe(1);
		expect(ana.getAttribute('aria-pressed')).toBe('true');
	});

	test('the owner changes the hours to cover; others only read them', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		putCoverageTarget.mockImplementation(async (b) => ({ ...TARGET, ...b, is_default: false }) as TeamCoverageTarget);
		const { container } = await render(TeamCalendar);

		await settle(() => expect(container.querySelector('[data-target-summary]')?.textContent).toBe('lun–sáb 8:00 a. m. – 8:00 p. m. · hora de Lima'));
		const change = [...container.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Cambiar')!;
		change.click();
		await settle(() => expect(document.querySelectorAll('[data-target-row]').length).toBe(7));
		const save = [...document.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === 'Guardar')!;
		const calls = calendar.mock.calls.length;
		save.click();
		await settle(() => expect(putCoverageTarget).toHaveBeenCalledTimes(1));
		expect(putCoverageTarget.mock.calls[0][0]).toEqual({ tz: 'America/Lima', days: TARGET.days });
		// The calendar is asked again past the server's cache.
		await settle(() => expect(calendar.mock.calls.slice(calls).some(([q]) => q.fresh)).toBe(true));
		await settle(() => expect(document.querySelectorAll('[data-target-row]').length).toBe(0)); // the dialog closed

		cleanup();
		calendar.mockImplementation(async (q) => answer(q, { target: { ...TARGET, can_edit: false } }));
		const second = await render(TeamCalendar);
		await settle(() => expect(second.container.querySelector('[data-target-summary]')).not.toBeNull());
		expect([...second.container.querySelectorAll('button')].some((b) => b.textContent?.trim() === 'Cambiar')).toBe(false);
	});

	test('a saved target shows its coverage at once, keeping who is free, before the complete answer', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		putCoverageTarget.mockImplementation(async (b) => ({ ...TARGET, ...b, is_default: false }) as TeamCoverageTarget);
		const { container } = await render(TeamCalendar);
		const todayCell = () => container.querySelector<HTMLButtonElement>(`button[data-day="${today}"]`)!;
		await settle(() => expect(container.querySelectorAll('[data-person-chip]').length).toBe(4));
		expect(todayCell().querySelector('[data-cell-uncovered]')).not.toBeNull();

		// After the save: the quick answer says everything is covered; the complete one never comes.
		const covered = { days: { [today]: { target: [[540, 720]] as [number, number][] } } };
		calendar.mockImplementation((q) =>
			q.free === false ? Promise.resolve(answer(q, { coverage: { mentoria: covered, soporte: covered } })) : new Promise<Answer>(() => {})
		);
		const calls = calendar.mock.calls.length;
		[...container.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Cambiar')!.click();
		await settle(() => expect(document.querySelectorAll('[data-target-row]').length).toBe(7));
		[...document.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === 'Guardar')!.click();

		await settle(() => expect(todayCell().querySelector('[data-cell-uncovered]')).toBeNull());
		expect(calendar.mock.calls[calls][0]).toMatchObject({ free: false, fresh: true });
		expect(todayCell().getAttribute('aria-label')).toContain('horario cubierto');
		// The free starts already known stay on screen meanwhile.
		expect(container.querySelectorAll('[data-person-chip]').length).toBe(4);
		expect(todayCell().getAttribute('aria-label')).toContain('3 personas libres');
	});

	test('when the complete answer fails, nobody stays "Revisando su calendario…"', async () => {
		calendar.mockImplementation((q) => (q.free === false ? Promise.resolve(answer(q)) : Promise.reject(new Error('lento'))));
		const { container } = await render(TeamCalendar);

		await settle(() => expect(container.textContent).toContain('No se pudieron revisar los horarios libres.'));
		const row = container.querySelector('[data-person-row="mentoria:ana"]')!;
		expect(row.textContent).toContain('No se pudo revisar su calendario; pulsa Actualizar.');
		expect(row.textContent).not.toContain('Revisando');
		expect(container.querySelector('[data-day-detail]')!.textContent).not.toContain('Revisando');
	});

	test('on a phone, tapping a day brings its detail into view and focus; loading does not', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);
		await settle(() => expect(container.querySelectorAll('[data-person-chip]').length).toBe(4));
		window.scrollTo(0, 0);
		expect(document.activeElement?.hasAttribute('data-day-heading')).toBe(false);
		const heading = () => container.querySelector<HTMLElement>('[data-day-heading]')!;
		// Below the fold at 375 x 812: a tap that only rings the cell would look like nothing happened.
		expect(heading().getBoundingClientRect().top).toBeGreaterThan(window.innerHeight);

		const p = periodOf('month', today);
		const other = today === p.from ? addDays(today, 1) : p.from;
		container.querySelector<HTMLButtonElement>(`button[data-day="${other}"]`)!.click();
		await settle(() => expect(document.activeElement?.hasAttribute('data-day-heading')).toBe(true));
		await settle(() => {
			const top = heading().getBoundingClientRect().top;
			expect(top).toBeGreaterThanOrEqual(0);
			expect(top).toBeLessThan(window.innerHeight - 40);
		});
		window.scrollTo(0, 0);
	});

	test('"Hoy" brings the detail back to today after looking at another day', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);
		await settle(() => expect(calendar).toHaveBeenCalledTimes(2));
		const hoy = () => [...container.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent?.trim() === 'Hoy')!;
		await settle(() => expect(container.querySelector('[data-day-heading]')).not.toBeNull());
		expect(hoy().disabled).toBe(true); // today is shown and picked

		const p = periodOf('month', today);
		const other = today === p.from ? addDays(today, 1) : p.from;
		container.querySelector<HTMLButtonElement>(`button[data-day="${other}"]`)!.click();
		await settle(() => expect(hoy().disabled).toBe(false));
		hoy().click();
		await settle(() => expect(container.querySelector(`button[data-day="${today}"]`)!.getAttribute('aria-pressed')).toBe('true'));
		expect(container.querySelector('[data-day-heading]')!.textContent).toContain('hoy');
		expect(calendar).toHaveBeenCalledTimes(2); // same month: nothing asked again
	});

	test('days more than a year ahead are not tappable empty days', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);
		await settle(() => expect(calendar).toHaveBeenCalledTimes(2));
		const last = addDays(today, 366);
		const next = container.querySelector<HTMLButtonElement>('button[aria-label="Periodo siguiente"]')!;
		const months = (Number(last.slice(0, 4)) - Number(today.slice(0, 4))) * 12 + Number(last.slice(5, 7)) - Number(today.slice(5, 7));
		for (let i = 0; i < months; i++) next.click();

		await settle(() => expect(calendar.mock.calls.at(-1)![0]).toMatchObject({ from: `${last.slice(0, 8)}01`, to: last }));
		await settle(() => expect(dayCells(container).length).toBe(Number(last.slice(8))));
		expect(container.querySelector(`button[data-day="${addDays(last, 1)}"]`)).toBeNull();
		expect(next.disabled).toBe(true);
		const detail = container.querySelector('[data-day-detail]')?.textContent ?? '';
		expect(detail).not.toContain('Día pasado');
	});

	test('the M / S letters are explained under "Todos", and week rows use whole words', async () => {
		calendar.mockImplementation(async (q) => answer(q));
		const { container } = await render(TeamCalendar);
		await settle(() => expect(container.querySelector('[data-legend-area="mentoria"]')).not.toBeNull());
		expect(container.querySelector('[data-legend-area="soporte"]')?.textContent).toContain('Soporte');
		const mentoria = [...container.querySelectorAll<HTMLButtonElement>('[aria-label="Filtrar por área"] button')].find(
			(b) => b.textContent?.trim() === 'Mentoría'
		)!;
		mentoria.click();
		await settle(() => expect(container.querySelector('[data-legend-area]')).toBeNull());

		viewButton(container, 'Semana').click();
		await settle(() => expect(container.querySelector(`button[data-day="${today}"] [data-week-counts]`)?.textContent).toContain('personas libres'));
		const row = container.querySelector(`button[data-day="${today}"] [data-week-counts]`)!.textContent!;
		expect(row).toBe('2 personas libres · 1 sesión');
		expect(container.textContent).not.toContain('ses.');
	});
});
