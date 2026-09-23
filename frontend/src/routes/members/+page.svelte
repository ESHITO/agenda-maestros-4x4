<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type TeamMember, type Invite, type UpcomingBooking } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Badge } from '$lib/components/ui/badge';
	import * as Select from '$lib/components/ui/select';
	import { toast } from 'svelte-sonner';

	let members: TeamMember[] = $state([]);
	let invites: Invite[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showArchived = $state(false);

	// Invite form
	let showInvite = $state(false);
	let inviteEmail = $state('');
	let inviting = $state(false);
	let inviteError = $state('');
	let inviteResult = $state<{ invite_url: string; email: string; email_sent: boolean; note: string } | null>(null);
	let copied = $state(false);

	// Confirm dialog
	let confirmOpen = $state(false);
	let confirmTitle = $state('');
	let confirmDescription = $state('');
	let confirmActionText = $state('Confirmar');
	let pendingAction: (() => void) | null = null;

	function openConfirm(opts: { title: string; description: string; confirmText: string; action: () => void }) {
		confirmTitle = opts.title;
		confirmDescription = opts.description;
		confirmActionText = opts.confirmText;
		pendingAction = opts.action;
		confirmOpen = true;
	}

	// Password reset — keyed by user id
	let resetTarget = $state<string | null>(null);
	let resetPassword = $state('');
	let resetting = $state(false);
	let resetError = $state('');
	let resetOk = $state(false);

	// Resolve-meetings (archive) dialog
	let resolveOpen = $state(false);
	let resolveMember = $state<TeamMember | null>(null);
	let resolveBookings = $state<UpcomingBooking[]>([]);
	let resolveChoice = $state<Record<string, string>>({});
	let resolveBusy = $state(false);
	let resolveError = $state('');

	let reassignTargets = $derived(members.filter((m) => !m.archived && m.id !== resolveMember?.id));

	async function load() {
		try {
			members = await api.get<TeamMember[]>(showArchived ? '/v1/users?include_archived=true' : '/v1/users');
		} catch (e: any) {
			error = e.message;
		}
		// Invites are admin-only (ListInvites in invites.go), but /v1/users now answers
		// support too. Fetched together in one Promise.all, the 403 on this call rejected
		// the pair and left a support user staring at an error banner over an empty
		// directory — the one thing the page exists to show them. Its own failure now
		// costs only the invites section, which support cannot act on anyway.
		try {
			invites = await api.get<Invite[]>('/v1/invites');
		} catch {
			invites = [];
		}
		loading = false;
	}

	onMount(load);

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
			const res = await api.post<{
				id: string; email: string; invite_url: string;
				expires_at: string; email_sent: boolean; note: string;
			}>('/v1/invites', { email: inviteEmail.trim().toLowerCase() });
			inviteResult = res;
			inviteEmail = '';
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

	// --- Role management (owner only) ---
	type AssignableRole = 'admin' | 'support' | 'member';
	const roleNames: Record<AssignableRole, string> = {
		admin: 'administrador',
		support: 'soporte',
		member: 'miembro'
	};

	async function setRole(m: TeamMember, role: AssignableRole) {
		try {
			await api.patch(`/v1/users/${m.id}/role`, { role });
			toast.success(`${m.name} ahora es ${roleNames[role]}`);
			await load();
		} catch (e: any) { toast.error(e.message || 'No se pudo cambiar el rol'); }
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
			const res = await api.get<{ items: UpcomingBooking[] }>(`/v1/users/${m.id}/upcoming-bookings`);
			if (res.items.length > 0) {
				resolveMember = m;
				resolveBookings = res.items;
				resolveChoice = {};
				resolveError = '';
				resolveOpen = true;
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

	async function doArchive(id: string) {
		try { await api.post(`/v1/users/${id}/archive`); toast.success('Miembro archivado'); await load(); }
		catch (e: any) { toast.error(e.message || 'No se pudo archivar al miembro'); }
	}

	async function restoreMember(m: TeamMember) {
		try { await api.post(`/v1/users/${m.id}/restore`); toast.success(`${m.name} restaurado`); await load(); }
		catch (e: any) { toast.error(e.message || 'No se pudo restaurar al miembro'); }
	}

	// --- Resolve-meetings dialog actions ---
	async function reassignOne(bookingId: string) {
		const hostId = resolveChoice[bookingId];
		if (!hostId) return;
		resolveBusy = true; resolveError = '';
		try {
			await api.post(`/v1/bookings/${bookingId}/reassign`, { host_id: hostId });
			resolveBookings = resolveBookings.filter((b) => b.id !== bookingId);
			await finishResolveIfDone();
		} catch (e: any) { resolveError = e.message; }
		finally { resolveBusy = false; }
	}

	async function cancelOne(bookingId: string) {
		resolveBusy = true; resolveError = '';
		try {
			await api.post(`/v1/bookings/${bookingId}/cancel`, { reason: 'Host is being archived' });
			resolveBookings = resolveBookings.filter((b) => b.id !== bookingId);
			await finishResolveIfDone();
		} catch (e: any) { resolveError = e.message; }
		finally { resolveBusy = false; }
	}

	async function cancelAllRemaining() {
		resolveBusy = true; resolveError = '';
		try {
			for (const b of [...resolveBookings]) {
				await api.post(`/v1/bookings/${b.id}/cancel`, { reason: 'Host is being archived' });
			}
			resolveBookings = [];
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
		await navigator.clipboard.writeText(url);
		copied = true;
		setTimeout(() => { copied = false; }, 2000);
	}

	function roleLabel(m: TeamMember) {
		return m.role === 'owner' ? 'Propietario'
			: m.role === 'admin' ? 'Administrador'
			: m.role === 'support' ? 'Soporte'
			: 'Miembro';
	}
	function roleVariant(m: TeamMember): 'default' | 'secondary' | 'outline' {
		return m.role === 'owner' ? 'default' : m.role === 'admin' ? 'secondary' : 'outline';
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
	destructive
	onConfirm={() => pendingAction?.()}
/>

<!-- Resolve-meetings dialog (shown when archiving a member with upcoming bookings) -->
<Dialog.Root bind:open={resolveOpen}>
	<Dialog.Content class="max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>Resolver las próximas reuniones de {resolveMember?.name}</Dialog.Title>
			<Dialog.Description>
				Quedan {resolveBookings.length} reunión{resolveBookings.length === 1 ? '' : 'es'} próxima{resolveBookings.length === 1 ? '' : 's'} por resolver.
				Reasigna cada una a otro miembro o cancélala. Cuando todas estén resueltas, el miembro se archivará automáticamente.
			</Dialog.Description>
		</Dialog.Header>

		{#if resolveError}
			<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{resolveError}</p>
		{/if}

		<div class="max-h-[50vh] space-y-2 overflow-y-auto">
			{#each resolveBookings as b (b.id)}
				<div class="rounded-lg border p-3">
					<div class="mb-2">
						<p class="text-sm font-medium">{b.event_type_name}</p>
						<p class="text-xs text-muted-foreground">
							{fmtDateTime(b.start_at)} · {b.attendee_name || b.attendee_email || 'asistente'}
						</p>
					</div>
					<div class="flex flex-wrap items-center gap-2">
						<Select.Root
							type="single"
							value={resolveChoice[b.id] ?? ''}
							onValueChange={(v) => { resolveChoice = { ...resolveChoice, [b.id]: v ?? '' }; }}
							disabled={resolveBusy || reassignTargets.length === 0}
						>
							<Select.Trigger class="w-fit min-w-40">
								{reassignTargets.find((t) => t.id === resolveChoice[b.id])?.name ?? 'Reasignar a…'}
							</Select.Trigger>
							<Select.Content>
								{#each reassignTargets as t}
									<Select.Item value={t.id} label={t.name}>{t.name}</Select.Item>
								{/each}
							</Select.Content>
						</Select.Root>
						<Button size="sm" variant="outline" class="h-8" disabled={resolveBusy || !resolveChoice[b.id]} onclick={() => reassignOne(b.id)}>
							Reasignar
						</Button>
						<Button size="sm" variant="ghost" class="h-8 text-destructive hover:text-destructive" disabled={resolveBusy} onclick={() => cancelOne(b.id)}>
							Cancelar reunión
						</Button>
					</div>
				</div>
			{/each}
			{#if reassignTargets.length === 0}
				<p class="text-xs text-muted-foreground">No hay otros miembros activos para reasignar — las reuniones solo pueden cancelarse.</p>
			{/if}
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

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Miembros</h1>
		<p class="mt-1 text-sm text-muted-foreground">Administra los miembros, roles e invitaciones del espacio de trabajo.</p>
	</div>
	{#if $currentUser?.is_admin}
		<Button onclick={() => { showInvite = !showInvite; inviteError = ''; inviteResult = null; }}>
			{showInvite ? 'Cancelar' : 'Invitar miembro'}
		</Button>
	{/if}
</div>

{#if showInvite}
	<div class="mb-6 rounded-lg border bg-card p-6">
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
				<div class="flex items-center gap-2">
					<code class="flex-1 overflow-x-auto rounded border bg-white px-2 py-1.5 text-xs font-mono text-gray-800">{inviteResult.invite_url}</code>
					<Button variant="outline" size="sm" onclick={() => copyInviteUrl(inviteResult!.invite_url)}>
						{copied ? '¡Copiado!' : 'Copiar'}
					</Button>
				</div>
			</div>
		{/if}

		{#if inviteError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{inviteError}</p>{/if}

		<div class="mb-4 space-y-1.5">
			<Label for="inv-email">Correo electrónico</Label>
			<Input id="inv-email" type="email" bind:value={inviteEmail} placeholder="miembro@ejemplo.com"
				onkeydown={(e) => e.key === 'Enter' && sendInvite()} />
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
			<h2 class="text-sm font-semibold text-muted-foreground uppercase tracking-wide">Miembros</h2>
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
			<div class="rounded-lg border bg-card overflow-hidden">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b">
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Nombre</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Rol</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Equipos</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Autenticación</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Se unió</th>
							{#if $currentUser?.is_admin}<th class="px-4 pb-3 pt-3"></th>{/if}
						</tr>
					</thead>
					<tbody class="divide-y">
						{#each members as m (m.id)}
							<tr class="transition-colors hover:bg-muted/30 {m.archived ? 'opacity-60' : ''}">
								<td class="px-4 py-3">
									<div class="flex items-center gap-2.5">
										{#if m.avatar_url}
											<img src={m.avatar_url} alt={m.name} class="h-7 w-7 shrink-0 rounded-full object-cover" />
										{:else}
											<div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">
												{m.name.slice(0, 2).toUpperCase()}
											</div>
										{/if}
										<div class="min-w-0">
											<p class="font-medium">
												{m.name}
												{#if m.id === $currentUser?.id}<span class="text-xs text-muted-foreground">(tú)</span>{/if}
											</p>
											<p class="text-xs text-muted-foreground">{m.email}</p>
										</div>
									</div>
								</td>
								<td class="px-4 py-3">
									<div class="flex items-center gap-1.5">
										<Badge variant={roleVariant(m)}>{roleLabel(m)}</Badge>
										{#if m.archived}<Badge variant="outline" class="text-xs text-muted-foreground">Archivado</Badge>{/if}
									</div>
								</td>
								<td class="px-4 py-3">
									<div class="flex gap-1.5 flex-wrap">
										{#each m.teams as tm}
											<Badge variant="secondary" class="text-xs">{tm.name}</Badge>
										{:else}
											<span class="text-xs text-muted-foreground/50">—</span>
										{/each}
									</div>
								</td>
								<td class="px-4 py-3">
									<div class="flex gap-1.5 flex-wrap">
										{#each authBadge(m) as b}
											<Badge variant="outline" class="text-xs">{b}</Badge>
										{/each}
									</div>
								</td>
								<td class="px-4 py-3 text-sm text-muted-foreground">{fmtDate(m.created_at)}</td>
								{#if $currentUser?.is_admin}
									<td class="px-4 py-3">
										<div class="flex flex-wrap items-center justify-end gap-1">
											{#if m.archived}
												{#if m.archived_by_name}
													<span class="text-xs text-muted-foreground">Archivado por {m.archived_by_name}</span>
												{/if}
												<!-- Owner can restore anyone; an admin only members they archived. -->
												{#if $currentUser.is_owner || m.archived_by === $currentUser.id}
													<Button size="sm" variant="outline" class="h-7 text-xs" onclick={() => restoreMember(m)}>Restaurar</Button>
												{/if}
											{:else if m.id !== $currentUser.id}
												{#if resetTarget === m.id}
													<div class="flex items-center gap-1.5">
														{#if resetOk}
															<span class="text-xs text-green-600 font-medium">Contraseña actualizada</span>
														{:else}
															<Input type="password" bind:value={resetPassword} placeholder="Nueva contraseña"
																class="h-7 w-36 text-xs" onkeydown={(e) => e.key === 'Enter' && submitReset(m.id)} />
															{#if resetError}<span class="text-xs text-destructive">{resetError}</span>{/if}
															<Button size="sm" variant="outline" class="h-7 text-xs" onclick={() => submitReset(m.id)} disabled={resetting}>
																{resetting ? '…' : 'Guardar'}
															</Button>
															<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={cancelReset}>Cancelar</Button>
														{/if}
													</div>
												{:else}
													{#if $currentUser.is_owner && !m.is_owner}
														<!-- Tres roles ya no caben en dos botones espejo: el selector muestra
														     el rol vigente y permite elegir otro. -->
														<Select.Root
															type="single"
															value={m.role}
															onValueChange={(v) => { if (v && v !== m.role) setRole(m, v as AssignableRole); }}
														>
															<Select.Trigger class="h-7 w-fit min-w-28 text-xs">{roleLabel(m)}</Select.Trigger>
															<Select.Content>
																<Select.Item value="member" label="Miembro">Miembro</Select.Item>
																<Select.Item value="support" label="Soporte">Soporte</Select.Item>
																<Select.Item value="admin" label="Administrador">Administrador</Select.Item>
															</Select.Content>
														</Select.Root>
														<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => confirmTransfer(m)}>Transferir propiedad</Button>
													{/if}
													<!-- Reset password + Archive only on members this viewer may manage:
													     never the owner; another admin only if the viewer is the owner. -->
													{#if !m.is_owner && (!m.is_admin || $currentUser.is_owner)}
														<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => startReset(m.id)}>Restablecer contraseña</Button>
														<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => startArchive(m)}>Archivar</Button>
													{/if}
												{/if}
											{/if}
										</div>
									</td>
								{/if}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			{#if $currentUser?.is_admin}
				<p class="mt-2 text-xs text-muted-foreground">
					Soporte puede ver todas las reservas del equipo y cancelarlas o reprogramarlas para ayudar a un
					miembro, además de consultar esta lista. No tiene acceso a la configuración, las integraciones ni
					la gestión de usuarios (invitar, archivar, roles o equipos).
				</p>
			{/if}
		{/if}
	</div>

	<!-- Pending invites -->
	{#if $currentUser?.is_admin && invites.length > 0}
		<div>
			<h2 class="mb-3 text-sm font-semibold text-muted-foreground uppercase tracking-wide">Invitaciones pendientes</h2>
			<div class="rounded-lg border bg-card overflow-hidden">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b">
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Correo electrónico</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Vence</th>
							<th class="px-4 pb-3 pt-3"></th>
						</tr>
					</thead>
					<tbody class="divide-y">
						{#each invites as inv}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3">{inv.email}</td>
								<td class="px-4 py-3 text-muted-foreground">
									{fmtDate(inv.expires_at)}
									<span class="ml-1 text-xs">({daysLeft(inv.expires_at)}d restantes)</span>
								</td>
								<td class="px-4 py-3 text-right whitespace-nowrap">
									<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => resendInvite(inv.id)}>
										Reenviar
									</Button>
									<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => revokeInvite(inv.id)}>
										Revocar
									</Button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<p class="mt-2 text-xs text-muted-foreground">
				Reenviar genera un enlace nuevo, reinicia el vencimiento de 7 días y lo vuelve a enviar por correo — el enlace anterior deja de funcionar.
			</p>
		</div>
	{/if}
{/if}
