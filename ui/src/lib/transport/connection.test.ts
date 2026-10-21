import { describe, it, expect, vi, afterEach } from 'vitest';
import { createPrismConnection } from './connection';
import type { MoQTransportCallbacks } from '$lib/prism/moq-transport';

// vi.mock is hoisted above the imports, so the fake is too.
const { sessions, FakeSession } = vi.hoisted(() => {
// A stand-in for the MoQ session: connect() resolves when the test says so,
// and the test decides whether the session came up.
let sessions: FakeSession[];
class FakeSession {
	transport: object | null = null;
	closed = false;
	resolveConnect!: (up: boolean) => void;
	rejectConnect!: () => void;
	constructor(
		public streamKey: string,
		public callbacks: MoQTransportCallbacks,
		public videoPriority: number,
		public baseUrl: string | undefined,
		public onlyTracks: string[] | undefined,
	) {
		sessions.push(this);
	}
	connect(): Promise<void> {
		return new Promise((resolve, reject) => {
			this.resolveConnect = (up) => {
				if (up && !this.closed) this.transport = {};
				resolve();
			};
			// The transport came up, then the handshake failed.
			this.rejectConnect = () => {
				this.transport = {};
				reject(new Error('Subscribe error'));
			};
		});
	}
	getTransport() {
		return this.transport;
	}
	close() {
		this.closed = true;
		this.transport = null;
	}
	/** The engine ends the session. */
	drop() {
		this.transport = null;
		this.callbacks.onClose();
	}
}
sessions = [];
return { sessions, FakeSession };
});
vi.mock('$lib/prism/moq-transport', () => ({ MoQTransport: FakeSession }));

