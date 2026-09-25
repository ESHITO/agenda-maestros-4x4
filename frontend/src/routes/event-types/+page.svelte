<script lang="ts">
	import { onMount } from 'svelte';
	import { base } from '$app/paths';
	import { api, teamApi, copyText, type EventType, type TeamSettings, type AvailabilityRule, type CopyLink } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Badge } from '$lib/components/ui/badge';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';

	let items: EventType[] = $state([]);
	let loading = $state(true);
	let showCreate = $state(false);

	let form = $state({ slug: '', name: '', description: '', duration_minutes: 30 });
	let creating = $state(false);
	let deleteOpen = $state(false);
	let deleteSlug = $state('');
	// Slug currently being duplicated, so the row's button can't be double-fired into two
	// copies while the request is in flight.
	let duplicating = $state('');

	// Fork: predefined types. The team settings (admins) name the Soporte rotation and,
	// for the owner, the mentors' links when the list item does not carry them. The
	// availability rules tell a mentor / support person that nobody can book them yet.
	let settings = $state<TeamSettings | null>(null);
	let hasRules = $state<boolean | null>(null);
	let openCopies = $state<Record<string, boolean>>({});

	// A copy is owned by the template's owner, so the owner's list holds every copy: they
	// are reached through the template card instead (one link per mentor). The mentor sees
	// their own copy (owned = false) as a read-only predefined type.
	const listed = $derived(items.filter((et) => !(et.team?.kind === 'mentoria_copy' && et.owned !== false)));

	let filter = $state<'active' | 'archived'>('active');
	const visible = $derived(listed.filter((et) => (filter === 'archived' ? !!et.archived : !et.archived)));
	const archivedCount = $derived(listed.filter((et) => et.archived).length);
	const activeCount = $derived(listed.length - archivedCount);

	// A predefined type this person attends (their copy, the shared Soporte, or the
	// template they own) is useless without availability: say so.
	const attendsPredefined = $derived(
		listed.some((et) => !et.archived && et.team && (et.owned === false || et.team.kind === 'mentoria_template'))
	);

	async function load() {
		try {
			const res = await api.get<{ items: EventType[] }>('/v1/event-types');
			items = res.items;
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron cargar los tipos de atención');
		} finally {
			loading = false;
		}
	}

	async function loadTeamContext() {
		if ($currentUser?.is_admin) {
			teamApi.getSettings().then((s) => (settings = s)).catch(() => {});
		}
		try {
			const res = await api.get<{ items: AvailabilityRule[] }>('/v1/availability-rules');
			hasRules = (res.items?.length ?? 0) > 0;
		} catch {
			hasRules = null; // unknown: say nothing rather than a false warning
		}
	}

	onMount(() => {
		load();
		loadTeamContext();
	});

	async function create() {
		if (!form.slug || !form.name || !form.duration_minutes) {
			toast.error('El slug, el nombre y la duración son obligatorios.');
			return;
		}
		creating = true;
		try {
			await api.post('/v1/event-types', {
				slug: form.slug,
				name: form.name,
				description: form.description || undefined,
				duration_minutes: Number(form.duration_minutes)
			});
			form = { slug: '', name: '', description: '', duration_minutes: 30 };
			showCreate = false;
			await load();
		} catch (e: any) {
			toast.error(e.message || 'No se pudo crear el tipo de atención');
		} finally {
			creating = false;
		}
	}

	async function saveActive(et: EventType, newActive: boolean) {
		try {
			await api.patch(`/v1/event-types/${et.slug}`, { is_active: newActive });
		} catch (e: any) {
			et.is_active = !newActive; // revert optimistic update
			toast.error(e.message || 'No se pudo actualizar el estado');
		}
	}

	// Fork: archiving the Mentoría template switches off every mentor's copy (a copy is
	// active only while its template is not archived), and archiving the Soporte type
	// leaves nothing to book for Soporte. Both ask first.
	let archiveOpen = $state(false);
	let archiveTarget = $state<EventType | null>(null);
	let archiveTitle = $state('');
	let archiveDescription = $state('');

	function askArchive(et: EventType) {
		const kind = et.team?.kind;
		const n = kind === 'mentoria_template' ? copiesOf(et) : 0;
		if (!et.archived && kind === 'mentoria_template' && n > 0) {
			archiveTitle = `¿Archivar la plantilla «${et.name}»?`;
			archiveDescription = `${n === 1 ? 'La copia del mentor dejará' : `Las ${n} copias de los mentores dejarán`} de aceptar reservas: los enlaces personales se desactivan hasta que la restaures. Las reservas ya hechas se conservan.`;
		} else if (!et.archived && kind === 'soporte_shared') {
			archiveTitle = `¿Archivar «${et.name}»?`;
			archiveDescription = 'Es el tipo de Soporte: nadie podrá reservar Soporte hasta que lo restaures. Las reservas ya hechas se conservan.';
		} else {
			archive(et, !et.archived);
			return;
		}
		archiveTarget = et;
		archiveOpen = true;
	}

	async function archive(et: EventType, archived: boolean) {
		try {
			await api.patch(`/v1/event-types/${et.slug}`, { archived });
			const kind = et.team?.kind;
			toast.success(
				archived && kind === 'mentoria_template' && copiesOf(et) > 0
					? 'Plantilla archivada: los enlaces de los mentores ya no aceptan reservas'
					: archived && kind === 'soporte_shared'
						? 'Tipo de Soporte archivado: nadie puede reservarlo'
						: archived ? 'Tipo de atención archivado' : 'Tipo de atención restaurado'
			);
			await load();
		} catch (e: any) {
			toast.error(e.message || 'No se pudo actualizar el tipo de atención');
		}
	}

	// The copy is created inactive, so the toast names the new slug: nothing appears on a
	// booking page until the operator edits it and switches it on.
	async function duplicateEventType(et: EventType) {
		if (duplicating) return;
		duplicating = et.slug;
		try {
			const copy = await api.post<EventType>(`/v1/event-types/${et.slug}/duplicate`);
			toast.success(`Duplicado como "${copy.slug}". Estará inactivo hasta que lo actives.`);
			await load();
		} catch (e: any) {
			toast.error(e.message || 'No se pudo duplicar el tipo de atención');
		} finally {
			duplicating = '';
		}
	}

	function del(slug: string) {
		deleteSlug = slug;
		deleteOpen = true;
	}

	async function doDelete() {
		try {
			await api.del(`/v1/event-types/${deleteSlug}`);
			await load();
		} catch (e: any) {
			toast.error(e.message || 'No se pudo eliminar el tipo de atención');
		}
	}

	function bookLink(slug: string) {
		return `${window.location.origin}/book/${slug}`;
	}

	async function copyLink(url: string) {
		if (await copyText(url)) toast.success('Enlace copiado');
		else toast.error('No se pudo copiar; mantén pulsado el enlace para copiarlo.');
	}

	// The mentors' links of the template: from the list item, else from the settings (both
	// owner-only on the server).
	function copyLinksOf(et: EventType): CopyLink[] {
		if (et.team?.copy_links) return et.team.copy_links;
		if (settings?.mentoria_template?.id === et.id) return settings.mentoria_template.copy_links ?? [];
		return [];
	}
	function copiesOf(et: EventType): number {
		return et.team?.copies ?? settings?.mentoria_template?.copies ?? copyLinksOf(et).length;
	}
	// S's rotation: from the list item (the server sends it to everyone who sees S), else the settings.
	function soporteHostsOf(et: EventType): { id: string; name: string }[] {
		return et.team?.hosts ?? settings?.soporte_shared?.hosts ?? [];
	}
