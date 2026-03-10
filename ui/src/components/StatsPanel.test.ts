import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import StatsPanel from './StatsPanel.svelte';

// Standard mock snapshot with pipeline data
function mockSnapshot(overrides: Record<string, any> = {}) {
	return {
		uptime_ms: 60000,
		switcher: {
			pipeline: {
				epoch: 3,
				run_count: 100,
				last_run_ns: 5_000_000,
				max_run_ns: 12_000_000,
				total_latency_us: 200,
				lip_sync_hint_us: 3000,
				active_nodes: [
					{ name: 'upstream-key', last_ns: 500_000, max_ns: 1_000_000, latency_us: 50 },
					{ name: 'h264-encode', last_ns: 4_000_000, max_ns: 10_000_000, latency_us: 150 },
				],
			},
			video_pipeline: {
				output_fps: 30,
				frames_processed: 100,
				frames_dropped: 0,
				queue_len: 2,
			},
			frame_budget_ms: 33.3,
			frame_pool: { hits: 990, misses: 10, capacity: 32 },
			cuts_total: 5,
			transitions_completed: 3,
			...overrides.switcher,
		},
		mixer: {
			mode: 'passthrough',
			frames_mixed: 0,
			frames_passthrough: 500,
			max_inter_frame_gap_ms: 34.0,
			...overrides.mixer,
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

	it('shows audio mixer section when present', async () => {
		mockFetch(mockSnapshot({
			mixer: {
				mode: 'mixing',
				frames_mixed: 1000,
				frames_passthrough: 0,
				max_inter_frame_gap_ms: 45.0,
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
	});

	it('shows system stats with frame pool hit rate', async () => {
		mockFetch(mockSnapshot());

		const { container } = render(StatsPanel, {
			props: { visible: true, onclose: vi.fn() },
		});

		await vi.advanceTimersByTimeAsync(100);

		const panel = container.querySelector('.stats-panel');
		expect(panel?.textContent).toContain('99.0% hit');
		expect(panel?.textContent).toContain('32 cap');
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
});
