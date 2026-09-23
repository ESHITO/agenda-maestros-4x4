<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type GoogleSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();

	let googleSettings = $state<GoogleSettings | null>(null);
	let clientID = $state('');
	let clientSecret = $state('');

	// Host the server builds its OAuth redirect URIs from. Prefer the server's
	// configured base_url so the displayed URIs match exactly what we send to
	// Google; fall back to the current origin if it's somehow blank.
	const redirectBase = $derived(
		googleSettings?.base_url || (typeof window !== 'undefined' ? window.location.origin : '')
	);
	const isLocal = $derived(redirectBase.includes('localhost') || redirectBase.includes('127.0.0.1'));

	// Catches the "moved to a custom domain but never updated BASE_URL" trap: the server
	// still computes redirect URIs from its own configured base_url, which can silently
	// drift from whatever domain an admin is actually browsing this page at (e.g. after
	// pointing a custom domain at a host whose BASE_URL secret still says the old default).
	const browserOrigin = $derived(typeof window !== 'undefined' ? window.location.origin : '');
	const originMismatch = $derived(
		!!googleSettings?.base_url && !!browserOrigin &&
		googleSettings.base_url.replace(/\/+$/, '') !== browserOrigin
	);

	onMount(() => loadingFlag.run(async () => {
		googleSettings = await api.get<GoogleSettings>('/v1/settings/google');
		clientID = googleSettings.client_id;
	}, 'No se pudo cargar la configuración de Google'));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { client_id: clientID };
			if (clientSecret) body.client_secret = clientSecret;
			googleSettings = await api.patch<GoogleSettings>('/v1/settings/google', body);
			clientSecret = '';
			toast.success('Guardado — ve a Calendario para conectar tu cuenta');
		}, 'No se pudo guardar la configuración de Google');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Se requiere acceso de administrador.</p>
{:else}

{#if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="max-w-lg space-y-4">

		{#if originMismatch}
			<div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
				<p class="font-medium">Esta página se está viendo desde un dominio distinto al configurado para Calnode</p>
				<p class="mt-1 text-amber-800">
					Estás navegando desde <code class="rounded bg-amber-100 px-1 font-mono">{browserOrigin}</code>, pero el
					<code class="rounded bg-amber-100 px-1 font-mono">BASE_URL</code> de este servidor está configurado como
					<code class="rounded bg-amber-100 px-1 font-mono">{googleSettings?.base_url}</code>. Los URIs de redirección
					de abajo se generan a partir de <code class="rounded bg-amber-100 px-1 font-mono">BASE_URL</code> —
					si <code class="rounded bg-amber-100 px-1 font-mono">{browserOrigin}</code> es tu dominio real
					(por ejemplo, después de apuntar un dominio personalizado a esta instancia), actualiza la
					variable de entorno/secreto <code class="rounded bg-amber-100 px-1 font-mono">BASE_URL</code> con
					tu proveedor de hosting y vuelve a desplegar, luego recarga esta página.
				</p>
			</div>
		{/if}

		{#if !googleSettings?.configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Instrucciones de configuración</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>
						Ve a <a href="https://console.cloud.google.com" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">console.cloud.google.com</a>.
						Si no tienes un proyecto, crea uno — cualquier nombre funciona.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>
						Ve a <span class="font-medium">API y servicios → Biblioteca</span>, busca
						<span class="font-medium">Google Calendar API</span> y actívala.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						Ve a <span class="font-medium">API y servicios → Pantalla de consentimiento de OAuth</span>.
						Elige <span class="font-medium">Externo</span> (o Interno si tienes Google Workspace).
						Completa el nombre de la app y tu correo, luego guarda.
						<p class="mt-1.5 text-xs text-muted-foreground">
							Mientras la app esté en <span class="font-medium">Prueba</span>, solo las cuentas de Google agregadas como usuarios de prueba podrán conectarse, y Google limita esto a 100 usuarios. Cuando tu equipo esté listo para conectar sus calendarios, haz clic en <span class="font-medium">Publicar app</span> para eliminar el límite (las apps Internas / de Workspace no tienen límite). Consulta la
							<a href="https://support.google.com/cloud/answer/15549945" target="_blank" rel="noopener noreferrer" class="text-primary underline">guía de Google</a>.
						</p>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>
						Ve a <span class="font-medium">Credenciales → Crear credenciales → ID de cliente de OAuth</span>.
						Configura el tipo de aplicación como <span class="font-medium">Aplicación web</span>.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">5</span>
					<div>
						En <span class="font-medium">URIs de redirección autorizados</span>, agrega ambos:
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/calendar/callback</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/auth/callback</code>
						Haz clic en <span class="font-medium">Crear</span>. Copia el ID de cliente y el Secreto de cliente que se muestran.
						{#if !isLocal}
							<p class="mt-1.5 text-xs text-muted-foreground">
								Si también ejecutas Calnode localmente, agrega también las
								variantes <code class="rounded bg-muted px-1">http://localhost:3000/…</code> de ambos URIs.
							</p>
						{/if}
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">6</span>
					<div>Pégalos en el formulario de abajo y guarda.</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Google OAuth</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">Habilita el inicio de sesión con Google y la integración con Google Calendar.</p>
				</div>
				{#if googleSettings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {googleSettings.configured ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {googleSettings.configured ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{googleSettings.configured ? 'Configurado' : 'No configurado'}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="g-client-id">ID de cliente</Label>
					<Input id="g-client-id" type="text" placeholder="123456789-abc.apps.googleusercontent.com" bind:value={clientID} />
				</div>
				<div class="space-y-1.5">
					<Label for="g-client-secret">Secreto de cliente</Label>
					<Input id="g-client-secret" type="password"
						placeholder={googleSettings?.client_secret_set ? '•••••••• (guardado)' : 'Ingresa el secreto de cliente'}
						bind:value={clientSecret} />
					{#if googleSettings?.client_secret_set && !clientSecret}
						<p class="text-xs text-muted-foreground">Guardado — déjalo en blanco para conservarlo.</p>
					{/if}
				</div>
			</div>

			{#if googleSettings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">URIs de redirección autorizados</p>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Estos deben estar registrados en tu cliente OAuth en Google Cloud (Credenciales → tu cliente → URIs de redirección autorizados).
					</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/calendar/callback</code>
					<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/auth/callback</code>
					{#if !isLocal}
						<p class="mt-1.5 text-xs text-muted-foreground">
							Si también ejecutas Calnode localmente, agrega también las
							variantes <code class="rounded bg-muted px-1">http://localhost:3000/…</code>.
						</p>
					{/if}
				</div>
			{/if}

			<div class="mt-5">
				<Button onclick={save} disabled={savingFlag.active}>
					{savingFlag.active ? 'Guardando…' : 'Guardar'}
				</Button>
			</div>
		</div>
	</div>
{/if}

{/if}
