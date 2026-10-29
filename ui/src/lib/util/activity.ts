/**
 * What the operator is doing in this tab: when they last touched it, how
 * long it has been hidden, and what a reload would cut off. A page that
 * reloads by itself checks all of it, so it never drops an action, an
 * upload, an unsaved edit or audio someone is listening to.
 */
let lastInput = Date.now();
let hiddenSince: number | null = null;
let inFlight = 0;
const holds = new Map<symbol, string>();

if (typeof window !== 'undefined') {
	const touched = () => { lastInput = Date.now(); };
	for (const type of ['pointerdown', 'keydown', 'wheel', 'input']) {
		window.addEventListener(type, touched, { capture: true, passive: true });
	}
	const visibility = () => { hiddenSince = document.hidden ? (hiddenSince ?? Date.now()) : null; };
	visibility();
	document.addEventListener('visibilitychange', visibility);
}

/** Milliseconds since the operator last used this tab. */
export function idleForMs(): number {
	return Date.now() - lastInput;
}

/** Milliseconds this tab has been hidden without a break; 0 while shown. */
export function hiddenForMs(): number {
	return hiddenSince === null ? 0 : Date.now() - hiddenSince;
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

/**
 * Marks state a reload would lose, an unsaved edit or audio being
 * monitored for instance, until the returned function is called. Use it
 * as an $effect's return value.
 */
export function holdReload(reason: string): () => void {
	const id = Symbol(reason);
	holds.set(id, reason);
	return () => { holds.delete(id); };
}

/** Whether a reload now would lose something: a change in flight or a hold. */
export function reloadWouldLoseWork(): boolean {
	return inFlight > 0 || holds.size > 0;
}

/** What holds the reload, for tests and diagnostics. */
export function reloadHolds(): string[] {
	return [...holds.values()];
}
