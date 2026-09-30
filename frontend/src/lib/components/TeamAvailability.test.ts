// Fork: the Panel's team calendar at phone width (375 px, the main device). The point of
// the timeline is seeing several people side by side in one hour ("superpuesta"), so two
// chips must share a line there; the day chips count people, not entries; and a failed
// load must not read as "nobody is free".
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';
import type { TeamAvailability as Answer, TeamAvailabilityPerson } from '$lib/api';
import { displayZone } from '$lib/prefs';
import { todayIn } from '$lib/team-availability';

const availability = vi.fn<(q: unknown) => Promise<Answer>>();

vi.mock('$lib/api', async (importOriginal) => {
	const mod = await importOriginal<typeof import('$lib/api')>();
	return { ...mod, teamApi: { ...mod.teamApi, availability: (q: unknown) => availability(q) } };
});

const { default: TeamAvailability } = await import('./TeamAvailability.svelte');

function person(user_id: string, name: string, area: 'mentoria' | 'soporte', day: string, extra: Partial<TeamAvailabilityPerson> = {}): TeamAvailabilityPerson {
	return {
		user_id,
		name,
		area,
		avatar_url: '',
		color: null,
		is_you: false,
		link: { slug: `${area}-${user_id}`, url: `https://agenda.example/book/${area}-${user_id}` },
		slots: [
			{ start: `${day}T09:00:00-05:00`, end: `${day}T09:30:00-05:00` },
			{ start: `${day}T09:30:00-05:00`, end: `${day}T10:00:00-05:00` }
		],
		...extra
	};
}

function answer(day: string): Answer {
	return {
		tz: displayZone(),
		from: day,
		to: day,
		generated_at: new Date().toISOString(),
		people: [
			person('owner', 'Daniel Pérez', 'mentoria', day, { is_you: true }),
			person('ana', 'Ana Torres', 'mentoria', day),
			person('bart', 'Bartolomé Ríos', 'soporte', day),
			person('owner', 'Daniel Pérez', 'soporte', day, { is_you: true })
		]
	};
}

// The seven day chips: their aria-label starts with the long day ("jueves 1 de octubre").
function dayChips(container: HTMLElement): HTMLButtonElement[] {
	return [...container.querySelectorAll<HTMLButtonElement>('button[aria-pressed]')].filter((b) =>
		/^\p{L}+ \d{1,2} de \p{L}+/u.test(b.getAttribute('aria-label') ?? '')
	);
}

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

beforeEach(async () => {
	await page.viewport(375, 812);
	document.body.style.margin = '0';
	document.body.style.padding = '0 16px'; // the admin shell's px-4 gutter
	availability.mockReset();
});

afterEach(() => {
	cleanup();
	document.body.style.padding = '';
});

describe('TeamAvailability at 375 px', () => {
	test('people free in the same hour sit side by side, and the day counts people', async () => {
		const today = todayIn(displayZone());
		availability.mockResolvedValue(answer(today));
		const { container } = await render(TeamAvailability);

		await settle(() => expect(container.querySelectorAll('[data-person-chip]').length).toBe(4));
		const chips = [...container.querySelectorAll<HTMLElement>('[data-person-chip]')];
		// All four are free at 9:00: at least the first two share a line.
		const tops = chips.map((c) => Math.round(c.getBoundingClientRect().top));
		expect(tops[1]).toBe(tops[0]);
		const rows = new Set(tops).size;
		expect(rows).toBeLessThanOrEqual(2);
		// No horizontal overflow of the page.
		expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(375);

		// Today's chip: 3 people (the owner is in both áreas), not 4 entries.
		const todayChip = dayChips(container).find((b) => b.getAttribute('aria-pressed') === 'true')!;
		expect(todayChip.getAttribute('aria-label')).toContain('3 personas libres');
		expect(todayChip.textContent).toContain('3');
	});

	test('a failed load shows no counts next to the error', async () => {
		availability.mockRejectedValue(new Error('Servidor caído'));
		const { container } = await render(TeamAvailability);

		await settle(() => expect(container.textContent).toContain('No se pudo cargar la disponibilidad del equipo.'));
		const chips = dayChips(container);
		expect(chips.length).toBe(7);
		for (const b of chips) {
			expect(b.querySelector('span:nth-child(3)')?.textContent?.trim()).toBe('·');
			expect(b.getAttribute('aria-label')).not.toMatch(/personas? libres?/);
		}
	});
});
