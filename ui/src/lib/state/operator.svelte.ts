import type { OperatorRole } from '$lib/api/types';
import { operatorRegister, operatorReconnect, operatorHeartbeat, setAuthToken } from '$lib/api/switch-api';

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
let heartbeatTimer: ReturnType<typeof setInterval> | undefined;

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

function startHeartbeat(): void {
	stopHeartbeat();
	heartbeatTimer = setInterval(() => {
		operatorHeartbeat().catch(() => {
			// Heartbeat failed — session may have expired
		});
	}, HEARTBEAT_INTERVAL);
}

function stopHeartbeat(): void {
	if (heartbeatTimer !== undefined) {
		clearInterval(heartbeatTimer);
		heartbeatTimer = undefined;
	}
}

export async function register(name: string, role: OperatorRole): Promise<void> {
	// Generate a client-side ID so both engines in a dual-engine setup
	// register the same operator ID, preventing lock divergence.
	const clientId = crypto.randomUUID();
	const result = await operatorRegister(name, role, clientId);
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
