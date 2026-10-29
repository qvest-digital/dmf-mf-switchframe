import { isTokenRefused } from '$lib/api/auth-gate';
import { hiddenForMs, idleForMs, reloadWouldLoseWork } from './activity';

export interface StaleBundleDeps {
	/** This page's build. */
	current: string;
	/** The build the engine that served this page serves now. */
	fetchVersion: () => Promise<string>;
	reload: () => void;
	/** Tells the operator a newer build is waiting; called once per build. */
	prompt: () => void;
	/** Survives a reload of this tab only (sessionStorage). */
	storage: Pick<Storage, 'getItem' | 'setItem'>;
	isTokenRefused?: () => boolean;
	idleForMs?: () => number;
	hiddenForMs?: () => number;
	reloadWouldLoseWork?: () => boolean;
}

export const STALE_CHECK_INTERVAL_MS = 60_000;
export const STALE_IDLE_MS = 5 * 60_000;
const RELOADED_FOR_KEY = 'switchframe_reloaded_for_build';

/**
 * A tab runs the build it loaded until it reloads, across any number of
 * engine upgrades, so a tab left open keeps every request bug its build
 * had long after the engines stopped serving it. Asks the engine that
 * served the page for its build every interval.
 *
 * Once that differs, the tab reloads only when nobody is using it and a
 * reload loses nothing: no input for idleMs, nothing reloadWouldLoseWork
 * reports (a change or upload in flight, an unsaved edit, comms, audio
 * being monitored), and either hidden for idleMs or holding a token the
 * engines refuse. Otherwise it tells the operator and leaves the reload to
 * them. It reloads by itself at most
 * once per build it finds: a reload that comes back on the old build, as
 * behind a cache, or while engines on different builds take turns serving
 * the page during a rollout, is not repeated. Returns a function that
 * stops it.
 */
export function watchStaleBundle(deps: StaleBundleDeps, intervalMs = STALE_CHECK_INTERVAL_MS, idleMs = STALE_IDLE_MS): () => void {
	const refused = deps.isTokenRefused ?? isTokenRefused;
	const idleFor = deps.idleForMs ?? idleForMs;
	const hiddenFor = deps.hiddenForMs ?? hiddenForMs;
	const busy = deps.reloadWouldLoseWork ?? reloadWouldLoseWork;
	let prompted = '';
	let stopped = false;

	const tick = async () => {
		let served: string;
		try {
			served = await deps.fetchVersion();
		} catch {
			return; // engine unreachable or mid-restart: ask again next time
		}
		if (stopped || !served || served === deps.current) return;

		const unused = idleFor() >= idleMs && !busy() && (refused() || hiddenFor() >= idleMs);
		if (unused && deps.storage.getItem(RELOADED_FOR_KEY) !== served) {
			deps.storage.setItem(RELOADED_FOR_KEY, served);
			stopped = true;
			clearInterval(timer);
			deps.reload();
		} else if (prompted !== served) {
			prompted = served;
			deps.prompt();
		}
	};

	const timer = setInterval(() => void tick(), intervalMs);
	return () => {
		stopped = true;
		clearInterval(timer);
	};
}

/**
 * This tab's sessionStorage, or a map that lasts until the page unloads
 * where the browser blocks site storage: reading sessionStorage then
 * throws, and the root layout would render nothing.
 */
export function tabStorage(): Pick<Storage, 'getItem' | 'setItem'> {
	try {
		const s = window.sessionStorage;
		s.getItem(RELOADED_FOR_KEY);
		return s;
	} catch {
		const m = new Map<string, string>();
		return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => void m.set(k, v) };
	}
}
