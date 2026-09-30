// The availability editor's save queue (owner report 30 Sep 2026: a quick second change
// while the first was saving was silently lost). Every case drives the queue by hand with
// deferred promises, so the order of requests and responses is exact.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SaveQueue, errorMessage, type SaveStatus } from './save-queue';

type Range = { start_time: string; end_time: string };

function deferred() {
	let resolve!: () => void;
	let reject!: (e: unknown) => void;
	const promise = new Promise<void>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

// A fake server: each send waits until the test settles it.
function harness(opts: { ready?: boolean; confirmed?: Range; savedMs?: number } = {}) {
	const sent: Range[] = [];
	const calls: ReturnType<typeof deferred>[] = [];
	const statuses: SaveStatus[] = [];
	const errors: unknown[] = [];
	const q = new SaveQueue<Range>({
		send: (v) => {
			sent.push(v);
			const d = deferred();
			calls.push(d);
			return d.promise;
		},
		onStatus: (s) => statuses.push(s),
		onError: (e) => errors.push(e),
		savedMs: opts.savedMs ?? 20,
		ready: opts.ready,
		confirmed: opts.confirmed
	});
	return { q, sent, calls, statuses, errors };
}

const r = (s: string, e: string): Range => ({ start_time: s, end_time: e });
const tick = () => new Promise((res) => setTimeout(res, 0));

afterEach(() => {
	vi.useRealTimers();
});

describe('SaveQueue', () => {
	it('sends at once and reports saving, then saved, then idle', async () => {
		const h = harness();
		h.q.push(r('10:00', '17:00'));
		expect(h.sent).toEqual([r('10:00', '17:00')]);
		expect(h.q.status).toBe('saving');
		expect(h.q.busy).toBe(true);
		h.calls[0].resolve();
		await tick();
		expect(h.q.status).toBe('saved');
		expect(h.q.busy).toBe(false);
		await new Promise((res) => setTimeout(res, 40));
		expect(h.q.status).toBe('idle');
		expect(h.statuses).toEqual(['saving', 'saved', 'idle']);
	});

	it('never has two requests in flight and sends only the latest pending value', async () => {
		const h = harness();
		h.q.push(r('10:00', '17:00'));
		h.q.push(r('11:00', '17:00'));
		h.q.push(r('12:00', '17:00'));
		h.q.push(r('12:00', '18:00'));
		expect(h.sent).toHaveLength(1);
		h.calls[0].resolve();
		await tick();
		expect(h.sent).toEqual([r('10:00', '17:00'), r('12:00', '18:00')]);
		expect(h.q.status).toBe('saving');
		h.calls[1].resolve();
		await tick();
		expect(h.sent).toHaveLength(2);
		expect(h.q.status).toBe('saved');
	});

	it('does not resend what the server already has', async () => {
		const h = harness({ confirmed: r('09:00', '17:00') });
		h.q.push(r('09:00', '17:00'));
		expect(h.sent).toHaveLength(0);
		h.q.push(r('10:00', '17:00'));
		h.q.push(r('09:00', '17:00')); // back to the original while the first is in flight
		h.calls[0].resolve();
		await tick();
		// The server now has 10:00, so going back to 09:00 must be sent.
		expect(h.sent).toEqual([r('10:00', '17:00'), r('09:00', '17:00')]);
	});

	it('holds edits until the first save (the POST) lands, then sends the latest', async () => {
		const h = harness({ ready: false });
		h.q.push(r('10:00', '17:00'));
		h.q.push(r('10:00', '15:00'));
		expect(h.sent).toHaveLength(0);
		expect(h.q.busy).toBe(true);
		h.q.start(r('09:00', '17:00'));
		expect(h.sent).toEqual([r('10:00', '15:00')]);
		h.calls[0].resolve();
		await tick();
		expect(h.q.status).toBe('saved');
	});

	it('start() with nothing new to send just reports saved', () => {
		const h = harness({ ready: false });
		h.q.push(r('09:00', '17:00'));
		h.q.start(r('09:00', '17:00'));
		expect(h.sent).toHaveLength(0);
		expect(h.q.status).toBe('saved');
		expect(h.q.busy).toBe(false);
	});

	it('a failure with nothing newer ends in error, calls onError and can be retried', async () => {
		const h = harness({ confirmed: r('09:00', '17:00') });
		h.q.push(r('10:00', '17:00'));
		h.calls[0].reject(new Error('end_time must be after start_time'));
		await tick();
		expect(h.q.status).toBe('error');
		expect(h.q.error).toBe('end_time must be after start_time');
		expect(h.errors).toHaveLength(1);
		expect(h.q.busy).toBe(false);
		expect(h.q.retry()).toEqual(r('10:00', '17:00'));
		expect(h.sent).toEqual([r('10:00', '17:00'), r('10:00', '17:00')]);
		h.calls[1].resolve();
		await tick();
		expect(h.q.status).toBe('saved');
		expect(h.q.retry()).toBeNull();
	});

	it('a failure replaced by a newer edit sends the newer edit instead (last write wins)', async () => {
		const h = harness();
		h.q.push(r('10:00', '17:00'));
		h.q.push(r('11:00', '17:00'));
		h.calls[0].reject(new Error('boom'));
		await tick();
		expect(h.errors).toHaveLength(0);
		expect(h.sent).toEqual([r('10:00', '17:00'), r('11:00', '17:00')]);
		h.calls[1].resolve();
		await tick();
		expect(h.q.status).toBe('saved');
	});

	it('going back to the saved value while a save is in flight: that failure is not reported', async () => {
		const h = harness({ confirmed: r('09:00', '17:00') });
		h.q.push(r('10:00', '17:00'));
		h.q.push(r('09:00', '17:00')); // back to what the server has
		h.calls[0].reject(new Error('boom'));
		await tick();
		expect(h.sent).toEqual([r('10:00', '17:00')]);
		expect(h.q.status).toBe('saved');
		expect(h.q.error).toBe('');
		expect(h.errors).toHaveLength(0);
		// Nothing to retry: resending 10:00 would undo the person's latest choice.
		expect(h.q.retry()).toBeNull();
		expect(h.sent).toHaveLength(1);
	});

	it('after a failure, picking the saved value clears the error and Reintentar', async () => {
		const h = harness({ confirmed: r('09:00', '17:00') });
		h.q.push(r('10:00', '17:00'));
		h.calls[0].reject(new Error('boom'));
		await tick();
		expect(h.q.status).toBe('error');
		h.q.push(r('09:00', '17:00'));
		expect(h.sent).toHaveLength(1);
		expect(h.q.status).toBe('saved');
		expect(h.q.error).toBe('');
		expect(h.q.retry()).toBeNull();
	});

	it('idle() waits for the in-flight save and the pending one', async () => {
		const h = harness();
		h.q.push(r('10:00', '17:00'));
		h.q.push(r('11:00', '17:00'));
		let done = false;
		h.q.idle().then(() => (done = true));
		h.calls[0].resolve();
		await tick();
		expect(done).toBe(false);
		h.calls[1].resolve();
		await tick();
		expect(done).toBe(true);
	});

	it('dispose() drops what was not sent and resolves idle() when the flight settles', async () => {
		const h = harness();
		h.q.push(r('10:00', '17:00'));
		h.q.push(r('11:00', '17:00'));
		let done = false;
		h.q.idle().then(() => (done = true));
		h.q.dispose();
		h.calls[0].resolve();
		await tick();
		expect(done).toBe(true);
		expect(h.sent).toHaveLength(1);
		h.q.push(r('12:00', '17:00'));
		expect(h.sent).toHaveLength(1);
	});

	it('setConfirmed is ignored while busy', async () => {
		const h = harness();
		h.q.push(r('10:00', '17:00'));
		h.q.setConfirmed(r('08:00', '09:00'));
		h.calls[0].resolve();
		await tick();
		h.q.setConfirmed(r('08:00', '09:00'));
		h.q.push(r('08:00', '09:00'));
		expect(h.sent).toHaveLength(1);
	});

	it('errorMessage prefers the error text and falls back to Spanish', () => {
		expect(errorMessage(new Error('x'))).toBe('x');
		expect(errorMessage('y')).toBe('y');
		expect(errorMessage(null)).toBe('No se pudo guardar.');
	});
});