</script>

<ConfirmDialog
	bind:open={deleteOpen}
	title="¿Eliminar el tipo de atención?"
	description="Esto eliminará permanentemente el tipo de atención y su enlace de reserva. Las reservas existentes no se ven afectadas."
	confirmText="Eliminar"
	destructive
	onConfirm={doDelete}
/>

<ConfirmDialog
	bind:open={archiveOpen}
	title={archiveTitle}
	description={archiveDescription}
	confirmText="Archivar"
	cancelText="Cancelar"
	destructive
	onConfirm={() => archiveTarget && archive(archiveTarget, true)}
/>

<svelte:head><title>Tipos de atención — Calnode</title></svelte:head>

<div class="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Tipos de atención</h1>
		<p class="mt-1 text-sm text-muted-foreground">Administra los tipos de reuniones que las personas pueden reservar contigo.</p>
	</div>
	<Button class="self-start sm:self-auto" onclick={() => { showCreate = !showCreate; }}>
		{showCreate ? 'Cancelar' : 'Nuevo tipo de atención'}
	</Button>
</div>

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-4 sm:p-6">
		<h2 class="mb-4 text-sm font-semibold">Nuevo tipo de atención</h2>
		<div class="mb-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
			<div class="space-y-1.5">
				<Label for="et-name">Nombre</Label>
				<Input id="et-name" bind:value={form.name} placeholder="Llamada de 30 minutos" />
			</div>
			<div class="space-y-1.5">
				<Label for="et-slug">Slug (URL)</Label>
				<Input id="et-slug" bind:value={form.slug} placeholder="30-min-call" />
			</div>
			<div class="space-y-1.5">
				<Label for="et-dur">Duración (minutos)</Label>
				<Input id="et-dur" type="number" min="5" bind:value={form.duration_minutes} />
			</div>
			<div class="space-y-1.5">
				<Label for="et-desc">Descripción (opcional)</Label>
				<Input id="et-desc" bind:value={form.description} placeholder="Breve descripción…" />
			</div>
		</div>
		<Button onclick={create} disabled={creating}>
			{creating ? 'Creando…' : 'Crear tipo de atención'}
		</Button>
	</div>
{/if}

