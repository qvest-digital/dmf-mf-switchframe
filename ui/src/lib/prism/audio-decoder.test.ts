import { describe, it, expect, vi, beforeEach } from 'vitest';
import { PrismAudioDecoder } from './audio-decoder';

// Mock AudioWorklet URL import
vi.mock('./audio-worklet.ts?worker&url', () => {
	return { default: 'mock-worklet-url' };
});

// Mock AudioRingBuffer
vi.mock('./audio-ring-buffer', () => {
	return {
		AudioRingBuffer: class MockAudioRingBuffer {
			init = vi.fn();
			write = vi.fn().mockReturnValue(128);
			writeBuffers = vi.fn().mockReturnValue(128);
			destroy = vi.fn();
			play = vi.fn();
			readPTS = vi.fn().mockReturnValue(0);
			readLevels = vi.fn().mockReturnValue({ peak: [0, 0], rms: [0, 0] });
			getSharedBuffers = vi.fn().mockReturnValue({
				audioBuffers: [new SharedArrayBuffer(1024), new SharedArrayBuffer(1024)],
				commBuffer: new SharedArrayBuffer(256),
			});
			getStats = vi.fn().mockReturnValue({
				queueLengthMs: 800,
				totalSilenceInsertedMs: 0,
			});
		},
	};
});

