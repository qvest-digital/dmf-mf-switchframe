/**
 * Whether the engines have refused the page's bearer token. Once every
 * engine in use has refused it, the next request would be refused too:
 * requests made while the gate is closed are answered without reaching an
 * engine, and polls wait until a new token is set. An engine that refuses
 * while another still accepts, such as one the operator has not reconnected
 * to yet, closes nothing.
 */
let refused = false;
const refusedBy = new Set<string>();
let refusedHandler: (() => void) | null = null;
let waiters: (() => void)[] = [];

export function isTokenRefused(): boolean {
	return refused;
}

/**
 * Records that the engine at origin refused the token, and closes the gate
 * when every engine in engineOrigins has. The handler runs once per
 * closing, not per request.
 */
export function tokenRefusedBy(origin: string, engineOrigins: string[]): void {
	refusedBy.add(origin);
	if (refused || !engineOrigins.every((o) => refusedBy.has(o))) return;
	refused = true;
	refusedHandler?.();
}

/** Records that the engine at origin accepted the token. */
export function tokenAcceptedBy(origin: string): void {
	refusedBy.delete(origin);
}

/** Opens the gate for a new token and resumes whatever waited for one. */
export function tokenReplaced(): void {
	refusedBy.clear();
	if (!refused) return;
	refused = false;
	const resume = waiters;
	waiters = [];
	for (const w of resume) w();
}

/** Resolves once the gate is open. */
export function whenTokenAccepted(): Promise<void> {
	if (!refused) return Promise.resolve();
	return new Promise((resolve) => waiters.push(resolve));
}

export function setTokenRefusedHandler(handler: (() => void) | null): void {
	refusedHandler = handler;
}
