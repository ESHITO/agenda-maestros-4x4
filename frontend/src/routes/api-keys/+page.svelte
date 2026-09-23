<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type APIKey } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Tooltip from '$lib/components/ui/tooltip';

	let items: APIKey[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showCreate = $state(false);
	let newName = $state('');
	let creating = $state(false);
	let createError = $state('');
	let newKey = $state('');
	let revokeOpen = $state(false);
	let revokeTarget = $state<{ id: string; name: string } | null>(null);

	async function load() {
		try {
			const res = await api.get<{ items: APIKey[] }>('/v1/api-keys');
			items = res.items;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function create() {
		createError = '';
		if (!newName.trim()) { createError = 'El nombre es obligatorio.'; return; }
		creating = true;
		try {
			const res = await api.post<{ key: string }>('/v1/api-keys', { name: newName.trim() });
			newKey = res.key;
			newName = '';
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	function revoke(id: string, name: string) {
		revokeTarget = { id, name };
		revokeOpen = true;
	}

	async function doRevoke() {
		if (!revokeTarget) return;
		try {
			await api.del(`/v1/api-keys/${revokeTarget.id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}

	function copyKey() {
		navigator.clipboard.writeText(newKey).catch(() => {});
	}
</script>

<ConfirmDialog
	bind:open={revokeOpen}
	title="¿Revocar clave de API?"
	description={revokeTarget ? `¿Revocar "${revokeTarget.name}"? Cualquier integración que la use dejará de funcionar de inmediato.` : ''}
	confirmText="Revocar"
	destructive
	onConfirm={doRevoke}
/>

<svelte:head><title>Claves de API — Calnode</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Claves de API</h1>
		<p class="mt-1 text-sm text-muted-foreground">Autentica herramientas de CLI e integraciones.</p>
	</div>
	<Button onclick={() => { showCreate = !showCreate; createError = ''; newKey = ''; }}>
		{showCreate ? 'Cancelar' : 'Nueva clave'}
	</Button>
</div>

{#if newKey}
	<div class="mb-6 rounded-lg border border-green-200 bg-green-50 p-4">
		<p class="mb-2 text-sm font-medium text-green-800">Clave creada — cópiala ahora. No se volverá a mostrar.</p>
		<div class="mb-3 rounded-md border bg-white px-3 py-2 font-mono text-xs text-foreground break-all">{newKey}</div>
		<Button variant="outline" size="sm" onclick={copyKey}>
			Copiar al portapapeles
		</Button>
	</div>
{/if}

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">Nueva clave de API</h2>
		{#if createError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{createError}</p>{/if}
		<div class="mb-4 max-w-sm space-y-1.5">
			<Label for="key-name">Nombre de la clave</Label>
			<Input
				id="key-name"
				bind:value={newName}
				placeholder="p. ej., pipeline de CI/CD"
			/>
		</div>
		<Button onclick={create} disabled={creating}>
			{creating ? 'Creando…' : 'Crear clave'}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No hay claves de API</p>
		<p class="mt-1 text-sm text-muted-foreground">Crea una clave para autenticar herramientas de CLI e integraciones.</p>
	</div>
{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Nombre</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Creación</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Último uso</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				<Tooltip.Provider>
					{#each items as k}
						<tr class="transition-colors hover:bg-muted/30">
							<td class="px-4 py-3 font-medium">{k.name}</td>
							<td class="px-4 py-3 text-muted-foreground">{fmtDate(k.created_at)}</td>
							<td class="px-4 py-3 text-muted-foreground">
								{#if k.last_used_at}{fmtDate(k.last_used_at)}{:else}Nunca{/if}
							</td>
							<td class="px-4 py-3 text-right">
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => revoke(k.id, k.name)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>Revocar clave</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