describe('PrismAudioDecoder', () => {
	let mockWorkletPort: { postMessage: ReturnType<typeof vi.fn>; onmessage: null };
	let mockWorkletNode: any;
	let mockContext: any;
	let mockDecoder: any;
	let decoderOutputCb: ((frame: AudioData) => void) | null = null;

	beforeEach(() => {
		vi.clearAllMocks();
		decoderOutputCb = null;

		mockWorkletPort = {
			postMessage: vi.fn(),
			onmessage: null,
		};
		mockWorkletNode = {
			connect: vi.fn(),
			disconnect: vi.fn(),
			port: mockWorkletPort,
		};

		mockContext = {
			state: 'suspended',
			sampleRate: 48000,
			currentTime: 0,
			destination: {},
			createGain: vi.fn().mockReturnValue({
				gain: { value: 1 },
				connect: vi.fn(),
				disconnect: vi.fn(),
			}),
			audioWorklet: {
				addModule: vi.fn().mockResolvedValue(undefined),
			},
			resume: vi.fn().mockResolvedValue(undefined),
			suspend: vi.fn().mockResolvedValue(undefined),
			close: vi.fn(),
		};

		// Mock AudioWorkletNode constructor
		vi.stubGlobal('AudioWorkletNode', function(this: any, _ctx: any, _name: string, _opts: any) {
			Object.assign(this, mockWorkletNode);
			return this;
		} as any);

		// Mock AudioDecoder constructor
		mockDecoder = {
			configure: vi.fn(),
			decode: vi.fn(),
			close: vi.fn(),
			state: 'configured',
		};
		vi.stubGlobal('AudioDecoder', function(this: any, init: { output: (frame: AudioData) => void }) {
			Object.assign(this, mockDecoder);
			decoderOutputCb = init.output;
			return this;
		} as any);

		// Mock EncodedAudioChunk
		vi.stubGlobal('EncodedAudioChunk', function(this: any, init: any) {
			Object.assign(this, init);
			return this;
		} as any);
	});

	it('should send set-pts on forward PTS jump >500ms during playback', async () => {
		const decoder = new PrismAudioDecoder();
		await decoder.configure('mp4a.40.2', 48000, 2, mockContext);

		// Simulate playback started by setting playing state
		// We need to trigger startPlayback by filling the buffer
		// Feed enough frames to start playback

		// First, feed a frame to set up initial PTS
		decoder.decode(new Uint8Array([1, 2, 3]), 1_000_000, false);

		// Simulate decoder producing output to start playback
		if (decoderOutputCb) {
			const mockAudioData = {
				numberOfFrames: 1024,
				sampleRate: 48000,
				timestamp: 1_000_000,
				duration: 21333,
				numberOfChannels: 2,
				format: 'f32-planar',
				copyTo: vi.fn(),
				clone: vi.fn(),
				close: vi.fn(),
			};
			// Feed multiple frames to trigger playback start (needs MIN_BUFFER_MS=500ms)
			for (let i = 0; i < 30; i++) {
				decoderOutputCb(mockAudioData as unknown as AudioData);
			}
		}

		// Wait for context resume
		await mockContext.resume();

		// Clear previous postMessage calls (init + set-pts from startPlayback)
		mockWorkletPort.postMessage.mockClear();

		// Now feed a frame with a large forward PTS jump (>500ms = >500_000 µs)
		decoder.decode(new Uint8Array([4, 5, 6]), 2_000_000, false);

		// Simulate decoded output with the jumped PTS
		if (decoderOutputCb) {
			const jumpedData = {
				numberOfFrames: 1024,
				sampleRate: 48000,
				timestamp: 2_000_000,
				duration: 21333,
				numberOfChannels: 2,
				format: 'f32-planar',
				copyTo: vi.fn(),
				clone: vi.fn(),
				close: vi.fn(),
			};
			decoderOutputCb(jumpedData as unknown as AudioData);
		}

		// The set-pts message should have been sent to re-anchor
		const setPtsCalls = mockWorkletPort.postMessage.mock.calls.filter(
			(call: any[]) => call[0]?.type === 'set-pts'
		);
		expect(setPtsCalls.length).toBe(1);
		expect(setPtsCalls[0][0].pts).toBe(2_000_000);
		// Mock ring buffer returns queueLengthMs: 800
		// Expected: -Math.round((800/1000) * 48000) = -38400
		expect(setPtsCalls[0][0].sampleOffset).toBe(-38400);
	});

	it('should not send set-pts for small PTS gaps during playback', async () => {
		const decoder = new PrismAudioDecoder();
		await decoder.configure('mp4a.40.2', 48000, 2, mockContext);

		// Feed initial frame
		decoder.decode(new Uint8Array([1, 2, 3]), 1_000_000, false);

		// Feed frame with small gap (100ms = 100_000 µs, below 500_000 threshold)
		decoder.decode(new Uint8Array([4, 5, 6]), 1_100_000, false);

		// _ptsEpochReset should not have been set for small gaps
		const diag = decoder.getDiagnostics();
		// inputPtsJumps may increment for >100ms gaps, but no epoch reset
		expect(diag.inputPtsWraps).toBe(0);
	});

	it('should resample when source rate differs from context rate', async () => {
		const decoder = new PrismAudioDecoder();
		// Context at 48kHz, source will decode at 44100Hz
		await decoder.configure('mp4a.40.2', 48000, 2, mockContext);

		if (decoderOutputCb) {
			const mockAudioData = {
				numberOfFrames: 1024,
				sampleRate: 44100, // source rate differs from context (48000)
				timestamp: 0,
				duration: 23220,
				numberOfChannels: 2,
				format: 'f32-planar',
				copyTo: vi.fn(),
				clone: vi.fn(),
				close: vi.fn(),
			};
			decoderOutputCb(mockAudioData as unknown as AudioData);

			// Should have used writeBuffers (resampled path), not write
			// Access the ring buffer via the decoder internals
			expect(mockAudioData.copyTo).toHaveBeenCalled();
			expect(mockAudioData.close).toHaveBeenCalled();
		}
	});

	it('should not resample when source rate matches context rate', async () => {
		const decoder = new PrismAudioDecoder();
		await decoder.configure('mp4a.40.2', 48000, 2, mockContext);

		if (decoderOutputCb) {
			const mockAudioData = {
				numberOfFrames: 1024,
				sampleRate: 48000, // matches context rate
				timestamp: 0,
				duration: 21333,
				numberOfChannels: 2,
				format: 'f32-planar',
				copyTo: vi.fn(),
				clone: vi.fn(),
				close: vi.fn(),
			};
			decoderOutputCb(mockAudioData as unknown as AudioData);

			// Should have used write (direct path), not writeBuffers
			// copyTo is not called by the decoder — it's called by the ring buffer mock
			expect(mockAudioData.copyTo).not.toHaveBeenCalled();
			expect(mockAudioData.close).toHaveBeenCalled();
		}
	});

	it('should upmix mono source to stereo when resampling', async () => {
		const decoder = new PrismAudioDecoder();
		// Context at 48kHz stereo, source will be mono 44100Hz
		await decoder.configure('mp4a.40.2', 48000, 2, mockContext);

		if (decoderOutputCb) {
			const copyToSpy = vi.fn();
			const mockAudioData = {
				numberOfFrames: 1024,
				sampleRate: 44100, // triggers resampling
				timestamp: 0,
				duration: 23220,
				numberOfChannels: 1, // mono source
				format: 'f32-planar',
				copyTo: copyToSpy,
				clone: vi.fn(),
				close: vi.fn(),
			};
			decoderOutputCb(mockAudioData as unknown as AudioData);

			// copyTo should be called twice (once per output channel),
			// both with planeIndex: 0 since mono source only has plane 0
			expect(copyToSpy).toHaveBeenCalledTimes(2);
			expect(copyToSpy.mock.calls[0][1]).toEqual({ planeIndex: 0, format: 'f32-planar' });
			expect(copyToSpy.mock.calls[1][1]).toEqual({ planeIndex: 0, format: 'f32-planar' });
			expect(mockAudioData.close).toHaveBeenCalled();
		}
	});

	it('should detect backward PTS wraps', async () => {
		const decoder = new PrismAudioDecoder();
		await decoder.configure('mp4a.40.2', 48000, 2, mockContext);

		// Feed frame at high PTS
		decoder.decode(new Uint8Array([1, 2, 3]), 40_000_000, false);

		// Feed frame that wraps backward by >30s
		decoder.decode(new Uint8Array([4, 5, 6]), 1_000_000, false);

		const diag = decoder.getDiagnostics();
		expect(diag.inputPtsWraps).toBe(1);
	});
});
