<script lang="ts">
	import { onMount } from 'svelte';
	import {
		api,
		teamApi,
		copyText,
		reassignErrorText,
		AREA_LABELS,
		INVITE_ROLE_LABELS,
		type Area,
		type EventType,
		type Invite,
		type InviteRole,
		type ReassignCandidate,
		type TeamMember,
		type TeamSettings,
		type UpcomingBooking
	} from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Badge } from '$lib/components/ui/badge';
	import * as Select from '$lib/components/ui/select';
	import { toast } from 'svelte-sonner';

	let members: TeamMember[] = $state([]);
	let invites: Invite[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showArchived = $state(false);

	// Invite form. The role is stored with the invite and applied when it is claimed
	// (Administrador only for the owner; the server enforces it too).
	let showInvite = $state(false);
	let inviteEmail = $state('');
	let inviteRole = $state<InviteRole>('mentoria');
	let inviting = $state(false);
	let inviteError = $state('');
	let inviteResult = $state<{ invite_url: string; email: string; email_sent: boolean; note: string } | null>(null);
	let copied = $state(false);

	// Confirm dialog
	let confirmOpen = $state(false);
	let confirmTitle = $state('');
	let confirmDescription = $state('');
	let confirmActionText = $state('Confirmar');
	let confirmDestructive = $state(true);
	let pendingAction: (() => void) | null = null;

	function openConfirm(opts: { title: string; description: string; confirmText: string; action: () => void; destructive?: boolean }) {
		confirmTitle = opts.title;
		confirmDescription = opts.description;
		confirmActionText = opts.confirmText;
		confirmDestructive = opts.destructive ?? true;
		pendingAction = opts.action;
		confirmOpen = true;
	}

	// Password reset — keyed by user id
	let resetTarget = $state<string | null>(null);
	let resetPassword = $state('');
	let resetting = $state(false);
	let resetError = $state('');
	let resetOk = $state(false);

	// Resolve-meetings (archive) dialog. Each booking offers only the people of its área
	// (GET /v1/bookings/{id}/reassign-candidates): a Mentoría session can go to another
	// mentor, a Soporte one to the support staff.
	let resolveOpen = $state(false);
	let resolveMember = $state<TeamMember | null>(null);
	let resolveBookings = $state<UpcomingBooking[]>([]);
	let resolveChoice = $state<Record<string, string>>({});
	let resolveCandidates = $state<Record<string, ReassignCandidate[] | 'loading' | 'error'>>({});
	// Sent as typed (or ''): it reaches the client in the e-mail and the WhatsApp {motivo}.
	let resolveReason = $state('');
	let resolveBusy = $state(false);
	let resolveError = $state('');

	// Fork: predefined types (owner edits, admins read).
	let settings = $state<TeamSettings | null>(null);
	let settingsError = $state('');
	let ownerTypes = $state<EventType[]>([]);
	let tplChoice = $state('');
	let supChoice = $state('');
	let savingSettings = $state(false);
	let settingsWarnings = $state<string[]>([]);

	const me = $derived($currentUser);
	const isOwnerViewer = $derived(!!$currentUser?.is_owner);

	async function load() {
		try {
			members = await api.get<TeamMember[]>(showArchived ? '/v1/users?include_archived=true' : '/v1/users');
		} catch (e: any) {
			error = e.message;
		}
		// Its own failure costs only the invites section.
		try {
			invites = await api.get<Invite[]>('/v1/invites');
		} catch {
			invites = [];
		}
		loading = false;
	}

	async function loadSettings() {
		try {
			applySettings(await teamApi.getSettings());
			settingsError = '';
		} catch (e: any) {
			settingsError = e?.message || 'Error de conexión';
		}
	}

	function applySettings(s: TeamSettings) {
		settings = s;
		tplChoice = s.mentoria_template?.id ?? '';
		supChoice = s.soporte_shared?.id ?? '';
	}

	// The owner picks among their own types: never a copy, never archived, and only ones in
	// the built-in video room (the server refuses the rest with a Spanish 400).
	async function loadOwnerTypes() {
		if (!$currentUser?.is_owner) return;
		try {
			const res = await api.get<{ items: EventType[] }>('/v1/event-types');
			ownerTypes = (res.items ?? []).filter(
				(et) => et.owned !== false && et.team?.kind !== 'mentoria_copy' && !et.archived && et.location_type === 'livekit'
			);
		} catch {
			ownerTypes = [];
		}
	}

	onMount(() => {
		load();
		loadSettings();
		loadOwnerTypes();
	});

	async function toggleArchived() {
		showArchived = !showArchived;
		await load();
	}

	async function sendInvite() {
		inviteError = '';
		inviteResult = null;
		if (!inviteEmail.trim()) { inviteError = 'El correo electrónico es obligatorio.'; return; }
		inviting = true;
		try {
			const res = await teamApi.createInvite(inviteEmail.trim().toLowerCase(), inviteRole);
			inviteResult = res;
			inviteEmail = '';
			inviteRole = 'mentoria';
			await load();
		} catch (e: any) {
			inviteError = e.message;
		} finally {
			inviting = false;
		}
	}

	function revokeInvite(id: string) {
		openConfirm({
			title: '¿Revocar invitación?',
			description: 'El enlace dejará de funcionar de inmediato.',
			confirmText: 'Revocar',
			action: async () => {
				try { await api.del(`/v1/invites/${id}`); await load(); }
				catch (e: any) { error = e.message; }
			}
		});
	}

	// Re-issue a pending invite: mints a fresh link + new 7-day expiry and re-emails
	// it (the old link stops working). The original token can't be recovered.
	async function resendInvite(id: string) {
		try {
			const res = await api.post<{ email: string; invite_url: string; email_sent: boolean }>(
				`/v1/invites/${id}/resend`, {}
			);
			await load();
			if (res.email_sent) {
				toast.success(`Invitación reenviada a ${res.email}`);
			} else {
				// No SMTP configured — surface the fresh link so the admin can send it manually.
				showInvite = true;
				inviteResult = {
					invite_url: res.invite_url, email: res.email, email_sent: false,
					note: 'El correo no está configurado — copia este enlace y envíalo manualmente.'
				};
				toast.success(`Nuevo enlace generado para ${res.email}`);
			}
		} catch (e: any) {
			toast.error(e.message || 'No se pudo reenviar la invitación');
		}
	}

	// --- Role and área (PUT /v1/users/{id}/team-role, one call) ---
	// UI role: Mentor / Soporte / Sin área are members with that área; Administrador keeps
	// its área as "También atiende". Matrix (the server enforces the same):
	//   owner  → any non-owner (tier + área), and their own área ("También atiende");
	//   admin  → only the área of non-admin members (never another admin, never themselves);
	//   nobody else changes anything.
	type UiRole = 'mentoria' | 'soporte' | 'none' | 'admin';
	const UI_ROLE_LABELS: Record<UiRole, string> = {
		mentoria: 'Mentor',
		soporte: 'Soporte',
		none: 'Sin área (no atiende)',
		admin: 'Administrador'
	};
	function uiRole(m: TeamMember): UiRole {
		if (m.is_admin) return 'admin';
		return m.area === 'mentoria' || m.area === 'soporte' ? m.area : 'none';
	}
	function areaOf(m: TeamMember): Area {
		return m.area === 'mentoria' || m.area === 'soporte' ? m.area : '';
	}

	function canEditRole(m: TeamMember): boolean {
		if (!me || m.archived || m.is_owner || m.id === me.id) return false;
		if (me.is_owner) return true;
		return me.is_admin && !m.is_admin;
	}
	// Only the owner sets "También atiende": on admins, and on their own row.
	function canEditAlso(m: TeamMember): boolean {
		if (!me?.is_owner || m.archived) return false;
		return (m.is_admin && !m.is_owner) || (m.is_owner && m.id === me.id);
	}
	// The template's owner has no copy (their link IS the template): only Soporte or nothing.
	function alsoOptions(m: TeamMember): Area[] {
		return m.is_owner ? ['', 'soporte'] : ['', 'mentoria', 'soporte'];
	}
	const alsoLabel = (a: Area) => (a ? AREA_LABELS[a] : 'No atiende');

	function roleOptionsFor(): UiRole[] {
		return me?.is_owner ? ['mentoria', 'soporte', 'none', 'admin'] : ['mentoria', 'soporte', 'none'];
	}

	function changeRole(m: TeamMember, v: string | undefined) {
		if (!v || v === uiRole(m)) return;
		const r = v as UiRole;
		if (r === 'admin') applyTeamRole(m, { tier: 'admin', area: areaOf(m) });
		else applyTeamRole(m, { tier: 'member', area: r === 'none' ? '' : r });
	}
	function changeAlso(m: TeamMember, v: string | undefined) {
		const a = (v === 'mentoria' || v === 'soporte' ? v : '') as Area;
		if (a === areaOf(m)) return;
		// The owner's tier stays owner; 'admin' is what the owner already is (is_admin = 1).
		applyTeamRole(m, { tier: 'admin', area: a });
	}

	// Slugs whose bookings belong to an área: Mentoría = the person's copy (their personal
	// link) and the template; Soporte = the shared type. Empty = cannot tell.
	function areaSlugs(m: TeamMember, a: Area): Set<string> {
		const out = new Set<string>();
		if (a === 'mentoria') {
			if (m.personal_link?.slug) out.add(m.personal_link.slug);
			if (settings?.mentoria_template?.slug) out.add(settings.mentoria_template.slug);
		} else if (a === 'soporte' && settings?.soporte_shared?.slug) {
			out.add(settings.soporte_shared.slug);
		}
		return out;
	}

	// Before an área change that leaves upcoming sessions behind, say so (they stay with the
	// person until someone passes them on or cancels them).
	async function applyTeamRole(m: TeamMember, body: { tier: 'admin' | 'member'; area: Area }) {
		const prev = areaOf(m);
		if (prev && body.area !== prev) {
			try {
				const res = await teamApi.upcomingBookings(m.id);
				const slugs = areaSlugs(m, prev);
				const left = (res.items ?? []).filter((b) => slugs.size === 0 || slugs.has(b.event_type_slug));
				if (left.length > 0) {
					const n = left.length;
					const what = slugs.size === 0 ? (n === 1 ? 'sesión próxima' : 'sesiones próximas') : `${n === 1 ? 'sesión próxima' : 'sesiones próximas'} de ${AREA_LABELS[prev]}`;
					openConfirm({
						title: `${m.name} tiene ${n} ${what}`,
						description: `Seguirán a su nombre hasta que las pases a otra persona o las canceles desde Reservas. ${prev === 'mentoria' ? 'Su enlace personal dejará de aceptar reservas nuevas.' : 'Dejará de recibir reservas nuevas de Soporte.'}`,
						confirmText: 'Cambiar de todas formas',
						destructive: false,
						action: () => doTeamRole(m, body)
					});
					return;
				}
			} catch { /* the pre-check is a courtesy: the change itself still reports the count */ }
		}
		await doTeamRole(m, body);
	}

	async function doTeamRole(m: TeamMember, body: { tier: 'admin' | 'member'; area: Area }) {
		try {
			const res = await teamApi.putTeamRole(m.id, body);
			const label = m.is_owner
				? (body.area ? `también atiende ${AREA_LABELS[body.area]}` : 'no atiende ningún área')
				: body.tier === 'admin'
					? `ahora es administrador${body.area ? ` y atiende ${AREA_LABELS[body.area]}` : ''}`
					: body.area === ''
						? (m.is_admin ? 'deja de ser administrador y no atiende Mentoría ni Soporte' : 'ya no atiende Mentoría ni Soporte')
						: `ahora es ${UI_ROLE_LABELS[body.area].toLowerCase()}`;
			toast.success(`${m.name} ${label}`);
			if (res?.upcoming_in_previous_area && res.upcoming_in_previous_area > 0) {
				const n = res.upcoming_in_previous_area;
				toast.warning(`${m.name} tiene ${n} ${n === 1 ? 'sesión próxima' : 'sesiones próximas'} del área anterior: pásalas a otra persona desde Reservas.`);
			}
			await Promise.all([load(), loadSettings()]);
		} catch (e: any) {
			toast.error(e.message || 'No se pudo cambiar el rol');
			await load();
		}
	}

	function confirmTransfer(m: TeamMember) {
		openConfirm({
			title: `¿Transferir la propiedad a ${m.name}?`,
			description: 'Pasarás a ser administrador y esta persona será la propietaria del espacio de trabajo. Solo el propietario puede hacer esto.',
			confirmText: 'Transferir propiedad',
			action: async () => {
				try { await api.post(`/v1/users/${m.id}/transfer-ownership`); toast.success(`${m.name} ahora es el propietario`); await load(); }
				catch (e: any) { toast.error(e.message || 'No se pudo transferir la propiedad'); }
			}
		});
	}

	// --- Archive / restore ---
	async function startArchive(m: TeamMember) {
		error = '';
		try {
			const res = await teamApi.upcomingBookings(m.id);
			if (res.items.length > 0) {
				resolveMember = m;
				resolveBookings = res.items;
				resolveChoice = {};
				resolveCandidates = {};
				resolveReason = '';
				resolveError = '';
				resolveOpen = true;
				loadCandidates(res.items);
			} else {
				openConfirm({
					title: `¿Archivar a ${m.name}?`,
					description: 'Perderá el acceso de inmediato y sus tipos de atención se desactivarán. Su registro e historial se conservan — podrás restaurarlo más adelante.',
					confirmText: 'Archivar',
					action: () => doArchive(m.id)
				});
			}
		} catch (e: any) { error = e.message; }
	}

	// One request at a time: the list is short and the server has a single DB connection.
	async function loadCandidates(list: UpcomingBooking[]) {
		for (const b of list) {
			resolveCandidates = { ...resolveCandidates, [b.id]: 'loading' };
			try {
				const cands = (await teamApi.reassignCandidates(b.id)).filter((c) => c.id !== resolveMember?.id);
				resolveCandidates = { ...resolveCandidates, [b.id]: cands };
			} catch {
				resolveCandidates = { ...resolveCandidates, [b.id]: 'error' };
			}
		}
	}
	function candidatesOf(id: string): ReassignCandidate[] {
		const c = resolveCandidates[id];
		return Array.isArray(c) ? c : [];
	}

	async function doArchive(id: string) {
		try { await api.post(`/v1/users/${id}/archive`); toast.success('Miembro archivado'); await Promise.all([load(), loadSettings()]); }
		catch (e: any) { toast.error(e.message || 'No se pudo archivar al miembro'); }
	}

	async function restoreMember(m: TeamMember) {
		try { await api.post(`/v1/users/${m.id}/restore`); toast.success(`${m.name} restaurado`); await Promise.all([load(), loadSettings()]); }
		catch (e: any) { toast.error(e.message || 'No se pudo restaurar al miembro'); }
	}

	// --- Resolve-meetings dialog actions ---
	async function reassignOne(bookingId: string) {
		const hostId = resolveChoice[bookingId];
		if (!hostId) return;
		resolveBusy = true; resolveError = '';
		try {
			await teamApi.reassign(bookingId, hostId);
			resolveBookings = resolveBookings.filter((b) => b.id !== bookingId);
			await finishResolveIfDone();
		} catch (e) { resolveError = reassignErrorText(e); }
		finally { resolveBusy = false; }
	}

	async function cancelOne(bookingId: string) {
		resolveBusy = true; resolveError = '';
		try {
			await api.post(`/v1/bookings/${bookingId}/cancel`, { reason: resolveReason.trim() });
			resolveBookings = resolveBookings.filter((b) => b.id !== bookingId);
			await finishResolveIfDone();
		} catch (e: any) { resolveError = e.message; }
		finally { resolveBusy = false; }
	}

	async function cancelAllRemaining() {
		resolveBusy = true; resolveError = '';
		try {
			for (const b of [...resolveBookings]) {
				await api.post(`/v1/bookings/${b.id}/cancel`, { reason: resolveReason.trim() });
				resolveBookings = resolveBookings.filter((x) => x.id !== b.id);
			}
			await finishResolveIfDone();
		} catch (e: any) { resolveError = e.message; }
		finally { resolveBusy = false; }
	}

	async function finishResolveIfDone() {
		if (resolveBookings.length === 0 && resolveMember) {
			const id = resolveMember.id;
			resolveOpen = false;
			resolveMember = null;
			await doArchive(id);
		}
	}

	// --- Password reset ---
	function startReset(id: string) { resetTarget = id; resetPassword = ''; resetError = ''; resetOk = false; }
	function cancelReset() { resetTarget = null; resetPassword = ''; resetError = ''; resetOk = false; }

	async function submitReset(userId: string) {
		resetError = ''; resetOk = false;
		if (!resetPassword) { resetError = 'La contraseña es obligatoria.'; return; }
		resetting = true;
		try {
			await api.post(`/v1/users/${userId}/password`, { password: resetPassword });
			resetOk = true; resetPassword = '';
			setTimeout(() => { resetTarget = null; resetOk = false; }, 2000);
		} catch (e: any) { resetError = e.message; }
		finally { resetting = false; }
	}

	async function copyInviteUrl(url: string) {
		if (!(await copyText(url))) { toast.error('No se pudo copiar; selecciona el enlace y cópialo.'); return; }
		copied = true;
		setTimeout(() => { copied = false; }, 2000);
	}

	async function copyLink(url: string) {
		if (await copyText(url)) toast.success('Enlace copiado');
		else toast.error('No se pudo copiar; mantén pulsado el enlace para copiarlo.');
	}

	// --- Tipos predefinidos (PUT /v1/team/settings, owner) ---
	const typeName = (id: string) => ownerTypes.find((t) => t.id === id)?.name
		?? (settings?.mentoria_template?.id === id ? settings.mentoria_template.name : undefined)
		?? (settings?.soporte_shared?.id === id ? settings.soporte_shared.name : undefined)
		?? '';
	const settingsDirty = $derived(
		!!settings && (tplChoice !== (settings.mentoria_template?.id ?? '') || supChoice !== (settings.soporte_shared?.id ?? ''))
	);

	function confirmSettings() {
		if (!settings) return;
		if (tplChoice && tplChoice === supChoice) {
			toast.error('Elige tipos distintos para Mentoría y Soporte.');
			return;
		}
		const lines: string[] = [];
		const oldT = settings.mentoria_template;
		if (tplChoice !== (oldT?.id ?? '')) {
			if (oldT) lines.push(`Las copias de «${oldT.name}» de cada mentor se desactivarán (sus reservas y enlaces se conservan; si vuelves a elegirla, se reactivan las mismas).`);
			if (tplChoice) lines.push(`Cada mentor recibirá su copia de «${typeName(tplChoice)}», con su propio enlace y los mismos datos, preguntas y textos de WhatsApp.`);
		}
		const oldS = settings.soporte_shared;
		if (supChoice !== (oldS?.id ?? '')) {
			if (oldS) lines.push(`«${oldS.name}» vuelve a atenderlo solo su propietario.`);
			if (supChoice) lines.push(`«${typeName(supChoice)}» se repartirá por turnos entre el personal de soporte.`);
		}
		openConfirm({
			title: '¿Guardar los tipos predefinidos?',
			description: lines.join(' '),
			confirmText: 'Guardar',
			destructive: false,
			action: saveSettings
		});
	}

	// The PUT's warnings (fork_team_api.go teamWarnings), each as a Spanish sentence.
	function warningLines(w: TeamSettings['warnings']): string[] {
		if (!w) return [];
		const out: string[] = [];
		const n = (v: number, one: string, many: string) => (v === 1 ? one : many.replace('{n}', String(v)));
		if (w.copies_created > 0)
			out.push(`Se ${n(w.copies_created, 'creó 1 copia', 'crearon {n} copias')} (una por mentor).`);
		if (w.copies_deactivated > 0)
			out.push(`Se ${n(w.copies_deactivated, 'desactivó 1 copia', 'desactivaron {n} copias')} (sus reservas y enlaces se conservan).`);
		if (w.mentoria_template_no_webhook)
			out.push('La plantilla de Mentoría no está en ningún webhook tuyo: sus sesiones no enviarán WhatsApp hasta que la añadas en Webhooks.');
		if (w.soporte_shared_no_webhook)
			out.push('El tipo de Soporte no está en ningún webhook tuyo: sus sesiones no enviarán WhatsApp hasta que lo añadas en Webhooks.');
		return out;
	}

	async function saveSettings() {
		savingSettings = true;
		try {
			const res = await teamApi.putSettings({
				mentoria_template_id: tplChoice || null,
				soporte_shared_id: supChoice || null
			});
			applySettings(res);
			settingsWarnings = warningLines(res.warnings);
			toast.success('Tipos predefinidos guardados');
			await Promise.all([load(), loadOwnerTypes()]);
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron guardar los tipos predefinidos');
		} finally {
			savingSettings = false;
		}
	}

	function bookUrl(slug: string) {
		return `${window.location.origin}/book/${slug}`;
	}

	function roleBadge(m: TeamMember): { label: string; variant: 'default' | 'secondary' | 'outline' } {
		if (m.is_owner) return { label: 'Propietario', variant: 'default' };
		if (m.is_admin) return { label: 'Administrador', variant: 'secondary' };
		if (m.area === 'mentoria') return { label: 'Mentor', variant: 'outline' };
		if (m.area === 'soporte') return { label: 'Soporte', variant: 'outline' };
		return { label: 'Sin área', variant: 'outline' };
	}

	function authBadge(m: TeamMember): string[] {
		const badges: string[] = [];
		if (m.provider === 'google') badges.push('Google');
		else if (m.provider === 'microsoft') badges.push('Microsoft');
		if (m.email_login) badges.push('Correo');
		return badges;
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}
	function fmtDateTime(iso: string) {
		return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
	}
	function daysLeft(iso: string) {
		const diff = new Date(iso).getTime() - Date.now();
		return Math.max(0, Math.ceil(diff / 86_400_000));
	}
</script>

<ConfirmDialog
	bind:open={confirmOpen}
	title={confirmTitle}
	description={confirmDescription}
	confirmText={confirmActionText}
	cancelText="Cancelar"
	destructive={confirmDestructive}
	onConfirm={() => pendingAction?.()}
/>

<!-- Resolve-meetings dialog (shown when archiving a member with upcoming bookings) -->
<Dialog.Root bind:open={resolveOpen}>
	<Dialog.Content class="max-w-[calc(100%-2rem)] rounded-lg sm:max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>Resolver las próximas reuniones de {resolveMember?.name}</Dialog.Title>
			<Dialog.Description>
				Quedan {resolveBookings.length} reunión{resolveBookings.length === 1 ? '' : 'es'} próxima{resolveBookings.length === 1 ? '' : 's'} por resolver.
				Pasa cada una a otra persona de su área o cancélala. Cuando todas estén resueltas, el miembro se archivará automáticamente.
			</Dialog.Description>
		</Dialog.Header>

		{#if resolveError}
			<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{resolveError}</p>
		{/if}

		<div class="max-h-[50vh] space-y-2 overflow-y-auto">
			{#each resolveBookings as b (b.id)}
				{@const cands = candidatesOf(b.id)}
				{@const cstate = resolveCandidates[b.id]}
				<div class="rounded-lg border p-3">
					<div class="mb-2">
						<p class="text-sm font-medium">{b.event_type_name}</p>
						<p class="text-xs text-muted-foreground">
							{fmtDateTime(b.start_at)} · {b.attendee_name || b.attendee_email || 'asistente'}
						</p>
					</div>
					{#if cstate === 'loading' || cstate === undefined}
						<p class="mb-2 text-xs text-muted-foreground">Buscando personas del área…</p>
					{:else if cstate === 'error'}
						<p class="mb-2 text-xs text-destructive">No se pudo cargar quién puede atenderla. Puedes cancelarla o cerrar y volver a intentarlo.</p>
					{:else if cands.length === 0}
						<p class="mb-2 text-xs text-muted-foreground">No hay otra persona del área disponible: solo puede cancelarse.</p>
					{/if}
					<div class="flex flex-wrap items-center gap-2">
						{#if cands.length > 0}
							<Select.Root
								type="single"
								value={resolveChoice[b.id] ?? ''}
								onValueChange={(v) => { resolveChoice = { ...resolveChoice, [b.id]: v ?? '' }; }}
								disabled={resolveBusy}
							>
								<Select.Trigger class="w-full min-w-40 sm:w-fit">
									{cands.find((t) => t.id === resolveChoice[b.id])?.name ?? 'Elegir persona…'}
								</Select.Trigger>
								<Select.Content>
									{#each cands as t (t.id)}
										<Select.Item value={t.id} label={t.name}>{t.name}</Select.Item>
									{/each}
								</Select.Content>
							</Select.Root>
							<Button size="sm" variant="outline" class="h-8" disabled={resolveBusy || !resolveChoice[b.id]} onclick={() => reassignOne(b.id)}>
								{resolveChoice[b.id] ? `Pasar a ${cands.find((t) => t.id === resolveChoice[b.id])?.name ?? 'otra persona'}` : 'Pasar a otra persona'}
							</Button>
						{/if}
						<Button size="sm" variant="ghost" class="h-8 text-destructive hover:text-destructive" disabled={resolveBusy} onclick={() => cancelOne(b.id)}>
							Cancelar reunión
						</Button>
					</div>
				</div>
			{/each}
		</div>

		<div class="space-y-1.5">
			<Label for="resolve-reason">Motivo si cancelas <span class="font-normal text-muted-foreground">(opcional — el cliente lo verá en el aviso)</span></Label>
			<Textarea id="resolve-reason" bind:value={resolveReason} rows={2} maxlength={300} placeholder="Por ejemplo: la persona que te atendía ya no está disponible" class="text-base sm:text-sm" />
		</div>

		<Dialog.Footer class="mt-2 gap-2 sm:justify-between">
			<Button variant="ghost" class="text-destructive hover:text-destructive" disabled={resolveBusy} onclick={cancelAllRemaining}>
				Cancelar todas las pendientes
			</Button>
			<Button variant="outline" disabled={resolveBusy} onclick={() => { resolveOpen = false; resolveMember = null; }}>
				Terminar después
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<svelte:head><title>Miembros — Calnode</title></svelte:head>

<div class="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Miembros</h1>
		<p class="mt-1 text-sm text-muted-foreground">Administra los miembros, roles e invitaciones del espacio de trabajo.</p>
	</div>
	{#if $currentUser?.is_admin}
		<Button class="self-start sm:self-auto" onclick={() => { showInvite = !showInvite; inviteError = ''; inviteResult = null; }}>
			{showInvite ? 'Cancelar' : 'Invitar miembro'}
		</Button>
	{/if}
</div>

{#if showInvite}
	<div class="mb-6 rounded-lg border bg-card p-4 sm:p-6">
		<h2 class="mb-4 text-sm font-semibold">Invitar a un miembro</h2>

		{#if inviteResult}
			<div class="mb-4 rounded-lg border border-amber-200 bg-amber-50 p-4">
				<p class="mb-1 text-sm font-semibold text-amber-900">Enlace de invitación generado</p>
				<p class="mb-3 text-xs text-amber-800">{inviteResult.note}</p>
				{#if inviteResult.email_sent}
					<p class="mb-3 text-xs text-amber-700">Se ha enviado un correo de invitación a {inviteResult.email}.</p>
				{:else}
					<p class="mb-3 text-xs text-amber-700">SMTP no está configurado — comparte este enlace directamente con {inviteResult.email}.</p>
				{/if}
				<div class="flex flex-col gap-2 sm:flex-row sm:items-center">
					<code class="min-w-0 flex-1 overflow-x-auto rounded border bg-white px-2 py-1.5 font-mono text-xs text-gray-800">{inviteResult.invite_url}</code>
					<Button variant="outline" size="sm" class="self-start sm:self-auto" onclick={() => copyInviteUrl(inviteResult!.invite_url)}>
						{copied ? '¡Copiado!' : 'Copiar enlace'}
					</Button>
				</div>
			</div>
		{/if}

		{#if inviteError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{inviteError}</p>{/if}

		<div class="mb-4 grid grid-cols-1 gap-4 sm:grid-cols-[minmax(0,1fr)_14rem]">
			<div class="space-y-1.5">
				<Label for="inv-email">Correo electrónico</Label>
				<Input id="inv-email" type="email" bind:value={inviteEmail} placeholder="miembro@ejemplo.com"
					onkeydown={(e) => e.key === 'Enter' && sendInvite()} />
			</div>
			<div class="space-y-1.5">
				<Label for="inv-role">Rol</Label>
				<Select.Root type="single" value={inviteRole} onValueChange={(v) => { if (v) inviteRole = v as InviteRole; }}>
					<Select.Trigger id="inv-role" class="w-full">{INVITE_ROLE_LABELS[inviteRole]}</Select.Trigger>
					<Select.Content>
						<Select.Item value="mentoria" label="Mentor">Mentor</Select.Item>
						<Select.Item value="soporte" label="Soporte">Soporte</Select.Item>
						{#if isOwnerViewer}
							<Select.Item value="admin" label="Administrador">Administrador</Select.Item>
						{/if}
					</Select.Content>
				</Select.Root>
			</div>
		</div>

		<Button onclick={sendInvite} disabled={inviting}>
			{inviting ? 'Generando…' : 'Generar enlace de invitación'}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="mb-8">
		<div class="mb-3 flex items-center justify-between">
			<h2 class="text-sm font-semibold uppercase tracking-wide text-muted-foreground">Miembros</h2>
			{#if $currentUser?.is_admin}
				<button class="text-xs text-muted-foreground hover:text-foreground" onclick={toggleArchived}>
					{showArchived ? 'Ocultar archivados' : 'Mostrar archivados'}
				</button>
			{/if}
		</div>
		{#if members.length === 0}
			<div class="rounded-lg border border-dashed bg-card p-8 text-center">
				<p class="text-sm text-muted-foreground">Aún no hay miembros.</p>
			</div>
		{:else}
			<!-- Fork: one card per person (the 6-column table clipped the controls at 375 px). -->
			<div class="overflow-hidden rounded-lg border bg-card">
				<ul class="divide-y">
					{#each members as m (m.id)}
						{@const rb = roleBadge(m)}
						<li class="space-y-3 p-4 transition-colors hover:bg-muted/20 {m.archived ? 'opacity-60' : ''}">
							<div class="flex items-start gap-3">
								{#if m.avatar_url}
									<img src={m.avatar_url} alt={m.name} class="h-8 w-8 shrink-0 rounded-full object-cover" />
								{:else}
									<div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">
										{m.name.slice(0, 2).toUpperCase()}
									</div>
								{/if}
								<div class="min-w-0 flex-1">
									<p class="break-words font-medium">
										{m.name}
										{#if m.id === $currentUser?.id}<span class="text-xs text-muted-foreground">(tú)</span>{/if}
									</p>
									<p class="break-all text-xs text-muted-foreground">{m.email}</p>
									<div class="mt-1.5 flex flex-wrap items-center gap-1.5">
										<Badge variant={rb.variant}>{rb.label}</Badge>
										{#if (m.is_owner || m.is_admin) && (m.area === 'mentoria' || m.area === 'soporte')}
											<Badge variant="outline" class="text-xs">Atiende {AREA_LABELS[m.area]}</Badge>
										{/if}
										{#if m.archived}<Badge variant="outline" class="text-xs text-muted-foreground">Archivado</Badge>{/if}
										{#each m.teams as tm}
											<Badge variant="secondary" class="text-xs">{tm.name}</Badge>
										{/each}
										{#each authBadge(m) as b}
											<Badge variant="outline" class="text-xs">{b}</Badge>
										{/each}
										<span class="text-xs text-muted-foreground">Se unió el {fmtDate(m.created_at)}</span>
									</div>
								</div>
							</div>

							{#if m.personal_link && !m.archived}
								<div class="flex flex-col gap-1.5 rounded-md border bg-background px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
									<div class="min-w-0">
										<p class="text-xs font-medium text-muted-foreground">
											Enlace personal{#if !m.personal_link.active}<span class="ml-1 font-normal">(inactivo)</span>{/if}
										</p>
										<a href={m.personal_link.url} target="_blank" rel="noopener noreferrer" class="break-all text-sm text-primary hover:underline">{m.personal_link.url}</a>
									</div>
									<Button variant="outline" size="sm" class="self-start sm:self-auto" onclick={() => copyLink(m.personal_link!.url)}>Copiar enlace</Button>
								</div>
							{/if}

							{#if $currentUser?.is_admin}
								<div class="flex flex-wrap items-end gap-2">
									{#if m.archived}
										{#if m.archived_by_name}
											<span class="text-xs text-muted-foreground">Archivado por {m.archived_by_name}</span>
										{/if}
										<!-- Owner can restore anyone; an admin only members they archived. -->
										{#if $currentUser.is_owner || m.archived_by === $currentUser.id}
											<Button size="sm" variant="outline" onclick={() => restoreMember(m)}>Restaurar</Button>
										{/if}
									{:else if resetTarget === m.id}
										<div class="flex w-full flex-wrap items-center gap-1.5">
											{#if resetOk}
												<span class="text-xs font-medium text-green-600">Contraseña actualizada</span>
											{:else}
												<Input type="password" bind:value={resetPassword} placeholder="Nueva contraseña"
													class="h-8 w-full text-base sm:w-44 sm:text-xs" onkeydown={(e) => e.key === 'Enter' && submitReset(m.id)} />
												<Button size="sm" variant="outline" onclick={() => submitReset(m.id)} disabled={resetting}>
													{resetting ? '…' : 'Guardar'}
												</Button>
												<Button size="sm" variant="ghost" onclick={cancelReset}>Cancelar</Button>
												{#if resetError}<span class="w-full text-xs text-destructive">{resetError}</span>{/if}
											{/if}
										</div>
									{:else}
										{#if canEditRole(m)}
											<div class="space-y-1">
												<p class="text-xs text-muted-foreground">Rol</p>
												<Select.Root type="single" bind:value={() => uiRole(m), (v) => changeRole(m, v)}>
													<Select.Trigger class="h-8 w-fit min-w-36 text-xs" aria-label="Rol de {m.name}">{UI_ROLE_LABELS[uiRole(m)]}</Select.Trigger>
													<Select.Content>
														{#each roleOptionsFor() as r (r)}
															<Select.Item value={r} label={UI_ROLE_LABELS[r]}>{UI_ROLE_LABELS[r]}</Select.Item>
														{/each}
													</Select.Content>
												</Select.Root>
											</div>
										{/if}
										{#if canEditAlso(m)}
											<div class="space-y-1">
												<p class="text-xs text-muted-foreground">También atiende</p>
												<Select.Root type="single" bind:value={() => areaOf(m) || 'none', (v) => changeAlso(m, v)}>
													<Select.Trigger class="h-8 w-fit min-w-32 text-xs" aria-label="También atiende ({m.name})">{alsoLabel(areaOf(m))}</Select.Trigger>
													<Select.Content>
														{#each alsoOptions(m) as a (a || 'none')}
															<Select.Item value={a || 'none'} label={alsoLabel(a)}>{alsoLabel(a)}</Select.Item>
														{/each}
													</Select.Content>
												</Select.Root>
											</div>
										{/if}
										{#if m.id !== $currentUser.id}
											{#if $currentUser.is_owner && !m.is_owner}
												<Button size="sm" variant="ghost" onclick={() => confirmTransfer(m)}>Transferir propiedad</Button>
											{/if}
											<!-- Reset password + Archive only on members this viewer may manage:
											     never the owner; another admin only if the viewer is the owner. -->
											{#if !m.is_owner && (!m.is_admin || $currentUser.is_owner)}
												<Button size="sm" variant="ghost" onclick={() => startReset(m.id)}>Restablecer contraseña</Button>
												<Button size="sm" variant="ghost" class="text-destructive hover:text-destructive" onclick={() => startArchive(m)}>Archivar</Button>
											{/if}
										{/if}
									{/if}
								</div>
							{/if}
						</li>
					{/each}
				</ul>
			</div>
			{#if $currentUser?.is_admin}
				<p class="mt-2 text-xs text-muted-foreground">
					<span class="font-medium">Mentor</span>: atiende la Mentoría con su propio enlace personal.
					<span class="font-medium">Soporte</span>: recibe por turnos las reservas de Soporte.
					<span class="font-medium">Sin área</span>: no atiende los tipos predefinidos. Cada persona ve solo sus reservas;
					el propietario y los administradores ven todas y pueden pasarlas a otra persona de la misma área.
				</p>
			{/if}
		{/if}
	</div>

	<!-- Fork: the two predefined types (owner edits, admins read). -->
	{#if $currentUser?.is_admin}
		<div class="mb-8">
			<h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-muted-foreground">Tipos predefinidos</h2>
			<div class="space-y-4 rounded-lg border bg-card p-4 sm:p-6">
				{#if settingsError && !settings}
					<div class="flex flex-col gap-2 rounded-md bg-destructive/10 px-3 py-2 sm:flex-row sm:items-center sm:justify-between" role="alert">
						<p class="text-sm text-destructive">No se pudieron cargar los tipos predefinidos.</p>
						<Button variant="outline" size="sm" onclick={loadSettings}>Reintentar</Button>
					</div>
				{:else if !settings}
					<p class="text-sm text-muted-foreground">Cargando…</p>
				{:else}
					{@const canEdit = settings.can_edit && isOwnerViewer}
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<div class="min-w-0 space-y-1.5">
							<p class="text-sm font-medium">Plantilla de Mentoría <span class="font-normal text-muted-foreground">(cada mentor recibe su copia)</span></p>
							{#if canEdit}
								<Select.Root type="single" value={tplChoice || 'none'} onValueChange={(v) => (tplChoice = !v || v === 'none' ? '' : v)} disabled={savingSettings}>
									<Select.Trigger class="w-full" aria-label="Plantilla de Mentoría">{tplChoice ? typeName(tplChoice) || 'Tipo actual' : 'Ninguna'}</Select.Trigger>
									<Select.Content>
										<Select.Item value="none" label="Ninguna">Ninguna</Select.Item>
										{#each ownerTypes as t (t.id)}
											<Select.Item value={t.id} label={t.name} disabled={t.id === supChoice}>{t.name}</Select.Item>
										{/each}
									</Select.Content>
								</Select.Root>
							{:else}
								<p class="text-sm">{settings.mentoria_template?.name ?? 'Ninguna'}</p>
							{/if}
							{#if settings.mentoria_template}
								{@const n = settings.mentoria_template.copies}
								<p class="text-xs text-muted-foreground">
									{n === 1 ? '1 copia (una por mentor)' : `${n} copias (una por mentor)`} ·
									<a href={bookUrl(settings.mentoria_template.slug)} target="_blank" rel="noopener noreferrer" class="underline">/book/{settings.mentoria_template.slug}</a>
								</p>
							{/if}
						</div>
						<div class="min-w-0 space-y-1.5">
							<p class="text-sm font-medium">Tipo de Soporte <span class="font-normal text-muted-foreground">(se reparte entre el personal de soporte)</span></p>
							{#if canEdit}
								<Select.Root type="single" value={supChoice || 'none'} onValueChange={(v) => (supChoice = !v || v === 'none' ? '' : v)} disabled={savingSettings}>
									<Select.Trigger class="w-full" aria-label="Tipo de Soporte">{supChoice ? typeName(supChoice) || 'Tipo actual' : 'Ninguno'}</Select.Trigger>
									<Select.Content>
										<Select.Item value="none" label="Ninguno">Ninguno</Select.Item>
										{#each ownerTypes as t (t.id)}
											<Select.Item value={t.id} label={t.name} disabled={t.id === tplChoice}>{t.name}</Select.Item>
										{/each}
									</Select.Content>
								</Select.Root>
							{:else}
								<p class="text-sm">{settings.soporte_shared?.name ?? 'Ninguno'}</p>
							{/if}
							{#if settings.soporte_shared}
								{@const hosts = settings.soporte_shared.hosts ?? []}
								<p class="text-xs text-muted-foreground">
									{hosts.length > 0 ? `Lo atienden: ${hosts.map((h) => h.name).join(', ')}` : 'Nadie tiene el área Soporte'} ·
									<a href={bookUrl(settings.soporte_shared.slug)} target="_blank" rel="noopener noreferrer" class="underline">/book/{settings.soporte_shared.slug}</a>
								</p>
							{/if}
						</div>
					</div>
					{#if canEdit}
						<p class="text-xs text-muted-foreground">
							Solo aparecen tus tipos de atención con la sala de video integrada. Los tipos predefinidos solo los edita el propietario;
							cada miembro puede crear además sus propios tipos.
						</p>
						<div class="flex justify-end">
							<Button onclick={confirmSettings} disabled={!settingsDirty || savingSettings}>
								{savingSettings ? 'Guardando…' : 'Guardar tipos predefinidos'}
							</Button>
						</div>
					{:else}
						<p class="text-xs text-muted-foreground">Predefinido por el propietario: solo él puede cambiarlos.</p>
					{/if}
					{#if settingsWarnings.length > 0}
						<ul class="space-y-1 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300" role="status">
							{#each settingsWarnings as w}<li>{w}</li>{/each}
						</ul>
					{/if}
					{#if settings.mentoria_template?.copy_links && settings.mentoria_template.copy_links.length > 0}
						<div class="space-y-1.5 border-t pt-4">
							<p class="text-sm font-medium">Enlaces de los mentores</p>
							<ul class="space-y-1.5">
								{#each settings.mentoria_template.copy_links as l (l.slug)}
									<li class="flex flex-col gap-1 rounded-md border px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
										<div class="min-w-0">
											<p class="text-sm font-medium">{l.mentor_name}{#if !l.active}<span class="ml-1.5 text-xs font-normal text-muted-foreground">(inactiva)</span>{/if}</p>
											<a href={l.url} target="_blank" rel="noopener noreferrer" class="break-all text-xs text-primary hover:underline">{l.url}</a>
										</div>
										<Button variant="outline" size="sm" class="self-start sm:self-auto" onclick={() => copyLink(l.url)}>Copiar enlace</Button>
									</li>
								{/each}
							</ul>
						</div>
					{/if}
				{/if}
			</div>
		</div>
	{/if}

	<!-- Pending invites -->
	{#if $currentUser?.is_admin && invites.length > 0}
		<div>
			<h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-muted-foreground">Invitaciones pendientes</h2>
			<div class="overflow-hidden rounded-lg border bg-card">
				<ul class="divide-y">
					{#each invites as inv (inv.id)}
						<li class="flex flex-col gap-2 p-4 transition-colors hover:bg-muted/20 sm:flex-row sm:items-center sm:justify-between">
							<div class="min-w-0">
								<div class="flex flex-wrap items-center gap-1.5">
									<p class="break-all text-sm font-medium">{inv.email}</p>
									{#if inv.role}
										<Badge variant="outline" class="text-xs">{INVITE_ROLE_LABELS[inv.role] ?? inv.role}</Badge>
									{:else}
										<Badge variant="outline" class="text-xs text-muted-foreground">Sin rol</Badge>
									{/if}
								</div>
								<p class="text-xs text-muted-foreground">
									Vence el {fmtDate(inv.expires_at)} ({daysLeft(inv.expires_at)}d restantes)
								</p>
							</div>
							<div class="flex flex-wrap gap-1">
								<Button size="sm" variant="ghost" onclick={() => resendInvite(inv.id)}>Reenviar</Button>
								<Button size="sm" variant="ghost" class="text-destructive hover:text-destructive" onclick={() => revokeInvite(inv.id)}>Revocar</Button>
							</div>
						</li>
					{/each}
				</ul>
			</div>
			<p class="mt-2 text-xs text-muted-foreground">
				Reenviar genera un enlace nuevo, reinicia el vencimiento de 7 días y lo vuelve a enviar por correo — el enlace anterior deja de funcionar.
			</p>
		</div>
	{/if}
{/if}
