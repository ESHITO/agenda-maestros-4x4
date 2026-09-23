<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Booking } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { prefs, fmtDateTime, fmtTime } from '$lib/prefs';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import * as Select from '$lib/components/ui/select';
	import { DatePicker } from '$lib/components/ui/date-picker';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';

	let items: Booking[] = $state([]);
	let loading = $state(true);
	let error = $state('');

	// Members see only their own hosted bookings. Owners/admins — and support, whose
	// every request starts with finding someone else's booking — can switch to a
	// workspace-wide view (?scope=all).
	//
	// This gate must match parseBookingListFilter in booking_handler.go, which grants
	// scope=all to IsAdmin || IsSupport. Leaving it on is_admin alone made the server
	// side unreachable: the toggle never rendered, the query never carried scope=all,
	// and a support user saw only the bookings they host themselves (usually none).
	const canSeeAll = $derived(($currentUser?.is_admin ?? false) || ($currentUser?.is_support ?? false));
	let scope = $state<'mine' | 'all'>('mine');

	// Filtering, sorting and paging all happen in SQL now. They used to happen here,
	// over a response that contained every booking the user could see - which meant
	// the page got slower with every booking ever made, and the server did unbounded
	// work to render 25 rows.
	let timeFilter = $state<'upcoming' | 'past'>('upcoming');
	let fEventType = $state('');
	let fHost = $state('');
	let fTeam = $state('');
	let fStatus = $state('');

	const PAGE_SIZE = 25;
	let offset = $state(0);
	let total = $state(0);
	let counts = $state({ upcoming: 0, past: 0 });

	// Options for the filter selects, fetched once.
	let eventTypes = $state<{ slug: string; name: string }[]>([]);
	let members = $state<{ id: string; name: string }[]>([]);
	let teams = $state<{ id: string; name: string }[]>([]);

	const hasFilters = $derived(!!(fEventType || fHost || fTeam || fStatus));
	const pageStart = $derived(total === 0 ? 0 : offset + 1);
	const pageEnd = $derived(Math.min(offset + items.length, total));
	const eventTypeName = $derived(
		(slug: string) => eventTypes.find((e) => e.slug === slug)?.name ?? slug
	);

	let reschedulingId = $state<string | null>(null);
	let reschedulingSlug = $state('');
	let rescheduleDate = $state('');
	let slots: { start: string; end: string }[] = $state([]);
	let slotsLoading = $state(false);
	let slotsError = $state('');
	let selectedSlot = $state('');
	let rescheduling = $state(false);
	let rescheduleError = $state('');

	type AnswerItem = { label: string; type: string; value: string };
	let expandedId = $state<string | null>(null);
	let answersCache: Record<string, AnswerItem[]> = $state({});
	let answersLoading: Record<string, boolean> = $state({});

	async function toggleExpand(id: string) {
		if (expandedId === id) { expandedId = null; return; }
		expandedId = id;
		if (answersCache[id] === undefined) {
			answersLoading[id] = true;
			try {
				const res = await api.get<{ items: AnswerItem[] }>(`/v1/bookings/${id}/answers`);
				answersCache[id] = res.items ?? [];
			} catch {
				answersCache[id] = [];
			}
			answersLoading[id] = false;
		}
	}

	function query(): string {
		const p = new URLSearchParams();
		if (scope === 'all' && canSeeAll) p.set('scope', 'all');
		p.set('when', timeFilter);
		// Past reads most-recent-first, upcoming soonest-first. Server-side now: sorting
		// a page in the browser would only ever sort that page.
		p.set('order', timeFilter === 'past' ? 'desc' : 'asc');
		if (fEventType) p.set('event_type', fEventType);
		if (fHost) p.set('host', fHost);
		if (fTeam) p.set('team', fTeam);
		if (fStatus) p.set('status', fStatus);
		p.set('limit', String(PAGE_SIZE));
		p.set('offset', String(offset));
		return p.toString();
	}

	// Filter changes can outrun their responses: pick an event type, then a status a
	// moment later, and the slower first reply would otherwise land last and overwrite
	// the newer one. Only the most recent request is allowed to apply.
	let loadSeq = 0;

	async function load() {
		const seq = ++loadSeq;
		try {
			const res = await api.get<{
				items: Booking[];
				total: number;
				counts: { upcoming: number; past: number };
			}>(`/v1/bookings?${query()}`);
			if (seq !== loadSeq) return;
			items = res.items ?? [];
			total = res.total ?? 0;
			counts = res.counts ?? { upcoming: 0, past: 0 };
			error = '';

			// The current page can fall off the end of the result set: cancel the only
			// booking on the last page and offset now points past it, which renders an
			// empty table under a "Showing 26-25 of 25" label with Next still enabled.
			// Step back to the last real page instead. offset > 0 bounds the recursion.
			if (items.length === 0 && offset > 0 && total > 0) {
				offset = Math.max(0, (Math.ceil(total / PAGE_SIZE) - 1) * PAGE_SIZE);
				await load();
				return;
			}
		} catch (e: any) {
			if (seq !== loadSeq) return;
			error = e.message;
		} finally {
			if (seq === loadSeq) loading = false;
		}
	}

	// Any change to what is being asked for starts again at the first page - staying on
	// page 3 of a filter that now matches four things shows an empty table.
	async function reload() {
		offset = 0;
		loading = true;
		await load();
	}

	async function setScope(s: 'mine' | 'all') {
		if (scope === s) return;
		scope = s;
		// Host and team only mean anything across the workspace.
		if (s === 'mine') { fHost = ''; fTeam = ''; }
		await reload();
	}

	async function setTimeFilter(t: 'upcoming' | 'past') {
		if (timeFilter === t) return;
		timeFilter = t;
		await reload();
	}

	function clearFilters() {
		fEventType = fHost = fTeam = fStatus = '';
		reload();
	}

	async function goTo(newOffset: number) {
		offset = Math.max(0, newOffset);
		loading = true;
		await load();
	}

	// Filter options. Failures are silent: a missing dropdown is a smaller problem than
	// an error banner over a working table, and members can't list users anyway.
	async function loadFilterOptions() {
		try {
			const res = await api.get<{ items: { slug: string; name: string }[] }>('/v1/event-types');
			eventTypes = res.items ?? [];
		} catch { /* leave the dropdown empty */ }
		if (!canSeeAll) return;
		try {
			// /v1/users returns a bare array, not an { items } envelope like the others.
			// Archived members are excluded by default, which is what we want here.
			members = (await api.get<{ id: string; name: string }[]>('/v1/users')) ?? [];
		} catch { /* leave the dropdown empty */ }
		try {
			const res = await api.get<{ items: { id: string; name: string }[] }>('/v1/teams');
			teams = res.items ?? [];
		} catch { /* leave the dropdown empty */ }
	}

	// Refresh re-fetches in place (no full-page "Loading…" flash) — just spins the button.
	let refreshing = $state(false);
	async function refresh() {
		if (refreshing) return;
		refreshing = true;
		error = '';
		await load();
		refreshing = false;
	}

	onMount(() => {
		load();
		loadFilterOptions();
	});

	let confirmOpen = $state(false);
	let pendingCancelId = $state<string | null>(null);

	function requestCancel(id: string) {
		pendingCancelId = id;
		confirmOpen = true;
	}

	async function cancel() {
		const id = pendingCancelId;
		if (!id) return;
		try {
			await api.post(`/v1/bookings/${id}/cancel`, { reason: 'cancelled by admin' });
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			confirmOpen = false;
			pendingCancelId = null;
		}
	}

	function startReschedule(b: Booking) {
		reschedulingId = b.id;
		reschedulingSlug = b.event_type_slug ?? '';
		rescheduleDate = '';
		slots = [];
		selectedSlot = '';
		rescheduleError = '';
		slotsError = '';
	}

	function cancelReschedule() {
		reschedulingId = null;
	}

	$effect(() => {
		if (rescheduleDate) loadSlots();
	});

	async function loadSlots() {
		if (!rescheduleDate) return;
		slotsLoading = true;
		slotsError = '';
		slots = [];
		selectedSlot = '';
		try {
			const tz = $prefs.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone;
			const res = await api.get<{ slots: { start: string; end: string }[] }>(
				`/v1/event-types/${reschedulingSlug}/slots?from=${rescheduleDate}&to=${rescheduleDate}&tz=${encodeURIComponent(tz)}`
			);
			slots = res.slots ?? [];
		} catch (e: any) {
			slotsError = e.message;
		} finally {
			slotsLoading = false;
		}
	}

	async function confirmReschedule() {
		if (!selectedSlot || !reschedulingId) return;
		rescheduling = true;
		rescheduleError = '';
		try {
			await api.patch(`/v1/bookings/${reschedulingId}/reschedule`, { start_at: selectedSlot });
			reschedulingId = null;
			await load();
		} catch (e: any) {
			rescheduleError = e.message;
		} finally {
			rescheduling = false;
		}
	}

	function fmt(iso: string) { return fmtDateTime(iso, $prefs); }
	function fmtSlotTime(iso: string) { return fmtTime(iso, $prefs); }
	function fmtMoney(cents?: number, cur?: string) {
		if (!cents) return '';
		const amt = (cents / 100).toFixed(2);
		const c = (cur || 'usd').toUpperCase();
		const sym: Record<string, string> = { USD: '$', EUR: '€', GBP: '£', AUD: 'A$', CAD: 'C$', NZD: 'NZ$' };
		return sym[c] ? sym[c] + amt : amt + ' ' + c;
	}
	const payLabel: Record<string, string> = { paid: 'Pagado', refunded: 'Reembolsado', pending: 'Pago pendiente' };
	const statusLabel: Record<string, string> = { confirmed: 'Confirmada', cancelled: 'Cancelada', rescheduled: 'Reprogramada' };

	function todayISO() {
		return new Date().toISOString().slice(0, 10);
	}
