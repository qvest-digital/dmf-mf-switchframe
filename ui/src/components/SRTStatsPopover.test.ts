import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SRTStatsPopover from './SRTStatsPopover.svelte';
import type { SRTSourceInfo } from '$lib/api/types';

function makeSRT(overrides: Partial<SRTSourceInfo> = {}): SRTSourceInfo {
	return {
		mode: 'listener',
		streamID: 'cam1',
		remoteAddr: '10.0.0.1:9000',
		latencyMs: 120,
		negotiatedLatencyMs: 120,
		rttMs: 15.2,
		rttVarMs: 2.1,
		lossRate: 0.02,
		bitrateKbps: 5200,
		recvBufMs: 80.5,
		recvBufPackets: 42,
		flightSize: 10,
		connected: true,
		uptimeMs: 360000,
		packetsReceived: 100000,
		packetsLost: 5,
		packetsDropped: 2,
		packetsRetransmitted: 3,
		packetsBelated: 1,
		reconnectCount: 0,
		...overrides,
	};
}

describe('SRTStatsPopover', () => {
	it('renders single column by default (no engineBStats)', () => {
		const { container } = render(SRTStatsPopover, {
			props: { srt: makeSRT(), sourceLabel: 'Camera 1', onclose: vi.fn() },
		});
		// Should show stats content
		expect(container.textContent).toContain('Camera 1');
		expect(container.textContent).toContain('Connection');
		expect(container.textContent).toContain('Network');
		expect(container.textContent).toContain('Packets');
		// Should not show engine column headers
		expect(container.querySelector('.engine-columns')).toBeNull();
		expect(container.textContent).not.toContain('Engine A');
		expect(container.textContent).not.toContain('Engine B');
	});

	it('renders single column when engineBStats is null', () => {
		const { container } = render(SRTStatsPopover, {
			props: { srt: makeSRT(), sourceLabel: 'Camera 1', onclose: vi.fn(), engineBStats: null },
		});
		expect(container.querySelector('.engine-columns')).toBeNull();
		expect(container.textContent).not.toContain('Engine A');
		expect(container.textContent).not.toContain('Engine B');
	});

	it('renders dual columns when engineBStats is provided', () => {
		const { container } = render(SRTStatsPopover, {
			props: {
				srt: makeSRT({ rttMs: 10.0 }),
				sourceLabel: 'Camera 1',
				onclose: vi.fn(),
				engineBStats: makeSRT({ rttMs: 25.0 }),
			},
		});
		const columns = container.querySelector('.engine-columns');
		expect(columns).toBeTruthy();
		expect(container.textContent).toContain('Engine A');
		expect(container.textContent).toContain('Engine B');
	});

	it('shows both engines stats values in dual column mode', () => {
		const { container } = render(SRTStatsPopover, {
			props: {
				srt: makeSRT({ bitrateKbps: 5200, packetsLost: 10 }),
				sourceLabel: 'Camera 1',
				onclose: vi.fn(),
				engineBStats: makeSRT({ bitrateKbps: 4800, packetsLost: 25 }),
			},
		});
		// Both engines should show their respective bitrate and packet stats
		expect(container.textContent).toContain('5.2 Mbps');
		expect(container.textContent).toContain('4.8 Mbps');
		expect(container.textContent).toContain('10');
		expect(container.textContent).toContain('25');
	});

	it('shows connection info for both engines', () => {
		const { container } = render(SRTStatsPopover, {
			props: {
				srt: makeSRT({ connected: true, latencyMs: 120 }),
				sourceLabel: 'Camera 1',
				onclose: vi.fn(),
				engineBStats: makeSRT({ connected: false, latencyMs: 200 }),
			},
		});
		expect(container.textContent).toContain('Connected');
		expect(container.textContent).toContain('Disconnected');
	});

	it('dual column mode uses wider max-width', () => {
		const { container } = render(SRTStatsPopover, {
			props: {
				srt: makeSRT(),
				sourceLabel: 'Camera 1',
				onclose: vi.fn(),
				engineBStats: makeSRT(),
			},
		});
		const popover = container.querySelector('.srt-popover');
		expect(popover?.classList.contains('dual-engine')).toBe(true);
	});

	it('single column mode does not have dual-engine class', () => {
		const { container } = render(SRTStatsPopover, {
			props: { srt: makeSRT(), sourceLabel: 'Camera 1', onclose: vi.fn() },
		});
		const popover = container.querySelector('.srt-popover');
		expect(popover?.classList.contains('dual-engine')).toBe(false);
	});

	it('calls onclose when Escape is pressed', async () => {
		const onclose = vi.fn();
		render(SRTStatsPopover, {
			props: { srt: makeSRT(), sourceLabel: 'Camera 1', onclose },
		});
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('calls onclose when backdrop is clicked', async () => {
		const onclose = vi.fn();
		const { container } = render(SRTStatsPopover, {
			props: { srt: makeSRT(), sourceLabel: 'Camera 1', onclose },
		});
		const backdrop = container.querySelector('.popover-backdrop') as HTMLElement;
		await fireEvent.click(backdrop);
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('shows health dots for both engines in dual mode', () => {
		const { container } = render(SRTStatsPopover, {
			props: {
				srt: makeSRT({ connected: true, lossRate: 0.0 }),
				sourceLabel: 'Camera 1',
				onclose: vi.fn(),
				engineBStats: makeSRT({ connected: true, lossRate: 2.0 }),
			},
		});
		// Should show two health dots in the dual header
		const engineHeaders = container.querySelectorAll('.engine-header');
		expect(engineHeaders.length).toBe(2);
	});
});
