<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Webhook, type WebhookDelivery } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';

	let items: Webhook[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showCreate = $state(false);

	const allEvents = [
		'booking.created',
		'booking.cancelled',
		'booking.rescheduled',
		'recording.completed',
		'transcript.ready',
		'notes.ready'
	];

	// Payload field catalog (keys must match the backend's webhook field keys).
	// `pii` flags personal data so the operator chooses consciously what leaves the system.
	type FieldDef = { key: string; label: string; pii?: boolean };
	const fieldGroups: { group: string; pii?: boolean; fields: FieldDef[] }[] = [
		{ group: 'Reserva', fields: [
			{ key: 'id', label: 'Referencia de la reserva' },
			{ key: 'status', label: 'Estado' },
			{ key: 'start_at', label: 'Hora de inicio' },
			{ key: 'end_at', label: 'Hora de fin' },
			{ key: 'created_at', label: 'Creado el' },
			{ key: 'location_value', label: 'Ubicación' },
			{ key: 'cancellation_reason', label: 'Motivo de cancelación' },
			{ key: 'previous_start_at', label: 'Inicio anterior (reprogramación)' },
			{ key: 'previous_end_at', label: 'Fin anterior (reprogramación)' },
		] },
		{ group: 'Pago', fields: [
			{ key: 'payment_status', label: 'Estado del pago' },
			{ key: 'amount_paid_cents', label: 'Monto pagado (centavos)' },
			{ key: 'amount_paid_currency', label: 'Moneda' },
		] },
		{ group: 'Tipo de atención', fields: [
			{ key: 'event_type_slug', label: 'Slug del tipo de atención' },
			{ key: 'event_type_name', label: 'Nombre del tipo de atención' },
		] },
		{ group: 'Anfitrión', fields: [
			{ key: 'host_id', label: 'ID del anfitrión' },
			{ key: 'host_name', label: 'Nombre del anfitrión' },
			{ key: 'host_email', label: 'Correo del anfitrión', pii: true },
		] },
		{ group: 'Asistente', pii: true, fields: [
			{ key: 'attendee_name', label: 'Nombre del asistente', pii: true },
			{ key: 'attendee_email', label: 'Correo del asistente', pii: true },
			{ key: 'attendee_timezone', label: 'Zona horaria del asistente', pii: true },
		] },
		{ group: 'Cuestionario', pii: true, fields: [
			{ key: 'answers', label: 'Respuestas del cuestionario', pii: true },
		] },
	];
	const allFieldKeys = fieldGroups.flatMap((g) => g.fields.map((f) => f.key));

	let form = $state<{ url: string; events: string[]; fields: string[] }>({
		url: '', events: ['booking.created', 'booking.cancelled'], fields: [...allFieldKeys]
	});

	// Delivery log (lazy-loaded per webhook).
	let openDeliveries = $state<string | null>(null);
	let deliveries = $state<WebhookDelivery[]>([]);
	let deliveriesLoading = $state(false);
	let creating = $state(false);
	let createError = $state('');
	let deleteOpen = $state(false);
	let deleteId = $state('');

	async function load() {
		try {
			const res = await api.get<{ items: Webhook[] }>('/v1/webhooks');
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
		if (!form.url) { createError = 'La URL es obligatoria.'; return; }
		if (!form.url.startsWith('https://')) { createError = 'La URL debe comenzar con https://'; return; }
		if (form.events.length === 0) { createError = 'Selecciona al menos un evento.'; return; }
		creating = true;
		try {
			await api.post('/v1/webhooks', { url: form.url, events: form.events, fields: form.fields });
			form = { url: '', events: ['booking.created', 'booking.cancelled'], fields: [...allFieldKeys] };
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	function del(id: string) {
		deleteId = id;
		deleteOpen = true;
	}

	async function doDelete() {
		try {
			await api.del(`/v1/webhooks/${deleteId}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}

	function toggleEvent(ev: string) {
		if (form.events.includes(ev)) {
			form.events = form.events.filter((e) => e !== ev);
		} else {
			form.events = [...form.events, ev];
		}
	}

	function toggleField(key: string) {
		form.fields = form.fields.includes(key)
			? form.fields.filter((f) => f !== key)
			: [...form.fields, key];
	}

	async function toggleDeliveries(id: string) {
		if (openDeliveries === id) { openDeliveries = null; return; }
		openDeliveries = id;
		deliveries = [];
		deliveriesLoading = true;
		try {
			const res = await api.get<{ items: WebhookDelivery[] }>(`/v1/webhooks/${id}/deliveries`);
			deliveries = res.items ?? [];
		} catch (e: any) {
			error = e.message;
		} finally {
			deliveriesLoading = false;
		}
	}
</script>

<ConfirmDialog
	bind:open={deleteOpen}
	title="¿Eliminar webhook?"
	description="El endpoint dejará de recibir eventos de inmediato."
	confirmText="Eliminar"
	destructive
	onConfirm={doDelete}
/>

<svelte:head><title>Webhooks — Calnode</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Webhooks</h1>
		<p class="mt-1 text-sm text-muted-foreground">Recibe notificaciones en tiempo real de eventos de reservas.</p>
	</div>
	<Button onclick={() => { showCreate = !showCreate; createError = ''; }}>
		{showCreate ? 'Cancelar' : 'Nuevo webhook'}
	</Button>
</div>

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">Nuevo webhook</h2>
		{#if createError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{createError}</p>{/if}

		<div class="mb-4 space-y-1.5">
			<Label for="wh-url">URL del endpoint</Label>
			<Input
				id="wh-url"
				type="url"
				bind:value={form.url}
				placeholder="https://your-server.com/hooks/calnode"
			/>
		</div>

		<div class="mb-4 space-y-2">
			<p class="text-sm font-medium">Eventos a enviar</p>
			{#each allEvents as ev}
				<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
					<Checkbox
						checked={form.events.includes(ev)}
						onCheckedChange={() => toggleEvent(ev)}
					/>
					<span>{ev}</span>
				</label>
			{/each}
		</div>

		<div class="mb-4 space-y-3">
			<p class="text-sm font-medium">Datos a enviar <span class="font-normal text-muted-foreground">— desmarca lo que no quieras enviar</span></p>
			{#each fieldGroups as grp}
				<div class="space-y-1.5">
					<p class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
						{grp.group}{#if grp.pii}<span class="ml-1.5 font-normal normal-case text-amber-600">· datos personales</span>{/if}
					</p>
					<div class="grid grid-cols-2 gap-x-4 gap-y-1">
						{#each grp.fields as f}
							<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
								<Checkbox checked={form.fields.includes(f.key)} onCheckedChange={() => toggleField(f.key)} />
								<span>{f.label}{#if f.pii}<span class="ml-1 text-[10px] font-medium uppercase text-amber-600">PII</span>{/if}</span>
							</label>
						{/each}
					</div>
				</div>
			{/each}
		</div>

		<Button onclick={create} disabled={creating}>
			{creating ? 'Creando…' : 'Crear webhook'}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No hay webhooks</p>
		<p class="mt-1 text-sm text-muted-foreground">Agrega un webhook para recibir notificaciones en tiempo real de eventos de reservas.</p>
	</div>
{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">URL</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Eventos</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Campos</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Estado</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Creado</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				<Tooltip.Provider>
					{#each items as wh}
						<tr class="transition-colors hover:bg-muted/30">
							<td class="max-w-xs overflow-hidden text-ellipsis whitespace-nowrap px-4 py-3 font-mono text-xs">{wh.url}</td>
							<td class="px-4 py-3 text-xs text-muted-foreground">{(wh.events ?? []).join(', ')}</td>
							<td class="px-4 py-3 text-xs text-muted-foreground">{(wh.fields ?? []).length} campos</td>
							<td class="px-4 py-3">
								{#if wh.is_active}
									<Badge class="bg-green-50 text-green-700 border-green-200">Activo</Badge>
								{:else}
									<Badge variant="secondary">Inactivo</Badge>
								{/if}
							</td>
							<td class="px-4 py-3 text-muted-foreground">{fmtDate(wh.created_at)}</td>
							<td class="px-4 py-3 text-right whitespace-nowrap">
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => toggleDeliveries(wh.id)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>{openDeliveries === wh.id ? 'Ocultar entregas' : 'Entregas recientes'}</Tooltip.Content>
								</Tooltip.Root>
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => del(wh.id)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>Eliminar webhook</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
						{#if openDeliveries === wh.id}
							<tr class="bg-muted/20">
								<td colspan="6" class="px-4 py-3">
									{#if deliveriesLoading}
										<p class="text-xs text-muted-foreground">Cargando entregas…</p>
									{:else if deliveries.length === 0}
										<p class="text-xs text-muted-foreground">Aún no hay entregas para este webhook.</p>
									{:else}
										<table class="w-full text-xs">
											<thead>
												<tr class="text-left text-muted-foreground">
													<th class="py-1 pr-4 font-medium">Evento</th>
													<th class="py-1 pr-4 font-medium">Estado</th>
													<th class="py-1 pr-4 font-medium">HTTP</th>
													<th class="py-1 pr-4 font-medium">Intentos</th>
													<th class="py-1 font-medium">Último intento</th>
												</tr>
											</thead>
											<tbody class="divide-y divide-border/50">
												{#each deliveries as d}
													<tr>
														<td class="py-1 pr-4 font-mono">{d.event}</td>
														<td class="py-1 pr-4">
															<span class={d.status === 'delivered' ? 'text-green-700' : d.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'}>{d.status}</span>
														</td>
														<td class="py-1 pr-4">{d.response_status ?? '—'}</td>
														<td class="py-1 pr-4">{d.attempt_count}</td>
														<td class="py-1 text-muted-foreground">{d.last_attempted_at ? new Date(d.last_attempted_at).toLocaleString() : '—'}</td>
													</tr>
												{/each}
											</tbody>
										</table>
									{/if}
								</td>
							</tr>
						{/if}
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
