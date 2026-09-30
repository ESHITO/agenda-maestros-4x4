<script lang="ts">
	// Tablet (md to lg) navigation rail: 76 px wide, icon over a short label, sticky for
	// the whole viewport height. Configuración, "Reportar" and "Salir" sit at the bottom.
	import { initials, railLabel, type NavItem } from '$lib/nav';

	let {
		items,
		isActive,
		profileHref,
		name,
		avatarUrl,
		onReport,
		onLogout
	}: {
		/** Every visible nav item; the ones without a section (Configuración) go to the bottom. */
		items: NavItem[];
		isActive: (item: NavItem) => boolean;
		profileHref: string;
		name: string;
		avatarUrl?: string | null;
		onReport: () => void;
		onLogout: () => void;
	} = $props();

	const mainItems = $derived(items.filter((i) => i.section));
	const footerItems = $derived(items.filter((i) => !i.section));

	const itemClass = (active: boolean) =>
		`flex h-14 w-full flex-col items-center justify-center gap-1 px-1 transition-colors ${
			active ? 'text-sidebar-foreground' : 'text-sidebar-foreground/60 hover:text-sidebar-foreground'
		}`;
	const pillClass = (active: boolean) =>
		`flex h-7 w-12 shrink-0 items-center justify-center rounded-full transition-colors [&_svg]:size-5 ${
			active ? 'bg-sidebar-accent text-sidebar-accent-foreground' : 'group-hover:bg-sidebar-accent/60'
		}`;
</script>

{#snippet railLink(item: NavItem)}
	{@const active = isActive(item)}
	<a href={item.href} aria-current={active ? 'page' : undefined} class="group {itemClass(active)}" title={item.label}>
		<span class={pillClass(active)}>{@html item.icon}</span>
		<span class="line-clamp-2 w-full text-center text-[10px] leading-[1.15] {active ? 'font-semibold' : 'font-medium'}">
			{railLabel(item)}
		</span>
	</a>
{/snippet}

<aside
	class="sticky top-0 hidden h-dvh w-[76px] shrink-0 flex-col border-r border-sidebar-border bg-sidebar md:flex lg:hidden"
	aria-label="Navegación principal"
>
	<!-- Avatar → profile. Clipped by the parent box, not the image (see the sidebar's note). -->
	<a
		href={profileHref}
		class="flex h-16 shrink-0 items-center justify-center border-b border-sidebar-border transition-colors hover:bg-sidebar-accent/60"
		title={name}
		aria-label="Mi perfil"
	>
		<span class="flex h-8 w-8 shrink-0 items-center justify-center overflow-hidden rounded-full bg-primary text-xs font-semibold text-primary-foreground">
			{#if avatarUrl}
				<img src={avatarUrl} alt={name} class="h-full w-full object-cover" />
			{:else}
				{initials(name || 'U')}
			{/if}
		</span>
	</a>

	<!-- Scrolls itself only in a window shorter than its items, without drawing a bar: on
	     Windows a classic scrollbar here would be a second one next to the document's. -->
	<nav class="app-scroll-quiet min-h-0 flex-1 overflow-y-auto py-1">
		{#each mainItems as item (item.href)}
			{@render railLink(item)}
		{/each}
	</nav>

	<div class="shrink-0 border-t border-sidebar-border py-1">
		{#each footerItems as item (item.href)}
			{@render railLink(item)}
		{/each}
		<button type="button" onclick={onReport} class="group {itemClass(false)}" title="Reportar un problema">
			<span class={pillClass(false)}>
				<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
					<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
				</svg>
			</span>
			<span class="text-[10px] font-medium leading-[1.15]">Reportar</span>
		</button>
		<button type="button" onclick={onLogout} class="group {itemClass(false)}" title="Cerrar sesión">
			<span class={pillClass(false)}>
				<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
					<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
					<polyline points="16 17 21 12 16 7"/>
					<line x1="21" y1="12" x2="9" y2="12"/>
				</svg>
			</span>
			<span class="text-[10px] font-medium leading-[1.15]">Salir</span>
		</button>
	</div>
</aside>
