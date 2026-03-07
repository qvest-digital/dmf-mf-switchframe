import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SimpleMode from './SimpleMode.svelte';
import type { ControlRoomState } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	setPreview: vi.fn(() => Promise.resolve({})),
	cut: vi.fn(() => Promise.resolve({})),
	startTransition: vi.fn(() => Promise.resolve({})),
	fadeToBlack: vi.fn(() => Promise.resolve({})),
	apiCall: vi.fn((p: Promise<unknown>) => p.catch(() => {})),
}));

function makeState(overrides: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 1000,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: undefined,
		masterLevel: 0,
		programPeak: [0, 0],
		tallyState: { cam1: 'program', cam2: 'preview', cam3: 'idle' },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' },
			cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' },
			cam3: { key: 'cam3', label: 'Camera 3', status: 'healthy' },
		},
		seq: 1,
		timestamp: Date.now(),
		...overrides,
	};
}

describe('SimpleMode', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('renders source buttons for each source', () => {
		render(SimpleMode, { props: { state: makeState() } });
		expect(screen.getAllByText(/Camera 1/).length).toBeGreaterThanOrEqual(1);
		expect(screen.getAllByText(/Camera 2/).length).toBeGreaterThanOrEqual(1);
		expect(screen.getAllByText(/Camera 3/).length).toBeGreaterThanOrEqual(1);
	});

	it('renders CUT and DISSOLVE buttons', () => {
		render(SimpleMode, { props: { state: makeState() } });
		expect(screen.getByText('CUT')).toBeTruthy();
		expect(screen.getByText('DISSOLVE')).toBeTruthy();
	});

	it('CUT button calls cut API', async () => {
		const { cut } = await import('$lib/api/switch-api');
		render(SimpleMode, { props: { state: makeState() } });
		await fireEvent.click(screen.getByText('CUT'));
		expect(cut).toHaveBeenCalledWith('cam2');
	});

	it('DISSOLVE button calls startTransition API', async () => {
		const { startTransition } = await import('$lib/api/switch-api');
		render(SimpleMode, { props: { state: makeState() } });
		await fireEvent.click(screen.getByText('DISSOLVE'));
		expect(startTransition).toHaveBeenCalledWith('cam2', 'mix', 1000);
	});

	it('CUT button disabled when no preview source', () => {
		render(SimpleMode, { props: { state: makeState({ previewSource: '' }) } });
		const btn = screen.getByText('CUT');
		expect(btn.hasAttribute('disabled')).toBe(true);
	});

	it('CUT button disabled during transition', () => {
		render(SimpleMode, { props: { state: makeState({ inTransition: true }) } });
		const btn = screen.getByText('CUT');
		expect(btn.hasAttribute('disabled')).toBe(true);
	});

	it('DISSOLVE button disabled when no preview source', () => {
		render(SimpleMode, { props: { state: makeState({ previewSource: '' }) } });
		const btn = screen.getByText('DISSOLVE');
		expect(btn.hasAttribute('disabled')).toBe(true);
	});

	it('DISSOLVE button disabled during transition', () => {
		render(SimpleMode, { props: { state: makeState({ inTransition: true }) } });
		const btn = screen.getByText('DISSOLVE');
		expect(btn.hasAttribute('disabled')).toBe(true);
	});

	it('DISSOLVE button disabled during FTB', () => {
		render(SimpleMode, { props: { state: makeState({ ftbActive: true }) } });
		const btn = screen.getByText('DISSOLVE');
		expect(btn.hasAttribute('disabled')).toBe(true);
	});

	it('source button click calls setPreview', async () => {
		const { setPreview } = await import('$lib/api/switch-api');
		render(SimpleMode, { props: { state: makeState() } });
		// Camera 3 only appears in the source button (not in preview/program labels)
		const cam3btns = screen.getAllByText(/Camera 3/);
		const cam3btn = cam3btns.find((el) => el.closest('.source-btn'));
		await fireEvent.click(cam3btn!);
		expect(setPreview).toHaveBeenCalledWith('cam3');
	});

	it('applies tally-program class to program source button', () => {
		render(SimpleMode, { props: { state: makeState() } });
		const buttons = screen.getAllByText(/Camera 1/);
		const cam1btn = buttons.find((el) => el.closest('.source-btn'))?.closest('button');
		expect(cam1btn?.classList.contains('tally-program')).toBe(true);
	});

	it('applies tally-preview class to preview source button', () => {
		render(SimpleMode, { props: { state: makeState() } });
		const buttons = screen.getAllByText(/Camera 2/);
		const cam2btn = buttons.find((el) => el.closest('.source-btn'))?.closest('button');
		expect(cam2btn?.classList.contains('tally-preview')).toBe(true);
	});

	it('gear icon fires layout switch callback', async () => {
		const switchFn = vi.fn();
		render(SimpleMode, { props: { state: makeState(), onSwitchLayout: switchFn } });
		const gearBtn = screen.getByTitle('Switch to traditional mode');
		await fireEvent.click(gearBtn);
		expect(switchFn).toHaveBeenCalled();
	});

	it('displays SwitchFrame brand', () => {
		render(SimpleMode, { props: { state: makeState() } });
		expect(screen.getByText('SwitchFrame')).toBeTruthy();
	});

	it('renders a FADE TO BLACK button', () => {
		render(SimpleMode, { props: { state: makeState() } });
		expect(screen.getByText('FADE TO BLACK')).toBeTruthy();
	});

	it('FADE TO BLACK button calls onFTB callback when clicked', async () => {
		const ftbFn = vi.fn();
		render(SimpleMode, { props: { state: makeState(), onFTB: ftbFn } });
		await fireEvent.click(screen.getByText('FADE TO BLACK'));
		expect(ftbFn).toHaveBeenCalled();
	});

	it('FADE TO BLACK button calls fadeToBlack API when no callback provided', async () => {
		const { fadeToBlack } = await import('$lib/api/switch-api');
		render(SimpleMode, { props: { state: makeState() } });
		await fireEvent.click(screen.getByText('FADE TO BLACK'));
		expect(fadeToBlack).toHaveBeenCalled();
	});

	it('FADE TO BLACK button shows ftb-active class when ftbActive is true', () => {
		render(SimpleMode, { props: { state: makeState({ ftbActive: true }) } });
		const btn = screen.getByText('FADE TO BLACK');
		expect(btn.classList.contains('ftb-active')).toBe(true);
	});

	// --- Source health indicators ---

	it('source button gets class source-stale when source status is stale', () => {
		const state = makeState({
			sources: {
				cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' },
				cam2: { key: 'cam2', label: 'Camera 2', status: 'stale' },
				cam3: { key: 'cam3', label: 'Camera 3', status: 'healthy' },
			},
		});
		render(SimpleMode, { props: { state } });
		const buttons = screen.getAllByText(/Camera 2/);
		const cam2btn = buttons.find((el) => el.closest('.source-btn'))?.closest('button');
		expect(cam2btn?.classList.contains('source-stale')).toBe(true);
	});

	it('source button gets class source-stale when source status is no_signal', () => {
		const state = makeState({
			sources: {
				cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' },
				cam2: { key: 'cam2', label: 'Camera 2', status: 'no_signal' },
				cam3: { key: 'cam3', label: 'Camera 3', status: 'healthy' },
			},
		});
		render(SimpleMode, { props: { state } });
		const buttons = screen.getAllByText(/Camera 2/);
		const cam2btn = buttons.find((el) => el.closest('.source-btn'))?.closest('button');
		expect(cam2btn?.classList.contains('source-stale')).toBe(true);
	});

	it('source button is disabled and shows OFFLINE text when status is offline', () => {
		const state = makeState({
			sources: {
				cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' },
				cam2: { key: 'cam2', label: 'Camera 2', status: 'offline' },
				cam3: { key: 'cam3', label: 'Camera 3', status: 'healthy' },
			},
		});
		render(SimpleMode, { props: { state } });
		// The button should have the source-offline class
		const offlineOverlays = screen.getAllByText('OFFLINE');
		expect(offlineOverlays.length).toBeGreaterThanOrEqual(1);
		// The overlay should be inside a source-btn
		const offlineBtn = offlineOverlays.find((el) => el.closest('.source-btn'))?.closest('button');
		expect(offlineBtn).toBeTruthy();
		expect(offlineBtn?.classList.contains('source-offline')).toBe(true);
		expect(offlineBtn?.hasAttribute('disabled')).toBe(true);
	});

	it('source button shows warning indicator when stale', () => {
		const state = makeState({
			sources: {
				cam1: { key: 'cam1', label: 'Camera 1', status: 'stale' },
				cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' },
				cam3: { key: 'cam3', label: 'Camera 3', status: 'healthy' },
			},
		});
		render(SimpleMode, { props: { state } });
		const warnings = screen.getAllByText('!');
		const cam1warning = warnings.find((el) => el.closest('.source-btn'));
		expect(cam1warning).toBeTruthy();
		expect(cam1warning?.classList.contains('health-warning')).toBe(true);
	});

	it('healthy source button does not get stale or offline classes', () => {
		render(SimpleMode, { props: { state: makeState() } });
		const buttons = screen.getAllByText(/Camera 1/);
		const cam1btn = buttons.find((el) => el.closest('.source-btn'))?.closest('button');
		expect(cam1btn?.classList.contains('source-stale')).toBe(false);
		expect(cam1btn?.classList.contains('source-offline')).toBe(false);
	});
});
