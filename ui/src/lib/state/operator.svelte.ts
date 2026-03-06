import type { OperatorRole } from '$lib/api/types';
import { operatorRegister, operatorReconnect, operatorHeartbeat, setAuthToken } from '$lib/api/switch-api';

const STORAGE_KEY = 'switchframe_operator_token';
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
	if (typeof sessionStorage === 'undefined') return null;
	return sessionStorage.getItem(STORAGE_KEY);
}

function storeToken(token: string): void {
	sessionStorage.setItem(STORAGE_KEY, token);
}

function clearStoredToken(): void {
	sessionStorage.removeItem(STORAGE_KEY);
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
	const result = await operatorRegister(name, role);
	session = {
		id: result.id,
		name: result.name,
		role: result.role,
		token: result.token,
	};
	// setAuthToken now writes to the same key as storeToken (switchframe_operator_token)
	setAuthToken(result.token);
	startHeartbeat();
}

export async function reconnect(): Promise<boolean> {
	const token = getStoredToken();
	if (!token) return false;

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
		startHeartbeat();
		return true;
	} catch {
		clearStoredToken();
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
