<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User } from '$lib/api';
	import { prefs, prefsFromUser, timezoneItems, WEEK_DAYS } from '$lib/prefs';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Select from '$lib/components/ui/select';
	import { Combobox } from '$lib/components/ui/combobox';
	import * as Avatar from '$lib/components/ui/avatar';
	import * as Dialog from '$lib/components/ui/dialog';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import type CropperType from 'cropperjs';

	let user = $state<User | null>(null);
	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	const uploadingFlag = createAsyncFlag();
	let avatarUrl = $state('');
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	let name = $state('');
	let booking_accent = $state('#111827');
	let timezone = $state('UTC');
	let time_format = $state<'12h' | '24h'>('12h');
	let week_start = $state(1);
	let date_format = $state<'dmy' | 'mdy' | 'ymd'>('dmy');

	// Crop dialog state — Cropper is lazy-loaded client-side only to avoid SSR failures
	let cropOpen = $state(false);
	let cropSrc = $state('');
	let cropperEl = $state<HTMLImageElement | undefined>(undefined);
	let hasExistingAvatar = $state(false);
	let cropperInstance: CropperType | null = null;
	let CropperClass: (typeof CropperType) | null = null;

	$effect(() => {
		if (!cropperEl || !CropperClass) return;
		const c = new CropperClass(cropperEl, {
			aspectRatio: 1,
			viewMode: 1,
			autoCropArea: 0.8,
			movable: true,
			zoomable: true,
			rotatable: false,
			scalable: false,
		});
		cropperInstance = c;
		return () => { c.destroy(); cropperInstance = null; };
	});

	async function onFileChange() {
		const file = fileInput?.files?.[0];
		if (!file) return;
		// Lazy-load Cropper only when the user actually picks a file
		if (!CropperClass) {
			const [mod] = await Promise.all([
				import('cropperjs'),
				import('cropperjs/dist/cropper.min.css'),
			]);
			CropperClass = mod.default;
		}
		hasExistingAvatar = !!avatarUrl;
		const reader = new FileReader();
		reader.onload = (e) => {
			cropSrc = e.target?.result as string;
			cropOpen = true;
		};
		reader.readAsDataURL(file);
	}

	onMount(() => loadingFlag.run(async () => {
		user = await api.get<User>('/v1/users/me');
		name = user.name ?? '';
		booking_accent = user.booking_accent;
		timezone = user.timezone;
		time_format = user.time_format ?? '12h';
		week_start = user.week_start ?? 1;
		date_format = user.date_format ?? 'dmy';
		avatarUrl = user.avatar_url ?? '';
	}, 'No se pudo cargar el perfil'));

	function cancelCrop() {
		cropOpen = false;
		cropSrc = '';
		if (fileInput) fileInput.value = '';
	}

	async function cropAndUpload() {
		if (!cropperInstance) return;

		await uploadingFlag.run(async () => {
			const croppedCanvas = cropperInstance!.getCroppedCanvas({ width: 400, height: 400 });
			const blob = await new Promise<Blob>((resolve, reject) =>
				croppedCanvas.toBlob(b => b ? resolve(b) : reject(new Error('No se pudo exportar la imagen')), 'image/jpeg', 0.88)
			);
			const data = new FormData();
			data.append('avatar', blob, 'avatar.jpg');
			const res = await api.postForm<{ avatar_url: string }>('/v1/users/me/avatar', data);
			avatarUrl = res.avatar_url;
			const updated = await api.get<User>('/v1/users/me');
			currentUser.set(updated);
			cropOpen = false;
			cropSrc = '';
			if (fileInput) fileInput.value = '';
			toast.success('Foto de perfil actualizada');
		}, 'No se pudo subir la foto');
	}

	async function removeAvatar() {
		try {
			await api.del('/v1/users/me/avatar');
			avatarUrl = '';
			const updated = await api.get<User>('/v1/users/me');
			currentUser.set(updated);
		} catch (e: any) {
			toast.error(e.message || 'No se pudo eliminar la foto');
		}
	}

	async function save() {
		await savingFlag.run(async () => {
			const updated = await api.patch<User>('/v1/users/me', {
				name, timezone, time_format, week_start, date_format, booking_accent,
			});
			currentUser.set(updated);
			prefs.set(prefsFromUser(updated));
			user = updated;
			toast.success('Configuración guardada');
		}, 'No se pudo guardar la configuración');
	}

	function initials(name: string) {
		return name.split(' ').map((p) => p[0]).join('').toUpperCase().slice(0, 2);
	}

	const DATE_FORMATS = [
		{ value: 'dmy', label: 'DD/MM/YYYY' },
		{ value: 'mdy', label: 'MM/DD/YYYY' },
		{ value: 'ymd', label: 'YYYY-MM-DD' },
	];
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<form onsubmit={(e) => { e.preventDefault(); save(); }} class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Perfil</h2>
			<div class="space-y-4">
				<div class="flex items-center gap-4">
					<input bind:this={fileInput} type="file" accept="image/jpeg,image/png,image/gif,image/webp" class="hidden" onchange={onFileChange} />
					<button
						type="button"
						onclick={() => fileInput?.click()}
						disabled={uploadingFlag.active}
						title={avatarUrl ? 'Reemplazar foto' : 'Subir foto'}
						class="group relative cursor-pointer rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-wait"
					>
						<Avatar.Root class="size-16 text-xl font-semibold">
							<Avatar.Image src={avatarUrl || undefined} alt={user?.name} />
							<Avatar.Fallback>{initials(user?.name ?? 'U')}</Avatar.Fallback>
						</Avatar.Root>
						<div class="absolute inset-0 flex items-center justify-center rounded-full bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
							<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/></svg>
						</div>
					</button>
					<!-- min-w-0 + flex-1 so this text column shrinks against the fixed-size avatar
					     rather than pushing the row wide. Defensive: the settings shell itself stops
					     laying out sensibly below ~600px (the nav and content sit in one flex row with
					     no stacking breakpoint), so narrow viewports are already degraded for reasons
					     this does not fix. -->
					<div class="flex min-w-0 flex-1 flex-col gap-1.5">
						<p class="text-sm font-medium">Foto de perfil</p>
						<p class="text-xs text-muted-foreground">Haz clic en tu avatar para {avatarUrl ? 'reemplazarla' : 'subirla'} · se muestra en 400×400, así que una imagen cuadrada funciona mejor · JPEG, PNG, GIF o WebP, máximo 5 MB · se guarda como JPEG</p>
						{#if avatarUrl}
							<Button type="button" variant="ghost" size="sm" onclick={removeAvatar} class="w-fit text-destructive hover:text-destructive">Eliminar foto</Button>
						{/if}
					</div>
				</div>

				<div class="space-y-2">
					<Label for="booking-accent">Color de acento de reservas</Label>
					<div class="flex items-center gap-3">
						<input id="booking-accent" type="color" bind:value={booking_accent} class="h-10 w-14 cursor-pointer rounded border p-1" />
						<span class="text-sm text-muted-foreground">{booking_accent}</span>
					</div>
					<p class="text-xs text-muted-foreground">Se usa en tus páginas de reservas.</p>
				</div>
				<div class="space-y-1.5">
					<Label for="name">Nombre</Label>
					<Input id="name" type="text" bind:value={name} placeholder="Tu nombre" />
					<p class="text-xs text-muted-foreground">Tu nombre personal, que se muestra como el anfitrión de la reunión. La marca de tu negocio (logo, nombre del negocio) se configura por separado en Configuración → Marca.</p>
				</div>
				<div class="space-y-1.5">
					<Label class="text-muted-foreground">Correo electrónico</Label>
					<Input type="email" disabled value={user?.email ?? ''} class="opacity-60" />
				</div>
			</div>
		</div>

		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Preferencias</h2>
			<div class="space-y-4">
				<div class="space-y-1.5">
					<Label for="timezone">Zona horaria</Label>
					<Combobox
						items={timezoneItems(timezone)}
						bind:value={timezone}
						placeholder="Seleccionar zona horaria…"
						searchPlaceholder="Buscar zonas horarias…"
					/>
					<p class="text-xs text-muted-foreground">Se usa para calcular los horarios disponibles en tus páginas de reservas.</p>
				</div>

				<div class="space-y-1.5">
					<p class="text-sm font-medium">Formato de hora</p>
					<div class="flex gap-2">
						{#each [{ value: '12h', label: '12 horas', hint: '1:30 PM' }, { value: '24h', label: '24 horas', hint: '13:30' }] as opt}
							<label class="flex flex-1 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors {time_format === opt.value ? 'border-primary bg-primary/5' : 'bg-background hover:bg-accent/50'}">
								<input type="radio" bind:group={time_format} value={opt.value} class="sr-only" />
								{opt.label}
								<span class="text-xs text-muted-foreground">({opt.hint})</span>
							</label>
						{/each}
					</div>
				</div>

				<div class="space-y-1.5">
					<Label for="week-start">Primer día de la semana</Label>
					<Select.Root type="single" value={String(week_start)} onValueChange={(v) => { if (v) week_start = Number(v); }}>
						<Select.Trigger id="week-start" class="w-full">
							{WEEK_DAYS[week_start]}
						</Select.Trigger>
						<Select.Content>
							{#each [0, 1] as d}
								<Select.Item value={String(d)} label={WEEK_DAYS[d]}>{WEEK_DAYS[d]}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>

				<div class="space-y-1.5">
					<Label for="date-format">Formato de fecha</Label>
					<Select.Root type="single" value={date_format} onValueChange={(v) => { if (v) date_format = v as 'dmy' | 'mdy' | 'ymd'; }}>
						<Select.Trigger id="date-format" class="w-full">
							{DATE_FORMATS.find((f) => f.value === date_format)?.label ?? 'Seleccionar…'}
						</Select.Trigger>
						<Select.Content>
							{#each DATE_FORMATS as f}
								<Select.Item value={f.value} label={f.label}>{f.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
			</div>
		</div>

		<Button type="submit" disabled={savingFlag.active}>
			{savingFlag.active ? 'Guardando…' : 'Guardar'}
		</Button>
	</form>
{/if}

<Dialog.Root bind:open={cropOpen} onOpenChange={(o) => { if (!o) cancelCrop(); }}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>{hasExistingAvatar ? 'Reemplazar foto' : 'Subir foto'}</Dialog.Title>
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
