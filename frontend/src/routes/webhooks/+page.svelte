<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Webhook, type WebhookDelivery, type WebhookEventType, type WebhookSettings } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Badge } from '$lib/components/ui/badge';
	import { Combobox } from '$lib/components/ui/combobox';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { currentUser } from '$lib/stores';
	import { timezoneItems } from '$lib/prefs';
	import { toast } from 'svelte-sonner';

	let items: Webhook[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showCreate = $state(false);

	// GET /v1/webhooks/settings (fork): the morning reminder hour and zone (saved by the
	// owner here, else REMINDER_MORNING_HOUR / REMINDER_MORNING_TIMEZONE) and whether this
	// user's webhooks receive the whole team's bookings (the owner).
	let morningHour = $state('08:00');
	let morningZone = $state(''); // "" = each client's own zone
	let teamScope = $state(false);
	let canEditSettings = $state(false);
	let settingsLoaded = $state(false);
	// A failed load must not show the 08:00 defaults as if they were saved, nor tell the
	// owner "only the owner can change it": the block says it could not load, with a retry.
	let settingsError = $state('');
	let retryingSettings = $state(false);

	// The "Recordatorio de la mañana" form (owner only). America/Lima is the suggestion:
	// most of the team and clients are there.
	const SUGGESTED_ZONE = 'America/Lima';
	let mHour = $state('08:00');
	let mMode = $state<'client' | 'fixed'>('client');
	let mZone = $state(SUGGESTED_ZONE);
	let savingMorning = $state(false);
	const zoneItems = $derived(timezoneItems(mZone));
	const morningDirty = $derived(
		mHour !== morningHour || (mMode === 'client' ? morningZone !== '' : mZone !== morningZone)
	);
	const zoneName = (z: string) => z.replaceAll('_', ' ');
	const morningSummary = $derived(
		morningZone
			? `A las ${morningHour} de ${zoneName(morningZone)} para todos los clientes, el día de la sesión según esa zona.`
			: `A las ${morningHour} en la hora de cada cliente, el día de su sesión.`
	);

	function applySettings(s: WebhookSettings) {
		if (s.reminder_morning_hour) morningHour = s.reminder_morning_hour;
		morningZone = s.reminder_morning_timezone ?? '';
		teamScope = !!s.team_scope;
		canEditSettings = !!s.can_edit;
		mHour = morningHour;
		mMode = morningZone ? 'fixed' : 'client';
		mZone = morningZone || SUGGESTED_ZONE;
	}

	async function saveMorning() {
		if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(mHour)) {
			toast.error('Escribe la hora con el formato HH:MM, por ejemplo 07:00.');
			return;
		}
		if (mMode === 'fixed' && !mZone) {
			toast.error('Elige la zona horaria.');
			return;
		}
		savingMorning = true;
		try {
			const s = await api.put<WebhookSettings>('/v1/webhooks/settings', {
				reminder_morning_hour: mHour,
				reminder_morning_timezone: mMode === 'fixed' ? mZone : ''
			});
			applySettings(s);
			if (s.resync_ok === false) {
				toast.warning('Guardado, pero no se pudieron reprogramar todos los recordatorios ya planificados. Se completará al reiniciar el servidor.');
			} else {
				const n = s.resynced_bookings ?? 0;
				toast.success(n > 0 ? `Guardado. Se revisaron los recordatorios de ${n} ${n === 1 ? 'reserva próxima' : 'reservas próximas'}.` : 'Guardado.');
			}
		} catch (e: any) {
			toast.error(e.message || 'No se pudo guardar');
		} finally {
			savingMorning = false;
		}
	}

	// Event catalog (keys must match the backend's validWebhookEvents). The three
	// reminders are separate events on purpose: FunnelChat can't branch on "event",
	// so each WhatsApp message gets its own webhook → its own flow.
	type EventDef = { key: string; label: string; description?: string };
	const eventDefs: EventDef[] = $derived([
		{ key: 'booking.created', label: 'Cita agendada', description: 'confirmación en cuanto se reserva' },
		{ key: 'booking.cancelled', label: 'Cita cancelada' },
		{ key: 'booking.rescheduled', label: 'Cita reprogramada' },
		{ key: 'booking.reminder_morning', label: 'Recordatorio de la mañana', description: morningZone
			? `el día de la cita a las ${morningHour} de ${zoneName(morningZone)}; solo si la cita es más de 1 hora después`
			: `el mismo día a las ${morningHour}, hora del cliente; solo si la cita es más de 1 hora después` },
		{ key: 'booking.reminder_1h', label: 'Recordatorio 1 hora antes' },
		{ key: 'booking.reminder_5m', label: 'Recordatorio 5 minutos antes' },
		{ key: 'recording.completed', label: 'Grabación lista' },
		{ key: 'transcript.ready', label: 'Transcripción lista' },
		{ key: 'notes.ready', label: 'Notas de la reunión listas' }
	]);

	// Payload field catalog (keys must match the backend's webhook field keys).
	// `pii` flags personal data so the operator chooses consciously what leaves the system.
	type FieldDef = { key: string; label: string; pii?: boolean };
	const fieldGroups: { group: string; pii?: boolean; fields: FieldDef[] }[] = [
		// Fork: the finished text, per event type and moment (event type → pestaña WhatsApp).
		// It holds the client's name and, with {cancelar}, their cancel link.
		{ group: 'WhatsApp', pii: true, fields: [
			{ key: 'whatsapp_message', label: 'Mensaje de WhatsApp (texto listo, se edita en cada tipo de atención)', pii: true },
		] },
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
			{ key: 'start_local', label: 'Fecha y hora del cliente (texto)' },
			{ key: 'start_local_date', label: 'Fecha del cliente (texto)' },
			{ key: 'start_local_time', label: 'Hora del cliente (texto)' },
			{ key: 'start_local_long', label: 'Fecha y hora del cliente, en palabras (martes 9 de marzo de 2027, 09:00)' },
			// The zone the three texts above are in; can be the host's when the client's is unknown.
			{ key: 'start_local_timezone', label: 'Zona horaria de esa fecha y hora', pii: true },
			// A bearer link: whoever holds it can reschedule or cancel the booking.
			{ key: 'manage_url', label: 'Enlace para cambiar o cancelar', pii: true },
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
			{ key: 'attendee_phone', label: 'Teléfono del asistente (+51987654321)', pii: true },
			{ key: 'attendee_whatsapp', label: 'WhatsApp del asistente (51987654321)', pii: true },
		] },
		{ group: 'Cuestionario', pii: true, fields: [
			{ key: 'answers', label: 'Respuestas del cuestionario', pii: true },
		] },
	];
	const allFieldKeys = fieldGroups.flatMap((g) => g.fields.map((f) => f.key));
	// Fork: only the owner may send whatsapp_message (the server refuses it for anyone else
	// with a 409: a second webhook would repeat the client's message). Others never see the
	// field nor get it pre-ticked, or creating any webhook would fail.
	const isOwner = $derived(!!$currentUser?.is_owner);
	const visibleFieldGroups = $derived(isOwner ? fieldGroups : fieldGroups.filter((g) => g.group !== 'WhatsApp'));
	const defaultFieldKeys = () =>
		$currentUser?.is_owner ? [...allFieldKeys] : allFieldKeys.filter((k) => k !== 'whatsapp_message');

	// Event types a webhook may be limited to (fork: GET /v1/webhooks/event-types). For the
	// owner that is every type of the team, since the owner's webhooks get every booking.
	// Inactive ones are kept only to name the filters already saved.
	let eventTypes: WebhookEventType[] = $state([]);
	let eventTypesLoaded = $state(false);
	let eventTypesFailed = $state(false);
	const activeEventTypes = $derived(eventTypes.filter((t) => t.is_active && !t.archived));
	// id → how the list names a type, by the same rule as the checkboxes: the name, plus
	// "— de <dueño>" for someone else's, plus the slug when two would still read the same
	// (names are not unique: two mentors can each have a «Mentoría privada»).
	const eventTypeLabels = $derived.by(() => {
		const base = (t: WebhookEventType) => t.name + (!t.owned && t.owner_name ? ` — de ${t.owner_name}` : '');
		const seen = new Map<string, number>();
		for (const t of eventTypes) seen.set(base(t), (seen.get(base(t)) ?? 0) + 1);
		return new Map(eventTypes.map((t) => [t.id, (seen.get(base(t)) ?? 0) > 1 ? `${base(t)} (${t.slug})` : base(t)]));
	});

	// No event pre-selected: each FunnelChat flow gets its own webhook, and a default like
	// "created + cancelled" would also fire a "5 minutes before" flow at booking time.
	// typeMode 'all' = every event type (event_type_ids []), 'some' = only the ticked ones.
	type WebhookForm = { url: string; events: string[]; fields: string[]; typeMode: 'all' | 'some'; eventTypeIds: string[] };
	const emptyForm = (): WebhookForm => ({
		url: '', events: [], fields: defaultFieldKeys(), typeMode: 'all', eventTypeIds: []
	});
	let form = $state<WebhookForm>(emptyForm());

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

	// On failure the team note falls back to the signed-in user's owner flag, and the
	// morning block shows the error and a retry instead of made-up values.
	async function loadSettings() {
		try {
			applySettings(await api.get<WebhookSettings>('/v1/webhooks/settings'));
			settingsError = '';
		} catch (e: any) {
			teamScope = !!$currentUser?.is_owner;
			settingsError = e?.message || 'Error de conexión';
		} finally {
			settingsLoaded = true;
		}
	}

	async function retrySettings() {
		retryingSettings = true;
		try {
			await loadSettings();
		} finally {
			retryingSettings = false;
		}
	}

	// Fork: a webhook created before whatsapp_message existed does not send it. One click
	// adds it to its fields (PATCH keeps every other field as it was).
	const WA_EVENTS = ['booking.created', 'booking.cancelled', 'booking.rescheduled',
		'booking.reminder_morning', 'booking.reminder_1h', 'booking.reminder_5m'];
	let addingWA = $state<string | null>(null);
	const sendsWhatsApp = (wh: Webhook) => (wh.fields ?? []).includes('whatsapp_message');
	const carriesMessage = (wh: Webhook) => (wh.events ?? []).some((e) => WA_EVENTS.includes(e));
	async function addWhatsAppField(wh: Webhook) {
		addingWA = wh.id;
		try {
			await api.patch(`/v1/webhooks/${wh.id}`, { fields: [...(wh.fields ?? []), 'whatsapp_message'] });
			toast.success('Listo: este webhook ahora envía el mensaje de WhatsApp (data.whatsapp_message).');
			await load();
		} catch (e: any) {
			toast.error(e.message || 'No se pudo actualizar el webhook');
		} finally {
			addingWA = null;
		}
	}

	async function loadEventTypes() {
		try {
			const res = await api.get<{ items: WebhookEventType[] }>('/v1/webhooks/event-types');
			eventTypes = res.items ?? [];
		} catch {
			eventTypesFailed = true;
		} finally {
			eventTypesLoaded = true;
		}
	}

	onMount(() => {
		load();
		loadSettings();
		loadEventTypes();
	});

	async function create() {
		createError = '';
		if (!form.url) { createError = 'La URL es obligatoria.'; return; }
		if (!form.url.startsWith('https://')) { createError = 'La URL debe comenzar con https://'; return; }
		if (form.events.length === 0) { createError = 'Selecciona al menos un evento.'; return; }
		if (form.typeMode === 'some' && form.eventTypeIds.length === 0) {
			createError = 'Marca al menos un tipo de atención, o elige «Todos los tipos».';
			return;
		}
		creating = true;
		try {
			await api.post('/v1/webhooks', {
				url: form.url,
				events: form.events,
				fields: form.fields,
				event_type_ids: form.typeMode === 'some' ? form.eventTypeIds : []
			});
			form = emptyForm();
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

	function toggleEventType(id: string) {
		form.eventTypeIds = form.eventTypeIds.includes(id)
			? form.eventTypeIds.filter((t) => t !== id)
			: [...form.eventTypeIds, id];
	}

	// "Todos los tipos" or the names of the types a webhook is limited to. An inactive
	// webhook with no types left lost its last type (the server switched it off rather than
	// let it widen to every type), so it must not read "Todos los tipos".
	function typesLabel(wh: Webhook): string {
		const ids = wh.event_type_ids ?? [];
		if (ids.length === 0) return wh.is_active ? 'Todos los tipos' : 'Ninguno (no envía nada)';
		if (!eventTypesLoaded) return '…';
		// Only the names failed to load; the filter itself is intact, so say that.
		if (eventTypesFailed) {
			const n = ids.length === 1 ? '1 tipo' : `${ids.length} tipos`;
			return `Limitado a ${n} de atención (no se pudieron cargar los nombres; recarga la página)`;
		}
		// Loaded, yet an id is missing: a deleted type takes its filter row with it, so this
		// one still exists but is out of this user's reach - a type they no longer attend
		// (or, for the owner, one created after the page loaded).
		const missing = teamScope ? 'un tipo de atención nuevo (recarga la página)' : 'un tipo de atención que ya no atiendes';
		const withCopies = (id: string) =>
			(eventTypes.find((t) => t.id === id)?.copies ?? 0) > 0 ? ' (incluye la copia personal de cada persona que lo atiende)' : '';
		return ids.map((id) => (eventTypeLabels.get(id) ? eventTypeLabels.get(id) + withCopies(id) : missing)).join(', ');
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
		{#if teamScope}
			<p class="mt-1 text-sm text-muted-foreground">Tus webhooks reciben las citas de todo el equipo.</p>
		{:else if settingsLoaded}
			<p class="mt-1 text-sm text-muted-foreground">Las citas que te agendan también llegan a los webhooks del dueño del equipo. Si él ya envía un mensaje de WhatsApp, no lo repitas aquí o el cliente lo recibirá dos veces.</p>
		{/if}
	</div>
	<Button onclick={() => { showCreate = !showCreate; createError = ''; }}>
		{showCreate ? 'Cancelar' : 'Nuevo webhook'}
	</Button>
</div>

<!-- Fork: the morning reminder's hour and zone. One rule for the whole team (the owner's
     webhooks send every mentor's reminders), so only the owner edits it. -->
{#if settingsLoaded}
	<div class="mb-6 rounded-lg border bg-card p-4 sm:p-6">
		<h2 class="text-sm font-semibold">Recordatorio de la mañana</h2>
		<p class="mt-1 text-sm text-muted-foreground">
			Se envía el día de la sesión a esta hora, siempre antes del aviso de 1 hora. Si la sesión es
			demasiado temprano (menos de 1 hora después), ese día no se envía.
		</p>
		{#if settingsError}
			<div class="mt-3 flex flex-col gap-2 rounded-md bg-destructive/10 px-3 py-2 sm:flex-row sm:items-center sm:justify-between" role="alert">
				<p class="text-sm text-destructive">No se pudo cargar la configuración del recordatorio. Lo que tengas guardado sigue igual.</p>
				<Button variant="outline" size="sm" onclick={retrySettings} disabled={retryingSettings}>
					{retryingSettings ? 'Cargando…' : 'Reintentar'}
				</Button>
			</div>
		{:else if canEditSettings}
			<div class="mt-4 grid gap-4 sm:grid-cols-[auto_minmax(0,1fr)]">
				<div class="space-y-1.5">
					<Label for="morning-hour">Hora</Label>
					<Input id="morning-hour" type="time" step="300" bind:value={mHour} class="h-10 w-36 text-base sm:text-sm" />
				</div>
				<div class="min-w-0 space-y-2">
					<p class="text-sm font-medium">¿Hora de dónde?</p>
					<div class="flex flex-col gap-2 sm:flex-row" role="radiogroup" aria-label="¿Hora de dónde?">
						{#each [{ value: 'client', label: 'Hora de cada cliente', hint: `a las ${mHour || '08:00'} de su país` }, { value: 'fixed', label: 'Una zona fija', hint: 'la misma para todos' }] as opt (opt.value)}
							<label class="flex flex-1 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring {mMode === opt.value ? 'border-primary bg-primary/5' : 'bg-background hover:bg-accent/50'}">
								<input type="radio" name="morning-mode" bind:group={mMode} value={opt.value} class="sr-only" />
								<span class="font-medium">{opt.label}</span>
								<span class="text-xs text-muted-foreground">({opt.hint})</span>
							</label>
						{/each}
					</div>
					{#if mMode === 'fixed'}
						<div class="space-y-1.5">
							<Label>Zona horaria</Label>
							<Combobox items={zoneItems} bind:value={mZone} placeholder="Elige una zona" searchPlaceholder="Busca una ciudad, p. ej. Lima" class="h-10" />
							{#if mZone !== SUGGESTED_ZONE}
								<button type="button" class="text-xs text-primary underline-offset-2 hover:underline" onclick={() => (mZone = SUGGESTED_ZONE)}>Usar America/Lima</button>
							{/if}
							<p class="text-xs text-muted-foreground">
								Ejemplo: con 07:00 de Lima, un cliente de Madrid lo recibe a las 14:00 (en verano) y uno de
								Ciudad de México a las 06:00. Si para alguien esa hora cae después de su aviso de 1 hora, no lo recibe.
							</p>
						</div>
					{/if}
				</div>
			</div>
			<div class="mt-4 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
				<p class="text-xs text-muted-foreground">Ahora: {morningSummary}</p>
				<Button onclick={saveMorning} disabled={savingMorning || !morningDirty}>
					{savingMorning ? 'Guardando…' : 'Guardar recordatorio'}
				</Button>
			</div>
		{:else}
			<p class="mt-3 text-sm">{morningSummary}</p>
			<p class="mt-1 text-xs text-muted-foreground">Solo el dueño del equipo puede cambiarlo.</p>
		{/if}
	</div>
{/if}

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
			<p class="text-xs text-muted-foreground">Para WhatsApp (FunnelChat), marca un solo evento por webhook: cada mensaje va a su propio flujo.</p>
			{#each eventDefs as ev (ev.key)}
				<label class="flex cursor-pointer items-start gap-2 text-sm">
					<Checkbox
						class="mt-0.5"
						checked={form.events.includes(ev.key)}
						onCheckedChange={() => toggleEvent(ev.key)}
					/>
					<span class="min-w-0">
						<span class="font-medium">{ev.label}</span>{#if ev.description}<span class="text-muted-foreground">{` — ${ev.description}`}</span>{/if}
						<span class="block break-all font-mono text-xs text-muted-foreground">{ev.key}</span>
					</span>
				</label>
			{/each}
		</div>

		<div class="mb-4 space-y-2">
			<p class="text-sm font-medium">¿Para qué tipos de atención?</p>
			<p class="text-xs text-muted-foreground">Para WhatsApp, elige solo los tipos de atención que deben recibir este mensaje. Las demás citas no lo enviarán.</p>
			<div class="flex flex-col gap-2 sm:flex-row" role="radiogroup" aria-label="¿Para qué tipos de atención?">
				{#each [{ value: 'all', label: 'Todos los tipos', hint: 'también los que crees después' }, { value: 'some', label: 'Solo algunos tipos', hint: 'tú eliges cuáles' }] as opt (opt.value)}
					<label class="flex flex-1 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring {form.typeMode === opt.value ? 'border-primary bg-primary/5' : 'bg-background hover:bg-accent/50'}">
						<input type="radio" name="wh-type-mode" bind:group={form.typeMode} value={opt.value} class="sr-only" />
						<span class="font-medium">{opt.label}</span>
						<span class="text-xs text-muted-foreground">({opt.hint})</span>
					</label>
				{/each}
			</div>
			{#if form.typeMode === 'some'}
				<div class="space-y-1.5 rounded-md border bg-background p-3">
					{#if !eventTypesLoaded}
						<p class="text-sm text-muted-foreground">Cargando tipos de atención…</p>
					{:else if eventTypesFailed}
						<p class="text-sm text-destructive">No se pudo cargar la lista de tipos de atención. Recarga la página.</p>
					{:else if activeEventTypes.length === 0}
						<p class="text-sm text-muted-foreground">No hay tipos de atención activos.</p>
					{:else}
						{#each activeEventTypes as et (et.id)}
							<label class="flex cursor-pointer items-start gap-2 text-sm">
								<Checkbox
									class="mt-0.5"
									checked={form.eventTypeIds.includes(et.id)}
									onCheckedChange={() => toggleEventType(et.id)}
								/>
								<span class="min-w-0">
									<span class="font-medium">{et.name}</span>{#if !et.owned && et.owner_name}<span class="text-muted-foreground">{` — de ${et.owner_name}`}</span>{/if}
									<span class="block break-all font-mono text-xs text-muted-foreground">{et.slug}</span>
									<!-- Fork: each template (Mentoría, Soporte) also covers every person's copy (copies are
									     not listed: the filter matches them through the template). -->
									{#if et.copies && et.copies > 0}
										<span class="block text-xs text-muted-foreground">Incluye la copia personal de cada persona que lo atiende ({et.copies === 1 ? '1 copia' : `${et.copies} copias`}).</span>
									{/if}
								</span>
							</label>
						{/each}
					{/if}
				</div>
			{/if}
		</div>

		<div class="mb-4 space-y-3">
			<p class="text-sm font-medium">Datos a enviar <span class="font-normal text-muted-foreground">— desmarca lo que no quieras enviar</span></p>
			{#each visibleFieldGroups as grp}
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
	<div class="rounded-lg border bg-card overflow-x-auto">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">URL</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Eventos</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Tipos de atención</th>
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
							<td class="max-w-56 overflow-hidden text-ellipsis whitespace-nowrap px-4 py-3 font-mono text-xs" title={wh.url}>{wh.url}</td>
							<td class="px-4 py-3 text-xs text-muted-foreground">{(wh.events ?? []).join(', ')}</td>
							<td class="min-w-36 px-4 py-3 text-xs">{typesLabel(wh)}</td>
							<td class="px-4 py-3 text-xs text-muted-foreground">
								<span class="whitespace-nowrap">{(wh.fields ?? []).length} campos</span>
								{#if sendsWhatsApp(wh)}
									<span class="mt-1 block whitespace-nowrap text-green-700">incluye mensaje de WhatsApp</span>
								{:else if carriesMessage(wh) && isOwner}
									<Button variant="outline" size="sm" class="mt-1 h-7 px-2 text-xs" disabled={addingWA === wh.id} onclick={() => addWhatsAppField(wh)}>
										{addingWA === wh.id ? 'Añadiendo…' : 'Añadir mensaje de WhatsApp'}
									</Button>
								{/if}
							</td>
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
								<td colspan="7" class="px-4 py-3">
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
	{#if items.some((wh) => !wh.is_active)}
		<p class="mt-3 text-sm text-muted-foreground">Un webhook queda «Inactivo» si se elimina el único tipo de atención al que estaba limitado: así no empieza a avisar de todas las citas. Si todavía lo necesitas, elimínalo y créalo de nuevo.</p>
	{/if}
{/if}
