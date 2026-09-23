<script lang="ts">
	import { page } from '$app/stores';
	import { base } from '$app/paths';
	import { currentUser } from '$lib/stores';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	const navItems = [
		{ section: 'Tu cuenta', href: `${base}/settings/profile`, label: 'Perfil' },
		{ section: 'Tu cuenta', href: `${base}/settings/notifications`, label: 'Notificaciones' },
		{ section: 'Espacio de trabajo', href: `${base}/settings/branding`, label: 'Marca', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/email`, label: 'Correo', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/google`, label: 'Google OAuth', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/zoom`, label: 'Zoom', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/video`, label: 'Video', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/storage`, label: 'Almacenamiento', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/payments`, label: 'Pagos', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/ai`, label: 'IA', adminOnly: true },
		{ section: 'Espacio de trabajo', href: `${base}/settings/tracking`, label: 'Seguimiento', adminOnly: true },
	];

	const visibleNavItems = $derived(navItems.filter((item) => !item.adminOnly || $currentUser?.is_admin));
</script>

<svelte:head><title>Configuración — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Configuración</h1>
	<p class="mt-1 text-sm text-muted-foreground">Administra tu perfil, tus preferencias y tus integraciones.</p>
</div>

<div class="flex gap-8">
	<nav class="w-44 shrink-0">
		<ul class="space-y-0.5">
			{#each visibleNavItems as item, i}
				{#if item.section !== visibleNavItems[i - 1]?.section}
					<li class="mb-1 px-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground/60 first:mt-0 mt-4">
						{item.section}
					</li>
				{/if}
				{@const active = $page.url.pathname.startsWith(item.href)}
				<li>
					<a
						href={item.href}
						class="block rounded-md px-3 py-2 text-sm font-medium transition-colors
							{active
								? 'bg-accent text-accent-foreground'
								: 'text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground'}"
					>
						{item.label}
					</a>
				</li>
			{/each}
		</ul>
	</nav>

	<div class="min-w-0 flex-1">
		{@render children()}
	</div>
</div>
