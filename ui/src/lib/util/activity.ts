/**
 * What the operator is doing in this tab: when they last touched it, and
 * how many requests that change something are still on their way. A page
 * that reloads by itself checks both, so it never drops an action or an
 * upload in progress.
 */
let lastInput = Date.now();
let inFlight = 0;

if (typeof window !== 'undefined') {
	const touched = () => { lastInput = Date.now(); };
	for (const type of ['pointerdown', 'keydown', 'wheel', 'input']) {
		window.addEventListener(type, touched, { capture: true, passive: true });
	}
}

/** Milliseconds since the operator last used this tab. */
export function idleForMs(): number {
	return Date.now() - lastInput;
}

/** Counts a mutation or upload until the returned function is called. */
export function trackChange(): () => void {
	inFlight++;
	let done = false;
	return () => {
		if (done) return;
		done = true;
		inFlight--;
	};
}

/** Whether a mutation or upload is still in flight. */
export function changesInFlight(): boolean {
	return inFlight > 0;
}
