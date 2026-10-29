import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
	fetchOIDCConfig, consumeSignInFragment, tokenExpiry, isSignedIn,
	refreshToken, renewLapsedToken, renewRefusedToken, signInHandoff, startTokenRefresh, signOut,
} from './oidc';
import { isTokenRefused, tokenRefusedBy, tokenReplaced } from './auth-gate';
import { getAuthToken, setAuthToken } from './switch-api';
import { setEngineUrls } from './base-url';

const REFRESH_KEY = 'switchframe_oidc_refresh';

/** An unsigned JWT; the UI only reads exp, the engines verify. */
function jwt(expSeconds: number, extra: Record<string, unknown> = {}): string {
	const b64 = (o: unknown) => btoa(JSON.stringify(o)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
	return `${b64({ alg: 'RS256' })}.${b64({ exp: expSeconds, ...extra })}.sig`;
}

function jsonResponse(status: number, body: unknown) {
	return { ok: status >= 200 && status < 300, status, json: () => Promise.resolve(body) };
}

beforeEach(() => {
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
	localStorage.clear();
	setEngineUrls(null);
	history.replaceState(null, '', '/');
});

describe('fetchOIDCConfig', () => {
	it('reports sign-in and the logout URL the engine offers', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { enabled: true, logoutUrl: 'https://idp.example/logout' }));
		vi.stubGlobal('fetch', fetchMock);

		expect(await fetchOIDCConfig()).toEqual({ enabled: true, logoutUrl: 'https://idp.example/logout' });
		expect(fetchMock).toHaveBeenCalledWith('/auth/config');
	});

	it('reads an engine without the endpoint as disabled', async () => {
		// The UI's SPA fallback answers unknown paths with index.html.
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
			ok: true, status: 200, json: () => Promise.reject(new SyntaxError('Unexpected token <')),
		}));
		expect(await fetchOIDCConfig()).toEqual({ enabled: false });

		vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('network')));
		expect(await fetchOIDCConfig()).toEqual({ enabled: false });
	});
});

describe('consumeSignInFragment', () => {
	it('stores both tokens and removes them from the address bar', () => {
		const idToken = jwt(2_000_000_000);
		history.replaceState(null, '', `/?x=1#id_token=${idToken}&refresh_token=rt-1`);

		expect(consumeSignInFragment()).toBe(true);
		expect(getAuthToken()).toBe(idToken);
		expect(localStorage.getItem(REFRESH_KEY)).toBe('rt-1');
		expect(window.location.hash).toBe('');
		expect(window.location.search).toBe('?x=1');
	});

	it('leaves the control-plane #token= handoff alone', () => {
		history.replaceState(null, '', '/#token=abc');
		expect(consumeSignInFragment()).toBe(false);
		expect(getAuthToken()).toBeNull();
		expect(window.location.hash).toBe('#token=abc');
	});
});

describe('tokenExpiry', () => {
	it('reads exp from a JWT and nothing from other tokens', () => {
		expect(tokenExpiry(jwt(1_700_000_000))).toBe(1_700_000_000_000);
		expect(tokenExpiry('a'.repeat(64))).toBeNull();
		expect(tokenExpiry(null)).toBeNull();
	});

	it('tells a signed-in token from a pasted API token', () => {
		setAuthToken('0123456789abcdef');
		expect(isSignedIn()).toBe(false);
		setAuthToken(jwt(2_000_000_000));
		expect(isSignedIn()).toBe(true);
	});
});

describe('refreshToken', () => {
	beforeEach(() => {
		setAuthToken(jwt(1));
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		setEngineUrls([{ id: 'A', url: 'https://a.example' }, { id: 'B', url: 'https://b.example' }]);
	});

	it('refreshes at the peer engine when the page engine is down', async () => {
		const fresh = jwt(2_000_000_000);
		const fetchMock = vi.fn()
			.mockRejectedValueOnce(new TypeError('network'))
			.mockResolvedValueOnce(jsonResponse(200, { id_token: fresh, refresh_token: 'rt-2', expires_at: 2_000_000_000 }));
		vi.stubGlobal('fetch', fetchMock);

		expect(await refreshToken()).toEqual({ outcome: 'refreshed' });
		expect(fetchMock.mock.calls.map(c => c[0])).toEqual(['https://a.example/auth/refresh', 'https://b.example/auth/refresh']);
		expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ refresh_token: 'rt-1' });
		expect(getAuthToken()).toBe(fresh);
		expect(localStorage.getItem(REFRESH_KEY)).toBe('rt-2');
	});

	it('keeps the refresh token when the provider does not rotate it', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { id_token: jwt(2_000_000_000), refresh_token: '' })));
		await refreshToken();
		expect(localStorage.getItem(REFRESH_KEY)).toBe('rt-1');
	});

	it('stops at a 403 with the engine\'s reason instead of asking the peer', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(403, { error: 'alice holds no role' }));
		vi.stubGlobal('fetch', fetchMock);

		expect(await refreshToken()).toEqual({ outcome: 'forbidden', message: 'alice holds no role' });
		expect(fetchMock).toHaveBeenCalledTimes(1);
	});

	it('distinguishes a refused refresh token from unreachable engines', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, { error: 'refused' })));
		expect(await refreshToken()).toEqual({ outcome: 'rejected' });

		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(503, { error: 'provider unavailable' })));
		expect(await refreshToken()).toEqual({ outcome: 'unreachable' });
	});
});

