<script lang="ts">
	// The profile photo picker: a hidden file input, the square crop dialog (Cropper.js,
	// lazy-loaded client-side only) and the 400×400 JPEG export. Shared by the profile page
	// (POST /v1/users/me/avatar) and Members (POST /v1/users/{id}/avatar): each caller only
	// says where the FormData goes. Call pick() to open the file chooser.
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import type CropperType from 'cropperjs';

	let {
		upload,
		replacing = false,
		busy = $bindable(false)
	}: {
		/** Sends the cropped photo (field "avatar"); throw to keep the dialog open with a toast. */
		upload: (data: FormData) => Promise<void>;
		/** The person already has a photo: the dialog says "Reemplazar foto". */
		replacing?: boolean;
		/** True while the upload is in flight. */
		busy?: boolean;
	} = $props();

	const uploadingFlag = createAsyncFlag();
	let fileInput = $state<HTMLInputElement | undefined>(undefined);
	let cropOpen = $state(false);
	let cropSrc = $state('');
	let cropperEl = $state<HTMLImageElement | undefined>(undefined);
	let wasReplacing = $state(false);
	let cropperInstance: CropperType | null = null;
	let CropperClass: typeof CropperType | null = null;

	$effect(() => {
		busy = uploadingFlag.active;
	});

	$effect(() => {
		if (!cropperEl || !CropperClass) return;
		const c = new CropperClass(cropperEl, {
			aspectRatio: 1,
			viewMode: 1,
			autoCropArea: 0.8,
			movable: true,
			zoomable: true,
			rotatable: false,
			scalable: false
		});
		cropperInstance = c;
		return () => {
			c.destroy();
			cropperInstance = null;
		};
	});

	/** Opens the file chooser. */
	export function pick() {
		fileInput?.click();
	}

	async function onFileChange() {
		const file = fileInput?.files?.[0];
		if (!file) return;
		// Lazy-load Cropper only when a file is actually picked.
		if (!CropperClass) {
			const [mod] = await Promise.all([import('cropperjs'), import('cropperjs/dist/cropper.min.css')]);
			CropperClass = mod.default;
		}
		wasReplacing = replacing;
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
	}

	async function cropAndUpload() {
		if (!cropperInstance) return;
		await uploadingFlag.run(async () => {
			const canvas = cropperInstance!.getCroppedCanvas({ width: 400, height: 400 });
			const blob = await new Promise<Blob>((resolve, reject) =>
				canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('No se pudo exportar la imagen'))), 'image/jpeg', 0.88)
			);
			const data = new FormData();
			data.append('avatar', blob, 'avatar.jpg');
			await upload(data);
			cancelCrop();
		}, 'No se pudo subir la foto');
	}
</script>

<input
	bind:this={fileInput}
	type="file"
	accept="image/jpeg,image/png,image/gif,image/webp"
	class="hidden"
	onchange={onFileChange}
/>

<Dialog.Root bind:open={cropOpen} onOpenChange={(o) => { if (!o) cancelCrop(); }}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>{wasReplacing ? 'Reemplazar foto' : 'Subir foto'}</Dialog.Title>
			<Dialog.Description>Arrastra o pellizca para ajustar. Se guardará el área recortada.</Dialog.Description>
		</Dialog.Header>
		<div class="mt-2 overflow-hidden rounded-md bg-muted" style="max-height: 360px;">
			{#if cropSrc}
				<img bind:this={cropperEl} src={cropSrc} alt="Vista previa del recorte" class="block max-w-full" />
			{/if}
		</div>
		<Dialog.Footer class="mt-4">
			<Button variant="outline" onclick={cancelCrop} disabled={uploadingFlag.active}>Cancelar</Button>
			<Button onclick={cropAndUpload} disabled={uploadingFlag.active}>
				{uploadingFlag.active ? 'Subiendo…' : 'Guardar foto'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
