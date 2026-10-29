import { untrack } from 'svelte';
import { isTokenRefused, whenTokenAccepted } from '$lib/api/auth-gate';

export interface PollOptions {
	/** Longest wait between runs while they fail. Default: 30s, or the interval if longer. */
	maxBackoffMs?: number;
	/** Run once at the start rather than one interval in. Default true. */
	immediate?: boolean;
	/** A run still going after this long is aborted and counts as failed. Default: 10s, or the interval if longer. */
	timeoutMs?: number;
}

/**
 * Runs task every intervalMs, counted from the end of the previous run, so
 * a poll never has more than one request outstanding. A run that throws or
 * outlasts timeoutMs doubles the wait, up to maxBackoffMs; one that
 * succeeds restores the interval. The task gets a signal that aborts on
 * the timeout and on stop, to pass to its requests. While the engines
 * refuse the page's token nothing runs, and the poll resumes once a new
 * token is set. Returns a function that stops it.
 *
 * The first run reads nothing reactively, so starting a poll from an
 * effect does not subscribe the effect to what the task reads.
 */
export function poll(task: (signal: AbortSignal) => Promise<unknown>, intervalMs: number, opts: PollOptions = {}): () => void {
	const maxBackoffMs = opts.maxBackoffMs ?? Math.max(intervalMs, 30_000);
	const timeoutMs = opts.timeoutMs ?? Math.max(intervalMs, 10_000);
	const stopped = new AbortController();
	let timer: ReturnType<typeof setTimeout> | undefined;
	let delay = intervalMs;

	const attempt = (): Promise<unknown> => {
		const run = new AbortController();
		const abortRun = () => run.abort();
		stopped.signal.addEventListener('abort', abortRun);
		let timeout: ReturnType<typeof setTimeout> | undefined;
		const timedOut = new Promise<never>((_, reject) => {
			timeout = setTimeout(() => {
				run.abort();
				reject(new Error(`poll run exceeded ${timeoutMs}ms`));
			}, timeoutMs);
		});
		return Promise.race([task(run.signal), timedOut]).finally(() => {
			clearTimeout(timeout);
			stopped.signal.removeEventListener('abort', abortRun);
		});
	};

	const run = async () => {
		timer = undefined;
		if (isTokenRefused()) {
			await whenTokenAccepted(stopped.signal);
			delay = intervalMs;
		}
		if (stopped.signal.aborted) return;
		try {
			await attempt();
			delay = intervalMs;
		} catch {
			delay = Math.min(delay * 2, maxBackoffMs);
		}
		if (stopped.signal.aborted) return;
		if (isTokenRefused()) void run();
		else timer = setTimeout(run, delay);
	};

	if (opts.immediate ?? true) untrack(() => void run());
	else timer = setTimeout(run, delay);

	return () => {
		stopped.abort();
		clearTimeout(timer);
	};
}
