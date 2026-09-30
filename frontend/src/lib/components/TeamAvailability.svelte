<script lang="ts">
	// Fork (Agenda Maestros 4x4): the Panel's "Disponibilidad del equipo" - who of Mentoría
	// and Soporte is free when, side by side, so the owner (admins, the support people) can
	// answer a request and hand over the right personal link. Times come from
	// GET /v1/team/availability, which computes each person's link exactly like its public
	// page: a time shown here is a time the client can book there.
	import { onMount } from 'svelte';
	import { teamApi, copyText, AREA_LABELS, type TeamAvailability, type TeamAvailabilityPerson } from '$lib/api';
	import { displayZone, timezoneItems, timezoneLabel } from '$lib/prefs';
	import {
		PRIORITY_ZONES,
		MAX_DAYS_AHEAD,
		ExpiringCache,
		addDays,
		countFreePeople,
		daysBetween,
		dayNumber,
		dowShort,
		firstName,
		fmtSlotTime,
		hourLabel,
		hourParts,
		initials,
		longDay,
		mergedRanges,
		slotDay,
		slotHour,
		startsOn,
		todayIn
	} from '$lib/team-availability';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Avatar from '$lib/components/ui/avatar';
	import { Combobox } from '$lib/components/ui/combobox';
	import { toast } from 'svelte-sonner';
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';

	type AreaFilter = 'all' | 'mentoria' | 'soporte';

	let area = $state<AreaFilter>('all');
	let tz = $state(displayZone());
	let weekOffset = $state(0);
	let selectedDay = $state('');
	let data = $state<TeamAvailability | null>(null);
	let loading = $state(true);
	let loadError = $state('');
	let dialogOpen = $state(false);
	let dialogPerson = $state<TeamAvailabilityPerson | null>(null);
	// Answers already fetched this visit, per zone + week, reused for as long as the server
	// caches them (60 s): older ones are fetched again, never shown stale.
	const cache = new ExpiringCache<TeamAvailability>();
	let requestSeq = 0;

	const today = $derived(todayIn(tz));
	const weekStart = $derived(addDays(today, weekOffset * 7));
	const days = $derived(Array.from({ length: 7 }, (_, i) => addDays(weekStart, i)));
	const maxOffset = Math.floor(MAX_DAYS_AHEAD / 7);

	const zoneItems = $derived.by(() => {
		const first = PRIORITY_ZONES.map((z) => ({ value: z.zone, label: `${z.country} · ${timezoneLabel(z.zone)}` }));
		const taken = new Set(first.map((z) => z.value));
		return [...first, ...timezoneItems(tz).filter((z) => !taken.has(z.value))];
	});

	const people = $derived((data?.people ?? []).filter((p) => area === 'all' || p.area === area));

	function freeOn(p: TeamAvailabilityPerson, day: string) {
		return !p.error && p.slots.some((s) => slotDay(s.start) === day);
	}

	// People (not entries: the owner is in both áreas) free each day; null while unknown.
	const countByDay = $derived(
		Object.fromEntries(days.map((d) => [d, data && !loading && !loadError ? countFreePeople(people, d) : null])) as Record<
			string,
			number | null
		>
	);

	// Hour rows of the chosen day: only hours where someone can start.
	const hourRows = $derived.by(() => {
		const rows = new Map<number, TeamAvailabilityPerson[]>();
		for (const p of people) {
			if (p.error) continue;
			const hours = new Set(startsOn(p, selectedDay).map(slotHour));
			for (const h of hours) {
				if (!rows.has(h)) rows.set(h, []);
				rows.get(h)!.push(p);
			}
		}
		return [...rows.entries()].sort((a, b) => a[0] - b[0]).map(([hour, list]) => ({ hour, people: list }));
	});

	const freeToday = $derived(people.filter((p) => freeOn(p, selectedDay)));
	const failed = $derived(people.filter((p) => p.error));

	function personKey(p: TeamAvailabilityPerson) {
		return `${p.area}:${p.user_id}`;
	}

	function dayDisabled(d: string) {
		return daysBetween(today, d) > MAX_DAYS_AHEAD;
	}

	async function load(fresh = false) {
		const from = weekStart;
		const to = addDays(weekStart, 6);
		const key = `${tz}|${from}`;
		const seq = ++requestSeq;
		loadError = '';
		if (fresh) cache.clear(); // "Actualizar": every week is re-fetched from now on
		const hit = fresh ? undefined : cache.get(key);
		if (hit) {
			data = hit;
			loading = false;
			return;
		}
		loading = true;
		try {
			const res = await teamApi.availability({ from, to, tz, area: 'all', fresh });
			if (seq !== requestSeq) return; // a newer week or zone was asked meanwhile
			cache.set(key, res);
			data = res;
		} catch (e) {
			if (seq !== requestSeq) return;
			data = null;
			loadError = e instanceof Error && e.message ? e.message : 'No se pudo cargar la disponibilidad.';
		} finally {
			if (seq === requestSeq) loading = false;
		}
	}

	function pickDefaultDay() {
		if (!days.includes(selectedDay)) {
			selectedDay = days.find((d) => !dayDisabled(d)) ?? days[0];
		}
	}

	function moveWeek(delta: number) {
		const next = Math.min(maxOffset, Math.max(0, weekOffset + delta));
		if (next === weekOffset) return;
		weekOffset = next;
		selectedDay = '';
		pickDefaultDay();
		load();
	}

	function changeZone(v: string) {
		if (!v || v === tz) return;
		tz = v;
		weekOffset = 0;
		selectedDay = '';
		pickDefaultDay();
		load();
	}

	function openPerson(p: TeamAvailabilityPerson) {
		dialogPerson = p;
		dialogOpen = true;
	}

	async function copyLink(p: TeamAvailabilityPerson) {
		if (await copyText(p.link.url)) {
			toast.success(`Enlace de ${firstName(p.name)} copiado`);
		} else {
			toast.error('No se pudo copiar el enlace. Ábrelo y cópialo desde la barra de direcciones.');
		}
	}

	function errorNote(p: TeamAvailabilityPerson) {
		switch (p.error_kind) {
			case 'calendar':
				return 'No se pudo revisar su calendario ahora; su horario no se muestra.';
			case 'timeout':
				return 'No hubo tiempo de revisar su horario; pulsa Actualizar.';
			default:
				return 'No se pudo calcular su horario ahora.';
		}
	}

	onMount(() => {
		pickDefaultDay();
		load();
	});
