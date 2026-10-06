<script lang="ts">
	// Fork (Agenda Maestros 4x4): the Panel's "Calendario del equipo" (owner, 6 Oct 2026).
	// Mes / 15 días / Semana - Mes by default, every time (never remembered) - so the owner
	// and the support people see the whole team at once: who works which days, who is free
	// (solid, in each person's colour), who is booked (stripes), and the hours of the
	// "horario a cubrir" that NOBODY works yet (red hatch, "Sin cubrir") - the hours to fill
	// with new people. Data: GET /v1/team/calendar, whose free starts are the public pages'
	// own (computeSlots), first asked without them (free=0, the database alone, at once) and
	// then complete. Tapping a day opens its detail below: coverage, lanes per person, who
	// can start at each hour, and each person's link to hand over.
	import { onMount, tick } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { onResume } from '$lib/refresh';
	import { teamApi, copyText, AREA_LABELS, type TeamCalendar, type TeamCalPerson, type TeamCoverageTarget } from '$lib/api';
	import { displayZone, timezoneItems, timezoneLabel } from '$lib/prefs';
	import {
		PRIORITY_ZONES,
		ExpiringCache,
		addDays,
		dayNumber,
		firstName,
		hourLabel,
		hourParts,
		initials,
		longDay,
		todayIn
	} from '$lib/team-availability';
	import {
		DEFAULT_VIEW,
		MAX_DAYS_AHEAD,
		OWNER_ONLY_STYLE,
		UNCOVERED_STYLE,
		VIEWS,
		VIEW_LABELS,
		WEEK_HEADERS,
		busyOn,
		countFreePeople,
		countSessions,
		countWorkingPeople,
		dayCountsText,
		dayCoverage,
		daysOf,
		distinctPeople,
		durationText,
		freeHourRows,
		freeSpans,
		freeStartLabels,
		gridDays,
		mergeQuickAnswer,
		ownerOnlyText,
		periodLabel,
		periodOf,
		personDayState,
		personDot,
		personDotStyle,
		rowsFor,
		shiftPeriod,
		shortDay,
		spanLabel,
		stripes,
		pct,
		tickLabel,
		timelineTicks,
		timelineWindow,
		targetSummary,
		tint,
		uncoveredText,
		type AreaFilter,
		type CalView,
		type DayCoverage,
		type PersonDot
	} from '$lib/team-calendar';
	import TeamDayTimeline from './TeamDayTimeline.svelte';
	import CoverageTargetDialog from './CoverageTargetDialog.svelte';
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
	import PencilIcon from '@lucide/svelte/icons/pencil';

	let view = $state<CalView>(DEFAULT_VIEW);
	let area = $state<AreaFilter>('all');
	let tz = $state(displayZone());
	let anchor = $state(todayIn(displayZone()));
	let selectedDay = $state('');
	/** People picked in the people row; empty = everybody. */
	const chosen = new SvelteSet<string>();
	let data = $state<TeamCalendar | null>(null);
	let loading = $state(true);
	let loadError = $state('');
	/** The complete answer (free starts) is on its way after the quick one. */
	let checkingFree = $state(false);
	let freeError = $state('');
	let dialogOpen = $state(false);
	let dialogKey = $state('');
	let targetOpen = $state(false);
	// Complete answers already fetched this visit, per zone + range, reused as long as the
	// server caches them (60 s), never longer.
	const cache = new ExpiringCache<TeamCalendar>();
	let requestSeq = 0;

	const today = $derived(todayIn(tz));
	const lastDay = $derived(addDays(today, MAX_DAYS_AHEAD));
	const period = $derived(periodOf(view, anchor));
	/** The range asked: the period, cut at the furthest day the server answers. */
	const reqTo = $derived(period.to > lastDay ? lastDay : period.to);
	// Days after reqTo cannot be asked: drawn like the days outside the period.
	const grid = $derived(gridDays(period.from, period.to, reqTo));
	const weekDays = $derived(daysOf(period.from, period.to));
	const canNext = $derived(shiftPeriod(view, period.from, 1) <= lastDay);
	/** The answer on screen is the one of the period shown (not the previous one while the
	 *  next loads, and nothing after a failed load). */
	const current = $derived(
		data && !loadError && data.from === period.from && data.to === reqTo && data.tz === tz ? data : null
	);

	const zoneItems = $derived.by(() => {
		const first = PRIORITY_ZONES.map((z) => ({ value: z.zone, label: `${z.country} · ${timezoneLabel(z.zone)}` }));
		const taken = new Set(first.map((z) => z.value));
		return [...first, ...timezoneItems(tz).filter((z) => !taken.has(z.value))];
	});

	/** Everybody of the área filter (the people row lists them). */
	const areaRows = $derived(rowsFor(current?.people ?? [], area));
	const teamPeople = $derived(distinctPeople(areaRows));
	/** The people picked who are in the área shown (a pick outside it is ignored, never
	 *  an empty calendar). Empty = everybody. */
	const picked = $derived(new Set([...chosen].filter((id) => teamPeople.some((p) => p.user_id === id))));
	/** The rows drawn: the área filter and the people picked. */
	const rows = $derived(rowsFor(current?.people ?? [], area, picked));
	const freeIncluded = $derived(!!current?.free_included);

	type CellInfo = {
		free: number;
		working: number;
		sessions: number;
		cov: DayCoverage;
		dots: (PersonDot & { user: string; name: string; color: string })[];
	};

	function cellInfo(day: string): CellInfo | null {
		if (!current || day < period.from || day > reqTo) return null;
		const dots: CellInfo['dots'] = [];
		for (const p of distinctPeople(rows)) {
			// Unknown free time (quick answer, calendar/timeout error) is its own mark, never
			// "works, nothing free"; fully booked is stripes; sessions add a ring.
			const d = personDot(
				rows.filter((r) => r.user_id === p.user_id),
				day,
				current.today,
				freeIncluded
			);
			if (d) dots.push({ ...d, user: p.user_id, name: p.name, color: p.color });
		}
		return {
			free: countFreePeople(rows, day),
			working: countWorkingPeople(rows, day),
			sessions: countSessions(rows, day),
			cov: dayCoverage(current, day, area),
			dots
		};
	}

	const cells = $derived(Object.fromEntries(daysOf(period.from, period.to).map((d) => [d, cellInfo(d)])) as Record<string, CellInfo | null>);

	const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;


	/** One window for the whole week, so every day's lanes line up on the same hours. */
	const weekRange = $derived.by(() => {
		let start = Infinity;
		let end = -Infinity;
		for (const d of weekDays) {
			const info = cells[d];
			if (!info) continue;
			const w = timelineWindow(rows, info.cov.areas, d);
			start = Math.min(start, w.start);
			end = Math.max(end, w.end);
		}
		return isFinite(start) ? { start, end } : { start: 480, end: 1200 };
	});

	function cellLabel(day: string, info: CellInfo | null): string {
		let s = longDay(day);
		if (day === today) s += ' (hoy)';
		if (!info) return s;
		if (day < (current?.today ?? today)) return `${s}: ${info.sessions} ${info.sessions === 1 ? 'sesión' : 'sesiones'}`;
		const parts: string[] = [];
		if (freeIncluded) parts.push(`${info.free} ${info.free === 1 ? 'persona libre' : 'personas libres'}`);
		parts.push(`${info.working} ${info.working === 1 ? 'trabaja' : 'trabajan'}`);
		if (info.sessions) parts.push(`${info.sessions} ${info.sessions === 1 ? 'sesión' : 'sesiones'}`);
		if (info.cov.state === 'gaps') parts.push(uncoveredText(info.cov, false));
		else if (info.cov.state === 'owner_only') parts.push(`${ownerOnlyText(info.cov, false)} del horario a cubrir`);
		else if (info.cov.state === 'covered') parts.push('horario cubierto');
		return `${s}: ${parts.join(', ')}`;
	}

	// ── The chosen day ──
	const dayInfo = $derived(selectedDay ? (cells[selectedDay] ?? null) : null);
	const dayRows = $derived(
		rows.filter((p) => {
			const d = p.days[selectedDay];
			return (d?.hours?.length ?? 0) > 0 || (d?.busy?.length ?? 0) > 0 || (p.error && selectedDay >= (current?.today ?? today));
		})
	);
	const offPeople = $derived(
		selectedDay >= (current?.today ?? today)
			? distinctPeople(rows.filter((p) => !dayRows.some((r) => r.user_id === p.user_id)))
			: []
	);
	const hourRows = $derived(freeIncluded ? freeHourRows(dayRows, selectedDay) : []);
	const dialogPerson = $derived(dialogKey ? ((current?.people ?? []).find((p) => p.key === dialogKey) ?? null) : null);

	let detailHeading = $state<HTMLHeadingElement | null>(null);

	/** The day's detail is drawn below the whole grid, the legend and the target (~900 px down
	 *  at 375 px, ~1000 px on a laptop month): after a tap, bring it into view and move focus to
	 *  its heading, or the tap seems to change nothing but a ring. Only after a tap (never on
	 *  load or when moving between periods), and only when the heading is not already fully on
	 *  screen - decided by position, not width. The heading's scroll margin clears the 48 px
	 *  sticky top bar. */
	async function showDetail() {
		if (typeof window === 'undefined') return;
		await tick();
		const h = detailHeading;
		if (!h) return;
		const r = h.getBoundingClientRect();
		if (r.top >= 56 && r.bottom <= window.innerHeight) return;
		const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
		h.focus({ preventScroll: true });
		h.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'start' });
	}

	function pickDay(d: string) {
		if (d < period.from || d > reqTo) return;
		selectedDay = d;
		void showDetail();
	}

	function defaultDay() {
		if (selectedDay >= period.from && selectedDay <= reqTo) return;
		selectedDay = today >= period.from && today <= reqTo ? today : period.from;
	}

	// silent = the background refresh: what is on screen stays, with no "Cargando…" and no
	// quick answer first, and a failure keeps it. coverageFirst = a new "horario a cubrir" was
	// saved: the quick answer (database only, a moment) brings the new coverage onto the
	// screen while the complete one (up to ~22 s) is computed; the free starts shown stay.
	async function load(opts: { fresh?: boolean; silent?: boolean; coverageFirst?: boolean } = {}) {
		const { fresh = false, silent = false, coverageFirst = false } = opts;
		const from = period.from;
		const to = reqTo;
		const zone = tz;
		const key = `${zone}|${from}|${to}`;
		const seq = ++requestSeq;
		if (fresh) cache.clear(); // "Actualizar": every range is asked again from now on
		// The background refresh never reads this cache: its TTL equals the timer's interval
		// and an entry is stamped when the answer ARRIVES, so it would still hit and the
		// calendar would refresh every ~2 minutes. It still sends no fresh=1: the server's own
		// cache drops on every write.
		const hit = fresh || silent ? undefined : cache.get(key);
		if (hit) {
			data = hit;
			loading = false;
			loadError = '';
			freeError = '';
			checkingFree = false;
			return;
		}
		// "Actualizar" or a saved target on the range already shown: it stays on screen (no
		// skeleton, no step back to the quick answer) while the complete one is computed.
		const shown = !!data && !loadError && data.from === from && data.to === to && data.tz === zone;
		if (!silent && !shown) {
			loading = true;
			loadError = '';
			freeError = '';
			// 1. What the database knows (hours, sessions, coverage): painted at once, the
			//    coverage - the point of the view - never waits for Google or Microsoft.
			try {
				const quick = await teamApi.calendar({ from, to, tz: zone, free: false, fresh });
				if (seq !== requestSeq) return;
				data = quick;
				loading = false;
			} catch (e) {
				if (seq !== requestSeq) return;
				data = null;
				loading = false;
				loadError = e instanceof Error && e.message ? e.message : 'No se pudo cargar el calendario.';
				return;
			}
		} else if (shown && coverageFirst) {
			checkingFree = true;
			try {
				const quick = await teamApi.calendar({ from, to, tz: zone, free: false, fresh });
				if (seq !== requestSeq) return;
				if (data) data = mergeQuickAnswer(data, quick);
			} catch {
				if (seq !== requestSeq) return;
				// Nothing to say: the complete answer below brings the new coverage too.
			}
		}
		// 2. The complete answer, with the free starts.
		checkingFree = true;
		try {
			const full = await teamApi.calendar({ from, to, tz: zone, fresh });
			if (seq !== requestSeq) return; // a newer range or zone was asked meanwhile
			cache.set(key, full);
			data = full;
			loadError = '';
			freeError = '';
			if (dialogKey && !full.people.some((p) => p.key === dialogKey)) dialogOpen = false;
		} catch {
			if (seq !== requestSeq || silent) return;
			if (data?.free_included) toast.error('No se pudo actualizar el calendario. Se muestra lo último que se cargó.');
			else freeError = 'No se pudieron revisar los horarios libres. Se muestran las horas de trabajo, las sesiones y lo que falta cubrir.';
		} finally {
			if (seq === requestSeq) {
				checkingFree = false;
				loading = false;
			}
		}
	}

	function setView(v: CalView) {
		if (v === view) return;
		// Keep the day being looked at inside the new period.
		anchor = selectedDay || anchor;
		view = v;
		defaultDay();
		void load();
	}

	function move(delta: number) {
		if (delta > 0 && !canNext) return;
		anchor = shiftPeriod(view, period.from, delta);
		selectedDay = '';
		defaultDay();
		void load();
	}

	/** "Hoy": back to today's period, or - when it is already shown - back to today's detail
	 *  after looking at another day. */
	function goToday() {
		const shown = today >= period.from && today <= period.to;
		anchor = today;
		selectedDay = today;
		if (shown) void showDetail();
		else void load();
	}

	function changeZone(v: string) {
		if (!v || v === tz) return;
		tz = v;
		defaultDay();
		void load();
	}

	function togglePerson(userId: string) {
		if (chosen.has(userId)) chosen.delete(userId);
		else chosen.add(userId);
	}

	function openPerson(p: TeamCalPerson) {
		dialogKey = p.key;
		dialogOpen = true;
	}

	async function copyLink(p: TeamCalPerson) {
		if (await copyText(p.link.url)) toast.success(`Enlace de ${firstName(p.name)} copiado`);
		else toast.error('No se pudo copiar el enlace. Ábrelo y cópialo desde la barra de direcciones.');
	}

	function targetSaved(t: TeamCoverageTarget) {
		if (data) data = { ...data, target: { ...t, can_edit: data.target.can_edit, is_default: false } };
		void load({ fresh: true, coverageFirst: true });
	}

	onMount(() => {
		defaultDay();
		void load();
		// Coming back to the tab asks past the server's cache (fresh=1); the minute-by-minute
		// refresh while visible does NOT: the server's cache already drops on every change,
		// so everybody looking at the same month shares one computation per minute.
		const stopResume = onResume(
			() => {
				if (loading || checkingFree) return;
				return load({ fresh: true, silent: true });
			},
			{ minIntervalMs: 15_000 }
		);
		const timer = setInterval(() => {
			if (document.visibilityState === 'hidden' || loading || checkingFree) return;
			void load({ silent: true });
		}, 60_000);
		return () => {
			stopResume();
			clearInterval(timer);
		};
	});