describe('renewLapsedToken', () => {
	beforeEach(() => {
		localStorage.setItem(REFRESH_KEY, 'rt-1');
	});

	it('renews a token that expired while the page was closed, before any request', async () => {
		setAuthToken(jwt(Math.floor(Date.now() / 1000) - 3600));
		const fresh = jwt(2_000_000_000);
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id_token: fresh, refresh_token: 'rt-2' }));
		vi.stubGlobal('fetch', fetchMock);

		expect(await renewLapsedToken()).toEqual({ outcome: 'refreshed' });
		expect(fetchMock.mock.calls[0][0]).toBe(`${window.location.origin}/auth/refresh`);
		expect(getAuthToken()).toBe(fresh);
	});

	it('leaves a valid token and a pasted API token alone', async () => {
		const fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);

		setAuthToken(jwt(2_000_000_000));
		expect(await renewLapsedToken()).toBeNull();
		setAuthToken('a'.repeat(64));
		expect(await renewLapsedToken()).toBeNull();
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it('forgets a refresh token the provider refused', async () => {
		setAuthToken(jwt(1));
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, { error: 'refused' })));

		expect(await renewLapsedToken()).toEqual({ outcome: 'rejected' });
		expect(localStorage.getItem(REFRESH_KEY)).toBeNull();
	});
});

describe('refreshToken in flight', () => {
	// A refused request at page load renews while onMount renews the
	// lapsed token: two exchanges of one refresh token, the second refused
	// by a provider that rotates them, which then discarded the first's.
	it('shares one exchange between callers that overlap', async () => {
		setAuthToken(jwt(1));
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		const fresh = jwt(2_000_000_000);
		let n = 0;
		const fetchMock = vi.fn(async () => (++n === 1
			? jsonResponse(200, { id_token: fresh, refresh_token: 'rt-2' })
			: jsonResponse(401, { error: 'refresh token reused' })));
		vi.stubGlobal('fetch', fetchMock);

		const results = await Promise.all([refreshToken(), renewLapsedToken()]);

		expect(fetchMock).toHaveBeenCalledTimes(1);
		expect(results).toEqual([{ outcome: 'refreshed' }, { outcome: 'refreshed' }]);
		expect(localStorage.getItem(REFRESH_KEY)).toBe('rt-2');
		expect(getAuthToken()).toBe(fresh);
	});
});

describe('renewRefusedToken', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2031-01-01T00:00:00Z'));
	});

	afterEach(() => {
		tokenReplaced();
		vi.useRealTimers();
	});

	function refuse() {
		tokenRefusedBy(window.location.origin, [window.location.origin]);
	}

	// A renewal the engines refuse as well must not turn into a refresh
	// per refused request.
	it('renews a refused sign-in once, then leaves it to the operator', async () => {
		setAuthToken(jwt(Math.floor(Date.now() / 1000) + 30));
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		const fresh = jwt(2_000_000_000);
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id_token: fresh, refresh_token: 'rt-2' }));
		vi.stubGlobal('fetch', fetchMock);

		refuse();
		expect(await renewRefusedToken()).toEqual({ renewed: true });
		expect(isTokenRefused()).toBe(false);
		expect(getAuthToken()).toBe(fresh);

		refuse();
		await vi.advanceTimersByTimeAsync(30_000);
		expect(await renewRefusedToken()).toEqual({ renewed: false, message: null });
		expect(fetchMock).toHaveBeenCalledTimes(1);
		expect(isTokenRefused()).toBe(true);
	});

	it('leaves a pasted API token to the prompt without a refresh', async () => {
		setAuthToken('a'.repeat(64));
		const fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);

		refuse();
		expect(await renewRefusedToken()).toEqual({ renewed: false, message: null });
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it('reports an expired session when the provider refuses the refresh', async () => {
		// Clear of the renewal an earlier case made.
		vi.setSystemTime(new Date('2031-01-02T00:00:00Z'));
		setAuthToken(jwt(Math.floor(Date.now() / 1000) + 30));
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, { error: 'refused' })));

		refuse();
		expect(await renewRefusedToken()).toEqual({ renewed: false, message: 'Session expired' });
		expect(localStorage.getItem(REFRESH_KEY)).toBeNull();
	});
});

