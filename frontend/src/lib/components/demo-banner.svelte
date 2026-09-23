<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { api } from '$lib/api';
	import { authStatus } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';

	let resetOpen = $state(false);
	let resetting = $state(false);

	async function doReset() {
		resetting = true;
		try {
			await api.post('/v1/demo/reset');
			toast.success('Demo reiniciada — recargando…');
			window.location.href = '/admin/';
		} catch (e: any) {
			toast.error(e.message ?? 'Error al reiniciar');
		} finally {
			resetting = false;
		}
	}
</script>

<ConfirmDialog
	bind:open={resetOpen}
	title="¿Reiniciar la demo ahora?"
	description="Se borran todos los tipos de atención, reservas y configuraciones, y se reemplazan con datos de ejemplo nuevos. Esto también ocurre automáticamente en un temporizador."
	confirmText="Reiniciar demo"
	onConfirm={doReset}
/>

<div class="flex items-center justify-between gap-3 border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-900">
	<p>
		<span class="font-semibold">Demo pública</span> — los datos aquí son visibles para todos y se
		reinician automáticamente. No ingreses nada privado.
		<a
			href="https://github.com/Calnode/calnode"
			target="_blank"
			rel="noopener noreferrer"
			class="ml-1 font-medium underline"
		>
			Ver código fuente
		</a>
	</p>
	<Button
		variant="outline"
		size="sm"
		class="shrink-0 border-amber-300 bg-white hover:bg-amber-100"
		onclick={() => (resetOpen = true)}
		disabled={resetting}
	>
		{resetting ? 'Reiniciando…' : 'Reiniciar demo ahora'}
	</Button>
</div>
