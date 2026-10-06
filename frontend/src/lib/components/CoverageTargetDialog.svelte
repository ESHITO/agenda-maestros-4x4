<script lang="ts">
	// Fork (Agenda Maestros 4x4): "Horario a cubrir" - the hours the owner wants someone of
	// each área to work. The team calendar marks every hour of it that nobody works as
	// "Sin cubrir". Owner only (PUT /v1/team/coverage-target answers 403 to anyone else).
	// One span per weekday, half-hour steps, the end may be midnight; no day on = no target.
	import { teamApi, type TeamCoverageTarget } from '$lib/api';
	import { timezoneItems, timezoneLabel } from '$lib/prefs';
	import { PRIORITY_ZONES } from '$lib/team-availability';
	import {
		TARGET_ENDS,
		TARGET_STARTS,
		WEEK_HEADERS,
		draftToDays,
		hhmmToMin,
		minToHhmm,
		targetDraft,
		targetTimeLabel,
		type TargetDraftDay
	} from '$lib/team-calendar';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Select from '$lib/components/ui/select';
	import { Switch } from '$lib/components/ui/switch';
	import { Combobox } from '$lib/components/ui/combobox';
	import { toast } from 'svelte-sonner';

	let {
		open = $bindable(false),
		target,
		onSaved
	}: { open?: boolean; target: TeamCoverageTarget; onSaved?: (t: TeamCoverageTarget) => void } = $props();

	let rows = $state<TargetDraftDay[]>([]);
	let tz = $state('America/Lima');
	let saving = $state(false);
	let error = $state('');

	// Every time it opens it starts from what is saved, never from an abandoned edit.
	let wasOpen = false;
	$effect(() => {
		if (open && !wasOpen) {
			rows = targetDraft(target);
			tz = target.tz;
			error = '';
		}
		wasOpen = open;
	});

	const zoneItems = $derived.by(() => {
		const first = PRIORITY_ZONES.map((z) => ({ value: z.zone, label: `${z.country} · ${timezoneLabel(z.zone)}` }));
		const taken = new Set(first.map((z) => z.value));
		return [...first, ...timezoneItems(tz).filter((z) => !taken.has(z.value))];
	});

	const startItems = TARGET_STARTS.map((t) => ({ value: t, label: targetTimeLabel(t) }));
	const endItems = TARGET_ENDS.map((t) => ({ value: t, label: targetTimeLabel(t) }));

	// Like the availability editor: an end not after the start moves the other end instead
	// of refusing (start + 1 h, at most midnight; end - 1 h, at least 00:00).
	function setTime(i: number, which: 'start' | 'end', v: string) {
		if (!v) return;
		const r = { ...rows[i], [which]: v };
		const s = hhmmToMin(r.start);
		const e = hhmmToMin(r.end);
		if (s >= e) {
			if (which === 'start') r.end = minToHhmm(Math.min(1440, s + 60));
			else r.start = minToHhmm(Math.max(0, e - 60));
		}
		rows[i] = r;
		error = '';
	}

	function copyMondayToWeekdays() {
		const mon = rows[0];
		rows = rows.map((r, i) => (i > 0 && i < 5 ? { ...r, on: mon.on, start: mon.start, end: mon.end } : r));
	}

	async function save() {
		const { days, error: problem } = draftToDays(rows);
		if (problem) {
			error = problem;
			return;
		}
		saving = true;
		error = '';
		try {
			const saved = await teamApi.putCoverageTarget({ tz, days });
			toast.success('Horario a cubrir guardado');
			open = false;
			onSaved?.(saved);
		} catch (e) {
			error = e instanceof Error && e.message ? e.message : 'No se pudo guardar el horario a cubrir.';
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-h-[calc(100dvh-2rem)] max-w-[calc(100%-2rem)] overflow-y-auto rounded-lg p-4 sm:max-w-lg sm:p-6">
		<Dialog.Header>
			<Dialog.Title>Horario a cubrir</Dialog.Title>
			<Dialog.Description>
				Las horas en que quieres que alguien de cada área esté trabajando. Las que nadie trabaja se marcan como «Sin cubrir».
			</Dialog.Description>
		</Dialog.Header>

		<div class="space-y-3">
			<div class="flex flex-col gap-1">
				<span class="text-xs text-muted-foreground">Horas en hora de</span>
				<Combobox items={zoneItems} bind:value={tz} placeholder="Elige una zona" searchPlaceholder="Buscar país o ciudad…" />
			</div>

			<ul class="divide-y rounded-lg border">
				{#each rows as r, i (r.dow)}
					{@const head = WEEK_HEADERS[(r.dow + 6) % 7]}
					<li class="flex flex-col gap-2 px-3 py-2.5 sm:flex-row sm:items-center sm:gap-3" data-target-row={r.dow}>
						<label class="flex items-center gap-2 text-sm font-medium first-letter:uppercase sm:w-32 sm:shrink-0">
							<Switch
								checked={r.on}
								onCheckedChange={(v) => {
									rows[i] = { ...rows[i], on: v };
									error = '';
								}}
								aria-label="Cubrir el {head.long}"
							/>
							<span class="first-letter:uppercase">{head.long}</span>
						</label>
						{#if r.on}
							<div class="flex flex-wrap items-center gap-1.5">
								<Select.Root type="single" value={r.start} onValueChange={(v) => setTime(i, 'start', v)}>
									<Select.Trigger class="w-fit" aria-label="Desde, {head.long}">{targetTimeLabel(r.start)}</Select.Trigger>
									<Select.Content>
										{#each startItems as t (t.value)}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
									</Select.Content>
								</Select.Root>
								<span class="text-muted-foreground">–</span>
								<Select.Root type="single" value={r.end} onValueChange={(v) => setTime(i, 'end', v)}>
									<Select.Trigger class="w-fit" aria-label="Hasta, {head.long}">{targetTimeLabel(r.end)}</Select.Trigger>
									<Select.Content>
										{#each endItems as t (t.value)}<Select.Item value={t.value} label={t.label}>{t.label}</Select.Item>{/each}
									</Select.Content>
								</Select.Root>
							</div>
						{:else}
							<span class="text-sm text-muted-foreground">No se mide este día</span>
						{/if}
					</li>
				{/each}
			</ul>
			<Button variant="link" size="xs" class="h-auto px-0" onclick={copyMondayToWeekdays}>Copiar el lunes de martes a viernes</Button>
			{#if error}
				<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>
			{/if}
		</div>

		<Dialog.Footer class="gap-2 sm:justify-end">
			<Button variant="outline" onclick={() => (open = false)} disabled={saving}>Cancelar</Button>
			<Button onclick={save} disabled={saving}>{saving ? 'Guardando…' : 'Guardar'}</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
