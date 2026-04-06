import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import IOPanel from './IOPanel.svelte';
import type { ControlRoomState, SourceHealthStatus } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	createSRTSource: vi.fn().mockResolvedValue({ key: 'srt:newcam' }),
	deleteSRTSource: vi.fn().mockResolvedValue(undefined),
	getSRTSourceStats: vi.fn().mockResolvedValue({
		mode: 'caller',
		streamID: 'live/mycam',
		remoteAddr: '10.0.0.5:6464',
		state: 'connected',
		connected: true,
		uptimeMs: 60000,
		latencyMs: 120,
		negotiatedLatencyMs: 120,
		rttMs: 5,
		rttVarMs: 1,
		recvRateMbps: 5.1,
		lossRatePct: 0.01,
		packetsReceived: 100000,
		packetsLost: 10,
		packetsDropped: 2,
		packetsRetransmitted: 8,
		packetsBelated: 0,
		recvBufMs: 80,
		recvBufPackets: 120,
		flightSize: 5,
	}),
	updateSRTLatency: vi.fn().mockResolvedValue(undefined),
	setLabel: vi.fn().mockResolvedValue({}),
	setSourceDelay: vi.fn().mockResolvedValue({}),
	addDestination: vi.fn().mockResolvedValue({ id: 'dest-new' }),
	removeDestination: vi.fn().mockResolvedValue(undefined),
	startDestination: vi.fn().mockResolvedValue({}),
	stopDestination: vi.fn().mockResolvedValue({}),
	startSRTOutput: vi.fn().mockResolvedValue({}),
	stopSRTOutput: vi.fn().mockResolvedValue({}),
	apiCall: vi.fn(),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: {},
		masterLevel: 0,
		programPeak: [0, 0] as [number, number],
		tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', type: 'demo' as const, status: 'healthy' as const, position: 1 },
			cam2: { key: 'cam2', label: 'Camera 2', type: 'demo' as const, status: 'healthy' as const, position: 2 },
			'srt:mycam': {
				key: 'srt:mycam',
				label: 'My SRT Cam',
				type: 'srt' as const,
				status: 'healthy' as const,
				position: 3,
				srt: {
					mode: 'caller' as const,
					streamID: 'live/mycam',
					remoteAddr: '10.0.0.5:6464',
					latencyMs: 120,
					rttMs: 5,
					lossRate: 0.01,
					bitrateKbps: 5100,
					recvBufMs: 80,
					connected: true,
				},
			},
		},
		destinations: [
			{
				id: 'dest-1',
				name: 'YouTube',
				type: 'srt-caller',
				address: 'srt.youtube.com',
				port: 9710,
				state: 'connected',
				bytesWritten: 1500000000,
				droppedPackets: 5,
				connections: 1,
			},
		],
		seq: 1,
		timestamp: Date.now(),
		...overrides,
	} as unknown as ControlRoomState;
}