</script>

{#snippet personAvatar(p: TeamCalPerson, size: 'sm' | 'default' | 'lg', ring = false)}
	<Avatar.Root {size} class="text-[0.65rem] font-semibold" style={ring ? `box-shadow: 0 0 0 2px ${p.color}` : undefined}>
		{#if p.avatar_url}
			<Avatar.Image src={p.avatar_url} alt={p.name} class="object-cover" />
		{/if}
		<Avatar.Fallback>{initials(p.name)}</Avatar.Fallback>
	</Avatar.Root>
{/snippet}

{#snippet swatch(style: string)}
	<span class="inline-block h-2.5 w-4 shrink-0 rounded-[3px]" {style}></span>
{/snippet}

{#snippet coverageBar(info: CellInfo | null)}
	<!-- Sin cubrir = a taller bar of red diagonal hatch; solo el propietario = amber dots: two
	     shapes, so the two never differ by hue alone (red/amber is a colour-blind pair). -->
	{#if info && info.cov.state === 'gaps'}
		<span class="block h-2 w-full rounded-sm" data-cell-uncovered style={UNCOVERED_STYLE}></span>
	{:else if info && info.cov.state === 'owner_only'}
		<span class="block h-1.5 w-full rounded-sm" data-cell-owner-only style={OWNER_ONLY_STYLE}></span>
	{:else if info && info.cov.state === 'covered'}
		<span class="block h-1.5 w-full rounded-sm bg-emerald-500/50"></span>
	{:else}
		<span class="block h-1.5"></span>
	{/if}
{/snippet}

{#snippet dot(d: CellInfo['dots'][number])}
	<span class="size-1.5 shrink-0 rounded-full sm:size-2" data-dot={d.kind} data-booked={d.booked ? '' : undefined} style={personDotStyle(d, d.color)}></span>
{/snippet}

<section class="mt-8" aria-labelledby="team-calendar-title">
	<div class="mb-3 flex items-start justify-between gap-2">
		<div class="min-w-0">
			<h2 id="team-calendar-title" class="text-lg font-semibold tracking-tight">Calendario del equipo</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Quién trabaja, quién está libre u ocupado, y qué horas no cubre nadie todavía.
			</p>
		</div>
		<Button
			variant="ghost"
			size="icon-sm"
			onclick={() => load({ fresh: true })}
			disabled={loading}
			aria-label="Actualizar"
			title="Actualizar"
		>
			<RefreshCwIcon class={loading || checkingFree ? 'animate-spin' : ''} />
		</Button>
	</div>

	<div class="rounded-lg border bg-card p-3 sm:p-4">
		<!-- View + filters -->
		<div class="flex flex-col gap-3 md:flex-row md:items-end md:justify-between">
			<div class="flex flex-col gap-2">
				<div class="grid grid-cols-3 gap-1 rounded-lg bg-muted p-1 sm:inline-grid sm:w-80" role="group" aria-label="Vista del calendario">
					{#each VIEWS as v (v)}
						<button
							type="button"
							class="rounded-md px-2 py-1.5 text-sm font-medium transition-colors
								{view === v ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
							aria-pressed={view === v}
							onclick={() => setView(v)}
						>
							{VIEW_LABELS[v]}
						</button>
					{/each}
				</div>
				<div class="flex flex-wrap gap-1.5" role="group" aria-label="Filtrar por área">
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
			</div>
			<div class="flex min-w-0 flex-col gap-1 md:w-72">
				<span class="text-xs text-muted-foreground">Ver en hora de</span>
				<Combobox
					items={zoneItems}
					bind:value={() => tz, (v) => changeZone(v)}
					placeholder="Elige una zona"
					searchPlaceholder="Buscar país o ciudad…"
				/>
			</div>
		</div>

		<!-- People: everybody in their colour; tapping some shows only them. -->
		{#if teamPeople.length > 0}
			<div class="mt-3 flex flex-wrap gap-1" role="group" aria-label="Personas del equipo">
				<button
					type="button"
					class="rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors
						{picked.size === 0 ? 'border-primary bg-primary text-primary-foreground' : 'bg-background hover:bg-muted'}"
					aria-pressed={picked.size === 0}
					onclick={() => chosen.clear()}
				>
					Todo el equipo
				</button>
				{#each teamPeople as p (p.user_id)}
					{@const on = picked.has(p.user_id)}
					<button
						type="button"
						data-person-filter
						class="inline-flex max-w-[10rem] items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium transition-colors
							{on ? 'bg-muted' : 'bg-background hover:bg-muted'}"
						style={on ? `border-color:${p.color}; box-shadow: inset 0 0 0 1px ${p.color}` : undefined}
						aria-pressed={on}
						aria-label="Ver solo a {p.name}{p.is_you ? ' (tú)' : ''}"
						onclick={() => togglePerson(p.user_id)}
					>
						<span class="size-2.5 shrink-0 rounded-full" style="background:{p.color}"></span>
						<span class="truncate">{firstName(p.name)}{p.is_you ? ' (tú)' : ''}</span>
					</button>
				{/each}
			</div>
		{/if}

		<!-- Period navigation -->
		<div class="mt-4 flex items-center gap-1">
			<Button variant="ghost" size="icon-sm" onclick={() => move(-1)} aria-label="Periodo anterior">
				<ChevronLeftIcon />
			</Button>
			<p class="min-w-0 flex-1 truncate text-center text-sm font-semibold first-letter:uppercase" aria-live="polite" data-period-label>
				{periodLabel(view, period.from, period.to)}
			</p>
			<Button variant="ghost" size="icon-sm" onclick={() => move(1)} disabled={!canNext} aria-label="Periodo siguiente">
				<ChevronRightIcon />
			</Button>
			<Button variant="outline" size="sm" onclick={goToday} disabled={today >= period.from && today <= period.to && selectedDay === today}>Hoy</Button>
		</div>

		{#if checkingFree && current}
			<p class="mt-1 text-center text-[0.7rem] text-muted-foreground" aria-live="polite">Revisando los calendarios de cada persona…</p>
		{/if}

		{#if loadError}
			<div class="mt-3 rounded-lg border border-destructive/40 bg-destructive/5 px-3 py-3 text-sm">
				<p class="text-destructive">No se pudo cargar el calendario del equipo.</p>
				<p class="mt-0.5 text-xs text-muted-foreground">{loadError}</p>
				<Button class="mt-2" variant="outline" size="sm" onclick={() => load({ fresh: true })}>Reintentar</Button>
			</div>
		{/if}
		{#if freeError && current}
			<p class="mt-3 rounded-md bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-300">{freeError}</p>
		{/if}

		<!-- The calendar -->
		<div class="mt-3" aria-busy={loading}>
			{#if view === 'week'}
				<!-- The week's hours, once, above the days (each day uses the same window). Left:
				     the card's 11 px + the lanes' 24 px label gutter + 4 px gap. -->
				<div class="relative mb-1 ml-[39px] mr-[11px] h-4 text-[0.6rem] text-muted-foreground" aria-hidden="true">
					{#each timelineTicks(weekRange, true) as t (t)}
						{@const x = pct(t, weekRange)}
						<span
							class="absolute whitespace-nowrap sm:hidden {x <= 2 ? '' : x >= 98 ? '-translate-x-full' : '-translate-x-1/2'}"
							style="left:{x}%">{tickLabel(t)}</span
						>
					{/each}
					{#each timelineTicks(weekRange, false) as t (t)}
						{@const x = pct(t, weekRange)}
						<span
							class="absolute hidden whitespace-nowrap sm:inline {x <= 2 ? '' : x >= 98 ? '-translate-x-full' : '-translate-x-1/2'}"
							style="left:{x}%">{tickLabel(t)}</span
						>
					{/each}
				</div>
				<ol class="space-y-1.5">
					{#each weekDays as d (d)}
						{@const info = cells[d]}
						{#if d > reqTo}
							<li class="rounded-lg border bg-muted/30 px-2.5 py-2 text-xs text-muted-foreground/70" data-day-beyond={d}>
								<span class="font-semibold first-letter:uppercase">{shortDay(d)}</span>
								<span class="ml-2">Más de un año: todavía no se puede consultar.</span>
							</li>
						{:else}
						<li>
							<button
								type="button"
								data-day={d}
								class="w-full rounded-lg border px-2.5 py-2 text-left transition-colors
									{d === selectedDay ? 'border-primary ring-1 ring-primary' : 'bg-background hover:bg-muted/50'}"
								aria-pressed={d === selectedDay}
								aria-label={cellLabel(d, info)}
								onclick={() => pickDay(d)}
							>
								<span class="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
									<span class="w-14 shrink-0 font-semibold first-letter:uppercase {d === today ? 'text-primary' : ''}">
										{d === today ? 'Hoy' : shortDay(d)}
									</span>
									{#if info}
										<!-- No truncate: on a phone the gaps text goes to its own line below, so the
										     spelled-out counts ("3 personas libres · 1 sesión") are never cut. -->
										<span class="min-w-0 flex-1 text-muted-foreground">
											{#if d < (current?.today ?? today)}
												{info.sessions} {info.sessions === 1 ? 'sesión' : 'sesiones'}
											{:else}
												<span class="sm:hidden" data-week-counts>{dayCountsText(info, freeIncluded, 'phone')}</span>
												<span class="hidden sm:inline">{dayCountsText(info, freeIncluded, 'wide')}</span>
											{/if}
										</span>
										{#if info.cov.state === 'gaps'}
											<span class="w-full pl-16 font-medium text-red-700 sm:w-auto sm:shrink-0 sm:pl-0 dark:text-red-400" data-gaps-text>{uncoveredText(info.cov, true)}</span>
										{:else if info.cov.state === 'owner_only'}
											<span class="w-full pl-16 font-medium text-amber-800 sm:w-auto sm:shrink-0 sm:pl-0 dark:text-amber-300">{ownerOnlyText(info.cov, true)}</span>
										{/if}
									{:else if loading}
										<span class="h-3 flex-1 animate-pulse rounded bg-muted"></span>
									{/if}
								</span>
								{#if info && current}
									<span class="mt-1.5 block">
										<!-- Every row of the filter on every day, in the same order: lane 3
										     is the same person all week (empty when off). -->
										<TeamDayTimeline
											people={rows}
											day={d}
											today={current.today}
											{freeIncluded}
											coverage={info.cov.areas}
											range={weekRange}
											compact
										/>
									</span>
								{/if}
							</button>
						</li>
						{/if}
					{/each}
				</ol>
			{:else}
				<div class="grid grid-cols-7 gap-px overflow-hidden rounded-lg border bg-border" data-grid={view}>
					{#each WEEK_HEADERS as h (h.short)}
						<div class="bg-muted/60 py-1 text-center text-[0.65rem] font-medium uppercase text-muted-foreground">
							<span class="sm:hidden" aria-hidden="true">{h.letter}</span>
							<span class="hidden sm:inline" aria-hidden="true">{h.short}</span>
							<span class="sr-only">{h.long}</span>
						</div>
					{/each}
					{#each grid as g (g.day)}
						{@const info = g.inRange ? cells[g.day] : null}
						{#if !g.inRange}
							<div class="bg-muted/30 p-1 text-[0.7rem] text-muted-foreground/50 {view === 'fortnight' ? 'min-h-20 sm:min-h-28' : 'min-h-16 sm:min-h-24'}" aria-hidden="true">
								{dayNumber(g.day)}
							</div>
						{:else}
							<button
								type="button"
								data-day={g.day}
								class="flex min-w-0 flex-col gap-1 p-1 text-left transition-colors sm:p-1.5
									{view === 'fortnight' ? 'min-h-20 sm:min-h-28' : 'min-h-16 sm:min-h-24'}
									{g.day === selectedDay ? 'bg-primary/10 ring-2 ring-inset ring-primary' : 'bg-background hover:bg-muted/50'}"
								aria-pressed={g.day === selectedDay}
								aria-label={cellLabel(g.day, info)}
								onclick={() => pickDay(g.day)}
							>
								<span class="flex items-center justify-between gap-0.5">
									<span
										class="grid size-5 place-items-center rounded-full text-[0.7rem] font-semibold
											{g.day === today ? 'bg-primary text-primary-foreground' : g.day < today ? 'text-muted-foreground' : ''}"
									>
										{dayNumber(g.day)}
									</span>
									{#if info}
										<span class="flex min-w-0 items-center gap-1 text-[0.6rem] leading-none text-muted-foreground sm:text-[0.65rem]" aria-hidden="true">
											{#if freeIncluded && g.day >= (current?.today ?? today) && info.free > 0}
												<span class="hidden lg:inline">{plural(info.free, 'libre', 'libres')}</span>
											{/if}
											{#if info.sessions > 0}
												<!-- Booked dates at a glance: the session count (a striped mark + the number on phones). -->
												<span class="inline-flex items-center gap-0.5 font-medium text-foreground" data-cell-sessions title={plural(info.sessions, 'sesión', 'sesiones')}>
													<span class="inline-block size-1.5 rounded-[2px] sm:hidden" style="background-color:{tint('#64748b', 20)};background-image:{stripes('#64748b', 1)};box-shadow:inset 0 0 0 1px #64748b"></span>{info.sessions}<span class="hidden md:inline">&nbsp;{info.sessions === 1 ? 'sesión' : 'sesiones'}</span>
												</span>
											{/if}
										</span>
									{/if}
								</span>
								{#if info}
									<!-- Phones: one dot per person (up to 8); from sm: names. -->
									<span class="flex flex-wrap gap-1 p-px sm:hidden">
										{#each info.dots.slice(0, 8) as d (d.user)}{@render dot(d)}{/each}
										{#if info.dots.length > 8}<span class="text-[0.55rem] leading-none text-muted-foreground">+{info.dots.length - 8}</span>{/if}
									</span>
									<span class="hidden min-w-0 flex-col gap-0.5 sm:flex">
										{#each info.dots.slice(0, view === 'fortnight' ? 5 : 3) as d (d.user)}
											<span class="flex min-w-0 items-center gap-1 text-[0.65rem] leading-tight">
												{@render dot(d)}<span class="truncate">{firstName(d.name)}</span>
											</span>
										{/each}
										{#if info.dots.length > (view === 'fortnight' ? 5 : 3)}
											<span class="text-[0.6rem] text-muted-foreground">+{info.dots.length - (view === 'fortnight' ? 5 : 3)} más</span>
										{/if}
									</span>
									<span class="mt-auto block">
										{#if info.cov.state === 'gaps'}
											<span class="mb-0.5 hidden truncate text-[0.6rem] font-medium text-red-700 sm:block dark:text-red-400">
												{uncoveredText(info.cov, true)}
											</span>
										{:else if info.cov.state === 'owner_only'}
											<span class="mb-0.5 hidden truncate text-[0.6rem] font-medium text-amber-800 sm:block dark:text-amber-300">
												{ownerOnlyText(info.cov, true)}
											</span>
										{/if}
										{@render coverageBar(info)}
									</span>
								{:else if loading}
									<span class="mt-1 h-2 w-3/4 animate-pulse rounded bg-muted"></span>
								{/if}
							</button>
						{/if}
					{/each}
				</div>
				{#if reqTo < period.to}
					<!-- The grey days after the one-year limit would look like the next month's padding. -->
					<p class="mt-1.5 text-xs text-muted-foreground">
						Después del {shortDay(reqTo)} todavía no se puede consultar (más de un año).
					</p>
				{/if}
			{/if}
		</div>

		<!-- Legend: never colour alone - each kind has its own pattern. -->
		<ul class="mt-3 flex flex-wrap gap-x-3 gap-y-1 text-[0.7rem] text-muted-foreground" aria-label="Leyenda">
			<li class="inline-flex items-center gap-1">{@render swatch(personDotStyle({ kind: 'free', booked: false }, '#64748b'))}Libre</li>
			<li class="inline-flex items-center gap-1">
				<span class="ml-0.5">{@render swatch(personDotStyle({ kind: 'free', booked: true }, '#64748b'))}</span>Libre, con sesiones
			</li>
			<li class="inline-flex items-center gap-1">
				{@render swatch(`background-color:${tint('#64748b', 15)};background-image:${stripes('#64748b', 2)};box-shadow:inset 0 0 0 1px #64748b`)}Ocupado
			</li>
			<li class="inline-flex items-center gap-1">{@render swatch(personDotStyle({ kind: 'full', booked: false }, '#64748b'))}Trabaja, sin cupos</li>
			<li class="inline-flex items-center gap-1" data-legend-unknown>
				{@render swatch(personDotStyle({ kind: 'unknown', booked: false }, '#64748b'))}Sin revisar su calendario
			</li>
			<li class="inline-flex items-center gap-1">{@render swatch(UNCOVERED_STYLE)}Sin cubrir</li>
			<li class="inline-flex items-center gap-1">{@render swatch(OWNER_ONLY_STYLE)}Solo el propietario</li>
			<li class="inline-flex items-center gap-1">{@render swatch('background:rgb(16 185 129 / 0.5)')}Cubierto</li>
			{#if area === 'all'}
				<!-- The letter on the chips' avatars and after the names in the lanes. -->
				<li class="inline-flex items-center gap-1" data-legend-area="mentoria">
					<span class="grid size-3.5 place-items-center rounded-full bg-primary text-[0.5rem] font-bold leading-none text-primary-foreground" aria-hidden="true">M</span>Mentoría
				</li>
				<li class="inline-flex items-center gap-1" data-legend-area="soporte">
					<span class="grid size-3.5 place-items-center rounded-full bg-amber-500 text-[0.5rem] font-bold leading-none text-white" aria-hidden="true">S</span>Soporte
				</li>
			{/if}
		</ul>

		<!-- The target -->
		{#if current}
			<div class="mt-3 flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md bg-muted/40 px-3 py-2 text-xs">
				<span class="font-medium">Horario a cubrir:</span>
				<span class="min-w-0 text-muted-foreground" data-target-summary>{targetSummary(current.target)}</span>
				{#if current.target.can_edit}
					<Button variant="link" size="xs" class="ml-auto h-auto px-0" onclick={() => (targetOpen = true)}>
						<PencilIcon />Cambiar
					</Button>
				{/if}
			</div>
		{/if}

		<!-- The chosen day -->
		{#if selectedDay && current && !loadError}
			<div class="mt-5 border-t pt-4" data-day-detail>
				<div class="flex flex-wrap items-baseline justify-between gap-x-2">
					<!-- Focus lands here after a tap on a phone (showDetail); scroll-mt clears the sticky top bar. -->
					<h3
						bind:this={detailHeading}
						tabindex="-1"
						class="scroll-mt-16 text-sm font-semibold outline-none first-letter:uppercase"
						data-day-heading
					>
						{longDay(selectedDay)}{selectedDay === today ? ' · hoy' : ''}
					</h3>
					{#if dayInfo && selectedDay >= current.today && selectedDay <= reqTo}
						<p class="text-xs text-muted-foreground" data-day-counts>{dayCountsText(dayInfo, freeIncluded, 'long')}</p>
					{/if}
				</div>

				{#if selectedDay > reqTo}
				<p class="mt-2 text-sm text-muted-foreground" data-day-beyond>Este día está a más de un año; todavía no se puede consultar.</p>
				{:else}
				<!-- Coverage of the day -->
				<div class="mt-2 space-y-1.5" data-day-coverage>
					{#if !dayInfo || dayInfo.cov.state === 'past'}
						<p class="text-xs text-muted-foreground">Día pasado: se muestran solo las sesiones.</p>
					{:else if dayInfo.cov.state === 'none'}
						<p class="text-xs text-muted-foreground">Este día no hay horario a cubrir.</p>
					{:else}
						{#each dayInfo.cov.areas as c (c.area)}
							<div class="rounded-md border px-2.5 py-1.5 text-xs">
								<span class="font-medium">{AREA_LABELS[c.area]}</span>
								{#if c.uncovered.length}
									<p class="mt-0.5 flex items-start gap-1.5 text-red-700 dark:text-red-400">
										<span class="mt-0.5">{@render swatch(UNCOVERED_STYLE)}</span>
										<span>Sin cubrir: {c.uncovered.map(spanLabel).join(' · ')}</span>
									</p>
								{/if}
								{#if c.ownerOnly.length}
									<p class="mt-0.5 flex items-start gap-1.5 text-amber-800 dark:text-amber-300">
										<span class="mt-0.5">{@render swatch(OWNER_ONLY_STYLE)}</span>
										<span>Solo el propietario: {c.ownerOnly.map(spanLabel).join(' · ')}</span>
									</p>
								{/if}
								{#if !c.uncovered.length && !c.ownerOnly.length}
									<p class="mt-0.5 text-emerald-700 dark:text-emerald-400">Todo el horario a cubrir tiene a alguien.</p>
								{/if}
							</div>
						{/each}
					{/if}
				</div>

				<!-- Lanes -->
				{#if dayRows.length > 0 || (dayInfo && dayInfo.cov.areas.length > 0)}
					<div class="mt-3">
						<TeamDayTimeline
							people={dayRows}
							day={selectedDay}
							today={current.today}
							{freeIncluded}
							coverage={dayInfo?.cov.areas ?? []}
							showAreaTag={area === 'all'}
							onPick={openPerson}
						/>
					</div>
				{/if}

				<!-- Who can start at each hour -->
				{#if selectedDay >= current.today}
					<h4 class="mb-1.5 mt-4 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Horarios libres</h4>
					{#if !freeIncluded}
						<p class="text-xs text-muted-foreground">{checkingFree ? 'Revisando los calendarios…' : 'No se pudieron revisar los horarios libres.'}</p>
					{:else if hourRows.length === 0}
						<p class="rounded-lg border border-dashed px-3 py-4 text-center text-sm text-muted-foreground">Nadie tiene horarios libres este día.</p>
					{:else}
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
										{#each row.people as p (p.key)}
											<button
												type="button"
												data-person-chip
												class="inline-flex max-w-full items-center gap-1 rounded-full border-2 bg-background py-0.5 pl-0.5 pr-2 text-xs font-medium transition-colors hover:bg-muted"
												style="border-color: {p.color}"
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
				{/if}

				<!-- Each person's day -->
				{#if dayRows.length > 0}
					<h4 class="mb-1.5 mt-4 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Personas</h4>
					<ul class="space-y-2">
						{#each dayRows as p (p.key)}
							{@const st = personDayState(p, selectedDay, current.today, freeIncluded, checkingFree)}
							{@const free = freeSpans(p, selectedDay)}
							{@const busy = busyOn(p, selectedDay)}
							<li class="flex items-start gap-2 rounded-lg border px-2.5 py-2" data-person-row={p.key}>
								<button type="button" class="flex min-w-0 flex-1 items-start gap-2 text-left" onclick={() => openPerson(p)}>
									<span class="mt-0.5 shrink-0">{@render personAvatar(p, 'default', true)}</span>
									<span class="min-w-0">
										<span class="block truncate text-sm font-medium">
											{p.name}{p.is_you ? ' (tú)' : ''}
											<span class="text-xs font-normal text-muted-foreground">· {AREA_LABELS[p.area]}</span>
										</span>
										{#if st.kind === 'free'}
											<span class="block text-xs text-foreground">Libre: {free.map(spanLabel).join(' · ')}</span>
										{:else if st.note}
											<span
												class="block text-xs {st.kind === 'unknown' || st.kind === 'error'
													? 'text-amber-700 dark:text-amber-400'
													: 'text-muted-foreground'}">{st.note}</span
											>
										{/if}
										{#if busy.length}
											<span class="block text-xs text-muted-foreground">
												Ocupado: {busy.map((b) => `${spanLabel([b.start, b.end])} (${b.type})`).join(' · ')}
											</span>
										{/if}
									</span>
								</button>
								<Button variant="outline" size="xs" onclick={() => copyLink(p)} aria-label="Copiar el enlace de {p.name}">
									<CopyIcon />Copiar
								</Button>
							</li>
						{/each}
					</ul>
				{:else if selectedDay >= current.today}
					<p class="mt-3 rounded-lg border border-dashed px-3 py-4 text-center text-sm text-muted-foreground">Nadie trabaja este día.</p>
				{:else}
					<p class="mt-3 text-center text-sm text-muted-foreground">No hubo sesiones este día.</p>
				{/if}
				{#if offPeople.length > 0}
					<p class="mt-2 text-xs text-muted-foreground">No trabajan este día: {offPeople.map((p) => firstName(p.name)).join(', ')}.</p>
				{/if}
				{/if}
			</div>
		{/if}

		{#if current && current.people.length === 0}
			<p class="mt-3 text-xs text-muted-foreground">
				Aún no hay tipos predefinidos de Mentoría o Soporte, o nadie del equipo tiene su enlace activo.
			</p>
		{/if}
	</div>
</section>

{#if current}
	<CoverageTargetDialog bind:open={targetOpen} target={current.target} onSaved={targetSaved} />
{/if}

<Dialog.Root bind:open={dialogOpen}>
	<Dialog.Content class="max-w-[calc(100%-2rem)] rounded-lg sm:max-w-md">
		{#if dialogPerson && current}
			{@const p = dialogPerson}
			{@const st = personDayState(p, selectedDay, current.today, freeIncluded, checkingFree)}
			{@const starts = freeStartLabels(p, selectedDay)}
			{@const busy = busyOn(p, selectedDay)}
			<Dialog.Header>
				<div class="flex items-center gap-3">
					<Avatar.Root size="lg" class="size-14 text-base font-semibold" style="box-shadow: 0 0 0 2px {p.color}">
						{#if p.avatar_url}
							<Avatar.Image src={p.avatar_url} alt={p.name} class="object-cover" />
						{/if}
						<Avatar.Fallback>{initials(p.name)}</Avatar.Fallback>
					</Avatar.Root>
					<div class="min-w-0 text-left">
						<Dialog.Title class="truncate">{p.name}{p.is_you ? ' (tú)' : ''}</Dialog.Title>
						<Dialog.Description>{AREA_LABELS[p.area]} · sesiones de {durationText(p.duration_min)}</Dialog.Description>
					</div>
				</div>
			</Dialog.Header>
			<div>
				<p class="mb-2 text-xs text-muted-foreground first-letter:uppercase">
					{longDay(selectedDay)} · hora de {tz.replaceAll('_', ' ')}
				</p>
				{#if st.kind === 'free'}
					<div class="flex max-h-48 flex-wrap gap-1.5 overflow-y-auto">
						{#each starts as s (s)}
							<span class="rounded-full border px-2 py-0.5 text-xs tabular-nums" style="border-color:{p.color}">{s}</span>
						{/each}
					</div>
				{:else}
					<p class="text-sm {st.kind === 'unknown' || st.kind === 'error' ? 'text-amber-700 dark:text-amber-400' : 'text-muted-foreground'}">
						{st.note || 'No tiene horarios libres este día.'}
					</p>
				{/if}
				{#if busy.length}
					<p class="mt-3 text-xs font-medium">Sesiones</p>
					<ul class="mt-1 space-y-0.5 text-xs text-muted-foreground">
						{#each busy as b (b.key)}<li>{spanLabel([b.start, b.end])} · {b.type}</li>{/each}
					</ul>
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
