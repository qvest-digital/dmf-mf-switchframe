import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import ProgramPreview from './ProgramPreview.svelte';

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
	timestamp: Date.now(),
};

describe('ProgramPreview with video', () => {
	it('should render canvas in program window', () => {
		const { container } = render(ProgramPreview, { props: { state: baseState } });
		const programCanvas = container.querySelector('.program-monitor canvas');
		expect(programCanvas).toBeTruthy();
	});

	it('should render canvas in preview window', () => {
		const { container } = render(ProgramPreview, { props: { state: baseState } });
		const previewCanvas = container.querySelector('.preview-monitor canvas');
		expect(previewCanvas).toBeTruthy();
	});

	it('should render canvases without id attributes', () => {
		const { container } = render(ProgramPreview, { props: { state: baseState } });
		const canvases = container.querySelectorAll('canvas');
		for (const canvas of canvases) {
			expect(canvas.id).toBe('');
		}
	});

	it('should show source label in program window', () => {
		const { container } = render(ProgramPreview, { props: { state: baseState } });
		expect(container.textContent).toContain('Camera 1');
	});

	it('should not show health alarm when program source is healthy', () => {
		const { container } = render(ProgramPreview, { props: { state: baseState } });
		expect(container.querySelector('.health-alarm')).toBeNull();
	});

	it('should show health alarm when program source is offline', () => {
		const degradedState = {
			...baseState,
			sources: {
				...baseState.sources,
				cam1: { ...baseState.sources.cam1, status: 'offline' as const },
			},
		};
		const { container } = render(ProgramPreview, { props: { state: degradedState } });
		const alarm = container.querySelector('.health-alarm');
		expect(alarm).toBeTruthy();
		expect(alarm?.textContent).toContain('Camera 1');
		expect(alarm?.textContent).toContain('OFFLINE');
	});

	it('should show health alarm only on program monitor, not preview', () => {
		const degradedState = {
			...baseState,
			sources: {
				...baseState.sources,
				cam1: { ...baseState.sources.cam1, status: 'no_signal' as const },
			},
		};
		const { container } = render(ProgramPreview, { props: { state: degradedState } });
		const programAlarm = container.querySelector('.program-monitor .health-alarm');
		const previewAlarm = container.querySelector('.preview-monitor .health-alarm');
		expect(programAlarm).toBeTruthy();
		expect(previewAlarm).toBeNull();
	});
});

describe('ProgramPreview SCTE-35 break status', () => {
	it('renders PREVIEW and PROGRAM labels', () => {
		const { container } = render(ProgramPreview, { props: { state: baseState } });
		expect(container.textContent).toContain('PREVIEW');
		expect(container.textContent).toContain('PROGRAM');
	});

	it('does not show break status when no SCTE-35 events', () => {
		const { container } = render(ProgramPreview, { props: { state: baseState } });
		const breakStatus = container.querySelector('.break-status');
		expect(breakStatus).toBeFalsy();
	});

	it('shows break status banner when active SCTE-35 out event', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {
					'1': {
						eventId: 1,
						commandType: 'splice_insert',
						isOut: true,
						durationMs: 30000,
						elapsedMs: 5000,
						remainingMs: 25000,
						autoReturn: true,
						held: false,
						spliceTimePts: 0,
						startedAt: Date.now() - 5000,
					},
				},
				eventLog: [],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(ProgramPreview, { props: { state } });
		const breakStatus = container.querySelector('.break-status');
		expect(breakStatus).toBeTruthy();
		expect(breakStatus!.textContent).toContain('AD BREAK');
	});

	it('shows HELD state in break status banner', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {
					'1': {
						eventId: 1,
						commandType: 'splice_insert',
						isOut: true,
						durationMs: 30000,
						elapsedMs: 5000,
						remainingMs: 25000,
						autoReturn: true,
						held: true,
						spliceTimePts: 0,
						startedAt: Date.now() - 5000,
					},
				},
				eventLog: [],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(ProgramPreview, { props: { state } });
		const breakStatus = container.querySelector('.break-status');
		expect(breakStatus).toBeTruthy();
		expect(breakStatus!.classList.contains('break-held')).toBe(true);
		expect(breakStatus!.textContent).toContain('HELD');
	});

	it('shows countdown in break status banner', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {
					'1': {
						eventId: 1,
						commandType: 'splice_insert',
						isOut: true,
						durationMs: 60000,
						elapsedMs: 10000,
						remainingMs: 50000,
						autoReturn: true,
						held: false,
						spliceTimePts: 0,
						startedAt: Date.now() - 10000,
					},
				},
				eventLog: [],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(ProgramPreview, { props: { state } });
		const countdown = container.querySelector('.break-countdown');
		expect(countdown).toBeTruthy();
		// Should show something like "0:50" (50 seconds remaining)
		expect(countdown!.textContent).toMatch(/\d+:\d{2}/);
	});

	it('does not show break status for non-out events', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {
					'1': {
						eventId: 1,
						commandType: 'splice_insert',
						isOut: false,
						durationMs: 30000,
						elapsedMs: 0,
						remainingMs: 30000,
						autoReturn: false,
						held: false,
						spliceTimePts: 0,
						startedAt: Date.now(),
					},
				},
				eventLog: [],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(ProgramPreview, { props: { state } });
		const breakStatus = container.querySelector('.break-status');
		expect(breakStatus).toBeFalsy();
	});
});
