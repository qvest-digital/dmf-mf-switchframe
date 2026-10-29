import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { coalesce, STALL_MS } from './coalesce';

/** A send whose requests settle when the test says so. */
function deferredSend() {
	const calls: { args: unknown[]; resolve: () => void; reject: () => void }[] = [];
	const send = vi.fn((...args: unknown[]) => new Promise<void>((resolve, reject) => {
		calls.push({ args, resolve, reject });
	}));
	return { send, calls };
}

describe('coalesce', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('sends the first value at once', () => {
		const { send } = deferredSend();
		const set = coalesce(send, 50);
		set(1);
		expect(send).toHaveBeenCalledTimes(1);
		expect(send).toHaveBeenCalledWith(1);
	});

	// A drag against a slow engine: a plain throttle starts a request every
	// window whether or not the last one answered, so they pile up and
	// settle in any order.
	it('keeps one request in flight and sends only the latest value next', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce(send, 50);
		set(1);
		for (let v = 2; v <= 30; v++) {
			set(v);
			await vi.advanceTimersByTimeAsync(16);
		}
		expect(send).toHaveBeenCalledTimes(1);

		calls[0].resolve();
		await vi.advanceTimersByTimeAsync(0);
		expect(send).toHaveBeenCalledTimes(2);
		expect(send).toHaveBeenLastCalledWith(30);
	});

	it('starts requests at least the interval apart', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce(send, 50);
		set(1);
		calls[0].resolve();
		await vi.advanceTimersByTimeAsync(5);
		set(2);
		expect(send).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(44);
		expect(send).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1);
		expect(send).toHaveBeenCalledTimes(2);
		expect(send).toHaveBeenLastCalledWith(2);
	});

	it('sends the final value after a failed request', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce(send, 0);
		set(1);
		set(2);
		calls[0].reject();
		await vi.advanceTimersByTimeAsync(0);
		expect(send).toHaveBeenLastCalledWith(2);
	});

	// A throttle shared by every channel strip replaced one channel's
	// pending final value with another's, so the first channel kept a value
	// from mid-drag.
	it('never drops one key\'s final value for another key\'s', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce((ch: string, v: number) => send(ch, v), 50, (ch) => ch);
		set('a', 1);
		set('a', 2);
		set('b', 7);
		expect(send).toHaveBeenCalledWith('b', 7);
		for (const c of calls) c.resolve();
		await vi.advanceTimersByTimeAsync(100);
		expect(send.mock.calls).toEqual([['a', 1], ['b', 7], ['a', 2]]);
	});

	it('sends nothing more once the last value went out', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce(send, 10);
		set(1);
		calls[0].resolve();
		await vi.advanceTimersByTimeAsync(1000);
		expect(send).toHaveBeenCalledTimes(1);
	});

	it('survives a send that throws', async () => {
		let n = 0;
		const send = vi.fn((_v: number) => { n++; if (n === 1) throw new Error('boom'); return Promise.resolve(); });
		const set = coalesce(send, 0);
		set(1);
		set(2);
		await vi.advanceTimersByTimeAsync(0);
		expect(send).toHaveBeenCalledTimes(2);
	});

	it('does not let an unanswered request hold the control forever', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce(send, 50);
		set(1);
		set(2);
		await vi.advanceTimersByTimeAsync(STALL_MS - 1);
		expect(send).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1);
		expect(send).toHaveBeenLastCalledWith(2);
		// The late answer to the first request starts nothing extra.
		calls[0].resolve();
		await vi.advanceTimersByTimeAsync(100);
		expect(send).toHaveBeenCalledTimes(2);
	});

	// A stalled request can reach the engine after the one that overtook
	// it; the engine then holds the older value.
	it('sends the newest value again when an overtaken request settles last', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce(send, 50);
		set(1);
		set(2);
		await vi.advanceTimersByTimeAsync(STALL_MS);
		expect(send).toHaveBeenLastCalledWith(2);
		calls[1].resolve();
		await vi.advanceTimersByTimeAsync(100);
		expect(send).toHaveBeenCalledTimes(2);
		calls[0].resolve();
		await vi.advanceTimersByTimeAsync(100);
		expect(send).toHaveBeenCalledTimes(3);
		expect(send).toHaveBeenLastCalledWith(2);
		calls[2].resolve();
		await vi.advanceTimersByTimeAsync(1000);
		expect(send).toHaveBeenCalledTimes(3);
	});

	it('does not repeat a value no request overtook', async () => {
		const { send, calls } = deferredSend();
		const set = coalesce(send, 50);
		set(1);
		await vi.advanceTimersByTimeAsync(STALL_MS * 2);
		calls[0].resolve();
		await vi.advanceTimersByTimeAsync(1000);
		expect(send).toHaveBeenCalledTimes(1);
	});
});
