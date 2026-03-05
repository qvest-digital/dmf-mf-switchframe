/**
 * Creates a throttled version of a function that fires at most once per `ms` milliseconds.
 *
 * - Leading edge: the first call fires immediately.
 * - Trailing edge: if calls arrive during the throttle window, the last one is
 *   scheduled to fire when the window expires. This guarantees the final value
 *   (e.g. fader release position) is always sent to the server.
 */
export function throttle<T extends (...args: any[]) => any>(fn: T, ms: number): T {
	let lastCall = 0;
	let timer: ReturnType<typeof setTimeout> | null = null;
	let pendingArgs: any[] | null = null;

	return ((...args: any[]) => {
		const now = Date.now();
		const remaining = ms - (now - lastCall);

		if (remaining <= 0) {
			// Outside the throttle window -- fire immediately
			if (timer) {
				clearTimeout(timer);
				timer = null;
			}
			pendingArgs = null;
			lastCall = now;
			fn(...args);
		} else {
			// Inside the window -- always save the latest args
			pendingArgs = args;
			if (!timer) {
				// Schedule a trailing-edge call
				timer = setTimeout(() => {
					lastCall = Date.now();
					timer = null;
					const a = pendingArgs!;
					pendingArgs = null;
					fn(...a);
				}, remaining);
			}
		}
	}) as T;
}