{#if !loading && attendsPredefined && hasRules === false}
	<div class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300" role="status">
		Aún no tienes horarios de disponibilidad: nadie puede reservar contigo hasta que los definas.
		<a href="{base}/availability" class="font-medium underline">Definir mi disponibilidad</a>
	</div>
{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else if listed.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">Aún no hay tipos de atención</p>
		<p class="mt-1 text-sm text-muted-foreground">Crea tu primer tipo de atención para empezar a aceptar reservas.</p>
	</div>
{:else}
	<div class="mb-4 inline-flex rounded-md border p-0.5 text-sm">
		<button type="button" class="rounded px-3 py-1 transition-colors {filter === 'active' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (filter = 'active')}>Activos ({activeCount})</button>
		<button type="button" class="rounded px-3 py-1 transition-colors {filter === 'archived' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (filter = 'archived')}>Archivados ({archivedCount})</button>
	</div>
	{#if visible.length === 0}
		<div class="rounded-lg border border-dashed bg-card p-12 text-center">
			<p class="text-sm text-muted-foreground">No hay tipos de atención {filter === 'archived' ? 'archivados' : 'activos'}.</p>
		</div>
	{:else}
	<!-- Fork: one card per type (the old 5-column table clipped the switch and the actions
	     at 375 px). Actions carry text: a Tooltip does not open on touch. -->
	<div class="overflow-hidden rounded-lg border bg-card">
		<ul class="divide-y">
			{#each visible as et (et.id)}
				{@const kind = et.team?.kind}
				{@const readOnly = et.owned === false}
				<li class="space-y-3 p-4 transition-colors hover:bg-muted/20">
					<div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
						<div class="min-w-0 flex-1 space-y-1">
							<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
								<p class="min-w-0 break-words font-medium">{et.name}</p>
								{#if kind && readOnly}
									<Badge variant="secondary" class="text-[10px]">Predefinido por el propietario</Badge>
								{:else if kind === 'mentoria_template'}
									<Badge variant="secondary" class="text-[10px]">Plantilla de Mentoría</Badge>
								{:else if kind === 'soporte_shared'}
									<Badge variant="secondary" class="text-[10px]">Soporte compartido</Badge>
								{:else if readOnly}
									<Badge variant="secondary" class="text-[10px]">Eres anfitrión</Badge>
								{/if}
								{#if !et.is_active && !et.archived}
									<Badge variant="outline" class="text-[10px] text-muted-foreground">Inactivo</Badge>
								{/if}
							</div>
							<p class="break-all text-xs text-muted-foreground">/book/{et.slug} · {et.duration_minutes} min</p>

							{#if kind === 'mentoria_template' && !readOnly}
								{@const n = copiesOf(et)}
								{@const links = copyLinksOf(et)}
								<p class="text-sm text-muted-foreground">
									{n === 1 ? '1 copia (una por mentor)' : `${n} copias (una por mentor)`}. Las copias siguen los cambios de esta plantilla.
								</p>
								{#if links.length > 0}
									<button type="button" class="text-xs font-medium text-primary underline-offset-2 hover:underline" aria-expanded={!!openCopies[et.id]} onclick={() => (openCopies = { ...openCopies, [et.id]: !openCopies[et.id] })}>
										{openCopies[et.id] ? 'Ocultar enlaces de los mentores' : 'Ver enlaces de los mentores'}
									</button>
									{#if openCopies[et.id]}
										<ul class="mt-1 space-y-1.5">
											{#each links as l (l.slug)}
												<li class="flex flex-col gap-1 rounded-md border px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
													<div class="min-w-0">
														<p class="text-sm font-medium">{l.mentor_name}{#if !l.active}<span class="ml-1.5 text-xs font-normal text-muted-foreground">(inactiva)</span>{/if}</p>
														<p class="break-all text-xs text-muted-foreground">{l.url}</p>
													</div>
													<Button variant="outline" size="sm" class="self-start sm:self-auto" onclick={() => copyLink(l.url)}>Copiar enlace</Button>
												</li>
											{/each}
										</ul>
									{/if}
								{/if}
							{:else if kind === 'soporte_shared' && !readOnly}
								{@const soporteHosts = soporteHostsOf(et)}
								<p class="text-sm text-muted-foreground">
									{#if !et.team?.hosts && !settings}
										Se reparte entre el personal de soporte (se asigna en Miembros).
									{:else if soporteHosts.length > 1}
										Se reparte por turnos entre: {soporteHosts.map((h) => h.name).join(', ')}.
									{:else if soporteHosts.length === 1}
										Lo atiende {soporteHosts[0].name}.
									{:else}
										Nadie tiene el área Soporte: lo atiendes tú. Asígnala en Miembros.
									{/if}
								</p>
							{:else if kind && readOnly}
								<p class="text-sm text-muted-foreground">
									{kind === 'soporte_shared'
										? 'Se reparte por turnos entre el personal de soporte. Lo configura el propietario.'
										: 'Tu enlace personal. Lo configura el propietario; tú defines tu disponibilidad.'}
								</p>
							{/if}
						</div>

						<!-- Active switch: only on what this person edits (a copy follows its template). -->
						{#if !readOnly}
							<label class="flex shrink-0 items-center gap-2 text-sm">
								<Switch bind:checked={et.is_active} onCheckedChange={(v) => saveActive(et, v)} disabled={et.archived} aria-label="Activo" />
								<span class="text-muted-foreground">Activo</span>
							</label>
						{/if}
					</div>

					<div class="flex flex-wrap items-center gap-1.5">
						<Button variant="outline" size="sm" onclick={() => copyLink(bookLink(et.slug))}>Copiar enlace</Button>
						<Button variant="ghost" size="sm" href={bookLink(et.slug)} target="_blank" rel="noopener noreferrer">Abrir página</Button>
						<Button variant="ghost" size="sm" href="{base}/event-types/{et.slug}">{readOnly ? 'Ver detalles' : 'Configurar'}</Button>
						{#if !readOnly}
							<Button variant="ghost" size="sm" onclick={() => duplicateEventType(et)} disabled={duplicating === et.slug}>
								{duplicating === et.slug ? 'Duplicando…' : 'Duplicar'}
							</Button>
							<Button variant="ghost" size="sm" onclick={() => askArchive(et)}>
								{et.archived ? 'Restaurar' : 'Archivar'}
							</Button>
							<!-- Fork: the server refuses deleting T while it has copies and S while it is
							     the Soporte type (409), so the button is not offered then. -->
							{#if !(kind === 'mentoria_template' && copiesOf(et) > 0) && kind !== 'soporte_shared'}
								<Button variant="ghost" size="sm" class="text-destructive hover:text-destructive" onclick={() => del(et.slug)}>Eliminar</Button>
							{/if}
						{/if}
					</div>
				</li>
			{/each}
		</ul>
	</div>
	{/if}
{/if}
