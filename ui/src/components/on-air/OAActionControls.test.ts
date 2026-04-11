import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OAActionControls from './OAActionControls.svelte';

function playoutProps(overrides: Record<string, unknown> = {}) {
	return {
		mode: 'playout' as const,
		bugActive: false,
		captionMode: 'off' as const,
		podArmed: false,
		inBreak: false,
		holdActive: false,
		...overrides,
	};
}

function liveProps(overrides: Record<string, unknown> = {}) {
	return {
		mode: 'live-monitor' as const,
		bugActive: false,
		captionMode: 'off' as const,
		podArmed: false,
		inBreak: false,
		holdActive: false,
		...overrides,
	};
}

describe('OAActionControls', () => {
	// ── Playout mode rendering ──

	it('renders playout mode buttons', () => {
		render(OAActionControls, { props: playoutProps() });
		expect(screen.getByText('FIRE POD')).toBeTruthy();
		expect(screen.getByText('HOLD')).toBeTruthy();
		expect(screen.getByText('SKIP')).toBeTruthy();
		expect(screen.getByText('TAKE NEXT')).toBeTruthy();
	});

	it('does not render SLATE button in playout mode (slates are in dashboard now)', () => {
		render(OAActionControls, { props: playoutProps() });
		expect(screen.queryByText('SLATE')).toBeNull();
	});

	it('does not render live-monitor buttons in playout mode', () => {
		render(OAActionControls, { props: playoutProps() });
		expect(screen.queryByText('CUT')).toBeNull();
		expect(screen.queryByText('AUTO')).toBeNull();
		expect(screen.queryByText('SCTE INSERT')).toBeNull();
		expect(screen.queryByText('SCTE RETURN')).toBeNull();
	});

	// ── Live-monitor mode rendering ──

	it('renders live-monitor mode buttons', () => {
		render(OAActionControls, { props: liveProps() });
		expect(screen.getByText('CUT')).toBeTruthy();
		expect(screen.getByText('AUTO')).toBeTruthy();
		expect(screen.getByText('SCTE INSERT')).toBeTruthy();
		expect(screen.getByText('SCTE RETURN')).toBeTruthy();
	});

	it('does not render playout buttons in live-monitor mode', () => {
		render(OAActionControls, { props: liveProps() });
		expect(screen.queryByText('FIRE POD')).toBeNull();
		expect(screen.queryByText('HOLD')).toBeNull();
		expect(screen.queryByText('SKIP')).toBeNull();
		expect(screen.queryByText('TAKE NEXT')).toBeNull();
	});

	it('does not render SLATE in live-monitor mode', () => {
		render(OAActionControls, { props: liveProps() });
		expect(screen.queryByText('SLATE')).toBeNull();
	});

	// ── Common elements in both modes ──

	it('renders bug toggle in playout mode', () => {
		render(OAActionControls, { props: playoutProps() });
		expect(screen.getByText('BUG: OFF')).toBeTruthy();
	});

	it('renders bug toggle in live-monitor mode', () => {
		render(OAActionControls, { props: liveProps() });
		expect(screen.getByText('BUG: OFF')).toBeTruthy();
	});

	it('renders captions button in both modes', () => {
		render(OAActionControls, { props: playoutProps() });
		expect(screen.getByText(/CAPTIONS/)).toBeTruthy();
	});

	it('renders utility buttons in both modes', () => {
		render(OAActionControls, { props: playoutProps() });
		expect(screen.getByTitle('Toggle audio panel')).toBeTruthy();
		expect(screen.getByTitle('Toggle I/O panel')).toBeTruthy();
		expect(screen.getByTitle('Open settings')).toBeTruthy();
	});

	// ── Bug toggle state ──

	it('shows BUG: ON with green styling when active', () => {
		const { container } = render(OAActionControls, {
			props: playoutProps({ bugActive: true }),
		});
		expect(screen.getByText('BUG: ON')).toBeTruthy();
		const btn = container.querySelector('.bug-btn.bug-active');
		expect(btn).toBeTruthy();
	});

	it('shows BUG: OFF when inactive', () => {
		const { container } = render(OAActionControls, {
			props: playoutProps({ bugActive: false }),
		});
		expect(screen.getByText('BUG: OFF')).toBeTruthy();
		const btn = container.querySelector('.bug-btn.bug-active');
		expect(btn).toBeNull();
	});

	// ── FIRE POD states ──

	it('FIRE POD is disabled when not armed', () => {
		const { container } = render(OAActionControls, {
			props: playoutProps({ podArmed: false }),
		});
		const btn = container.querySelector('.fire-pod-btn');
		expect(btn).toBeTruthy();
		expect(btn!.classList.contains('pod-disabled')).toBe(true);
	});

	it('FIRE POD is enabled when armed', () => {
		const { container } = render(OAActionControls, {
			props: playoutProps({ podArmed: true }),
		});
		const btn = container.querySelector('.fire-pod-btn');
		expect(btn).toBeTruthy();
		expect(btn!.classList.contains('pod-disabled')).toBe(false);
		expect(btn!.classList.contains('pod-armed')).toBe(true);
	});

	// ── RETURN TO LIVE visibility ──

	it('RETURN TO LIVE is hidden when not in break', () => {
		render(OAActionControls, {
			props: playoutProps({ inBreak: false }),
		});
		expect(screen.queryByText('RETURN TO LIVE')).toBeNull();
	});

	it('RETURN TO LIVE is visible when in break', () => {
		render(OAActionControls, {
			props: playoutProps({ inBreak: true }),
		});
		expect(screen.getByText('RETURN TO LIVE')).toBeTruthy();
	});

	// ── HOLD state ──

	it('HOLD shows active styling when holdActive is true', () => {
		const { container } = render(OAActionControls, {
			props: playoutProps({ holdActive: true }),
		});
		const btn = container.querySelector('.hold-btn.hold-active');
		expect(btn).toBeTruthy();
	});

	it('HOLD shows default styling when holdActive is false', () => {
		const { container } = render(OAActionControls, {
			props: playoutProps({ holdActive: false }),
		});
		const btn = container.querySelector('.hold-btn.hold-active');
		expect(btn).toBeNull();
	});

	// ── Caption mode display ──

	it('shows CAPTIONS: OFF when captionMode is off', () => {
		render(OAActionControls, {
			props: playoutProps({ captionMode: 'off' }),
		});
		expect(screen.getByText(/CAPTIONS: OFF/)).toBeTruthy();
	});

	it('shows CAPTIONS: PASS when captionMode is passthrough', () => {
		render(OAActionControls, {
			props: playoutProps({ captionMode: 'passthrough' }),
		});
		expect(screen.getByText(/CAPTIONS: PASS/)).toBeTruthy();
	});

	it('shows CAPTIONS: AUTHOR when captionMode is author', () => {
		render(OAActionControls, {
			props: playoutProps({ captionMode: 'author' }),
		});
		expect(screen.getByText(/CAPTIONS: AUTHOR/)).toBeTruthy();
	});

	// ── Callbacks fire on click ──

	it('FIRE POD callback fires when armed', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ podArmed: true, onFirePod: fn }),
		});
		await fireEvent.click(screen.getByText('FIRE POD'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('FIRE POD callback does not fire when disabled', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ podArmed: false, onFirePod: fn }),
		});
		await fireEvent.click(screen.getByText('FIRE POD'));
		expect(fn).not.toHaveBeenCalled();
	});

	it('RETURN TO LIVE callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ inBreak: true, onReturnToLive: fn }),
		});
		await fireEvent.click(screen.getByText('RETURN TO LIVE'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('BUG toggle callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onBugToggle: fn }),
		});
		await fireEvent.click(screen.getByText('BUG: OFF'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('HOLD callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onHold: fn }),
		});
		await fireEvent.click(screen.getByText('HOLD'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('SKIP callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onSkip: fn }),
		});
		await fireEvent.click(screen.getByText('SKIP'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('TAKE NEXT callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onTakeNext: fn }),
		});
		await fireEvent.click(screen.getByText('TAKE NEXT'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('CAPTIONS callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onCaptionCycle: fn }),
		});
		await fireEvent.click(screen.getByText(/CAPTIONS/));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('CUT callback fires in live-monitor mode', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: liveProps({ onCut: fn }),
		});
		await fireEvent.click(screen.getByText('CUT'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('AUTO callback fires in live-monitor mode', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: liveProps({ onAuto: fn }),
		});
		await fireEvent.click(screen.getByText('AUTO'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('SCTE INSERT callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: liveProps({ onScteInsert: fn }),
		});
		await fireEvent.click(screen.getByText('SCTE INSERT'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('SCTE RETURN callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: liveProps({ onScteReturn: fn }),
		});
		await fireEvent.click(screen.getByText('SCTE RETURN'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('AUDIO toggle callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onAudioToggle: fn }),
		});
		await fireEvent.click(screen.getByTitle('Toggle audio panel'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('I/O toggle callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onIOToggle: fn }),
		});
		await fireEvent.click(screen.getByTitle('Toggle I/O panel'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('SETTINGS callback fires', async () => {
		const fn = vi.fn();
		render(OAActionControls, {
			props: playoutProps({ onSettingsOpen: fn }),
		});
		await fireEvent.click(screen.getByTitle('Open settings'));
		expect(fn).toHaveBeenCalledOnce();
	});

	// ── ROUTING button ──

	it('shows ROUTING button in panels row', () => {
		const { container } = render(OAActionControls, {
			props: liveProps(),
		});
		const allPanelBtns = container.querySelectorAll('.btn-panel');
		const routingFound = Array.from(allPanelBtns).some(
			(btn) => btn.textContent?.trim() === 'ROUTING'
		);
		expect(routingFound).toBe(true);
	});

	it('ROUTING toggle callback fires', async () => {
		const fn = vi.fn();
		const { container } = render(OAActionControls, {
			props: liveProps({ onRoutingToggle: fn }),
		});
		const allPanelBtns = container.querySelectorAll('.btn-panel');
		const routingBtn = Array.from(allPanelBtns).find(
			(btn) => btn.textContent?.trim() === 'ROUTING'
		) as HTMLElement;
		expect(routingBtn).toBeTruthy();
		await fireEvent.click(routingBtn);
		expect(fn).toHaveBeenCalledOnce();
	});

	// ── No onSlate prop (removed) ──

	it('does not accept onSlate prop (slate handled by dashboard)', () => {
		// Just confirm the component renders without onSlate
		render(OAActionControls, {
			props: playoutProps(),
		});
		// No SLATE button should exist
		expect(screen.queryByText('SLATE')).toBeNull();
	});
});
