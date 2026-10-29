/**
 * OpenID Connect sign-in. The engine runs the provider flow under /auth/
 * and hands back the provider's ID token in the URL fragment. That token is
 * the bearer token for every engine: each verifies it on its own, so the
 * same token reaches Engine A and Engine B without a second sign-in.
 */
import { getAuthToken, setAuthToken } from './switch-api';
import { isTokenRefused } from './auth-gate';
import { getEngineUrls } from './base-url';
import { disconnect } from '$lib/state/operator.svelte';

const REFRESH_KEY = 'switchframe_oidc_refresh';
/** Refresh this long before the ID token expires. */
const REFRESH_MARGIN_MS = 60_000;
/** Retry interval while no engine can refresh. */
const RETRY_MS = 10_000;
/** A token refused again this soon after a renewal is not renewed again. */
const RENEW_AGAIN_AFTER_MS = 60_000;

export interface OIDCConfig {
	enabled: boolean;
	/** Provider logout URL, without id_token_hint. */
	logoutUrl?: string;
}

/**
 * Asks the page's engine whether it offers sign-in. An engine without the
 * endpoint serves the UI's index.html for /auth/config, which fails to
 * parse and reads as disabled.
 */
export async function fetchOIDCConfig(): Promise<OIDCConfig> {
	try {
		const res = await fetch('/auth/config');
		if (!res.ok) return { enabled: false };
		const body = await res.json();
		return {
			enabled: body?.enabled === true,
			logoutUrl: typeof body?.logoutUrl === 'string' ? body.logoutUrl : undefined,
		};
	} catch {
		return { enabled: false };
	}
}

/** Navigates to the engine's sign-in, which returns to this page. */
export function signIn(): void {
	window.location.assign('/auth/login');
}

/**
 * Stores the tokens /auth/callback put in the URL fragment and removes them
 * from the address bar and history. Returns whether a sign-in was consumed.
 */
export function consumeSignInFragment(): boolean {
	if (typeof window === 'undefined') return false;
	const hash = window.location.hash;
	if (!hash.startsWith('#id_token=')) return false;

	const params = new URLSearchParams(hash.slice(1));
	const idToken = params.get('id_token');
	if (!idToken) return false;
	setAuthToken(idToken);
	setRefreshToken(params.get('refresh_token'));
	history.replaceState(null, '', window.location.pathname + window.location.search);
	return true;
}

/** Expiry of a JWT in ms since the epoch; null for any other token. */
export function tokenExpiry(token: string | null): number | null {
	const parts = token?.split('.') ?? [];
	if (parts.length !== 3) return null;
	try {
		const b64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
		const claims = JSON.parse(atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4)));
		return typeof claims.exp === 'number' ? claims.exp * 1000 : null;
	} catch {
		return null;
	}
}

/** Whether the current bearer token came from a sign-in. */
export function isSignedIn(): boolean {
	return tokenExpiry(getAuthToken()) !== null;
}

export function clearRefreshToken(): void {
	setRefreshToken(null);
}

function setRefreshToken(token: string | null): void {
	if (token) localStorage.setItem(REFRESH_KEY, token);
	else localStorage.removeItem(REFRESH_KEY);
}

export type RefreshResult =
	| { outcome: 'refreshed' }
	/** Signed in, but no longer holds a role. */
	| { outcome: 'forbidden'; message: string }
	/** The provider refused the refresh token, or there is none. */
	| { outcome: 'rejected' }
	/** No engine answered. */
	| { outcome: 'unreachable' };

/**
 * Exchanges the stored refresh token for a new ID token at the page's
 * engine, then at the peer: while Engine A is down, Engine B keeps the
 * session alive.
 */
export async function refreshToken(): Promise<RefreshResult> {
	const refresh = localStorage.getItem(REFRESH_KEY);
	if (!refresh) return { outcome: 'rejected' };

	const engines = getEngineUrls()?.map(e => e.url) ?? [window.location.origin];
	for (const engine of engines) {
		let res: Response;
		try {
			res = await fetch(`${engine}/auth/refresh`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ refresh_token: refresh }),
			});
		} catch {
			continue;
		}
		if (res.ok) {
			const body = await res.json();
			setAuthToken(body.id_token);
			setRefreshToken(body.refresh_token || refresh);
			return { outcome: 'refreshed' };
		}
		// Both engines share the provider; their answer would be the same.
		if (res.status === 403) {
			const body = await res.json().catch(() => ({}));
			return { outcome: 'forbidden', message: body.error ?? 'No switchframe role' };
		}
		if (res.status === 400 || res.status === 401) return { outcome: 'rejected' };
	}
	return { outcome: 'unreachable' };
}

