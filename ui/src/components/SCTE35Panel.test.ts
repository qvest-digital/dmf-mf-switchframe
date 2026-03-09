import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SCTE35Panel from './SCTE35Panel.svelte';

vi.mock('$lib/api/switch-api', () => ({
	scte35Cue: vi.fn().mockResolvedValue({ programSource: 'cam1', seq: 1 }),
	scte35Return: vi.fn().mockResolvedValue({ programSource: 'cam1', seq: 2 }),
	scte35Hold: vi.fn().mockResolvedValue({ programSource: 'cam1', seq: 3 }),
	scte35Extend: vi.fn().mockResolvedValue({ programSource: 'cam1', seq: 4 }),
	scte35Cancel: vi.fn().mockResolvedValue({ programSource: 'cam1', seq: 5 }),
	apiCall: vi.fn(),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

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
		cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const },
		cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const },
	},
	seq: 1,
	timestamp: Date.now(),
};

describe('SCTE35Panel', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	// --- Zone 1: Quick Actions ---

	it('renders QUICK ACTIONS zone title', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		expect(container.textContent).toContain('QUICK ACTIONS');
	});

	it('renders CUE BUILDER zone title', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		expect(container.textContent).toContain('CUE BUILDER');
	});

	it('renders EVENT LOG zone title', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		expect(container.textContent).toContain('EVENT LOG');
	});

	it('shows ON AIR status when no active events', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const badge = container.querySelector('.status-badge');
		expect(badge).toBeTruthy();
		expect(badge!.textContent).toBe('ON AIR');
		expect(badge!.classList.contains('status-on-air')).toBe(true);
	});

	it('shows IN BREAK status when active out event exists', () => {
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const badge = container.querySelector('.status-badge');
		expect(badge!.textContent).toBe('IN BREAK');
		expect(badge!.classList.contains('status-break')).toBe(true);
	});

	it('shows HELD status when an event is held', () => {
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const badge = container.querySelector('.status-badge');
		expect(badge!.textContent).toBe('HELD');
		expect(badge!.classList.contains('status-held')).toBe(true);
	});

	it('renders duration preset buttons', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const buttons = container.querySelectorAll('.dur-btn');
		expect(buttons.length).toBe(4);
		const labels = Array.from(buttons).map(b => b.textContent?.trim());
		expect(labels).toContain('30s');
		expect(labels).toContain('60s');
		expect(labels).toContain('90s');
		expect(labels).toContain('120s');
	});

	it('has 30s selected by default', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const buttons = container.querySelectorAll('.dur-btn');
		const active = Array.from(buttons).find(b => b.classList.contains('active'));
		expect(active).toBeTruthy();
		expect(active!.textContent?.trim()).toBe('30s');
	});

	it('renders custom duration input', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const input = container.querySelector('.dur-custom') as HTMLInputElement;
		expect(input).toBeTruthy();
		expect(input.placeholder).toBe('Custom');
	});

	it('renders Auto-return checkbox (checked by default)', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		expect(container.textContent).toContain('Auto-return');
		const checkbox = container.querySelector('input[type="checkbox"]') as HTMLInputElement;
		expect(checkbox).toBeTruthy();
		expect(checkbox.checked).toBe(true);
	});

	it('renders pre-roll dropdown', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		expect(container.textContent).toContain('Pre-roll:');
		const select = container.querySelector('.preroll-dropdown') as HTMLSelectElement;
		expect(select).toBeTruthy();
		expect(select.options.length).toBe(4);
	});

	it('renders AD BREAK button', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const btn = container.querySelector('.ad-break-btn') as HTMLButtonElement;
		expect(btn).toBeTruthy();
		expect(btn.textContent?.trim()).toBe('AD BREAK');
	});

	it('does not show RETURN button when no active out events', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const returnBtn = container.querySelector('.return-btn');
		expect(returnBtn).toBeFalsy();
	});

	it('shows RETURN button when there is an active out event', () => {
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const returnBtn = container.querySelector('.return-btn');
		expect(returnBtn).toBeTruthy();
		expect(returnBtn!.textContent?.trim()).toBe('RETURN');
	});

	it('calls scte35Cue via apiCall when AD BREAK clicked', async () => {
		const { apiCall, scte35Cue } = await import('$lib/api/switch-api');
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const btn = container.querySelector('.ad-break-btn') as HTMLButtonElement;
		await fireEvent.click(btn);
		expect(apiCall).toHaveBeenCalled();
		const callArgs = vi.mocked(apiCall).mock.calls[0];
		expect(callArgs[1]).toBe('SCTE-35 cue');
		// scte35Cue should have been called with a splice_insert request
		expect(scte35Cue).toHaveBeenCalledWith(expect.objectContaining({
			commandType: 'splice_insert',
			isOut: true,
			durationMs: 30000,
			autoReturn: true,
		}));
	});

	it('calls scte35Return via apiCall when RETURN clicked', async () => {
		const { apiCall, scte35Return } = await import('$lib/api/switch-api');
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const btn = container.querySelector('.return-btn') as HTMLButtonElement;
		await fireEvent.click(btn);
		expect(apiCall).toHaveBeenCalled();
		expect(scte35Return).toHaveBeenCalled();
	});

	// --- Active Events ---

	it('renders active events with countdown', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {
					'42': {
						eventId: 42,
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
		const { container } = render(SCTE35Panel, { props: { state } });
		expect(container.textContent).toContain('#42');
		expect(container.textContent).toContain('SPLICE');
	});

	it('renders HOLD button for auto-return events that are not held', () => {
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
						elapsedMs: 0,
						remainingMs: 30000,
						autoReturn: true,
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const holdBtn = container.querySelector('.hold-btn');
		expect(holdBtn).toBeTruthy();
		expect(holdBtn!.textContent?.trim()).toBe('HOLD');
	});

	it('does not render HOLD button when event is held', () => {
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
						elapsedMs: 0,
						remainingMs: 30000,
						autoReturn: true,
						held: true,
						spliceTimePts: 0,
						startedAt: Date.now(),
					},
				},
				eventLog: [],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(SCTE35Panel, { props: { state } });
		const holdBtn = container.querySelector('.hold-btn');
		expect(holdBtn).toBeFalsy();
	});

	it('renders EXTEND button and input for active events', () => {
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
						elapsedMs: 0,
						remainingMs: 30000,
						autoReturn: true,
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const extendBtn = container.querySelector('.extend-btn');
		const extendInput = container.querySelector('.extend-input');
		expect(extendBtn).toBeTruthy();
		expect(extendInput).toBeTruthy();
		expect(extendBtn!.textContent?.trim()).toBe('EXTEND');
	});

	it('renders CANCEL button for active events', () => {
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
						elapsedMs: 0,
						remainingMs: 30000,
						autoReturn: true,
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const cancelBtn = container.querySelector('.cancel-evt-btn');
		expect(cancelBtn).toBeTruthy();
		expect(cancelBtn!.textContent?.trim()).toBe('CANCEL');
	});

	it('shows HELD in countdown when event is held', () => {
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
		const { container } = render(SCTE35Panel, { props: { state } });
		const countdown = container.querySelector('.evt-countdown');
		expect(countdown!.textContent).toBe('HELD');
	});

	it('shows TIME SIG label for time_signal events', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {
					'1': {
						eventId: 1,
						commandType: 'time_signal',
						isOut: true,
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
		const { container } = render(SCTE35Panel, { props: { state } });
		expect(container.textContent).toContain('TIME SIG');
	});

	// --- Zone 2: Advanced Cue Builder ---

	it('renders Splice Insert and Time Signal tabs', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const tabs = container.querySelectorAll('.adv-tab');
		expect(tabs.length).toBe(2);
		expect(tabs[0].textContent?.trim()).toBe('Splice Insert');
		expect(tabs[1].textContent?.trim()).toBe('Time Signal');
	});

	it('defaults to Splice Insert tab active', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const tabs = container.querySelectorAll('.adv-tab');
		expect(tabs[0].classList.contains('active')).toBe(true);
		expect(tabs[1].classList.contains('active')).toBe(false);
	});

	it('shows segmentation fields when Time Signal tab clicked', async () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		// Initially no segmentation dropdown visible
		let segLabel = Array.from(container.querySelectorAll('.field-label')).find(
			el => el.textContent?.includes('Segmentation:')
		);
		expect(segLabel).toBeFalsy();

		// Click Time Signal tab
		const tabs = container.querySelectorAll('.adv-tab');
		await fireEvent.click(tabs[1]);

		// Now segmentation dropdown should be visible
		segLabel = Array.from(container.querySelectorAll('.field-label')).find(
			el => el.textContent?.includes('Segmentation:')
		);
		expect(segLabel).toBeTruthy();
	});

	it('renders SEND CUE button', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const btn = container.querySelector('.send-cue-btn') as HTMLButtonElement;
		expect(btn).toBeTruthy();
		expect(btn.textContent?.trim()).toBe('SEND CUE');
	});

	it('calls scte35Cue via apiCall when SEND CUE clicked (splice_insert)', async () => {
		const { apiCall, scte35Cue } = await import('$lib/api/switch-api');
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const btn = container.querySelector('.send-cue-btn') as HTMLButtonElement;
		await fireEvent.click(btn);
		expect(apiCall).toHaveBeenCalled();
		expect(scte35Cue).toHaveBeenCalledWith(expect.objectContaining({
			commandType: 'splice_insert',
			isOut: true,
		}));
	});

	it('renders Duration and Timing fields in cue builder', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const labels = Array.from(container.querySelectorAll('.field-label')).map(
			el => el.textContent?.trim()
		);
		expect(labels.some(l => l?.startsWith('Duration (s):'))).toBe(true);
		expect(labels.some(l => l?.startsWith('Timing:'))).toBe(true);
	});

	// --- Zone 3: Event Log ---

	it('shows empty state when no events', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		expect(container.textContent).toContain('No events');
	});

	it('shows event count badge', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const logCount = container.querySelector('.log-count');
		expect(logCount).toBeTruthy();
		expect(logCount!.textContent).toBe('0');
	});

	it('renders event log entries', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {},
				eventLog: [
					{
						eventId: 1,
						commandType: 'splice_insert',
						isOut: true,
						durationMs: 30000,
						autoReturn: true,
						timestamp: Date.now() - 60000,
						status: 'completed',
					},
					{
						eventId: 2,
						commandType: 'splice_insert',
						isOut: false,
						autoReturn: false,
						timestamp: Date.now() - 30000,
						status: 'completed',
					},
				],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(SCTE35Panel, { props: { state } });
		expect(container.textContent).toContain('#1');
		expect(container.textContent).toContain('#2');
		const logCount = container.querySelector('.log-count');
		expect(logCount!.textContent).toBe('2');
	});

	it('renders CUE OUT badge for out events in log', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {},
				eventLog: [
					{
						eventId: 1,
						commandType: 'splice_insert',
						isOut: true,
						durationMs: 30000,
						autoReturn: true,
						timestamp: Date.now(),
						status: 'completed',
					},
				],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(SCTE35Panel, { props: { state } });
		const badges = container.querySelectorAll('.log-type-badge');
		expect(badges.length).toBeGreaterThan(0);
		expect(badges[0].textContent).toBe('CUE OUT');
	});

	it('renders RETURN badge for return events in log', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {},
				eventLog: [
					{
						eventId: 1,
						commandType: 'splice_insert',
						isOut: false,
						autoReturn: false,
						timestamp: Date.now(),
						status: 'completed',
					},
				],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(SCTE35Panel, { props: { state } });
		const badges = container.querySelectorAll('.log-type-badge');
		expect(badges[0].textContent).toBe('RETURN');
	});

	it('renders CANCEL badge for cancelled events in log', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {},
				eventLog: [
					{
						eventId: 1,
						commandType: 'splice_insert',
						isOut: true,
						durationMs: 30000,
						autoReturn: true,
						timestamp: Date.now(),
						status: 'cancelled',
					},
				],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(SCTE35Panel, { props: { state } });
		const badges = container.querySelectorAll('.log-type-badge');
		expect(badges[0].textContent).toBe('CANCEL');
	});

	it('shows duration in event log for cue-out events', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {},
				eventLog: [
					{
						eventId: 1,
						commandType: 'splice_insert',
						isOut: true,
						durationMs: 60000,
						autoReturn: true,
						timestamp: Date.now(),
						status: 'completed',
					},
				],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(SCTE35Panel, { props: { state } });
		expect(container.textContent).toContain('60s');
	});

	it('renders TIME SIG badge for time_signal events in log', () => {
		const state = {
			...baseState,
			scte35: {
				enabled: true,
				activeEvents: {},
				eventLog: [
					{
						eventId: 1,
						commandType: 'time_signal',
						isOut: true,
						durationMs: 30000,
						autoReturn: false,
						timestamp: Date.now(),
						status: 'completed',
					},
				],
				heartbeatOk: true,
				config: { heartbeatIntervalMs: 5000, defaultPreRollMs: 2000, pid: 500, verifyEncoding: false },
			},
		};
		const { container } = render(SCTE35Panel, { props: { state } });
		const badges = container.querySelectorAll('.log-type-badge');
		expect(badges[0].textContent).toBe('TIME SIG');
	});

	it('renders three-column grid layout', () => {
		const { container } = render(SCTE35Panel, { props: { state: baseState } });
		const panel = container.querySelector('.scte35-panel');
		expect(panel).toBeTruthy();
		const zones = panel!.querySelectorAll('.zone');
		expect(zones.length).toBe(3);
	});
});
