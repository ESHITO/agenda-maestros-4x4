<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type AvailabilityRule, type AvailabilityOverride } from '$lib/api';
	import { prefs, fmtDate } from '$lib/prefs';
	import { onResume } from '$lib/refresh';
	import { SaveQueue, errorMessage, type SaveStatus } from '$lib/save-queue';
	import { fixRange, type TimeRange } from '$lib/time-range';
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
	// Owner report (30 Sep 2026): while a mentor set his hours, each change needed a reload
	// to show. The selects used to lock while their PATCH was in flight, so a quick second
	// change was silently dropped, and a start after the end was refused without saving.
	// Now every edit shows at once and is saved through one SaveQueue per block
	// ($lib/save-queue: serialized, coalesced, last write wins), with its own status.
	//
	// `key` is local and stable (the {#each} key and the queue's key); `id` arrives when the
	// POST returns. Code always finds a block again by key through findBlock(), so it
	// mutates the element as stored in the reactive array (a Svelte 5 proxy), never a stale
	// literal (the old "saving… stuck until refresh" bug).
	type DayBlock = { key: string; id: string; start_time: string; end_time: string; status: SaveStatus; error: string };
	type DayState = { day_of_week: number; blocks: DayBlock[]; error: string };

	let days: DayState[] = $state(
		Array.from({ length: 7 }, (_, i) => ({ day_of_week: i, blocks: [], error: '' }))
	);

	let rulesLoading = $state(true);
	let rulesError = $state('');

	let orderedDays = $derived([...days].sort((a, b) => {
		const ws = $prefs.week_start ?? 1;
		return ((a.day_of_week - ws + 7) % 7) - ((b.day_of_week - ws + 7) % 7);
	}));

	// Not reactive: bookkeeping of the saves in flight.
	const queues = new Map<string, SaveQueue<TimeRange>>();
	const creating = new Set<string>(); // keys whose POST has not returned
	const deleteOnCreate = new Set<string>(); // removed before their POST returned
	const deletingIds = new Set<string>(); // DELETE in flight: a re-sync must not bring them back
	let keySeq = 0;
	// Only the newest GET may apply.
	let rulesSeq = 0;
	// A clock ticked by every successful write. A GET remembers the tick it started at, and
	// a block written after that tick (or a rule deleted after it) keeps what the page
	// knows: the answer may predate the write, and must not paint the old value back.
	let writeClock = 0;
	const writtenAt = new Map<string, number>(); // block key → tick of its last write
	const deletedAt = new Map<string, number>(); // rule id → tick of its DELETE
	const wrote = (key: string) => writtenAt.set(key, ++writeClock);

	const newKey = () => `b${++keySeq}`;
	const sameRange = (a: TimeRange, b: TimeRange) => a.start_time === b.start_time && a.end_time === b.end_time;

	function findBlock(key: string): { day: DayState; block: DayBlock } | null {
		for (const day of days) {
			for (const block of day.blocks) if (block.key === key) return { day, block };
		}
		return null;
	}

	function makeQueue(key: string, ready: boolean, confirmed?: TimeRange) {
		const q = new SaveQueue<TimeRange>({
			ready,
			confirmed,
			equals: sameRange,
			send: async (v) => {
				const id = findBlock(key)?.block.id;
				if (!id) throw new Error('Este horario ya no existe.');
				await api.patch(`/v1/availability-rules/${id}`, v);
				wrote(key);
			},
			onStatus: (status, error) => {
				const loc = findBlock(key);
				if (!loc) return;
				loc.block.status = status;
				loc.block.error = error;
			},
			// Show the server's truth for this block (the error and "Reintentar" stay);
			// blocks with edits still on their way keep them.
			onError: () => { void loadRules(true); }
		});
		queues.set(key, q);
		return q;
	}

	// True while any weekly edit has not reached the server yet.
	function rulesBusy() {
		if (creating.size > 0 || deletingIds.size > 0) return true;
		for (const q of queues.values()) if (q.busy) return true;
		return false;
	}

	// Merges the server's rules into what is shown. A block with an edit or a POST still in
	// flight keeps its local values; every other block takes the server's, and blocks the
	// server no longer has disappear. Rules the page does not show yet are added.
	function applyServerRules(items: AvailabilityRule[], startedAt: number) {
		const byId = new Map(items.map((r) => [r.id, r]));
		for (const day of days) {
			const kept: DayBlock[] = [];
			for (const b of day.blocks) {
				const q = queues.get(b.key);
				if (!b.id || q?.busy || (writtenAt.get(b.key) ?? 0) > startedAt) {
					kept.push(b);
					if (b.id) byId.delete(b.id);
					continue;
				}
				const srv = byId.get(b.id);
				if (!srv || srv.day_of_week !== day.day_of_week) {
					q?.dispose();
					queues.delete(b.key);
					continue;
				}
				byId.delete(b.id);
				b.start_time = srv.start_time;
				b.end_time = srv.end_time;
				q?.setConfirmed({ start_time: srv.start_time, end_time: srv.end_time });
				kept.push(b);
			}
			day.blocks = kept;
		}
		// While a POST is in flight its rule may already be in this answer, but the placeholder
		// has no id to match it yet: adding unknown rules now would show that block twice (two
		// queues editing one rule). Rules created elsewhere appear on the next re-sync.
		const mayHoldPendingCreate = creating.size > 0;
		for (const r of byId.values()) {
			if (mayHoldPendingCreate) break;
			if (deletingIds.has(r.id) || (deletedAt.get(r.id) ?? 0) > startedAt) continue;
			const day = days[r.day_of_week];
			if (!day) continue;
			const key = newKey();
			day.blocks.push({ key, id: r.id, start_time: r.start_time, end_time: r.end_time, status: 'idle', error: '' });
			makeQueue(key, true, { start_time: r.start_time, end_time: r.end_time });
		}
		for (const day of days) {
			day.blocks.sort((a, b) => a.start_time.localeCompare(b.start_time));
		}
	}

	// quiet = a re-sync in the background: no "Cargando…", and a failure keeps what is shown.
	async function loadRules(quiet = false) {
		const seq = ++rulesSeq;
		const startedAt = writeClock;
		if (!quiet) {
			rulesError = '';
			rulesLoading = true;
		}
		try {
			const res = await api.get<{ items: AvailabilityRule[] }>('/v1/availability-rules');
			if (seq !== rulesSeq) return;
			if (!quiet) for (const day of days) day.error = '';
			applyServerRules(res.items ?? [], startedAt);
			rulesError = '';
		} catch (e: unknown) {
			if (seq !== rulesSeq || quiet) return;
			rulesError = errorMessage(e, 'No se pudo cargar tu horario.');
		} finally {
			if (!quiet) rulesLoading = false;
		}
	}

	async function addBlock(dayOfWeek: number) {
		const day = days[dayOfWeek];
		if (!day) return;
		const initial: TimeRange = { start_time: '09:00', end_time: '17:00' };
		const key = newKey();
		day.error = '';
		// Shown and editable at once; edits made before the POST returns wait in the queue
		// and are sent as soon as the id arrives.
		day.blocks.push({ key, id: '', ...initial, status: 'saving', error: '' });
		const q = makeQueue(key, false);
		creating.add(key);
		try {
			const r = await api.post<AvailabilityRule>('/v1/availability-rules', { day_of_week: dayOfWeek, ...initial });
			wrote(key);
			creating.delete(key);
			if (deleteOnCreate.delete(key)) {
				// Removed while it was being created: remove it on the server too.
				q.dispose();
				queues.delete(key);
				await deleteRule(r.id, dayOfWeek);
				return;
			}
			const loc = findBlock(key);
			if (!loc) return;
			loc.block.id = r.id;
			q.start({ start_time: r.start_time || initial.start_time, end_time: r.end_time || initial.end_time });
		} catch (e: unknown) {
			creating.delete(key);
			q.dispose();
			queues.delete(key);
			const loc = findBlock(key);
			if (loc) loc.day.blocks.splice(loc.day.blocks.indexOf(loc.block), 1);
			if (!deleteOnCreate.delete(key)) day.error = errorMessage(e, 'No se pudo agregar el horario.');
		}
	}

	function changeTime(key: string, which: 'start' | 'end', value: string | undefined) {
		if (!value) return;
		const loc = findBlock(key);
		if (!loc) return;
		const b = loc.block;
		if ((which === 'start' ? b.start_time : b.end_time) === value) return;
		// A start not before the end moves the end (and vice versa) instead of refusing.
		const next = fixRange(
			{ start_time: which === 'start' ? value : b.start_time, end_time: which === 'end' ? value : b.end_time },
			which
		);
		b.start_time = next.start_time;
		b.end_time = next.end_time;
		loc.day.error = '';
		const q = queues.get(key);
		if (!q) return;
		if (!q.ready) {
			b.status = 'saving';
			b.error = '';
		}
		q.push(next);
	}

	function retryBlock(key: string) {
		const loc = findBlock(key);
		const v = queues.get(key)?.retry();
		if (loc && v) {
			loc.block.start_time = v.start_time;
			loc.block.end_time = v.end_time;
		}
	}

	async function removeBlock(key: string) {
		const loc = findBlock(key);
		if (!loc) return;
		const { day, block } = loc;
		const id = block.id;
		const dayOfWeek = day.day_of_week;
		day.error = '';
		// Gone from the screen at once.
		day.blocks.splice(day.blocks.indexOf(block), 1);
		const q = queues.get(key);
		queues.delete(key);
		q?.dispose();
		if (!id) {
			deleteOnCreate.add(key); // addBlock deletes it once the POST returns
			return;
		}
		deletingIds.add(id);
		await q?.idle(); // an edit already on its way settles first
		await deleteRule(id, dayOfWeek);
	}

	async function deleteRule(id: string, dayOfWeek: number) {
		deletingIds.add(id);
		try {
			await api.del(`/v1/availability-rules/${id}`);
			deletedAt.set(id, ++writeClock);
			deletingIds.delete(id);
		} catch (e: unknown) {
			deletingIds.delete(id);
			await loadRules(true); // it comes back as the server still has it
			const day = days[dayOfWeek];
			if (day) day.error = `No se pudo quitar el horario: ${errorMessage(e)}`;
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
	let ovAddError = $state('');

	let deleteOvOpen = $state(false);
	let deleteOvId = $state('');

	let deleteGroupId = $state('');
	let deleteGroupOpen = $state(false);

	// Adds and deletes show at once; the list is then re-synced from the server.
	let ovSeq = 0;
	let ovOps = 0; // adds/deletes in flight
	let ovFailure = ''; // a failed delete, shown once the list is re-synced
	// An add or delete starts: a GET already on its way predates it and must not apply
	// (it would wipe the placeholder rows or bring the deleted ones back).
	function startOvOp() {
		ovOps++;
		ovSeq++;
	}
	// An add or delete settled: re-sync only when the last one did, so an answer never lands
	// over another operation still in flight (no row vanishing and coming back).
	async function afterOvOp(failure = '') {
		if (failure) ovFailure = failure;
		if (ovOps > 0) return;
		await loadOverrides(true);
		if (ovFailure && ovOps === 0) {
			overridesError = ovFailure;
			ovFailure = '';
		}
	}
	let tmpSeq = 0;
	const TMP = 'tmp-';
	const isTmp = (id: string | undefined) => !!id && id.startsWith(TMP);

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

	async function loadOverrides(quiet = false) {
		const seq = ++ovSeq;
		try {
			const res = await api.get<{ items: AvailabilityOverride[] }>('/v1/availability-overrides');
			if (seq !== ovSeq || ovOps > 0) return;
			overrides = (res.items ?? []).sort((a, b) => a.date.localeCompare(b.date));
			overridesError = '';
		} catch (e: unknown) {
			if (seq !== ovSeq || quiet) return;
			overridesError = errorMessage(e, 'No se pudieron cargar las excepciones.');
		} finally {
			overridesLoading = false;
		}
	}

	// Every date of [from … to], "YYYY-MM-DD" (UTC arithmetic: these are calendar dates).
	function datesBetween(from: string, to: string): string[] {
		const out: string[] = [];
		let t = Date.parse(`${from}T00:00:00Z`);
		const end = Date.parse(`${to}T00:00:00Z`);
		while (t <= end && out.length < 367) {
			out.push(new Date(t).toISOString().slice(0, 10));
			t += 86_400_000;
		}
		return out;
	}

	// The rows the server will create, shown until the re-sync replaces them.
	function placeholderRows(f: typeof ovForm, isRange: boolean): AvailabilityOverride[] {
		const n = ++tmpSeq;
		if (f.reason === 'custom_hours') {
			return [{ id: `${TMP}${n}`, date: f.date, is_available: true, reason: f.reason, start_time: f.start_time, end_time: f.end_time }];
		}
		if (!isRange) {
			return [{ id: `${TMP}${n}`, date: f.date, is_available: false, reason: f.reason, start_time: null, end_time: null }];
		}
		return datesBetween(f.date, f.end_date).map((d, i) => ({
			id: `${TMP}${n}-${i}`,
			date: d,
			is_available: false,
			reason: f.reason,
			start_time: null,
			end_time: null,
			group_id: `${TMP}g${n}`
		}));
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
		const sent = { ...ovForm };
		const EMPTY_FORM = { date: '', end_date: '', reason: 'day_off' as OverrideReason, start_time: '09:00', end_time: '17:00' };
		const isRange = sent.reason !== 'custom_hours' && !!sent.end_date && sent.end_date > sent.date;
		const temp = placeholderRows(sent, isRange);
		const tempIds = new Set(temp.map((o) => o.id));
		overrides = [...overrides, ...temp].sort((a, b) => a.date.localeCompare(b.date));
		ovForm = { ...EMPTY_FORM };
		startOvOp();
		try {
			await api.post('/v1/availability-overrides', {
				date: sent.date,
				reason: sent.reason,
				...(sent.reason === 'custom_hours'
					? { start_time: sent.start_time, end_time: sent.end_time }
					: {}),
				...(isRange ? { end_date: sent.end_date } : {})
			});
		} catch (e: unknown) {
			overrides = overrides.filter((o) => !tempIds.has(o.id));
			const msg = errorMessage(e, 'No se pudo agregar la excepción.');
			// Give the form back as it was, so nothing has to be typed again - unless the
			// person already started the next exception: never overwrite what they typed.
			const untouched = (Object.keys(EMPTY_FORM) as (keyof typeof EMPTY_FORM)[]).every((k) => ovForm[k] === EMPTY_FORM[k]);
			if (untouched) {
				ovForm = sent;
				ovAddError = msg;
			} else {
				ovAddError = `No se pudo agregar la excepción del ${fmtDate(sent.date)}: ${msg}`;
			}
		} finally {
			ovOps--;
		}
		await afterOvOp();
	}

	function deleteOverride(id: string) {
		if (isTmp(id)) return;
		deleteOvId = id;
		deleteOvOpen = true;
	}

	function deleteGroup(groupId: string) {
		if (isTmp(groupId)) return;
		deleteGroupId = groupId;
		deleteGroupOpen = true;
	}

	async function removeOverrides(match: (o: AvailabilityOverride) => boolean, path: string) {
		overrides = overrides.filter((o) => !match(o));
		startOvOp();
		let failure = '';
		try {
			await api.del(path);
		} catch (e: unknown) {
			failure = errorMessage(e, 'No se pudo eliminar la excepción.');
		} finally {
			ovOps--;
		}
		await afterOvOp(failure);
	}

	function doDeleteOverride() {
		const id = deleteOvId;
		void removeOverrides((o) => o.id === id, `/v1/availability-overrides/${id}`);
	}

	function doDeleteGroup() {
		const gid = deleteGroupId;
		void removeOverrides((o) => o.group_id === gid, `/v1/availability-overrides/group/${gid}`);
	}

	// The custom-hours pair: a start not before the end moves the end, like the weekly blocks.
	function changeOvTime(which: 'start' | 'end', value: string | undefined) {
		if (!value) return;
		const next = fixRange(
			{ start_time: which === 'start' ? value : ovForm.start_time, end_time: which === 'end' ? value : ovForm.end_time },
			which
		);
		ovForm.start_time = next.start_time;
		ovForm.end_time = next.end_time;
	}

	onMount(() => {
		// Both lists at once.
		void Promise.all([loadRules(), loadOverrides()]);
		// Coming back to the tab (after looking at the booking page, another device…) shows
		// what the server has now - but never over an edit still on its way.
		return onResume(
			async () => {
				await Promise.all([
					rulesBusy() || rulesLoading ? null : loadRules(true),
					ovOps > 0 || overridesLoading ? null : loadOverrides(true)
				]);
			},
			{ minIntervalMs: 5_000 }
		);
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
					<!-- The day name sits above its blocks on a phone: beside them, two time selects
					     and the delete button did not fit in 375 px. -->
					<div class="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:gap-4 {i > 0 ? 'border-t' : ''}">
						<!-- Day name -->
						<div class="shrink-0 text-sm sm:w-24 sm:pt-1.5 {day.blocks.length === 0 ? 'font-normal text-muted-foreground' : 'font-medium'}">
							{DAY_NAMES[day.day_of_week]}
						</div>

						<!-- Blocks -->
						<div class="min-w-0 flex-1 space-y-2">
							{#if day.error}
								<p class="text-xs text-destructive">{day.error}</p>
							{/if}

							{#if day.blocks.length === 0}
								<div class="flex items-center gap-3 py-0.5">
									<span class="text-sm text-muted-foreground/50">Sin horario configurado</span>
									<button
										onclick={() => addBlock(day.day_of_week)}
										class="text-sm text-primary hover:underline"
									>
										+ Agregar horario
									</button>
								</div>
							{:else}
								{#each day.blocks as block (block.key)}
									<!-- Never locked while saving: every change shows at once and the
									     block's save queue sends the latest one (see the script). -->
									<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
										<Select.Root
											type="single"
											value={block.start_time}
											onValueChange={(v) => changeTime(block.key, 'start', v)}
										>
											<Select.Trigger class="w-fit">{timeLabel(block.start_time)}</Select.Trigger>
											<Select.Content>
												{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
											</Select.Content>
										</Select.Root>
										<span class="text-muted-foreground">–</span>
										<Select.Root
											type="single"
											value={block.end_time}
											onValueChange={(v) => changeTime(block.key, 'end', v)}
										>
											<Select.Trigger class="w-fit">{timeLabel(block.end_time)}</Select.Trigger>
											<Select.Content>
												{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
											</Select.Content>
										</Select.Root>
										<!-- The status never reflows the row: on a phone it has its own line below
										     the selects, always reserved (min-h), so "Quitar" never jumps while
										     "Guardando…" comes and goes (375 px: selects + Quitar fill the row);
										     from sm it sits inline with a fixed minimum width. -->
										<span class="order-last inline-flex min-h-4 basis-full flex-wrap items-center gap-1 text-xs sm:order-none sm:min-h-0 sm:min-w-[5.5rem] sm:basis-auto" aria-live="polite">
											{#if block.status === 'saving'}
												<span class="text-muted-foreground">Guardando…</span>
											{:else if block.status === 'saved'}
												<span class="text-emerald-700 dark:text-emerald-400">Guardado ✓</span>
											{:else if block.status === 'error'}
												<span class="text-destructive">{block.error || 'No se pudo guardar.'}</span>
												<Button variant="link" size="xs" class="h-10 px-2 md:h-auto md:px-1" onclick={() => retryBlock(block.key)}>Reintentar</Button>
											{/if}
										</span>
										<!-- On a phone the delete is a 40 px text button (a Tooltip never opens
										     on touch, and a 32 px icon is a poor target); the icon + Tooltip from md. -->
										<Button
											variant="ghost"
											class="h-10 text-destructive hover:text-destructive md:hidden"
											onclick={() => removeBlock(block.key)}
										>
											Quitar
										</Button>
										<div class="hidden md:block">
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													aria-label="Eliminar horario"
													onclick={() => removeBlock(block.key)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Eliminar</Tooltip.Content>
											</Tooltip.Root>
										</div>
									</div>
								{/each}
								<button
									onclick={() => addBlock(day.day_of_week)}
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
			<!-- Below md, one card per exception (the 4-column table clipped at 375 px). The
			     delete action carries text there: a Tooltip does not open on touch. -->
			<ul class="divide-y md:hidden">
				{#each overrideEntries as entry (entry.kind === 'span' ? entry.group_id : entry.ov.id)}
					<li class="flex items-center justify-between gap-3 px-4 py-3">
						<div class="min-w-0 space-y-1">
							{#if entry.kind === 'span'}
								<p class="font-medium">{fmtDate(entry.start, $prefs)} – {fmtDate(entry.end, $prefs)}</p>
								<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
									{#if entry.reason === 'out_of_office'}
										<Badge class="bg-amber-50 text-amber-700 border-amber-200">Fuera de oficina</Badge>
									{:else}
										<Badge variant="secondary">Día libre</Badge>
									{/if}
									<span>{entry.days} días</span>
								</div>
							{:else}
								{@const ov = entry.ov}
								<p class="font-medium">{fmtDate(ov.date, $prefs)}</p>
								<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
									{#if ov.reason === 'custom_hours'}
										<Badge class="bg-blue-50 text-blue-700 border-blue-200">Horario personalizado</Badge>
									{:else if ov.reason === 'out_of_office'}
										<Badge class="bg-amber-50 text-amber-700 border-amber-200">Fuera de oficina</Badge>
									{:else}
										<Badge variant="secondary">Día libre</Badge>
									{/if}
									<span>
										{ov.is_available && ov.start_time && ov.end_time
											? `${fmtTime(ov.start_time)} – ${fmtTime(ov.end_time)}`
											: '1 día'}
									</span>
								</div>
							{/if}
						</div>
						<!-- Phone-only: 40 px tall (a finger's target), not the table's 28 px. -->
						{#if entry.kind === 'span'}
							<Button variant="ghost" class="h-10 shrink-0 text-destructive hover:text-destructive" onclick={() => deleteGroup(entry.group_id)} disabled={isTmp(entry.group_id)}>Eliminar</Button>
						{:else}
							<Button variant="ghost" class="h-10 shrink-0 text-destructive hover:text-destructive" onclick={() => deleteOverride(entry.ov.id)} disabled={isTmp(entry.ov.id)}>Eliminar</Button>
						{/if}
					</li>
				{/each}
			</ul>
			<table class="hidden w-full text-sm md:table">
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
												<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} aria-label="Eliminar rango" onclick={() => deleteGroup(entry.group_id)} disabled={isTmp(entry.group_id)}>
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
													aria-label="Eliminar excepción"
													onclick={() => deleteOverride(ov.id)}
													disabled={isTmp(ov.id)}
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
						<Select.Root type="single" value={ovForm.start_time} onValueChange={(v) => changeOvTime('start', v)}>
							<Select.Trigger id="ov-start" class="w-fit">{timeLabel(ovForm.start_time)}</Select.Trigger>
							<Select.Content>
								{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
							</Select.Content>
						</Select.Root>
					</div>
					<div class="space-y-1.5">
						<label for="ov-end" class="text-sm font-medium">Hasta</label>
						<Select.Root type="single" value={ovForm.end_time} onValueChange={(v) => changeOvTime('end', v)}>
							<Select.Trigger id="ov-end" class="w-fit">{timeLabel(ovForm.end_time)}</Select.Trigger>
							<Select.Content>
								{#each timeOptions as t}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
							</Select.Content>
						</Select.Root>
					</div>
				{/if}
				<Button class="w-full sm:w-auto" onclick={addOverride}>
					Agregar excepción
				</Button>
			</div>
		</div>
	</div>
</div>
