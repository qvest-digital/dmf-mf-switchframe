import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import StatsPanel from './StatsPanel.svelte';

// Standard mock snapshot with pipeline data
function mockSnapshot(overrides: Record<string, any> = {}) {
	return {
		uptime_ms: 60000,
		switcher: {
			program_source: 'cam1',
			preview_source: 'cam2',
			state: 'idle',
			pipeline: {
				epoch: 3,
				run_count: 100,
				last_run_ns: 5_000_000,
				max_run_ns: 12_000_000,
				total_latency_us: 200,
				lip_sync_hint_us: 3000,
				total_nodes: 6,
				active_nodes: [
					{ name: 'upstream-key', last_ns: 500_000, max_ns: 1_000_000, latency_us: 50 },
					{ name: 'h264-encode', last_ns: 4_000_000, max_ns: 10_000_000, latency_us: 150 },
				],
			},
			video_pipeline: {
				output_fps: 30,
				frames_processed: 100,
				frames_broadcast: 98,
				frames_dropped: 0,
				encode_nil: 0,
				queue_len: 2,
				last_proc_time_ms: 5.0,
				max_proc_time_ms: 12.0,
				max_broadcast_gap_ms: 35.5,
				route_to_engine: 10,
				route_to_pipeline: 90,
				route_filtered: 500,
				trans_seam_last_ms: 0.5,
				trans_seam_max_ms: 2.1,
				trans_seam_count: 2,
			},
			frame_budget_ms: 33.3,
			frame_pool: { hits: 990, misses: 10, capacity: 32, buf_size: 3110400 },
			source_decoders: { active_count: 4, estimated_yuv_mb: 12 },
			sources: {
				cam1: { video_frames_in: 1800, audio_frames_in: 3600, health_status: 'healthy', last_frame_ago_ms: 5, raw_pipeline: true },
				cam2: { video_frames_in: 1800, audio_frames_in: 3600, health_status: 'healthy', last_frame_ago_ms: 8, raw_pipeline: true },
			},
			cuts_total: 5,
			transitions_started: 3,
			transitions_completed: 3,
			deadline_violations: 0,
			frame_rate_converter: { quality: 'mcfi' },
			program_relay_viewers: [
				{ id: 'moq-1', video_sent: 100, video_dropped: 0, audio_sent: 200, audio_dropped: 0, bytes_sent: 1048576 },
			],
			...overrides.switcher,
		},
		mixer: {
			mode: 'passthrough',
			program_peak_dbfs: [-20.0, -22.3],
			channels_active: 1,
			channels_muted: 0,
			frames_mixed: 0,
			frames_passthrough: 500,
			frames_output_total: 500,
			crossfade_count: 1,
			crossfade_timeouts: 0,
			decode_errors: 0,
			encode_errors: 0,
			deadline_flushes: 0,
			max_inter_frame_gap_ms: 34.0,
			mode_transitions: 4,
			trans_crossfade_count: 2,
			...overrides.mixer,
		},
		output: {
			recording: { active: false },
			srt: { active: false },
			viewer: null,
			...overrides.output,
		},
		replay: {
			state: 'idle',
			buffers: {
				cam1: { frameCount: 1440, gopCount: 65, durationSecs: 60.0, bytesUsed: 13_900_000 },
				cam2: { frameCount: 1420, gopCount: 64, durationSecs: 59.0, bytesUsed: 12_500_000 },
			},
			...overrides.replay,
		},
		...overrides,
	};
}

function mockFetch(data: Record<string, any>) {
	return vi.spyOn(globalThis, 'fetch').mockResolvedValue({
		ok: true,
		json: () => Promise.resolve(data),
	} as Response);
}

