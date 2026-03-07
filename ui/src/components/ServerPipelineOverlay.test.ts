import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import ServerPipelineOverlay from './ServerPipelineOverlay.svelte';

describe('ServerPipelineOverlay', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it('renders nothing by default (hidden)', () => {
		const { container } = render(ServerPipelineOverlay);
		const overlay = container.querySelector('.server-pipeline-overlay');
		expect(overlay).toBeFalsy();
	});

	it('shows overlay after Shift+P keydown', async () => {
		const mockSnapshot = {
			switcher: {
				video_pipeline: {
					output_fps: 30,
					frames_processed: 100,
					frames_broadcast: 99,
					frames_dropped: 1,
					queue_len: 2,
					decode_last_ms: 5.2,
					decode_max_ms: 12.1,
					key_last_ms: 0,
					key_max_ms: 0,
					composite_last_ms: 0,
					composite_max_ms: 0,
					encode_last_ms: 8.3,
					encode_max_ms: 20.5,
					last_proc_time_ms: 14.1,
					max_proc_time_ms: 32.0,
					max_broadcast_gap_ms: 35.2,
					route_to_engine: 50,
					route_to_pipeline: 200,
					route_filtered: 10,
				},
			},
			mixer: {
				mode: 'passthrough',
				frames_mixed: 0,
				frames_passthrough: 500,
				max_inter_frame_gap_ms: 34.0,
			},
		};

		vi.spyOn(globalThis, 'fetch').mockResolvedValue({
			ok: true,
			json: () => Promise.resolve(mockSnapshot),
		} as Response);

		const { container } = render(ServerPipelineOverlay);

		// Simulate Shift+P
		document.dispatchEvent(new KeyboardEvent('keydown', {
			code: 'KeyP',
			key: 'P',
			shiftKey: true,
		}));

		// Wait for fetch + render
		await vi.advanceTimersByTimeAsync(100);

		const overlay = container.querySelector('.server-pipeline-overlay');
		expect(overlay).toBeTruthy();
		expect(overlay?.textContent).toContain('SERVER PIPELINE');
		expect(overlay?.textContent).toContain('30'); // output FPS
		expect(overlay?.textContent).toContain('5.2ms'); // decode last
	});

	it('hides overlay on second Shift+P', async () => {
		vi.spyOn(globalThis, 'fetch').mockResolvedValue({
			ok: true,
			json: () => Promise.resolve({ switcher: { video_pipeline: {} } }),
		} as Response);

		const { container } = render(ServerPipelineOverlay);

		// Toggle on
		document.dispatchEvent(new KeyboardEvent('keydown', {
			code: 'KeyP', key: 'P', shiftKey: true,
		}));
		await vi.advanceTimersByTimeAsync(100);
		expect(container.querySelector('.server-pipeline-overlay')).toBeTruthy();

		// Toggle off
		document.dispatchEvent(new KeyboardEvent('keydown', {
			code: 'KeyP', key: 'P', shiftKey: true,
		}));
		await vi.advanceTimersByTimeAsync(100);
		expect(container.querySelector('.server-pipeline-overlay')).toBeFalsy();
	});

	it('shows transition engine section when present in snapshot', async () => {
		const mockSnapshot = {
			switcher: {
				video_pipeline: { output_fps: 30 },
				transition_engine: {
					ingest_last_ms: 15.2,
					ingest_max_ms: 28.1,
					decode_last_ms: 8.0,
					decode_max_ms: 15.3,
					blend_last_ms: 2.1,
					blend_max_ms: 3.5,
					frames_ingested: 60,
					frames_blended: 30,
				},
			},
		};

		vi.spyOn(globalThis, 'fetch').mockResolvedValue({
			ok: true,
			json: () => Promise.resolve(mockSnapshot),
		} as Response);

		const { container } = render(ServerPipelineOverlay);

		document.dispatchEvent(new KeyboardEvent('keydown', {
			code: 'KeyP', key: 'P', shiftKey: true,
		}));
		await vi.advanceTimersByTimeAsync(100);

		const overlay = container.querySelector('.server-pipeline-overlay');
		expect(overlay?.textContent).toContain('TRANSITION ENGINE');
		expect(overlay?.textContent).toContain('15.2ms'); // ingest last
		expect(overlay?.textContent).toContain('60'); // frames ingested
	});
});
