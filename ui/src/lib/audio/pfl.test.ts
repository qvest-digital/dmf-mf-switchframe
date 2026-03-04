import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createPFLManager } from './pfl';

// Mock the PrismAudioDecoder since it uses WebCodecs, AudioWorklet, and
// SharedArrayBuffer which are not available in the jsdom test environment.
vi.mock('$lib/prism/audio-decoder', () => {
	return {
		PrismAudioDecoder: class MockPrismAudioDecoder {
			configure = vi.fn().mockResolvedValue(undefined);
			decode = vi.fn();
			setMuted = vi.fn();
			isMuted = vi.fn().mockReturnValue(true);
			enableMetering = vi.fn();
			disableMetering = vi.fn();
			getLevels = vi.fn().mockReturnValue({ peak: [0, 0], rms: [0, 0], peakHold: [0, 0], channels: 2 });
			getPlaybackPTS = vi.fn().mockReturnValue(-1);
			getStats = vi.fn().mockReturnValue({ queueLengthMs: 0, totalSilenceInsertedMs: 0, isPlaying: false });
			reset = vi.fn();
			setSuspended = vi.fn();
		},
	};
});

// Mock AudioContext
const mockAudioContext = {
	state: 'suspended',
	sampleRate: 48000,
	resume: vi.fn().mockResolvedValue(undefined),
	close: vi.fn().mockResolvedValue(undefined),
	createGain: vi.fn().mockReturnValue({
		gain: { value: 1 },
		connect: vi.fn(),
		disconnect: vi.fn(),
	}),
	destination: {},
	audioWorklet: {
		addModule: vi.fn().mockResolvedValue(undefined),
	},
};

vi.stubGlobal('AudioContext', class MockAudioContext {
	state = mockAudioContext.state;
	sampleRate = mockAudioContext.sampleRate;
	resume = mockAudioContext.resume;
	close = mockAudioContext.close;
	createGain = mockAudioContext.createGain;
	destination = mockAudioContext.destination;
	audioWorklet = mockAudioContext.audioWorklet;
});

describe('PFLManager', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('should start with no active PFL', () => {
		const pfl = createPFLManager();
		expect(pfl.activeSource).toBeNull();
	});

	it('should enable PFL for a source', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		pfl.enablePFL('cam1');
		expect(pfl.activeSource).toBe('cam1');
	});

	it('should disable PFL', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		pfl.enablePFL('cam1');
		pfl.disablePFL();
		expect(pfl.activeSource).toBeNull();
	});

	it('should switch PFL between sources', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		await pfl.addSource('cam2');
		pfl.enablePFL('cam1');
		pfl.enablePFL('cam2');
		expect(pfl.activeSource).toBe('cam2');
	});

	it('should mute previous source when switching PFL', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		await pfl.addSource('cam2');

		pfl.enablePFL('cam1');
		const decoder1 = pfl.getDecoder('cam1');
		expect(decoder1!.setMuted).toHaveBeenCalledWith(false);

		pfl.enablePFL('cam2');
		// cam1 should be re-muted
		expect(decoder1!.setMuted).toHaveBeenCalledWith(true);
		// cam2 should be unmuted
		const decoder2 = pfl.getDecoder('cam2');
		expect(decoder2!.setMuted).toHaveBeenCalledWith(false);
	});

	it('should return levels for a source', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		const levels = pfl.getSourceLevels('cam1');
		expect(levels).toEqual({ peakL: 0, peakR: 0, rmsL: 0, rmsR: 0 });
	});

	it('should return zero levels for unknown source', () => {
		const pfl = createPFLManager();
		const levels = pfl.getSourceLevels('unknown');
		expect(levels).toEqual({ peakL: 0, peakR: 0, rmsL: 0, rmsR: 0 });
	});

	it('should enable metering on all added sources', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		const decoder = pfl.getDecoder('cam1');
		expect(decoder!.enableMetering).toHaveBeenCalled();
	});

	it('should feed audio frames to decoder', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		const data = new Uint8Array([1, 2, 3]);
		pfl.feedAudioFrame('cam1', data, 1000);
		const decoder = pfl.getDecoder('cam1');
		expect(decoder!.decode).toHaveBeenCalledWith(data, 1000, false);
	});

	it('should remove source and clean up decoder', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		const decoder = pfl.getDecoder('cam1');
		pfl.removeSource('cam1');
		expect(decoder!.reset).toHaveBeenCalled();
		expect(pfl.getDecoder('cam1')).toBeNull();
	});

	it('should clear active source when removed source was PFLd', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		pfl.enablePFL('cam1');
		pfl.removeSource('cam1');
		expect(pfl.activeSource).toBeNull();
	});

	it('should clean up on destroy', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		pfl.enablePFL('cam1');
		pfl.destroy();
		expect(pfl.activeSource).toBeNull();
	});

	it('should return playback PTS for source', async () => {
		const pfl = createPFLManager();
		await pfl.addSource('cam1');
		const pts = pfl.getPlaybackPTS('cam1');
		expect(pts).toBe(-1); // mock returns -1
	});

	it('should return -1 PTS for unknown source', () => {
		const pfl = createPFLManager();
		const pts = pfl.getPlaybackPTS('unknown');
		expect(pts).toBe(-1);
	});
});