</script>

<svelte:head><title>Reservas — Calnode</title></svelte:head>

<div class="mb-8 flex items-start justify-between gap-4">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Reservas</h1>
		<p class="mt-1 text-sm text-muted-foreground">
			{scope === 'all' ? 'Todas las reuniones del equipo de trabajo.' : 'Reuniones de las que eres anfitrión.'}
		</p>
	</div>
	<div class="flex shrink-0 items-center gap-2">
		<Button variant="outline" size="sm" onclick={refresh} disabled={refreshing} aria-label="Actualizar reservas">
			<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class={refreshing ? 'animate-spin' : ''}><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
			Actualizar
		</Button>
		{#if canSeeAll}
			<div class="inline-flex rounded-md border p-0.5">
				<button
					class="rounded px-3 py-1 text-sm font-medium transition-colors {scope === 'mine' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
					onclick={() => setScope('mine')}
				>Mis reservas</button>
				<button
					class="rounded px-3 py-1 text-sm font-medium transition-colors {scope === 'all' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
					onclick={() => setScope('all')}
				>Todas las reservas</button>
			</div>
		{/if}
	</div>
</div>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

<!-- "No bookings yet" is a claim about the workspace, so it must not be shown when the
     request failed and the counts are simply unknown - that reads as data loss. -->
{#if counts.upcoming === 0 && counts.past === 0 && !hasFilters && !loading && !error}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">Aún no hay reservas</p>
		<p class="mt-1 text-sm text-muted-foreground">Las reservas aparecerán aquí cuando los asistentes agenden tiempo contigo.</p>
	</div>
{:else}
	<div class="mb-4 flex flex-wrap items-center gap-2">
		<div class="inline-flex rounded-md border p-0.5 text-sm">
			<button type="button" class="rounded px-3 py-1 transition-colors {timeFilter === 'upcoming' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => setTimeFilter('upcoming')}>Próximas ({counts.upcoming})</button>
			<button type="button" class="rounded px-3 py-1 transition-colors {timeFilter === 'past' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => setTimeFilter('past')}>Pasadas ({counts.past})</button>
		</div>

		<div class="ml-auto flex flex-wrap items-center gap-2">
			<Select.Root type="single" bind:value={fEventType} onValueChange={reload}>
				<Select.Trigger class="h-9 w-[170px]" aria-label="Filtrar por tipo de atención">
					{fEventType ? eventTypeName(fEventType) : 'Todos los tipos de atención'}
				</Select.Trigger>
				<Select.Content>
					<Select.Item value="" label="Todos los tipos de atención">Todos los tipos de atención</Select.Item>
					{#each eventTypes as et}
						<Select.Item value={et.slug} label={et.name}>{et.name}</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>

			{#if canSeeAll && scope === 'all'}
				<Select.Root type="single" bind:value={fHost} onValueChange={reload}>
					<Select.Trigger class="h-9 w-[150px]" aria-label="Filtrar por anfitrión">
						{members.find((m) => m.id === fHost)?.name ?? 'Todos los anfitriones'}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="" label="Todos los anfitriones">Todos los anfitriones</Select.Item>
						{#each members as m}
							<Select.Item value={m.id} label={m.name}>{m.name}</Select.Item>
						{/each}
					</Select.Content>
				</Select.Root>

				{#if teams.length > 0}
					<Select.Root type="single" bind:value={fTeam} onValueChange={reload}>
						<Select.Trigger class="h-9 w-[140px]" aria-label="Filtrar por equipo">
							{teams.find((tm) => tm.id === fTeam)?.name ?? 'Todos los equipos'}
						</Select.Trigger>
						<Select.Content>
							<Select.Item value="" label="Todos los equipos">Todos los equipos</Select.Item>
							{#each teams as tm}
								<Select.Item value={tm.id} label={tm.name}>{tm.name}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				{/if}
			{/if}

			<Select.Root type="single" bind:value={fStatus} onValueChange={reload}>
				<Select.Trigger class="h-9 w-[140px]" aria-label="Filtrar por estado">
					{fStatus || 'Cualquier estado'}
				</Select.Trigger>
				<Select.Content>
					<Select.Item value="" label="Cualquier estado">Cualquier estado</Select.Item>
					<Select.Item value="confirmed" label="Confirmada">Confirmada</Select.Item>
					<Select.Item value="rescheduled" label="Reprogramada">Reprogramada</Select.Item>
					<Select.Item value="cancelled" label="Cancelada">Cancelada</Select.Item>
				</Select.Content>
			</Select.Root>

			{#if hasFilters}
				<Button variant="ghost" size="sm" onclick={clearFilters}>Limpiar</Button>
			{/if}
		</div>
	</div>

	{#if loading}
		<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
	{:else if items.length === 0}
		<div class="rounded-lg border border-dashed bg-card p-12 text-center">
			<p class="text-sm text-muted-foreground">
				{hasFilters ? 'No hay reservas que coincidan con estos filtros.' : (timeFilter === 'past' ? 'No hay reservas pasadas.' : 'No hay próximas reservas.')}
			</p>
			{#if hasFilters}
				<Button variant="outline" size="sm" class="mt-3" onclick={clearFilters}>Limpiar filtros</Button>
			{/if}
		</div>
	{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Asistente</th>
					{#if scope === 'all'}<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Anfitrión</th>{/if}
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Tipo de atención</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Hora de inicio</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Estado</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				{#each items as b}
					<tr class="transition-colors hover:bg-muted/30">
						<td class="px-4 py-3">
							{#if b.attendees && b.attendees.length > 0}
								<div class="font-medium">{b.attendees[0].name}</div>
								<div class="text-xs text-muted-foreground">{b.attendees[0].email}</div>
							{:else}
								<span class="text-muted-foreground">—</span>
							{/if}
						</td>
						{#if scope === 'all'}<td class="px-4 py-3 text-muted-foreground">{b.host_name || '—'}</td>{/if}
						<td class="px-4 py-3 font-mono text-xs text-muted-foreground">{b.event_type_slug ?? '—'}</td>
						<td class="px-4 py-3 text-muted-foreground">{fmt(b.start_at)}</td>
						<td class="px-4 py-3">
							<div class="flex flex-wrap items-center gap-1.5">
								{#if b.status === 'confirmed'}
									<Badge class="bg-green-50 text-green-700 border-green-200">{statusLabel[b.status] ?? b.status}</Badge>
								{:else if b.status === 'cancelled'}
									<Badge variant="destructive" class="bg-destructive/10 text-destructive border-transparent">{statusLabel[b.status] ?? b.status}</Badge>
								{:else}
									<Badge variant="secondary">{statusLabel[b.status] ?? b.status}</Badge>
								{/if}
								{#if b.payment_status === 'paid'}
									<Badge class="border-emerald-200 bg-emerald-50 text-emerald-700">{fmtMoney(b.amount_paid_cents, b.amount_paid_currency)}</Badge>
								{:else if b.payment_status === 'refunded'}
									<Badge variant="secondary" class="text-muted-foreground">reembolsado</Badge>
								{:else if b.payment_status === 'pending'}
									<Badge class="border-amber-200 bg-amber-50 text-amber-700">no pagado</Badge>
								{/if}
							</div>
						</td>
						<td class="px-4 py-3">
							<Tooltip.Provider>
								<div class="flex items-center justify-end gap-1">
									<Tooltip.Root>
										<Tooltip.Trigger
											class={buttonVariants({ variant: 'ghost', size: 'icon' })}
											onclick={() => toggleExpand(b.id)}
											aria-expanded={expandedId === b.id}
										>
											<svg
												xmlns="http://www.w3.org/2000/svg" width="16" height="16"
												viewBox="0 0 24 24" fill="none" stroke="currentColor"
												stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
												style="transition:transform .15s;transform:rotate({expandedId === b.id ? 180 : 0}deg)"
											><polyline points="6 9 12 15 18 9"/></svg>
										</Tooltip.Trigger>
										<Tooltip.Content>{expandedId === b.id ? 'Ocultar respuestas' : 'Ver respuestas'}</Tooltip.Content>
									</Tooltip.Root>

									{#if b.status === 'confirmed'}
										{#if reschedulingId === b.id}
											<Button variant="outline" size="sm" onclick={cancelReschedule}>
												Cancelar reprogramación
											</Button>
										{:else}
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => startReschedule(b)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Reprogramar</Tooltip.Content>
											</Tooltip.Root>

											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => requestCancel(b.id)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Cancelar reserva</Tooltip.Content>
											</Tooltip.Root>
										{/if}
									{/if}
								</div>
							</Tooltip.Provider>
						</td>
					</tr>

					{#if expandedId === b.id}
						<tr>
							<td colspan={scope === 'all' ? 6 : 5} class="p-0">
								<div class="border-t bg-muted/20 px-4 py-3">
									<p class="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Detalles</p>
									<dl class="mb-3 space-y-1.5 text-sm">
										<div class="flex gap-4">
											<dt class="w-48 shrink-0 font-medium text-foreground">Reservado el</dt>
											<dd class="text-muted-foreground">{fmt(b.created_at)}</dd>
										</div>
										{#if b.payment_status}
											<div class="flex gap-4">
												<dt class="w-48 shrink-0 font-medium text-foreground">Pago</dt>
												<dd class="text-muted-foreground">
													{payLabel[b.payment_status] ?? b.payment_status}{#if b.amount_paid_cents} · {fmtMoney(b.amount_paid_cents, b.amount_paid_currency)}{/if}
												</dd>
											</div>
										{/if}
										{#if b.location_value}
											<div class="flex gap-4">
												<dt class="w-48 shrink-0 font-medium text-foreground">Ubicación</dt>
												<dd class="break-all text-muted-foreground">
													{#if /^https?:/.test(b.location_value)}
														<a href={b.location_value} target="_blank" rel="noopener noreferrer" class="text-primary underline">{b.location_value}</a>
													{:else}{b.location_value}{/if}
												</dd>
											</div>
										{/if}
									</dl>
									<p class="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Respuestas del formulario</p>
									{#if answersLoading[b.id]}
										<p class="text-sm text-muted-foreground">Cargando…</p>
									{:else if !answersCache[b.id] || answersCache[b.id].length === 0}
										<p class="text-sm text-muted-foreground">No hay respuestas de formulario para esta reserva.</p>
									{:else}
										<dl class="space-y-2">
											{#each answersCache[b.id] as a}
												<div class="flex gap-4 text-sm">
													<dt class="w-48 shrink-0 font-medium text-foreground">{a.label}</dt>
													<dd class="text-muted-foreground {a.type !== 'checkbox' ? 'whitespace-pre-wrap' : ''}">
														{#if a.type === 'checkbox'}
															<!-- Liberal comparison on purpose. Checkbox answers are canonicalised to
															     "yes"/"no" on the way in now, but rows created before that landed hold
															     whatever the surface sent - the embed widget sent "Yes". A strict
															     === 'yes' renders those as "No", i.e. the opposite of what the guest
															     ticked, which matters when the question is a consent checkbox. -->
															{['yes', 'true', '1', 'on', 'checked'].includes(String(a.value).trim().toLowerCase()) ? 'Sí' : 'No'}
														{:else}
															{a.value || '—'}
														{/if}
													</dd>
												</div>
											{/each}
										</dl>
									{/if}
								</div>
							</td>
						</tr>
					{/if}

					{#if reschedulingId === b.id}
						<tr>
							<td colspan={scope === 'all' ? 6 : 5} class="p-0">
								<div class="border-t bg-muted/30 px-4 py-4">
									<p class="mb-3 text-sm font-medium">Reprogramar — {b.attendees?.[0]?.name ?? 'asistente'}</p>

									<div class="flex flex-wrap items-end gap-3">
										<div class="space-y-1.5">
											<p class="text-sm font-medium">Nueva fecha</p>
											<DatePicker
												bind:value={rescheduleDate}
												placeholder="Elige una fecha"
												minToday
												class="w-[180px]"
											/>
										</div>

										{#if slotsLoading}
											<p class="pb-1 text-sm text-muted-foreground">Cargando horarios…</p>
										{:else if slotsError}
											<p class="rounded-md bg-destructive/10 px-3 py-1.5 text-sm text-destructive">{slotsError}</p>
										{:else if rescheduleDate && slots.length === 0}
											<p class="pb-1 text-sm text-muted-foreground">No hay horarios disponibles en esta fecha.</p>
										{/if}
									</div>

									{#if slots.length > 0}
										<div class="mt-3 flex flex-wrap gap-2">
											{#each slots as slot}
												<button
													onclick={() => (selectedSlot = slot.start)}
													class="inline-flex items-center justify-center rounded-md px-3 py-1.5 text-xs font-medium transition-colors {selectedSlot === slot.start ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'border bg-background hover:bg-accent hover:text-accent-foreground'}"
												>
													{fmtSlotTime(slot.start)}
												</button>
											{/each}
										</div>
									{/if}

									{#if rescheduleError}
										<p class="mt-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{rescheduleError}</p>
									{/if}

									{#if selectedSlot}
										<div class="mt-4 flex gap-2">
											<Button onclick={confirmReschedule} disabled={rescheduling}>
												{rescheduling ? 'Reprogramando…' : `Confirmar — ${fmtSlotTime(selectedSlot)}`}
											</Button>
											<Button variant="outline" onclick={cancelReschedule}>
												Cancelar
											</Button>
										</div>
									{/if}
								</div>
							</td>
						</tr>
					{/if}
				{/each}
			</tbody>
		</table>
	</div>
	{#if total > PAGE_SIZE}
		<div class="mt-4 flex items-center justify-between gap-4">
			<p class="text-sm text-muted-foreground">Mostrando {pageStart}–{pageEnd} de {total}</p>
			<div class="flex items-center gap-2">
				<Button variant="outline" size="sm" disabled={offset === 0} onclick={() => goTo(offset - PAGE_SIZE)}>
					Anterior
				</Button>
				<Button variant="outline" size="sm" disabled={pageEnd >= total} onclick={() => goTo(offset + PAGE_SIZE)}>
					Siguiente
				</Button>
			</div>
		</div>
	{/if}
	{/if}
{/if}

<ConfirmDialog
	bind:open={confirmOpen}
	title="¿Cancelar esta reserva?"
	description="Se notificará al asistente y el horario quedará libre. Esta acción no se puede deshacer."
	confirmText="Cancelar reserva"
	cancelText="Mantener reserva"
	destructive
	onConfirm={cancel}
/>
