<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type ZoomSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();

	let settings = $state<ZoomSettings | null>(null);
	let clientID = $state('');
	let clientSecret = $state('');

	const redirectURI = $derived(settings?.redirect_uri || '');

	onMount(() => loadingFlag.run(async () => {
		settings = await api.get<ZoomSettings>('/v1/settings/zoom');
		clientID = settings.client_id;
	}, 'Could not load Zoom settings'));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { client_id: clientID };
			if (clientSecret) body.client_secret = clientSecret;
			settings = await api.patch<ZoomSettings>('/v1/settings/zoom', body);
			clientSecret = '';
			toast.success('Guardado — cada anfitrión ya puede conectar Zoom desde la página de Calendario');
		}, 'Could not save Zoom settings');
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

		{#if !settings?.configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Instrucciones de configuración</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>
						Ve al <a href="https://marketplace.zoom.us" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">Zoom App Marketplace</a>
						e inicia sesión con tu cuenta de Zoom. Haz clic en <span class="font-medium">Desarrollar → Crear app</span>.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>
						Elige una <span class="font-medium">App General</span> (OAuth gestionado por el usuario). Mientras
						no esté publicada, Zoom solo permite conectarse a los usuarios <span class="font-medium">dentro de la cuenta de Zoom propietaria
						de la app</span>; los anfitriones con sus propias cuentas de Zoom separadas necesitan una app publicada
						(consulta DEPLOY.md, "Zoom meeting links"). Si eso descarta a Zoom, el video integrado no necesita
						ninguna app de Zoom — consulta <a href="https://github.com/Calnode/calnode/blob/main/docs/VIDEO.md" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">docs/VIDEO.md</a>.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						En <span class="font-medium">OAuth</span>, configura la <span class="font-medium">URL de redirección para OAuth</span>
						(y agrégala a la lista de permitidos) con:
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectURI}</code>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>
						En <span class="font-medium">Alcances</span>, agrega <code class="rounded bg-muted px-1">meeting:write</code>
						(crear, actualizar y eliminar reuniones en nombre del usuario).
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">5</span>
					<div>
						Copia el <span class="font-medium">ID de cliente</span> y el <span class="font-medium">Secreto de cliente</span>
						desde la información básica de la app, pégalos abajo y guarda.
					</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Zoom</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Permite que cada anfitrión conecte su propia cuenta de Zoom para que las reservas ubicadas en
						Zoom obtengan automáticamente un enlace de reunión real.
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
					<Label for="z-client-id">ID de cliente</Label>
					<Input id="z-client-id" type="text" placeholder="abcdEFGhij-KLmnOPqrS" bind:value={clientID} />
				</div>
				<div class="space-y-1.5">
					<Label for="z-client-secret">Secreto de cliente</Label>
					<Input id="z-client-secret" type="password"
						placeholder={settings?.client_secret_set ? '•••••••• (guardado)' : 'Ingresa el secreto de cliente'}
						bind:value={clientSecret} />
					{#if settings?.client_secret_set && !clientSecret}
						<p class="text-xs text-muted-foreground">Guardado — déjalo en blanco para conservarlo.</p>
					{/if}
				</div>
			</div>

			{#if settings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">URL de redirección</p>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Esto debe registrarse en tu app de Zoom (OAuth → URL de redirección + lista de permitidos de OAuth).
					</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectURI}</code>
				</div>
				<div class="mt-4 rounded-md bg-amber-50 px-3 py-2.5 text-xs text-amber-800 ring-1 ring-inset ring-amber-200">
					<span class="font-medium">Límite para varios miembros:</span> solo los usuarios de Zoom en la misma cuenta
					de Zoom que el propietario de esta app pueden conectar una app sin publicar — los miembros de otras cuentas
					son rechazados por Zoom antes de que Calnode intervenga. Opciones:
					<a href="https://github.com/Calnode/calnode/blob/main/docs/ZOOM.md" target="_blank" rel="noopener noreferrer" class="font-medium underline">docs/ZOOM.md</a>
					(el video integrado no necesita ninguna app de Zoom).
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
