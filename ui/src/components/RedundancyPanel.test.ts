import { describe, it, expect, vi } from 'vitest';
import { tick } from 'svelte';
import { render, fireEvent } from '@testing-library/svelte';
import RedundancyPanel from './RedundancyPanel.svelte';
import type { SyncHealth, SyncStatus, EngineHealthDetail } from '$lib/state/reconciler.svelte';
import type { DivergenceLogEntry } from '$lib/state/divergence-log.svelte';

function makeSyncHealth(overrides: Partial<SyncHealth> & { status?: SyncStatus } = {}): SyncHealth {
	return {
		status: 'synced',
		divergences: [],
		authoritative: 'A',
		engineA: { lastSeen: Date.now(), lastCommandSeq: 42, connected: true },
		engineB: { lastSeen: Date.now(), lastCommandSeq: 42, connected: true },
		...overrides,
	};
}

function makeEngineHealth(overrides: Partial<EngineHealthDetail> = {}): EngineHealthDetail {
	return {
		connected: true,
		rtt: 12,
		clockOffset: 150,
		lastSeq: 42,
		uptime: 3_600_000, // 1 hour
		lastSeen: Date.now(),
		sources: {},
		...overrides,
	};
}

function makeDefaultProps(overrides: Record<string, unknown> = {}) {
	return {
		health: makeSyncHealth(),
		engineHealth: { A: makeEngineHealth(), B: makeEngineHealth() },
		divergenceLog: [] as DivergenceLogEntry[],
		visible: true,
		onclose: vi.fn(),
		...overrides,
	};
}

