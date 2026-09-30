<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { base } from '$app/paths';
	import '../app.css';
	import { api, type User } from '$lib/api';
	import { currentUser, authStatus, type AuthStatus } from '$lib/stores';
	import { prefs, prefsFromUser } from '$lib/prefs';
	import { bottomTabs, initials, type NavItem } from '$lib/nav';
	import { Toaster } from '$lib/components/ui/sonner';
	import { buttonVariants } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import DemoBanner from '$lib/components/demo-banner.svelte';
	import NavTabBar from '$lib/components/nav-tab-bar.svelte';
	import NavRail from '$lib/components/nav-rail.svelte';
	import NavSheet from '$lib/components/nav-sheet.svelte';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	let checking = $state(true);
	let reportOpen = $state(false);

	// Fork (Agenda Maestros 4x4): ONE scroller (the document) and a shell per size. Phone
	// (< md, the primary case): sticky top bar + fixed bottom tab bar whose "Más" opens a
	// bottom sheet with every section. Tablet (md-lg): a sticky 76 px rail. Desktop (lg+):
	// the sticky full sidebar. The sheet is closed on every navigation...
	let moreOpen = $state(false);
	$effect(() => {
		void $page.url.pathname;
		moreOpen = false;
	});
	// ...and when the screen reaches md (a phone turned sideways): the sheet's content is
	// md:hidden, but the Dialog's own dark overlay is not, and would stay over the page.
	// The same media query places the toasts: top-center on phones (a bottom toast would
	// sit on the tab bar), bottom-right from md up. The built-in browser does not emit
	// `resize`; matchMedia's `change` is what fires.
	let isMd = $state(false);
	$effect(() => {
		const mq = window.matchMedia('(min-width: 768px)');
		const apply = () => {
			isMd = mq.matches;
			if (mq.matches) moreOpen = false;
		};
		apply();
		mq.addEventListener('change', apply);
		return () => mq.removeEventListener('change', apply);
	});
	let recordingsConfigured = $state(false);
	let version = $state('');

	const ISSUES_URL = 'https://github.com/Calnode/calnode/issues';
	const NEW_ISSUE_URL = 'https://github.com/Calnode/calnode/issues/new/choose';
	const RELEASES_URL = 'https://github.com/Calnode/calnode/releases';
	const PROFILE_HREF = `${base}/settings/profile`;

	const isLogin = $derived($page.route.id === '/login');
	const isPublicRoute = $derived(
		$page.route.id === '/login' ||
		$page.route.id === '/claim' ||
		$page.route.id === '/forgot-password' ||
		$page.route.id === '/reset-password' ||
		$page.route.id === '/invite/[token]'
	);

	// The rail's and the tab bar's short labels come from SHORT_LABELS in lib/nav.ts (keyed by
	// `label`), so the tests can check their length against the real data.
	const navItems: NavItem[] = [
		{
			section: 'Programación',
			href: `${base}/`,
			label: 'Inicio',
			adminOnly: false,
			exact: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg>`
		},
		{
			section: 'Programación',
			href: `${base}/event-types`,
			label: 'Tipos de atención',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>`
		},
		{
			section: 'Programación',
			href: `${base}/availability`,
			label: 'Disponibilidad',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>`
		},
		{
			section: 'Programación',
			href: `${base}/bookings`,
			label: 'Reservas',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>`
		},
		{
			section: 'Programación',
			href: `${base}/calendar`,
			label: 'Calendario',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/><rect x="8" y="14" width="2" height="2"/><rect x="13" y="14" width="2" height="2"/></svg>`
		},
		{
			section: 'Equipo',
			href: `${base}/recordings`,
			label: 'Grabaciones',
			adminOnly: true,
			requiresFeature: 'recordings',
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/></svg>`
		},
		{
			section: 'Equipo',
			href: `${base}/members`,
			label: 'Miembros',
			adminOnly: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>`
		},
		{
			section: 'Equipo',
			href: `${base}/teams`,
			label: 'Equipos',
			adminOnly: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 19a6 6 0 0 0-12 0"/><circle cx="8" cy="9" r="4"/><path d="M22 19a6 6 0 0 0-6-6 4 4 0 1 0-3-7"/></svg>`
		},
		{
			section: 'Desarrollador',
			href: `${base}/api-keys`,
			label: 'Claves de API',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>`
		},
		{
			section: 'Desarrollador',
			href: `${base}/connections`,
			label: 'Aplicaciones conectadas',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22v-5"/><path d="M9 8V2"/><path d="M15 8V2"/><path d="M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z"/></svg>`
		},
		{
			section: 'Desarrollador',
			href: `${base}/webhooks`,
			label: 'Webhooks',
			// Fork: only owner and admins. A member's webhook would repeat the owner's
			// WhatsApp to the client. Claves de API and Aplicaciones conectadas stay visible:
			// a member must still be able to revoke what they created.
			adminOnly: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>`
		},
		{
			section: null,
			href: `${base}/settings`,
			label: 'Configuración',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`
		}
	];

	// The fork's old "support" desk tier (which also saw Miembros) is retired: áreas
	// (Mentoría / Soporte) grant no extra page, so adminOnly means owner and admins only.
	const visibleNavItems = $derived(
		navItems.filter(
			(item) =>
				(!item.adminOnly || $currentUser?.is_admin) &&
				!($authStatus.demo_mode && item.label === 'Calendario') &&
				(item.requiresFeature !== 'recordings' || recordingsConfigured)
		)
	);

	// One rule for every surface (sidebar, rail, tab bar, sheet, top bar).
	const isActive = (item: NavItem) =>
		item.exact
			? $page.url.pathname === item.href || $page.url.pathname === base
			: $page.url.pathname.startsWith(item.href);

	// The phone top bar names the current section.
	const activeItem = $derived(visibleNavItems.find(isActive));
	const activeLabel = $derived(activeItem?.label ?? 'Agenda');

	// Phone tab bar: 4 destinations by priority (lib/nav.ts) + "Más". "Más" reads as active
	// while the sheet is open or when the current section is one that only lives there.
	const tabs = $derived(bottomTabs(visibleNavItems));
	const moreActive = $derived(moreOpen || (!!activeItem && !tabs.includes(activeItem)));

	onMount(async () => {
		if (isPublicRoute) {
			checking = false;
			return;
		}
		try {
			const me = await api.get<User>('/v1/users/me');
			currentUser.set(me);
			prefs.set(prefsFromUser(me));
			if (me.is_admin) {
				try {
					const lk = await api.get<{ configured: boolean }>('/v1/settings/livekit');
					recordingsConfigured = lk.configured;
				} catch {
					// Non-critical — Recordings just stays hidden from nav.
				}
			}
		} catch {
			window.location.href = '/admin/login';
			return;
		}
		try {
			authStatus.set(await api.get<AuthStatus>('/v1/auth/status'));
		} catch {
			// Non-critical — the demo banner and calendar/Zoom hiding just won't show.
		}
		try {
			const v = await api.get<{ version: string }>('/version');
			version = v.version;
		} catch {
			// Non-critical - the sidebar version link just won't show.
		}
		checking = false;
	});

	async function logout() {
		await fetch('/v1/auth/logout', { method: 'POST', credentials: 'same-origin' });
		window.location.href = '/admin/login';
	}

	function openReport() {
		moreOpen = false;
		reportOpen = true;
	}
