// Owner report (30 Sep 2026): a mentor sharing his screen had to reload the page after
// every change to see it. Admin pages loaded their data once, on mount, and never again.
//
// onResume re-runs a page's (quiet) load when the person comes back to it - the tab turns
// visible again or the window regains focus - as long as the last run is older than
// `minIntervalMs`, and, with `everyMs`, also every so often while the tab is visible.
// It never runs `fn` twice at once, and it returns the cleanup, so a page writes:
//
//     onMount(() => onResume(() => load({ quiet: true }), { minIntervalMs: 5000 }));
//
// (Svelte calls the function onMount returns when the page is destroyed.)

export type ResumeOptions = {
	/** A resume within this long of the previous run does nothing. Default 5 s. */
	minIntervalMs?: number;
	/** Also run every this many ms while the page is visible. Omitted or 0 = never. */
	everyMs?: number;
	/** Test seams; the real document, window and clock by default. */
	doc?: Pick<Document, 'visibilityState' | 'addEventListener' | 'removeEventListener'>;
	win?: Pick<Window, 'addEventListener' | 'removeEventListener'>;
	now?: () => number;
};

export const DEFAULT_RESUME_INTERVAL_MS = 5_000;

export function onResume(fn: () => unknown, opts: ResumeOptions = {}): () => void {
	const doc = opts.doc ?? (typeof document !== 'undefined' ? document : undefined);
	const win = opts.win ?? (typeof window !== 'undefined' ? window : undefined);
	const now = opts.now ?? (() => Date.now());
	const minInterval = opts.minIntervalMs ?? DEFAULT_RESUME_INTERVAL_MS;
	if (!doc || !win) return () => {};

	// The page has just loaded its data when this is set up, so that counts as a run.
	let lastRun = now();
	let running = false;
	let stopped = false;

	const visible = () => doc.visibilityState !== 'hidden';

	async function run() {
		if (stopped || running) return;
		running = true;
		lastRun = now();
		try {
			await fn();
		} catch {
			// The page's own load reports its errors; a failed refresh must not break the next.
		} finally {
			running = false;
		}
	}

	function onWake() {
		if (!visible()) return;
		if (now() - lastRun < minInterval) return;
		void run();
	}

	doc.addEventListener('visibilitychange', onWake);
	win.addEventListener('focus', onWake);

	let timer: ReturnType<typeof setInterval> | null = null;
	if (opts.everyMs && opts.everyMs > 0) {
		const every = opts.everyMs;
		timer = setInterval(() => {
			// A hidden tab is not refreshed; coming back to it fires onWake instead.
			if (!visible()) return;
			// A resume (or a manual refresh through fn) ran recently: wait for the next tick.
			if (now() - lastRun < every - 50) return;
			void run();
		}, every);
	}

	return () => {
		stopped = true;
		doc.removeEventListener('visibilitychange', onWake);
		win.removeEventListener('focus', onWake);
		if (timer !== null) clearInterval(timer);
	};
}
