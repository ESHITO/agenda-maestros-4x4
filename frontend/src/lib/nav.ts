// Admin shell navigation - the pure part (no $app imports, so it runs in vitest).
// The layout builds `navItems` (hrefs carry `base`, icons are inline SVG), filters it by
// role into `visibleNavItems`, and feeds that list here.

export type NavItem = {
	/** Section heading in the sidebar and in the "Más" sheet; null = footer item (Configuración). */
	section: string | null;
	href: string;
	label: string;
	/** Short label for the tablet rail and the phone tab bar; overrides SHORT_LABELS. */
	railLabel?: string;
	/** Inline SVG (16 px); the tab bar and rail scale it to 20 px with CSS. */
	icon: string;
	adminOnly: boolean;
	/** Active only on this exact path (Inicio), not on every path below it. */
	exact?: boolean;
	requiresFeature?: string;
};

/**
 * Which destinations the phone tab bar shows, most useful first. Every role gets a
 * sensible bar: the owner sees Inicio, Reservas, Miembros, Disponibilidad; a mentor
 * (no Miembros) sees Inicio, Reservas, Disponibilidad, Tipos de atención.
 */
export const TAB_PRIORITY = [
	'Inicio',
	'Reservas',
	'Miembros',
	'Disponibilidad',
	'Tipos de atención',
	'Calendario'
] as const;

export const TAB_COUNT = 4;

/**
 * The first `max` items of TAB_PRIORITY that this user can see, in priority order.
 * Everything else (and these too) stays reachable from the "Más" sheet.
 */
export function bottomTabs<T extends { label: string }>(visibleItems: T[], max = TAB_COUNT): T[] {
	const tabs: T[] = [];
	for (const label of TAB_PRIORITY) {
		if (tabs.length >= max) break;
		const item = visibleItems.find((i) => i.label === label);
		if (item) tabs.push(item);
	}
	return tabs;
}

/**
 * Short labels for the narrow surfaces: the tablet rail (76 px) and the phone tab bar
 * (75 px per tab at 375 px, 72 px on a 360 px Android). One place for both, keyed by the
 * sidebar label, so the layout's `navItems` needs no per-item field.
 */
export const SHORT_LABELS: Record<string, string> = {
	'Tipos de atención': 'Atención',
	Disponibilidad: 'Horario',
	'Claves de API': 'API',
	'Aplicaciones conectadas': 'Apps',
	Configuración: 'Ajustes'
};

/**
 * The longest label a tab can print on one line at 10 px: "Disponibilidad" (14) already
 * clips on a 360 px phone when semibold, so every tab-bar label must be shorter.
 */
export const SHORT_LABEL_MAX = 12;

/** The short label of the rail and the tab bar: the item's own, else SHORT_LABELS, else `label`. */
export function railLabel(item: { label: string; railLabel?: string }): string {
	return item.railLabel ?? SHORT_LABELS[item.label] ?? item.label;
}

/** Up to two initials for the avatar fallback ("Ana María Pérez" → "AM"). */
export function initials(name: string): string {
	return name
		.split(' ')
		.filter(Boolean)
		.map((p) => p[0])
		.join('')
		.toUpperCase()
		.slice(0, 2);
}