describe('StatsPanel', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it('renders hidden by default (off-screen transform)', () => {
		const { container } = render(StatsPanel, {
			props: { visible: false, onclose: vi.fn() },
		});
		const panel = container.querySelector('.stats-panel');
		expect(panel).toBeTruthy();
		expect(panel?.classList.contains('visible')).toBe(false);
	});

	it('slides in when visible is true and fetches snapshot', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.classList.contains('visible')).toBe(true);
		expect(panel?.textContent).toContain('PIPELINE MONITOR');
		expect(panel?.textContent).toContain('epoch 3');
		expect(panel?.textContent).toContain('30 fps');
	});

	it('does not poll when hidden', async () => {
		const fetchSpy = mockFetch({});

		render(StatsPanel, {
			props: { visible: false, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(5000);
		expect(fetchSpy).not.toHaveBeenCalled();
	});

	it('shows transition engine section when present', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				transition_engine: {
					ingest_last_ms: 15.2,
					ingest_max_ms: 28.1,
					blend_last_ms: 2.1,
					blend_max_ms: 3.5,
					frames_ingested: 60,
					frames_blended: 30,
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('TRANSITION ENGINE');
		expect(panel?.textContent).toContain('15.2ms');
		expect(panel?.textContent).toContain('60');
	});

	it('calls onclose when close button clicked', async () => {
		mockFetch(mockSnapshot());

		const onclose = vi.fn();
		const { container } = render(StatsPanel, {
			props: { visible: true, onclose },
		});

		await vi.advanceTimersByTimeAsync(100);

		const closeBtn = container.querySelector('.close-btn') as HTMLButtonElement;
		closeBtn?.click();
		expect(onclose).toHaveBeenCalled();
	});

	it('close button has aria-label', () => {
		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});
		const closeBtn = container.querySelector('.close-btn');
		expect(closeBtn?.getAttribute('aria-label')).toBe('Close stats panel');
	});

	it('has role=complementary and aria-label on panel', () => {
		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});
		const panel = container.querySelector('.stats-panel');
		expect(panel?.getAttribute('role')).toBe('complementary');
		expect(panel?.getAttribute('aria-label')).toBe('Pipeline stats');
	});

	it('handles fetch failure gracefully', async () => {
		vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('network error'));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		// Should show loading state, not crash
		expect(container.querySelector('.loading')).toBeTruthy();
	});

	it('handles non-ok response gracefully', async () => {
		vi.spyOn(globalThis, 'fetch').mockResolvedValue({
			ok: false,
			status: 500,
		} as Response);

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		// Should show loading state, not crash
		expect(container.querySelector('.loading')).toBeTruthy();
	});

	it('shows alarm state when node is critical', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				pipeline: {
					epoch: 1,
					run_count: 10,
					last_run_ns: 30_000_000,
					max_run_ns: 35_000_000,
					total_latency_us: 500,
					lip_sync_hint_us: 0,
					active_nodes: [
						{ name: 'h264-encode', last_ns: 30_000_000, max_ns: 35_000_000, latency_us: 500 },
					],
				},
				frame_budget_ms: 33.3,
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const header = container.querySelector('.stats-header');
		expect(header?.classList.contains('alarm')).toBe(true);
	});

	it('shows audio mixer section with peaks and crossfades', async () => {
		mockFetch(mockSnapshot({
			mixer: {
				mode: 'mixing',
				program_peak_dbfs: [-20.0, -22.3],
				channels_active: 2,
				channels_muted: 1,
				frames_mixed: 1000,
				frames_passthrough: 0,
				max_inter_frame_gap_ms: 45.0,
				crossfade_count: 3,
				trans_crossfade_count: 5,
				mode_transitions: 8,
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('AUDIO MIXER');
		expect(panel?.textContent).toContain('mixing');
		expect(panel?.textContent).toContain('1,000 mixed');
		expect(panel?.textContent).toContain('-20.0');
		expect(panel?.textContent).toContain('-22.3');
		expect(panel?.textContent).toContain('3 cut');
		expect(panel?.textContent).toContain('5 trans');
		expect(panel?.textContent).toContain('8 transitions');
	});

	it('shows system stats with frame pool hit rate and memory', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('99.0% hit');
		expect(panel?.textContent).toContain('32 cap');
		expect(panel?.textContent).toContain('~132MB'); // 95 pool + 25 replay + 12 dec
	});

	it('shows lip-sync gauge with correct value', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				pipeline: {
					epoch: 1,
					run_count: 10,
					last_run_ns: 5_000_000,
					max_run_ns: 10_000_000,
					total_latency_us: 200,
					lip_sync_hint_us: 12000, // 12ms
					active_nodes: [],
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('LIP SYNC');
		expect(panel?.textContent).toContain('12.0ms');
	});

	it('sparkline SVGs have aria-hidden', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const svgs = container.querySelectorAll('.sparkline-svg');
		for (const svg of svgs) {
			expect(svg.getAttribute('aria-hidden')).toBe('true');
		}
	});

	it('polls at 2s interval when visible', async () => {
		const fetchSpy = mockFetch(mockSnapshot());

		render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		// Initial poll
		await vi.advanceTimersByTimeAsync(100);
		const initialCalls = fetchSpy.mock.calls.length;
		expect(initialCalls).toBeGreaterThan(0);

		// Wait for two more poll intervals
		await vi.advanceTimersByTimeAsync(4100);
		expect(fetchSpy.mock.calls.length).toBeGreaterThan(initialCalls);
	});

	it('shows update dot when data is received', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const dot = container.querySelector('.update-dot');
		expect(dot).toBeTruthy();
	});

	it('shows source health grid', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('SOURCES');
		expect(panel?.textContent).toContain('2 sources');
		expect(panel?.textContent).toContain('cam1');
		expect(panel?.textContent).toContain('cam2');
		// Source FPS: 1800 frames / 60s = 30fps
		expect(panel?.textContent).toContain('30fps');
	});

	it('shows output section', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('OUTPUT');
		expect(panel?.textContent).toContain('inactive');
		expect(panel?.textContent).toContain('1 connected');
		expect(panel?.textContent).toContain('0 drops');
	});

	it('shows replay buffer section', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('REPLAY');
		expect(panel?.textContent).toContain('1m0s');
		expect(panel?.textContent).toContain('1,440f');
		expect(panel?.textContent).toContain('65 GOPs');
	});

	it('shows max broadcast gap chip', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('35.5ms max gap');
	});

	it('shows transition seam chip', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('2.1ms seam');
	});

	it('shows over-budget indicator when pipeline exceeds budget', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				pipeline: {
					epoch: 1,
					run_count: 10,
					last_run_ns: 40_000_000, // 40ms > 33.3ms budget
					max_run_ns: 45_000_000,
					total_latency_us: 200,
					lip_sync_hint_us: 0,
					active_nodes: [
						{ name: 'h264-encode', last_ns: 40_000_000, max_ns: 45_000_000, latency_us: 500 },
					],
				},
				frame_budget_ms: 33.3,
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const summary = container.querySelector('.budget-summary');
		expect(summary?.classList.contains('over-budget')).toBe(true);
		expect(summary?.textContent).toContain('OVER');
	});

	it('shows dynamic node graph with layout-compositor', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				pipeline: {
					epoch: 5,
					run_count: 100,
					last_run_ns: 5_000_000,
					max_run_ns: 10_000_000,
					total_latency_us: 200,
					lip_sync_hint_us: 0,
					total_nodes: 6,
					active_nodes: [
						{ name: 'layout-compositor', last_ns: 1_000_000, max_ns: 8_000_000, latency_us: 1000 },
						{ name: 'raw-sink-mxl', last_ns: 47_000, max_ns: 4_000_000, latency_us: 50 },
						{ name: 'raw-sink-monitor', last_ns: 338_000, max_ns: 73_000_000, latency_us: 50 },
						{ name: 'h264-encode', last_ns: 146_000, max_ns: 7_000_000, latency_us: 10000 },
					],
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const nodeNames = container.querySelectorAll('.node-name');
		const names = Array.from(nodeNames).map(el => el.textContent);
		// Should include LAY (layout-compositor), MXL, MON (branch), ENC
		expect(names).toContain('LAY');
		expect(names).toContain('MXL');
		expect(names).toContain('MON');
		expect(names).toContain('ENC');
		// Should NOT include DSK (compositor) since layout-compositor is active
		expect(names).not.toContain('DSK');
	});

	it('shows audio errors when present', async () => {
		mockFetch(mockSnapshot({
			mixer: {
				mode: 'mixing',
				frames_mixed: 100,
				frames_passthrough: 0,
				decode_errors: 3,
				encode_errors: 1,
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('3 dec');
		expect(panel?.textContent).toContain('1 enc');
	});

	it('shows DEC badge for sources with active decoder', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const badges = container.querySelectorAll('.source-badge');
		expect(badges.length).toBe(2); // cam1 and cam2 both have raw_pipeline: true
	});

	it('shows viewer count and bytes sent', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('1 connected');
		expect(panel?.textContent).toContain('1.0MB');
	});
});
