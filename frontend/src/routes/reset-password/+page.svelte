<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';

	let token = $state('');
	let password = $state('');
	let confirm = $state('');
	let submitting = $state(false);
	let error = $state('');

	onMount(() => {
		token = $page.url.searchParams.get('token') ?? '';
		if (!token) {
			error = 'A este enlace de restablecimiento le falta el token. Solicita uno nuevo abajo.';
		}
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (password.length < 8) {
			error = 'La contraseña debe tener al menos 8 caracteres.';
			return;
		}
		if (password !== confirm) {
			error = 'Las dos contraseñas no coinciden.';
			return;
		}
		error = '';
		submitting = true;
		try {
			const res = await fetch('/v1/auth/password/reset', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ token, new_password: password })
			});
			if (res.ok) {
				// The reset signs the user straight in.
				window.location.href = '/admin';
			} else {
				const data = await res.json().catch(() => ({}));
				error = data.error || 'No se pudo restablecer tu contraseña. El enlace pudo haber caducado.';
			}
		} catch {
			error = 'No se pudo restablecer tu contraseña. Inténtalo de nuevo.';
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>Establecer una nueva contraseña — Calnode</title></svelte:head>

<div class="flex min-h-screen items-center justify-center bg-muted/30 p-6">
	<div class="w-full max-w-sm">
		<div class="mb-8 text-center">
			<h1 class="text-xl font-semibold tracking-tight">Establece una nueva contraseña</h1>
			<p class="mt-2 text-sm text-muted-foreground">Elige una contraseña de al menos 8 caracteres.</p>
		</div>

		{#if error && !token}
			<div class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
			<p class="mt-4 text-center text-sm">
				<a href="/admin/forgot-password" class="text-muted-foreground hover:underline"
					>Solicitar un nuevo enlace de restablecimiento</a
				>
			</p>
		{:else}
			<form onsubmit={submit} class="space-y-4">
				<div class="space-y-1.5">
					<Label for="password">Nueva contraseña</Label>
					<Input
						id="password"
						type="password"
						autocomplete="new-password"
						bind:value={password}
						required
					/>
				</div>
				<div class="space-y-1.5">
					<Label for="confirm">Confirmar nueva contraseña</Label>
					<Input
						id="confirm"
						type="password"
						autocomplete="new-password"
						bind:value={confirm}
						required
					/>
				</div>
				{#if error}
					<div class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
				{/if}
				<Button type="submit" class="h-11 w-full" disabled={submitting || !token}>
					{submitting ? 'Estableciendo…' : 'Establecer nueva contraseña'}
				</Button>
			</form>
		{/if}
	</div>
</div>
