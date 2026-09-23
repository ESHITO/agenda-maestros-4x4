<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Select from '$lib/components/ui/select';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import type CropperType from 'cropperjs';

	type Branding = {
		business_name: string;
		logo_url: string;
		logo_height: number;
		logo_opacity: number;
		banner_url: string;
		banner_opacity: number;
		privacy_url: string;
		terms_url: string;
		fallback_locale: string;
		supported_locales: { code: string; name: string }[];
	};

	// Both logo and banner share the same upload/crop dialog; cropTarget picks
	// which endpoint + field name cropAndUpload() posts to.
	type CropTarget = 'logo' | 'banner';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	const uploadingFlag = createAsyncFlag();
	let businessName = $state('');
	let logoUrl = $state('');
	let logoHeight = $state(28);
	let logoOpacity = $state(100);
	let bannerUrl = $state('');
	let bannerOpacity = $state(100);
	let privacyUrl = $state('');
	let termsUrl = $state('');
	let fallbackLocale = $state('en');
	let supportedLocales = $state<{ code: string; name: string }[]>([]);
	let fileInput = $state<HTMLInputElement | undefined>();
	let bannerFileInput = $state<HTMLInputElement | undefined>();

	// Crop dialog — Cropper is lazy-loaded client-side only to avoid SSR failures.
	let cropOpen = $state(false);
	let cropSrc = $state('');
	let cropTarget = $state<CropTarget>('logo');
	let cropperEl = $state<HTMLImageElement | undefined>();
	let cropperInstance: CropperType | null = null;
	let CropperClass: typeof CropperType | null = null;

	$effect(() => {
		if (!cropperEl || !CropperClass) return;
		// Free-form crop (aspectRatio NaN) with the whole image selected by default,
		// so cropping is optional — the user can adjust or just Save the full image.
		const c = new CropperClass(cropperEl, {
			aspectRatio: NaN,
			viewMode: 1,
			autoCropArea: 1,
			movable: true,
			zoomable: true,
			rotatable: false,
			scalable: false,
			background: true
		});
		cropperInstance = c;
		return () => { c.destroy(); cropperInstance = null; };
	});

	onMount(() => loadingFlag.run(async () => {
		const b = await api.get<Branding>('/v1/settings/branding');
		businessName = b.business_name ?? '';
		logoUrl = b.logo_url ?? '';
		logoHeight = b.logo_height || 28;
		logoOpacity = b.logo_opacity || 100;
		bannerUrl = b.banner_url ?? '';
		bannerOpacity = b.banner_opacity || 100;
		privacyUrl = b.privacy_url ?? '';
		termsUrl = b.terms_url ?? '';
		fallbackLocale = b.fallback_locale || 'en';
		supportedLocales = b.supported_locales ?? [];
	}, 'No se pudieron cargar los ajustes de marca'));

	async function onFileChange(target: CropTarget) {
		const input = target === 'logo' ? fileInput : bannerFileInput;
		const file = input?.files?.[0];
		if (!file) return;
		if (!CropperClass) {
			const [mod] = await Promise.all([
				import('cropperjs'),
				import('cropperjs/dist/cropper.min.css')
			]);
			CropperClass = mod.default;
		}
		cropTarget = target;
		const reader = new FileReader();
		reader.onload = (e) => {
			cropSrc = e.target?.result as string;
			cropOpen = true;
		};
		reader.readAsDataURL(file);
	}

	function cancelCrop() {
		cropOpen = false;
		cropSrc = '';
		if (fileInput) fileInput.value = '';
		if (bannerFileInput) bannerFileInput.value = '';
	}

	async function cropAndUpload() {
		if (!cropperInstance) return;
		const target = cropTarget;
		await uploadingFlag.run(async () => {
			const canvas = cropperInstance!.getCroppedCanvas({ maxWidth: 1200, maxHeight: 1200 });
			const blob = await new Promise<Blob>((resolve, reject) =>
				canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('Canvas export failed'))), 'image/png')
			);
			const data = new FormData();
			data.append(target, blob, `${target}.png`);
			if (target === 'logo') {
				const res = await api.postForm<{ logo_url: string }>('/v1/settings/branding/logo', data);
				logoUrl = res.logo_url;
			} else {
				const res = await api.postForm<{ banner_url: string }>('/v1/settings/branding/banner', data);
				bannerUrl = res.banner_url;
			}
			cropOpen = false;
			cropSrc = '';
			if (fileInput) fileInput.value = '';
			if (bannerFileInput) bannerFileInput.value = '';
			toast.success(target === 'logo' ? 'Logo subido' : 'Banner subido');
		}, `No se pudo subir ${target === 'logo' ? 'el logo' : 'el banner'}`);
	}

	async function removeLogo() {
		try {
			await api.del('/v1/settings/branding/logo');
			logoUrl = '';
			toast.success('Logo eliminado');
		} catch (e: any) {
			toast.error(e.message || 'No se pudo eliminar el logo');
		}
	}

	async function removeBanner() {
		try {
			await api.del('/v1/settings/branding/banner');
			bannerUrl = '';
			toast.success('Banner eliminado');
		} catch (e: any) {
			toast.error(e.message || 'No se pudo eliminar el banner');
		}
	}

	async function save() {
		await savingFlag.run(async () => {
			const b = await api.patch<Branding>('/v1/settings/branding', {
				business_name: businessName,
				logo_height: logoHeight,
				logo_opacity: logoOpacity,
				banner_opacity: bannerOpacity,
				privacy_url: privacyUrl,
				terms_url: termsUrl,
				fallback_locale: fallbackLocale
			});
			businessName = b.business_name ?? '';
			logoHeight = b.logo_height || 28;
			logoOpacity = b.logo_opacity || 100;
			bannerOpacity = b.banner_opacity || 100;
			privacyUrl = b.privacy_url ?? '';
			termsUrl = b.terms_url ?? '';
			fallbackLocale = b.fallback_locale || 'en';
			toast.success('Marca guardada');
		}, 'No se pudieron guardar los ajustes de marca');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Se requiere acceso de administrador.</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="max-w-2xl space-y-6">
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Marca</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Tu logo aparece en la parte superior de los correos de confirmación de reservas y en tus páginas
				públicas de reserva y gestión. El nombre del negocio es el texto que se muestra cuando no hay logo.
			</p>

			<div class="mt-4 space-y-1.5">
				<Label for="business-name">Nombre del negocio</Label>
				<Input id="business-name" bind:value={businessName} placeholder="Orchestratr" maxlength={200} />
				<p class="text-xs text-muted-foreground">Se usa cuando no hay logo. Vuelve a “Calnode” si se deja en blanco.</p>
			</div>

			<div class="mt-5 space-y-3">
				<Label>Logo</Label>
				<input bind:this={fileInput} type="file" accept="image/jpeg,image/png,image/gif,image/webp" class="hidden" onchange={() => onFileChange('logo')} />
				<button
					type="button"
					onclick={() => fileInput?.click()}
					disabled={uploadingFlag.active}
					title={logoUrl ? 'Reemplazar logo' : 'Subir logo'}
					class="group relative flex min-h-[88px] w-full max-w-md cursor-pointer items-center justify-center overflow-hidden rounded-md border bg-white px-4 py-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-wait"
				>
					{#if logoUrl}
						<img src={logoUrl} alt="Logo" style="height:{logoHeight}px;width:auto;opacity:{logoOpacity / 100};" />
					{:else}
						<span class="text-sm text-muted-foreground">Haz clic para subir un logo</span>
					{/if}
					<div class="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
						<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/></svg>
					</div>
				</button>

				{#if logoUrl}
					<div class="max-w-md space-y-3">
						<div class="flex items-center gap-3">
							<span class="w-14 text-xs font-medium text-muted-foreground">Tamaño</span>
							<input type="range" min="16" max="64" step="1" bind:value={logoHeight} class="flex-1 accent-primary" />
							<span class="w-10 text-right text-xs tabular-nums text-muted-foreground">{logoHeight}px</span>
						</div>
						<div class="flex items-center gap-3">
							<span class="w-14 text-xs font-medium text-muted-foreground">Opacidad</span>
							<input type="range" min="20" max="100" step="1" bind:value={logoOpacity} class="flex-1 accent-primary" />
							<span class="w-10 text-right text-xs tabular-nums text-muted-foreground">{logoOpacity}%</span>
						</div>
						<Button type="button" variant="ghost" size="sm" onclick={removeLogo} class="text-destructive hover:text-destructive">Eliminar logo</Button>
					</div>
				{/if}

				<p class="text-xs text-muted-foreground">
					Haz clic en el recuadro para subir. Se muestra hasta 600×160, así que lo que sea más ancho
					que alto funciona mejor. Un PNG con fondo transparente se ve mejor sobre un fondo claro.
					Cualquier forma sirve — puedes recortarlo después. JPEG, PNG, GIF o WebP, máx. 5 MB; se
					vuelve a codificar como PNG. Ajusta el tamaño y la opacidad (la vista previa se actualiza
					en vivo) y luego Guarda.
				</p>
			</div>

			<div class="mt-6 space-y-3 border-t pt-5">
				<Label>Banner</Label>
				<input bind:this={bannerFileInput} type="file" accept="image/jpeg,image/png,image/gif,image/webp" class="hidden" onchange={() => onFileChange('banner')} />
				<button
					type="button"
					onclick={() => bannerFileInput?.click()}
					disabled={uploadingFlag.active}
					title={bannerUrl ? 'Reemplazar banner' : 'Subir banner'}
					class="group relative flex min-h-[88px] w-full max-w-md cursor-pointer items-center justify-center overflow-hidden rounded-md border bg-white px-4 py-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-wait"
				>
					{#if bannerUrl}
						<img src={bannerUrl} alt="Banner" style="width:100%;height:auto;opacity:{bannerOpacity / 100};" />
					{:else}
						<span class="text-sm text-muted-foreground">Haz clic para subir un banner</span>
					{/if}
					<div class="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
						<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/></svg>
					</div>
				</button>

				{#if bannerUrl}
					<div class="max-w-md space-y-3">
						<div class="flex items-center gap-3">
							<span class="w-14 text-xs font-medium text-muted-foreground">Opacidad</span>
							<input type="range" min="20" max="100" step="1" bind:value={bannerOpacity} class="flex-1 accent-primary" />
							<span class="w-10 text-right text-xs tabular-nums text-muted-foreground">{bannerOpacity}%</span>
						</div>
						<Button type="button" variant="ghost" size="sm" onclick={removeBanner} class="text-destructive hover:text-destructive">Eliminar banner</Button>
					</div>
				{/if}

				<p class="text-xs text-muted-foreground">
					Se muestra a todo el ancho debajo de tu logo en los correos de reserva y en tus páginas
					públicas de reserva/gestión — se oculta por completo si no está configurado. Se muestra
					hasta 1600×800, así que las imágenes anchas funcionan mejor. JPEG, PNG, GIF o WebP, máx. 5
					MB; se vuelve a codificar como PNG.
				</p>
			</div>
		</div>

		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Enlaces legales</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Enlaces a tu propia política de privacidad y términos. Aparecen en el pie de tu página pública
				de reserva, y tu política de privacidad se enlaza desde el banner de consentimiento de cookies.
				Tú eres el responsable de los datos de las reservas hechas a través de tu Calnode — esto solo
				dirige a los visitantes a tus políticas.
			</p>

			<div class="mt-4 space-y-1.5">
				<Label for="privacy-url">URL de política de privacidad</Label>
				<Input id="privacy-url" type="url" bind:value={privacyUrl} placeholder="https://example.com/privacy" maxlength={500} />
			</div>

			<div class="mt-4 space-y-1.5">
				<Label for="terms-url">URL de términos</Label>
				<Input id="terms-url" type="url" bind:value={termsUrl} placeholder="https://example.com/terms" maxlength={500} />
				<p class="text-xs text-muted-foreground">Deja un campo en blanco para ocultar ese enlace. Debe ser una URL http(s):// completa.</p>
			</div>
		</div>

		{#if supportedLocales.length > 1}
			<div class="rounded-lg border bg-card p-6">
				<h2 class="text-sm font-semibold">Idioma</h2>
				<p class="mt-0.5 text-xs text-muted-foreground">
					Tus páginas públicas de reserva y correos se traducen según el visitante — la mayoría ve
					automáticamente el idioma de su navegador. Esto define lo que ve un visitante cuando su
					navegador solicita un idioma que no soportas (por ejemplo, un operador que atiende
					mayormente a clientes hispanohablantes podría preferir español aquí en vez de inglés).
				</p>
				<div class="mt-4 max-w-xs space-y-1.5">
					<Label for="fallback-locale">Idioma de respaldo</Label>
					<Select.Root
						type="single"
						value={fallbackLocale}
						onValueChange={(v) => { if (v) fallbackLocale = v; }}
					>
						<Select.Trigger id="fallback-locale" class="w-full">
							{supportedLocales.find((l) => l.code === fallbackLocale)?.name ?? 'Seleccionar…'}
						</Select.Trigger>
						<Select.Content>
							{#each supportedLocales as loc}
								<Select.Item value={loc.code} label={loc.name}>{loc.name}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
			</div>
		{/if}

		<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Guardando…' : 'Guardar'}</Button>
	</div>

	<Dialog.Root bind:open={cropOpen} onOpenChange={(o) => { if (!o) cancelCrop(); }}>
		<Dialog.Content class="max-w-lg">
			<Dialog.Header>
				<Dialog.Title>{cropTarget === 'logo' ? 'Recortar logo' : 'Recortar banner'}</Dialog.Title>
				<Dialog.Description>Arrastra para ajustar, o simplemente guarda para usar la imagen completa.</Dialog.Description>
			</Dialog.Header>
			<div class="mt-2 overflow-hidden rounded-md bg-muted" style="max-height: 360px;">
				{#if cropSrc}
					<img bind:this={cropperEl} src={cropSrc} alt="Vista previa de recorte" class="block max-w-full" />
				{/if}
			</div>
			<Dialog.Footer class="mt-4">
				<Button variant="outline" onclick={cancelCrop} disabled={uploadingFlag.active}>Cancelar</Button>
				<Button onclick={cropAndUpload} disabled={uploadingFlag.active}>{uploadingFlag.active ? 'Subiendo…' : cropTarget === 'logo' ? 'Guardar logo' : 'Guardar banner'}</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}
