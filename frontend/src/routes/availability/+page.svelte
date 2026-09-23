<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type AvailabilityRule, type AvailabilityOverride } from '$lib/api';
	import { prefs, fmtDate } from '$lib/prefs';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import * as Select from '$lib/components/ui/select';
	import { DatePicker } from '$lib/components/ui/date-picker';

	const DAY_NAMES = ['Domingo', 'Lunes', 'Martes', 'Miércoles', 'Jueves', 'Viernes', 'Sábado'];

	const TIME_SLOTS: string[] = [];
	for (let h = 0; h < 24; h++) {
		for (const m of [0, 30]) {
			TIME_SLOTS.push(`${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`);
		}
	}

	function fmtTime(t: string) {
		const [hh, mm] = t.split(':').map(Number);
		if ($prefs.time_format === '24h') {
			return mm === 0 ? `${String(hh).padStart(2,'0')}:00` : `${String(hh).padStart(2,'0')}:${String(mm).padStart(2,'0')}`;
		}
		const ampm = hh < 12 ? 'am' : 'pm';
		const h12 = hh % 12 || 12;
		return mm === 0 ? `${h12}${ampm}` : `${h12}:${String(mm).padStart(2,'0')}${ampm}`;
	}

	// ── Weekly rules — 7-day model ────────────────────────────────────────────────
	type DayBlock = { id: string; start_time: string; end_time: string; saving: boolean; error: string }
	type DayState = { day_of_week: number; blocks: DayBlock[]; error: string }

	let days: DayState[] = $state(
		Array.from({ length: 7 }, (_, i) => ({ day_of_week: i, blocks: [], error: '' }))
	);

	let rulesLoading = $state(true);
	let rulesError = $state('');

	let orderedDays = $derived([...days].sort((a, b) => {
		const ws = $prefs.week_start ?? 1;
		return ((a.day_of_week - ws + 7) % 7) - ((b.day_of_week - ws + 7) % 7);
	}));

	async function loadRules() {
		rulesError = '';
		rulesLoading = true;
		try {
			const res = await api.get<{ items: AvailabilityRule[] }>('/v1/availability-rules');
			for (const day of days) { day.blocks = []; day.error = ''; }
			for (const r of (res.items ?? [])) {
				const day = days[r.day_of_week];
				if (day) day.blocks.push({ id: r.id, start_time: r.start_time, end_time: r.end_time, saving: false, error: '' });
			}
			for (const day of days) {
				day.blocks.sort((a, b) => a.start_time.localeCompare(b.start_time));
			}
		} catch (e: any) {
			rulesError = e.message;
		} finally {
			rulesLoading = false;
		}
	}

	async function addBlock(day: DayState) {
		day.blocks.push({ id: '', start_time: '09:00', end_time: '17:00', saving: true, error: '' });
		// Operate on the element AS STORED in the reactive array (a proxy), not the literal we
		// pushed — Svelte 5 proxies pushed objects, so mutating the local literal wouldn't update
		// the UI (the "saving…" stuck-until-refresh bug).
		const block = day.blocks[day.blocks.length - 1];
		try {
			const r = await api.post<AvailabilityRule>('/v1/availability-rules', {
				day_of_week: day.day_of_week,
				start_time: '09:00',
				end_time: '17:00'
			});
			block.id = r.id;
		} catch (e: any) {
			const idx = day.blocks.indexOf(block);
			if (idx !== -1) day.blocks.splice(idx, 1);
			day.error = e.message;
		} finally {
			block.saving = false;
		}
	}

	async function updateBlock(block: DayBlock) {
		if (!block.id) return;
		if (block.start_time >= block.end_time) {
			block.error = 'El fin debe ser posterior al inicio';
			return;
		}
		block.error = '';
		block.saving = true;
		try {
			await api.patch(`/v1/availability-rules/${block.id}`, {
				start_time: block.start_time,
				end_time: block.end_time
			});
		} catch (e: any) {
			block.error = e.message;
		} finally {
			block.saving = false;
		}
	}

	async function removeBlock(day: DayState, block: DayBlock) {
		if (!block.id) return;
		block.saving = true;
		try {
			await api.del(`/v1/availability-rules/${block.id}`);
			const idx = day.blocks.indexOf(block);
			if (idx !== -1) day.blocks.splice(idx, 1);
		} catch (e: any) {
			block.error = e.message;
			block.saving = false;
		}
	}

	// ── Date overrides ────────────────────────────────────────────────────────────
	let overrides: AvailabilityOverride[] = $state([]);
	let overridesLoading = $state(true);
	let overridesError = $state('');

	type OverrideReason = 'day_off' | 'out_of_office' | 'custom_hours';
	const REASON_LABELS: Record<OverrideReason, string> = {
		day_off: 'Día libre',
		out_of_office: 'Fuera de oficina',
		custom_hours: 'Horario personalizado'
	};

	let ovForm = $state({ date: '', end_date: '', reason: 'day_off' as OverrideReason, start_time: '09:00', end_time: '17:00' });
	let addingOv = $state(false);
	let ovAddError = $state('');

	let deleteOvOpen = $state(false);
	let deleteOvId = $state('');

	let deleteGroupId = $state('');
	let deleteGroupOpen = $state(false);

	// Collapse per-date rows that share a group_id (a multi-day span) into one entry.
	type OvEntry =
		| { kind: 'single'; ov: AvailabilityOverride }
		| { kind: 'span'; group_id: string; reason: OverrideReason; start: string; end: string; days: number };
	const overrideEntries = $derived.by((): OvEntry[] => {
		const groups = new Map<string, AvailabilityOverride[]>();
		const entries: OvEntry[] = [];
		for (const ov of overrides) {
			if (ov.group_id) {
				const arr = groups.get(ov.group_id) ?? [];
				arr.push(ov);
				groups.set(ov.group_id, arr);
			} else {
				entries.push({ kind: 'single', ov });
			}
		}
		for (const [gid, arr] of groups) {
			const dates = arr.map((o) => o.date).sort();
			entries.push({
				kind: 'span',
				group_id: gid,
				reason: (arr[0].reason ?? 'out_of_office') as OverrideReason,
				start: dates[0],
				end: dates[dates.length - 1],
				days: dates.length
			});
		}
		return entries.sort((a, b) =>
			(a.kind === 'single' ? a.ov.date : a.start).localeCompare(b.kind === 'single' ? b.ov.date : b.start)
		);
	});

	async function loadOverrides() {
		overridesError = '';
		try {
			const res = await api.get<{ items: AvailabilityOverride[] }>('/v1/availability-overrides');
			overrides = (res.items ?? []).sort((a, b) => a.date.localeCompare(b.date));
		} catch (e: any) {
			overridesError = e.message;
		} finally {
			overridesLoading = false;
		}
	}




	async function addOverride() {
		ovAddError = '';
		if (!ovForm.date) { ovAddError = 'La fecha es obligatoria.'; return; }
		if (ovForm.reason === 'custom_hours' && (!ovForm.start_time || !ovForm.end_time)) {
			ovAddError = 'La hora de inicio y fin son obligatorias para el horario personalizado.'; return;
		}
		if (ovForm.reason === 'custom_hours' && ovForm.start_time >= ovForm.end_time) {
			ovAddError = 'La hora de fin debe ser posterior a la hora de inicio.'; return;
		}
		if (ovForm.end_date && ovForm.end_date < ovForm.date) {
			ovAddError = 'La fecha de fin debe ser igual o posterior a la fecha de inicio.'; return;
		}
		addingOv = true;
		try {
			const isRange = ovForm.reason !== 'custom_hours' && !!ovForm.end_date && ovForm.end_date > ovForm.date;
			await api.post('/v1/availability-overrides', {
				date: ovForm.date,
				reason: ovForm.reason,
				...(ovForm.reason === 'custom_hours'
					? { start_time: ovForm.start_time, end_time: ovForm.end_time }
					: {}),
				...(isRange ? { end_date: ovForm.end_date } : {})
			});
			ovForm = { date: '', end_date: '', reason: 'day_off', start_time: '09:00', end_time: '17:00' };
			await loadOverrides();
		} catch (e: any) {
			ovAddError = e.message;
		} finally {
			addingOv = false;
		}
	}

	function deleteOverride(id: string) {
		deleteOvId = id;
		deleteOvOpen = true;
	}

	async function doDeleteOverride() {
		try {
			await api.del(`/v1/availability-overrides/${deleteOvId}`);
			await loadOverrides();
		} catch (e: any) {
			overridesError = e.message;
		}
	}

	function deleteGroup(groupId: string) {
		deleteGroupId = groupId;
		deleteGroupOpen = true;
	}

	async function doDeleteGroup() {
		try {
			await api.del(`/v1/availability-overrides/group/${deleteGroupId}`);
			await loadOverrides();
		} catch (e: any) {
			overridesError = e.message;
		}
	}

	onMount(() => {
		loadRules();
		loadOverrides();
	});

	const REASON_OPTIONS: { value: OverrideReason; label: string }[] = [
		{ value: 'day_off', label: 'Día libre' },
		{ value: 'out_of_office', label: 'Fuera de oficina' },
		{ value: 'custom_hours', label: 'Horario personalizado' },
	];

	// Time-of-day options for the start/end selects, formatted per the user's prefs.
	let timeOptions = $derived(TIME_SLOTS.map((t) => ({ value: t, label: fmtTime(t) })));
	function timeLabel(t: string) { return fmtTime(t); }
