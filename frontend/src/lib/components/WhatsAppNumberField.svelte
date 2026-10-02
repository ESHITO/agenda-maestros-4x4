<!--
  Fork (Agenda Maestros 4x4): one person's WhatsApp number, for the notices to the host
  ("nueva sesión agendada", "faltan 5 minutos"). Used by Perfil («Tu WhatsApp», PUT
  /v1/users/me/whatsapp) and by Miembros (PUT /v1/users/{id}/whatsapp: the owner for anyone,
  an admin for non-admin members). Saved on its own, never with another form.

  A country picker (the same phone-data.json as the booking page's) plus the national
  number; a number typed with "+" carries its own code. The server is the judge (country
  code required, the code must exist) and answers Spanish 400s, shown under the field.
  The picker is built from the shadcn Popover + Input + Button, like Combobox, because the
  Combobox items are text only and the flags must be images (Windows shows emoji flags as
  two letters).
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import * as Popover from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import { toast } from 'svelte-sonner';
	import {
		combinePhone,
		countryName,
		doubledCodeHint,
		filterCountries,
		flagUrl,
		formatPhone,
		guessCountry,
		loadPhoneData,
		noticeClockHint,
		phoneDraftError,
		sortCountries,
		splitPhone,
		waMeUrl,
		type MemberWhatsApp,
		type PhoneCountry,
		type PhoneData
	} from '$lib/whatsapp-phone';

	let {
		phone = null,
		country = null,
		zone = '',
		self = false,
		personName = '',
		id,
		openWhenEmpty = false,
		save
	}: {
		/** The stored number, E.164 ("+51987654321"); null/"" = none. */
		phone?: string | null;
		/** The country the server worked out for it (settles a shared code like +1). */
		country?: string | null;
		/** The person's profile zone: picks the starting country and explains whose time
		 *  the notices show. */
		zone?: string;
		/** The signed-in user's own number (tú) or someone else's (su). */
		self?: boolean;
		personName?: string;
		/** Unique per page, for the label/input pair. */
		id: string;
		/** With no number yet, show the form straight away (Perfil) instead of a button. */
		openWhenEmpty?: boolean;
		/** Saves (a number) or removes (null); answers like GET. Throws the server's error. */
		save: (phone: string | null) => Promise<MemberWhatsApp>;
	} = $props();

	let data = $state<PhoneData | null>(null);
	let dataError = $state('');
	let editing = $state(false);
	let iso = $state('');
	let raw = $state('');
	let busy = $state(false);
	let error = $state('');
	let pickerOpen = $state(false);
	let filter = $state('');
	let removeOpen = $state(false);
	let numberInput = $state<HTMLInputElement | null>(null);

	const countries = $derived(data ? sortCountries(data.countries) : ([] as PhoneCountry[]));
	const filtered = $derived(filterCountries(countries, filter));
	const dial = $derived(countries.find((c) => c[0] === iso)?.[1] ?? '');
	const hasNumber = $derived(!!phone);
	const shown = $derived(formatPhone(phone, data?.countries ?? [], country));
	const storedCountry = $derived(country || (data ? splitPhone(phone, data.countries)?.iso ?? '' : ''));
	const draft = $derived(combinePhone(dial, raw));
	const draftShown = $derived(formatPhone(draft, data?.countries ?? [], iso));
	const doubledCode = $derived(doubledCodeHint(dial, raw));
	const draftIso = $derived(
		draft && data ? (splitPhone(draft, data.countries, iso)?.iso ?? '') : ''
	);
	const tu = $derived(self ? 'tu' : 'su');

	async function loadData() {
		dataError = '';
		try {
			data = await loadPhoneData();
		} catch (e: any) {
			dataError = e?.message || 'No se pudo cargar la lista de países.';
		}
	}

	onMount(() => {
		loadData().then(() => {
			if (openWhenEmpty && !phone && !editing) startEdit(false);
		});
	});

	// Start the form: the stored number split by its code, else the profile's country.
	async function startEdit(focus = true) {
		error = '';
		if (!data) await loadData();
		const parts = data ? splitPhone(phone, data.countries, country) : null;
		if (parts) {
			iso = parts.iso;
			raw = parts.national;
		} else {
			iso = data ? guessCountry(zone, data) : '';
			raw = '';
		}
		editing = true;
		if (!focus) return;
		await tick();
		numberInput?.focus();
	}

	function cancelEdit() {
		editing = false;
		error = '';
	}

	function pick(code: string) {
		iso = code;
		error = '';
		pickerOpen = false;
		filter = '';
		tick().then(() => numberInput?.focus());
	}

	function onFilterKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			if (filtered.length > 0) pick(filtered[0][0]);
		}
	}

	// Ctrl/Cmd+S while typing the number saves THIS field (it is never part of the page's
	// form). preventDefault also tells the page's own shortcut (saveOnCmdS) to stand back,
	// or Perfil would say «Configuración guardada» without saving the number.
	function onNumberKeydown(e: KeyboardEvent) {
		if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S')) {
			e.preventDefault();
			if (!busy) void submit();
		}
	}

	async function submit() {
		const why = phoneDraftError(dial, raw);
		if (why) {
			error = why;
			return;
		}
		busy = true;
		error = '';
		try {
			const res = await save(draft);
			editing = false;
			toast.success(
				self
					? 'Listo: los avisos de tus sesiones llegarán a este WhatsApp.'
					: `WhatsApp de ${personName || 'esta persona'} guardado.`
			);
			// The parent updates `phone`/`country` from the answer.
			void res;
		} catch (e: any) {
			error = e?.message || 'No se pudo guardar el número.';
		} finally {
			busy = false;
		}
	}

	async function remove() {
		busy = true;
		error = '';
		try {
			await save(null);
			editing = false;
			toast.success(self ? 'Quitaste tu WhatsApp: ya no recibirás los avisos.' : `WhatsApp de ${personName || 'esta persona'} quitado.`);
		} catch (e: any) {
			toast.error(e?.message || 'No se pudo quitar el número.');
		} finally {
			busy = false;
		}
	}
