<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	type Tracking = {
		head_html: string;
		csp_allow: string;
		datalayer_enabled: boolean;
		datalayer_fields: string[];
		available_fields: string[];
		gtm_container_id: string;
		ga4_measurement_id: string;
	};

	const fieldLabels: Record<string, string> = {
		booking_id: 'Referencia de reserva', event_type_slug: 'Slug del tipo de atención', event_type_name: 'Nombre del tipo de atención',
		start_at: 'Hora de inicio', end_at: 'Hora de fin', status: 'Estado', location: 'Ubicación',
		host_name: 'Nombre del anfitrión', attendee_name: 'Nombre del asistente', attendee_email: 'Correo del asistente',
		attendee_timezone: 'Zona horaria del asistente', answers: 'Respuestas del formulario',
		value: 'Ingreso / monto', currency: 'Moneda', is_paid: 'Indicador de pago', transaction_id: 'ID de transacción'
	};
	const piiFields = new Set(['attendee_name', 'attendee_email', 'attendee_timezone', 'answers']);

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	let headHtml = $state('');
	let cspAllow = $state('');
	let dlEnabled = $state(false);
	let dlFields = $state<string[]>([]);
	let availableFields = $state<string[]>([]);
	let gtmId = $state('');
	let ga4Id = $state('');

	onMount(() => loadingFlag.run(async () => {
		const t = await api.get<Tracking>('/v1/settings/tracking');
		headHtml = t.head_html ?? '';
		cspAllow = t.csp_allow ?? '';
		dlEnabled = t.datalayer_enabled;
		dlFields = t.datalayer_fields ?? [];
		availableFields = t.available_fields ?? [];
		gtmId = t.gtm_container_id ?? '';
		ga4Id = t.ga4_measurement_id ?? '';
	}, 'No se pudo cargar la configuración de seguimiento'));

	function toggleField(key: string) {
		dlFields = dlFields.includes(key) ? dlFields.filter((f) => f !== key) : [...dlFields, key];
	}

	async function save() {
		await savingFlag.run(async () => {
			const t = await api.patch<Tracking>('/v1/settings/tracking', {
				head_html: headHtml,
				csp_allow: cspAllow,
				datalayer_enabled: dlEnabled,
				datalayer_fields: dlFields,
				gtm_container_id: gtmId.trim(),
				ga4_measurement_id: ga4Id.trim()
			});
			dlFields = t.datalayer_fields ?? [];
			gtmId = t.gtm_container_id ?? '';
			ga4Id = t.ga4_measurement_id ?? '';
			toast.success('Configuración de seguimiento guardada');
		}, 'No se pudo guardar la configuración de seguimiento');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Se requiere acceso de administrador.</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="max-w-2xl space-y-6">
		<!-- Native GA4 / GTM -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Google Analytics &amp; Tag Manager</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Ingresa un ID y Calnode carga la etiqueta oficial en tu página de reserva automáticamente, sin necesidad
				de pegar ningún fragmento de código, y el CSP de la página se gestiona por ti. Deja un campo en blanco
				para desactivar esa etiqueta.
			</p>
			<div class="mt-4 grid gap-4 sm:grid-cols-2">
				<div class="space-y-1.5">
					<Label for="gtm-id">ID de contenedor de GTM</Label>
					<Input id="gtm-id" bind:value={gtmId} placeholder="GTM-XXXXXXX" class="font-mono" />
					<p class="text-xs text-muted-foreground">
						Recomendado — gestiona las etiquetas de GA4 + Ads. Actívalas con el evento
						<code class="rounded bg-muted px-1">calnode_booking_confirmed</code> del dataLayer de abajo.
					</p>
				</div>
				<div class="space-y-1.5">
					<Label for="ga4-id">ID de medición de GA4</Label>
					<Input id="ga4-id" bind:value={ga4Id} placeholder="G-XXXXXXXXXX" class="font-mono" />
					<p class="text-xs text-muted-foreground">
						Carga GA4 directamente. Las reservas disparan un evento <code class="rounded bg-muted px-1">purchase</code> /
						<code class="rounded bg-muted px-1">generate_lead</code> con el ingreso.
					</p>
				</div>
			</div>
		</div>

		<!-- Code injection -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Inyección de código (head)</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				HTML/JS sin procesar que se inyecta en el &lt;head&gt; de tus páginas públicas de reserva y gestión — para
				cualquier etiqueta <em>no</em> cubierta arriba (Meta Pixel, Plausible, personalizada). Se ejecuta en los
				navegadores de los visitantes; solo los administradores pueden configurarlo.
			</p>
			<div class="mt-4 space-y-1.5">
				<Label for="head-html">HTML del &lt;head&gt;</Label>
				<Textarea id="head-html" bind:value={headHtml} rows={8} class="font-mono text-xs"
					placeholder="<!-- Pega aquí tu fragmento de GTM / GA4 / Meta Pixel -->" />
			</div>
			<div class="mt-4 space-y-1.5">
				<Label for="csp-allow">Orígenes permitidos <span class="font-normal text-muted-foreground">(opcional)</span></Label>
				<Input id="csp-allow" bind:value={cspAllow}
					placeholder="https://www.googletagmanager.com https://*.google-analytics.com" />
				<p class="text-xs text-muted-foreground">
					Déjalo en blanco para permitir cualquier origen <code class="rounded bg-muted px-1">https:</code> mientras
					la inyección esté activa — es lo más simple, y las etiquetas gestionadas por GTM funcionan sin problema.
					Complétalo para restringir el CSP de la página solo a estos orígenes (separados por espacios); las
					etiquetas de otros dominios quedarán bloqueadas.
				</p>
			</div>
		</div>

		<!-- dataLayer events -->
		<div class="rounded-lg border bg-card p-6">
			<div class="flex items-start justify-between gap-4">
				<div>
					<h2 class="text-sm font-semibold">Eventos de dataLayer</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Envía <code class="rounded bg-muted px-1">calnode_booking_confirmed</code> /
						<code class="rounded bg-muted px-1">_cancelled</code> /
						<code class="rounded bg-muted px-1">_rescheduled</code> a
						<code class="rounded bg-muted px-1">window.dataLayer</code> para que GTM pueda activarse con ellos.
					</p>
				</div>
				<Switch bind:checked={dlEnabled} />
			</div>
			{#if dlEnabled}
				<div class="mt-4 space-y-2">
					<p class="text-xs font-medium text-muted-foreground">
						Campos a incluir — desmarca lo que no quieras exponer al navegador / GTM.
					</p>
					<div class="grid grid-cols-2 gap-x-4 gap-y-1">
						{#each availableFields as key}
							<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
								<Checkbox checked={dlFields.includes(key)} onCheckedChange={() => toggleField(key)} />
								<span>{fieldLabels[key] ?? key}{#if piiFields.has(key)}<span class="ml-1 text-[10px] font-medium uppercase text-amber-600">PII</span>{/if}</span>
							</label>
						{/each}
					</div>
				</div>
			{/if}
		</div>

		<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Guardando…' : 'Guardar'}</Button>
	</div>
{/if}
