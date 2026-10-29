import type { OperatorRole } from '$lib/api/types';
import { operatorRegister, operatorReconnect, operatorHeartbeat, setAuthToken, SwitchApiError } from '$lib/api/switch-api';
import { poll } from '$lib/util/poll';

const STORAGE_KEY = 'switchframe_operator_token';
const SESSION_CACHE_KEY = 'switchframe_operator_session';
const HEARTBEAT_INTERVAL = 10_000; // 10s

export interface OperatorSession {
	id: string;
	name: string;
	role: OperatorRole;
	token: string;
}

let session = $state<OperatorSession | null>(null);
let stopHeartbeatPoll: (() => void) | undefined;

function getStoredToken(): string | null {
	if (typeof localStorage === 'undefined') return null;
	return localStorage.getItem(STORAGE_KEY);
}

function clearStoredToken(): void {
	localStorage.removeItem(STORAGE_KEY);
}

function cacheSession(s: OperatorSession): void {
	try {
		sessionStorage.setItem(SESSION_CACHE_KEY, JSON.stringify({
			id: s.id, name: s.name, role: s.role,
		}));
	} catch { /* private browsing — ignore */ }
}

function getCachedSession(): Partial<OperatorSession> | null {
	try {
		const raw = sessionStorage.getItem(SESSION_CACHE_KEY);
		return raw ? JSON.parse(raw) : null;
	} catch { return null; }
}

function clearCachedSession(): void {
	try { sessionStorage.removeItem(SESSION_CACHE_KEY); } catch { /* ignore */ }
}

// A 401 means the engines no longer know the operator: the heartbeat
// stops and one reconnect is tried, which restarts it if the operator is
// still known. Any other failure backs the heartbeat off.
async function heartbeat(): Promise<void> {
	try {
		await operatorHeartbeat();
	} catch (e) {
		if (e instanceof SwitchApiError && e.status === 401) {
			stopHeartbeat();
			void reconnect();
			return;
		}
		throw e;
	}
}

function startHeartbeat(): void {
	stopHeartbeat();
	stopHeartbeatPoll = poll(heartbeat, HEARTBEAT_INTERVAL, { immediate: false });
}

function stopHeartbeat(): void {
	stopHeartbeatPoll?.();
	stopHeartbeatPoll = undefined;
}

/** A bearer token in the form the engine generates: 32 random bytes as hex. */
export function newOperatorToken(): string {
	const bytes = crypto.getRandomValues(new Uint8Array(32));
	return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export async function register(name: string, role: OperatorRole): Promise<void> {
	// Generate the ID and the token client-side so both engines in a
	// dual-engine setup register the same operator under the same token:
	// lock state stays in step, and the token held here is valid on both.
	const clientId = crypto.randomUUID();
	const result = await operatorRegister(name, role, clientId, newOperatorToken());
	session = {
		id: result.id,
		name: result.name,
		role: result.role,
		token: result.token,
	};
	setAuthToken(result.token);
	cacheSession(session);
	startHeartbeat();
}

export async function reconnect(): Promise<boolean> {
	const token = getStoredToken();
	if (!token) return false;

	// Instant display from cache while API reconnects
	const cached = getCachedSession();
	if (cached?.id && cached?.name && cached?.role) {
		session = { id: cached.id, name: cached.name, role: cached.role as OperatorRole, token };
	}

	// Set as auth token before reconnecting
	setAuthToken(token);

	try {
		const result = await operatorReconnect();
		session = {
			id: result.id,
			name: result.name,
			role: result.role,
			token,
		};
		cacheSession(session);
		startHeartbeat();
		return true;
	} catch {
		// Don't clear the stored token on reconnect failure —
		// it's also the API auth token. Clearing it would cause
		// all subsequent API calls to fail with 401.
		stopHeartbeat();
		session = null;
		clearCachedSession();
		return false;
	}
}

export function disconnect(): void {
	stopHeartbeat();
	clearStoredToken();
	session = null;
}

export function getSession(): OperatorSession | null {
	return session;
}

export function isRegistered(): boolean {
	return session !== null;
}

export function hasStoredToken(): boolean {
	return getStoredToken() !== null;
}

export function destroy(): void {
	stopHeartbeat();
}
