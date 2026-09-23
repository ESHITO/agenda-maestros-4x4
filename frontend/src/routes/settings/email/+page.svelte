<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User, type EmailSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	const testingFlag = createAsyncFlag();

	let emailSettings = $state<EmailSettings | null>(null);
	let smtpHost = $state('');
	let smtpPort = $state('587');
	let smtpUser = $state('');
	let smtpPass = $state('');
	let smtpTLS = $state(false);
	let smtpStartTLS = $state(true);
	let emailFrom = $state('');
	let emailFromName = $state('Calnode');
	let resendApiKey = $state('');
	// Distinct from "the field is blank": blank means keep the stored key, this means
	// deliberately remove it and go back to SMTP.
	let clearResendKey = $state(false);

	let userEmail = $state('');

	// The server decides the transport; mirror its answer rather than re-deriving it here,
	// so the page can never claim one path while another is delivering.
	// clearResendKey has to win over the server's answer: once the admin has chosen to
	// remove the key, the SMTP fields are about to become live again and must stop being
	// dimmed, even though the server still reports transport === 'resend_api'.
	const usingResend = $derived(
		!clearResendKey && (emailSettings?.transport === 'resend_api' || !!resendApiKey),
	);

	onMount(() => loadingFlag.run(async () => {
		const [me, email] = await Promise.all([
			api.get<User>('/v1/users/me'),
			api.get<EmailSettings>('/v1/settings/email'),
		]);
		userEmail = me.email;
		emailSettings = email;
		smtpHost = email.smtp_host;
		smtpPort = email.smtp_port || '587';
		smtpUser = email.smtp_user;
		smtpTLS = email.smtp_tls;
		smtpStartTLS = email.smtp_starttls;
		emailFrom = email.email_from;
		emailFromName = email.email_from_name || 'Calnode';
	}, 'No se pudo cargar la configuración de correo'));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = {
				smtp_host: smtpHost, smtp_port: smtpPort, smtp_user: smtpUser,
				smtp_tls: smtpTLS, smtp_starttls: smtpStartTLS,
				email_from: emailFrom, email_from_name: emailFromName,
			};
			if (smtpPass) body.smtp_pass = smtpPass;
			// Omit the key entirely to keep the stored one; send "" only to clear it.
			if (resendApiKey) body.resend_api_key = resendApiKey;
			else if (clearResendKey) body.resend_api_key = '';
			emailSettings = await api.patch<EmailSettings>('/v1/settings/email', body);
			smtpPass = '';
			resendApiKey = '';
			clearResendKey = false;
			toast.success('Configuración de correo guardada');
		}, 'No se pudo guardar la configuración de correo');
	}

	async function test() {
		await testingFlag.run(async () => {
			try {
				await api.post('/v1/settings/email/test');
			} catch (e: any) {
				if (e.message?.startsWith('Email is not configured')) {
					throw new Error('Guarda tu configuración primero y vuelve a intentarlo.');
				}
				throw e;
			}
			toast.success(`Correo de prueba enviado a ${userEmail}`);
		}, 'No se pudo enviar el correo de prueba');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Se requiere acceso de administrador.</p>
{:else}

{#if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Cargando…</p>
{:else}
	<div class="max-w-lg">
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Correo electrónico</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">Cómo Calnode envía los correos de las reservas.</p>
				</div>
				{#if emailSettings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {emailSettings.enabled ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {emailSettings.enabled ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{emailSettings.transport === 'resend_api'
							? 'Enviando mediante la API de Resend'
							: emailSettings.transport === 'smtp'
								? 'Enviando por SMTP'
								: 'No configurado'}
					</span>
				{/if}
			</div>

			<div class="space-y-4">
				<div class="space-y-2 rounded-md border p-3">
					<div class="flex items-center justify-between gap-4">
						<div>
							<p class="text-xs font-medium">Clave de API de Resend</p>
							<p class="text-xs text-muted-foreground">
								Envía por HTTPS en lugar de SMTP. Usa esta opción si tu proveedor de alojamiento bloquea SMTP.
							</p>
						</div>
					</div>
					<Input id="resend-key" type="password"
						placeholder={emailSettings?.resend_api_key_set ? '•••••••• (guardada)' : 're_...'}
						bind:value={resendApiKey}
						disabled={clearResendKey} />
					{#if emailSettings?.resend_api_key_set && !resendApiKey && !clearResendKey}
						<div class="flex items-center justify-between gap-2">
							<p class="text-xs text-muted-foreground">Guardada — déjala en blanco para conservarla.</p>
							<Button variant="ghost" size="sm" class="h-6 px-2 text-xs"
								onclick={() => (clearResendKey = true)}>Quitar clave</Button>
						</div>
					{:else if clearResendKey}
						<div class="flex items-center justify-between gap-2">
							<p class="text-xs text-amber-700">Se eliminará al guardar; se usará SMTP en su lugar.</p>
							<Button variant="ghost" size="sm" class="h-6 px-2 text-xs"
								onclick={() => (clearResendKey = false)}>Deshacer</Button>
						</div>
					{/if}
					<p class="text-xs text-muted-foreground">
						Muchos proveedores de hosting (incluido Railway por debajo del plan Pro) bloquean
						por completo el SMTP saliente, lo cual se ve idéntico a una contraseña incorrecta.
						Una clave de API evita ese problema.
					</p>
				</div>

				<div class="space-y-4" class:opacity-60={usingResend}>
					{#if usingResend}
						<p class="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
							El correo se está enviando mediante la API de Resend, así que esta configuración
							de SMTP no está en uso. Se conserva para que puedas volver a ella quitando la
							clave de arriba.
						</p>
					{/if}
				<div class="grid grid-cols-3 gap-3">
					<div class="col-span-2 space-y-1.5">
						<Label for="smtp-host">Servidor SMTP</Label>
						<Input id="smtp-host" type="text" placeholder="smtp.gmail.com" bind:value={smtpHost} />
					</div>
					<div class="space-y-1.5">
						<Label for="smtp-port">Puerto</Label>
						<Input id="smtp-port" type="text" placeholder="587" bind:value={smtpPort} />
					</div>
				</div>

				<div class="grid grid-cols-2 gap-3">
					<div class="space-y-1.5">
						<Label for="smtp-user">Usuario</Label>
						<Input id="smtp-user" type="text" placeholder="you@example.com" bind:value={smtpUser} />
					</div>
					<div class="space-y-1.5">
						<Label for="smtp-pass">Contraseña</Label>
						<Input id="smtp-pass" type="password"
							placeholder={emailSettings?.smtp_pass_set ? '•••••••• (guardada)' : 'Ingresa la contraseña'}
							bind:value={smtpPass} />
						{#if emailSettings?.smtp_pass_set && !smtpPass}
							<p class="text-xs text-muted-foreground">Guardada — déjala en blanco para conservarla.</p>
						{/if}
					</div>
				</div>

				<div class="grid grid-cols-2 gap-3">
					<div class="space-y-1.5">
						<Label for="email-from">Dirección de remitente</Label>
						<Input id="email-from" type="email" placeholder="bookings@example.com" bind:value={emailFrom} />
					</div>
					<div class="space-y-1.5">
						<Label for="email-from-name">Nombre del remitente</Label>
						<Input id="email-from-name" type="text" placeholder="Calnode" bind:value={emailFromName} />
					</div>
				</div>

				<div class="space-y-2 rounded-md border p-3">
					<p class="text-xs font-medium text-muted-foreground">TLS / cifrado</p>
					<div class="flex items-center justify-between gap-4">
						<div>
							<Label for="smtp-starttls" class="cursor-pointer font-normal">STARTTLS</Label>
							<p class="text-xs text-muted-foreground">Recomendado para el puerto 587</p>
						</div>
						<Switch id="smtp-starttls" bind:checked={smtpStartTLS} />
					</div>
					<div class="flex items-center justify-between gap-4">
						<div>
							<Label for="smtp-tls" class="cursor-pointer font-normal">TLS implícito</Label>
							<p class="text-xs text-muted-foreground">Para el puerto 465 (SSL)</p>
						</div>
						<Switch id="smtp-tls" bind:checked={smtpTLS} />
					</div>
				</div>
				</div>
			</div>

			<div class="mt-5 flex flex-wrap items-center gap-3">
				<Button onclick={save} disabled={savingFlag.active}>
					{savingFlag.active ? 'Guardando…' : 'Guardar'}
				</Button>
				<Button variant="outline" onclick={test} disabled={testingFlag.active || !emailSettings?.enabled}>
					{testingFlag.active ? 'Enviando…' : 'Enviar correo de prueba'}
				</Button>
			</div>
		</div>
	</div>
{/if}

{/if}
