import { isTokenRefused } from '$lib/api/auth-gate';

export interface StaleBundleDeps {
	/** Resolves true once the engine serves a build other than this page's (SvelteKit's updated.check). */
	check: () => Promise<boolean>;
	reload: () => void;
	isHidden: () => boolean;
	/** Tells the operator a newer build is waiting; called once. */
	prompt: () => void;
	isTokenRefused?: () => boolean;
}

export const STALE_CHECK_INTERVAL_MS = 60_000;
export const STALE_HIDDEN_RELOAD_MS = 5 * 60_000;

/**
 * A tab runs the build it loaded until it reloads, across any number of
 * engine upgrades, so a tab left open keeps every request bug its build
 * had long after the engines stopped serving it. Checks the engine's build
 * every interval. Once it differs, a tab nobody is using -- hidden for
 * hiddenReloadMs, or holding a token the engines refuse -- reloads; a tab
 * in use is asked to, so nothing reloads under an operator. Returns a
 * function that stops it.
 */
export function watchStaleBundle(
	deps: StaleBundleDeps,
	intervalMs = STALE_CHECK_INTERVAL_MS,
	hiddenReloadMs = STALE_HIDDEN_RELOAD_MS,
): () => void {
	const refused = deps.isTokenRefused ?? isTokenRefused;
	let stale = false;
	let prompted = false;
	let hiddenFor = 0;
	let stopped = false;

	const tick = async () => {
		hiddenFor = deps.isHidden() ? hiddenFor + intervalMs : 0;
		if (!stale) {
			try {
				stale = await deps.check();
			} catch {
				return; // engine unreachable: ask again next time
			}
			if (!stale || stopped) return;
		}
		if (refused() || hiddenFor >= hiddenReloadMs) {
			stopped = true;
			clearInterval(timer);
			deps.reload();
		} else if (!prompted) {
			prompted = true;
			deps.prompt();
		}
	};

	const timer = setInterval(() => void tick(), intervalMs);
	return () => {
		stopped = true;
		clearInterval(timer);
	};
}
