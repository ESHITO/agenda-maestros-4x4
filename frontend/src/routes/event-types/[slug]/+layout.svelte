<script lang="ts">
	// Fork: SvelteKit keeps the same page component when only [slug] changes (the link from
	// a mentor's copy to its template, the goto after a rename), and the editor reads the
	// slug once and loads in onMount. Keying on the slug mounts a fresh editor instead.
	import { page } from '$app/stores';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();
</script>

{#key $page.params.slug}
	{@render children()}
{/key}
