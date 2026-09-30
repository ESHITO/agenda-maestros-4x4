// The phone tab bar shows 4 destinations + "Más". Which 4 depends on the role: the list
// is chosen by priority from the items the user can see, so a mentor (no Miembros) still
// gets a full bar. Run: pnpm exec vitest run src/lib/nav.test.ts
import { describe, expect, it } from 'vitest';
import { SHORT_LABELS, SHORT_LABEL_MAX, TAB_PRIORITY, bottomTabs, railLabel } from './nav';

const item = (label: string, extra: Partial<{ railLabel: string; section: string | null }> = {}) => ({
	label,
	section: 'Programación',
	...extra
});

// The sidebar's order for an owner (every item visible).
const owner = [
	item('Inicio'),
	item('Tipos de atención'),
	item('Disponibilidad', { railLabel: 'Horario' }),
	item('Reservas'),
	item('Calendario'),
	item('Grabaciones', { section: 'Equipo' }),
	item('Miembros', { section: 'Equipo' }),
	item('Equipos', { section: 'Equipo' }),
	item('Claves de API', { section: 'Desarrollador', railLabel: 'API' }),
	item('Aplicaciones conectadas', { section: 'Desarrollador', railLabel: 'Apps' }),
	item('Webhooks', { section: 'Desarrollador' }),
	item('Configuración', { section: null })
];

// A mentor: no adminOnly items (Grabaciones, Miembros, Equipos, Webhooks).
const mentor = owner.filter((i) => !['Grabaciones', 'Miembros', 'Equipos', 'Webhooks'].includes(i.label));

const labels = (items: { label: string }[]) => items.map((i) => i.label);

describe('bottomTabs', () => {
	it('gives the owner Inicio, Reservas, Miembros, Disponibilidad - in priority order, not sidebar order', () => {
		expect(labels(bottomTabs(owner))).toEqual(['Inicio', 'Reservas', 'Miembros', 'Disponibilidad']);
	});

	it('gives a mentor (no Miembros) Inicio, Reservas, Disponibilidad, Tipos de atención', () => {
		expect(labels(bottomTabs(mentor))).toEqual(['Inicio', 'Reservas', 'Disponibilidad', 'Tipos de atención']);
	});

	it('returns the very objects it was given (hrefs and icons travel with them)', () => {
		const tabs = bottomTabs(owner);
		expect(tabs[0]).toBe(owner[0]);
		expect(tabs[2]).toBe(owner.find((i) => i.label === 'Miembros'));
	});

	it('never exceeds the tab count and never repeats a destination', () => {
		const tabs = bottomTabs(owner);
		expect(tabs).toHaveLength(4);
		expect(new Set(labels(tabs)).size).toBe(4);
		expect(bottomTabs(owner, 2).map((i) => i.label)).toEqual(['Inicio', 'Reservas']);
	});

	it('fills what it can when few destinations are visible (demo mode hides Calendario)', () => {
		const demoMentor = mentor.filter((i) => i.label !== 'Calendario');
		expect(labels(bottomTabs(demoMentor))).toEqual(['Inicio', 'Reservas', 'Disponibilidad', 'Tipos de atención']);
		expect(labels(bottomTabs([item('Inicio'), item('Configuración', { section: null })]))).toEqual(['Inicio']);
		expect(bottomTabs([])).toEqual([]);
	});

	it('ignores items outside the priority list (Configuración lives in Más)', () => {
		expect(labels(bottomTabs(owner))).not.toContain('Configuración');
		expect(TAB_PRIORITY).not.toContain('Configuración');
	});
});

describe('railLabel', () => {
	it('prefers the item\'s own short label, then the shared table, then the full label', () => {
		expect(railLabel(item('Disponibilidad', { railLabel: 'Turnos' }))).toBe('Turnos');
		expect(railLabel(item('Disponibilidad'))).toBe('Horario');
		expect(railLabel(item('Tipos de atención'))).toBe('Atención');
		expect(railLabel(item('Reservas'))).toBe('Reservas');
	});

	// The tab bar prints one line at 10 px in a 67 px cell (375 px phone, px-1): "Tipos de
	// atención" measured 78-80 px and showed "Tipos de aten…" until it got a short label.
	it('keeps every tab-bar candidate short enough for one line on a 360 px phone', () => {
		for (const label of TAB_PRIORITY) {
			expect(railLabel(item(label)).length, label).toBeLessThanOrEqual(SHORT_LABEL_MAX);
		}
	});

	it('keeps the rail labels short too (the rail is 76 px, two lines allowed)', () => {
		for (const short of Object.values(SHORT_LABELS)) {
			expect(short.length).toBeLessThanOrEqual(SHORT_LABEL_MAX);
		}
	});
});