</script>

{#snippet avatar(sizeClass: string)}
	<!-- Fixed-size clip container: the image fills it and is clipped by the
	     parent's overflow-hidden (not its own border-radius). A rounded,
	     object-cover image on its own composited layer gets mis-painted by
	     Chrome — a smeared tile over the sidebar — when the main panel
	     repaints on scroll; clipping via the parent box prevents that. -->
	<span class="flex {sizeClass} shrink-0 items-center justify-center overflow-hidden rounded-full bg-primary text-xs font-semibold text-primary-foreground">
		{#if $currentUser?.avatar_url}
			<img src={$currentUser.avatar_url} alt={$currentUser.name} class="h-full w-full object-cover" />
		{:else}
			{initials($currentUser?.name ?? 'U')}
		{/if}
	</span>
{/snippet}

{#snippet sidebarContent()}
	<!-- User section -->
	<a href={PROFILE_HREF} class="flex items-center gap-3 border-b border-sidebar-border px-4 py-3 hover:bg-sidebar-accent/60 transition-colors">
		{@render avatar('h-7 w-7')}
		<div class="min-w-0 flex-1">
			<p class="truncate text-sm font-medium text-sidebar-foreground">{$currentUser?.name ?? ''}</p>
		</div>
	</a>

	<!-- Nav. Compact spacing (py-1.5 links, mt-3 sections): the owner's 12 entries fit a
	     1366×768 laptop without the column scrolling itself. -->
	<nav class="flex-1 space-y-0.5 p-2">
		{#each visibleNavItems as item, i (item.href)}
			{#if item.section && item.section !== visibleNavItems[i - 1]?.section}
				<p class="mb-1 px-2.5 text-xs font-semibold uppercase tracking-wide text-sidebar-foreground/40 first:mt-0 mt-3">
					{item.section}
				</p>
			{:else if !item.section && visibleNavItems[i - 1]?.section}
				<div class="my-2 border-t border-sidebar-border"></div>
			{/if}
			{@const active = isActive(item)}
			<a
				href={item.href}
				aria-current={active ? 'page' : undefined}
				class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm font-medium transition-colors
					{active
						? 'bg-sidebar-accent text-sidebar-accent-foreground'
						: 'text-sidebar-foreground/70 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground'}"
			>
				<span class="shrink-0 {active ? 'opacity-100' : 'opacity-60'}">{@html item.icon}</span>
				{item.label}
			</a>
		{/each}
	</nav>

	<!-- Footer -->
	<div class="border-t border-sidebar-border p-2">
		<button
			onclick={openReport}
			class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-sidebar-foreground/45 transition-colors hover:bg-sidebar-accent/60 hover:text-sidebar-foreground/70"
		>
			<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0">
				<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
			</svg>
			Reportar un problema
		</button>
		<button
			onclick={logout}
			class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium text-sidebar-foreground/60 transition-colors hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground"
		>
			<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0">
				<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
				<polyline points="16 17 21 12 16 7"/>
				<line x1="21" y1="12" x2="9" y2="12"/>
			</svg>
			Cerrar sesión
		</button>
		{#if version}
			<a
				href={RELEASES_URL}
				target="_blank"
				rel="noopener noreferrer"
				class="mt-1 block px-2.5 py-1 text-xs text-sidebar-foreground/35 transition-colors hover:text-sidebar-foreground/60"
			>
				{version}
			</a>
		{/if}
	</div>
{/snippet}

<!-- Phones: top-center, pushed below the 48 px sticky top bar (sonner's own mobile offset
     is 16 px, which put every toast over the section title). From md: bottom-right. -->
<Toaster
	position={isMd ? 'bottom-right' : 'top-center'}
	mobileOffset={{ top: 'calc(3.5rem + env(safe-area-inset-top, 0px))' }}
/>

{#if isPublicRoute}
	{@render children()}
{:else if checking}
	<div class="flex min-h-dvh items-center justify-center text-sm text-muted-foreground">
		Cargando…
	</div>
{:else}
	<!-- .app-shell defines --app-bottom-inset (app.css). Normal flow: the document is the only scroller. -->
	<div class="app-shell flex min-h-dvh flex-col">
		<div class="flex flex-1 items-stretch">
			<!-- Desktop (lg+): the full sidebar. The column stretches with the document (its
			     tint runs the whole height); the sticky block inside stays in view, capped at
			     the viewport, and scrolls itself - bar hidden - only in a window shorter than
			     its content. -->
			<aside class="hidden w-56 shrink-0 border-r border-sidebar-border bg-sidebar lg:block">
				<div class="app-scroll-quiet sticky top-0 flex h-dvh flex-col overflow-y-auto">
					{@render sidebarContent()}
				</div>
			</aside>

			<!-- Tablet (md to lg): the rail (sticky, md:flex lg:hidden inside). -->
			<NavRail
				items={visibleNavItems}
				{isActive}
				profileHref={PROFILE_HREF}
				name={$currentUser?.name ?? ''}
				avatarUrl={$currentUser?.avatar_url}
				onReport={openReport}
				onLogout={logout}
			/>

			<div class="flex min-w-0 flex-1 flex-col">
				<!-- The demo banner sits in the content column, not above the side columns: above
				     them it pushed the sticky sidebar/rail down by its height, and their bottom
				     (Salir, the version) sat below the fold until the page was scrolled. -->
				{#if $authStatus.demo_mode}
					<DemoBanner />
				{/if}
				<!-- Phone top bar: the section's name and the avatar (→ profile). The menu lives in "Más". -->
				<header
					class="sticky top-0 z-30 flex items-center gap-3 border-b border-sidebar-border bg-sidebar/95 px-4 backdrop-blur md:hidden"
					style="padding-top: env(safe-area-inset-top, 0px)"
				>
					<p class="min-w-0 flex-1 truncate py-3 text-base font-semibold leading-6">{activeLabel}</p>
					<a
						href={PROFILE_HREF}
						class="-mr-2 flex h-11 w-11 shrink-0 items-center justify-center rounded-full"
						aria-label="Mi perfil"
					>
						{@render avatar('h-8 w-8')}
					</a>
				</header>
				<!-- Main content: no overflow rules; the bottom padding reserves the tab bar on phones. -->
				<main class="flex-1 bg-background">
					<div class="mx-auto max-w-4xl px-4 py-4 pb-[calc(var(--app-bottom-inset)+1rem)] md:px-8 md:py-8">
						{@render children()}
					</div>
				</main>
			</div>
		</div>
	</div>

	<!-- Phone (< md): the fixed bottom tab bar and the "Más" sheet. -->
	<NavTabBar {tabs} {isActive} {moreActive} onMore={() => (moreOpen = true)} />
	<NavSheet
		bind:open={moreOpen}
		items={visibleNavItems}
		{isActive}
		profileHref={PROFILE_HREF}
		name={$currentUser?.name ?? ''}
		avatarUrl={$currentUser?.avatar_url}
		{version}
		releasesUrl={RELEASES_URL}
		onReport={openReport}
		onLogout={logout}
	/>

	<Dialog.Root bind:open={reportOpen}>
		<Dialog.Content class="max-w-[calc(100%-2rem)] rounded-lg sm:max-w-md">
			<Dialog.Header>
				<Dialog.Title>Reportar un problema</Dialog.Title>
				<Dialog.Description>
					Por favor <strong>busca primero en los problemas existentes</strong> — puede que ya esté
					reportado o que ya se esté trabajando en ello. Este seguimiento es para <strong>errores
					reproducibles</strong> en Calnode; para ayuda de configuración o preguntas de
					“cómo hago…”, usa Discussions en su lugar.
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Footer class="gap-2 sm:justify-between">
				<a
					href={ISSUES_URL}
					target="_blank"
					rel="noopener noreferrer"
					onclick={() => (reportOpen = false)}
					class={buttonVariants({ variant: 'outline' })}
				>
					Buscar problemas existentes
				</a>
				<a
					href={NEW_ISSUE_URL}
					target="_blank"
					rel="noopener noreferrer"
					onclick={() => (reportOpen = false)}
					class={buttonVariants()}
				>
					Reportar un error
				</a>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}
