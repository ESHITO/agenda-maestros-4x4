<script lang="ts">
	import { page } from '$app/stores';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';

	let { slug }: { slug: string } = $props();

	// The widget derives its API base from the script's own origin, so the
	// instance origin is all the snippet needs.
	const embedOrigin = $derived($page.url.origin);
	const inlineSnippet = $derived(
		`<script src="${embedOrigin}/embed.js" async><\/script>\n<calnode-booking slug="${slug}"></calnode-booking>`
	);
	const popupSnippet = $derived(
		`<script src="${embedOrigin}/embed.js" async><\/script>\n<button data-calnode-popup="${slug}">Reservar una llamada</button>`
	);
	let copied = $state('');
	function copyEmbed(kind: string, text: string) {
		navigator.clipboard.writeText(text).then(() => {
			copied = kind;
			setTimeout(() => { if (copied === kind) copied = ''; }, 1500);
		}).catch(() => toast.error('No se pudo copiar al portapapeles'));
	}
</script>

<!-- Embed -->
<div class="mt-8">
	<h2 class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Insertar</h2>
	<p class="mb-3 text-sm text-muted-foreground">Agrega este widget de reservas a tu sitio web — se renderiza en línea, sin iframe. Pégalo antes de <code>&lt;/body&gt;</code>.</p>
	<div class="space-y-5 rounded-lg border bg-card p-6">
		{#each [{ key: 'inline', label: 'En línea', hint: 'Inserta el calendario de reservas directamente en la página.', code: inlineSnippet }, { key: 'popup', label: 'Botón emergente', hint: 'Un botón que abre el widget de reservas en una ventana modal.', code: popupSnippet }] as snip}
			<div>
				<div class="mb-1.5 flex items-center justify-between">
					<div>
						<span class="text-sm font-medium">{snip.label}</span>
						<span class="ml-2 text-xs text-muted-foreground">{snip.hint}</span>
					</div>
					<Button variant="outline" size="sm" onclick={() => copyEmbed(snip.key, snip.code)}>
						{copied === snip.key ? '¡Copiado!' : 'Copiar'}
					</Button>
				</div>
				<pre class="overflow-x-auto rounded-md bg-muted px-3 py-2.5 text-xs leading-relaxed"><code>{snip.code}</code></pre>
			</div>
		{/each}
		<p class="text-xs text-muted-foreground">El widget llama a la API pública de reservas de esta instancia. Para restringir qué sitios pueden insertarlo, configura <code>EMBED_ALLOWED_ORIGINS</code>.</p>
	</div>
</div>