</script>

<ConfirmDialog
	bind:open={deleteOvOpen}
	title="¿Eliminar esta excepción de fecha?"
	description="Tu horario semanal por defecto volverá a aplicarse en esta fecha."
	confirmText="Eliminar"
	destructive
	onConfirm={doDeleteOverride}
/>

<ConfirmDialog
	bind:open={deleteGroupOpen}
	title="¿Eliminar este rango de fuera de oficina?"
	description="Todos los días de este rango volverán a estar disponibles para reservar."
	confirmText="Eliminar"
	destructive
	onConfirm={doDeleteGroup}
/>

<svelte:head><title>Disponibilidad — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Disponibilidad</h1>
	<p class="mt-1 text-sm text-muted-foreground">Configura tu horario semanal y bloquea fechas específicas.</p>
</div>

<!-- Weekly Hours -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Horario semanal</h2>

	{#if rulesError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{rulesError}</p>{/if}

	{#if rulesLoading}
		<p class="py-4 text-sm text-muted-foreground">Cargando…</p>
	{:else}
		<div class="rounded-lg border bg-card">
			<Tooltip.Provider>
				{#each orderedDays as day, i}
					<div class="flex gap-4 px-4 py-3 {i > 0 ? 'border-t' : ''}">
						<!-- Day name -->
						<div class="w-24 shrink-0 pt-1.5 text-sm {day.blocks.length === 0 ? 'font-normal text-muted-foreground' : 'font-medium'}">
							{DAY_NAMES[day.day_of_week]}
						</div>

						<!-- Blocks -->
						<div class="flex-1 space-y-2">
							{#if day.error}
								<p class="text-xs text-destructive">{day.error}</p>
							{/if}

							{#if day.blocks.length === 0}
								<div class="flex items-center gap-3 py-0.5">
									<span class="text-sm text-muted-foreground/50">Sin horario configurado</span>
									<button
										onclick={() => addBlock(day)}
										class="text-sm text-primary hover:underline"
									>
										+ Agregar horario
									</button>
								</div>
							{:else}
								{#each day.blocks as block}
									<div class="flex items-center gap-2">
										<Select.Root
											type="single"
											bind:value={block.start_time}
											onValueChange={() => updateBlock(block)}
											disabled={block.saving || !block.id}
										>
											<Select.Trigger class="w-fit">{timeLabel(block.start_time)}</Select.Trigger>
											<Select.Content>
												{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
											</Select.Content>
										</Select.Root>
										<span class="text-muted-foreground">–</span>
										<Select.Root
											type="single"
											bind:value={block.end_time}
											onValueChange={() => updateBlock(block)}
											disabled={block.saving || !block.id}
										>
											<Select.Trigger class="w-fit">{timeLabel(block.end_time)}</Select.Trigger>
											<Select.Content>
												{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
											</Select.Content>
										</Select.Root>
										{#if block.saving}
											<span class="text-xs text-muted-foreground">guardando…</span>
										{:else if block.error}
											<span class="text-xs text-destructive">{block.error}</span>
										{/if}
										<Tooltip.Root>
											<Tooltip.Trigger
												class={buttonVariants({ variant: 'ghost', size: 'icon' })}
												onclick={() => removeBlock(day, block)}
												disabled={block.saving}
											>
												<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
											</Tooltip.Trigger>
											<Tooltip.Content>Eliminar</Tooltip.Content>
										</Tooltip.Root>
									</div>
								{/each}
								<button
									onclick={() => addBlock(day)}
									class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
								>
									<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
									Agregar bloque
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</Tooltip.Provider>
		</div>
	{/if}
</div>

<!-- Date Overrides -->
<div>
	<h2 class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Excepciones de fecha</h2>
	<p class="mb-3 text-sm text-muted-foreground">Bloquea una fecha específica o configura un horario personalizado para ella.</p>

	<div class="rounded-lg border bg-card">
		{#if overridesError}<p class="px-4 pt-4 text-sm text-destructive">{overridesError}</p>{/if}

		{#if overridesLoading}
			<p class="px-4 py-4 text-sm text-muted-foreground">Cargando…</p>
		{:else if overrides.length > 0}
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b">
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Fecha</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Tipo</th>
						<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Duración</th>
						<th class="px-4 pb-3 pt-3"></th>
					</tr>
				</thead>
				<tbody class="divide-y">
					{#each overrideEntries as entry (entry.kind === 'span' ? entry.group_id : entry.ov.id)}
						{#if entry.kind === 'span'}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3 font-medium">{fmtDate(entry.start, $prefs)} – {fmtDate(entry.end, $prefs)}</td>
								<td class="px-4 py-3">
									{#if entry.reason === 'out_of_office'}
										<Badge class="bg-amber-50 text-amber-700 border-amber-200">Fuera de oficina</Badge>
									{:else}
										<Badge variant="secondary">Día libre</Badge>
									{/if}
								</td>
								<td class="px-4 py-3 text-muted-foreground">{entry.days} días</td>
								<td class="px-4 py-3">
									<Tooltip.Provider>
										<div class="flex items-center justify-end gap-1">
											<Tooltip.Root>
												<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => deleteGroup(entry.group_id)}>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Eliminar rango</Tooltip.Content>
											</Tooltip.Root>
										</div>
									</Tooltip.Provider>
								</td>
							</tr>
						{:else}
							{@const ov = entry.ov}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3 font-medium">{fmtDate(ov.date, $prefs)}</td>
								<td class="px-4 py-3">
									{#if ov.reason === 'custom_hours'}
										<Badge class="bg-blue-50 text-blue-700 border-blue-200">Horario personalizado</Badge>
									{:else if ov.reason === 'out_of_office'}
										<Badge class="bg-amber-50 text-amber-700 border-amber-200">Fuera de oficina</Badge>
									{:else}
										<Badge variant="secondary">Día libre</Badge>
									{/if}
								</td>
								<td class="px-4 py-3 text-muted-foreground">
									{ov.is_available && ov.start_time && ov.end_time
										? `${fmtTime(ov.start_time)} – ${fmtTime(ov.end_time)}`
										: '1 día'}
								</td>
								<td class="px-4 py-3">
									<Tooltip.Provider>
										<div class="flex items-center justify-end gap-1">
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => deleteOverride(ov.id)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Eliminar</Tooltip.Content>
											</Tooltip.Root>
										</div>
									</Tooltip.Provider>
								</td>
							</tr>
						{/if}
					{/each}
				</tbody>
			</table>
		{:else}
			<p class="px-4 py-4 text-sm text-muted-foreground">Aún no hay excepciones.</p>
		{/if}

		<!-- Add override form -->
		<div class="border-t px-4 py-4">
			{#if ovAddError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{ovAddError}</p>{/if}
			<div class="flex flex-wrap items-end gap-3">
				<div class="space-y-1.5">
					<label for="ov-date" class="text-sm font-medium">Fecha</label>
					<DatePicker bind:value={ovForm.date} placeholder="Elige una fecha" />
				</div>
				{#if ovForm.reason !== 'custom_hours'}
					<div class="space-y-1.5">
						<label for="ov-end-date" class="text-sm font-medium">Hasta <span class="font-normal text-muted-foreground">(opcional)</span></label>
						<DatePicker bind:value={ovForm.end_date} placeholder="Mismo día" />
					</div>
				{/if}
				<div class="space-y-1.5">
					<label for="ov-type" class="text-sm font-medium">Tipo</label>
					<Select.Root type="single" value={ovForm.reason} onValueChange={(v) => { if (v) ovForm.reason = v as OverrideReason; }}>
						<Select.Trigger id="ov-type" class="w-fit">{REASON_LABELS[ovForm.reason]}</Select.Trigger>
						<Select.Content>
							{#each REASON_OPTIONS as r}
								<Select.Item value={r.value} label={r.label}>{r.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
				{#if ovForm.reason === 'custom_hours'}
					<div class="space-y-1.5">
						<label for="ov-start" class="text-sm font-medium">Desde</label>
						<Select.Root type="single" bind:value={ovForm.start_time}>
							<Select.Trigger id="ov-start" class="w-fit">{timeLabel(ovForm.start_time)}</Select.Trigger>
							<Select.Content>
								{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
							</Select.Content>
						</Select.Root>
					</div>
					<div class="space-y-1.5">
						<label for="ov-end" class="text-sm font-medium">Hasta</label>
						<Select.Root type="single" bind:value={ovForm.end_time}>
							<Select.Trigger id="ov-end" class="w-fit">{timeLabel(ovForm.end_time)}</Select.Trigger>
							<Select.Content>
								{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
							</Select.Content>
						</Select.Root>
					</div>
				{/if}
				<Button onclick={addOverride} disabled={addingOv}>
					{addingOv ? 'Agregando…' : 'Agregar excepción'}
				</Button>
			</div>
		</div>
	</div>
</div>
