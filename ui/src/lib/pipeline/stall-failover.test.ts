import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { MediaPipeline } from '$lib/transport/media-pipeline';

// Two real media pipelines, Engine A's and the PipelineManager's Engine B,
// with the decoders, renderers and transports they drive mocked out.
vi.mock('$lib/prism/video-decoder', () => ({
	PrismVideoDecoder: class {
		preload = vi.fn(); configure = vi.fn(); decode = vi.fn(); reset = vi.fn();
		flush = vi.fn(); pause = vi.fn(); resume = vi.fn();
		getDiagnostics = vi.fn().mockResolvedValue({});
	},
}));
vi.mock('$lib/prism/video-render-buffer', () => ({
	VideoRenderBuffer: class { addFrame = vi.fn(); clear = vi.fn(); takeNewestFrame = vi.fn().mockReturnValue({ frame: null }); },
}));
vi.mock('$lib/prism/audio-decoder', () => ({ PrismAudioDecoder: class {} }));
vi.mock('$lib/prism/renderer', () => ({
	PrismRenderer: class { start = vi.fn(); destroy = vi.fn(); resetSync = vi.fn(); getDiagnostics = vi.fn(); },
}));
vi.mock('$lib/prism/moq-transport', () => ({
	MoQTransport: class { connect = vi.fn().mockResolvedValue(undefined); close = vi.fn(); getDiagnostics = vi.fn(); getTransport = vi.fn(); },
}));

const created: MediaPipeline[] = [];
vi.mock('$lib/transport/media-pipeline', async (importOriginal) => {
	const original = await importOriginal<typeof import('$lib/transport/media-pipeline')>();
	return {
		...original,
		createMediaPipeline: vi.fn((config?: Parameters<typeof original.createMediaPipeline>[0]) => {
			const p = original.createMediaPipeline(config);
			created.push(p);
			return p;
		}),
	};
});

import { createMediaPipeline, STALL_THRESHOLD_MS } from '$lib/transport/media-pipeline';
import { clearAllStalls } from '$lib/transport/source-errors.svelte';
import { PipelineManager } from './manager';

describe('stall failover across two pipelines', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		created.length = 0;
		clearAllStalls();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	// The page refuses a stall failover while Engine B's program feed does
	// not deliver either. Engine A reports a stall on its transition into
	// one, and that refusal is not retried on its own: the stall flag is
	// kept per source key for both pipelines, so Engine B's frames clear
	// it and Engine A reports again, which fails over once B delivers.
	it('fails over on a stall refused while Engine B was silent once Engine B delivers', () => {
		let refused = 0;
		let failovers = 0;
		const frame = new Uint8Array([1]);
		let manager: PipelineManager;
		const pipelineA = createMediaPipeline({
			onSourceStalled: (key) => {
				// The page's stall handler on Engine A.
				if (key !== 'program-preview') return;
				if (manager.refusesStallFailover()) refused++;
				else failovers++;
			},
		});
		manager = new PipelineManager(pipelineA, () => []);
		manager.createEngineBPipeline('https://engine-b.example.com');
		manager.connectSourceB('program-preview');
		const pipelineB = created[1];

		pipelineA.addSource('program-preview');
		pipelineA.feedVideoFrame('program-preview', frame, true, 0, null);
		pipelineA.startStallDetection();

		// Engine A stalls; Engine B has delivered nothing.
		vi.advanceTimersByTime(2 * STALL_THRESHOLD_MS);
		expect(refused).toBe(1);
		expect(failovers).toBe(0);

		// Engine B delivers while Engine A stays stalled.
		for (let t = 0; t < 2 * STALL_THRESHOLD_MS; t += 40) {
			pipelineB.feedVideoFrame('program-preview', frame, t === 0, t, null);
			vi.advanceTimersByTime(40);
		}

		expect(failovers).toBeGreaterThanOrEqual(1);
		expect(refused).toBe(1);
		manager.destroy();
	});
});
