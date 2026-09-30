// One save queue per edited thing (in Disponibilidad: per weekly block).
//
// Owner report (30 Sep 2026): the availability editor disabled a block's two selects
// while its PATCH was in flight (0.3-1 s in production), so a quick second change was
// silently lost and the mentor thought nothing had been saved. With this queue the
// controls never lock:
//
// - saves are serialized: at most one request in flight per queue;
// - they coalesce: edits made meanwhile collapse into the LATEST value, sent once the
//   in-flight request settles (last write wins; intermediate values are never sent);
// - a value equal to what the server already has is not sent again;
// - a queue can start "not ready" (a block whose POST has not returned an id yet): edits
//   are kept and sent once `start()` says the first save landed;
// - a failure with nothing newer pending ends in status 'error' (the value is kept for
//   `retry()`) and calls `onError`, so the page can re-sync from the server;
// - an edit back to the server's value settles the queue: it clears a failed save (no
//   stale error, no "Reintentar" that would resend an abandoned value).
//
// Framework-free on purpose, so it is unit-tested without mounting the page.

export type SaveStatus = 'idle' | 'saving' | 'saved' | 'error';

export type SaveQueueOptions<V> = {
	/** Sends one value to the server. A rejection is a failed save. */
	send: (value: V) => Promise<unknown>;
	/** Called on every status change, with the error message when status is 'error'. */
	onStatus?: (status: SaveStatus, error: string) => void;
	/** Called after a failed save that nothing newer replaced. */
	onError?: (error: unknown, value: V) => void;
	equals?: (a: V, b: V) => boolean;
	/** How long 'saved' is shown before going back to 'idle'. Default 2 s. */
	savedMs?: number;
	/** false = hold edits until start() (the thing does not exist on the server yet). */
	ready?: boolean;
	/** What the server already has, if known. */
	confirmed?: V;
};

export const SAVED_FLASH_MS = 2_000;

export function errorMessage(e: unknown, fallback = 'No se pudo guardar.'): string {
	if (e instanceof Error && e.message) return e.message;
	if (typeof e === 'string' && e) return e;
	return fallback;
}

export class SaveQueue<V> {
	#opts: SaveQueueOptions<V>;
	#equals: (a: V, b: V) => boolean;
	#pending: { value: V } | null = null;
	#inFlight = false;
	#ready: boolean;
	#confirmed: { value: V } | null;
	#failed: { value: V } | null = null;
	#status: SaveStatus = 'idle';
	#error = '';
	#disposed = false;
	#savedTimer: ReturnType<typeof setTimeout> | null = null;
	#idleWaiters: (() => void)[] = [];

	constructor(opts: SaveQueueOptions<V>) {
		this.#opts = opts;
		this.#equals = opts.equals ?? ((a, b) => JSON.stringify(a) === JSON.stringify(b));
		this.#ready = opts.ready ?? true;
		this.#confirmed = opts.confirmed === undefined ? null : { value: opts.confirmed };
	}

	get status(): SaveStatus {
		return this.#status;
	}
	get error(): string {
		return this.#error;
	}
	/** True while a save is in flight, an edit waits to be sent, or the queue is not ready. */
	get busy(): boolean {
		return this.#inFlight || this.#pending !== null || !this.#ready;
	}
	get ready(): boolean {
		return this.#ready;
	}

	/** Records the latest value to save and sends it as soon as the queue allows. */
	push(value: V): void {
		if (this.#disposed) return;
		this.#pending = { value };
		this.#failed = null;
		// Picking back what the server already has settles a failed save: the person's latest
		// choice is saved, so the error (and its "Reintentar") must go.
		if (this.#pump() === 'settled' && this.#status === 'error') this.#setStatus('saved');
	}

	/** The first save landed (e.g. the POST returned the id) with `confirmed`: send what waited. */
	start(confirmed?: V): void {
		if (this.#disposed) return;
		if (confirmed !== undefined) this.#confirmed = { value: confirmed };
		this.#ready = true;
		if (this.#pump() !== 'sent' && !this.#inFlight) {
			this.#setStatus('saved');
		}
	}

	/** Re-sends the value of the last failed save. Returns it (to show it again), or null. */
	retry(): V | null {
		const f = this.#failed;
		if (!f) return null;
		// Whatever the server holds now, the retried value must be sent.
		this.#confirmed = null;
		this.push(f.value);
		return f.value;
	}

	/** The server's value, learned by a re-sync while this queue was not busy. */
	setConfirmed(value: V): void {
		if (this.busy) return;
		this.#confirmed = { value };
	}

	/** Resolves once nothing is in flight or waiting (immediately if so already). */
	idle(): Promise<void> {
		if (!this.#inFlight && this.#pending === null) return Promise.resolve();
		return new Promise((resolve) => this.#idleWaiters.push(resolve));
	}

	/** Drops anything not yet sent; the request in flight (if any) still settles. */
	dispose(): void {
		this.#disposed = true;
		this.#pending = null;
		if (this.#savedTimer !== null) clearTimeout(this.#savedTimer);
		this.#savedTimer = null;
		if (!this.#inFlight) this.#flushIdle();
	}

	// Starts the next send if allowed. 'sent' = one started; 'settled' = the pending value
	// equals what the server already has, so it was consumed without a request (the person's
	// latest choice is saved); 'none' = nothing was consumed.
	#pump(): 'sent' | 'settled' | 'none' {
		if (!this.#ready || this.#inFlight || this.#pending === null || this.#disposed) return 'none';
		const { value } = this.#pending;
		this.#pending = null;
		if (this.#confirmed && this.#equals(this.#confirmed.value, value)) {
			this.#failed = null;
			this.#flushIdle();
			return 'settled';
		}
		this.#inFlight = true;
		this.#setStatus('saving');
		void this.#send(value);
		return 'sent';
	}

	async #send(value: V): Promise<void> {
		let failure: { error: unknown } | null = null;
		try {
			await this.#opts.send(value);
			this.#confirmed = { value };
		} catch (e) {
			failure = { error: e };
		}
		this.#inFlight = false;
		if (this.#disposed) {
			this.#flushIdle();
			return;
		}
		// A newer edit waits: it replaces this one, whatever happened to it.
		const next = this.#pump();
		if (next === 'sent' || this.#inFlight) return;
		// The newer edit equals what the server has (e.g. the person went back to the saved
		// value while this one was in flight): their latest choice is saved, so this value's
		// failure is not theirs any more - no error, and nothing for "Reintentar" to resend.
		if (failure && next !== 'settled') {
			this.#failed = { value };
			this.#setStatus('error', errorMessage(failure.error));
			this.#flushIdle();
			this.#opts.onError?.(failure.error, value);
			return;
		}
		this.#setStatus('saved');
		this.#flushIdle();
	}

	#setStatus(status: SaveStatus, error = ''): void {
		if (this.#savedTimer !== null) {
			clearTimeout(this.#savedTimer);
			this.#savedTimer = null;
		}
		this.#status = status;
		this.#error = error;
		this.#opts.onStatus?.(status, error);
		if (status === 'saved') {
			this.#savedTimer = setTimeout(() => {
				this.#savedTimer = null;
				if (this.#status === 'saved') this.#setStatus('idle');
			}, this.#opts.savedMs ?? SAVED_FLASH_MS);
		}
	}

	#flushIdle(): void {
		if (this.#inFlight || this.#pending !== null) return;
		const waiters = this.#idleWaiters;
		this.#idleWaiters = [];
		for (const w of waiters) w();
	}
}
