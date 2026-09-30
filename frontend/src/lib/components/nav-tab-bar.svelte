<script lang="ts">
	// Phone (< md) bottom tab bar: 4 destinations chosen by `bottomTabs` + "Más", which
	// opens the sheet with every visible item. Fixed to the bottom; the shell reserves its
	// height through --app-bottom-inset (app.css) so the page never ends hidden under it.
	// Labels are the short ones (lib/nav.ts SHORT_LABELS): a tab is 67 px wide at 375 px and
	// prints one line at 10 px, where "Tipos de atención" clipped to "Tipos de aten…".
	import { railLabel, type NavItem } from '$lib/nav';

	let {
		tabs,
		isActive,
		moreActive = false,
		onMore
	}: {
		tabs: NavItem[];
		isActive: (item: NavItem) => boolean;
		/** The sheet is open, or the current section is one that only lives in "Más". */
		moreActive?: boolean;
		onMore: () => void;
	} = $props();
</script>

<nav
	aria-label="Navegación principal"
	class="fixed inset-x-0 bottom-0 z-40 border-t border-sidebar-border bg-sidebar md:hidden"
	style="padding-bottom: env(safe-area-inset-bottom, 0px)"
>
	<ul class="flex h-14 items-stretch">
		{#each tabs as item (item.href)}
			{@const active = isActive(item)}
			<li class="min-w-0 flex-1">
				<a
					href={item.href}
					aria-current={active ? 'page' : undefined}
					class="flex h-full min-h-11 w-full flex-col items-center justify-center gap-0.5 px-1 transition-colors
						{active ? 'text-primary' : 'text-sidebar-foreground/60 active:text-sidebar-foreground'}"
					title={item.label}
				>
					<span class="shrink-0 [&_svg]:size-5">{@html item.icon}</span>
					<span class="w-full truncate text-center text-[10px] leading-tight {active ? 'font-semibold' : 'font-medium'}">
						{railLabel(item)}
					</span>
				</a>
			</li>
		{/each}
		<li class="min-w-0 flex-1">
			<button
				type="button"
				onclick={onMore}
				aria-haspopup="dialog"
				class="flex h-full min-h-11 w-full flex-col items-center justify-center gap-0.5 px-1 transition-colors
					{moreActive ? 'text-primary' : 'text-sidebar-foreground/60 active:text-sidebar-foreground'}"
			>
				<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0" aria-hidden="true">
					<circle cx="5" cy="12" r="1.5" fill="currentColor"/>
					<circle cx="12" cy="12" r="1.5" fill="currentColor"/>
					<circle cx="19" cy="12" r="1.5" fill="currentColor"/>
				</svg>
				<span class="text-[10px] leading-tight {moreActive ? 'font-semibold' : 'font-medium'}">Más</span>
			</button>
		</li>
	</ul>
</nav>
