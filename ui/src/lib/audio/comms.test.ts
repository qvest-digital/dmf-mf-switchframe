import { describe, it, expect, vi, beforeEach } from 'vitest';
import { CommsAudioManager } from './comms';

// --- Mock browser APIs ---

// Minimal WritableStream writer
function createMockWriter() {
	return {
		write: vi.fn().mockResolvedValue(undefined),
		close: vi.fn().mockResolvedValue(undefined),
		releaseLock: vi.fn(),
	};
}

// Minimal ReadableStream reader that blocks indefinitely until released
function createMockReader() {
	let resolve: ((v: { value: undefined; done: boolean }) => void) | null = null;
	return {
		read: vi.fn(() => new Promise<{ value: undefined; done: boolean }>((r) => { resolve = r; })),
		cancel: vi.fn().mockResolvedValue(undefined),
		releaseLock: vi.fn(),
		_finish: () => resolve?.({ value: undefined, done: true }),
	};
}

function createMockBidiStream(writer: ReturnType<typeof createMockWriter>, reader: ReturnType<typeof createMockReader>) {
	return {
		writable: { getWriter: () => writer },
		readable: { getReader: () => reader },
	};
}

function createMockTransport(bidiStream: ReturnType<typeof createMockBidiStream>) {
	return {
		createBidirectionalStream: vi.fn().mockResolvedValue(bidiStream),
		closed: new Promise<void>(() => {}), // never resolves
	} as unknown as WebTransport;
}

// Stub AudioContext, getUserMedia, AudioEncoder, AudioDecoder
function stubBrowserAPIs() {
	const mockAudioCtx = {
		sampleRate: 48000,
		currentTime: 0,
		destination: {},
		createGain: () => ({
			gain: { value: 1.0 },
			connect: vi.fn(),
			disconnect: vi.fn(),
		}),
		createBuffer: () => ({
			getChannelData: () => new Float32Array(960),
		}),
		createBufferSource: () => ({
			buffer: null,
			connect: vi.fn(),
			start: vi.fn(),
		}),
		createMediaStreamSource: () => ({
			connect: vi.fn(),
		}),
		audioWorklet: {
			addModule: vi.fn().mockResolvedValue(undefined),
		},
		close: vi.fn().mockResolvedValue(undefined),
	};

	// AudioContext must be a constructor (called with `new`), so use a class.
	vi.stubGlobal('AudioContext', class MockAudioContext {
		sampleRate = mockAudioCtx.sampleRate;
		currentTime = mockAudioCtx.currentTime;
		destination = mockAudioCtx.destination;
		audioWorklet = mockAudioCtx.audioWorklet;
		createGain = mockAudioCtx.createGain;
		createBuffer = mockAudioCtx.createBuffer;
		createBufferSource = mockAudioCtx.createBufferSource;
		createMediaStreamSource = mockAudioCtx.createMediaStreamSource;
		close = mockAudioCtx.close;
	});

	vi.stubGlobal('AudioWorkletNode', vi.fn(function MockWorkletNode(this: Record<string, unknown>) {
		this.port = { onmessage: null };
		this.connect = vi.fn();
		this.disconnect = vi.fn();
	}));

	const mockTrack = { stop: vi.fn(), enabled: true };
	const mockStream = {
		getAudioTracks: () => [mockTrack],
		getTracks: () => [mockTrack],
	};
	vi.stubGlobal('navigator', {
		mediaDevices: {
			getUserMedia: vi.fn().mockResolvedValue(mockStream),
		},
	});

	vi.stubGlobal('AudioEncoder', vi.fn(function MockEncoder(this: Record<string, unknown>) {
		this.configure = vi.fn();
		this.encode = vi.fn();
		this.close = vi.fn();
	}));

	vi.stubGlobal('AudioDecoder', vi.fn(function MockDecoder(this: Record<string, unknown>) {
		this.configure = vi.fn();
		this.decode = vi.fn();
		this.close = vi.fn();
	}));

	vi.stubGlobal('AudioData', vi.fn(function MockAudioData(this: Record<string, unknown>) {
		this.close = vi.fn();
	}));

	// URL.createObjectURL / revokeObjectURL for worklet blob
	vi.stubGlobal('URL', {
		createObjectURL: vi.fn(() => 'blob:test'),
		revokeObjectURL: vi.fn(),
	});

	return { mockStream, mockTrack };
}

