<!--
  Fork (Agenda Maestros 4x4): the WhatsApp texts of one event type, one per moment.

  The agenda composes the message and sends it as data.whatsapp_message (to the client) or
  data.host_whatsapp_message (to the HOST - the mentor or support person attending, the
  "Avisos al mentor o soporte" section, internal/webhook/fork_host.go); FunnelChat only
  forwards it. All eight texts are saved together with PUT
  /v1/event-types/{slug}/whatsapp-messages (an empty box = the built-in default, shown as the
  placeholder). "Ver ejemplo" asks the SERVER to render them (POST .../preview): the markers
  are never filled in here, so the example is exactly what would be sent, dropped lines
  included. Each section has its own marker chips: a client text cannot use the host's link
  and a host text cannot use the client's (lib/host-notices.ts mirrors the server's check).
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { api, type WhatsAppMessages, type WhatsAppMoment, type WhatsAppPreview } from '$lib/api';
	import { CLIENT_MARKERS, HOST_MARKERS, isHostMoment, markerMisuse, type MarkerDef } from '$lib/host-notices';
	import { Button } from '$lib/components/ui/button';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { toast } from 'svelte-sonner';

	let { slug }: { slug: string } = $props();

	type MomentDef = { key: WhatsAppMoment; label: string; when: string };

	const CLIENT: MomentDef[] = [
		{ key: 'created', label: 'Confirmación', when: 'En cuanto el cliente agenda.' },
		{ key: 'reminder_morning', label: 'Recordatorio de la mañana', when: 'El día de la sesión, a la hora fijada en Webhooks.' },
		{ key: 'reminder_1h', label: '1 hora antes', when: 'Una hora antes de empezar.' },
		{ key: 'reminder_5m', label: '5 minutos antes', when: 'Cinco minutos antes de empezar.' },
		{ key: 'cancelled', label: 'Cancelación', when: 'Cuando se cancela la sesión.' },
		{ key: 'rescheduled', label: 'Reprogramación', when: 'Cuando cambia la fecha o la persona que atiende.' }
	];
	const HOST: MomentDef[] = [
		{ key: 'host_created', label: 'Nueva sesión agendada', when: 'En cuanto el cliente agenda, o cuando le pasan la sesión a otra persona.' },
		{ key: 'host_reminder_5m', label: 'Faltan 5 minutos', when: 'Cinco minutos antes de empezar, con su enlace para entrar.' }
	];
	const MOMENTS: MomentDef[] = [...CLIENT, ...HOST];

	const emptyTexts = (): Record<WhatsAppMoment, string> => ({
		created: '', reminder_morning: '', reminder_1h: '', reminder_5m: '', cancelled: '', rescheduled: '',
		host_created: '', host_reminder_5m: ''
	});

	let texts = $state<Record<WhatsAppMoment, string>>(emptyTexts());
	let saved = $state<Record<WhatsAppMoment, string>>(emptyTexts());
	let defaults = $state<Record<WhatsAppMoment, string>>(emptyTexts());
	let loading = $state(true);
	let loadError = $state('');
	let saving = $state(false);
	let previewing = $state(false);
	let preview = $state<WhatsAppPreview | null>(null);
	let previewError = $state('');

	const changed = (k: WhatsAppMoment) => texts[k].trim() !== saved[k].trim();
	const dirty = $derived(MOMENTS.some((m) => changed(m.key)));
	// A marker outside its audience, only in texts that changed (the server checks the same,
	// "validate on change": a stored text is never blamed for an unrelated save).
	const misuse = $derived(
		Object.fromEntries(MOMENTS.map((m) => [m.key, changed(m.key) ? markerMisuse(m.key, texts[m.key]) : ''])) as Record<WhatsAppMoment, string>
	);
	const hasMisuse = $derived(MOMENTS.some((m) => misuse[m.key] !== ''));

	// The box a marker chip inserts into: the last one focused in that chip's section (else
	// the section's first). Every key starts as null, not undefined: bind:ref={undefined} on a
	// prop with a fallback throws.
	let areas = $state<Record<WhatsAppMoment, HTMLTextAreaElement | null>>({
		created: null, reminder_morning: null, reminder_1h: null, reminder_5m: null, cancelled: null, rescheduled: null,
		host_created: null, host_reminder_5m: null
	});
	let lastClient = $state<WhatsAppMoment>('created');
	let lastHost = $state<WhatsAppMoment>('host_created');
	function focused(key: WhatsAppMoment) {
		if (isHostMoment(key)) lastHost = key;
		else lastClient = key;
	}

	function apply(res: WhatsAppMessages) {
		const t = emptyTexts();
		for (const m of MOMENTS) t[m.key] = res[m.key] ?? '';
		texts = { ...t };
		saved = { ...t };
		defaults = { ...emptyTexts(), ...(res.defaults ?? {}) };
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			apply(await api.get<WhatsAppMessages>(`/v1/event-types/${slug}/whatsapp-messages`));
		} catch (e: any) {
			loadError = e.message || 'No se pudieron cargar los mensajes.';
		} finally {
			loading = false;
		}
	}

	async function save() {
		// Never after a failed load: the boxes would be empty and saving would wipe the texts.
		if (loadError || loading) return;
		if (hasMisuse) {
			toast.error('Corrige los datos marcados en rojo antes de guardar.');
			return;
		}
		saving = true;
		try {
			apply(await api.put<WhatsAppMessages>(`/v1/event-types/${slug}/whatsapp-messages`, texts));
			toast.success('Mensajes de WhatsApp guardados');
		} catch (e: any) {
			toast.error(e.message || 'No se pudieron guardar los mensajes');
		} finally {
			saving = false;
		}
	}

	// Ctrl/Cmd+S on this tab (the page routes the shortcut here instead of saving the event
	// type, which would say "Cambios guardados" while these texts stayed unsaved).
	export function saveFromShortcut() {
		if (!saving && dirty) save();
	}

	async function showPreview() {
		previewing = true;
		previewError = '';
		try {
			preview = await api.post<WhatsAppPreview>(`/v1/event-types/${slug}/whatsapp-messages/preview`, texts);
		} catch (e: any) {
			previewError = e.message || 'No se pudo generar el ejemplo.';
		} finally {
			previewing = false;
		}
	}

	async function insertMarker(marker: string, host: boolean) {
		const key = host ? lastHost : lastClient;
		const el = areas[key];
		const value = texts[key];
		const startPos = el?.selectionStart ?? value.length;
		const endPos = el?.selectionEnd ?? value.length;
		texts[key] = value.slice(0, startPos) + marker + value.slice(endPos);
		await tick();
		if (el) {
			el.focus();
			const caret = startPos + marker.length;
			el.setSelectionRange(caret, caret);
		}
	}

	function useDefault(key: WhatsAppMoment) {
		texts[key] = defaults[key];
	}

	onMount(load);
