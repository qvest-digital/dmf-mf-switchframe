import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { poll } from './poll';
import { tokenRefusedBy, tokenReplaced, tokenWaiterCount } from '$lib/api/auth-gate';

describe('poll', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		tokenReplaced();
		vi.useRealTimers();
	});

	// A poll on a timer of its own stacked requests behind a slow engine.
	it('starts the next run an interval after the previous one ends', async () => {
		let inFlight = 0;
		let maxInFlight = 0;
		const task = vi.fn(async () => {
			inFlight++;
			maxInFlight = Math.max(maxInFlight, inFlight);
			await new Promise((r) => setTimeout(r, 2500));
			inFlight--;
		});
		const stop = poll(task, 1000);

		await vi.advanceTimersByTimeAsync(10_000);
		stop();

		expect(maxInFlight).toBe(1);
		// Runs at 0, 3.5s and 7s, each taking 2.5s.
		expect(task).toHaveBeenCalledTimes(3);
	});

	it('backs off while the task fails and returns to the interval after a success', async () => {
		let failing = true;
		const runs: number[] = [];
		const start = Date.now();
		const stop = poll(async () => {
			runs.push(Date.now() - start);
			if (failing) throw new Error('unreachable');
		}, 1000, { maxBackoffMs: 8000 });

		await vi.advanceTimersByTimeAsync(31_000);
		expect(runs).toEqual([0, 2000, 6000, 14_000, 22_000, 30_000]);

		failing = false;
		runs.length = 0;
		await vi.advanceTimersByTimeAsync(10_500);
		stop();
		expect(runs).toEqual([38_000, 39_000, 40_000, 41_000]);
	});

	it('runs nothing while the token is refused and resumes once a new one is set', async () => {
		const task = vi.fn(async () => {});
		const stop = poll(task, 1000);
		await vi.advanceTimersByTimeAsync(0);
		expect(task).toHaveBeenCalledTimes(1);

		tokenRefusedBy('https://engine.example', ['https://engine.example']);
		await vi.advanceTimersByTimeAsync(60_000);
		expect(task).toHaveBeenCalledTimes(1);

		tokenReplaced();
		await vi.advanceTimersByTimeAsync(0);
		expect(task).toHaveBeenCalledTimes(2);
		stop();
	});

	it('lets go of a stopped poll that waits for a token', async () => {
		tokenRefusedBy('https://engine.example', ['https://engine.example']);
		const stops = Array.from({ length: 3 }, () => poll(async () => {}, 1000));
		await vi.advanceTimersByTimeAsync(0);
		expect(tokenWaiterCount()).toBe(3);

		for (const stop of stops) stop();
		await vi.advanceTimersByTimeAsync(0);

		expect(tokenWaiterCount()).toBe(0);
	});

	// A request that never answers held the panel still until the browser
	// gave up on it.
	it('aborts a run that hangs and counts it as failed', async () => {
		const signals: AbortSignal[] = [];
		const task = vi.fn((signal: AbortSignal) => {
			signals.push(signal);
			return new Promise<void>(() => {});
		});
		const stop = poll(task, 1000, { timeoutMs: 3000 });

		await vi.advanceTimersByTimeAsync(3000);
		expect(signals[0].aborted).toBe(true);
		// Next run after the doubled wait.
		await vi.advanceTimersByTimeAsync(1999);
		expect(task).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1);
		expect(task).toHaveBeenCalledTimes(2);
		stop();
		expect(signals[1].aborted).toBe(true);
	});

	it('runs nothing after stop', async () => {
		const task = vi.fn(async () => {});
		const stop = poll(task, 1000, { immediate: false });
		stop();
		await vi.advanceTimersByTimeAsync(10_000);
		expect(task).not.toHaveBeenCalled();
	});
});
