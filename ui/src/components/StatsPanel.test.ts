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

	it('shows codec info badges with HW/SW indicator', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				...mockSnapshot().switcher,
				codec: { encoder: 'libx264', decoder: 'h264', hw_accel: false },
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('libx264');
		expect(panel?.textContent).toContain('h264');
		// Should show SW badge (not HW)
		const swBadge = container.querySelector('.hw-badge.sw');
		expect(swBadge).toBeTruthy();
		expect(swBadge?.textContent).toBe('SW');
	});

	it('shows HW badge when hardware acceleration is active', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				...mockSnapshot().switcher,
				codec: { encoder: 'h264_nvenc', decoder: 'h264', hw_accel: true },
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('h264_nvenc');
		const hwBadge = container.querySelector('.hw-badge.hw');
		expect(hwBadge).toBeTruthy();
		expect(hwBadge?.textContent).toBe('HW');
	});

	it('shows per-source decoder stats when available', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				...mockSnapshot().switcher,
				sources: {
					cam1: { video_frames_in: 1800, audio_frames_in: 3600, health_status: 'healthy', last_frame_ago_ms: 5, raw_pipeline: true, decoder_active: true, decoder_avg_fps: 29.97, decoder_avg_frame_bytes: 45000 },
					cam2: { video_frames_in: 1800, audio_frames_in: 3600, health_status: 'healthy', last_frame_ago_ms: 8, raw_pipeline: true },
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		// cam1 should show decoded FPS (30) instead of computed FPS
		expect(panel?.textContent).toContain('30fps');
		// cam1 should show frame size
		expect(panel?.textContent).toContain('43.9KB');
	});

	it('shows SRT detail when active', async () => {
		mockFetch(mockSnapshot({
			output: {
				recording: { active: false },
				srt: { active: true, mode: 'caller', address: '192.168.1.100', port: 9000, bytesWritten: 10485760, droppedPackets: 2, overflowCount: 0 },
				viewer: null,
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('LIVE');
		expect(panel?.textContent).toContain('caller');
		expect(panel?.textContent).toContain('192.168.1.100:9000');
		expect(panel?.textContent).toContain('10.0MB');
		expect(panel?.textContent).toContain('2 drops');
	});

	it('shows muxer PTS as timecode', async () => {
		mockFetch(mockSnapshot({
			output: {
				recording: { active: true },
				srt: { active: false },
				viewer: null,
				muxer_pts: 5400000, // 60s at 90kHz
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('Muxer PTS');
		expect(panel?.textContent).toContain('00:01:00:00');
	});

	it('shows CBR pacer stats', async () => {
		mockFetch(mockSnapshot({
			output: {
				recording: { active: true },
				srt: { active: false },
				viewer: null,
				cbr_pacer: { enabled: true, muxrateBps: 10000000, nullPacketsTotal: 5000, realBytesTotal: 1000000, padBytesTotal: 200000, burstTicksTotal: 10 },
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('CBR');
		expect(panel?.textContent).toContain('10.0 Mbps');
		expect(panel?.textContent).toContain('5,000 null');
	});

	it('shows LUFS metering with EBU R128 color coding', async () => {
		mockFetch(mockSnapshot({
			mixer: {
				mode: 'mixing',
				frames_mixed: 100,
				frames_passthrough: 0,
				loudness: { momentary_lufs: -23.5, short_term_lufs: -24.0, integrated_lufs: -14.5 },
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('-23.5');
		expect(panel?.textContent).toContain('-24.0');
		expect(panel?.textContent).toContain('-14.5');
	});

	it('shows per-channel audio details', async () => {
		mockFetch(mockSnapshot({
			mixer: {
				mode: 'mixing',
				frames_mixed: 100,
				frames_passthrough: 0,
				channels: {
					cam1: { active: true, muted: false, afv: true, level: 0, trim: 0, eq_bypassed: false, compressor_bypassed: true, delay_ms: 50, peak_l_dbfs: -18.5, peak_r_dbfs: -20.1 },
					cam2: { active: true, muted: true, afv: false, level: -6, trim: 3, eq_bypassed: true, compressor_bypassed: true, delay_ms: 0 },
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		// cam1: has EQ active (not bypassed), AFV, 50ms delay
		expect(panel?.textContent).toContain('AFV');
		expect(panel?.textContent).toContain('EQ');
		expect(panel?.textContent).toContain('50ms');
		expect(panel?.textContent).toContain('-18.5dB');
		// cam2: muted
		expect(container.querySelector('.flag-mute')).toBeTruthy();
	});

	it('shows frame sync section with FRC state', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				...mockSnapshot().switcher,
				frame_sync: {
					frc_quality: 'mcfi',
					sources: {
						cam1: { audio_miss_count: 0, video_count: 1, audio_count: 1, raw_video_count: 1, frc: { requested_quality: 'mcfi', effective_quality: 'mcfi', scene_change: false, me_last_ns: 2_000_000, has_two_frames: true, degraded: false } },
						cam2: { audio_miss_count: 3, video_count: 0, audio_count: 0, raw_video_count: 0 },
					},
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('FRAME SYNC');
		expect(panel?.textContent).toContain('FRC: mcfi');
		// cam1 FRC details
		expect(panel?.textContent).toContain('2.00ms ME');
		// cam2 audio miss
		expect(panel?.textContent).toContain('miss:3');
	});

	it('shows FRC degradation warning', async () => {
		mockFetch(mockSnapshot({
			switcher: {
				...mockSnapshot().switcher,
				frame_sync: {
					frc_quality: 'mcfi',
					sources: {
						cam1: { audio_miss_count: 0, video_count: 1, audio_count: 1, raw_video_count: 1, frc: { requested_quality: 'mcfi', effective_quality: 'blend', scene_change: false, me_last_ns: 20_000_000, has_two_frames: true, degraded: true } },
					},
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('DEGRADED');
		expect(panel?.textContent).toContain('req: mcfi');
	});

	it('shows L/R peak split when channels diverge by more than 3dB', async () => {
		mockFetch(mockSnapshot({
			mixer: {
				mode: 'mixing',
				frames_mixed: 100,
				frames_passthrough: 0,
				channels: {
					cam1: { active: true, muted: false, afv: false, level: 0, trim: 0, eq_bypassed: true, compressor_bypassed: true, delay_ms: 0, peak_l_dbfs: -12.0, peak_r_dbfs: -20.0 },
					cam2: { active: true, muted: false, afv: false, level: 0, trim: 0, eq_bypassed: true, compressor_bypassed: true, delay_ms: 0, peak_l_dbfs: -18.0, peak_r_dbfs: -19.5 },
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		// cam1: 8dB difference → should show L/R split
		expect(container.querySelector('.lr-split')).toBeTruthy();
		expect(panel?.textContent).toContain('L-12.0');
		expect(panel?.textContent).toContain('R-20.0');
		// cam1 combined value: max(-12, -20) = -12.0dB
		expect(panel?.textContent).toContain('-12.0dB');
		// cam2: 1.5dB difference → no split, just combined max(-18, -19.5) = -18.0dB
		const splits = container.querySelectorAll('.lr-split');
		expect(splits.length).toBe(1); // only cam1
	});

	it('uses relative FPS thresholds based on pipeline frame budget', async () => {
		// 60fps pipeline: frame_budget_ms = 16.67
		// Expected FPS = 60, warn at <51 (85%), crit at <30 (50%)
		mockFetch(mockSnapshot({
			switcher: {
				...mockSnapshot().switcher,
				frame_budget_ms: 16.67,
				sources: {
					cam1: { video_frames_in: 0, audio_frames_in: 0, health_status: 'healthy', last_frame_ago_ms: 5, raw_pipeline: true, decoder_active: true, decoder_avg_fps: 55, decoder_avg_frame_bytes: 40000 },
					cam2: { video_frames_in: 0, audio_frames_in: 0, health_status: 'healthy', last_frame_ago_ms: 5, raw_pipeline: true, decoder_active: true, decoder_avg_fps: 25, decoder_avg_frame_bytes: 40000 },
				},
			},
		}));

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const fpsSpans = container.querySelectorAll('.source-fps');
		// cam1 at 55fps (>51 threshold) → no warning classes
		const cam1Fps = Array.from(fpsSpans).find(el => el.textContent?.includes('55'));
		expect(cam1Fps).toBeTruthy();
		expect(cam1Fps?.classList.contains('warn-text')).toBe(false);
		expect(cam1Fps?.classList.contains('crit-text')).toBe(false);
		// cam2 at 25fps (<30 threshold) → crit
		const cam2Fps = Array.from(fpsSpans).find(el => el.textContent?.includes('25'));
		expect(cam2Fps).toBeTruthy();
		expect(cam2Fps?.classList.contains('crit-text')).toBe(true);
	});

	it('shows real async encode timing instead of near-zero Process() timing', async () => {
		// Simulate what the server sends: near-zero last_ns (Process() enqueue time)
		// but real encode timing in encode_last_ns/encode_max_ns.
		const snap = mockSnapshot({
			switcher: {
				...mockSnapshot().switcher,
				pipeline: {
					epoch: 3,
					run_count: 100,
					last_run_ns: 1_500_000, // 1.5ms pipeline loop (without real encode)
					max_run_ns: 3_000_000,
					total_latency_us: 200,
					lip_sync_hint_us: 3000,
					total_nodes: 2,
					active_nodes: [
						{ name: 'upstream-key', last_ns: 500_000, max_ns: 1_000_000, latency_us: 50 },
						{
							name: 'h264-encode',
							last_ns: 500, // near-zero: Process() just enqueues
							max_ns: 800,
							latency_us: 10000,
							encode_last_ns: 12_000_000, // 12ms real encode
							encode_max_ns: 18_000_000, // 18ms peak
							encode_total: 5000,
							encode_queue_len: 1,
						},
					],
				},
			},
		});
		mockFetch(snap);

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		const text = panel?.textContent ?? '';

		// Should show the real 12ms encode time in the node diagram, not 0.00ms
		expect(text).toContain('12.00ms');
		// Should show encode total count
		expect(text).toContain('5,000 enc');
		// Should show queue depth
		expect(text).toContain('q1/2');

		// Budget bars: sync pipeline bar shows 1.5ms, encode bar shows 12ms separately
		const summaries = panel?.querySelectorAll('.budget-summary') ?? [];
		expect(summaries.length).toBeGreaterThanOrEqual(2);
		// First summary: sync pipeline (1.5ms)
		expect(summaries[0]?.textContent).toContain('1.5ms');
		// Second summary: async encode (12.0ms)
		expect(summaries[1]?.textContent).toContain('12.0ms');
		expect(summaries[1]?.textContent).toContain('async');
	});

	// --- Perf view tests ---

	function mockPerfSnapshot(overrides: Record<string, any> = {}) {
		return {
			timestamp: '2026-01-01T00:00:00Z',
			uptime_ms: 60000,
			frame_budget_ns: 33333333,
			sources: {
				cam1: {
					health: 'healthy',
					decode: {
						current: { last_ns: 2500000, drops: 0, avg_fps: 29.97, avg_frame_bytes: 45000 },
						windows: {
							'1s': { min_ns: 2100000, max_ns: 3400000, mean_ns: 2600000, p95_ns: 3100000 },
							'10s': { min_ns: 1900000, max_ns: 4200000, mean_ns: 2550000, p95_ns: 3500000 },
							'60s': { min_ns: 1800000, max_ns: 8200000, mean_ns: 2500000, p95_ns: 3800000 },
						},
					},
				},
			},
			pipeline: {
				current: { last_ns: 1500000, queue_len: 0 },
				windows: {
					'1s': { min_ns: 1000000, max_ns: 2000000, mean_ns: 1500000, p95_ns: 1800000 },
					'10s': { min_ns: 900000, max_ns: 3000000, mean_ns: 1400000, p95_ns: 2500000 },
					'60s': { min_ns: 800000, max_ns: 5000000, mean_ns: 1300000, p95_ns: 3000000 },
				},
				nodes: {
					'h264-encode': {
						current: { last_ns: 9500000 },
						windows: {
							'1s': { min_ns: 8000000, max_ns: 11000000, mean_ns: 9500000, p95_ns: 10500000 },
							'10s': { min_ns: 7000000, max_ns: 12000000, mean_ns: 9000000, p95_ns: 11000000 },
							'60s': { min_ns: 6000000, max_ns: 15000000, mean_ns: 8500000, p95_ns: 13000000 },
						},
					},
				},
				deadline_violations: 3,
				budget_pct: 4.5,
			},
			e2e: {
				current: { last_ns: 15200000 },
				windows: {
					'1s': { min_ns: 12000000, max_ns: 18000000, mean_ns: 15000000, p95_ns: 17000000 },
					'10s': { min_ns: 10000000, max_ns: 20000000, mean_ns: 14000000, p95_ns: 19000000 },
					'60s': { min_ns: 8000000, max_ns: 25000000, mean_ns: 13000000, p95_ns: 22000000 },
				},
			},
			audio: {
				mode: 'passthrough',
				mix_cycle: {
					current: { last_ns: 0 },
					windows: {
						'1s': { min_ns: 0, max_ns: 0, mean_ns: 0, p95_ns: 0 },
						'10s': { min_ns: 0, max_ns: 0, mean_ns: 0, p95_ns: 0 },
						'60s': { min_ns: 0, max_ns: 0, mean_ns: 0, p95_ns: 0 },
					},
				},
				counters: { output: 169200, passthrough: 169200, mixed: 0, decode_errors: 0, encode_errors: 0 },
				loudness: { momentary_lufs: -23.4, short_term_lufs: -22.8, integrated_lufs: -23.1 },
			},
			broadcast: {
				frames: 108000,
				output_fps: 30,
				gap: {
					current: { max_ns: 35000000 },
					windows: {
						'1s': { min_ns: 30000000, max_ns: 36000000, mean_ns: 33000000, p95_ns: 35000000 },
						'10s': { min_ns: 28000000, max_ns: 40000000, mean_ns: 33000000, p95_ns: 38000000 },
						'60s': { min_ns: 25000000, max_ns: 45000000, mean_ns: 33000000, p95_ns: 40000000 },
					},
				},
			},
			output: {
				viewer: { video_sent: 108000, video_dropped: 0, audio_dropped: 0 },
				muxer_pts: 9720000000,
				srt: { bytes_written: 540000000, overflow_count: 0 },
				recording: { active: false },
			},
			baseline: null,
			...overrides,
		};
	}

	it('shows Perf/Debug view toggle when visible', async () => {
		mockFetch(mockSnapshot());
		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});
		await vi.advanceTimersByTimeAsync(100);
		const toggleBtns = container.querySelectorAll('.toggle-btn');
		expect(toggleBtns.length).toBe(3);
		expect(toggleBtns[0]?.textContent).toBe('Debug');
		expect(toggleBtns[1]?.textContent).toBe('Perf');
		expect(toggleBtns[2]?.textContent).toBe('Browser');
	});

	it('switches to perf view and fetches /api/perf', async () => {
		const perfResponse = mockPerfSnapshot();

		// First fetch: debug snapshot
		const fetchSpy = vi.spyOn(globalThis, 'fetch')
			.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(mockSnapshot()) } as Response)
			.mockResolvedValue({ ok: true, json: () => Promise.resolve(perfResponse) } as Response);

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		// Click Perf toggle
		const perfBtn = container.querySelectorAll('.toggle-btn')[1] as HTMLButtonElement;
		perfBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		// Verify it fetched /api/perf
		const perfCalls = fetchSpy.mock.calls.filter(c => String(c[0]).includes('/api/perf'));
		expect(perfCalls.length).toBeGreaterThan(0);

		// Verify waterfall is rendered
		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('PIPELINE WATERFALL');
		expect(panel?.textContent).toContain('H.264 Encode');
		expect(panel?.textContent).toContain('E2E LATENCY');
		expect(panel?.textContent).toContain('AUDIO');
		expect(panel?.textContent).toContain('passthrough');
	});

	it('shows window selector buttons in perf view', async () => {
		const perfResponse = mockPerfSnapshot();
		vi.spyOn(globalThis, 'fetch')
			.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(mockSnapshot()) } as Response)
			.mockResolvedValue({ ok: true, json: () => Promise.resolve(perfResponse) } as Response);

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});
		await vi.advanceTimersByTimeAsync(100);

		// Switch to perf
		const perfBtn = container.querySelectorAll('.toggle-btn')[1] as HTMLButtonElement;
		perfBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const windowBtns = container.querySelectorAll('.window-btn');
		expect(windowBtns.length).toBe(3);
		expect(windowBtns[0]?.textContent).toBe('1s');
		expect(windowBtns[1]?.textContent).toBe('10s');
		expect(windowBtns[2]?.textContent).toBe('60s');
		// 10s should be active by default
		expect(windowBtns[1]?.classList.contains('active')).toBe(true);
	});

	it('shows source decode info in perf view', async () => {
		const perfResponse = mockPerfSnapshot();
		vi.spyOn(globalThis, 'fetch')
			.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(mockSnapshot()) } as Response)
			.mockResolvedValue({ ok: true, json: () => Promise.resolve(perfResponse) } as Response);

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});
		await vi.advanceTimersByTimeAsync(100);

		// Switch to perf
		const perfBtn = container.querySelectorAll('.toggle-btn')[1] as HTMLButtonElement;
		perfBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('SOURCE DECODE');
		expect(panel?.textContent).toContain('cam1');
		expect(panel?.textContent).toContain('30.0fps');
	});

	it('shows broadcast and output sections in perf view', async () => {
		const perfResponse = mockPerfSnapshot();
		vi.spyOn(globalThis, 'fetch')
			.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(mockSnapshot()) } as Response)
			.mockResolvedValue({ ok: true, json: () => Promise.resolve(perfResponse) } as Response);

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});
		await vi.advanceTimersByTimeAsync(100);

		// Switch to perf
		const perfBtn = container.querySelectorAll('.toggle-btn')[1] as HTMLButtonElement;
		perfBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('BROADCAST');
		expect(panel?.textContent).toContain('OUTPUT');
		expect(panel?.textContent).toContain('108,000');
		expect(panel?.textContent).toContain('BASELINE');
	});

	it('shows loading state in perf view before data arrives', async () => {
		// Mock debug fetch to succeed, then make perf fetch hang
		vi.spyOn(globalThis, 'fetch')
			.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(mockSnapshot()) } as Response)
			.mockImplementation(() => new Promise(() => {})); // never resolves

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});
		await vi.advanceTimersByTimeAsync(100);

		// Switch to perf - data won't arrive
		const perfBtn = container.querySelectorAll('.toggle-btn')[1] as HTMLButtonElement;
		perfBtn?.click();
		await vi.advanceTimersByTimeAsync(50);

		expect(container.querySelector('.loading')?.textContent).toContain('Loading perf data...');
	});

	// --- Browser tab tests ---

	function mockBrowserDiagnostics() {
		return {
			cam1: {
				renderer: {
					rafCount: 1200,
					framesDrawn: 600,
					framesSkipped: 10,
					avgRafIntervalMs: 16.67,
					minRafIntervalMs: 15.0,
					maxRafIntervalMs: 20.0,
					avgDrawMs: 0.5,
					maxDrawMs: 2.1,
					avgFrameIntervalMs: 33.3,
					minFrameIntervalMs: 30.0,
					maxFrameIntervalMs: 40.0,
					avSyncMs: 12.5,
					avSyncMin: -5.0,
					avSyncMax: 25.0,
					avSyncAvg: 10.3,
					clockMode: 'audio',
					emptyBufferHits: 3,
					currentVideoPTS: 5_000_000, // 5 seconds in microseconds
					currentAudioPTS: 4_987_500,
					videoQueueSize: 2,
					videoQueueMs: 66,
					videoTotalDiscarded: 15,
				},
				videoDecoder: null,
				audio: {
					callbackCount: 500,
					callbacksPerSec: 46.9,
					avgCallbackIntervalMs: 21.3,
					minCallbackIntervalMs: 20.0,
					maxCallbackIntervalMs: 25.0,
					scheduleAheadMs: 100,
					lastDriftMs: 2.1,
					maxDriftMs: 8.5,
					gapRepairs: 1,
					underruns: 0,
					totalSilenceMs: 45,
					totalScheduled: 500,
					decodeErrors: 0,
					ptsJumps: 0,
					ptsJumpMaxMs: 0,
					inputPtsJumps: 2,
					inputPtsWraps: 0,
					lastInputPTS: 5_000_000,
					lastOutputPTS: 4_990_000,
					contextState: 'running',
					contextSampleRate: 48000,
					contextCurrentTime: 5.0,
					contextBaseLatency: 0.005,
					contextOutputLatency: 0.01,
					isPlaying: true,
					pendingFrames: 0,
				},
				transport: null,
			},
			program: {
				renderer: {
					rafCount: 1200,
					framesDrawn: 71,
					framesSkipped: 825,
					avgRafIntervalMs: 16.67,
					minRafIntervalMs: 15.0,
					maxRafIntervalMs: 20.0,
					avgDrawMs: 0.8,
					maxDrawMs: 3.0,
					avgFrameIntervalMs: 133.0,
					minFrameIntervalMs: 33.0,
					maxFrameIntervalMs: 500.0,
					avSyncMs: 229.0,
					avSyncMin: -2700.0,
					avSyncMax: 2100.0,
					avSyncAvg: -602.0,
					clockMode: 'audio',
					emptyBufferHits: 0,
					currentVideoPTS: 10_000_000,
					currentAudioPTS: 9_771_000,
					videoQueueSize: 90,
					videoQueueMs: 3000,
					videoTotalDiscarded: 443,
				},
				videoDecoder: null,
				audio: {
					callbackCount: 500,
					callbacksPerSec: 46.9,
					avgCallbackIntervalMs: 21.3,
					minCallbackIntervalMs: 20.0,
					maxCallbackIntervalMs: 25.0,
					scheduleAheadMs: 100,
					lastDriftMs: 0.5,
					maxDriftMs: 3.0,
					gapRepairs: 0,
					underruns: 0,
					totalSilenceMs: 2916,
					totalScheduled: 500,
					decodeErrors: 0,
					ptsJumps: 0,
					ptsJumpMaxMs: 0,
					inputPtsJumps: 15,
					inputPtsWraps: 0,
					lastInputPTS: 10_000_000,
					lastOutputPTS: 9_990_000,
					contextState: 'running',
					contextSampleRate: 48000,
					contextCurrentTime: 10.0,
					contextBaseLatency: 0.005,
					contextOutputLatency: 0.01,
					isPlaying: true,
					pendingFrames: 0,
				},
				transport: null,
			},
		};
	}

	it('shows "No browser data" when pipeline is null', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: null },
		});
		await vi.advanceTimersByTimeAsync(100);

		// Switch to browser tab
		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		expect(container.querySelector('.loading')?.textContent).toContain('No browser data');
	});

	it('renders per-source cards in browser tab with section headers', async () => {
		mockFetch(mockSnapshot());

		const mockPipeline = {
			getAllDiagnostics: vi.fn().mockResolvedValue(mockBrowserDiagnostics()),
		};

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: mockPipeline as any },
		});
		await vi.advanceTimersByTimeAsync(100);

		// Switch to browser tab
		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		const text = panel?.textContent ?? '';

		// Source headers (uppercased)
		expect(text).toContain('CAM1');
		expect(text).toContain('PROGRAM');

		// Section headers
		expect(text).toContain('Render');
		expect(text).toContain('A/V Sync');
		expect(text).toContain('Audio');
	});

	it('shows render health metrics in browser tab', async () => {
		mockFetch(mockSnapshot());

		const mockPipeline = {
			getAllDiagnostics: vi.fn().mockResolvedValue(mockBrowserDiagnostics()),
		};

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: mockPipeline as any },
		});
		await vi.advanceTimersByTimeAsync(100);

		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		const text = panel?.textContent ?? '';

		// cam1 render stats
		expect(text).toContain('600/1200 rAF'); // draw rate
		expect(text).toContain('2 frames'); // queue size (cam1)
		expect(text).toContain('15'); // discarded
	});

	it('shows A/V sync metrics in browser tab', async () => {
		mockFetch(mockSnapshot());

		const mockPipeline = {
			getAllDiagnostics: vi.fn().mockResolvedValue(mockBrowserDiagnostics()),
		};

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: mockPipeline as any },
		});
		await vi.advanceTimersByTimeAsync(100);

		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		const text = panel?.textContent ?? '';

		// program has bad sync (229ms current, -602ms avg)
		expect(text).toContain('229.0ms');
		expect(text).toContain('-602.0ms');

		// PTS timecodes should be rendered
		expect(text).toContain('00:00:05.000'); // cam1 video PTS = 5s
		expect(text).toContain('00:00:10.000'); // program video PTS = 10s
	});

	it('shows audio pipeline metrics in browser tab', async () => {
		mockFetch(mockSnapshot());

		const mockPipeline = {
			getAllDiagnostics: vi.fn().mockResolvedValue(mockBrowserDiagnostics()),
		};

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: mockPipeline as any },
		});
		await vi.advanceTimersByTimeAsync(100);

		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		const text = panel?.textContent ?? '';

		// cam1 audio: 45ms silence, 2 PTS jumps, running state
		expect(text).toContain('45ms'); // silence
		expect(text).toContain('running'); // context state

		// program audio: 2916ms silence (critical)
		expect(text).toContain('2916ms');
	});

	it('applies correct status colors for queue size thresholds', async () => {
		mockFetch(mockSnapshot());

		const mockPipeline = {
			getAllDiagnostics: vi.fn().mockResolvedValue(mockBrowserDiagnostics()),
		};

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: mockPipeline as any },
		});
		await vi.advanceTimersByTimeAsync(100);

		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		// Find health dots in the browser view
		const healthDots = container.querySelectorAll('.health-dot');
		const dotClasses = Array.from(healthDots).map(d => d.className);

		// cam1 queue=2 → ok, program queue=90 → crit
		// There should be some 'ok' and some 'crit' dots
		expect(dotClasses.some(c => c.includes('ok'))).toBe(true);
		expect(dotClasses.some(c => c.includes('crit'))).toBe(true);
	});

	it('formats negative PTS as placeholder', async () => {
		mockFetch(mockSnapshot());

		const diag = mockBrowserDiagnostics();
		// Set audio PTS to -1 (no audio)
		(diag.cam1.renderer as any).currentAudioPTS = -1;

		const mockPipeline = {
			getAllDiagnostics: vi.fn().mockResolvedValue(diag),
		};

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: mockPipeline as any },
		});
		await vi.advanceTimersByTimeAsync(100);

		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('--:--:--.---');
	});

	it('polls browser diagnostics at 2s interval', async () => {
		mockFetch(mockSnapshot());

		const getAllDiagnostics = vi.fn().mockResolvedValue(mockBrowserDiagnostics());
		const mockPipeline = { getAllDiagnostics };

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn(), pipeline: mockPipeline as any },
		});
		await vi.advanceTimersByTimeAsync(100);

		// Switch to browser tab
		const browserBtn = container.querySelectorAll('.toggle-btn')[2] as HTMLButtonElement;
		browserBtn?.click();
		await vi.advanceTimersByTimeAsync(100);

		const initialCalls = getAllDiagnostics.mock.calls.length;
		expect(initialCalls).toBeGreaterThan(0);

		// Advance two poll intervals
		await vi.advanceTimersByTimeAsync(4100);
		expect(getAllDiagnostics.mock.calls.length).toBeGreaterThan(initialCalls);
	});
});