describe('signInHandoff', () => {
	it('carries both tokens, in the form consumeSignInFragment reads on the other engine', () => {
		const idToken = jwt(2_000_000_000);
		setAuthToken(idToken);
		localStorage.setItem(REFRESH_KEY, 'rt-1');

		const handoff = signInHandoff();
		localStorage.clear();
		history.replaceState(null, '', '/#' + handoff);
		expect(consumeSignInFragment()).toBe(true);
		expect(getAuthToken()).toBe(idToken);
		expect(localStorage.getItem(REFRESH_KEY)).toBe('rt-1');
	});

	it('is null for a pasted API token, which keeps the #token= handoff', () => {
		setAuthToken('a'.repeat(64));
		expect(signInHandoff()).toBeNull();
	});
});

describe('startTokenRefresh', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2030-01-01T00:00:00Z'));
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	const nowS = () => Math.floor(Date.now() / 1000);

	it('refreshes a minute before expiry and again after each refresh', async () => {
		setAuthToken(jwt(nowS() + 300));
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		const fetchMock = vi.fn().mockImplementation(() =>
			Promise.resolve(jsonResponse(200, { id_token: jwt(nowS() + 300), refresh_token: 'rt-1' })));
		vi.stubGlobal('fetch', fetchMock);
		const onSignedOut = vi.fn();

		const stop = startTokenRefresh(onSignedOut);
		await vi.advanceTimersByTimeAsync(239_000);
		expect(fetchMock).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(1_000);
		expect(fetchMock).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(240_000);
		expect(fetchMock).toHaveBeenCalledTimes(2);

		stop();
		await vi.advanceTimersByTimeAsync(600_000);
		expect(fetchMock).toHaveBeenCalledTimes(2);
		expect(onSignedOut).not.toHaveBeenCalled();
	});

	it('retries while no engine answers', async () => {
		setAuthToken(jwt(nowS() + 30));
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		const fetchMock = vi.fn().mockRejectedValue(new TypeError('network'));
		vi.stubGlobal('fetch', fetchMock);

		const stop = startTokenRefresh(vi.fn());
		await vi.advanceTimersByTimeAsync(0);
		expect(fetchMock).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(10_000);
		expect(fetchMock).toHaveBeenCalledTimes(2);
		stop();
	});

	it('signs out when the role is gone and forgets the refresh token', async () => {
		setAuthToken(jwt(nowS() + 30));
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(403, { error: 'alice holds no role' })));
		const onSignedOut = vi.fn();

		startTokenRefresh(onSignedOut);
		await vi.advanceTimersByTimeAsync(0);
		expect(onSignedOut).toHaveBeenCalledWith('alice holds no role');
		expect(localStorage.getItem(REFRESH_KEY)).toBeNull();
	});

	it('without a refresh token, signs out when the token lapses', async () => {
		setAuthToken(jwt(nowS() + 30));
		const onSignedOut = vi.fn();
		startTokenRefresh(onSignedOut);
		await vi.advanceTimersByTimeAsync(29_000);
		expect(onSignedOut).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(1_000);
		expect(onSignedOut).toHaveBeenCalledWith('Session expired');
	});

	it('leaves a pasted API token alone', async () => {
		setAuthToken('0123456789abcdef');
		const fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
		const onSignedOut = vi.fn();
		startTokenRefresh(onSignedOut);
		await vi.advanceTimersByTimeAsync(3_600_000);
		expect(fetchMock).not.toHaveBeenCalled();
		expect(onSignedOut).not.toHaveBeenCalled();
	});
});

describe('signOut', () => {
	it('clears the session and ends it at the provider', () => {
		const idToken = jwt(2_000_000_000);
		setAuthToken(idToken);
		localStorage.setItem(REFRESH_KEY, 'rt-1');
		const assign = vi.fn();
		vi.stubGlobal('location', { ...window.location, assign, reload: vi.fn() });

		signOut('https://idp.example/logout?client_id=switchframe&post_logout_redirect_uri=https%3A%2F%2Fsf.example%2F');

		expect(getAuthToken()).toBeNull();
		expect(localStorage.getItem(REFRESH_KEY)).toBeNull();
		const url = new URL(assign.mock.calls[0][0]);
		expect(url.searchParams.get('client_id')).toBe('switchframe');
		expect(url.searchParams.get('post_logout_redirect_uri')).toBe('https://sf.example/');
		expect(url.searchParams.get('id_token_hint')).toBe(idToken);
	});
});
