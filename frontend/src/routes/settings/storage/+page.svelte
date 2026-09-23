<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Switch } from '$lib/components/ui/switch';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	type StorageSettings = {
		backups_configured: boolean;
		backups_bucket: string;
		backups_endpoint: string;
		recordings_enabled: boolean;
		recordings_storage_ready: boolean;
		recordings_prefix: string;
	};

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	let settings = $state<StorageSettings | null>(null);
	let recordingsEnabled = $state(false);

	onMount(() => loadingFlag.run(async () => {
		settings = await api.get<StorageSettings>('/v1/settings/storage');
		recordingsEnabled = settings.recordings_enabled;
	}, 'No se pudo cargar la configuración de almacenamiento'));

	async function save() {
		await savingFlag.run(async () => {
			settings = await api.patch<StorageSettings>('/v1/settings/storage', { recordings_enabled: recordingsEnabled });
			toast.success('Guardado');
		}, 'No se pudo guardar');
	}

	function badge(ok: boolean, on = 'Configurado', off = 'No configurado') {
		return { text: ok ? on : off, cls: ok ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700', dot: ok ? 'bg-green-500' : 'bg-amber-400' };
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Se requiere acceso de administrador.</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="max-w-lg space-y-4">
		<p class="text-sm text-muted-foreground">El almacenamiento de objetos se usa en dos lugares — copias de seguridad de la base de datos y grabaciones de reuniones. Comparten un mismo bucket.</p>

		{#if !settings?.backups_configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Instrucciones de configuración</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>
						Crea un bucket con cualquier proveedor de almacenamiento de objetos compatible
						con S3 — por ejemplo
						<a href="https://developers.cloudflare.com/r2/" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">Cloudflare R2</a>,
						<a href="https://www.backblaze.com/cloud-storage" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">Backblaze B2</a>,
						o <a href="https://aws.amazon.com/s3/" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">AWS S3</a>.
						Este es el mismo bucket en el que escriben tanto las copias de seguridad de
						la base de datos como las grabaciones de reuniones.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>Genera un ID de clave de acceso y una clave de acceso secreta para ese bucket.</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						No hay un formulario para esto — configura estas variables de entorno/secretos
						con tu proveedor de hosting, y luego vuelve a desplegar:
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_REPLICA_URL=s3://your-bucket-name/calnode</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_ACCESS_KEY_ID=…</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_SECRET_ACCESS_KEY=…</code>
						<p class="mt-1.5 text-xs text-muted-foreground">
							Para cualquier proveedor que no sea AWS S3 (R2, B2, MinIO, etc.), también
							configura <code class="rounded bg-muted px-1">LITESTREAM_ENDPOINT</code> con la URL del
							endpoint compatible con S3 de ese proveedor. <code class="rounded bg-muted px-1">LITESTREAM_REGION</code> es
							opcional — solo algunos proveedores lo necesitan.
						</p>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>
						Recarga esta página — una vez que muestre <span class="font-medium">Configurado</span> abajo,
						las grabaciones de reuniones podrán activarse debajo.
					</div>
				</li>
			</ol>
		</div>
		{/if}

		<!-- Backups (read-only; configured via environment) -->
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-3 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Copias de seguridad de la base de datos (Litestream)</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">Replicación continua de SQLite a tu bucket. Se configura mediante variables de entorno al desplegar.</p>
				</div>
				{#if settings}
					{@const b = badge(settings.backups_configured)}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {b.cls}">
						<span class="h-1.5 w-1.5 rounded-full {b.dot}"></span>{b.text}
					</span>
				{/if}
			</div>
			{#if settings?.backups_configured}
				<dl class="mt-2 space-y-1 text-xs">
					<div class="flex gap-2"><dt class="w-20 text-muted-foreground">Bucket</dt><dd class="font-mono">{settings.backups_bucket || '—'}</dd></div>
					{#if settings.backups_endpoint}<div class="flex gap-2"><dt class="w-20 text-muted-foreground">Endpoint</dt><dd class="font-mono break-all">{settings.backups_endpoint}</dd></div>{/if}
				</dl>
			{:else}
				<p class="text-xs text-muted-foreground">Configura <code class="rounded bg-muted px-1">LITESTREAM_REPLICA_URL</code> y las credenciales para activar las copias de seguridad — consulta las instrucciones de configuración arriba.</p>
			{/if}
		</div>

		<!-- Recordings -->
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-3 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Grabaciones de reuniones</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">Las grabaciones de video de LiveKit se suben al bucket de copias de seguridad bajo un prefijo <code class="rounded bg-muted px-1">{settings?.recordings_prefix || 'recordings/'}</code>.</p>
				</div>
				{#if settings}
					{@const b = badge(settings.recordings_storage_ready, 'Almacenamiento listo', 'Sin bucket')}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {b.cls}">
						<span class="h-1.5 w-1.5 rounded-full {b.dot}"></span>{b.text}
					</span>
				{/if}
			</div>
			<label class="flex cursor-pointer items-start justify-between gap-3">
				<span>
					<span class="text-sm font-medium">Permitir que los anfitriones graben las reuniones</span>
					<span class="mt-0.5 block text-xs text-muted-foreground">
						El anfitrión obtiene un botón de Grabar en la llamada; todos ven un aviso de “Grabando”.
						{#if !settings?.recordings_storage_ready}<span class="text-amber-700"> Configura primero las copias de seguridad (ver las instrucciones de configuración arriba) — las grabaciones necesitan un bucket.</span>{/if}
					</span>
				</span>
				<Switch class="mt-0.5 shrink-0" bind:checked={recordingsEnabled} disabled={!settings?.recordings_storage_ready} />
			</label>
			<div class="mt-5"><Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Guardando…' : 'Guardar'}</Button></div>
		</div>
	</div>
{/if}
