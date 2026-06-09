import { describe, it, expect, vi, afterEach } from 'vitest';
import { createPrismConnection } from './connection';

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

	it('does not auto-reconnect after an explicit disconnect()', async () => {
		vi.useFakeTimers();

		// cert-hash fetch returns a trusted cert (no serverCertificateHashes path).
		const mockFetch = vi.fn().mockResolvedValue({
			json: () => Promise.resolve({ trusted: true }),
		} as unknown as Response);
		vi.stubGlobal('fetch', mockFetch);

		// Controllable transport.closed so we can resolve it on demand (as the
		// WebTransport spec does when close() is called).
		let resolveClosed!: () => void;
		const WebTransportMock = vi.fn(function () {
			return {
				ready: Promise.resolve(),
				closed: new Promise<void>((resolve) => {
					resolveClosed = resolve;
				}),
				close: vi.fn(() => resolveClosed()),
			};
		});
		vi.stubGlobal('WebTransport', WebTransportMock);

		const conn = createPrismConnection({
			url: 'https://localhost:8080',
			onControlState: vi.fn(),
			onConnectionChange: vi.fn(),
		});

		// Establish the connection (fetch + WebTransport.ready resolve).
		const connecting = conn.connect();
		await vi.advanceTimersByTimeAsync(0);
		await connecting;
		expect(conn.state).toBe('connected');
		expect(WebTransportMock).toHaveBeenCalledTimes(1);

		// Deliberately disconnect. close() resolves transport.closed, which fires
		// the registered .then handler asynchronously.
		conn.disconnect();
		expect(conn.state).toBe('disconnected');

		// Flush the closed-handler microtask, then advance well past the retry
		// delay (2000ms + up to 20% jitter). A resurrected connection would call
		// connect() -> new WebTransport here.
		await vi.advanceTimersByTimeAsync(5000);

		expect(WebTransportMock).toHaveBeenCalledTimes(1);
		expect(conn.state).toBe('disconnected');
	});

	describe('disconnect() racing an in-flight connect()', () => {
		it('does not transition to error or retry when disconnect() lands while awaiting transport.ready', async () => {
			vi.useFakeTimers();

			const mockFetch = vi.fn().mockResolvedValue({
				json: () => Promise.resolve({ trusted: true }),
			} as unknown as Response);
			vi.stubGlobal('fetch', mockFetch);

			// transport.ready stays pending until close() rejects it — models
			// the multi-second connect window the standalone WebTransport hits.
			let rejectReady!: (e: Error) => void;
			const WebTransportMock = vi.fn(function () {
				return {
					ready: new Promise<void>((_, reject) => {
						rejectReady = reject;
					}),
					closed: new Promise<void>(() => {}),
					close: vi.fn(() => rejectReady(new Error('WebTransport closed'))),
				};
			});
			vi.stubGlobal('WebTransport', WebTransportMock);

			const states: string[] = [];
			const conn = createPrismConnection({
				url: 'https://localhost:8080',
				onControlState: vi.fn(),
				onConnectionChange: (s) => states.push(s),
			});

			const connecting = conn.connect();
			// Flush the cert fetch so connect() is now awaiting transport.ready.
			await vi.advanceTimersByTimeAsync(0);
			expect(WebTransportMock).toHaveBeenCalledTimes(1);

			// disconnect() while connect() is suspended on transport.ready.
			conn.disconnect();
			await connecting;

			// The catch path must not undo the disconnect: no 'error' state
			// (which would resurrect REST polling in ConnectionManager).
			expect(states).not.toContain('error');
			expect(conn.state).toBe('disconnected');

			// And no retry may be scheduled.
			await vi.advanceTimersByTimeAsync(60_000);
			expect(WebTransportMock).toHaveBeenCalledTimes(1);
			expect(conn.state).toBe('disconnected');
		});

		it('does not create (and leak) a WebTransport when disconnect() lands during the cert fetch', async () => {
			vi.useFakeTimers();

			let resolveFetch!: (r: unknown) => void;
			const mockFetch = vi.fn(
				() =>
					new Promise((resolve) => {
						resolveFetch = resolve;
					})
			);
			vi.stubGlobal('fetch', mockFetch);

			const WebTransportMock = vi.fn(function () {
				return {
					ready: Promise.resolve(),
					closed: new Promise<void>(() => {}),
					close: vi.fn(),
				};
			});
			vi.stubGlobal('WebTransport', WebTransportMock);

			const states: string[] = [];
			const conn = createPrismConnection({
				url: 'https://localhost:8080',
				onControlState: vi.fn(),
				onConnectionChange: (s) => states.push(s),
			});

			const connecting = conn.connect();
			// disconnect() while connect() is suspended on the cert fetch.
			conn.disconnect();

			resolveFetch({ json: () => Promise.resolve({ trusted: true }) });
			await connecting;
			await vi.advanceTimersByTimeAsync(0);

			// A transport created here would never be closed (disconnect already
			// ran) — connect() must bail out instead.
			expect(WebTransportMock).not.toHaveBeenCalled();
			expect(states).not.toContain('connected');
			expect(conn.state).toBe('disconnected');
			expect(conn.transport).toBeNull();
		});
	});
});