</script>

<ConfirmDialog
	bind:open={removeOpen}
	title={self ? '¿Quitar tu WhatsApp?' : `¿Quitar el WhatsApp de ${personName || 'esta persona'}?`}
	description={self
		? 'Dejarás de recibir los avisos de tus sesiones (nueva sesión y «faltan 5 minutos»).'
		: 'Dejará de recibir los avisos de sus sesiones (nueva sesión y «faltan 5 minutos»).'}
	confirmText="Quitar"
	cancelText="Cancelar"
	destructive
	onConfirm={remove}
/>

<div class="space-y-2">
	{#if !editing}
		{#if hasNumber}
			<div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
				<div class="flex min-w-0 items-center gap-2">
					{#if flagUrl(storedCountry)}
						<img src={flagUrl(storedCountry)} alt="" class="h-3.5 w-5 shrink-0 rounded-[2px] object-cover shadow-xs" />
					{/if}
					<div class="min-w-0">
						<a href={waMeUrl(phone)} target="_blank" rel="noopener noreferrer" class="font-mono text-sm font-medium tabular-nums hover:underline" title="Abrir en WhatsApp">{shown}</a>
						{#if storedCountry}<span class="ml-1 text-xs text-muted-foreground">{countryName(storedCountry)}</span>{/if}
					</div>
				</div>
				<div class="flex gap-1.5">
					<Button size="sm" variant="outline" onclick={() => startEdit()} disabled={busy}>Cambiar</Button>
					<Button size="sm" variant="ghost" class="text-destructive hover:text-destructive" onclick={() => (removeOpen = true)} disabled={busy}>Quitar</Button>
				</div>
			</div>
			<p class="text-xs text-muted-foreground">{noticeClockHint(zone, storedCountry ? countryName(storedCountry) : '', self)}</p>
		{:else}
			<div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
				<p class="text-sm text-muted-foreground">
					{self ? 'Aún no pusiste tu número: no recibirás los avisos de tus sesiones.' : 'Sin WhatsApp: no recibirá los avisos de sus sesiones.'}
				</p>
				<Button size="sm" variant="outline" class="self-start sm:self-auto" onclick={() => startEdit()}>
					{self ? 'Poner mi WhatsApp' : 'Poner su WhatsApp'}
				</Button>
			</div>
		{/if}
	{:else}
		<form class="space-y-2" onsubmit={(e) => { e.preventDefault(); submit(); }}>
			<Label for="{id}-number" class="text-xs">Número de WhatsApp, con el código de {tu} país</Label>
			<div class="flex gap-2">
				<Popover.Root bind:open={pickerOpen} onOpenChange={(o) => { if (!o) filter = ''; }}>
					<Popover.Trigger
						class="border-input focus-visible:border-ring focus-visible:ring-ring/50 inline-flex h-10 shrink-0 items-center gap-1.5 rounded-lg border bg-transparent px-2.5 text-sm outline-none focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50"
						aria-label={iso ? `País: ${countryName(iso)} (+${dial}). Cambiar` : 'Elegir país'}
						disabled={!data}
					>
						{#if iso && flagUrl(iso)}
							<img src={flagUrl(iso)} alt="" class="h-3.5 w-5 rounded-[2px] object-cover shadow-xs" />
							<span class="font-mono tabular-nums">+{dial}</span>
						{:else}
							<span class="text-muted-foreground">País</span>
						{/if}
						<ChevronDownIcon class="size-4 text-muted-foreground opacity-60" />
					</Popover.Trigger>
					<Popover.Content class="w-[min(20rem,calc(100vw-2rem))] p-0" align="start">
						<div class="p-2">
							<Input bind:value={filter} placeholder="Busca tu país o su código (+51)" onkeydown={onFilterKeydown} class="text-base sm:text-sm" autofocus />
						</div>
						<div class="max-h-72 overflow-y-auto p-1">
							{#if filtered.length === 0}
								<p class="px-2 py-3 text-center text-sm text-muted-foreground">Sin resultados.</p>
							{:else}
								{#each filtered as c (c[0])}
									<button
										type="button"
										onclick={() => pick(c[0])}
										class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm hover:bg-accent hover:text-accent-foreground"
									>
										<CheckIcon class="size-4 shrink-0 {iso === c[0] ? 'opacity-100' : 'opacity-0'}" />
										{#if flagUrl(c[0])}
											<img src={flagUrl(c[0])} alt="" loading="lazy" class="h-3.5 w-5 shrink-0 rounded-[2px] object-cover shadow-xs" />
										{:else}
											<span class="w-5 shrink-0"></span>
										{/if}
										<span class="min-w-0 flex-1 truncate">{countryName(c[0])}</span>
										<span class="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">+{c[1]}</span>
									</button>
								{/each}
							{/if}
						</div>
					</Popover.Content>
				</Popover.Root>
				<Input
					id="{id}-number"
					bind:ref={numberInput}
					bind:value={raw}
					type="tel"
					inputmode="tel"
					autocomplete="tel-national"
					placeholder="987 654 321"
					aria-invalid={error ? true : undefined}
					oninput={() => (error = '')}
					onkeydown={onNumberKeydown}
					class="h-10 min-w-0 flex-1 text-base sm:text-sm"
				/>
			</div>
			{#if dataError}
				<p class="text-xs text-destructive">
					{dataError} Puedes escribir el número con «+» y su código.
					<button type="button" class="underline" onclick={loadData}>Reintentar</button>
				</p>
			{/if}
			{#if error}
				<p class="text-xs text-destructive" role="alert">{error}</p>
			{:else if draft && draftShown}
				<p class="text-xs text-muted-foreground">
					Se guardará como <span class="font-mono font-medium text-foreground tabular-nums">{draftShown}</span>{#if draftIso}{` (${countryName(draftIso)})`}{/if}.
				</p>
				{#if doubledCode}
					<p class="text-xs font-medium text-amber-700 dark:text-amber-400" role="status">{doubledCode}</p>
				{/if}
			{:else}
				<p class="text-xs text-muted-foreground">Elige el país y escribe el número como lo marcarías en ese país.</p>
			{/if}
			<div class="flex flex-col gap-2 sm:flex-row">
				<Button type="submit" class="h-10 sm:h-8" disabled={busy}>{busy ? 'Guardando…' : 'Guardar WhatsApp'}</Button>
				{#if hasNumber || !openWhenEmpty}
					<Button type="button" variant="ghost" class="h-10 sm:h-8" onclick={cancelEdit} disabled={busy}>Cancelar</Button>
				{/if}
			</div>
		</form>
	{/if}
</div>
