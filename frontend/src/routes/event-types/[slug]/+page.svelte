<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import { api, copyText, type EventType, type EventTypeHost, type TeamMember, type Team, type CalendarStatus, type ZoomStatus } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import * as Select from '$lib/components/ui/select';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import QuestionsPanel from '$lib/components/event-types/QuestionsPanel.svelte';
	import EmbedPanel from '$lib/components/event-types/EmbedPanel.svelte';
	// Fork: WhatsApp texts per moment (saved on their own, see the component).
	import WhatsAppMessagesPanel from '$lib/components/event-types/WhatsAppMessagesPanel.svelte';

	// Ordered by expected usage. 'custom_video' is retired from the picker but the
	// backend still renders any legacy event types that use it.
	// Spanish labels for the read-only routing_mode summary (edit mode derives its
	// own wording from the two-question flow above); falls back to the raw value
	// for any mode this UI doesn't know about.
	const ROUTING_MODE_LABELS: Record<string, string> = {
		fixed: 'Fijo',
		round_robin: 'Rotación',
		collective: 'Colectivo',
	};

	const LOCATION_TYPES = [
		{ value: 'zoom',         label: 'Zoom' },
		{ value: 'teams',        label: 'Microsoft Teams' },
		{ value: 'google_meet',  label: 'Google Meet' },
		{ value: 'livekit',      label: 'Calnode Video (LiveKit)' },
		{ value: 'phone',        label: 'Llamada telefónica' },
		{ value: 'link',         label: 'Enlace de video' },
		{ value: 'in_person',    label: 'En persona' },
	];

	const LOCATION_NEEDS_VALUE: Record<string, string> = {
		link: 'URL de la reunión', zoom: 'Enlace de Zoom', google_meet: 'Enlace de Meet',
		teams: 'Enlace de Teams', phone: 'Número de teléfono', in_person: 'Dirección', custom_video: 'URL de la reunión',
	};
	// Placeholder hint per type — in-person is the only optional value.
	const LOCATION_PLACEHOLDER: Record<string, string> = {
		zoom: 'https://…zoom.us/j/… (obligatorio)', link: 'https://… (obligatorio)',
		phone: '+1 555 123 4567 (obligatorio)', in_person: 'Dirección (opcional)',
	};

	// ── Event type ───────────────────────────────────────────────────────────────
	let et = $state<EventType | null>(null);
	const TABS = [
		{ id: 'general', label: 'General' },
		{ id: 'hosts', label: 'Anfitriones' },
		{ id: 'notifications', label: 'Notificaciones' },
		{ id: 'questions', label: 'Preguntas' },
		{ id: 'whatsapp', label: 'WhatsApp' }, // fork
		{ id: 'embed', label: 'Insertar' }
	] as const;
	let activeTab = $state<(typeof TABS)[number]['id']>('general');
	let waPanel = $state<{ saveFromShortcut: () => void } | undefined>(); // fork: Ctrl/Cmd+S on the WhatsApp tab
	let etLoading = $state(true);
	let etError = $state('');
	let etSaving = $state(false);

	// ── Fork: predefined types (et.team) ─────────────────────────────────────────
	// Decided BEFORE `owned`: a mentor's copy is owned by the template's owner, so for the
	// owner owned = true, yet every edit of a copy is refused (409) - it is read-only for
	// everyone, and edited through its template. The template (T) and the shared Soporte
	// type (S) stay editable by their owner, except who hosts them: that follows the áreas
	// set in Miembros, so no hosts PUT and no routing fields in the PATCH.
	const teamKind = $derived(et?.team?.kind);
	const isCopy = $derived(teamKind === 'mentoria_copy');
	const isManaged = $derived(teamKind === 'mentoria_template' || teamKind === 'soporte_shared');
	const mentorName = $derived(et?.team?.mentor_name || et?.mentor_name || et?.team?.host_name || '');
	async function copyBookLink() {
		const url = `${window.location.origin}/book/${et?.slug ?? slug}`;
		if (await copyText(url)) toast.success('Enlace copiado');
		else toast.error('No se pudo copiar; mantén pulsado el enlace para copiarlo.');
	}

	// Connected calendar (the owner's) — drives the meeting-link auto-generation hint.
	let calStatus = $state<CalendarStatus | null>(null);
	// Owner's Zoom connection — drives the Zoom auto-mint hint.
	let zoomStatus = $state<ZoomStatus | null>(null);
	// Which connected provider can natively mint each online platform's link.
	const PLATFORM_PROVIDER: Record<string, string> = { google_meet: 'google', teams: 'microsoft' };
	const isOnlineMeeting = (t: string) => t === 'google_meet' || t === 'teams';

	let form = $state({
		name: '', slug: '', description: '', duration_minutes: 30, slot_interval_minutes: 30,
		is_active: true, is_public: true, show_taken_slots: false,
		location_type: 'link', location_value: '',
		buffer_before_minutes: 0, buffer_after_minutes: 0,
		min_notice_minutes: 0, max_future_days: 60,
		max_active_bookings: 1,
		price_cents: 0, currency: 'usd',
	});

	// Price is edited in major units (e.g. dollars); stored as integer cents.
	let priceMajor = $state('0');

	// True when the connected calendar will auto-generate the chosen platform's link.
	const meetAutoGen = $derived(
		isOnlineMeeting(form.location_type) &&
			!!calStatus?.connected &&
			calStatus?.provider === PLATFORM_PROVIDER[form.location_type]
	);

	// True when the owner's connected Zoom account will auto-mint a meeting per booking.
	const zoomAutoGen = $derived(form.location_type === 'zoom' && !!zoomStatus?.connected);

	// ── Routing ──────────────────────────────────────────────────────────────────
	// The editor asks two plain questions — "who can host?" and (for a team)
	// "do they rotate or all attend?" — and derives routing_mode + host roles from
	// the answers. The engine and DB are unchanged; this is purely how roles are
	// authored.
	const RR_STRATEGIES = [
		{ value: 'even',     label: 'Equitativo — menos reservas próximas' },
		{ value: 'priority', label: 'Prioridad — primero el inicio de la lista' },
		{ value: 'soonest',  label: 'Disponibilidad más próxima' },
	];
	type Strategy = 'even' | 'priority' | 'soonest';
	let rrStrategy = $state<Strategy>('even');

	type Host = { user_id: string; name: string; email: string };
	type TogetherHost = Host & { optional: boolean };

	// Q1: just me, or specific people?  Q2 (people only): rotate, or all attend?
	let hostScope = $state<'me' | 'people'>('me');
	let staffing = $state<'rotate' | 'together'>('rotate');
	// Rotation pool (each "rotation"); together = required + optional join-if-free.
	let rotationHosts = $state<Host[]>([]);
	let togetherHosts = $state<TogetherHost[]>([]);
	let hostsLoaded = $state(false);
	let transferOwner = $state('');
	let transferring = $state(false);
	let transferHosts = $state<EventTypeHost[]>([]);
	async function transferEvent() {
		if (!transferOwner || !$currentUser) return;
		transferring = true;
		try {
			await api.post(`/v1/event-types/${slug}/transfer`, { expected_owner_id: $currentUser.id, new_owner_id: transferOwner });
			toast.success('Evento transferido');
			await goto(`${base}/event-types`);
		} catch (error) {
			toast.error(error instanceof Error ? error.message : 'No se pudo transferir el evento');
		} finally {
			transferring = false;
		}
	}

	let members = $state<TeamMember[]>([]);
	let teams = $state<Team[]>([]);

	// routing_mode is derived from the two answers — never set directly.
	const routingMode = $derived(
		hostScope === 'me' ? 'fixed' : staffing === 'rotate' ? 'round_robin' : 'collective'
	);

	async function loadHosts() {
		try {
			const res = await api.get<{ items: EventTypeHost[] }>(`/v1/event-types/${slug}/hosts`);
			const items = res.items ?? [];
			transferHosts = items.filter(h => h.role === 'required' && h.user_id !== $currentUser?.id);
			const toHost = (h: EventTypeHost): Host => ({ user_id: h.user_id, name: h.name, email: h.email });
			rotationHosts = items.filter((h) => h.role === 'rotation').map(toHost);
			togetherHosts = items
				.filter((h) => h.role === 'required' || h.role === 'optional')
				.map((h) => ({ ...toHost(h), optional: h.role === 'optional' }));
			hostsLoaded = true;
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron cargar los anfitriones');
		}
	}

	async function loadMembers() {
		if (members.length > 0) return;
		try {
			members = await api.get<TeamMember[]>('/v1/users');
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron cargar los miembros');
		}
	}

	async function loadTeams() {
		if (teams.length > 0) return;
		try {
			const res = await api.get<{ items: Team[] }>('/v1/teams');
			teams = res.items ?? [];
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron cargar los equipos');
		}
	}

	function setScope(s: 'me' | 'people') {
		hostScope = s;
		if (s === 'people') {
			if (!hostsLoaded) loadHosts();
			loadMembers();
			loadTeams();
		}
	}

	// Members already chosen in the *active* staffing list, so the pickers don't
	// re-offer them. Scoped to the visible list (rotation vs together): otherwise
	// hosts loaded for the other mode — e.g. a collective event's members still in
	// togetherHosts after switching to Rotate — would wrongly mark everyone
	// unavailable and make addOne skip every team member.
	const assignedIds = $derived(
		new Set((staffing === 'rotate' ? rotationHosts : togetherHosts).map((h) => h.user_id))
	);
	// Any active member not already chosen — including the current user, who can
	// be a host like anyone else.
	const availableMembers = $derived(
		members.filter((m) => !m.archived && !assignedIds.has(m.id))
	);

	type Target = 'rotation' | 'together';
	function addOne(target: Target, h: Host) {
		// Dedup against this target's own list, not the union — a person can sit in
		// only one staffing list, and the other list may hold stale members from a
		// previous mode.
		const list: { user_id: string }[] = target === 'rotation' ? rotationHosts : togetherHosts;
		if (list.some((x) => x.user_id === h.user_id)) return;
		if (target === 'rotation') rotationHosts = [...rotationHosts, h];
		else togetherHosts = [...togetherHosts, { ...h, optional: false }];
	}
	function addMember(target: Target, userId: string | undefined) {
		if (!userId) return;
		const m = members.find((x) => x.id === userId);
		if (m) addOne(target, { user_id: m.id, name: m.name, email: m.email });
	}
	async function addTeam(target: Target, teamId: string | undefined) {
		if (!teamId) return;
		try {
			const team = await api.get<Team>(`/v1/teams/${teamId}`);
			(team.members ?? [])
				.filter((tm) => !tm.archived)
				.forEach((tm) => addOne(target, { user_id: tm.id, name: tm.name, email: tm.email }));
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron cargar los miembros del equipo');
		}
	}
	function removePerson(target: Target, userId: string) {
		if (target === 'rotation') rotationHosts = rotationHosts.filter((h) => h.user_id !== userId);
		else togetherHosts = togetherHosts.filter((h) => h.user_id !== userId);
	}
	function setOptional(userId: string, optional: boolean) {
		togetherHosts = togetherHosts.map((h) => (h.user_id === userId ? { ...h, optional } : h));
	}
	// Reorder the rotation pool (priority = list position; index 0 is highest).
	function moveRotation(idx: number, dir: -1 | 1) {
		const list = [...rotationHosts];
		const j = idx + dir;
		if (j < 0 || j >= list.length) return;
		[list[idx], list[j]] = [list[j], list[idx]];
		rotationHosts = list;
	}

	// Notification / messaging state
	const REMINDER_OPTIONS = [
		{ value: 1, label: '1 hora antes' },
		{ value: 2, label: '2 horas antes' },
		{ value: 4, label: '4 horas antes' },
		{ value: 8, label: '8 horas antes' },
		{ value: 12, label: '12 horas antes' },
		{ value: 24, label: '24 horas antes' },
		{ value: 48, label: '2 días antes' },
		{ value: 72, label: '3 días antes' },
		{ value: 168, label: '1 semana antes' },
	];
	let reminders = $state<number[]>([]);
	let msg_confirmation = $state('');
	let msg_cancellation = $state('');
	let msg_reschedule = $state('');
	let msg_reminder = $state('');
	// Assistant's opening chat line for this event type — blank keeps the built-in
	// (translated) default; set overrides it verbatim, same as the email notes above.
	let msg_greeting = $state('');
	// Optional custom subject lines (empty = built-in default subject).
	let subj_confirmation = $state('');
	let subj_cancellation = $state('');
	let subj_reschedule = $state('');
	let subj_reminder = $state('');
	// Track which message accordions are open
	let msgOpen = $state({ confirmation: false, cancellation: false, reschedule: false, reminder: false });
	// Track preview toggle per accordion
	let previewOpen = $state({ confirmation: false, cancellation: false, reschedule: false, reminder: false });
	// Test email send state per type
	type MsgKey = 'confirmation' | 'cancellation' | 'reschedule' | 'reminder';
	let testSending = $state<Partial<Record<MsgKey, boolean>>>({});
	let testSent    = $state<Partial<Record<MsgKey, boolean>>>({});
	let testError   = $state<Partial<Record<MsgKey, string>>>({});

	let allowPhoneCall = $state(false);
	const slug = $page.params.slug;

	async function loadET() {
		etError = '';
		try {
			et = await api.get<EventType>(`/v1/event-types/${slug}`);
			allowPhoneCall = et.allow_phone_call;
			form = {
				name: et.name,
				slug: et.slug,
				description: et.description ?? '',
				duration_minutes: et.duration_minutes,
				slot_interval_minutes: et.slot_interval_minutes,
				is_active: et.is_active,
				is_public: et.is_public,
				show_taken_slots: et.show_taken_slots ?? false,
				location_type: et.location_type,
				location_value: et.location_value ?? '',
				buffer_before_minutes: et.buffer_before_minutes,
				buffer_after_minutes: et.buffer_after_minutes,
				min_notice_minutes: et.min_notice_minutes,
				max_future_days: et.max_future_days,
				max_active_bookings: et.max_active_bookings,
				price_cents: et.price_cents ?? 0,
				currency: et.currency ?? 'usd',
			};
			priceMajor = ((et.price_cents ?? 0) / 100).toFixed(2);
			reminders = et.reminders ?? [];
			if (et.routing_mode === 'round_robin') { hostScope = 'people'; staffing = 'rotate'; }
			else if (et.routing_mode === 'collective') { hostScope = 'people'; staffing = 'together'; }
			else { hostScope = 'me'; }
			rrStrategy = (['even', 'priority', 'soonest'].includes(et.rr_strategy ?? '')
				? et.rr_strategy : 'even') as Strategy;
			msg_confirmation = et.msg_confirmation ?? '';
			msg_cancellation = et.msg_cancellation ?? '';
			msg_reschedule = et.msg_reschedule ?? '';
			msg_reminder = et.msg_reminder ?? '';
			msg_greeting = et.msg_greeting ?? '';
			subj_confirmation = et.subj_confirmation ?? '';
			subj_cancellation = et.subj_cancellation ?? '';
			subj_reschedule = et.subj_reschedule ?? '';
			subj_reminder = et.subj_reminder ?? '';
		} catch (e: any) {
			etError = e.message;
		} finally {
			etLoading = false;
		}
	}

	async function saveET() {
		// Read-only views (a hosted type, a mentor's copy) have nothing to save; Ctrl/Cmd+S
		// must not send a PATCH the server refuses.
		if (!et || et.owned === false || isCopy) return;
		if (!form.name.trim()) { toast.error('El nombre es obligatorio.'); return; }
		if (form.duration_minutes < 5) { toast.error('La duración debe ser de al menos 5 minutos.'); return; }
		// Matches the API, which only requires a positive value. A stricter floor here would
		// make an event type configured below it via the API unsaveable from the editor -
		// including when the person is editing something else entirely.
		if (form.slot_interval_minutes < 1) { toast.error('El intervalo entre turnos debe ser de al menos 1 minuto.'); return; }
		if (form.max_active_bookings < 0) { toast.error('Las reservas activas máximas no pueden ser negativas (0 = ilimitado).'); return; }
		// Fork: a predefined type's hosts come from Miembros, so the host questions do not apply.
		const managed = isManaged;
		if (!managed && routingMode === 'round_robin' && rotationHosts.length === 0) {
			toast.error('Agrega al menos una persona a la rotación'); return;
		}
		if (!managed && routingMode === 'collective' && !togetherHosts.some((h) => !h.optional)) {
			toast.error('Agrega al menos un anfitrión requerido (alguien que siempre asista)'); return;
		}
		etSaving = true;
		try {
			const payload: Record<string, unknown> = {
				slug: form.slug.trim(),
				name: form.name.trim(),
				description: form.description.trim() || null,
				duration_minutes: Number(form.duration_minutes),
				slot_interval_minutes: Number(form.slot_interval_minutes),
				is_active: form.is_active,
				is_public: form.is_public,
				show_taken_slots: form.show_taken_slots,
				allow_phone_call: allowPhoneCall,
				location_type: form.location_type,
				location_value: form.location_value.trim() || null,
				buffer_before_minutes: Number(form.buffer_before_minutes),
				buffer_after_minutes: Number(form.buffer_after_minutes),
				min_notice_minutes: Number(form.min_notice_minutes),
				max_future_days: Number(form.max_future_days),
				max_active_bookings: Number(form.max_active_bookings),
				price_cents: Math.max(0, Math.round(Number(priceMajor) * 100)) || 0,
				currency: form.currency.trim().toLowerCase() || 'usd',
				routing_mode: routingMode,
				rr_strategy: rrStrategy,
				reminders,
				// Not `|| null`: this form always saves the whole page state, so a blanked
				// field must send '' to actually clear it. The API treats null as "leave
				// unchanged" (for partial-PATCH callers), which would silently keep the old
				// value here — the field would look cleared in the UI but persist server-side.
				msg_confirmation: msg_confirmation.trim(),
				msg_cancellation: msg_cancellation.trim(),
				msg_reschedule: msg_reschedule.trim(),
				msg_reminder: msg_reminder.trim(),
				msg_greeting: msg_greeting.trim(),
				subj_confirmation: subj_confirmation.trim(),
				subj_cancellation: subj_cancellation.trim(),
				subj_reschedule: subj_reschedule.trim(),
				subj_reminder: subj_reminder.trim(),
			};
			// Fork: left out on T and S. Sending them from a page loaded before the Soporte
			// rotation changed would put the stored routing back (the server also refuses a
			// change with 409).
			if (managed) {
				delete payload.routing_mode;
				delete payload.rr_strategy;
			}
			const updated = await api.patch<EventType>(`/v1/event-types/${slug}`, payload);
			// A rename moves the row out from under the name this page was loaded with, so
			// every request after the PATCH has to use the one the server just confirmed.
			const effSlug = updated?.slug || slug;

			if (managed) {
				// Fork: hosts are assigned from Miembros (the server refuses this PUT).
			} else if (routingMode === 'round_robin') {
				await api.put(`/v1/event-types/${effSlug}/hosts`, {
					hosts: rotationHosts.map((hh, i) => ({ user_id: hh.user_id, role: 'rotation', priority: i })),
				});
			} else if (routingMode === 'collective') {
				await api.put(`/v1/event-types/${effSlug}/hosts`, {
					hosts: togetherHosts.map((hh, i) => ({
						user_id: hh.user_id, role: hh.optional ? 'optional' : 'required', priority: i,
					})),
				});
			} else {
				await api.put(`/v1/event-types/${effSlug}/hosts`, {
					hosts: [{ user_id: $currentUser?.id, role: 'required', priority: 0 }],
				});
			}
			toast.success('Cambios guardados');
			// The URL carries the slug, so a rename has to move the page too or a reload
			// lands on a 404. replaceState: the old address no longer resolves, and
			// leaving it in history is a back button that breaks.
			if (effSlug !== slug) {
				await goto(`${base}/event-types/${effSlug}`, { replaceState: true });
				return;
			}
			await loadET();
			await loadHosts();
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron guardar los cambios');
		} finally {
			etSaving = false;
		}
	}

	async function sendTestEmail(type: MsgKey) {
		testSending = { ...testSending, [type]: true };
		testSent    = { ...testSent,    [type]: false };
		testError   = { ...testError,   [type]: '' };
		try {
			await api.post(`/v1/event-types/${slug}/test-email`, { type });
			testSent = { ...testSent, [type]: true };
			setTimeout(() => { testSent = { ...testSent, [type]: false }; }, 4000);
		} catch (e: any) {
			testError = { ...testError, [type]: e.message };
		} finally {
			testSending = { ...testSending, [type]: false };
		}
	}

	function buildPreview(type: MsgKey, note: string): string {
		const name     = et?.name ?? 'Mi evento';
		const loc      = et?.location_value ? `\nUbicación: ${et.location_value}` : '';
		const dur      = et?.duration_minutes ?? 30;
		const noteBlk  = note.trim() ? `\n---\n${note.trim()}\n` : '';
		const start    = 'Mañana, 2:00 PM UTC';
		const prev     = 'Hoy, 2:00 PM UTC';
		// Compute end time correctly by adding duration to 14:00.
		const endTotalMin = 14 * 60 + dur;
		const endH24      = Math.floor(endTotalMin / 60) % 24;
		const endMin      = endTotalMin % 60;
		const endPeriod   = endH24 < 12 ? 'AM' : 'PM';
		const endH12      = endH24 % 12 || 12;
		const end         = `Mañana, ${endH12}:${String(endMin).padStart(2, '0')} ${endPeriod} UTC`;

		switch (type) {
			case 'confirmation':
				return `Hola, Alex Johnson,\n\nTu reserva ha sido confirmada.\n\nEvento:   ${name}\nCon:      ${et?.name ?? 'Anfitrión'}\nInicio:   ${start}\nFin:      ${end}${loc}\n\nReferencia de la reserva: preview-test\n\nPara cancelar, visita:\n[página de reserva]${noteBlk}\n— Calnode`;
			case 'cancellation':
				return `Hola, Alex Johnson,\n\nTu reserva ha sido cancelada.\n\nEvento:   ${name}\nCon:      ${et?.name ?? 'Anfitrión'}\nInicio:   ${start}\nFin:      ${end}\n\nPara reservar de nuevo, visita:\n[página de reserva]${noteBlk}\n— Calnode`;
			case 'reschedule':
				return `Hola, Alex Johnson,\n\nTu reserva ha sido reprogramada.\n\nEvento:   ${name}\nCon:      ${et?.name ?? 'Anfitrión'}\nAntes:    ${prev}\nAhora:    ${start}\nFin:      ${end}${loc}\n\nReferencia de la reserva: preview-test${noteBlk}\n— Calnode`;
			case 'reminder':
				return `Hola, Alex Johnson,\n\nEste es un recordatorio de que tu reserva se acerca.\n\nEvento:   ${name}\nCon:      ${et?.name ?? 'Anfitrión'}\nInicio:   ${start}\nFin:      ${end}${loc}\n\nReferencia de la reserva: preview-test${noteBlk}\n— Calnode`;
		}
	}

	onMount(async () => {
		await loadET();
		// Editor-only data (owner-scoped endpoints) — skip for read-only hosts, and for a
		// mentor's copy (read-only for everyone, fork).
		if (et?.owned === false || et?.team?.kind === 'mentoria_copy') return;
		// Connected calendar — best-effort; drives the meeting-link hint only.
		api.get<CalendarStatus>('/v1/calendar/status').then((s) => (calStatus = s)).catch(() => {});
	api.get<ZoomStatus>('/v1/zoom/status').then((s) => (zoomStatus = s)).catch(() => {});
		await loadHosts();
		if (hostScope === 'people') {
			loadMembers();
			loadTeams();
		}
	});