</script>

{#snippet personAvatar(p: TeamAvailabilityPerson, size: 'sm' | 'default' | 'lg')}
	<Avatar.Root {size} class="text-[0.65rem] font-semibold">
		{#if p.avatar_url}
			<Avatar.Image src={p.avatar_url} alt={p.name} class="object-cover" />
		{/if}
		<Avatar.Fallback>{initials(p.name)}</Avatar.Fallback>
	</Avatar.Root>
{/snippet}

{#snippet copyButton(p: TeamAvailabilityPerson)}
	<Button variant="outline" size="xs" onclick={() => copyLink(p)} aria-label="Copiar el enlace de {p.name}">
		<CopyIcon />Copiar
	</Button>
{/snippet}

<section class="mt-8" aria-labelledby="team-availability-title">
	<div class="mb-3 flex items-start justify-between gap-2">
		<div class="min-w-0">
			<h2 id="team-availability-title" class="text-lg font-semibold tracking-tight">Disponibilidad del equipo</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Horarios libres de Mentoría y Soporte, tal como los ve el cliente en cada enlace.
			</p>
		</div>
		<Button variant="ghost" size="icon-sm" onclick={() => load(true)} disabled={loading} aria-label="Actualizar" title="Actualizar">
			<RefreshCwIcon class={loading ? 'animate-spin' : ''} />
		</Button>
	</div>

	<div class="rounded-lg border bg-card p-3 sm:p-4">
		<!-- Filters -->
		<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
			<div class="flex gap-1.5" role="group" aria-label="Filtrar por área">
				{#each [['all', 'Todos'], ['mentoria', 'Mentoría'], ['soporte', 'Soporte']] as [value, label] (value)}
					<Button
						size="sm"
						variant={area === value ? 'default' : 'outline'}
						class="rounded-full"
						aria-pressed={area === value}
						onclick={() => (area = value as AreaFilter)}
					>
						{label}
					</Button>
				{/each}
			</div>
			<div class="flex min-w-0 flex-col gap-1 sm:w-72">
				<span class="text-xs text-muted-foreground">Ver en hora de</span>
				<Combobox
					items={zoneItems}
					bind:value={() => tz, (v) => changeZone(v)}
					placeholder="Elige una zona"
					searchPlaceholder="Buscar país o ciudad…"
				/>
			</div>
		</div>

		<!-- Day strip -->
		<div class="mt-4 flex items-center gap-1">
			<Button variant="ghost" size="icon-sm" onclick={() => moveWeek(-1)} disabled={weekOffset === 0} aria-label="Semana anterior">
				<ChevronLeftIcon />
			</Button>
			<div class="grid flex-1 grid-cols-7 gap-1">
				{#each days as d (d)}
					{@const disabled = dayDisabled(d)}
					{@const count = disabled ? null : (countByDay[d] ?? null)}
					<button
						type="button"
						class="flex min-w-0 flex-col items-center rounded-lg border px-0.5 py-1.5 text-center transition-colors disabled:opacity-40
							{d === selectedDay ? 'border-primary bg-primary text-primary-foreground' : 'bg-background hover:bg-muted'}"
						{disabled}
						aria-pressed={d === selectedDay}
						aria-label="{longDay(d)}{count === null ? '' : `, ${count} ${count === 1 ? 'persona libre' : 'personas libres'}`}"
						onclick={() => (selectedDay = d)}
					>
						<span class="text-[0.65rem] uppercase leading-none">{d === today ? 'hoy' : dowShort(d)}</span>
						<span class="mt-1 text-sm font-semibold leading-none">{dayNumber(d)}</span>
						<span
							class="mt-1 min-w-4 rounded-full px-1 text-[0.6rem] leading-4
								{d === selectedDay ? 'bg-primary-foreground/20' : count ? 'bg-muted text-foreground' : 'text-muted-foreground'}"
						>
							{count === null ? '·' : count}
						</span>
					</button>
				{/each}
			</div>
			<Button variant="ghost" size="icon-sm" onclick={() => moveWeek(1)} disabled={weekOffset >= maxOffset} aria-label="Semana siguiente">
				<ChevronRightIcon />
			</Button>
		</div>

		<!-- The chosen day -->
		<div class="mt-4">
			<p class="mb-2 text-sm font-medium first-letter:uppercase">{selectedDay ? longDay(selectedDay) : ''}</p>

			{#if loading}
				<div class="space-y-2" aria-busy="true" aria-label="Cargando disponibilidad">
					{#each [0, 1, 2] as i (i)}
						<div class="flex items-center gap-3">
							<div class="h-4 w-16 animate-pulse rounded bg-muted"></div>
							<div class="h-8 flex-1 animate-pulse rounded-full bg-muted"></div>
						</div>
					{/each}
				</div>
			{:else if loadError}
				<div class="rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-3 text-sm">
					<p class="text-destructive">No se pudo cargar la disponibilidad del equipo.</p>
					<p class="mt-0.5 text-xs text-muted-foreground">{loadError}</p>
					<Button class="mt-2" variant="outline" size="sm" onclick={() => load(true)}>Reintentar</Button>
				</div>
			{:else}
				{#if hourRows.length === 0}
					<p class="rounded-lg border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">
						Nadie tiene horarios libres este día.
					</p>
				{:else}
					{#if area === 'all'}
						<p class="mb-1.5 flex items-center gap-3 text-[0.7rem] text-muted-foreground" aria-hidden="true">
							<span class="inline-flex items-center gap-1"
								><span class="grid size-3.5 place-items-center rounded-full bg-primary text-[0.5rem] font-bold text-primary-foreground">M</span
								>Mentoría</span
							>
							<span class="inline-flex items-center gap-1"
								><span class="grid size-3.5 place-items-center rounded-full bg-amber-500 text-[0.5rem] font-bold text-white">S</span>Soporte</span
							>
						</p>
					{/if}
					<ol class="divide-y rounded-lg border">
						{#each hourRows as row (row.hour)}
							{@const hp = hourParts(row.hour)}
							<li class="flex gap-2 px-2 py-2 sm:gap-3 sm:px-3">
								<span class="sr-only">{hourLabel(row.hour)}</span>
								<!-- Phones: "9:00" over "a. m." in a narrow column, so two or three people fit side by side. -->
								<span class="w-10 shrink-0 pt-1 text-xs leading-tight tabular-nums text-muted-foreground sm:w-[4.5rem] sm:pt-1.5" aria-hidden="true">
									<span class="block sm:inline">{hp.time}</span>
									<span class="block whitespace-nowrap text-[0.6rem] sm:inline sm:text-xs">{hp.period}</span>
								</span>
								<div class="flex min-w-0 flex-1 flex-wrap gap-1">
									{#each row.people as p (personKey(p))}
										<button
											type="button"
											data-person-chip
											class="inline-flex max-w-full items-center gap-1 rounded-full border-2 bg-background py-0.5 pl-0.5 pr-2 text-xs font-medium transition-colors hover:bg-muted"
											style={p.color ? `border-color: ${p.color}` : undefined}
											onclick={() => openPerson(p)}
											aria-label="{p.name}{p.is_you ? ' (tú)' : ''}, {AREA_LABELS[p.area]}: ver horarios y enlace"
										>
											<span class="relative shrink-0">
												{@render personAvatar(p, 'sm')}
												{#if area === 'all'}
													<!-- The área as one letter on the avatar: it takes no width in the row. -->
													<span
														class="absolute -bottom-1 -right-1 grid size-3.5 place-items-center rounded-full border border-background text-[0.5rem] font-bold leading-none
															{p.area === 'soporte' ? 'bg-amber-500 text-white' : 'bg-primary text-primary-foreground'}"
														aria-hidden="true">{p.area === 'soporte' ? 'S' : 'M'}</span
													>
												{/if}
											</span>
											<span class="max-w-[6.5rem] truncate">{firstName(p.name)}</span>
										</button>
									{/each}
								</div>
							</li>
						{/each}
					</ol>
				{/if}

				<!-- Per-person summary: usable without tapping the chips -->
				{#if freeToday.length > 0 || failed.length > 0}
					<h3 class="mb-2 mt-5 text-sm font-medium">Resumen del día</h3>
					<ul class="space-y-2">
						{#each freeToday as p (personKey(p))}
							<li class="flex items-center gap-2 rounded-lg border px-2.5 py-2">
								<button type="button" class="flex min-w-0 flex-1 items-center gap-2 text-left" onclick={() => openPerson(p)}>
									{@render personAvatar(p, 'default')}
									<span class="min-w-0">
										<span class="block truncate text-sm font-medium">
											{p.name}{p.is_you ? ' (tú)' : ''}
											<span class="text-xs font-normal text-muted-foreground">· {AREA_LABELS[p.area]}</span>
										</span>
										<span class="block text-xs text-muted-foreground">{mergedRanges(p, selectedDay).join(' · ')}</span>
									</span>
								</button>
								{@render copyButton(p)}
							</li>
						{/each}
						{#each failed as p (personKey(p))}
							<li class="flex items-center gap-2 rounded-lg border border-dashed px-2.5 py-2">
								{@render personAvatar(p, 'default')}
								<span class="min-w-0 flex-1">
									<span class="block truncate text-sm font-medium">
										{p.name}
										<span class="text-xs font-normal text-muted-foreground">· {AREA_LABELS[p.area]}</span>
									</span>
									<span class="block text-xs text-amber-700 dark:text-amber-400">{errorNote(p)}</span>
								</span>
								{@render copyButton(p)}
							</li>
						{/each}
					</ul>
				{/if}
				{#if data && data.people.length === 0}
					<p class="mt-3 text-xs text-muted-foreground">
						Aún no hay tipos predefinidos de Mentoría o Soporte, o nadie del equipo tiene su enlace activo.
					</p>
				{/if}
			{/if}
		</div>
	</div>
</section>

<Dialog.Root bind:open={dialogOpen}>
	<Dialog.Content class="max-w-[calc(100%-2rem)] rounded-lg sm:max-w-md">
		{#if dialogPerson}
			{@const p = dialogPerson}
			{@const starts = startsOn(p, selectedDay)}
			<Dialog.Header>
				<div class="flex items-center gap-3">
					<Avatar.Root size="lg" class="size-14 text-base font-semibold" style={p.color ? `box-shadow: 0 0 0 2px ${p.color}` : undefined}>
						{#if p.avatar_url}
							<Avatar.Image src={p.avatar_url} alt={p.name} class="object-cover" />
						{/if}
						<Avatar.Fallback>{initials(p.name)}</Avatar.Fallback>
					</Avatar.Root>
					<div class="min-w-0 text-left">
						<Dialog.Title class="truncate">{p.name}{p.is_you ? ' (tú)' : ''}</Dialog.Title>
						<Dialog.Description>{AREA_LABELS[p.area]}</Dialog.Description>
					</div>
				</div>
			</Dialog.Header>
			<div>
				<p class="mb-2 text-xs text-muted-foreground first-letter:uppercase">
					{longDay(selectedDay)} · hora de {tz.replaceAll('_', ' ')}
				</p>
				{#if p.error}
					<p class="text-sm text-amber-700 dark:text-amber-400">{errorNote(p)}</p>
				{:else if starts.length === 0}
					<p class="text-sm text-muted-foreground">No tiene horarios libres este día.</p>
				{:else}
					<div class="flex max-h-48 flex-wrap gap-1.5 overflow-y-auto">
						{#each starts as s (s)}
							<span class="rounded-full border px-2 py-0.5 text-xs tabular-nums">{fmtSlotTime(s)}</span>
						{/each}
					</div>
				{/if}
				<p class="mt-3 truncate font-mono text-xs text-muted-foreground">{p.link.url}</p>
			</div>
			<Dialog.Footer class="gap-2 sm:justify-end">
				<Button variant="outline" href={p.link.url} target="_blank" rel="noopener noreferrer">
					<ExternalLinkIcon />Abrir página
				</Button>
				<Button onclick={() => copyLink(p)}>
					<CopyIcon />Copiar enlace
				</Button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>
