<script lang="ts">
	// Brand mark for a calendar provider. Google and Microsoft use their official
	// marks (same paths as the sign-in buttons in routes/login) so the two surfaces
	// can't drift; CalDAV covers several vendors (iCloud, Fastmail, Nextcloud), so it
	// stays a neutral calendar glyph rather than picking one of them.
	let { provider, size = 20, muted = false }: {
		provider?: string;
		size?: number;
		muted?: boolean;
	} = $props();
</script>

{#if provider === 'google'}
	<svg width={size} height={size} viewBox="0 0 48 48" aria-hidden="true" class="shrink-0 {muted ? 'opacity-60' : ''}">
		<path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/>
		<path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/>
		<path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/>
		<path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.18 1.48-4.97 2.29-8.16 2.29-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/>
	</svg>
{:else if provider === 'microsoft'}
	<svg width={size} height={size} viewBox="0 0 23 23" aria-hidden="true" class="shrink-0 {muted ? 'opacity-60' : ''}">
		<path fill="#F25022" d="M1 1h10v10H1z"/>
		<path fill="#7FBA00" d="M12 1h10v10H12z"/>
		<path fill="#00A4EF" d="M1 12h10v10H1z"/>
		<path fill="#FFB900" d="M12 12h10v10H12z"/>
	</svg>
{:else}
	<svg xmlns="http://www.w3.org/2000/svg" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="shrink-0 {muted ? 'text-muted-foreground/60' : 'text-muted-foreground'}">
		<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
	</svg>
{/if}