describe('RedundancyPanel', () => {
	it('renders when visible', () => {
		const { container } = render(RedundancyPanel, { props: makeDefaultProps() });
		const panel = container.querySelector('.redundancy-panel');
		expect(panel).toBeTruthy();
		expect(panel?.classList.contains('visible')).toBe(true);
	});

	it('is hidden when not visible', () => {
		const { container } = render(RedundancyPanel, { props: makeDefaultProps({ visible: false }) });
		const panel = container.querySelector('.redundancy-panel');
		expect(panel).toBeTruthy();
		expect(panel?.classList.contains('visible')).toBe(false);
	});

	it('shows engine names', () => {
		const { container } = render(RedundancyPanel, { props: makeDefaultProps() });
		const text = container.textContent ?? '';
		expect(text).toContain('Engine A');
		expect(text).toContain('Engine B');
	});

	it('shows authoritative star on correct engine', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ authoritative: 'B' }),
			}),
		});
		const engineRows = container.querySelectorAll('.engine-card');
		expect(engineRows.length).toBe(2);
		// Engine A (index 0) should not have star
		expect(engineRows[0].querySelector('.auth-star')).toBeNull();
		// Engine B (index 1) should have star
		expect(engineRows[1].querySelector('.auth-star')).toBeTruthy();
	});

	it('renders divergence log entries', () => {
		const now = Date.now();
		const log: DivergenceLogEntry[] = [
			{ timestamp: now - 1000, type: 'diverged', message: 'programSource mismatch' },
			{ timestamp: now - 5000, type: 'resolved', message: 'State reconciled' },
		];
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({ divergenceLog: log }),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('programSource mismatch');
		expect(text).toContain('State reconciled');
	});

	it('force reconcile buttons disabled when synced', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'synced' }),
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		expect(buttons.length).toBe(2);
		for (const btn of buttons) {
			expect((btn as HTMLButtonElement).disabled).toBe(true);
		}
	});

	it('force reconcile buttons enabled when diverged', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'diverged' }),
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		expect(buttons.length).toBe(2);
		for (const btn of buttons) {
			expect((btn as HTMLButtonElement).disabled).toBe(false);
		}
	});

	it('close button calls onclose', async () => {
		const onclose = vi.fn();
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({ onclose }),
		});
		const closeBtn = container.querySelector('.close-btn');
		expect(closeBtn).toBeTruthy();
		await fireEvent.click(closeBtn!);
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('force A->B shows confirmation then calls onforcereconcile with AtoB', async () => {
		const onforcereconcile = vi.fn();
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'diverged' }),
				onforcereconcile,
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		// First button is A->B — should show confirmation, not call directly
		await fireEvent.click(buttons[0]);
		expect(onforcereconcile).not.toHaveBeenCalled();

		// Confirmation message should be visible
		const text = container.textContent ?? '';
		expect(text).toContain("Engine A's complete state to Engine B");

		// Click Confirm
		const confirmBtn = container.querySelector('.confirm-btn');
		expect(confirmBtn).toBeTruthy();
		await fireEvent.click(confirmBtn!);
		expect(onforcereconcile).toHaveBeenCalledWith('AtoB');
	});

	it('force B->A shows confirmation then calls onforcereconcile with BtoA', async () => {
		const onforcereconcile = vi.fn();
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'diverged' }),
				onforcereconcile,
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		// Second button is B->A
		await fireEvent.click(buttons[1]);
		expect(onforcereconcile).not.toHaveBeenCalled();

		const text = container.textContent ?? '';
		expect(text).toContain("Engine B's complete state to Engine A");

		const confirmBtn = container.querySelector('.confirm-btn');
		await fireEvent.click(confirmBtn!);
		expect(onforcereconcile).toHaveBeenCalledWith('BtoA');
	});

	it('cancel hides reconcile confirmation', async () => {
		const onforcereconcile = vi.fn();
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'diverged' }),
				onforcereconcile,
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		await fireEvent.click(buttons[0]);

		// Confirmation should be visible
		expect(container.querySelector('.reconcile-confirm')).toBeTruthy();

		// Click Cancel
		const cancelBtn = container.querySelector('.confirm-cancel-btn');
		expect(cancelBtn).toBeTruthy();
		await fireEvent.click(cancelBtn!);

		// Confirmation should be gone, buttons should be back
		expect(container.querySelector('.reconcile-confirm')).toBeNull();
		expect(container.querySelectorAll('.reconcile-btn').length).toBe(2);
		expect(onforcereconcile).not.toHaveBeenCalled();
	});

	it('shows connected/disconnected status for engines', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				engineHealth: {
					A: makeEngineHealth({ connected: true }),
					B: makeEngineHealth({ connected: false }),
				},
			}),
		});
		const dots = container.querySelectorAll('.conn-dot');
		expect(dots.length).toBe(2);
		expect(dots[0].classList.contains('connected')).toBe(true);
		expect(dots[1].classList.contains('connected')).toBe(false);
	});

	it('shows RTT values', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				engineHealth: {
					A: makeEngineHealth({ rtt: 15 }),
					B: makeEngineHealth({ rtt: 23 }),
				},
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('15ms');
		expect(text).toContain('23ms');
	});

	it('shows uptime formatted', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				engineHealth: {
					A: makeEngineHealth({ uptime: 7_260_000 }), // 2h 1m
					B: makeEngineHealth({ uptime: 300_000 }), // 5m
				},
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('2h 1m');
		expect(text).toContain('0h 5m');
	});

	it('shows sources healthy message when all match', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				engineHealth: {
					A: makeEngineHealth({ sources: { cam1: 'healthy', cam2: 'healthy' } }),
					B: makeEngineHealth({ sources: { cam1: 'healthy', cam2: 'healthy' } }),
				},
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('All sources healthy on both engines');
	});

	it('shows source divergence when health differs', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				engineHealth: {
					A: makeEngineHealth({ sources: { cam1: 'healthy', cam2: 'stale' } }),
					B: makeEngineHealth({ sources: { cam1: 'healthy', cam2: 'healthy' } }),
				},
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('cam2');
	});

	it('caps divergence log at 20 entries by default', () => {
		const now = Date.now();
		const log: DivergenceLogEntry[] = Array.from({ length: 25 }, (_, i) => ({
			timestamp: now - i * 1000,
			type: 'info' as const,
			message: `Entry ${i}`,
		}));
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({ divergenceLog: log }),
		});
		const entries = container.querySelectorAll('.log-entry');
		expect(entries.length).toBe(20);
	});

	it('shows "Show all" when log has more than 20 entries', () => {
		const now = Date.now();
		const log: DivergenceLogEntry[] = Array.from({ length: 25 }, (_, i) => ({
			timestamp: now - i * 1000,
			type: 'info' as const,
			message: `Entry ${i}`,
		}));
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({ divergenceLog: log }),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('Show all');
	});

	it('force reconcile buttons disabled for single-engine', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'single-engine' as const }),
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		expect(buttons.length).toBe(2);
		for (const btn of buttons) {
			expect((btn as HTMLButtonElement).disabled).toBe(true);
		}
	});

	it('force reconcile buttons disabled for disconnected', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'disconnected' as const }),
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		expect(buttons.length).toBe(2);
		for (const btn of buttons) {
			expect((btn as HTMLButtonElement).disabled).toBe(true);
		}
	});

	it('force reconcile buttons disabled for recovering', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'recovering' as const }),
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		expect(buttons.length).toBe(2);
		for (const btn of buttons) {
			expect((btn as HTMLButtonElement).disabled).toBe(true);
		}
	});

	it('force reconcile buttons enabled for mismatch', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'mismatch' as const }),
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		for (const btn of buttons) {
			expect((btn as HTMLButtonElement).disabled).toBe(false);
		}
	});

	it('force reconcile buttons enabled for lagging', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				health: makeSyncHealth({ status: 'lagging' as const }),
			}),
		});
		const buttons = container.querySelectorAll('.reconcile-btn');
		for (const btn of buttons) {
			expect((btn as HTMLButtonElement).disabled).toBe(false);
		}
	});

	it('renders audio comparison when provided', async () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				audioComparison: [
					{ channel: 'CH1', levelA: -12.5, levelB: -12.3, withinTolerance: true },
					{ channel: 'CH2', levelA: -6.0, levelB: -18.0, withinTolerance: false },
				],
			}),
		});
		// Header should show out of tolerance
		const text = container.textContent ?? '';
		expect(text).toContain('1 out of tolerance');

		// Expand the section
		const toggles = container.querySelectorAll('.section-toggle');
		const audioToggle = Array.from(toggles).find(t => t.textContent?.includes('AUDIO SYNC'));
		expect(audioToggle).toBeTruthy();
		await fireEvent.click(audioToggle!);

		// Should show channel data
		const expanded = container.textContent ?? '';
		expect(expanded).toContain('CH1');
		expect(expanded).toContain('CH2');
		expect(expanded).toContain('-12.5dB');
		expect(expanded).toContain('-18.0dB');
	});

	it('shows audio in sync when all channels within tolerance', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				audioComparison: [
					{ channel: 'CH1', levelA: -12.5, levelB: -12.3, withinTolerance: true },
				],
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('Audio in sync');
	});

	it('renders output status when provided', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				outputStatus: {
					recording: { A: true, B: true },
					destinations: [
						{
							name: 'SRT Out 1',
							A: { state: 'connected', bitrate: 5000 },
							B: { state: 'connected', bitrate: 4800 },
						},
					],
				},
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('REC');
		expect(text).toContain('SRT Out 1');
		expect(text).toContain('5k');
		expect(text).toContain('5k / 5k');
	});

	it('shows recording mismatch when engines differ', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				outputStatus: {
					recording: { A: true, B: false },
					destinations: [],
				},
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('MISMATCH');
	});

	it('shows playout out of sync when diverged', async () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				playoutComparison: {
					itemA: 'clip_001.ts',
					itemB: 'clip_002.ts',
					inSync: false,
					ptsFrameDelta: 5,
				},
			}),
		});
		// Header should show out of sync
		const text = container.textContent ?? '';
		expect(text).toContain('out of sync');

		// The $effect auto-expands the playout section when out of sync.
		// Flush effects, then check if already expanded before clicking.
		await tick();
		const toggles = container.querySelectorAll('.section-toggle');
		const playoutToggle = Array.from(toggles).find(t => t.textContent?.includes('PLAYOUT SYNC'));
		const alreadyExpanded = container.querySelector('.playout-comparison') !== null;
		if (!alreadyExpanded) {
			await fireEvent.click(playoutToggle!);
		}

		const expanded = container.textContent ?? '';
		expect(expanded).toContain('clip_001.ts');
		expect(expanded).toContain('clip_002.ts');
		expect(expanded).toContain('5 frames');
	});

	it('shows playout in sync when matched', () => {
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({
				playoutComparison: {
					itemA: 'clip_001.ts',
					itemB: 'clip_001.ts',
					inSync: true,
				},
			}),
		});
		const text = container.textContent ?? '';
		expect(text).toContain('Playout in sync');
	});

	it('renders program compare section with No signal placeholders', () => {
		const { container } = render(RedundancyPanel, { props: makeDefaultProps() });
		const text = container.textContent ?? '';
		expect(text).toContain('PROGRAM COMPARE');
		expect(text).toContain('Full Compare');
		// Thumbnail labels present
		expect(text).toContain('Engine A');
		expect(text).toContain('Engine B');
		// No signal placeholders shown when no thumbnails loaded
		const placeholders = container.querySelectorAll('.thumb-placeholder');
		expect(placeholders.length).toBe(2);
		expect(placeholders[0].textContent).toBe('No signal');
		expect(placeholders[1].textContent).toBe('No signal');
	});

	it('calls oncompare when Full Compare button is clicked', async () => {
		const oncompare = vi.fn();
		const { container } = render(RedundancyPanel, {
			props: makeDefaultProps({ oncompare }),
		});
		const compareBtn = container.querySelector('.compare-btn');
		expect(compareBtn).toBeTruthy();
		await fireEvent.click(compareBtn!);
		expect(oncompare).toHaveBeenCalledOnce();
	});
});
