<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type OAuthConnection } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';

	let items: OAuthConnection[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let revokeOpen = $state(false);
	let revokeTarget = $state<{ id: string; name: string } | null>(null);

	// Read from the browser rather than a config value: this is whatever host the admin
	// actually reached us on, which is the one an external app needs to be able to resolve.
	// A configured BASE_URL can be stale or internal; the address in the URL bar cannot.
	let origin = $state('');
	const mcpUrl = $derived(origin ? `${origin}/mcp` : '');

	let copied = $state(false);
	let copyTimer: ReturnType<typeof setTimeout> | null = null;

	async function copyMcpUrl() {
		if (!mcpUrl) return;
		try {
			await navigator.clipboard.writeText(mcpUrl);
			copied = true;
			if (copyTimer !== null) clearTimeout(copyTimer);
			copyTimer = setTimeout(() => (copied = false), 2000);
		} catch {
			// Clipboard access is blocked outside a secure context, and on a self-hosted
			// instance served over plain http that is the normal case - so say what to do
			// rather than just failing.
			toast.error('No se pudo copiar. Selecciona la URL y cópiala manualmente.');
		}
	}

	async function load() {
		try {
			const res = await api.get<{ items: OAuthConnection[] }>('/v1/oauth/connections');
			items = res.items;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		origin = window.location.origin;
		load();
	});

	function revoke(id: string, name: string) {
		revokeTarget = { id, name };
		revokeOpen = true;
	}

	async function doRevoke() {
		if (!revokeTarget) return;
		try {
			await api.del(`/v1/oauth/connections/${revokeTarget.id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}
</script>

<ConfirmDialog
	bind:open={revokeOpen}
	title="¿Desconectar la app?"
	description={revokeTarget
		? `¿Desconectar "${revokeTarget.name}"? Perderá el acceso a tus herramientas de agenda de inmediato y deberá volver a conectarse para recuperarlo.`
		: ''}
	confirmText="Desconectar"
	destructive
	onConfirm={doRevoke}
/>

<svelte:head><title>Aplicaciones conectadas — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Aplicaciones conectadas</h1>
	<p class="mt-1 text-sm text-muted-foreground">
		Agentes de IA y otras aplicaciones que has conectado a tus herramientas de agenda (MCP) mediante inicio de sesión.
	</p>
</div>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

<!-- Shown whether or not anything is connected: the URL is what you need to add the SECOND
     app too, and it used to vanish the moment the first one appeared. -->
<div class="mb-6 rounded-lg border bg-card p-5">
	<h2 class="text-sm font-semibold">Conectar una app</h2>
	<p class="mt-1 text-sm text-muted-foreground">
		Agrega esta URL como un conector personalizado en cualquier app compatible con MCP y luego inicia sesión cuando te lo pida.
		La app aparecerá abajo una vez que la apruebes.
	</p>

	<div class="mt-3 flex items-center gap-2">
		<Input
			readonly
			value={mcpUrl}
			aria-label="URL del conector MCP"
			onclick={(e) => e.currentTarget.select()}
			class="min-w-0 flex-1 bg-muted/40 font-mono"
		/>
		<Button variant="outline" onclick={copyMcpUrl} disabled={!mcpUrl}>
			{copied ? 'Copiado' : 'Copiar'}
		</Button>
	</div>

	<p class="mt-3 text-xs text-muted-foreground">
		En Claude: <span class="font-medium">Configuración → Conectores → Agregar conector personalizado</span>,
		pega la URL y luego inicia sesión con tu cuenta de Calnode para autorizarlo. El acceso está limitado a
		tu propio rol, y puedes revocarlo aquí en cualquier momento.
	</p>
</div>

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No hay aplicaciones conectadas</p>
		<p class="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
			Usa la URL de arriba para agregar Calnode a una app compatible con MCP. Una vez que la apruebes, aparecerá aquí.
		</p>
	</div>
{:else}
	<div class="overflow-hidden rounded-lg border bg-card">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Aplicación</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Conectada</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Último uso</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				<Tooltip.Provider>
					{#each items as c}
						<tr class="transition-colors hover:bg-muted/30">
							<td class="px-4 py-3 font-medium">{c.client_name}</td>
							<td class="px-4 py-3 text-muted-foreground">{fmtDate(c.created_at)}</td>
							<td class="px-4 py-3 text-muted-foreground">
								{#if c.last_used_at}{fmtDate(c.last_used_at)}{:else}Nunca{/if}
							</td>
							<td class="px-4 py-3 text-right">
								<Tooltip.Root>
									<Tooltip.Trigger
										class={buttonVariants({ variant: 'ghost', size: 'icon' })}
										onclick={() => revoke(c.id, c.client_name)}
									>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>Desconectar</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