describe('PrismConnection', () => {
	afterEach(() => {
		vi.useRealTimers();
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it('should create connection with correct config', () => {
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: vi.fn(),
		});
		expect(conn).toBeDefined();
		expect(conn.state).toBe('disconnected');
	});

	it('should call onControlState when control data arrives', () => {
		const onControlState = vi.fn();
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState,
			onConnectionChange: vi.fn(),
		});

		// Simulate control track data
		const state = { seq: 1, programSource: 'cam1' };
		const data = new TextEncoder().encode(JSON.stringify(state));
		conn._handleControlData(data);

		expect(onControlState).toHaveBeenCalledWith(data);
	});

	it('should track connection state', () => {
		const states: string[] = [];
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: (s) => states.push(s),
		});

		conn._setConnectionState('connecting');
		conn._setConnectionState('connected');
		expect(states).toEqual(['connecting', 'connected']);
	});

	it('should expose current state via getter', () => {
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: vi.fn(),
		});

		expect(conn.state).toBe('disconnected');
		conn._setConnectionState('connecting');
		expect(conn.state).toBe('connecting');
		conn._setConnectionState('connected');
		expect(conn.state).toBe('connected');
		conn._setConnectionState('error');
		expect(conn.state).toBe('error');
	});

	it('should not call onConnectionChange for same state', () => {
		const onConnectionChange = vi.fn();
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange,
		});

		conn._setConnectionState('connecting');
		conn._setConnectionState('connecting');
		expect(onConnectionChange).toHaveBeenCalledTimes(1);
	});

	it('should expose transport as null before connect', () => {
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: vi.fn(),
		});
		expect(conn.transport).toBeNull();
	});

	it('should disconnect and reset state', () => {
		const states: string[] = [];
		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: (s) => states.push(s),
		});

		conn._setConnectionState('connected');
		conn.disconnect();
		expect(conn.state).toBe('disconnected');
		expect(states).toContain('disconnected');
	});

	it('subscribes to the control track only, on the engine it was given', async () => {
		sessions.length = 0;
		const conn = createPrismConnection({ url: 'https://engine-b.example.com', onControlState: vi.fn(), onConnectionChange: vi.fn() });
		const connecting = conn.connect();
		sessions[0].resolveConnect(true);
		await connecting;
		expect(sessions[0].baseUrl).toBe('https://engine-b.example.com');
		expect(sessions[0].onlyTracks).toEqual(['control']);
		expect(conn.state).toBe('connected');
		expect(conn.transport).not.toBeNull();
		conn.disconnect();
	});

	// The engine's state changes arrive on this session's control track.
	it('forwards control track data', async () => {
		sessions.length = 0;
		const onControlState = vi.fn();
		const conn = createPrismConnection({ url: 'https://e', onControlState, onConnectionChange: vi.fn() });
		const connecting = conn.connect();
		sessions[0].resolveConnect(true);
		await connecting;
		const data = new Uint8Array([0xff, 1, 2]);
		sessions[0].callbacks.onControlState!(data);
		expect(onControlState).toHaveBeenCalledWith(data);
		conn.disconnect();
	});

	it('reports an error and retries when the session does not come up', async () => {
		vi.useFakeTimers();
		sessions.length = 0;
		const states: string[] = [];
		const conn = createPrismConnection({ url: 'https://e', onControlState: vi.fn(), onConnectionChange: (s) => states.push(s) });
		const connecting = conn.connect();
		sessions[0].resolveConnect(false);
		await connecting;
		expect(conn.state).toBe('error');
		expect(sessions[0].closed).toBe(true);
		await vi.advanceTimersByTimeAsync(2500);
		expect(sessions).toHaveLength(2);
		conn.disconnect();
	});

	// A session whose subscription failed carries no control data; read as
	// connected it would keep the engine looking alive.
	it('reports an error and retries when the handshake fails after the transport is up', async () => {
		vi.useFakeTimers();
		sessions.length = 0;
		const conn = createPrismConnection({ url: 'https://e', onControlState: vi.fn(), onConnectionChange: vi.fn() });
		const connecting = conn.connect();
		sessions[0].rejectConnect();
		await connecting;
		expect(conn.state).toBe('error');
		expect(sessions[0].closed).toBe(true);
		expect(conn.transport).toBeNull();
		await vi.advanceTimersByTimeAsync(2500);
		expect(sessions).toHaveLength(2);
		conn.disconnect();
	});

	it('reconnects when the engine ends the session', async () => {
		vi.useFakeTimers();
		sessions.length = 0;
		const conn = createPrismConnection({ url: 'https://e', onControlState: vi.fn(), onConnectionChange: vi.fn() });
		const connecting = conn.connect();
		sessions[0].resolveConnect(true);
		await connecting;
		sessions[0].drop();
		expect(conn.state).toBe('disconnected');
		await vi.advanceTimersByTimeAsync(2500);
		expect(sessions).toHaveLength(2);
		conn.disconnect();
	});

	it('does not auto-reconnect after an explicit disconnect()', async () => {
		vi.useFakeTimers();
		sessions.length = 0;
		const conn = createPrismConnection({ url: 'https://e', onControlState: vi.fn(), onConnectionChange: vi.fn() });
		const connecting = conn.connect();
		sessions[0].resolveConnect(true);
		await connecting;
		conn.disconnect();
		expect(sessions[0].closed).toBe(true);
		await vi.advanceTimersByTimeAsync(60_000);
		expect(sessions).toHaveLength(1);
		expect(conn.state).toBe('disconnected');
	});

	it('closes and neither errors nor retries when disconnect() lands during connect()', async () => {
		vi.useFakeTimers();
		sessions.length = 0;
		const states: string[] = [];
		const conn = createPrismConnection({ url: 'https://e', onControlState: vi.fn(), onConnectionChange: (s) => states.push(s) });
		const connecting = conn.connect();
		conn.disconnect();
		sessions[0].resolveConnect(true);
		await connecting;
		expect(sessions[0].closed).toBe(true);
		expect(states).not.toContain('error');
		expect(states).not.toContain('connected');
		expect(conn.transport).toBeNull();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(sessions).toHaveLength(1);
	});
});
