<script lang="ts">
	// Phone "Más" bottom sheet: a shadcn Dialog anchored to the bottom (no animation, like the
	// old side menu) listing EVERY visible nav item by section as 48 px rows, then the profile
	// row, "Reportar un problema", "Cerrar sesión" and the version. The layout closes it on
	// navigation and when the screen reaches md (its overlay is not md:hidden).
	import * as Dialog from '$lib/components/ui/dialog';
	import { initials, type NavItem } from '$lib/nav';

	let {
		open = $bindable(false),
		items,
		isActive,
		profileHref,
		name,
		avatarUrl,
		version = '',
		releasesUrl,
		onReport,
		onLogout
	}: {
		open?: boolean;
		items: NavItem[];
		isActive: (item: NavItem) => boolean;
		profileHref: string;
		name: string;
		avatarUrl?: string | null;
		version?: string;
		releasesUrl: string;
		onReport: () => void;
		onLogout: () => void;
	} = $props();

	const rowClass =
		'flex h-12 w-full items-center gap-3 rounded-lg px-3 text-base transition-colors active:bg-sidebar-accent/60';
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="bottom-0 left-0 right-0 top-auto flex max-h-[85dvh] w-full max-w-none translate-x-0 translate-y-0 flex-col gap-0 overflow-y-auto rounded-t-2xl rounded-b-none border-x-0 border-b-0 border-sidebar-border bg-sidebar p-0 sm:rounded-t-2xl sm:rounded-b-none md:hidden data-[state=open]:animate-none! data-[state=closed]:animate-none!"
	>
		<Dialog.Title class="sr-only">Menú</Dialog.Title>
		<Dialog.Description class="sr-only">Todas las secciones, tu perfil y cerrar sesión.</Dialog.Description>

		<!-- Grab handle row; the Dialog's own close (X) sits at its right. -->
		<div class="flex h-10 shrink-0 items-center justify-center">
			<span class="h-1 w-10 rounded-full bg-sidebar-foreground/20" aria-hidden="true"></span>
		</div>

		<nav class="px-2 pb-2" style="padding-bottom: calc(0.5rem + env(safe-area-inset-bottom, 0px))">
			{#each items as item, i (item.href)}
				{#if item.section && item.section !== items[i - 1]?.section}
					<p class="mb-1 mt-3 px-3 text-xs font-semibold uppercase tracking-wide text-sidebar-foreground/40 first:mt-0">
						{item.section}
					</p>
				{:else if !item.section && items[i - 1]?.section}
					<div class="my-2 border-t border-sidebar-border"></div>
				{/if}
				{@const active = isActive(item)}
				<a
					href={item.href}
					aria-current={active ? 'page' : undefined}
					class="{rowClass} {active
						? 'bg-sidebar-accent font-semibold text-sidebar-accent-foreground'
						: 'font-medium text-sidebar-foreground/80'}"
				>
					<span class="shrink-0 [&_svg]:size-5 {active ? 'opacity-100' : 'opacity-60'}">{@html item.icon}</span>
					<span class="truncate">{item.label}</span>
				</a>
			{/each}

			<div class="my-2 border-t border-sidebar-border"></div>

			<a href={profileHref} class="{rowClass} font-medium text-sidebar-foreground/80">
				<span class="flex h-7 w-7 shrink-0 items-center justify-center overflow-hidden rounded-full bg-primary text-xs font-semibold text-primary-foreground">
					{#if avatarUrl}
						<img src={avatarUrl} alt={name} class="h-full w-full object-cover" />
					{:else}
						{initials(name || 'U')}
					{/if}
				</span>
				<span class="min-w-0 flex-1">
					<span class="block truncate">{name}</span>
					<span class="block text-xs font-normal text-sidebar-foreground/50">Mi perfil</span>
				</span>
			</a>

			<button type="button" onclick={onReport} class="{rowClass} font-medium text-sidebar-foreground/80">
				<span class="flex w-7 shrink-0 justify-center opacity-60">
					<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
						<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
					</svg>
				</span>
				Reportar un problema
			</button>

			<button type="button" onclick={onLogout} class="{rowClass} font-medium text-sidebar-foreground/80">
				<span class="flex w-7 shrink-0 justify-center opacity-60">
					<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
						<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
						<polyline points="16 17 21 12 16 7"/>
						<line x1="21" y1="12" x2="9" y2="12"/>
					</svg>
				</span>
				Cerrar sesión
			</button>

			{#if version}
				<a
					href={releasesUrl}
					target="_blank"
					rel="noopener noreferrer"
					class="mt-1 block px-3 py-2 text-xs text-sidebar-foreground/35 transition-colors hover:text-sidebar-foreground/60"
				>
					{version}
				</a>
			{/if}
		</nav>
	</Dialog.Content>
</Dialog.Root>
