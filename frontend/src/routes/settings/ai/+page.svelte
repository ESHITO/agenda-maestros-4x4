<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type LLMSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	const testingFlag = createAsyncFlag();

	let settings = $state<LLMSettings | null>(null);
	let enabled = $state(false);
	let endpoint = $state('');
	let model = $state('');
	let apiKey = $state('');
	let extraInstructions = $state('');

	onMount(() => loadingFlag.run(async () => {
		settings = await api.get<LLMSettings>('/v1/settings/llm');
		enabled = settings.enabled;
		endpoint = settings.endpoint;
		model = settings.model;
		extraInstructions = settings.extra_instructions;
	}, 'No se pudo cargar la configuración de IA'));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = {
				enabled, endpoint: endpoint.trim(), model: model.trim(),
				extra_instructions: extraInstructions
			};
			if (apiKey) body.api_key = apiKey;
			settings = await api.patch<LLMSettings>('/v1/settings/llm', body);
			enabled = settings.enabled;
			extraInstructions = settings.extra_instructions;
			apiKey = '';
			toast.success(settings.active ? 'Guardado — la IA está activa' : 'Guardado');
		}, 'No se pudo guardar la configuración de IA');
	}

	async function testConnection() {
		if (!endpoint.trim() || !model.trim()) {
			toast.error('Ingresa un endpoint y un modelo primero');
			return;
		}
		await testingFlag.run(async () => {
			const res = await api.post<{ ok: boolean; latency_ms?: number; error?: string }>(
				'/v1/settings/llm/test',
				{ endpoint: endpoint.trim(), model: model.trim(), api_key: apiKey || undefined }
			);
			if (res.ok) toast.success(`Conexión exitosa (${res.latency_ms} ms)`);
			else toast.error(`Prueba fallida: ${res.error}`);
		}, 'La solicitud de prueba falló');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Se requiere acceso de administrador.</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Asistente de IA</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Habilita la reserva conversacional en tus páginas de reserva. Usa tu propio
						modelo — cualquier endpoint compatible con OpenAI (un modelo alojado, o uno
						que tú mismo ejecutes). Desactivado por defecto; si no está disponible,
						quienes reservan simplemente usan el calendario.
					</p>
				</div>
				{#if settings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {settings.active ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {settings.active ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{settings.active ? 'Activo' : settings.configured ? 'Configurado (desactivado)' : 'No configurado'}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="ai-endpoint">Endpoint de la API</Label>
					<Input id="ai-endpoint" type="text" placeholder="https://api.openai.com/v1" bind:value={endpoint} />
					<p class="text-xs text-muted-foreground">URL base compatible con OpenAI; el cliente añade <code class="rounded bg-muted px-1">/chat/completions</code>.</p>
				</div>
				<div class="space-y-1.5">
					<Label for="ai-model">Modelo</Label>
					<Input id="ai-model" type="text" placeholder="gpt-4o-mini / claude-haiku-4-5 / gemini-flash …" bind:value={model} />
				</div>
				<div class="space-y-1.5">
					<Label for="ai-key">Clave de API</Label>
					<Input id="ai-key" type="password"
						placeholder={settings?.api_key_set ? '•••••••• (guardada)' : 'Ingresa la clave de API (opcional para endpoints locales)'}
						bind:value={apiKey} />
					{#if settings?.api_key_set && !apiKey}
						<p class="text-xs text-muted-foreground">Guardada — déjalo en blanco para conservarla.</p>
					{/if}
				</div>

				<div class="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2">
					<div>
						<Label for="ai-enabled" class="text-sm">Activar funciones de IA</Label>
						<p class="text-xs text-muted-foreground">Activa la reserva conversacional en tus páginas de reserva.</p>
					</div>
					<Switch id="ai-enabled" bind:checked={enabled} />
				</div>
			</div>

			<div class="mt-5 flex gap-2">
				<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Guardando…' : 'Guardar'}</Button>
				<Button variant="outline" onclick={testConnection} disabled={testingFlag.active}>
					{testingFlag.active ? 'Probando…' : 'Probar conexión'}
				</Button>
			</div>
		</div>

		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Instrucciones del asistente</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Indicaciones adicionales — tono, contexto del negocio, qué hacer y qué no. Se
				agregan a las instrucciones integradas (que gestionan el flujo de reserva y la
				seguridad, y no se pueden editar).
			</p>
			<div class="mt-3 space-y-1.5">
				<Label for="ai-extra">Instrucciones adicionales</Label>
				<Textarea id="ai-extra" rows={4} bind:value={extraInstructions}
					placeholder="ej. Mantén un tono cálido y profesional. Somos un despacho de abogados — menciona que las consultas son confidenciales." />
				<p class="text-xs text-muted-foreground">
					El asistente responde automáticamente en el idioma de cada visitante. Escribir
					estas instrucciones en un idioma distinto al de tus visitantes funciona, pero
					es algo menos confiable — para mejores resultados, escríbelas en el idioma que
					use la mayoría de tus visitantes.
				</p>
			</div>
			{#if settings?.base_prompt}
				<details class="mt-4">
					<summary class="cursor-pointer text-xs font-medium text-muted-foreground">Ver instrucciones base integradas (solo lectura)</summary>
					<pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">{settings.base_prompt}</pre>
				</details>
			{/if}
			<div class="mt-5">
				<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Guardando…' : 'Guardar'}</Button>
			</div>
		</div>

		<p class="text-xs text-muted-foreground">
			Privacidad: el asistente solo recibe ventanas de disponibilidad calculadas y
			detalles públicos del tipo de atención — nunca los títulos de tus eventos de
			calendario, asistentes u otros datos privados. Para necesidades estrictas de
			residencia de datos, apunta el endpoint a una inferencia que controles.
		</p>
	</div>
{/if}
