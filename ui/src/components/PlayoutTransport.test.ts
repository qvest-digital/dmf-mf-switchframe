import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import { tick } from 'svelte';
import PlayoutTransport from './PlayoutTransport.svelte';
import type { PlayoutChannelState } from '$lib/api/types';
import * as api from '$lib/api/switch-api';

vi.mock('$lib/api/switch-api', () => ({
	playoutStart: vi.fn().mockResolvedValue(undefined),
	playoutStop: vi.fn().mockResolvedValue(undefined),
	playoutNext: vi.fn().mockResolvedValue(undefined),
	playoutPrevious: vi.fn().mockResolvedValue(undefined),
	playoutTakeManual: vi.fn().mockResolvedValue(undefined),
	playoutReturnToAuto: vi.fn().mockResolvedValue(undefined),
	apiCall: vi.fn(),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

function makeChannel(overrides: Partial<PlayoutChannelState> = {}): PlayoutChannelState {
	return {
		id: 1,
		state: 'stopped',
		itemIndex: -1,
		itemCount: 0,
		...overrides,
	};
}

describe('PlayoutTransport', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('renders transport buttons (at least 4 .t-btn elements)', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel() },
		});
		const buttons = container.querySelectorAll('.t-btn');
		expect(buttons.length).toBeGreaterThanOrEqual(4);
	});

	it('disables prev/next/stop when stopped', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'stopped' }) },
		});
		const prevBtn = container.querySelector('button[title="Previous item"]') as HTMLButtonElement;
		const nextBtn = container.querySelector('button[title="Next item"]') as HTMLButtonElement;
		const stopBtn = container.querySelector('button[title="Stop playout"]') as HTMLButtonElement;

		expect(prevBtn.disabled).toBe(true);
		expect(nextBtn.disabled).toBe(true);
		expect(stopBtn.disabled).toBe(true);
	});

	it('shows AUTOMATED mode pill when mode is automated', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const pill = container.querySelector('.mode-pill.auto');
		expect(pill).toBeTruthy();
		expect(pill!.textContent).toBe('AUTOMATED');
	});

	it('shows MANUAL mode pill when mode is manual', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'manual' }) },
		});
		const pill = container.querySelector('.mode-pill.manual');
		expect(pill).toBeTruthy();
		expect(pill!.textContent).toBe('MANUAL');
	});

	it('shows BREAK mode pill when mode is break', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'break' }) },
		});
		const pill = container.querySelector('.mode-pill.break-mode');
		expect(pill).toBeTruthy();
		expect(pill!.textContent).toBe('BREAK');
	});

	it('shows TAKE MANUAL button when in auto mode', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const modeBtn = container.querySelector('.mode-btn');
		expect(modeBtn).toBeTruthy();
		expect(modeBtn!.textContent).toBe('TAKE MANUAL');
	});

	it('shows RETURN TO AUTO button when in manual mode', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'manual' }) },
		});
		const modeBtn = container.querySelector('.mode-btn');
		expect(modeBtn).toBeTruthy();
		expect(modeBtn!.textContent).toBe('RETURN TO AUTO');
	});

	it('calls apiCall on play button click when stopped', async () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'stopped' }) },
		});
		const playBtn = container.querySelector('.t-main') as HTMLButtonElement;
		expect(playBtn).toBeTruthy();

		await fireEvent.click(playBtn);
		await tick();

		expect(api.apiCall).toHaveBeenCalled();
		expect(api.playoutStart).toHaveBeenCalledWith(1);
	});

	it('shows on-air styling when on air', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const mainBtn = container.querySelector('.t-main.on-air');
		expect(mainBtn).toBeTruthy();
	});

	// ── Stop confirmation tests ──

	it('first stop click shows CONFIRM STOP when on-air', async () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const stopBtn = container.querySelector('.t-stop') as HTMLButtonElement;
		expect(stopBtn).toBeTruthy();

		await fireEvent.click(stopBtn);
		await tick();

		// Should NOT have called stop yet
		expect(api.playoutStop).not.toHaveBeenCalled();
		expect(api.apiCall).not.toHaveBeenCalled();

		// Button should show confirm text and have confirming class
		expect(stopBtn.textContent?.trim()).toBe('CONFIRM STOP');
		expect(stopBtn.classList.contains('confirming')).toBe(true);
	});

	it('second stop click within 3s executes stop', async () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const stopBtn = container.querySelector('.t-stop') as HTMLButtonElement;

		// First click - enter confirm mode
		await fireEvent.click(stopBtn);
		await tick();
		expect(api.apiCall).not.toHaveBeenCalled();

		// Second click - should execute stop
		await fireEvent.click(stopBtn);
		await tick();

		expect(api.apiCall).toHaveBeenCalled();
		expect(api.playoutStop).toHaveBeenCalledWith(1);
	});

	it('confirm stop reverts after 3s timeout', async () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const stopBtn = container.querySelector('.t-stop') as HTMLButtonElement;

		// First click - enter confirm mode
		await fireEvent.click(stopBtn);
		await tick();
		expect(stopBtn.textContent?.trim()).toBe('CONFIRM STOP');

		// Advance past the 3s timeout
		vi.advanceTimersByTime(3100);
		await tick();

		// Should revert - no longer showing CONFIRM STOP
		expect(stopBtn.classList.contains('confirming')).toBe(false);
		expect(stopBtn.textContent?.trim()).not.toBe('CONFIRM STOP');
	});

	it('single click stop works when not on-air', async () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'cued' }) },
		});
		const stopBtn = container.querySelector('.t-stop') as HTMLButtonElement;
		expect(stopBtn).toBeTruthy();

		await fireEvent.click(stopBtn);
		await tick();

		// Should execute stop directly without confirmation
		expect(api.apiCall).toHaveBeenCalled();
		expect(api.playoutStop).toHaveBeenCalledWith(1);
	});

	// ── Mode pill styling tests ──

	it('auto mode pill has green styling with border', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const pill = container.querySelector('.mode-pill.auto') as HTMLElement;
		expect(pill).toBeTruthy();
		// Verify the class is applied (CSS will handle the visual styling)
		expect(pill.classList.contains('auto')).toBe(true);
	});

	it('manual mode pill has animation class', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'manual' }) },
		});
		const pill = container.querySelector('.mode-pill.manual') as HTMLElement;
		expect(pill).toBeTruthy();
		// The manual class applies the mode-pulse animation via CSS
		expect(pill.classList.contains('manual')).toBe(true);
	});

	it('break mode pill has animation class', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'break' }) },
		});
		const pill = container.querySelector('.mode-pill.break-mode') as HTMLElement;
		expect(pill).toBeTruthy();
		expect(pill.classList.contains('break-mode')).toBe(true);
	});

	// ── Elapsed time tests ──

	it('shows elapsed time when on-air', async () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		await tick();

		const elapsedEl = container.querySelector('.elapsed');
		expect(elapsedEl).toBeTruthy();
		expect(elapsedEl!.textContent).toBe('0:00');
	});

	it('does not show elapsed time when stopped', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'stopped' }) },
		});
		const elapsedEl = container.querySelector('.elapsed');
		expect(elapsedEl).toBeFalsy();
	});

	// ── Next button styling ──

	it('next button has t-next class for larger sizing', () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const nextBtn = container.querySelector('.t-next');
		expect(nextBtn).toBeTruthy();
	});

	// ── Tooltip tests ──

	it('stop button shows confirm tooltip when confirming', async () => {
		const { container } = render(PlayoutTransport, {
			props: { channel: makeChannel({ state: 'on_air', mode: 'automated' }) },
		});
		const stopBtn = container.querySelector('.t-stop') as HTMLButtonElement;

		await fireEvent.click(stopBtn);
		await tick();

		expect(stopBtn.title).toBe('Click again to confirm stop');
	});

	// ── Dual-engine cache warning tests ──

	it('shows cache warning when engine has fetching items on play click', async () => {
		const getCacheStatus = vi.fn().mockImplementation((engine: string) => {
			if (engine === 'A') return Promise.resolve({ cacheDir: '/tmp', maxBytes: 1e9, usedBytes: 5e8, itemCount: 10, fetchingCount: 0, errorCount: 0 });
			return Promise.resolve({ cacheDir: '/tmp', maxBytes: 1e9, usedBytes: 5e8, itemCount: 8, fetchingCount: 2, errorCount: 0 });
		});
		const { container } = render(PlayoutTransport, {
			props: {
				channel: makeChannel({ state: 'stopped' }),
				isDualEngine: true,
				getCacheStatus,
			},
		});
		const playBtn = container.querySelector('.t-main') as HTMLButtonElement;
		await fireEvent.click(playBtn);
		await tick();

		// Should still call play (non-blocking)
		expect(api.apiCall).toHaveBeenCalled();
		expect(api.playoutStart).toHaveBeenCalledWith(1);

		// Should show cache warning
		const warning = container.querySelector('.cache-warning');
		expect(warning).toBeTruthy();
		expect(warning!.textContent).toContain('Engine B');
		expect(warning!.textContent).toContain('2');
	});

	it('does not show cache warning in single-engine mode', async () => {
		const { container } = render(PlayoutTransport, {
			props: {
				channel: makeChannel({ state: 'stopped' }),
				isDualEngine: false,
			},
		});
		const playBtn = container.querySelector('.t-main') as HTMLButtonElement;
		await fireEvent.click(playBtn);
		await tick();

		const warning = container.querySelector('.cache-warning');
		expect(warning).toBeFalsy();
	});

	it('does not show cache warning when both engines have fetchingCount === 0', async () => {
		const getCacheStatus = vi.fn().mockResolvedValue({
			cacheDir: '/tmp', maxBytes: 1e9, usedBytes: 5e8, itemCount: 10, fetchingCount: 0, errorCount: 0,
		});
		const { container } = render(PlayoutTransport, {
			props: {
				channel: makeChannel({ state: 'stopped' }),
				isDualEngine: true,
				getCacheStatus,
			},
		});
		const playBtn = container.querySelector('.t-main') as HTMLButtonElement;
		await fireEvent.click(playBtn);
		await tick();

		const warning = container.querySelector('.cache-warning');
		expect(warning).toBeFalsy();
	});

	it('shows cache warning for both engines when both are fetching', async () => {
		const getCacheStatus = vi.fn().mockImplementation((engine: string) => {
			if (engine === 'A') return Promise.resolve({ cacheDir: '/tmp', maxBytes: 1e9, usedBytes: 5e8, itemCount: 10, fetchingCount: 3, errorCount: 0 });
			return Promise.resolve({ cacheDir: '/tmp', maxBytes: 1e9, usedBytes: 5e8, itemCount: 8, fetchingCount: 1, errorCount: 0 });
		});
		const { container } = render(PlayoutTransport, {
			props: {
				channel: makeChannel({ state: 'stopped' }),
				isDualEngine: true,
				getCacheStatus,
			},
		});
		const playBtn = container.querySelector('.t-main') as HTMLButtonElement;
		await fireEvent.click(playBtn);
		await tick();

		const warning = container.querySelector('.cache-warning');
		expect(warning).toBeTruthy();
		expect(warning!.textContent).toContain('Engine A');
		expect(warning!.textContent).toContain('Engine B');
	});
});
