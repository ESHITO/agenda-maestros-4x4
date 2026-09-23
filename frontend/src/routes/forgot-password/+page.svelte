<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';

	let email = $state('');
	let submitting = $state(false);
	let message = $state('');
	let error = $state('');

	const isValidEmail = (e: string) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const addr = email.trim().toLowerCase();
		// Require a valid address up front — otherwise the request silently no-ops
		// (the endpoint returns the same generic message), which looks broken.
		if (!isValidEmail(addr)) {
			error = 'Ingresa una dirección de correo electrónico válida primero.';
			return;
		}
		error = '';
		submitting = true;
		try {
			const res = await fetch('/v1/auth/password/forgot', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ email: addr })
			});
			const data = await res.json().catch(() => ({}));
			message =
				data.message ||
				'Si existe una cuenta con ese correo electrónico, te enviaremos un enlace para restablecer la contraseña.';
		} catch {
			message = 'Si existe una cuenta con ese correo electrónico, te enviaremos un enlace para restablecer la contraseña.';
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>Olvidé mi contraseña — Calnode</title></svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/30 p-6">
	<div class="w-full max-w-sm">
		<div class="mb-8 text-center">
			<h1 class="text-xl font-semibold tracking-tight">¿Olvidaste tu contraseña?</h1>
			<p class="mt-2 text-sm text-muted-foreground">
				Ingresa el correo electrónico de tu cuenta y te enviaremos un enlace para establecer una nueva contraseña.
			</p>
		</div>

		{#if message}
			<div class="rounded-md bg-green-50 px-3 py-2.5 text-sm text-green-700">{message}</div>
			<p class="mt-4 text-center text-sm">
				<a href="/admin/login" class="text-muted-foreground hover:underline">Volver a iniciar sesión</a>
			</p>
		{:else}
			<form onsubmit={submit} class="space-y-4">
				<div class="space-y-1.5">
					<Label for="email">Correo electrónico</Label>
					<Input
						id="email"
						type="email"
						autocomplete="email"
						placeholder="tu@ejemplo.com"
						bind:value={email}
						oninput={() => (error = '')}
						aria-invalid={error ? 'true' : undefined}
						required
					/>
					{#if error}
						<p class="text-xs text-destructive">{error}</p>
					{/if}
				</div>
				<Button type="submit" class="h-11 w-full" disabled={submitting}>
					{submitting ? 'Enviando…' : 'Enviar enlace de restablecimiento'}
				</Button>
			</form>
			<p class="mt-4 text-center text-sm">
				<a href="/admin/login" class="text-muted-foreground hover:underline">Volver a iniciar sesión</a>
			</p>
		{/if}
	</div>
</div>
