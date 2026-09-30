// onResume: admin pages re-load when the person comes back to them (owner report
// 30 Sep 2026: every change needed a manual reload to show).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { onResume } from './refresh';

function fakeEnv() {
	const doc = new EventTarget() as EventTarget & { visibilityState: DocumentVisibilityState };
	doc.visibilityState = 'visible';
	const win = new EventTarget();
	let t = 1_000_000;
	return {
		doc,
		win,
		now: () => t,
		advance: (ms: number) => (t += ms),
		hide: () => {
			doc.visibilityState = 'hidden';
			doc.dispatchEvent(new Event('visibilitychange'));
		},
		show: () => {
			doc.visibilityState = 'visible';
			doc.dispatchEvent(new Event('visibilitychange'));
		},
		focus: () => win.dispatchEvent(new Event('focus'))
	};
}

const flush = () => Promise.resolve();

afterEach(() => {
	vi.useRealTimers();
});

describe('onResume', () => {
	it('runs on visible and on focus once the minimum interval has passed', async () => {
		const env = fakeEnv();
		const fn = vi.fn();
		const stop = onResume(fn, { minIntervalMs: 5000, doc: env.doc as any, win: env.win as any, now: env.now });
		env.focus(); // just loaded: too soon
		expect(fn).not.toHaveBeenCalled();
		env.hide();
		env.advance(6000);
		env.show();
		expect(fn).toHaveBeenCalledTimes(1);
		await flush();
		env.focus(); // right after the previous run: too soon
		expect(fn).toHaveBeenCalledTimes(1);
		env.advance(5000);
		env.focus();
		expect(fn).toHaveBeenCalledTimes(2);
		stop();
	});

	it('does nothing while hidden and never runs twice at once', async () => {
		const env = fakeEnv();
		let release!: () => void;
		const fn = vi.fn(() => new Promise<void>((res) => (release = res)));
		const stop = onResume(fn, { minIntervalMs: 0, doc: env.doc as any, win: env.win as any, now: env.now });
		env.doc.visibilityState = 'hidden';
		env.focus();
		expect(fn).not.toHaveBeenCalled();
		env.doc.visibilityState = 'visible';
		env.focus();
		env.focus();
		expect(fn).toHaveBeenCalledTimes(1);
		release();
		await flush();
		await flush();
		env.focus();
		expect(fn).toHaveBeenCalledTimes(2);
		stop();
	});

	it('a failing fn does not stop later runs', async () => {
		const env = fakeEnv();
		const fn = vi.fn(async () => {
			throw new Error('offline');
		});
		const stop = onResume(fn, { minIntervalMs: 0, doc: env.doc as any, win: env.win as any, now: env.now });
		env.focus();
		await flush();
		await flush();
		env.focus();
		expect(fn).toHaveBeenCalledTimes(2);
		stop();
	});

	it('everyMs runs periodically only while visible', () => {
		vi.useFakeTimers();
		const env = fakeEnv();
		const fn = vi.fn();
		const now = () => Date.now();
		const stop = onResume(fn, { minIntervalMs: 5000, everyMs: 60_000, doc: env.doc as any, win: env.win as any, now });
		vi.advanceTimersByTime(59_000);
		expect(fn).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1_000);
		expect(fn).toHaveBeenCalledTimes(1);
		env.doc.visibilityState = 'hidden';
		vi.advanceTimersByTime(120_000);
		expect(fn).toHaveBeenCalledTimes(1);
		stop();
	});

	it('stops listening after cleanup', () => {
		const env = fakeEnv();
		const fn = vi.fn();
		const stop = onResume(fn, { minIntervalMs: 0, doc: env.doc as any, win: env.win as any, now: env.now });
		stop();
		env.advance(10_000);
		env.focus();
		env.show();
		expect(fn).not.toHaveBeenCalled();
	});
});
