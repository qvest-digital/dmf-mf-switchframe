import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { playoutClips, operatorHeartbeat, setAuthToken, engineFetch, authHeaders } from './switch-api';
import { setEngineUrls } from './base-url';
import { isTokenRefused, setTokenAcceptedHandler, setTokenRefusedHandler, tokenReplaced } from './auth-gate';

const A = 'https://engine-a.example';
const B = 'https://engine-b.example';

function respond(status: number): Response {
	const body = status === 200 ? [] : { error: 'invalid token' };
	return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('refused token', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		localStorage.setItem('switchframe_operator_token', 'expired-sign-in');
		setEngineUrls([{ id: 'A', url: A }, { id: 'B', url: B }]);
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.unstubAllGlobals();
		setEngineUrls(null);
		setTokenRefusedHandler(null);
		tokenReplaced();
		localStorage.clear();
	});

	// An expired sign-in behind a clip list that fetched again on every
	// state snapshot sent 45 GET /api/playout/clips a second, each answered
	// 401 and then tried on the other engine as well.
	it('sends a refused token to each engine once, however often the caller asks', async () => {
		const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => respond(401));
		vi.stubGlobal('fetch', fetchMock);
		const refused = vi.fn();
		setTokenRefusedHandler(refused);

		for (let i = 0; i < 45 * 10; i++) {
			await expect(playoutClips()).rejects.toMatchObject({ status: 401 });
			await vi.advanceTimersByTimeAsync(1000 / 45);
		}

		expect(fetchMock.mock.calls.map((c) => String(c[0]))).toEqual([
			`${A}/api/playout/clips`,
			`${B}/api/playout/clips`,
		]);
		expect(refused).toHaveBeenCalledTimes(1);
	});

	// An engine the operator has not reconnected to yet refuses the token
	// the other one accepts; the page keeps working through that one.
	it('keeps sending while one engine still accepts the token', async () => {
		const fetchMock = vi.fn(async (url: string) => respond(url.startsWith(A) ? 401 : 200));
		vi.stubGlobal('fetch', fetchMock);

		for (let i = 0; i < 5; i++) await expect(playoutClips()).resolves.toEqual([]);

		expect(isTokenRefused()).toBe(false);
		expect(fetchMock.mock.calls.filter((c) => String(c[0]).startsWith(B))).toHaveLength(5);
	});

	it('sends again once a new token is set', async () => {
		const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => respond(401));
		vi.stubGlobal('fetch', fetchMock);
		await expect(playoutClips()).rejects.toMatchObject({ status: 401 });
		expect(isTokenRefused()).toBe(true);

		fetchMock.mockImplementation(async () => respond(200));
		setAuthToken('fresh-sign-in');

		await expect(playoutClips()).resolves.toEqual([]);
		expect(fetchMock).toHaveBeenCalledTimes(3);
		expect(fetchMock.mock.calls[2][1]?.headers).toMatchObject({ Authorization: 'Bearer fresh-sign-in' });
	});

	// Requests still in flight with the old token answer 401 after a new
	// token was set; counting them closed the gate on a token no engine had
	// seen, behind a sign-in prompt nothing took down.
	it('ignores a 401 for a token that has since been replaced', async () => {
		const pending: { auth: string | null; resolve: (r: Response) => void }[] = [];
		vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => new Promise<Response>((resolve) => {
			pending.push({ auth: new Headers(init?.headers).get('Authorization'), resolve });
		})));
		const reqs = [A, B, A, B].map((e) => engineFetch(`${e}/api/perf`, { headers: authHeaders() }));
		pending[0].resolve(respond(401));
		pending[1].resolve(respond(401));
		await Promise.all([reqs[0], reqs[1]]);
		expect(isTokenRefused()).toBe(true);

		setAuthToken('new-valid');
		pending[2].resolve(respond(401));
		pending[3].resolve(respond(401));
		await Promise.all([reqs[2], reqs[3]]);

		expect(pending.slice(2).map((p) => p.auth)).toEqual(['Bearer expired-sign-in', 'Bearer expired-sign-in']);
		expect(isTokenRefused()).toBe(false);
		void engineFetch(`${A}/api/perf`, { headers: authHeaders() });
		expect(pending).toHaveLength(5);
		expect(pending[4].auth).toBe('Bearer new-valid');
	});

	// A renewal by the refresh timer after the prompt went up left the
	// prompt over a working token.
	it('tells the page when a new token opens the gate', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => respond(401)));
		const accepted = vi.fn();
		setTokenAcceptedHandler(accepted);
		await expect(playoutClips()).rejects.toMatchObject({ status: 401 });
		expect(accepted).not.toHaveBeenCalled();

		setAuthToken('renewed-by-timer');

		expect(accepted).toHaveBeenCalledTimes(1);
		setTokenAcceptedHandler(null);
	});

	it('opens when another tab stores a new token', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => respond(401)));
		await expect(playoutClips()).rejects.toMatchObject({ status: 401 });
		expect(isTokenRefused()).toBe(true);

		localStorage.setItem('switchframe_operator_token', 'other-tab-sign-in');
		window.dispatchEvent(new StorageEvent('storage', {
			key: 'switchframe_operator_token', oldValue: 'expired-sign-in', newValue: 'other-tab-sign-in',
		}));

		expect(isTokenRefused()).toBe(false);
	});

	// The heartbeat's 401 means the engines no longer know the operator,
	// which the operator state answers with a reconnect.
	it('does not read an operator heartbeat refusal as a refused token', async () => {
		vi.stubGlobal('fetch', vi.fn(async (url: string) => respond(url.endsWith('/heartbeat') ? 401 : 200)));

		await expect(operatorHeartbeat()).rejects.toMatchObject({ status: 401 });

		expect(isTokenRefused()).toBe(false);
		await expect(playoutClips()).resolves.toEqual([]);
	});
});
