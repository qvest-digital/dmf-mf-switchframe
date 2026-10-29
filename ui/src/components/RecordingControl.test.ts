import { describe, it, expect } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import RecordingControl from './RecordingControl.svelte';

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

describe('RecordingControl', () => {
	it('should render REC when idle', () => {
		const { container } = render(RecordingControl, { props: { state: baseState } });
		expect(container.textContent).toContain('REC');
	});

	it('should show .rec-active class when recording active', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 10 },
		};
		const { container } = render(RecordingControl, { props: { state } });
		const active = container.querySelector('.rec-active');
		expect(active).toBeTruthy();
	});

	it('should show duration in MM:SS format (65.5 secs -> 01:05)', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 65.5 },
		};
		const { container } = render(RecordingControl, { props: { state } });
		expect(container.textContent).toContain('01:05');
	});

	it('should show error text when error', () => {
		const state = {
			...baseState,
			recording: { active: false, error: 'disk full' },
		};
		const { container } = render(RecordingControl, { props: { state } });
		expect(container.textContent).toContain('disk full');
	});

	it('should show .rec-stop button when recording', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 30 },
		};
		const { container } = render(RecordingControl, { props: { state } });
		const stopBtn = container.querySelector('.rec-stop');
		expect(stopBtn).toBeTruthy();
	});

	it('should not show .rec-active when idle', () => {
		const { container } = render(RecordingControl, { props: { state: baseState } });
		const active = container.querySelector('.rec-active');
		expect(active).toBeFalsy();
	});

	it('should format zero duration as 00:00', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 0 },
		};
		const { container } = render(RecordingControl, { props: { state } });
		expect(container.textContent).toContain('00:00');
	});

	it('should show confirmation dialog when stop is clicked', async () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 30 },
		};
		const { container } = render(RecordingControl, { props: { state } });
		const stopBtn = container.querySelector('.rec-stop') as HTMLButtonElement;
		await fireEvent.click(stopBtn);
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeTruthy();
		expect(container.textContent).toContain('Stop recording?');
	});

	it('should dismiss confirmation dialog on cancel', async () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 30 },
		};
		const { container } = render(RecordingControl, { props: { state } });
		const stopBtn = container.querySelector('.rec-stop') as HTMLButtonElement;
		await fireEvent.click(stopBtn);
		const cancelBtn = container.querySelector('.cancel-btn') as HTMLButtonElement;
		await fireEvent.click(cancelBtn);
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeFalsy();
	});

	// --- Dual-engine per-engine recording badges ---

	it('should show dual dots when recording states differ (A recording, B not)', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 10 },
		};
		const { container } = render(RecordingControl, {
			props: {
				state,
				isDualEngine: true,
				engineBRecording: { active: false },
				activeEngineId: 'A' as const,
			},
		});
		const engineStatus = container.querySelector('[data-testid="engine-rec-status"]');
		expect(engineStatus).toBeTruthy();
		// A should have recording dot, B should not
		const dots = engineStatus!.querySelectorAll('.rec-dot-small');
		expect(dots).toHaveLength(2);
		expect(dots[0].classList.contains('recording')).toBe(true);
		expect(dots[1].classList.contains('recording')).toBe(false);
	});

	it('should show dual dots when recording states differ (A not recording, B recording)', () => {
		const state = {
			...baseState,
			recording: { active: false },
		};
		const { container } = render(RecordingControl, {
			props: {
				state,
				isDualEngine: true,
				engineBRecording: { active: true, durationSecs: 5 },
				activeEngineId: 'A' as const,
			},
		});
		const engineStatus = container.querySelector('[data-testid="engine-rec-status"]');
		expect(engineStatus).toBeTruthy();
		const dots = engineStatus!.querySelectorAll('.rec-dot-small');
		expect(dots).toHaveLength(2);
		expect(dots[0].classList.contains('recording')).toBe(false);
		expect(dots[1].classList.contains('recording')).toBe(true);
	});

	it('should NOT show dual dots when recording states match (both recording)', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 10 },
		};
		const { container } = render(RecordingControl, {
			props: {
				state,
				isDualEngine: true,
				engineBRecording: { active: true, durationSecs: 10 },
				activeEngineId: 'A' as const,
			},
		});
		const engineStatus = container.querySelector('[data-testid="engine-rec-status"]');
		expect(engineStatus).toBeFalsy();
	});

	it('should NOT show dual dots when recording states match (both idle)', () => {
		const { container } = render(RecordingControl, {
			props: {
				state: baseState,
				isDualEngine: true,
				engineBRecording: { active: false },
				activeEngineId: 'A' as const,
			},
		});
		const engineStatus = container.querySelector('[data-testid="engine-rec-status"]');
		expect(engineStatus).toBeFalsy();
	});

	it('should show per-engine fix buttons when states differ (A recording, B not)', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 10 },
		};
		const { container } = render(RecordingControl, {
			props: {
				state,
				isDualEngine: true,
				engineBRecording: { active: false },
				activeEngineId: 'A' as const,
			},
		});
		const engineStatus = container.querySelector('[data-testid="engine-rec-status"]');
		expect(engineStatus).toBeTruthy();
		const fixBtns = engineStatus!.querySelectorAll('.engine-fix-btn');
		expect(fixBtns).toHaveLength(2);
		// A is recording -> STOP button; B is idle -> REC button
		expect(fixBtns[0].textContent).toBe('STOP');
		expect(fixBtns[1].textContent).toBe('REC');
	});

	it('should show per-engine fix buttons when states differ (A idle, B recording)', () => {
		const state = {
			...baseState,
			recording: { active: false },
		};
		const { container } = render(RecordingControl, {
			props: {
				state,
				isDualEngine: true,
				engineBRecording: { active: true, durationSecs: 5 },
				activeEngineId: 'A' as const,
			},
		});
		const engineStatus = container.querySelector('[data-testid="engine-rec-status"]');
		expect(engineStatus).toBeTruthy();
		const fixBtns = engineStatus!.querySelectorAll('.engine-fix-btn');
		expect(fixBtns).toHaveLength(2);
		// A is idle -> REC button; B is recording -> STOP button
		expect(fixBtns[0].textContent).toBe('REC');
		expect(fixBtns[1].textContent).toBe('STOP');
	});

	it('should NOT show dual dots when not in dual-engine mode', () => {
		const state = {
			...baseState,
			recording: { active: true, filename: 'out.ts', durationSecs: 10 },
		};
		const { container } = render(RecordingControl, {
			props: {
				state,
				isDualEngine: false,
				engineBRecording: { active: false },
			},
		});
		const engineStatus = container.querySelector('[data-testid="engine-rec-status"]');
		expect(engineStatus).toBeFalsy();
	});
});

describe('RecordingControl without program video', () => {
	it('flags a running recording that has written nothing after two seconds', () => {
		const state = { ...baseState, recording: { active: true, filename: 'r.ts', durationSecs: 5 } };
		const { container } = render(RecordingControl, { props: { state } });
		expect(container.querySelector('.rec-waiting')?.textContent).toContain('NO VIDEO');
	});

	it('does not flag one that is writing, or one that just started', () => {
		for (const recording of [
			{ active: true, filename: 'r.ts', durationSecs: 5, bytesWritten: 1880 },
			{ active: true, filename: 'r.ts', durationSecs: 0.5 },
		]) {
			const { container, unmount } = render(RecordingControl, { props: { state: { ...baseState, recording } } });
			expect(container.querySelector('.rec-waiting')).toBeNull();
			unmount();
		}
	});

	it('shows why a stopped recording recorded nothing', () => {
		const state = { ...baseState, recording: { active: false, error: 'no program video reached the recorder, so nothing was recorded' } };
		const { container } = render(RecordingControl, { props: { state } });
		expect(container.textContent).toContain('no program video');
	});
});
