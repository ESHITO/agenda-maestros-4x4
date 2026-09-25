<!--
  Fork (Agenda Maestros 4x4): the WhatsApp texts of one event type, one per moment.

  The agenda composes the message and sends it as data.whatsapp_message; FunnelChat only
  forwards it. Texts are saved with PUT /v1/event-types/{slug}/whatsapp-messages (an empty
  box = the built-in default, shown as the placeholder). "Ver ejemplo" asks the SERVER to
  render them (POST .../preview): the markers are never filled in here, so the example is
  exactly what a client would get, dropped lines included.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { api, type WhatsAppMessages, type WhatsAppMoment, type WhatsAppPreview } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { toast } from 'svelte-sonner';

	let { slug }: { slug: string } = $props();

	const MOMENTS: { key: WhatsAppMoment; label: string; when: string }[] = [
		{ key: 'created', label: 'Confirmación', when: 'En cuanto el cliente agenda.' },
		{ key: 'reminder_morning', label: 'Recordatorio de la mañana', when: 'El día de la sesión, a la hora fijada en Webhooks.' },
		{ key: 'reminder_1h', label: '1 hora antes', when: 'Una hora antes de empezar.' },
		{ key: 'reminder_5m', label: '5 minutos antes', when: 'Cinco minutos antes de empezar.' },
		{ key: 'cancelled', label: 'Cancelación', when: 'Cuando se cancela la sesión.' },
		{ key: 'rescheduled', label: 'Reprogramación', when: 'Cuando cambia la fecha o la persona que atiende.' }
	];

	const MARKERS: { key: string; help: string }[] = [
		{ key: '{nombre}', help: 'Nombre del cliente' },
		{ key: '{mentor}', help: 'Quién atiende la sesión' },
		{ key: '{tipo}', help: 'Nombre de este tipo de atención' },
		{ key: '{tema}', help: 'Lo que el cliente respondió en la primera pregunta de texto (por ejemplo «¿Qué te gustaría hablar en esta sesión?»)' },
		{ key: '{fecha}', help: 'Fecha y hora completas, en la hora del cliente: «martes 30 de septiembre de 2026, 10:00»' },
		{ key: '{dia}', help: 'Día en palabras, en la hora del cliente: «martes 30 de septiembre»' },
		{ key: '{hora}', help: 'Solo la hora, en la hora del cliente: «10:00»' },
		{ key: '{enlace}', help: 'Enlace para que el cliente entre a la sesión' },
		{ key: '{cancelar}', help: 'Enlace para que el cliente cancele o cambie la fecha' },
		{ key: '{motivo}', help: 'Motivo de la cancelación (solo en «Cancelación»)' }
	];

	const emptyTexts = (): Record<WhatsAppMoment, string> => ({
		created: '', reminder_morning: '', reminder_1h: '', reminder_5m: '', cancelled: '', rescheduled: ''
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

	const dirty = $derived(MOMENTS.some((m) => texts[m.key].trim() !== saved[m.key].trim()));

	// The box a marker chip inserts into: the last one focused (else the first). Every key
	// starts as null, not undefined: bind:ref={undefined} on a prop with a fallback throws.
	let areas = $state<Record<WhatsAppMoment, HTMLTextAreaElement | null>>({
		created: null, reminder_morning: null, reminder_1h: null, reminder_5m: null, cancelled: null, rescheduled: null
	});
	let lastFocused = $state<WhatsAppMoment>('created');

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

	async function insertMarker(marker: string) {
		const key = lastFocused;
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

		<div>
			<p class="mb-2 text-sm font-medium">Datos que puedes usar <span class="font-normal text-muted-foreground">— toca uno para añadirlo donde está el cursor</span></p>
			<ul class="grid gap-1.5 sm:grid-cols-2">
				{#each MARKERS as mk (mk.key)}
					<li class="flex items-start gap-2 text-sm">
						<button
							type="button"
							class="shrink-0 rounded-md border bg-background px-2 py-0.5 font-mono text-xs transition-colors hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
							onclick={() => insertMarker(mk.key)}
							aria-label={`Añadir ${mk.key}: ${mk.help}`}
						>{mk.key}</button>
						<span class="min-w-0 text-muted-foreground">{mk.help}</span>
					</li>
				{/each}
			</ul>
		</div>

		{#if loading}
			<p class="text-sm text-muted-foreground">Cargando mensajes…</p>
		{:else if loadError}
			<div class="space-y-2">
				<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{loadError}</p>
				<Button variant="outline" size="sm" onclick={load}>Reintentar</Button>
			</div>
		{:else}
			<div class="space-y-5">
				{#each MOMENTS as m, i (m.key)}
					<div class="space-y-1.5">
						<div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
							<Label for="wa-{m.key}" class="text-sm font-medium">{i + 1}. {m.label}</Label>
							<span class="text-xs text-muted-foreground">{m.when}</span>
						</div>
						<Textarea
							id="wa-{m.key}"
							bind:ref={areas[m.key]}
							bind:value={texts[m.key]}
							onfocus={() => (lastFocused = m.key)}
							onpointerdown={() => (lastFocused = m.key)}
							oninput={() => (lastFocused = m.key)}
							rows={6}
							maxlength={3000}
							placeholder={defaults[m.key]}
							class="min-h-32 resize-y font-sans text-base sm:text-sm"
						/>
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
				{/each}
			</div>

			<div class="flex flex-col gap-2 border-t pt-4 sm:flex-row sm:items-center sm:justify-end">
				{#if dirty}<span class="text-xs text-amber-700 sm:mr-auto">Tienes cambios sin guardar.</span>{/if}
				<Button type="button" variant="outline" onclick={showPreview} disabled={previewing}>
					{previewing ? 'Generando ejemplo…' : 'Ver ejemplo'}
				</Button>
				<Button type="button" onclick={save} disabled={saving || !dirty}>
					{saving ? 'Guardando…' : 'Guardar mensajes'}
				</Button>
			</div>

			{#if previewError}
				<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{previewError}</p>
			{/if}
			{#if preview}
				<div class="space-y-3 rounded-md border bg-muted/30 p-3 sm:p-4">
					<p class="text-xs text-muted-foreground">
						Ejemplo con datos ficticios: cliente «María Pérez», sesión mañana a las 10:00
						(hora de {preview.timezone.replaceAll('_', ' ')}), atendida por ti. Así lo recibiría el cliente.
						{#if !preview.has_text_question}
							Este tipo de atención no tiene preguntas de texto, así que <code class="font-mono">{'{tema}'}</code> siempre queda vacío y su línea no se envía.
						{/if}
					</p>
					{#each MOMENTS as m, i (m.key)}
						<div>
							<p class="mb-1 text-xs font-semibold text-muted-foreground">{i + 1}. {m.label}</p>
							<div class="max-w-md rounded-lg rounded-tl-none border border-green-200 bg-green-50 px-3 py-2 text-sm leading-relaxed break-words whitespace-pre-wrap text-foreground dark:border-green-900 dark:bg-green-950/40">{preview[m.key] || '(mensaje vacío: no se enviará texto)'}</div>
						</div>
					{/each}
				</div>
			{/if}
		{/if}
	</div>
</div>