/**
 * Renews a sign-in token that lapsed while the page was closed, so the first
 * request after a reload carries a valid one instead of opening on the
 * prompt. Returns null when nothing needed renewing.
 */
export async function renewLapsedToken(): Promise<RefreshResult | null> {
	const expiry = tokenExpiry(getAuthToken());
	if (expiry === null || expiry - REFRESH_MARGIN_MS > Date.now()) return null;
	const result = await refreshToken();
	if (result.outcome === 'forbidden' || result.outcome === 'rejected') clearRefreshToken();
	return result;
}

let lastRenewalAt = -Infinity;

/**
 * Answers the engines refusing the page's token. A sign-in is renewed with
 * one refresh; one refused again within RENEW_AGAIN_AFTER_MS, a refresh
 * that fails, or a token that is not from a sign-in leaves the operator to
 * sign in, with the message to show.
 */
export async function renewRefusedToken(): Promise<{ renewed: true } | { renewed: false; message: string | null }> {
	const now = Date.now();
	if (!isSignedIn() || now - lastRenewalAt < RENEW_AGAIN_AFTER_MS) return { renewed: false, message: null };
	lastRenewalAt = now;
	const result = await refreshToken();
	switch (result.outcome) {
	case 'refreshed':
		// A provider that handed back the refused token renewed nothing.
		return isTokenRefused() ? { renewed: false, message: 'Session expired' } : { renewed: true };
	case 'forbidden':
		clearRefreshToken();
		return { renewed: false, message: result.message };
	case 'rejected':
		clearRefreshToken();
		return { renewed: false, message: 'Session expired' };
	case 'unreachable':
		return { renewed: false, message: 'Session expired' };
	}
}

/**
 * The URL fragment that carries this sign-in to another engine's page:
 * localStorage is per origin, so without the refresh token the session
 * there would end when the ID token expires. Null when not signed in.
 */
export function signInHandoff(): string | null {
	const idToken = getAuthToken();
	if (tokenExpiry(idToken) === null) return null;
	const params = new URLSearchParams({ id_token: idToken! });
	const refresh = localStorage.getItem(REFRESH_KEY);
	if (refresh) params.set('refresh_token', refresh);
	return params.toString();
}

/**
 * Keeps a signed-in token fresh: refreshes ahead of expiry and calls
 * onSignedOut when the session cannot continue. A token that is not from a
 * sign-in is left alone. Returns a function that stops the timer.
 */
export function startTokenRefresh(onSignedOut: (message?: string) => void): () => void {
	let timer: ReturnType<typeof setTimeout> | undefined;
	let stopped = false;

	const schedule = (delayMs: number) => {
		if (!stopped) timer = setTimeout(tick, Math.max(0, delayMs));
	};

	const scheduleFromToken = () => {
		const expiry = tokenExpiry(getAuthToken());
		if (expiry === null) return;
		if (!localStorage.getItem(REFRESH_KEY)) {
			// Nothing to refresh with: sign in again once the token lapses.
			timer = setTimeout(() => onSignedOut('Session expired'), Math.max(0, expiry - Date.now()));
			return;
		}
		schedule(expiry - REFRESH_MARGIN_MS - Date.now());
	};

	const tick = async () => {
		const result = await refreshToken();
		if (stopped) return;
		switch (result.outcome) {
		case 'refreshed':
			scheduleFromToken();
			break;
		case 'unreachable':
			schedule(RETRY_MS);
			break;
		case 'forbidden':
			clearRefreshToken();
			onSignedOut(result.message);
			break;
		case 'rejected':
			clearRefreshToken();
			onSignedOut('Session expired');
			break;
		}
	};

	scheduleFromToken();
	return () => {
		stopped = true;
		clearTimeout(timer);
	};
}

/** Forgets the session here and ends it at the provider. */
export function signOut(logoutUrl?: string): void {
	const idToken = getAuthToken();
	clearRefreshToken();
	disconnect();
	if (!logoutUrl) {
		window.location.reload();
		return;
	}
	const url = new URL(logoutUrl);
	if (idToken) url.searchParams.set('id_token_hint', idToken);
	window.location.assign(url.toString());
}
