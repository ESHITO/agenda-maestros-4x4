<script lang="ts">
	// Fork (Agenda Maestros 4x4): one day of the team calendar as horizontal lanes - the
	// coverage of each área on top (red diagonal hatch = nobody works that hour, amber dots =
	// only the owner), set apart from the people, then one lane per person in their colour:
	// solid = free to book, faint = works but nothing free, dashed outline = works but their
	// calendar is not checked yet (or could not be), stripes = a session. Never colour alone:
	// every kind has its own pattern, and the lane says it in text for screen readers.
	// `compact` is the week view's thin version: no names or axis, but a 24 px gutter with the
	// área letter (coverage) or the person's dot and initials, and the caller passes the SAME
	// people every day so a lane is the same person all week.
	import type { TeamCalPerson } from '$lib/api';
	import { AREA_LABELS } from '$lib/api';
	import {
		AREA_LETTER,
		OWNER_ONLY_STYLE,
		UNCOVERED_STYLE,
		busyOn,
		freeSpans,
		freeUnknown,
		pct,
		spanLabel,
		stripes,
		tickLabel,
		timelineTicks,
		timelineWindow,
		tint,
		type AreaCoverage
	} from '$lib/team-calendar';
	import { firstName, initials } from '$lib/team-availability';

	let {
		people,
		day,
		today,
		freeIncluded,
		coverage,
		compact = false,
		showAreaTag = false,
		range,
		onPick
	}: {
		people: TeamCalPerson[];
		day: string;
		today: string;
		freeIncluded: boolean;
		coverage: AreaCoverage[];
		compact?: boolean;
		showAreaTag?: boolean;
		/** A fixed window (the week view lines every day up on the same hours). */
		range?: { start: number; end: number };
		onPick?: (p: TeamCalPerson) => void;
	} = $props();

	const past = $derived(day < today);
	const win = $derived(range ?? timelineWindow(people, coverage, day));
	const ticks = $derived(timelineTicks(win, true));
	const ticksWide = $derived(timelineTicks(win, false));

	// The first and last ticks lean inwards so "8 a. m." never spills out of the card.
	function tickAlign(t: number) {
		const x = pct(t, win);
		return x <= 2 ? 'translate-x-0' : x >= 98 ? '-translate-x-full' : '-translate-x-1/2';
	}

	function seg(s: number, e: number) {
		const l = pct(s, win);
		return `left:${l}%;width:${Math.max(0.6, pct(e, win) - l)}%`;
	}

	function laneText(p: TeamCalPerson): string {
		const d = p.days[day];
		const parts: string[] = [];
		if (!past) {
			const free = freeUnknown(p, freeIncluded) ? [] : freeSpans(p, day);
			if (free.length) parts.push(`libre ${free.map(spanLabel).join(', ')}`);
			else if ((d?.hours?.length ?? 0) > 0)
				parts.push(
					`trabaja ${d!.hours!.map(spanLabel).join(', ')}${freeUnknown(p, freeIncluded) ? ', sin revisar su calendario' : ', sin cupos libres'}`
				);
		}
		const busy = busyOn(p, day);
		if (busy.length) parts.push(`ocupado ${busy.map((b) => spanLabel([b.start, b.end])).join(', ')}`);
		return `${p.name}: ${parts.join('; ') || 'nada este día'}`;
	}

	const laneRow = $derived(compact ? 'flex items-center gap-1' : 'sm:flex sm:items-center sm:gap-2');
</script>

