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
		cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const, lastFrameTime: 0 },
		cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const, lastFrameTime: 0 },
	},
	seq: 1,
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
});
