<script lang="ts">
	import { onMount } from 'svelte';
	import {
		api,
		teamApi,
		reassignErrorText,
		AREA_LABELS,
		type Attendance,
		type Booking,
		type EventType,
		type ReassignCandidate,
		type WhatsAppNotice
	} from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { prefs, fmtDateTime, fmtTime } from '$lib/prefs';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import * as Select from '$lib/components/ui/select';
	import { DatePicker } from '$lib/components/ui/date-picker';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import * as Dialog from '$lib/components/ui/dialog';
	import { toast } from 'svelte-sonner';

	let items: Booking[] = $state([]);
	let loading = $state(true);
	let error = $state('');

	// Members (mentors, support staff) see only their own hosted bookings. The owner and
	// admins supervise, so they can switch to the workspace-wide view (?scope=all). Must
	// match parseBookingListFilter in booking_handler.go (the retired "support" desk tier
	// no longer widens it).
	const canSeeAll = $derived($currentUser?.is_admin ?? false);

	// Fork: the owner and admins open on "Todas las reservas" unless they chose otherwise;
	// the choice is remembered per user in this browser (staff may share one). Storage can
	// be blocked (private mode, policies), so every access is guarded: no stored value just
	// means the default. The layout mounts pages only once currentUser is set.
	function initialScope(): 'mine' | 'all' {
		if (!$currentUser?.is_admin) return 'mine';
		try {
			const v = localStorage.getItem(`agenda.bookings.scope.${$currentUser.id}`);
			if (v === 'mine' || v === 'all') return v;
		} catch { /* storage unavailable: use the default */ }
		return 'all';
	}
	function rememberScope(s: 'mine' | 'all') {
		if (!$currentUser) return;
		try { localStorage.setItem(`agenda.bookings.scope.${$currentUser.id}`, s); } catch { /* not remembered, harmless */ }
	}
	let scope = $state<'mine' | 'all'>(initialScope());

	// Filtering, sorting and paging all happen in SQL now. They used to happen here,
	// over a response that contained every booking the user could see - which meant
	// the page got slower with every booking ever made, and the server did unbounded
	// work to render 25 rows.
	let timeFilter = $state<'upcoming' | 'past'>('upcoming');
	let fEventType = $state('');
	let fHost = $state('');
	let fTeam = $state('');
	let fStatus = $state('');
	// Fork: 'mentoria' | 'soporte', derived server-side from the booking's type.
	let fArea = $state('');

	const PAGE_SIZE = 25;
	let offset = $state(0);
	let total = $state(0);
	let counts = $state({ upcoming: 0, past: 0 });

	// Options for the filter selects, fetched once. label = what the option reads (the
	// Mentoría template says it covers every mentor: the server expands its slug to the
	// template plus all the mentors' copies).
	let eventTypes = $state<{ slug: string; name: string; label: string }[]>([]);
	let members = $state<{ id: string; name: string }[]>([]);
	let teams = $state<{ id: string; name: string }[]>([]);

	const hasFilters = $derived(!!(fEventType || fHost || fTeam || fStatus || fArea));
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
	// A failed request is NOT cached as "no answers": to a supervisor that reads as "the
	// client answered nothing". It shows an error with a retry instead.
	let answersFailed: Record<string, boolean> = $state({});

	async function loadAnswers(id: string) {
		answersLoading[id] = true;
		answersFailed[id] = false;
		try {
			const res = await api.get<{ items: AnswerItem[] }>(`/v1/bookings/${id}/answers`);
			answersCache[id] = res.items ?? [];
		} catch {
			answersFailed[id] = true;
		}
		answersLoading[id] = false;
	}

	async function toggleExpand(id: string) {
		if (expandedId === id) { expandedId = null; return; }
		expandedId = id;
		if (answersCache[id] === undefined && !answersLoading[id]) await loadAnswers(id);
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
		if (fArea) p.set('area', fArea);
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
		rememberScope(s);
		// Host, team and área only mean anything across the workspace.
		if (s === 'mine') { fHost = ''; fTeam = ''; fArea = ''; }
		await reload();
	}

	async function setTimeFilter(t: 'upcoming' | 'past') {
		if (timeFilter === t) return;
		timeFilter = t;
		await reload();
	}

	function clearFilters() {
		fEventType = fHost = fTeam = fStatus = fArea = '';
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
		const opts = new Map<string, { slug: string; name: string; label: string }>();
		const allMentors = (name: string) => `${name} (todos los mentores)`;
		try {
			const res = await api.get<{ items: EventType[] }>('/v1/event-types');
			for (const et of res.items ?? []) {
				const kind = et.team?.kind;
				if (kind === 'mentoria_copy') {
					// Copies never get an option of their own. A mentor's copy stands for the
					// template (same name, and the server narrows it to what they host); the
					// owner's list holds every copy, and they all collapse into the template.
					const slug = et.team?.template_slug;
					if (slug && !opts.has(slug)) {
						const name = et.team?.template_name || et.name;
						opts.set(slug, { slug, name, label: name });
					}
					continue;
				}
				const label = kind === 'mentoria_template' ? allMentors(et.name) : et.name;
				opts.set(et.slug, { slug: et.slug, name: et.name, label });
			}
		} catch { /* leave the dropdown empty */ }
		eventTypes = [...opts.values()];
		if (!canSeeAll) return;
		// An admin who is not the owner lists only their own/hosted types; the team's two
		// predefined types come from the team settings so they can filter by them too.
		try {
			const ts = await teamApi.getSettings();
			if (ts.mentoria_template) {
				const t = ts.mentoria_template;
				opts.set(t.slug, { slug: t.slug, name: t.name, label: allMentors(t.name) });
			}
			if (ts.soporte_shared && !opts.has(ts.soporte_shared.slug)) {
				const t = ts.soporte_shared;
				opts.set(t.slug, { slug: t.slug, name: t.name, label: t.name });
			}
			eventTypes = [...opts.values()];
		} catch { /* the own/hosted options stay */ }
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
	let pendingCancelName = $state('');
	// Optional, and sent as typed: it reaches the client in the cancellation e-mail and in
	// the WhatsApp {motivo}. Empty sends "" - that line is then dropped from the message
	// (it used to send the English "cancelled by admin" to Spanish-speaking clients).
	let cancelReason = $state('');

	function requestCancel(b: Booking) {
		pendingCancelId = b.id;
		pendingCancelName = b.attendees?.[0]?.name ?? '';
		cancelReason = '';
		confirmOpen = true;
	}

	async function cancel() {
		const id = pendingCancelId;
		if (!id) return;
		try {
			await api.post(`/v1/bookings/${id}/cancel`, { reason: cancelReason.trim() });
			toast.success('Reunión cancelada. Los avisos pendientes se detuvieron.');
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			confirmOpen = false;
			pendingCancelId = null;
		}
	}

	// A confirmed meeting that has not started yet: the only kind worth cancelling.
	function cancellable(b: Booking) {
		return b.status === 'confirmed' && new Date(b.start_at).getTime() > Date.now();
	}

	// ── Fork: the four WhatsApp notices of each booking (GET /v1/bookings "whatsapp") ──
	const NOTICE_LABELS: Record<WhatsAppNotice['kind'], string> = {
		created: 'Confirmación',
		morning: 'Mañana',
		'1h': '1 hora',
		'5m': '5 min'
	};
	const NOTICE_STATES: Record<WhatsAppNotice['status'], { label: string; cls: string }> = {
		sent: { label: 'Enviado', cls: 'border-green-200 bg-green-50 text-green-800 dark:border-green-900 dark:bg-green-950/40 dark:text-green-300' },
		pending: { label: 'Pendiente', cls: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300' },
		sending: { label: 'Enviando…', cls: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300' },
		failed: { label: 'Falló', cls: 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300' },
		cancelled: { label: 'Cancelado', cls: 'border-border bg-muted/50 text-muted-foreground line-through decoration-1' },
		missed: { label: 'No salió', cls: 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300' },
		unknown: { label: 'Sin registro', cls: 'border-dashed border-border bg-background text-muted-foreground' },
		not_applicable: { label: 'No aplica', cls: 'border-dashed border-border bg-background text-muted-foreground' }
	};

	// Short "when" for a notice, in the viewer's own zone: "hoy 07:00", "mañana 07:00",
	// or "30/09 07:00" (day/month order follows the user's date preference).
	function shortWhen(iso?: string): string {
		if (!iso) return '';
		const d = new Date(iso);
		if (isNaN(d.getTime())) return '';
		const time = fmtTime(iso, $prefs);
		const dayKey = (x: Date) => `${x.getFullYear()}-${x.getMonth()}-${x.getDate()}`;
		const today = new Date();
		const tomorrow = new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1);
		const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
		if (dayKey(d) === dayKey(today)) return `hoy ${time}`;
		if (dayKey(d) === dayKey(tomorrow)) return `mañana ${time}`;
		if (dayKey(d) === dayKey(yesterday)) return `ayer ${time}`;
		const dd = String(d.getDate()).padStart(2, '0');
		const mm = String(d.getMonth() + 1).padStart(2, '0');
		return `${$prefs.date_format === 'mdy' || $prefs.date_format === 'ymd' ? `${mm}/${dd}` : `${dd}/${mm}`} ${time}`;
	}

	function noticeDetail(n: WhatsAppNotice): string {
		if (n.status === 'pending' || n.status === 'sent' || n.status === 'missed') return shortWhen(n.at);
		return '';
	}

	// Full sentence for screen readers and the hover title.
	function noticeText(n: WhatsAppNotice, i: number): string {
		const base = `Aviso ${i + 1}, ${NOTICE_LABELS[n.kind]}: ${(NOTICE_STATES[n.status] ?? NOTICE_STATES.not_applicable).label}`;
		if (n.status === 'pending' && n.at) return `${base}, programado para ${fmt(n.at)}`;
		if (n.status === 'sent' && n.at) return `${base} el ${fmt(n.at)}`;
		if (n.status === 'failed') return `${base}: el webhook no respondió bien tras varios intentos`;
		if (n.status === 'not_applicable') return `${base}: no se programó (por la hora de la cita o porque no hay webhook para este tipo)`;
		if (n.status === 'cancelled') return `${base}: la reunión se canceló antes de enviarlo`;
		if (n.status === 'missed') return `${base}: no se envió a su hora (el servidor estaba detenido o dormido) y se descartó para no llegar tarde`;
		if (n.status === 'unknown') return `${base}: sin registro (los registros de envío se borran a los 30 días)`;
		return base;
	}

	// ── Fork: "Pasar a otra persona" (owner and admins) ──
	// One Dialog: the candidates of the booking's área (reassign-candidates), what will
	// happen, and one button. No stacked ConfirmDialog (it cannot show busy nor stay open).
	let passOpen = $state(false);
	let passBooking = $state<Booking | null>(null);
	let passCandidates = $state<ReassignCandidate[]>([]);
	let passLoading = $state(false);
	let passLoadError = $state('');
	let passChoice = $state('');
	let passBusy = $state(false);
	let passError = $state('');
	const passChosen = $derived(passCandidates.find((c) => c.id === passChoice));
	// The change reaches the client by WhatsApp only through a webhook of this type: when all
	// four notices are "No aplica" there is none, so the dialog does not promise it.
	const passHasWhatsApp = $derived(!!passBooking?.whatsapp?.some((n) => n.status !== 'not_applicable'));

	async function loadPassCandidates() {
		if (!passBooking) return;
		passLoading = true;
		passLoadError = '';
		try {
			const id = passBooking.id;
			const list = await teamApi.reassignCandidates(id);
			if (passBooking?.id !== id) return;
			passCandidates = list;
		} catch (e: any) {
			passLoadError = e?.message || 'No se pudo cargar la lista de personas.';
		} finally {
			passLoading = false;
		}
	}

	function openPass(b: Booking) {
		passBooking = b;
		passCandidates = [];
		passChoice = '';
		passError = '';
		passOpen = true;
		loadPassCandidates();
	}

	async function confirmPass() {
		if (!passBooking || !passChosen || passBusy) return;
		passBusy = true;
		passError = '';
		try {
			await teamApi.reassign(passBooking.id, passChosen.id);
			toast.success(`La reunión ahora la atiende ${passChosen.name}. Se avisó al cliente.`);
			passOpen = false;
			await load();
		} catch (e) {
			passError = reassignErrorText(e);
		} finally {
			passBusy = false;
		}
	}

	// Pass only a confirmed session that has not started (the same rule as cancelling).
	function passable(b: Booking) {
		return canSeeAll && cancellable(b);
	}

	// Reprogramar: the server lets only the primary host move a session (PATCH .../reschedule
	// 404s for anyone else), and only upcoming ones make sense.
	function reschedulable(b: Booking) {
		return b.status === 'confirmed' && !!$currentUser && b.host_id === $currentUser.id &&
			new Date(b.start_at).getTime() > Date.now();
	}

	// ── Fork: attendance from the video room (list item "attendance") ──
	// A chip in the WhatsApp notices' style, no emoji. Nothing for pending / not applicable.
	const ATTENDANCE_CLS = {
		ok: 'border-green-200 bg-green-50 text-green-800 dark:border-green-900 dark:bg-green-950/40 dark:text-green-300',
		warn: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300',
		bad: 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300',
		live: 'border-sky-200 bg-sky-50 text-sky-800 dark:border-sky-900 dark:bg-sky-950/40 dark:text-sky-300'
	};
	function attendanceChip(b: Booking): { label: string; cls: string } | null {
		const a: Attendance | undefined = b.attendance;
		if (!a) return null;
		switch (a.status) {
			case 'attended':
				return {
					label: a.minutes_together && a.minutes_together > 0 ? `Atendida · ${a.minutes_together} min` : 'Atendida',
					cls: ATTENDANCE_CLS.ok
				};
			case 'attended_unverified':
				return { label: 'Entraron 2 personas; no se identificó a quien atiende', cls: ATTENDANCE_CLS.warn };
			case 'client_absent':
				return { label: 'El cliente no entró', cls: ATTENDANCE_CLS.warn };
			case 'host_absent':
				return { label: `${b.host_name || 'Quien atiende'} no entró`, cls: ATTENDANCE_CLS.bad };
			case 'in_progress':
				return { label: 'En curso', cls: ATTENDANCE_CLS.live };
			case 'nobody':
				return { label: 'Nadie entró', cls: ATTENDANCE_CLS.bad };
			default:
				return null;
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

<!-- Stacks on a phone: the scope toggle beside the title overflowed 375 px. -->
<div class="mb-8 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Reservas</h1>
		<p class="mt-1 text-sm text-muted-foreground">
			{scope === 'all' ? 'Todas las reuniones del equipo de trabajo.' : 'Reuniones de las que eres anfitrión.'}
		</p>
	</div>
	<div class="flex flex-wrap items-center gap-2 sm:shrink-0">
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

		<div class="grid w-full grid-cols-1 gap-2 sm:ml-auto sm:flex sm:w-auto sm:flex-wrap sm:items-center">
			<Select.Root type="single" bind:value={fEventType} onValueChange={reload}>
				<Select.Trigger class="h-9 w-full sm:w-[170px]" aria-label="Filtrar por tipo de atención">
					{fEventType ? eventTypeName(fEventType) : 'Todos los tipos de atención'}
				</Select.Trigger>
				<Select.Content>
					<Select.Item value="" label="Todos los tipos de atención">Todos los tipos de atención</Select.Item>
					{#each eventTypes as et (et.slug)}
						<Select.Item value={et.slug} label={et.label}>{et.label}</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>

			{#if canSeeAll && scope === 'all'}
				<!-- Fork: área from the booking's type (Mentoría = the template and every copy). -->
				<Select.Root type="single" bind:value={fArea} onValueChange={reload}>
					<Select.Trigger class="h-9 w-full sm:w-[140px]" aria-label="Filtrar por área">
						{fArea === 'mentoria' || fArea === 'soporte' ? AREA_LABELS[fArea] : 'Todas las áreas'}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="" label="Todas las áreas">Todas las áreas</Select.Item>
						<Select.Item value="mentoria" label="Mentoría">Mentoría</Select.Item>
						<Select.Item value="soporte" label="Soporte">Soporte</Select.Item>
					</Select.Content>
				</Select.Root>

				<Select.Root type="single" bind:value={fHost} onValueChange={reload}>
					<Select.Trigger class="h-9 w-full sm:w-[150px]" aria-label="Filtrar por anfitrión">
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
						<Select.Trigger class="h-9 w-full sm:w-[140px]" aria-label="Filtrar por equipo">
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
				<Select.Trigger class="h-9 w-full sm:w-[140px]" aria-label="Filtrar por estado">
					{fStatus ? (statusLabel[fStatus] ?? fStatus) : 'Cualquier estado'}
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
	<!-- Fork: one card per booking instead of a table row, so it stacks on a phone (375 px)
	     without overflowing. The client's NAME leads; then when, what and who attends, the
	     four WhatsApp notices, and the actions. -->
	<div class="overflow-hidden rounded-lg border bg-card">
		<Tooltip.Provider>
		<ul class="divide-y">
			{#each items as b (b.id)}
				<li class="transition-colors hover:bg-muted/20">
					<div class="space-y-3 p-4">
					<div class="flex flex-col gap-3 md:flex-row md:items-start md:justify-between md:gap-6">
						<div class="min-w-0 flex-1 space-y-2">
							<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
								<p class="min-w-0 break-words text-base font-semibold leading-snug">
									{b.attendees?.[0]?.name || 'Sin nombre'}
								</p>
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
							{#if b.attendees?.[0]?.email}
								<p class="-mt-1 break-all text-xs text-muted-foreground">{b.attendees[0].email}</p>
							{/if}
							<dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-sm">
								<dt class="text-muted-foreground">Fecha</dt>
								<dd class="font-medium">{fmt(b.start_at)}</dd>
								<dt class="text-muted-foreground">Tipo</dt>
								<dd class="break-words">{b.event_type_name || eventTypeName(b.event_type_slug)}</dd>
								<dt class="text-muted-foreground">Atiende</dt>
								<dd class="break-words">{b.host_name || '—'}</dd>
								{#if attendanceChip(b)}
									{@const chip = attendanceChip(b)!}
									<dt class="text-muted-foreground">Asistencia</dt>
									<dd class="min-w-0">
										<span class="inline-flex max-w-full rounded-md border px-2 py-0.5 text-xs font-medium {chip.cls}">{chip.label}</span>
									</dd>
								{/if}
								{#if b.status === 'cancelled' && b.cancellation_reason}
									<dt class="text-muted-foreground">Motivo</dt>
									<dd class="break-words text-muted-foreground">{b.cancellation_reason}</dd>
								{/if}
							</dl>
						</div>

						<div class="flex shrink-0 flex-wrap items-center gap-1 md:justify-end">
							<Tooltip.Root>
								<Tooltip.Trigger
									class={buttonVariants({ variant: 'ghost', size: 'icon' })}
									onclick={() => toggleExpand(b.id)}
									aria-expanded={expandedId === b.id}
									aria-label={expandedId === b.id ? 'Ocultar respuestas' : 'Ver respuestas'}
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
									<!-- Fork: only the person who attends moves their own upcoming session. -->
									{#if reschedulable(b)}
										<Tooltip.Root>
											<Tooltip.Trigger
												class={buttonVariants({ variant: 'ghost', size: 'icon' })}
												onclick={() => startReschedule(b)}
												aria-label="Reprogramar"
											>
												<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
											</Tooltip.Trigger>
											<Tooltip.Content>Reprogramar</Tooltip.Content>
										</Tooltip.Root>
									{/if}
									{#if passable(b)}
										<Button variant="outline" size="sm" onclick={() => openPass(b)}>
											Pasar a otra persona
										</Button>
									{/if}
									{#if cancellable(b)}
										<Button
											variant="outline"
											size="sm"
											class="text-destructive hover:bg-destructive/10 hover:text-destructive"
											onclick={() => requestCancel(b)}
										>
											Cancelar reunión
										</Button>
									{/if}
								{/if}
							{/if}
						</div>
					</div>
					{#if b.whatsapp && b.whatsapp.length > 0}
						<!-- Full card width, and four columns only once the card itself is wide
						     (@container): the viewport says nothing about the room left beside the
						     desktop sidebar, which squeezed four columns to "C…" at 768-1024 px. -->
						<div class="@container">
							<p class="mb-1 text-xs font-medium text-muted-foreground">Avisos de WhatsApp</p>
							<ul class="grid grid-cols-2 gap-1.5 @2xl:grid-cols-4" aria-label="Avisos de WhatsApp">
								{#each b.whatsapp as n, i (n.kind)}
									{@const st = NOTICE_STATES[n.status] ?? NOTICE_STATES.not_applicable}
									{@const detail = noticeDetail(n)}
									<li
										class="flex min-w-0 items-center gap-1.5 rounded-md border px-2 py-1 text-xs {st.cls}"
										title={noticeText(n, i)}
										aria-label={noticeText(n, i)}
									>
										<span class="flex size-5 shrink-0 items-center justify-center rounded-full bg-background/80 text-[11px] font-semibold text-foreground no-underline" aria-hidden="true">{i + 1}</span>
										<span class="min-w-0 leading-tight" aria-hidden="true">
											<span class="block truncate font-medium">{NOTICE_LABELS[n.kind] ?? n.kind}</span>
											<span class="block truncate">{st.label}</span>
											{#if detail}<span class="block truncate tabular-nums">{detail}</span>{/if}
										</span>
									</li>
								{/each}
							</ul>
						</div>
					{/if}
					</div>

					{#if expandedId === b.id}
						<div class="border-t bg-muted/20 px-4 py-3">
							<p class="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Detalles</p>
							<dl class="mb-3 space-y-1.5 text-sm">
								<div class="flex flex-col gap-0.5 sm:flex-row sm:gap-4">
									<dt class="shrink-0 font-medium text-foreground sm:w-48">Reservado el</dt>
									<dd class="text-muted-foreground">{fmt(b.created_at)}</dd>
								</div>
								{#if b.payment_status}
									<div class="flex flex-col gap-0.5 sm:flex-row sm:gap-4">
										<dt class="shrink-0 font-medium text-foreground sm:w-48">Pago</dt>
										<dd class="text-muted-foreground">
											{payLabel[b.payment_status] ?? b.payment_status}{#if b.amount_paid_cents} · {fmtMoney(b.amount_paid_cents, b.amount_paid_currency)}{/if}
										</dd>
									</div>
								{/if}
								{#if b.location_value}
									<div class="flex flex-col gap-0.5 sm:flex-row sm:gap-4">
										<dt class="shrink-0 font-medium text-foreground sm:w-48">Ubicación</dt>
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
							{:else if answersFailed[b.id]}
								<div class="flex flex-col gap-2 rounded-md bg-destructive/10 px-3 py-2 sm:flex-row sm:items-center sm:justify-between" role="alert">
									<p class="text-sm text-destructive">No se pudieron cargar las respuestas.</p>
									<Button variant="outline" size="sm" onclick={() => loadAnswers(b.id)}>Reintentar</Button>
								</div>
							{:else if !answersCache[b.id] || answersCache[b.id].length === 0}
								<p class="text-sm text-muted-foreground">No hay respuestas de formulario para esta reserva.</p>
							{:else}
								<dl class="space-y-2">
									{#each answersCache[b.id] as a}
										<div class="flex flex-col gap-0.5 text-sm sm:flex-row sm:gap-4">
											<dt class="shrink-0 font-medium text-foreground sm:w-48">{a.label}</dt>
											<dd class="break-words text-muted-foreground {a.type !== 'checkbox' ? 'whitespace-pre-wrap' : ''}">
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
					{/if}

					{#if reschedulingId === b.id}
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
								<div class="mt-4 flex flex-wrap gap-2">
									<Button onclick={confirmReschedule} disabled={rescheduling}>
										{rescheduling ? 'Reprogramando…' : `Confirmar — ${fmtSlotTime(selectedSlot)}`}
									</Button>
									<Button variant="outline" onclick={cancelReschedule}>
										Cancelar
									</Button>
								</div>
							{/if}
						</div>
					{/if}
				</li>
			{/each}
		</ul>
		</Tooltip.Provider>
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

<!-- Fork: "Pasar a otra persona" - one Dialog, sized for a phone (no edge-to-edge at 375 px). -->
<Dialog.Root bind:open={passOpen}>
	<Dialog.Content class="max-w-[calc(100%-2rem)] rounded-lg sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Pasar a otra persona</Dialog.Title>
			<Dialog.Description>
				{#if passBooking}
					{passBooking.attendees?.[0]?.name || 'Sin nombre'} · {fmt(passBooking.start_at)}
					{#if passBooking.host_name} · la atiende {passBooking.host_name}{/if}
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		<div class="max-h-[50vh] space-y-3 overflow-y-auto">
			{#if passLoading}
				<p class="text-sm text-muted-foreground">Cargando personas…</p>
			{:else if passLoadError}
				<div class="flex flex-col gap-2 rounded-md bg-destructive/10 px-3 py-2 sm:flex-row sm:items-center sm:justify-between" role="alert">
					<p class="text-sm text-destructive">No se pudo cargar la lista de personas.</p>
					<Button variant="outline" size="sm" onclick={loadPassCandidates}>Reintentar</Button>
				</div>
			{:else if passCandidates.length === 0}
				<p class="text-sm text-muted-foreground">No hay otra persona del área disponible. Puedes cancelar la reunión en su lugar.</p>
			{:else}
				<div class="space-y-1.5">
					<Label for="pass-to">¿A quién?</Label>
					<Select.Root type="single" bind:value={passChoice} disabled={passBusy}>
						<Select.Trigger id="pass-to" class="w-full">
							{passChosen?.name ?? 'Elige una persona…'}
						</Select.Trigger>
						<Select.Content>
							{#each passCandidates as c (c.id)}
								<Select.Item value={c.id} label={c.name}>
									{c.name}{#if c.area === 'mentoria' || c.area === 'soporte'}<span class="ml-1 text-xs text-muted-foreground">· {AREA_LABELS[c.area]}</span>{/if}
								</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
				<p class="text-sm text-muted-foreground">
					La fecha y la hora no cambian y la reunión pasa a la agenda de esa persona.
					{#if passHasWhatsApp}
						Se avisará al cliente por correo (si está configurado) y por WhatsApp, si tu webhook incluye los cambios de horario.
					{:else}
						Se avisará al cliente por correo, si está configurado.
					{/if}
				</p>
			{/if}
			{#if passError}
				<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{passError}</p>
			{/if}
		</div>

		<Dialog.Footer class="gap-2">
			<Button variant="outline" disabled={passBusy} onclick={() => (passOpen = false)}>Cancelar</Button>
			<Button disabled={!passChosen || passBusy} onclick={confirmPass}>
				{passBusy ? 'Pasando…' : passChosen ? `Pasar a ${passChosen.name}` : 'Pasar'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title={pendingCancelName ? `¿Cancelar la reunión con ${pendingCancelName}?` : '¿Cancelar esta reunión?'}
	description="Se avisará al cliente y el horario quedará libre. Los recordatorios de WhatsApp pendientes se detienen. Esta acción no se puede deshacer."
	confirmText="Cancelar reunión"
	cancelText="Mantener reunión"
	destructive
	onConfirm={cancel}
>
	<div class="space-y-1.5">
		<Label for="cancel-reason">Motivo <span class="font-normal text-muted-foreground">(opcional — el cliente lo verá en el aviso de cancelación)</span></Label>
		<Textarea id="cancel-reason" bind:value={cancelReason} rows={3} maxlength={300} placeholder="Por ejemplo: el mentor tuvo un imprevisto" class="text-base sm:text-sm" />
	</div>
</ConfirmDialog>