</script>

{#snippet readOnlySummary(e: EventType)}
	<div class="rounded-lg border bg-card p-4 sm:p-6">
		<dl class="grid grid-cols-1 gap-x-4 gap-y-1 text-sm sm:grid-cols-[140px_1fr] sm:gap-y-3">
			<dt class="text-muted-foreground">Nombre</dt><dd class="mb-2 font-medium sm:mb-0">{e.name}</dd>
			{#if e.description}<dt class="text-muted-foreground">Descripción</dt><dd class="mb-2 whitespace-pre-line sm:mb-0">{e.description}</dd>{/if}
			<dt class="text-muted-foreground">Duración</dt><dd class="mb-2 sm:mb-0">{e.duration_minutes} min</dd>
			{#if e.slot_interval_minutes !== e.duration_minutes}
				<dt class="text-muted-foreground">Intervalo entre turnos</dt><dd class="mb-2 sm:mb-0">{e.slot_interval_minutes} min</dd>
			{/if}
			<dt class="text-muted-foreground">Ubicación</dt><dd class="mb-2 break-words sm:mb-0">{LOCATION_TYPES.find((l) => l.value === e.location_type)?.label ?? e.location_type}{#if e.location_value} · {e.location_value}{/if}</dd>
			<dt class="text-muted-foreground">Enrutamiento</dt><dd class="mb-2 capitalize sm:mb-0">{ROUTING_MODE_LABELS[e.routing_mode] ?? e.routing_mode.replace('_', ' ')}</dd>
			<dt class="text-muted-foreground">Estado</dt><dd class="mb-2 sm:mb-0">{e.is_active ? 'Activo' : 'Inactivo'} · {e.is_public ? 'Listado' : 'No listado (solo por enlace)'}</dd>
			<dt class="text-muted-foreground">Página de reserva</dt>
			<dd class="flex flex-wrap items-center gap-2">
				<a href="/book/{e.slug}" target="_blank" rel="noopener" class="break-all text-primary underline">/book/{e.slug}</a>
				<Button variant="outline" size="sm" onclick={copyBookLink}>Copiar enlace</Button>
			</dd>
		</dl>
	</div>
{/snippet}

{#snippet hostPickers(target: Target, idPrefix: string)}
	<div class="grid grid-cols-2 gap-4">
		<div class="space-y-1.5">
			<Label for="{idPrefix}-add-member">Agregar miembro</Label>
			<Select.Root type="single" value="" onValueChange={(v) => addMember(target, v)}>
				<Select.Trigger id="{idPrefix}-add-member" class="w-full">Selecciona un miembro…</Select.Trigger>
				<Select.Content>
					{#if availableMembers.length > 0}
						{#each availableMembers as m}
							<Select.Item value={m.id} label={m.name}>{m.name} · {m.email}</Select.Item>
						{/each}
					{:else}
						<div class="px-2 py-1.5 text-xs text-muted-foreground">No hay miembros disponibles</div>
					{/if}
				</Select.Content>
			</Select.Root>
		</div>
		<div class="space-y-1.5">
			<Label for="{idPrefix}-add-team">Agregar equipo</Label>
			<Select.Root type="single" value="" onValueChange={(v) => addTeam(target, v)}>
				<Select.Trigger id="{idPrefix}-add-team" class="w-full">Selecciona un equipo…</Select.Trigger>
				<Select.Content>
					{#if teams.length > 0}
						{#each teams as t}
							<Select.Item value={t.id} label={t.name}>{t.name} ({t.member_count})</Select.Item>
						{/each}
					{:else}
						<div class="px-2 py-1.5 text-xs text-muted-foreground">Sin equipos</div>
					{/if}
				</Select.Content>
			</Select.Root>
		</div>
	</div>
{/snippet}

<svelte:head><title>{et?.name ?? slug} — Tipo de atención — Calnode</title></svelte:head>
<!-- Fork: on the WhatsApp tab the shortcut saves the texts (they have their own save). -->
<svelte:window
	onkeydown={saveOnCmdS(
		() => (activeTab === 'whatsapp' ? waPanel?.saveFromShortcut() : saveET()),
		() => activeTab === 'whatsapp' || !etSaving
	)}
/>

<div class="mb-8">
	<a href="{base}/event-types" class="mb-2 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
		<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
		Tipos de atención
	</a>
	<div class="flex items-center gap-3">
		<h1 class="text-2xl font-semibold tracking-tight">{et?.name ?? slug}</h1>
		<Tooltip.Provider>
			<Tooltip.Root>
				<Tooltip.Trigger
					class={buttonVariants({ variant: 'ghost', size: 'icon' })}
					onclick={() => window.open(`/book/${slug}`, '_blank')}
				>
					<!-- External link icon (matches the event-types list) -->
					<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
				</Tooltip.Trigger>
				<Tooltip.Content>Vista previa de la página de reserva</Tooltip.Content>
			</Tooltip.Root>
		</Tooltip.Provider>
	</div>
</div>

{#if etLoading}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else if etError}
	<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{etError}</p>
{:else if et && et.team?.kind === 'mentoria_copy'}

<!-- Fork: a mentor's copy of the Mentoría template - read-only for everyone. -->
<div class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">
	{#if et.owned !== false}
		Es la copia de {mentorName ? mentorName : 'un mentor'}.
		{#if et.team.template_slug}
			Se edita en la plantilla:
			<a href="{base}/event-types/{et.team.template_slug}" class="font-medium underline">{et.team.template_name || et.team.template_slug}</a>.
		{:else}
			Se edita en la plantilla{et.team.template_name ? `: ${et.team.template_name}` : ''}.
		{/if}
		Los cambios de la plantilla llegan solos a todas las copias.
	{:else}
		<span class="font-medium">Predefinido por el propietario.</span>
		Este es tu enlace personal: compártelo con tus clientes. Tú defines tus horarios en
		<a href="{base}/availability" class="font-medium underline">Disponibilidad</a>.
	{/if}
</div>
{@render readOnlySummary(et)}

{:else if et && et.owned === false}

<!-- Read-only: the user hosts this event type but doesn't own it -->
<div class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
	{#if et.team}
		<!-- Fork: a predefined type this person attends (the shared Soporte type). -->
		<span class="font-medium">Predefinido por el propietario.</span>
		Se reparte por turnos entre el personal de soporte; tú defines tus horarios en
		<a href="{base}/availability" class="font-medium underline">Disponibilidad</a>.
	{:else if et.owner_email}
		<span class="font-medium">{et.owner_name || et.owner_email}</span> creó este tipo de atención.
		<a href="mailto:{et.owner_email}?subject={encodeURIComponent('Solicitud de cambio: ' + et.name)}" class="font-medium underline">Envíale un mensaje</a> para solicitar cambios.
	{:else if et.owner_name}
		<span class="font-medium">{et.owner_name}</span> creó este tipo de atención. Envíale un mensaje para solicitar cambios.
	{:else}
		Este tipo de atención está administrado por su propietario. Contáctalo para solicitar cambios.
	{/if}
</div>
{@render readOnlySummary(et)}

{:else}

<!-- Fork: what a predefined type does, above the tabs. -->
{#if teamKind === 'mentoria_template'}
	<div class="mb-4 rounded-lg border bg-muted/30 px-4 py-3 text-sm">
		<span class="font-medium">Plantilla de Mentoría.</span>
		Cada mentor tiene su propia copia con su enlace personal{#if et?.team?.copies !== undefined}{' '}({et.team.copies === 1 ? '1 copia' : `${et.team.copies} copias`}){/if},
		y las copias siguen los cambios que guardes aquí (datos generales, notificaciones, preguntas y textos de WhatsApp).
		Este enlace sigue siendo el tuyo.
	</div>
{:else if teamKind === 'soporte_shared'}
	<div class="mb-4 rounded-lg border bg-muted/30 px-4 py-3 text-sm">
		<span class="font-medium">Tipo de Soporte.</span>
		Un solo enlace que se reparte por turnos entre el personal de soporte (el área se asigna en
		<a href="{base}/members" class="underline">Miembros</a>).
	</div>
{/if}

<div class="mb-6 flex gap-1 overflow-x-auto border-b">
	{#each TABS as t}
		<button
			type="button"
			onclick={() => (activeTab = t.id)}
			class="-mb-px shrink-0 border-b-2 px-3 py-2 text-sm font-medium transition-colors {activeTab === t.id ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
		>
			{t.label}
		</button>
	{/each}
</div>

{#if activeTab === 'general'}
<!-- General Settings -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">General</h2>
	<div class="rounded-lg border bg-card p-6">
		<div class="grid grid-cols-2 gap-4">
			<div class="space-y-1.5">
				<Label for="et-name">Nombre</Label>
				<Input id="et-name" bind:value={form.name} />
			</div>
			<div class="space-y-1.5 col-span-2">
				<Label for="et-slug">Enlace de reserva</Label>
				<div class="flex items-center gap-1.5">
					<span class="text-sm text-muted-foreground whitespace-nowrap">/book/</span>
					<Input id="et-slug" bind:value={form.slug} />
				</div>
				<p class="text-xs text-muted-foreground">
					Editable hasta la primera reserva, después de la cual los enlaces ya están en
					circulación. Útil sobre todo justo después de duplicar, cuando la copia llega
					con <code>-copy</code> al final.
				</p>
			</div>
			<div class="space-y-1.5">
				<Label for="et-dur">Duración (minutos)</Label>
				<Input id="et-dur" type="number" min="5" step="5" bind:value={form.duration_minutes} />
				<p class="text-xs text-muted-foreground">Cuánto dura la reunión.</p>
			</div>
			<div class="space-y-1.5">
				<Label for="et-slot">Intervalo entre turnos (minutos)</Label>
				<Input id="et-slot" type="number" min="1" step="5" bind:value={form.slot_interval_minutes} />
				<p class="text-xs text-muted-foreground">
					Cada cuánto puede empezar una reserva. Normalmente es igual a la duración. Bájalo
					para ofrecer más horarios de inicio, o súbelo para mantener los turnos en punto.
				</p>
			</div>
			<div class="col-span-2 space-y-1.5">
				<Label for="et-desc">Descripción</Label>
				<Textarea id="et-desc" bind:value={form.description} placeholder="Opcional — admite **negrita** y *cursiva* en markdown" rows={3} class="resize-y" />
			</div>
			<div class="space-y-1.5">
				<p class="text-sm font-medium">Estado</p>
				<div class="flex items-center gap-2">
					<Checkbox id="is-active" bind:checked={form.is_active} />
					<Label for="is-active" class="cursor-pointer font-normal">Activo (aceptando reservas)</Label>
				</div>
			</div>
			<div class="space-y-1.5">
				<p class="text-sm font-medium">Visibilidad</p>
				<div class="flex items-center gap-2">
					<Checkbox id="is-public" bind:checked={form.is_public} />
					<Label for="is-public" class="cursor-pointer font-normal">Público (visible en la página de reserva)</Label>
				</div>
			</div>
		</div>

		<div class="mt-4 space-y-1.5">
			<div class="flex items-center gap-2">
				<Checkbox id="show-taken" bind:checked={form.show_taken_slots} />
				<Label for="show-taken" class="cursor-pointer font-normal">Mostrar horarios reservados como no disponibles</Label>
			</div>
			<p class="text-xs text-muted-foreground">
				Los horarios reservados aparecen tachados en la página de reserva en lugar de
				ocultarse, para que los visitantes vean cómo se va llenando el calendario. Solo se
				muestran los horarios dentro de tu horario laboral, nunca el resto de tu día.
				{#if form.show_taken_slots}
					<span class="text-amber-700 dark:text-amber-500">
						Cualquiera con el enlace de reserva puede ver qué horarios están ocupados.
					</span>
				{/if}
			</p>
		</div>

		{#if isOnlineMeeting(form.location_type)}
			<label class="flex items-center gap-3 text-sm"><input type="checkbox" bind:checked={allowPhoneCall} />Permitir que los invitados elijan una llamada telefónica ingresando su número</label>
		{/if}
		<!-- Location -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Ubicación</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-loc">Tipo</Label>
					<Select.Root type="single" bind:value={form.location_type} disabled={isManaged && et?.location_type === 'livekit'}>
						<Select.Trigger id="et-loc" class="w-full">
							{LOCATION_TYPES.find((lt) => lt.value === form.location_type)?.label ?? 'Selecciona…'}
						</Select.Trigger>
						<Select.Content>
							{#each LOCATION_TYPES as lt}
								<Select.Item value={lt.value} label={lt.label}>{lt.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
					{#if isManaged}
						<p class="text-xs text-muted-foreground">Los tipos predefinidos usan la sala de video integrada.</p>
					{/if}
				</div>
				<div class="space-y-1.5">
					{#if isOnlineMeeting(form.location_type)}
						{@const platform = form.location_type === 'teams' ? 'Microsoft Teams' : 'Google Meet'}
						<Label for="et-loc-val">Enlace de {platform}</Label>
						{#if meetAutoGen}
							<p class="rounded-md border border-green-600/20 bg-green-50 px-3 py-2 text-sm text-green-700">
								Se genera automáticamente un enlace de {platform} para cada reserva desde tu calendario conectado.{#if form.location_type === 'teams'} Las cuentas personales de Microsoft no pueden generar enlaces de Teams — agrega uno abajo como respaldo.{/if}
							</p>
						{:else}
							<p class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
								{calStatus?.connected
									? `Tu calendario conectado es ${calStatus.provider === 'google' ? 'Google' : 'Microsoft'}, así que no se generará automáticamente un enlace de ${platform}.`
									: `No hay ningún calendario conectado, así que no se puede generar automáticamente un enlace de ${platform}.`}
								Pega un enlace abajo y se usará para cada reserva.
							</p>
						{/if}
						<Input id="et-loc-val" bind:value={form.location_value} placeholder={meetAutoGen ? 'Enlace de respaldo opcional' : `Pega un enlace de ${platform}`} />
					{:else if form.location_type === 'zoom'}
						<Label for="et-loc-val">Enlace de Zoom</Label>
						{#if zoomAutoGen}
							<p class="rounded-md border border-green-600/20 bg-green-50 px-3 py-2 text-sm text-green-700">
								Se crea automáticamente una reunión de Zoom para cada reserva con la cuenta de Zoom conectada del anfitrión asignado.
							</p>
						{:else}
							<p class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
								{zoomStatus?.configured
									? 'Conecta tu cuenta de Zoom en la página de Calendario para generar enlaces de reunión automáticamente,'
									: 'Zoom no está configurado para este espacio de trabajo (un administrador puede agregarlo en Configuración → Zoom),'}
								o pega un enlace de Zoom abajo para usarlo en cada reserva.
							</p>
						{/if}
						<Input id="et-loc-val" bind:value={form.location_value} placeholder={zoomAutoGen ? 'Enlace de respaldo opcional' : 'https://…zoom.us/j/…'} />
					{:else if form.location_type === 'livekit'}
						<Label>Sala de video</Label>
						<p class="rounded-md border border-green-600/20 bg-green-50 px-3 py-2 text-sm text-green-700">
							Se crea automáticamente una sala de video segura para cada reserva — no se necesita ningún enlace. Los invitados se unen desde el navegador. (Configura el servidor en Configuración → Video.)
						</p>
					{:else}
						<Label for="et-loc-val">{LOCATION_NEEDS_VALUE[form.location_type] ?? 'Detalles'}</Label>
						<Input id="et-loc-val" bind:value={form.location_value} placeholder={LOCATION_PLACEHOLDER[form.location_type] ?? 'Opcional'} />
					{/if}
				</div>
			</div>
		</div>

		<!-- Price -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Precio</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-price">Monto</Label>
					<Input id="et-price" type="number" min="0" step="0.01" bind:value={priceMajor} placeholder="0.00" />
				</div>
				<div class="space-y-1.5">
					<Label for="et-currency">Moneda</Label>
					<Input id="et-currency" type="text" maxlength={3} bind:value={form.currency} placeholder="usd" />
				</div>
			</div>
			<p class="mt-2 text-xs text-muted-foreground">
				Déjalo en 0 para un evento gratuito. Un precio envía a quienes reservan a Stripe Checkout antes de
				confirmar el turno — requiere tener <a href="/admin/settings/payments" class="underline">Stripe</a> conectado.
			</p>
		</div>

		<!-- Scheduling -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Programación</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-buf-before">Margen antes (min)</Label>
					<Input id="et-buf-before" type="number" min="0" step="5" bind:value={form.buffer_before_minutes} />
					<p class="text-xs text-muted-foreground">Tiempo bloqueado antes de cada reunión</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-buf-after">Margen después (min)</Label>
					<Input id="et-buf-after" type="number" min="0" step="5" bind:value={form.buffer_after_minutes} />
					<p class="text-xs text-muted-foreground">Tiempo bloqueado después de cada reunión</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-notice">Aviso mínimo (min)</Label>
					<Input id="et-notice" type="number" min="0" step="30" bind:value={form.min_notice_minutes} />
					<p class="text-xs text-muted-foreground">ej. 60 = las reservas deben ser con 1h+ de anticipación</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-future">Ventana de reserva (días)</Label>
					<Input id="et-future" type="number" min="0" bind:value={form.max_future_days} />
					<p class="text-xs text-muted-foreground">Con cuánta anticipación puede reservar la gente. 0 = ilimitado</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-max-active">Máximo de reservas activas por persona</Label>
					<Input id="et-max-active" type="number" min="0" bind:value={form.max_active_bookings} />
					<p class="text-xs text-muted-foreground">Reservas próximas que puede tener un mismo asistente (por correo). 0 = ilimitado</p>
				</div>
			</div>
		</div>

	</div>
</div>
{/if}

{#if activeTab === 'hosts' && isManaged}
<!-- Fork: who hosts T / S follows the áreas; nothing to edit here. -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Anfitriones</h2>
	<div class="rounded-lg border bg-card p-4 sm:p-6">
		<p class="mb-4 text-sm text-muted-foreground">
			Asignados por área — se cambian en <a href="{base}/members" class="underline">Miembros</a>.
		</p>
		{#if teamKind === 'mentoria_template'}
			<p class="mb-3 text-sm">
				Esta plantilla la atiendes tú. Cada mentor atiende su propia copia, con su enlace personal.
			</p>
		{:else}
			<p class="mb-3 text-sm">
				{et?.routing_mode === 'round_robin'
					? 'Se reparte por turnos entre el personal de soporte:'
					: 'Nadie tiene el área Soporte todavía, así que lo atiende su propietario:'}
			</p>
		{/if}
		{#if !hostsLoaded}
			<p class="text-sm text-muted-foreground">Cargando…</p>
		{:else}
			{@const shown = rotationHosts.length > 0 ? rotationHosts : togetherHosts}
			{#if shown.length > 0}
				<ul class="space-y-2">
					{#each shown as h (h.user_id)}
						<li class="min-w-0 rounded-md border px-3 py-2">
							<div class="truncate text-sm font-medium">{h.name}</div>
							<div class="truncate text-xs text-muted-foreground">{h.email}</div>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="text-sm text-muted-foreground">Sin anfitriones.</p>
			{/if}
		{/if}
	</div>
</div>
{:else if activeTab === 'hosts'}
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Anfitriones</h2>
	<div class="rounded-lg border bg-card p-6">
		<div>
			<p class="-mt-1 mb-4 text-sm text-muted-foreground">Quién puede ser anfitrión de este evento y cómo se asignan las reuniones.</p>

			<!-- Q1 — who can host -->
			<div class="space-y-1.5">
				<Label>¿Quién puede ser anfitrión de este evento?</Label>
				<div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
					<button type="button" onclick={() => setScope('me')}
						class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {hostScope === 'me' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
						Solo yo
					</button>
					<button type="button" onclick={() => setScope('people')}
						class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {hostScope === 'people' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
						Personas específicas
					</button>
				</div>
				<p class="text-xs text-muted-foreground">
					{#if hostScope === 'me'}
						Todas las reservas te llegan a ti.
					{:else}
						Elige quién puede tomar estas reservas — agrega miembros individualmente o incorpora un equipo completo.
					{/if}
				</p>
			</div>

			{#if hostScope === 'people'}
				<!-- Q2 — how the meeting is staffed -->
				<div class="mt-4 space-y-1.5">
					<Label>¿Cómo se debe asignar la reunión?</Label>
					<div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
						<button type="button" onclick={() => (staffing = 'rotate')}
							class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing === 'rotate' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
							Rotar entre ellos
						</button>
						<button type="button" onclick={() => (staffing = 'together')}
							class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing === 'together' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
							Todos asisten
						</button>
					</div>
					<p class="text-xs text-muted-foreground">
						{#if staffing === 'rotate'}
							Se reserva a una persona disponible por turno, repartiendo las reservas entre el grupo.
						{:else}
							Todos se unen a la misma reunión. Un turno solo se ofrece cuando todas las personas requeridas están libres.
						{/if}
					</p>
				</div>

				{#if staffing === 'rotate'}
					<div class="mt-4 space-y-3">
						<div class="space-y-1.5">
							<Label for="rr-strategy">A quién se elige</Label>
							<Select.Root type="single" value={rrStrategy} onValueChange={(v) => { if (v) rrStrategy = v as Strategy; }}>
								<Select.Trigger id="rr-strategy" class="w-full">
									{RR_STRATEGIES.find((s) => s.value === rrStrategy)?.label ?? 'Selecciona…'}
								</Select.Trigger>
								<Select.Content>
									{#each RR_STRATEGIES as s}
										<Select.Item value={s.value} label={s.label}>{s.label}</Select.Item>
									{/each}
								</Select.Content>
							</Select.Root>
							<p class="text-xs text-muted-foreground">
								{#if rrStrategy === 'priority'}
									Se reserva a la persona más arriba en la lista que esté libre; se baja en la lista cuando están ocupados.
								{:else if rrStrategy === 'soonest'}
									Ofrece el turno más próximo que tenga libre cualquiera del grupo.
								{:else}
									Reparte las reservas de forma equitativa — se reserva a quien tenga menos reuniones próximas.
								{/if}
							</p>
						</div>

						<p class="text-sm font-medium">Personas en la rotación</p>
						{#if rotationHosts.length > 0}
							<div class="space-y-2">
								{#each rotationHosts as h, i (h.user_id)}
									<div class="flex items-center justify-between gap-2 rounded-md border px-3 py-2">
										<div class="flex min-w-0 items-center gap-2">
											{#if rrStrategy === 'priority'}
												<span class="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground tabular-nums">{i + 1}</span>
											{/if}
											<div class="min-w-0">
												<div class="truncate text-sm font-medium">{h.name}</div>
												<div class="truncate text-xs text-muted-foreground">{h.email}</div>
											</div>
										</div>
										<div class="flex shrink-0 items-center gap-1">
											{#if rrStrategy === 'priority'}
												<Button type="button" variant="ghost" size="icon" disabled={i === 0} aria-label="Subir" onclick={() => moveRotation(i, -1)}>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="18 15 12 9 6 15"/></svg>
												</Button>
												<Button type="button" variant="ghost" size="icon" disabled={i === rotationHosts.length - 1} aria-label="Bajar" onclick={() => moveRotation(i, 1)}>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>
												</Button>
											{/if}
											<Button type="button" variant="ghost" size="sm" onclick={() => removePerson('rotation', h.user_id)}>Quitar</Button>
										</div>
									</div>
								{/each}
							</div>
						{:else}
							<p class="text-xs text-muted-foreground">Agrega personas o un equipo a la rotación.</p>
						{/if}
						{@render hostPickers('rotation', 'rr')}
					</div>
				{:else}
					<div class="mt-4 space-y-3">
						<p class="text-sm font-medium">Quién asiste</p>
						{#if togetherHosts.length > 0}
							<div class="space-y-2">
								{#each togetherHosts as h (h.user_id)}
									<div class="flex items-center justify-between gap-2 rounded-md border px-3 py-2">
										<div class="min-w-0">
											<div class="truncate text-sm font-medium">{h.name}</div>
											<div class="truncate text-xs text-muted-foreground">{h.email}</div>
										</div>
										<div class="flex shrink-0 items-center gap-2">
											<div class="inline-flex rounded-md border p-0.5">
												<button type="button" onclick={() => setOptional(h.user_id, false)}
													class="rounded px-2 py-0.5 text-xs font-medium transition-colors {!h.optional ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}">
													Requerido
												</button>
												<button type="button" onclick={() => setOptional(h.user_id, true)}
													class="rounded px-2 py-0.5 text-xs font-medium transition-colors {h.optional ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}">
													Opcional
												</button>
											</div>
											<Button type="button" variant="ghost" size="sm" onclick={() => removePerson('together', h.user_id)}>Quitar</Button>
										</div>
									</div>
								{/each}
							</div>
						{:else}
							<p class="text-xs text-muted-foreground">Agrega a las personas que asisten a esta reunión.</p>
						{/if}
						<p class="text-xs text-muted-foreground">
							Los anfitriones <span class="font-medium text-foreground">requeridos</span> siempre asisten y deben estar libres para que se abra un turno.
							Los anfitriones <span class="font-medium text-foreground">opcionales</span> se unen solo cuando están libres — nunca bloquean un turno.
						</p>
						{@render hostPickers('together', 'grp')}
					</div>
				{/if}
			{/if}
		</div>
	</div>
</div>
{/if}

{#if activeTab === 'notifications'}
<!-- Notifications -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Notificaciones</h2>
	<div class="rounded-lg border bg-card p-6 space-y-6">

		<!-- Reminders -->
		<div>
			<p class="mb-1 text-sm font-medium">Recordatorios</p>
			<p class="mb-3 text-xs text-muted-foreground">Envía a los asistentes un correo de recordatorio antes de la reunión. Si no configuras ninguno, se usa 24 horas antes por defecto.</p>
			<div class="space-y-2">
				{#each reminders as hb, i}
					<div class="flex items-center gap-2">
						<Select.Root
							type="single"
							value={String(hb)}
							onValueChange={(v) => {
								if (!v) return;
								const n = Number(v);
								reminders = reminders.map((r, idx) => idx === i ? n : r);
							}}
						>
							<Select.Trigger class="w-full">
								{REMINDER_OPTIONS.find((o) => o.value === hb)?.label ?? 'Selecciona…'}
							</Select.Trigger>
							<Select.Content>
								{#each REMINDER_OPTIONS as opt}
									<Select.Item value={String(opt.value)} label={opt.label}>{opt.label}</Select.Item>
								{/each}
							</Select.Content>
						</Select.Root>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onclick={() => { reminders = reminders.filter((_, idx) => idx !== i); }}
						>
							<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
						</Button>
					</div>
				{/each}
				{#if reminders.length < 5}
					<Button
						type="button"
						variant="outline"
						size="sm"
						onclick={() => {
							const used = new Set(reminders);
							const next = REMINDER_OPTIONS.find(o => !used.has(o.value));
							if (next) reminders = [...reminders, next.value];
						}}
					>
						+ Agregar recordatorio
					</Button>
				{/if}
			</div>
		</div>

		<!-- Custom messages -->
		<div class="border-t pt-5">
			<p class="mb-1 text-sm font-medium">Mensajes personalizados</p>
			<p class="mb-3 text-xs text-muted-foreground">Agrega una nota opcional que se añade a cada tipo de correo.</p>
			<div class="space-y-2">
				{#each [
					{ key: 'confirmation' as const, label: 'Confirmación de reserva' },
					{ key: 'cancellation' as const, label: 'Aviso de cancelación' },
					{ key: 'reschedule' as const, label: 'Aviso de reprogramación' },
					{ key: 'reminder' as const, label: 'Correo de recordatorio' },
				] as item}
					<div class="rounded-md border">
						<button
							type="button"
							class="flex w-full items-center justify-between px-4 py-3 text-sm font-medium hover:bg-muted/30 transition-colors"
							onclick={() => { msgOpen[item.key] = !msgOpen[item.key]; }}
						>
							<span>{item.label}</span>
							<svg
								xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24"
								fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
								class="transition-transform {msgOpen[item.key] ? 'rotate-180' : ''}"
							><polyline points="6 9 12 15 18 9"/></svg>
						</button>
						{#if msgOpen[item.key]}
							{@const note = item.key === 'confirmation' ? msg_confirmation
								: item.key === 'cancellation' ? msg_cancellation
								: item.key === 'reschedule'   ? msg_reschedule
								: msg_reminder}
							<div class="border-t px-4 pb-4 pt-3 space-y-3">
								<div class="space-y-1.5">
									<Label class="text-xs text-muted-foreground">Asunto <span class="font-normal">(opcional — en blanco usa el predeterminado)</span></Label>
									{#if item.key === 'confirmation'}
										<Input bind:value={subj_confirmation} placeholder={`Reserva confirmada: ${form.name}`} />
									{:else if item.key === 'cancellation'}
										<Input bind:value={subj_cancellation} placeholder={`Reserva cancelada: ${form.name}`} />
									{:else if item.key === 'reschedule'}
										<Input bind:value={subj_reschedule} placeholder={`Reserva reprogramada: ${form.name}`} />
									{:else if item.key === 'reminder'}
										<Input bind:value={subj_reminder} placeholder={`Recordatorio: ${form.name} se acerca`} />
									{/if}
								</div>
								{#if item.key === 'confirmation'}
									<Textarea bind:value={msg_confirmation} rows={3} placeholder="Agrega una nota personalizada para los asistentes…" />
								{:else if item.key === 'cancellation'}
									<Textarea bind:value={msg_cancellation} rows={3} placeholder="Agrega una nota personalizada para los asistentes…" />
								{:else if item.key === 'reschedule'}
									<Textarea bind:value={msg_reschedule} rows={3} placeholder="Agrega una nota personalizada para los asistentes…" />
								{:else if item.key === 'reminder'}
									<Textarea bind:value={msg_reminder} rows={3} placeholder="Agrega una nota personalizada para los asistentes…" />
								{/if}

								<!-- Preview toggle -->
								<button
									type="button"
									class="text-xs text-muted-foreground hover:text-foreground underline-offset-2 hover:underline"
									onclick={() => { previewOpen[item.key] = !previewOpen[item.key]; }}
								>{previewOpen[item.key] ? 'Ocultar vista previa' : 'Mostrar vista previa del correo'}</button>

								{#if previewOpen[item.key]}
									<pre class="rounded-md border bg-muted/30 px-4 py-3 text-xs leading-relaxed whitespace-pre-wrap font-mono text-muted-foreground overflow-auto max-h-64">{buildPreview(item.key, note)}</pre>
								{/if}

								<!-- Send test button -->
								<div class="flex items-center gap-3">
									<Button
										type="button"
										variant="outline"
										size="sm"
										disabled={testSending[item.key]}
										onclick={() => sendTestEmail(item.key)}
									>
										{testSending[item.key] ? 'Enviando…' : 'Enviar correo de prueba'}
									</Button>
									{#if testSent[item.key]}
										<span class="text-xs text-green-600">Correo de prueba enviado a tu bandeja de entrada.</span>
									{/if}
									{#if testError[item.key]}
										<span class="text-xs text-destructive">
											{#if testError[item.key] === 'Email is not configured on this server — add SMTP settings to enable sending'}
												SMTP no está configurado — <a href="{base}/settings/email" class="underline">configúralo en Configuración</a>.
											{:else}
												{testError[item.key]}
											{/if}
										</span>
									{/if}
								</div>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		</div>

		<!-- Assistant greeting -->
		<div class="border-t pt-5">
			<p class="mb-1 text-sm font-medium">Saludo del asistente</p>
			<p class="mb-3 text-xs text-muted-foreground">
				La primera línea del asistente de chat en la página de reserva de este evento. Déjalo
				en blanco para usar el saludo predeterminado, que se traduce automáticamente según el
				idioma del visitante; un saludo personalizado se muestra tal cual está escrito, en
				todos los idiomas.
			</p>
			<Textarea bind:value={msg_greeting} rows={2} placeholder="¡Hola! Cuéntame más o menos cuándo te gustaría reunirte…" />
		</div>

	</div>
</div>
{/if}

{#if activeTab === 'hosts' && $currentUser?.is_admin && !et?.team}
	<div class="mt-6 space-y-3 border-t pt-5">
		<Label for="transfer-owner">Transferir propiedad</Label>
		<p class="text-sm text-muted-foreground">Elige un anfitrión requerido guardado. La URL de reserva no cambia. La transferencia solo está disponible cuando no hay reservas próximas. Las conexiones de calendario y la disponibilidad global se quedan con cada cuenta.</p>
		<select id="transfer-owner" bind:value={transferOwner} class="h-9 w-full rounded-md border bg-background px-3 text-sm">
			<option value="">Selecciona el nuevo propietario</option>
			{#each transferHosts as host}<option value={host.user_id}>{host.name || host.email}</option>{/each}
		</select>
		<Button variant="outline" disabled={!transferOwner || transferring} onclick={transferEvent}>{transferring ? 'Transfiriendo…' : 'Transferir propiedad'}</Button>
	</div>
{/if}

{#if activeTab === 'questions'}
	<QuestionsPanel slug={slug ?? ''} />
{/if}

{#if activeTab === 'embed'}
	<EmbedPanel slug={et?.slug ?? ''} />
{/if}

<!-- Fork: kept mounted (only hidden) so unsaved texts survive a trip to another tab. -->
<div class:hidden={activeTab !== 'whatsapp'}>
	<WhatsAppMessagesPanel bind:this={waPanel} slug={et?.slug ?? slug ?? ''} />
</div>

{#if activeTab === 'general' || activeTab === 'hosts' || activeTab === 'notifications'}
	<div class="sticky bottom-0 mt-4 flex justify-end border-t bg-background/90 py-3 backdrop-blur">
		<Button onclick={saveET} disabled={etSaving}>
			{etSaving ? 'Guardando…' : 'Guardar cambios'}
		</Button>
	</div>
{/if}

{/if}
