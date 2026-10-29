import { untrack } from 'svelte';
import { isTokenRefused, whenTokenAccepted } from '$lib/api/auth-gate';

export interface PollOptions {
	/** Longest wait between runs while they fail. Default: 30s, or the interval if longer. */
	maxBackoffMs?: number;
	/** Run once at the start rather than one interval in. Default true. */
	immediate?: boolean;
}

/**
 * Runs task every intervalMs, counted from the end of the previous run, so
 * a poll never has more than one request outstanding. A run that throws
 * doubles the wait, up to maxBackoffMs; one that succeeds restores the
 * interval. While the engines refuse the page's token nothing runs, and the
 * poll resumes once a new token is set. Returns a function that stops it.
 *
 * The first run reads nothing reactively, so starting a poll from an
 * effect does not subscribe the effect to what the task reads.
 */
export function poll(task: () => Promise<unknown>, intervalMs: number, opts: PollOptions = {}): () => void {
	const maxBackoffMs = opts.maxBackoffMs ?? Math.max(intervalMs, 30_000);
	let stopped = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let delay = intervalMs;

	const run = async () => {
		timer = undefined;
		if (isTokenRefused()) {
			await whenTokenAccepted();
			delay = intervalMs;
		}
		if (stopped) return;
		try {
			await task();
			delay = intervalMs;
		} catch {
			delay = Math.min(delay * 2, maxBackoffMs);
		}
		if (stopped) return;
		if (isTokenRefused()) void run();
		else timer = setTimeout(run, delay);
	};

	if (opts.immediate ?? true) untrack(() => void run());
	else timer = setTimeout(run, delay);

	return () => {
		stopped = true;
		clearTimeout(timer);
	};
}
