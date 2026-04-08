import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import { fireEvent } from '@testing-library/svelte';
import OutputControls from './OutputControls.svelte';
import { getConfirmMode, setConfirmMode } from '$lib/state/preferences.svelte';
import type { SyncHealth, SyncStatus } from '$lib/state/reconciler.svelte';

function mockSyncHealth(status: SyncStatus): SyncHealth {
	return {
		status,
		divergences: [],
		authoritative: 'A',
		engineA: { lastSeen: 0, lastCommandSeq: 0, connected: true },
		engineB: { lastSeen: 0, lastCommandSeq: 0, connected: true },
		warnings: [],
		staleEngines: [],
	};
}

const baseState = {
	programSource: 'cam1',
	previewSource: 'cam2',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioChannels: undefined,
	masterLevel: 0,
	programPeak: [0, 0] as [number, number],
	tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
	sources: {
		cam1: { key: 'cam1', label: 'Camera 1', type: 'demo' as const, status: 'healthy' as const },
		cam2: { key: 'cam2', label: 'Camera 2', type: 'demo' as const, status: 'healthy' as const },
	},
	seq: 1,
	lastCommandSeq: 0,
	timestamp: Date.now(),
};

describe('OutputControls', () => {
	it('should render recording control', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('REC');
	});

	it('should render I/O button', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('I/O');
	});

	it('should show I/O active indicator when ioPanelVisible', () => {
		const { container } = render(OutputControls, { props: { state: baseState, ioPanelVisible: true } });
		const indicator = container.querySelector('.io-active');
		expect(indicator).toBeTruthy();
	});

	it('should show I/O active indicator when SRT output is active', () => {
		const state = {
			...baseState,
			srtOutput: { active: true, mode: 'caller' as const, state: 'active' },
		};
		const { container } = render(OutputControls, { props: { state } });
		const indicator = container.querySelector('.io-active');
		expect(indicator).toBeTruthy();
	});

	it('should not show I/O active indicator when inactive and panel closed', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		const indicator = container.querySelector('.io-active');
		expect(indicator).toBeFalsy();
	});

	it('should have output-controls container', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		const controls = container.querySelector('.output-controls');
		expect(controls).toBeTruthy();
	});

	it('should render a CONFIRM button', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		expect(container.textContent).toContain('CONFIRM');
	});

	it('should toggle confirm mode on click', async () => {
		setConfirmMode(false);
		const { container } = render(OutputControls, { props: { state: baseState } });
		const btn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		expect(btn).toBeTruthy();
		expect(getConfirmMode()).toBe(false);
		await fireEvent.click(btn);
		expect(getConfirmMode()).toBe(true);
		await fireEvent.click(btn);
		expect(getConfirmMode()).toBe(false);
	});

	it('should show active state when confirm mode is on', () => {
		setConfirmMode(true);
		const { container } = render(OutputControls, { props: { state: baseState } });
		const btn = container.querySelector('.confirm-btn');
		expect(btn).toBeTruthy();
		expect(btn?.classList.contains('confirm-active')).toBe(true);
	});

	it('I/O button calls onToggleIOPanel callback', async () => {
		const onToggleIOPanel = vi.fn();
		const { container } = render(OutputControls, { props: { state: baseState, onToggleIOPanel } });
		const ioBtn = container.querySelector('.io-btn') as HTMLButtonElement;
		expect(ioBtn).toBeTruthy();
		await fireEvent.click(ioBtn);
		expect(onToggleIOPanel).toHaveBeenCalledOnce();
	});

	it('I/O button shows io-active class when ioPanelVisible is true', () => {
		const { container } = render(OutputControls, { props: { state: baseState, ioPanelVisible: true } });
		const ioBtn = container.querySelector('.io-btn');
		expect(ioBtn).toBeTruthy();
		expect(ioBtn?.classList.contains('io-active')).toBe(true);
	});

	it('I/O button shows io-warning class when SRT input is unhealthy', () => {
		const state = {
			...baseState,
			sources: {
				...baseState.sources,
				srt1: { key: 'srt1', label: 'SRT 1', type: 'srt' as const, status: 'no_signal' as const },
			},
		};
		const { container } = render(OutputControls, { props: { state } });
		const ioBtn = container.querySelector('.io-btn');
		expect(ioBtn).toBeTruthy();
		expect(ioBtn?.classList.contains('io-warning')).toBe(true);
	});

	it('I/O button does not show io-warning when io-active takes precedence', () => {
		const state = {
			...baseState,
			sources: {
				...baseState.sources,
				srt1: { key: 'srt1', label: 'SRT 1', type: 'srt' as const, status: 'no_signal' as const },
			},
		};
		const { container } = render(OutputControls, { props: { state, ioPanelVisible: true } });
		const ioBtn = container.querySelector('.io-btn');
		expect(ioBtn).toBeTruthy();
		// When active, io-active takes priority over io-warning
		expect(ioBtn?.classList.contains('io-active')).toBe(true);
		expect(ioBtn?.classList.contains('io-warning')).toBe(false);
	});

	// --- Format Badge ---

	it('should not show format badge when pipelineFormat is missing', () => {
		const { container } = render(OutputControls, { props: { state: baseState } });
		const badge = container.querySelector('.format-badge');
		expect(badge).toBeFalsy();
	});

	it('should not show format badge when codec is missing', () => {
		const state = {
			...baseState,
			pipelineFormat: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: 'standard' },
		};
		const { container } = render(OutputControls, { props: { state } });
		const badge = container.querySelector('.format-badge');
		expect(badge).toBeFalsy();
	});

	it('should show H.264 · 420 8-bit badge for standard format', () => {
		const state = {
			...baseState,
			pipelineFormat: {
				width: 1920, height: 1080, fpsNum: 30, fpsDen: 1,
				name: 'standard', codec: 'h264', colorFormat: 'yuv420p',
			},
		};
		const { container } = render(OutputControls, { props: { state } });
		const badge = container.querySelector('.format-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toContain('H.264');
		expect(badge?.textContent).toContain('420 8-bit');
	});

	it('should show HEVC · 420 8-bit badge for standard-hevc format', () => {
		const state = {
			...baseState,
			pipelineFormat: {
				width: 1920, height: 1080, fpsNum: 30, fpsDen: 1,
				name: 'standard-hevc', codec: 'h265', colorFormat: 'yuv420p',
			},
		};
		const { container } = render(OutputControls, { props: { state } });
		const badge = container.querySelector('.format-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toContain('HEVC');
		expect(badge?.textContent).toContain('420 8-bit');
	});

	it('should show HEVC · 422 10-bit badge for professional format', () => {
		const state = {
			...baseState,
			pipelineFormat: {
				width: 1920, height: 1080, fpsNum: 30, fpsDen: 1,
				name: 'professional', codec: 'h265', colorFormat: 'yuv422p10le',
			},
		};
		const { container } = render(OutputControls, { props: { state } });
		const badge = container.querySelector('.format-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toContain('HEVC');
		expect(badge?.textContent).toContain('422 10-bit');
	});

	// --- Dual-engine per-engine output badges ---

	it('should show per-engine output badges when SRT output states differ', () => {
		const state = {
			...baseState,
			srtOutput: { active: true, mode: 'caller' as const, state: 'active' },
		};
		const engineBState = {
			...baseState,
			srtOutput: { active: false, mode: 'caller' as const, state: 'idle' },
		};
		const { container } = render(OutputControls, {
			props: { state, isDualEngine: true, engineBState, activeEngineId: 'A' as const },
		});
		const outputStatus = container.querySelector('[data-testid="engine-output-status"]');
		expect(outputStatus).toBeTruthy();
		const dots = outputStatus!.querySelectorAll('.out-dot');
		expect(dots).toHaveLength(2);
		expect(dots[0].classList.contains('active')).toBe(true);
		expect(dots[1].classList.contains('active')).toBe(false);
	});

	it('should NOT show per-engine output badges when SRT output states match', () => {
		const state = {
			...baseState,
			srtOutput: { active: true, mode: 'caller' as const, state: 'active' },
		};
		const engineBState = {
			...baseState,
			srtOutput: { active: true, mode: 'caller' as const, state: 'active' },
		};
		const { container } = render(OutputControls, {
			props: { state, isDualEngine: true, engineBState, activeEngineId: 'A' as const },
		});
		const outputStatus = container.querySelector('[data-testid="engine-output-status"]');
		expect(outputStatus).toBeFalsy();
	});

	it('should NOT show per-engine output badges when not in dual-engine mode', () => {
		const state = {
			...baseState,
			srtOutput: { active: true, mode: 'caller' as const, state: 'active' },
		};
		const engineBState = {
			...baseState,
			srtOutput: { active: false, mode: 'caller' as const, state: 'idle' },
		};
		const { container } = render(OutputControls, {
			props: { state, isDualEngine: false, engineBState },
		});
		const outputStatus = container.querySelector('[data-testid="engine-output-status"]');
		expect(outputStatus).toBeFalsy();
	});

	it('should NOT show per-engine output badges when both SRT inactive', () => {
		const { container } = render(OutputControls, {
			props: { state: baseState, isDualEngine: true, engineBState: baseState, activeEngineId: 'A' as const },
		});
		const outputStatus = container.querySelector('[data-testid="engine-output-status"]');
		expect(outputStatus).toBeFalsy();
	});

	// --- DUAL badge ---

	it('shows DUAL badge when isDualEngine is true', () => {
		const { container } = render(OutputControls, {
			props: { state: baseState, isDualEngine: true },
		});
		const badge = container.querySelector('.dual-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent?.trim()).toBe('DUAL');
	});

	it('does not show DUAL badge when isDualEngine is false', () => {
		const { container } = render(OutputControls, {
			props: { state: baseState, isDualEngine: false },
		});
		const badge = container.querySelector('.dual-badge');
		expect(badge).toBeFalsy();
	});

	it('DUAL badge has warning class when syncHealth status is lagging', () => {
		const { container } = render(OutputControls, {
			props: {
				state: baseState,
				isDualEngine: true,
				syncHealth: mockSyncHealth('lagging'),
			},
		});
		const badge = container.querySelector('.dual-badge');
		expect(badge).toBeTruthy();
		expect(badge?.classList.contains('warning')).toBe(true);
	});

	it('DUAL badge has error class when syncHealth status is mismatch', () => {
		const { container } = render(OutputControls, {
			props: {
				state: baseState,
				isDualEngine: true,
				syncHealth: mockSyncHealth('mismatch'),
			},
		});
		const badge = container.querySelector('.dual-badge');
		expect(badge).toBeTruthy();
		expect(badge?.classList.contains('error')).toBe(true);
	});

	it('DUAL badge has no status class when syncHealth is null', () => {
		const { container } = render(OutputControls, {
			props: {
				state: baseState,
				isDualEngine: true,
				syncHealth: null,
			},
		});
		const badge = container.querySelector('.dual-badge');
		expect(badge).toBeTruthy();
		expect(badge?.classList.contains('warning')).toBe(false);
		expect(badge?.classList.contains('error')).toBe(false);
		expect(badge?.classList.contains('synced')).toBe(false);
	});

	// --- Clock divergence warning ---

	it('shows CLOCK warning when syncHealth has clock_diverged warning', () => {
		const health = mockSyncHealth('synced');
		health.warnings = ['clock_diverged'];
		const { container } = render(OutputControls, {
			props: { state: baseState, syncHealth: health },
		});
		const clockWarn = container.querySelector('.clock-warn');
		expect(clockWarn).toBeTruthy();
		expect(clockWarn?.textContent?.trim()).toBe('CLOCK');
	});

	it('does not show CLOCK warning when syncHealth has no clock_diverged warning', () => {
		const { container } = render(OutputControls, {
			props: { state: baseState, syncHealth: mockSyncHealth('synced') },
		});
		const clockWarn = container.querySelector('.clock-warn');
		expect(clockWarn).toBeFalsy();
	});

	it('does not show CLOCK warning when syncHealth is null', () => {
		const { container } = render(OutputControls, {
			props: { state: baseState, syncHealth: null },
		});
		const clockWarn = container.querySelector('.clock-warn');
		expect(clockWarn).toBeFalsy();
	});

	it('DUAL badge has synced class when syncHealth status is synced', () => {
		const { container } = render(OutputControls, {
			props: {
				state: baseState,
				isDualEngine: true,
				syncHealth: mockSyncHealth('synced'),
			},
		});
		const badge = container.querySelector('.dual-badge');
		expect(badge).toBeTruthy();
		expect(badge?.classList.contains('synced')).toBe(true);
	});
});
