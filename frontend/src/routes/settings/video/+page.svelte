<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	type LiveKitSettings = {
		url: string;
		api_key: string;
		api_secret_set: boolean;
		configured: boolean;
	};
	type NotetakerSettings = { enabled: boolean; stt_api_key_set: boolean };
	type StorageStatus = { recordings_storage_ready: boolean; recordings_enabled: boolean };

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();

	let settings = $state<LiveKitSettings | null>(null);
	let url = $state('');
	let apiKey = $state('');
	let apiSecret = $state('');
	let webhookUrl = $state('');

	let notetaker = $state<NotetakerSettings | null>(null);
	let notetakerEnabled = $state(false);
	let deepgramKey = $state('');

	let storage = $state<StorageStatus | null>(null);
	// Only worth surfacing once LiveKit itself works — there's nothing to record yet otherwise.
	const recordingNotReady = $derived(
		settings?.configured && storage !== null && (!storage.recordings_storage_ready || !storage.recordings_enabled)
	);

	onMount(() => loadingFlag.run(async () => {
		webhookUrl = `${window.location.origin}/v1/livekit/webhook`;
		settings = await api.get<LiveKitSettings>('/v1/settings/livekit');
		url = settings.url;
		apiKey = settings.api_key;
		notetaker = await api.get<NotetakerSettings>('/v1/settings/notetaker');
		notetakerEnabled = notetaker.enabled;
		storage = await api.get<StorageStatus>('/v1/settings/storage');
	}, 'No se pudo cargar la configuración de video'));

	async function saveNotetaker() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { enabled: notetakerEnabled };
			if (deepgramKey.trim()) body.stt_api_key = deepgramKey.trim();
			notetaker = await api.patch<NotetakerSettings>('/v1/settings/notetaker', body);
			notetakerEnabled = notetaker.enabled;
			deepgramKey = '';
			toast.success('Configuración del notetaker guardada');
		}, 'No se pudo guardar la configuración del notetaker');
	}

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { url: url.trim(), api_key: apiKey.trim() };
			if (apiSecret) body.api_secret = apiSecret;
			settings = await api.patch<LiveKitSettings>('/v1/settings/livekit', body);
			apiSecret = '';
			toast.success('Guardado — "Calnode Video (LiveKit)" ahora se puede seleccionar como ubicación en los tipos de atención');
		}, 'No se pudo guardar la configuración de video');
	}

	async function copyWebhook() {
		try {
			await navigator.clipboard.writeText(webhookUrl);
			toast.success('URL del webhook copiada');
		} catch {
			toast.error('No se pudo copiar — selecciona y copia manualmente');
		}
	}

	async function disconnect() {
		await savingFlag.run(async () => {
			settings = await api.patch<LiveKitSettings>('/v1/settings/livekit', { url: '' });
			url = ''; apiKey = ''; apiSecret = '';
			toast.success('LiveKit desconectado');
		}, 'No se pudo desconectar');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Se requiere acceso de administrador.</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="max-w-lg space-y-4">
		{#if !settings?.configured}
			<div class="rounded-lg border bg-card p-6">
				<h2 class="mb-4 text-sm font-semibold">Instrucciones de configuración</h2>
				<ol class="space-y-4 text-sm">
					<li class="flex gap-3">
						<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
						<div>
							Crea un proyecto gratuito en <a href="https://cloud.livekit.io" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">cloud.livekit.io</a>
							(o ejecuta tu propio <a href="https://docs.livekit.io/home/self-hosting/local/" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">servidor autoalojado</a> — mismos campos).
						</div>
					</li>
					<li class="flex gap-3">
						<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
						<div>
							Copia la <span class="font-medium">URL de WebSocket</span> del proyecto (se ve como
							<code class="rounded bg-muted px-1 text-xs">wss://yourproject.livekit.cloud</code>) y una
							<span class="font-medium">clave</span> de API + un <span class="font-medium">secreto</span>.
						</div>
					</li>
					<li class="flex gap-3">
						<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
						<div>Pégalos abajo y guarda. Las reservas que usen "Calnode Video (LiveKit)" obtendrán entonces una sala integrada — sin necesidad de conexión por anfitrión.</div>
					</li>
				</ol>
			</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Calnode Video (LiveKit)</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Videollamadas integradas alojadas en tu servidor de LiveKit. Cada reserva obtiene un enlace
						de sala; los invitados se unen desde el navegador — sin necesidad de cuenta ni aplicación.
					</p>
				</div>
				{#if settings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {settings.configured ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {settings.configured ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{settings.configured ? 'Configurado' : 'No configurado'}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="lk-url">URL del servidor</Label>
					<Input id="lk-url" type="text" placeholder="wss://yourproject.livekit.cloud" bind:value={url} />
				</div>
				<div class="space-y-1.5">
					<Label for="lk-key">Clave de API</Label>
					<Input id="lk-key" type="text" placeholder="APIxxxxxxxx" bind:value={apiKey} />
				</div>
				<div class="space-y-1.5">
					<Label for="lk-secret">Secreto de API</Label>
					<Input id="lk-secret" type="password"
						placeholder={settings?.api_secret_set ? '•••••••• (guardado)' : 'Ingresa el secreto de API'}
						bind:value={apiSecret} />
					{#if settings?.api_secret_set && !apiSecret}
						<p class="text-xs text-muted-foreground">Guardado — déjalo en blanco para conservarlo.</p>
					{/if}
				</div>
			</div>

			<div class="mt-5 flex items-center gap-3">
				<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Guardando…' : 'Guardar'}</Button>
				{#if settings?.configured}
					<Button variant="ghost" onclick={disconnect} disabled={savingFlag.active}>Desconectar</Button>
				{/if}
			</div>
		</div>

		{#if recordingNotReady}
			<div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
				{#if !storage?.recordings_storage_ready}
					<p>Las grabaciones de reuniones necesitan que primero se configure el almacenamiento de objetos.</p>
				{:else}
					<p>El almacenamiento de objetos está listo — activa las grabaciones para que los anfitriones puedan grabar reuniones.</p>
				{/if}
				<a href="/admin/settings/storage" class="mt-1 inline-block font-medium text-amber-900 underline underline-offset-2">
					Ir a Configuración → Almacenamiento
				</a>
			</div>
		{/if}

		{#if settings?.configured}
			<div class="rounded-lg border bg-card p-6">
				<h2 class="text-sm font-semibold">Webhook (recomendado)</h2>
				<p class="mt-0.5 text-xs text-muted-foreground">
					Registra esta URL en LiveKit para que las grabaciones finalicen con la duración correcta y
					terminen limpiamente cuando se cierra una reunión. Las grabaciones igual funcionan sin esto
					— solo las hace más confiables.
				</p>
				<div class="mt-3 flex items-center gap-2">
					<code class="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1.5 text-xs">{webhookUrl}</code>
					<Button variant="outline" size="sm" onclick={copyWebhook}>Copiar</Button>
				</div>
				<ol class="mt-4 space-y-2 text-xs text-muted-foreground">
					<li>
						1. En LiveKit Cloud, abre <span class="font-medium">Project → Settings → Webhooks</span>
						(<a href="https://docs.livekit.io/home/server/webhooks/" target="_blank" rel="noopener noreferrer" class="text-primary underline">documentación</a>).
					</li>
					<li>2. Agrega un webhook con la URL de arriba, y adjunta la <span class="font-medium">misma clave de API</span> que ingresaste aquí — con ella se firman los eventos para que Calnode pueda verificarlos.</li>
					<li>3. Guarda. LiveKit envía todos los eventos del proyecto a esta única URL; Calnode verifica cada firma y usa los relacionados con grabaciones.</li>
				</ol>
			</div>

			<div class="rounded-lg border bg-card p-6">
				<div class="mb-3 flex items-start justify-between gap-3">
					<div>
						<h2 class="text-sm font-semibold">Notas de reunión con IA (notetaker)</h2>
						<p class="mt-0.5 text-xs text-muted-foreground">
							Después de grabar una reunión, la transcribe y la resume en notas sobre la reserva
							(usando tu LLM configurado). Necesita grabación activada, un LLM configurado y una
							clave de Deepgram.
						</p>
					</div>
					<Switch bind:checked={notetakerEnabled} />
				</div>
				<div class="space-y-1.5">
					<Label for="dg-key">Clave de API de Deepgram</Label>
					<Input id="dg-key" type="password"
						placeholder={notetaker?.stt_api_key_set ? '•••••••• (guardado)' : 'Ingresa la clave de API de Deepgram'}
						bind:value={deepgramKey} />
					<p class="text-xs text-muted-foreground">
						Consíguela en <a href="https://console.deepgram.com" target="_blank" rel="noopener noreferrer" class="text-primary underline">console.deepgram.com</a>.{#if notetaker?.stt_api_key_set} Guardado — déjalo en blanco para conservarlo.{/if}
					</p>
				</div>
				<div class="mt-5">
					<Button onclick={saveNotetaker} disabled={savingFlag.active}>{savingFlag.active ? 'Guardando…' : 'Guardar'}</Button>
				</div>
			</div>
		{/if}
	</div>
{/if}