</script>

{#snippet markerList(list: MarkerDef[], host: boolean)}
	<div>
		<p class="mb-2 text-sm font-medium">Datos que puedes usar <span class="font-normal text-muted-foreground">— toca uno para añadirlo donde está el cursor</span></p>
		<ul class="grid gap-1.5 sm:grid-cols-2">
			{#each list as mk (mk.key)}
				<li class="flex items-start gap-2 text-sm">
					<button
						type="button"
						class="shrink-0 rounded-md border bg-background px-2 py-0.5 font-mono text-xs transition-colors hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
						onclick={() => insertMarker(mk.key, host)}
						aria-label={`Añadir ${mk.key}: ${mk.help}`}
					>{mk.key}</button>
					<span class="min-w-0 text-muted-foreground">{mk.help}</span>
				</li>
			{/each}
		</ul>
	</div>
{/snippet}

{#snippet momentBox(m: MomentDef, n: number)}
	<div class="space-y-1.5">
		<div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
			<Label for="wa-{m.key}" class="text-sm font-medium">{n}. {m.label}</Label>
			<span class="text-xs text-muted-foreground">{m.when}</span>
		</div>
		<Textarea
			id="wa-{m.key}"
			bind:ref={areas[m.key]}
			bind:value={texts[m.key]}
			onfocus={() => focused(m.key)}
			onpointerdown={() => focused(m.key)}
			oninput={() => focused(m.key)}
			rows={isHostMoment(m.key) ? 9 : 6}
			maxlength={3000}
			placeholder={defaults[m.key]}
			aria-invalid={misuse[m.key] ? true : undefined}
			aria-describedby={misuse[m.key] ? `wa-${m.key}-misuse` : undefined}
			class="min-h-32 resize-y font-sans text-base sm:text-sm"
		/>
		{#if misuse[m.key]}
			<p id="wa-{m.key}-misuse" class="text-xs text-destructive" role="alert">{misuse[m.key]}</p>
		{/if}
		<div class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
			<span>
				{#if texts[m.key].trim() === ''}
					Se enviará el texto por defecto.
				{:else}
					Texto propio · {texts[m.key].length} caracteres
				{/if}
			</span>
			{#if texts[m.key].trim() === ''}
				<Button type="button" variant="ghost" size="sm" onclick={() => useDefault(m.key)}>Editar a partir del texto por defecto</Button>
			{:else}
				<Button type="button" variant="ghost" size="sm" onclick={() => (texts[m.key] = '')}>Volver al texto por defecto</Button>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet bubble(m: MomentDef, n: number)}
	<div>
		<p class="mb-1 text-xs font-semibold text-muted-foreground">{n}. {m.label}</p>
		<div class="max-w-md rounded-lg rounded-tl-none border border-green-200 bg-green-50 px-3 py-2 text-sm leading-relaxed break-words whitespace-pre-wrap text-foreground dark:border-green-900 dark:bg-green-950/40">{preview?.[m.key] || '(mensaje vacío: no se enviará texto)'}</div>
	</div>
{/snippet}

<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Mensajes de WhatsApp</h2>
	<div class="space-y-6 rounded-lg border bg-card p-4 sm:p-6">
		<div class="space-y-2 text-sm text-muted-foreground">
			<p>
				La agenda escribe el mensaje de cada momento con los datos de la cita y lo envía listo a
				FunnelChat en el dato <code class="rounded bg-muted px-1 font-mono text-xs">whatsapp_message</code>.
				Para que llegue, el webhook de ese momento debe incluir «Mensaje de WhatsApp» (página Webhooks).
			</p>
			<p>
				Si dejas un recuadro vacío se envía el texto por defecto (el que ves en gris). Puedes usar
				<strong>*negrita*</strong>, <em>_cursiva_</em> y emojis como en WhatsApp.
				<span class="font-medium text-foreground">Si un dato queda vacío</span> (por ejemplo, el cliente no escribió el tema),
				se quita toda esa línea del mensaje.
			</p>
		</div>

		{#if loading}
			<p class="text-sm text-muted-foreground">Cargando mensajes…</p>
		{:else if loadError}
			<div class="space-y-2">
				<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{loadError}</p>
				<Button variant="outline" size="sm" onclick={load}>Reintentar</Button>
			</div>
		{:else}
			<section class="space-y-5" aria-labelledby="wa-client-title">
				<h3 id="wa-client-title" class="text-base font-semibold">Mensajes al cliente</h3>
				{@render markerList(CLIENT_MARKERS, false)}
				{#each CLIENT as m, i (m.key)}
					{@render momentBox(m, i + 1)}
				{/each}
			</section>

			<section class="space-y-5 border-t pt-6" aria-labelledby="wa-host-title">
				<div class="space-y-2">
					<h3 id="wa-host-title" class="text-base font-semibold">Avisos al mentor o soporte</h3>
					<p class="text-sm text-muted-foreground">
						Le llegan a la persona que atiende la sesión (el mentor, la persona de soporte o tú), al
						WhatsApp que tiene en su perfil o que le pusiste en Miembros. Quien no tiene número no recibe
						nada. La fecha y la hora van en <span class="font-medium text-foreground">su propia hora local</span>,
						con su país y su bandera: no tiene que convertir nada.
					</p>
					<p class="text-sm text-muted-foreground">
						Cada aviso necesita su propio webhook en la página Webhooks, con el evento «Aviso al anfitrión: …» y
						los datos «Mensaje para el anfitrión» y «WhatsApp del anfitrión». En FunnelChat, el número es
						<code class="rounded bg-muted px-1 font-mono text-xs">data.host_whatsapp</code> y el texto
						<code class="rounded bg-muted px-1 font-mono text-xs">data.host_whatsapp_message</code>.
					</p>
					<p class="text-xs text-muted-foreground">
						Puedes cambiar el título por el de este tipo de atención, por ejemplo «*Nueva Mentoría agendada*».
					</p>
				</div>
				{@render markerList(HOST_MARKERS, true)}
				{#each HOST as m, i (m.key)}
					{@render momentBox(m, i + 1)}
				{/each}
			</section>

			<div class="flex flex-col gap-2 border-t pt-4 sm:flex-row sm:items-center sm:justify-end">
				{#if hasMisuse}
					<span class="text-xs text-destructive sm:mr-auto">Hay datos que no sirven en ese mensaje (marcados en rojo).</span>
				{:else if dirty}
					<span class="text-xs text-amber-700 sm:mr-auto">Tienes cambios sin guardar.</span>
				{/if}
				<Button type="button" variant="outline" onclick={showPreview} disabled={previewing}>
					{previewing ? 'Generando ejemplo…' : 'Ver ejemplo'}
				</Button>
				<Button type="button" onclick={save} disabled={saving || !dirty || hasMisuse}>
					{saving ? 'Guardando…' : 'Guardar mensajes'}
				</Button>
			</div>

			{#if previewError}
				<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{previewError}</p>
			{/if}
			{#if preview}
				<div class="space-y-3 rounded-md border bg-muted/30 p-3 sm:p-4">
					<p class="text-xs text-muted-foreground">
						Ejemplo con datos ficticios: cliente «María Pérez», sesión mañana a las 10:00 a. m.
						(hora de {preview.timezone.replaceAll('_', ' ')}), atendida por ti. Así lo recibiría el cliente.
						{#if !preview.has_text_question}
							Este tipo de atención no tiene preguntas de texto, así que <code class="font-mono">{'{tema}'}</code> siempre queda vacío y su línea no se envía.
						{/if}
					</p>
					{#each CLIENT as m, i (m.key)}
						{@render bubble(m, i + 1)}
					{/each}
					<p class="border-t pt-3 text-xs text-muted-foreground">
						Así lo recibiría quien atiende la sesión. En el ejemplo la hora sale en tu zona horaria, como si
						atendieras tú; a cada persona le llega en la suya.
					</p>
					{#each HOST as m, i (m.key)}
						{@render bubble(m, i + 1)}
					{/each}
				</div>
			{/if}
		{/if}
	</div>
</div>