describe('IOPanel', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('renders nothing visible when not visible', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: false },
		});
		const panel = container.querySelector('.io-panel');
		expect(panel).toBeTruthy();
		expect(panel?.classList.contains('visible')).toBe(false);
	});

	it('renders INPUTS and OUTPUTS headers when visible', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		expect(container.textContent).toContain('INPUTS');
		expect(container.textContent).toContain('OUTPUTS');
	});

	it('shows source count in INPUTS header', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		expect(container.textContent).toContain('INPUTS (3)');
	});

	it('renders all sources as rows with correct type badges', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		const badges = container.querySelectorAll('.type-badge');
		expect(badges.length).toBeGreaterThanOrEqual(3);
		const badgeTexts = Array.from(badges).map((b) => b.textContent?.trim());
		expect(badgeTexts.filter((t) => t === 'Demo')).toHaveLength(2);
		expect(badgeTexts.filter((t) => t === 'SRT')).toHaveLength(1);
	});

	it('shows destination rows', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		expect(container.textContent).toContain('YouTube');
		expect(container.textContent).toContain('1.5 GB');
	});

	it('calls onclose when close button clicked', async () => {
		const onclose = vi.fn();
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true, onclose },
		});
		const closeBtn = container.querySelector('.close-btn');
		expect(closeBtn).toBeTruthy();
		await fireEvent.click(closeBtn!);
		expect(onclose).toHaveBeenCalled();
	});

	it('calls onclose when Escape pressed', async () => {
		const onclose = vi.fn();
		render(IOPanel, {
			props: { state: makeState(), visible: true, onclose },
		});
		await fireEvent.keyDown(document, { key: 'Escape' });
		expect(onclose).toHaveBeenCalled();
	});

	it('does not call onclose on Escape when not visible', async () => {
		const onclose = vi.fn();
		render(IOPanel, {
			props: { state: makeState(), visible: false, onclose },
		});
		await fireEvent.keyDown(document, { key: 'Escape' });
		expect(onclose).not.toHaveBeenCalled();
	});

	it('shows recording status when recording active', () => {
		const state = makeState({
			recording: {
				active: true,
				filename: 'program_20260315_120000_001.ts',
				bytesWritten: 524288000,
				durationSecs: 120,
			},
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		expect(container.textContent).toContain('REC');
		expect(container.textContent).toContain('program_20260315_120000_001.ts');
		expect(container.textContent).toContain('524.3 MB');
	});

	it('shows recording inactive when not recording', () => {
		const state = makeState({
			recording: { active: false },
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		expect(container.textContent).toContain('Recording inactive');
	});

	it('shows Add SRT Source button', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		expect(container.textContent).toContain('Add SRT Source');
	});

	it('shows Add Destination button', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		expect(container.textContent).toContain('Add Destination');
	});

	it('expands source row on click showing details', async () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		const inputRows = container.querySelectorAll('.source-row');
		expect(inputRows.length).toBeGreaterThanOrEqual(1);
		// Click the first source row header
		const rowHeader = inputRows[0].querySelector('.row-header');
		expect(rowHeader).toBeTruthy();
		await fireEvent.click(rowHeader!);
		// Should show expanded detail
		const detail = inputRows[0].querySelector('.row-detail');
		expect(detail).toBeTruthy();
	});

	it('shows SRT-specific stats in expanded SRT row', async () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		// Find the SRT source row
		const sourceRows = container.querySelectorAll('.source-row');
		let srtRow: Element | null = null;
		sourceRows.forEach((row) => {
			if (row.textContent?.includes('My SRT Cam')) {
				srtRow = row;
			}
		});
		expect(srtRow).toBeTruthy();
		// Click to expand
		const rowHeader = srtRow!.querySelector('.row-header');
		await fireEvent.click(rowHeader!);
		// Should show SRT-specific fields
		expect(srtRow!.textContent).toContain('Mode');
		expect(srtRow!.textContent).toContain('caller');
		expect(srtRow!.textContent).toContain('Latency');
	});

	it('sorts sources by position then key', () => {
		const state = makeState({
			sources: {
				zzz: { key: 'zzz', type: 'demo' as const, status: 'healthy' as const, position: 2 },
				aaa: { key: 'aaa', type: 'demo' as const, status: 'healthy' as const, position: 1 },
				mmm: { key: 'mmm', type: 'demo' as const, status: 'healthy' as const },
			},
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		const rows = container.querySelectorAll('.source-row .row-label');
		const labels = Array.from(rows).map((r) => r.textContent?.trim());
		// position 1 first, then position 2, then undefined position
		expect(labels[0]).toBe('aaa');
		expect(labels[1]).toBe('zzz');
		expect(labels[2]).toBe('mmm');
	});

	it('shows status dots with correct classes', () => {
		const state = makeState({
			sources: {
				a: { key: 'a', type: 'demo' as const, status: 'healthy' as const, position: 1 },
				b: { key: 'b', type: 'demo' as const, status: 'stale' as const, position: 2 },
				c: { key: 'c', type: 'demo' as const, status: 'offline' as const, position: 3 },
			},
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		const dots = container.querySelectorAll('.status-dot');
		expect(dots.length).toBeGreaterThanOrEqual(3);
		const classes = Array.from(dots).map((d) => d.className);
		expect(classes.some((c) => c.includes('healthy'))).toBe(true);
		expect(classes.some((c) => c.includes('stale'))).toBe(true);
		expect(classes.some((c) => c.includes('offline'))).toBe(true);
	});

	it('shows legacy SRT output when active', () => {
		const state = makeState({
			srtOutput: {
				active: true,
				mode: 'caller',
				address: 'srt.example.com',
				port: 9710,
				state: 'connected',
				bytesWritten: 2000000,
			},
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		expect(container.textContent).toContain('Legacy SRT');
		expect(container.textContent).toContain('srt.example.com');
	});

	it('collapses sections when header clicked', async () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		const sectionHeaders = container.querySelectorAll('.section-header');
		const inputsHeader = Array.from(sectionHeaders).find((h) =>
			h.textContent?.includes('INPUTS')
		);
		expect(inputsHeader).toBeTruthy();
		await fireEvent.click(inputsHeader!);
		// After collapse, source rows should not be visible
		const sourceRows = container.querySelectorAll('.source-row');
		expect(sourceRows.length).toBe(0);
	});

	it('shows SRT bitrate in source detail', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		expect(container.textContent).toContain('5.1 Mbps');
	});

	it('shows RTMP destination with correct badge and hostname', () => {
		const state = makeState({
			destinations: [
				{
					id: 'rtmp-1',
					name: '',
					type: 'rtmp',
					url: 'rtmp://a.rtmp.youtube.com/live2/xxxx-xxxx',
					state: 'connected',
					bytesWritten: 500000000,
				},
			],
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		expect(container.textContent).toContain('RTMP');
		expect(container.textContent).toContain('a.rtmp.youtube.com');
		expect(container.textContent).toContain('500.0 MB');
	});

	it('shows RTMP destination name when provided', () => {
		const state = makeState({
			destinations: [
				{
					id: 'rtmp-2',
					name: 'YouTube Live',
					type: 'rtmp',
					url: 'rtmp://a.rtmp.youtube.com/live2/xxxx-xxxx',
					state: 'active',
					bytesWritten: 0,
				},
			],
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		expect(container.textContent).toContain('YouTube Live');
	});

	it('masks RTMP stream key in expanded detail panel with reveal toggle', async () => {
		const state = makeState({
			destinations: [
				{
					id: 'rtmp-3',
					type: 'rtmp',
					url: 'rtmp://live.twitch.tv/app/live_12345',
					state: 'connected',
					bytesWritten: 100000,
				},
			],
		});
		const { container } = render(IOPanel, {
			props: { state, visible: true },
		});
		const destRow = container.querySelector('.dest-row');
		expect(destRow).toBeTruthy();
		const rowHeader = destRow!.querySelector('.row-header');
		await fireEvent.click(rowHeader!);
		expect(destRow!.textContent).toContain('RTMP (push)');
		// Stream key should be masked by default
		expect(destRow!.textContent).toContain('rtmp://live.twitch.tv/app/****');
		expect(destRow!.textContent).not.toContain('live_12345');
		// Click "Show" to reveal
		const showBtn = destRow!.querySelector('.reveal-btn');
		expect(showBtn).toBeTruthy();
		expect(showBtn!.textContent).toContain('Show');
		await fireEvent.click(showBtn!);
		expect(destRow!.textContent).toContain('rtmp://live.twitch.tv/app/live_12345');
		expect(showBtn!.textContent).toContain('Hide');
	});

	it('shows RTMP connection guide', () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		expect(container.textContent).toContain('RTMP Output');
		expect(container.textContent).toContain('rtmp://a.rtmp.youtube.com/live2/YOUR-STREAM-KEY');
	});

	it('shows RTMP option in destination type dropdown', async () => {
		const { container } = render(IOPanel, {
			props: { state: makeState(), visible: true },
		});
		const addBtn = container.querySelector('.add-btn');
		const addDestBtn = Array.from(container.querySelectorAll('.add-btn')).find(
			(b) => b.textContent?.includes('Add Destination')
		);
		expect(addDestBtn).toBeTruthy();
		await fireEvent.click(addDestBtn!);
		const select = container.querySelector('.add-form select');
		expect(select).toBeTruthy();
		const options = select!.querySelectorAll('option');
		const optionValues = Array.from(options).map((o) => o.value);
		expect(optionValues).toContain('rtmp');
	});
});

describe('IOPanel dual-engine features', () => {
	it('shows dual SRT endpoints info blurb when isDualEngine is true', () => {
		const { container } = render(IOPanel, {
			props: {
				state: makeState(),
				visible: true,
				isDualEngine: true,
				engineEndpoints: { A: 'srt://10.0.1.1:6464', B: 'srt://10.0.2.1:6464' },
			},
		});
		expect(container.textContent).toContain('Sources must push to both engines');
		expect(container.textContent).toContain('Engine A:');
		expect(container.textContent).toContain('srt://10.0.1.1:6464');
		expect(container.textContent).toContain('Engine B:');
		expect(container.textContent).toContain('srt://10.0.2.1:6464');
	});

	it('does not show dual SRT endpoints when not in dual-engine mode', () => {
		const { container } = render(IOPanel, {
			props: {
				state: makeState(),
				visible: true,
				isDualEngine: false,
				engineEndpoints: { A: 'srt://10.0.1.1:6464', B: 'srt://10.0.2.1:6464' },
			},
		});
		expect(container.textContent).not.toContain('Sources must push to both engines');
	});

	it('does not show dual SRT endpoints when engineEndpoints is null', () => {
		const { container } = render(IOPanel, {
			props: {
				state: makeState(),
				visible: true,
				isDualEngine: true,
				engineEndpoints: null,
			},
		});
		expect(container.textContent).not.toContain('Sources must push to both engines');
	});

	it('shows dual health dots on sources with differing health', () => {
		const engineSourceHealth: Record<string, { A: SourceHealthStatus; B: SourceHealthStatus }> = {
			cam1: { A: 'healthy', B: 'stale' },
			cam2: { A: 'healthy', B: 'healthy' },
		};
		const { container } = render(IOPanel, {
			props: {
				state: makeState(),
				visible: true,
				isDualEngine: true,
				engineSourceHealth,
			},
		});
		// Find cam1 row — should have dual health dots (engines disagree)
		const sourceRows = container.querySelectorAll('.source-row');
		let cam1Row: Element | null = null;
		let cam2Row: Element | null = null;
		sourceRows.forEach((row) => {
			if (row.textContent?.includes('Camera 1')) cam1Row = row;
			if (row.textContent?.includes('Camera 2')) cam2Row = row;
		});
		expect(cam1Row).toBeTruthy();
		expect(cam2Row).toBeTruthy();
		// cam1 disagrees — should show engine-health-dots
		const cam1Dots = cam1Row!.querySelector('.engine-health-dots');
		expect(cam1Dots).toBeTruthy();
		expect(cam1Dots!.textContent).toContain('A');
		expect(cam1Dots!.textContent).toContain('B');
		// cam2 agrees — should NOT show engine-health-dots
		const cam2Dots = cam2Row!.querySelector('.engine-health-dots');
		expect(cam2Dots).toBeNull();
	});

	it('does not show dual health dots when engineSourceHealth is null', () => {
		const { container } = render(IOPanel, {
			props: {
				state: makeState(),
				visible: true,
				isDualEngine: true,
				engineSourceHealth: null,
			},
		});
		const dots = container.querySelectorAll('.engine-health-dots');
		expect(dots.length).toBe(0);
	});

	it('shows per-engine output status on destinations', () => {
		const engineDestinations: Record<string, { A: { state: string; bitrate?: number }; B: { state: string; bitrate?: number } }> = {
			'dest-1': { A: { state: 'connected', bitrate: 5000 }, B: { state: 'reconnecting', bitrate: 0 } },
		};
		const { container } = render(IOPanel, {
			props: {
				state: makeState(),
				visible: true,
				isDualEngine: true,
				engineDestinations,
			},
		});
		// Find dest row
		const destRows = container.querySelectorAll('.dest-row');
		expect(destRows.length).toBeGreaterThanOrEqual(1);
		const ytRow = Array.from(destRows).find((r) => r.textContent?.includes('YouTube'));
		expect(ytRow).toBeTruthy();
		// Should show dual engine status
		const engineStatus = ytRow!.querySelector('.dest-engine-status');
		expect(engineStatus).toBeTruthy();
		expect(engineStatus!.textContent).toContain('A');
		expect(engineStatus!.textContent).toContain('B');
		expect(engineStatus!.textContent).toContain('5.0 Mbps');
	});

	it('does not show per-engine output status when engineDestinations is null', () => {
		const { container } = render(IOPanel, {
			props: {
				state: makeState(),
				visible: true,
				isDualEngine: true,
				engineDestinations: null,
			},
		});
		const engineStatus = container.querySelectorAll('.dest-engine-status');
		expect(engineStatus.length).toBe(0);
	});
});