<div class="relative" data-timeline>
	{#if !compact}
		<!-- Axis: fewer ticks on a phone, where "12 p. m." needs room. -->
		<div class="relative ml-0 h-4 text-[0.6rem] text-muted-foreground sm:ml-34" aria-hidden="true">
			{#each ticks as t (t)}
				<span class="absolute {tickAlign(t)} whitespace-nowrap sm:hidden" style="left:{pct(t, win)}%">{tickLabel(t)}</span>
			{/each}
			{#each ticksWide as t (t)}
				<span class="absolute hidden {tickAlign(t)} whitespace-nowrap sm:inline" style="left:{pct(t, win)}%">{tickLabel(t)}</span>
			{/each}
		</div>
	{/if}

	{#if coverage.length > 0}
		<!-- Coverage: taller than a person's lane and set apart by a dashed rule. -->
		<div class="{compact ? 'mb-1 space-y-0.5 border-b border-dashed pb-1' : 'mb-2 space-y-1.5 border-b border-dashed pb-2'}" data-coverage-block>
			{#each coverage as c (c.area)}
				<div class={laneRow} data-coverage-lane={c.area}>
					{#if compact}
						<span class="w-6 shrink-0 text-[0.55rem] font-bold leading-none text-muted-foreground" title="Cobertura · {AREA_LABELS[c.area]}" aria-hidden="true">
							{AREA_LETTER[c.area]}
						</span>
					{:else}
						<span class="block truncate text-[0.7rem] font-medium text-muted-foreground sm:w-32 sm:shrink-0">
							Cobertura · {AREA_LABELS[c.area]}
						</span>
					{/if}
					<div class="relative {compact ? 'h-2' : 'h-3'} w-full overflow-hidden rounded-sm bg-muted/40">
						{#each c.target as t, i (i)}
							<span class="absolute inset-y-0 bg-emerald-500/25" style={seg(t[0], t[1])}></span>
						{/each}
						{#each c.ownerOnly as t, i (i)}
							<span class="absolute inset-y-0" data-owner-only style="{seg(t[0], t[1])};{OWNER_ONLY_STYLE}" title="Solo el propietario: {spanLabel(t)}"></span>
						{/each}
						{#each c.uncovered as t, i (i)}
							<span class="absolute inset-y-0" data-uncovered style="{seg(t[0], t[1])};{UNCOVERED_STYLE}" title="Sin cubrir: {spanLabel(t)}"></span>
						{/each}
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<div class={compact ? 'space-y-0.5' : 'space-y-1.5'}>
		{#each people as p (p.key)}
			{@const d = p.days[day]}
			{@const unknown = !past && freeUnknown(p, freeIncluded)}
			<div class={laneRow} data-lane={p.key}>
				{#if compact}
					<span class="flex w-6 shrink-0 items-center gap-0.5 overflow-hidden" title="{p.name} · {AREA_LABELS[p.area]}" aria-hidden="true">
						<span class="size-1.5 shrink-0 rounded-full" style="background:{p.color}"></span>
						<span class="truncate text-[0.5rem] font-semibold leading-none text-muted-foreground">{initials(p.name)}</span>
					</span>
				{:else}
					<button
						type="button"
						class="flex max-w-full items-center gap-1 truncate text-left text-[0.7rem] font-medium hover:underline sm:w-32 sm:shrink-0"
						onclick={() => onPick?.(p)}
						tabindex="-1"
						aria-hidden="true"
					>
						<span class="size-2 shrink-0 rounded-full" style="background:{p.color}"></span>
						<span class="truncate">{firstName(p.name)}{showAreaTag ? ` · ${AREA_LETTER[p.area]}` : ''}</span>
					</button>
				{/if}
				<div class="relative {compact ? 'h-1.5' : 'h-3.5'} w-full overflow-hidden rounded-sm bg-muted/30">
					{#if !past}
						{#each d?.hours ?? [] as h, i (i)}
							<span
								class="absolute inset-y-0 rounded-[2px]"
								data-hours={unknown ? 'unknown' : 'known'}
								style="{seg(h[0], h[1])};{unknown ? `border:1px dashed ${p.color}` : `background:${tint(p.color, 28)}`}"
							></span>
						{/each}
						{#if !unknown}
							{#each freeSpans(p, day) as f, i (i)}
								<span class="absolute inset-y-0" data-free style="{seg(f[0], f[1])};background:{p.color}"></span>
							{/each}
						{/if}
					{/if}
					{#each busyOn(p, day) as b (b.key)}
						<span
							class="absolute inset-y-0"
							data-busy
							style="{seg(b.start, b.end)};background-color:{tint(p.color, 15)};background-image:{stripes(p.color, compact ? 2 : 3)};box-shadow:inset 0 0 0 1px {p.color}"
							title="{p.name}: {b.type} {spanLabel([b.start, b.end])}"
						></span>
					{/each}
				</div>
				<span class="sr-only">{laneText(p)}</span>
			</div>
		{/each}
	</div>
</div>
