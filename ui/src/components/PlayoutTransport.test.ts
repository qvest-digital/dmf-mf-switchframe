import { describe, it, expect, vi, beforeEach } from 'vitest';
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
});