describe('CommsAudioManager', () => {
	let writerA: ReturnType<typeof createMockWriter>;
	let readerA: ReturnType<typeof createMockReader>;
	let transportA: WebTransport;

	let writerB: ReturnType<typeof createMockWriter>;
	let readerB: ReturnType<typeof createMockReader>;
	let transportB: WebTransport;

	beforeEach(() => {
		stubBrowserAPIs();

		writerA = createMockWriter();
		readerA = createMockReader();
		transportA = createMockTransport(createMockBidiStream(writerA, readerA));

		writerB = createMockWriter();
		readerB = createMockReader();
		transportB = createMockTransport(createMockBidiStream(writerB, readerB));
	});

	it('start sends hello handshake', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});
		await mgr.start(transportA);

		// Should have written the hello control message
		expect(writerA.write).toHaveBeenCalledTimes(1);
		const msg = writerA.write.mock.calls[0][0] as Uint8Array;
		expect(msg[0]).toBe(0x02); // MSG_CONTROL
		const payloadLen = (msg[1] << 8) | msg[2];
		const payload = JSON.parse(new TextDecoder().decode(msg.slice(3, 3 + payloadLen)));
		expect(payload.action).toBe('hello');
		expect(payload.operatorId).toBe('op1');

		await mgr.stop();
	});

	it('reconnect opens new bidi stream on new transport', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});
		await mgr.start(transportA);

		// Reconnect to transport B
		await mgr.reconnect(transportB);

		// Old writer should have been closed
		expect(writerA.close).toHaveBeenCalled();
		// Old reader should have been cancelled
		expect(readerA.cancel).toHaveBeenCalled();

		// New transport should have opened a bidi stream
		expect((transportB.createBidirectionalStream as ReturnType<typeof vi.fn>)).toHaveBeenCalled();

		// Should have sent hello on the new writer
		expect(writerB.write).toHaveBeenCalledTimes(1);
		const msg = writerB.write.mock.calls[0][0] as Uint8Array;
		expect(msg[0]).toBe(0x02); // MSG_CONTROL

		await mgr.stop();
	});

	it('reconnect preserves microphone capture (no new getUserMedia)', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});
		await mgr.start(transportA);

		const callsBefore = (navigator.mediaDevices.getUserMedia as ReturnType<typeof vi.fn>).mock.calls.length;

		await mgr.reconnect(transportB);

		const callsAfter = (navigator.mediaDevices.getUserMedia as ReturnType<typeof vi.fn>).mock.calls.length;
		expect(callsAfter).toBe(callsBefore); // no new mic request

		await mgr.stop();
	});

	it('reconnect is a no-op when not running', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});

		// Not started, reconnect should not throw
		await mgr.reconnect(transportB);
		expect((transportB.createBidirectionalStream as ReturnType<typeof vi.fn>)).not.toHaveBeenCalled();
	});

	it('reconnect resets decode timestamp for clean audio scheduling', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});
		await mgr.start(transportA);
		await mgr.reconnect(transportB);

		// Verify new codec instances were created (encoder/decoder re-configured)
		// The AudioEncoder constructor should have been called twice (start + reconnect)
		expect((globalThis.AudioEncoder as unknown as ReturnType<typeof vi.fn>).mock.calls.length).toBe(2);
		expect((globalThis.AudioDecoder as unknown as ReturnType<typeof vi.fn>).mock.calls.length).toBe(2);

		await mgr.stop();
	});

	it('concurrent reconnects are serialized (second is no-op)', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});
		await mgr.start(transportA);

		// Fire two reconnects concurrently — the second should be dropped
		const [r1, r2] = await Promise.all([
			mgr.reconnect(transportB),
			mgr.reconnect(transportB),
		]);

		// Only one bidi stream should have been opened on transportB
		expect((transportB.createBidirectionalStream as ReturnType<typeof vi.fn>).mock.calls.length).toBe(1);

		await mgr.stop();
	});

	it('isActive returns correct state', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});

		expect(mgr.isActive).toBe(false);
		await mgr.start(transportA);
		expect(mgr.isActive).toBe(true);
		await mgr.stop();
		expect(mgr.isActive).toBe(false);
	});

	it('stop is safe to call when not started', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});

		// Should not throw
		await mgr.stop();
		expect(mgr.isActive).toBe(false);
	});

	it('stop cleans up writer and reader', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});
		await mgr.start(transportA);
		await mgr.stop();

		expect(writerA.close).toHaveBeenCalled();
		expect(readerA.cancel).toHaveBeenCalled();
	});

	it('reconnect sends hello with correct operatorId', async () => {
		const mgr = new CommsAudioManager({
			operatorId: 'op42',
			operatorName: 'Bob',
		});
		await mgr.start(transportA);
		await mgr.reconnect(transportB);

		const msg = writerB.write.mock.calls[0][0] as Uint8Array;
		expect(msg[0]).toBe(0x02); // MSG_CONTROL
		const payloadLen = (msg[1] << 8) | msg[2];
		const payload = JSON.parse(new TextDecoder().decode(msg.slice(3, 3 + payloadLen)));
		expect(payload.operatorId).toBe('op42');

		await mgr.stop();
	});

	it('start fails gracefully when bidi stream creation fails', async () => {
		const badTransport = {
			createBidirectionalStream: vi.fn().mockRejectedValue(new Error('stream refused')),
			closed: new Promise<void>(() => {}),
		} as unknown as WebTransport;

		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});

		// Should throw or handle the error gracefully
		await expect(mgr.start(badTransport)).rejects.toThrow('stream refused');
		expect(mgr.isActive).toBe(false);
	});

	it('onStreamError is called when readLoop errors', async () => {
		const onStreamError = vi.fn();
		const onError = vi.fn();

		// Create a reader whose read() rejects to simulate a broken stream
		const errorReader = {
			read: vi.fn().mockRejectedValue(new Error('stream reset')),
			cancel: vi.fn().mockResolvedValue(undefined),
			releaseLock: vi.fn(),
		};
		const errorWriter = createMockWriter();
		const errorBidi = {
			writable: { getWriter: () => errorWriter },
			readable: { getReader: () => errorReader },
		};
		const errorTransport = {
			createBidirectionalStream: vi.fn().mockResolvedValue(errorBidi),
			closed: new Promise<void>(() => {}),
		} as unknown as WebTransport;

		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
			onError: (msg) => onError(msg),
			onStreamError,
		});

		await mgr.start(errorTransport);

		// readLoop runs asynchronously — give it a tick to hit the error
		await new Promise((r) => setTimeout(r, 10));

		expect(onStreamError).toHaveBeenCalled();
		expect(onError).toHaveBeenCalledWith(expect.stringContaining('Comms read error'));

		await mgr.stop();
	});

	it('onStreamError is called when writer.write fails', async () => {
		const onStreamError = vi.fn();
		const onError = vi.fn();

		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
			onError: (msg) => onError(msg),
			onStreamError,
		});

		// Start with a working transport
		await mgr.start(transportA);
		onStreamError.mockClear();
		onError.mockClear();

		// Now make writes fail
		writerA.write.mockRejectedValue(new Error('pipe broken'));

		// Trigger the onEncodedChunk callback by getting the encoder's output callback.
		// The AudioEncoder mock stores the constructor args.
		const encoderCalls = (globalThis.AudioEncoder as unknown as ReturnType<typeof vi.fn>).mock.calls;
		const lastEncoderInit = encoderCalls[encoderCalls.length - 1][0] as { output: (chunk: EncodedAudioChunk) => void };

		// Create a mock EncodedAudioChunk
		const mockChunk = {
			byteLength: 10,
			copyTo: vi.fn((dest: Uint8Array) => dest.fill(0)),
		} as unknown as EncodedAudioChunk;

		lastEncoderInit.output(mockChunk);

		// The write rejection is async — give it a tick
		await new Promise((r) => setTimeout(r, 10));

		expect(onStreamError).toHaveBeenCalled();
		expect(onError).toHaveBeenCalledWith(expect.stringContaining('Comms send error'));

		await mgr.stop();
	});

	it('reconnect tolerates old writer close failure', async () => {
		const failingWriter = createMockWriter();
		failingWriter.close.mockRejectedValue(new Error('already closed'));

		const failingReader = createMockReader();
		const failingTransport = createMockTransport(
			createMockBidiStream(failingWriter, failingReader),
		);

		const mgr = new CommsAudioManager({
			operatorId: 'op1',
			operatorName: 'Alice',
		});

		await mgr.start(failingTransport);

		// Reconnect should succeed even if old writer.close() rejects
		await mgr.reconnect(transportB);

		// New transport should have opened a bidi stream
		expect((transportB.createBidirectionalStream as ReturnType<typeof vi.fn>)).toHaveBeenCalled();
		expect(writerB.write).toHaveBeenCalledTimes(1);

		await mgr.stop();
	});
});
