import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OAStatusBar from './OAStatusBar.svelte';

function defaultProps() {
	return {
		programSource: 'Live Program',
		bugActive: false,
		scteState: 'content' as const,
		isDualEngine: false,
	};
}

describe('OAStatusBar', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date(2026, 3, 9, 14, 32, 7));
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('renders program source label', () => {
		render(OAStatusBar, { props: defaultProps() });
		expect(screen.getByText('Live Program')).toBeTruthy();
	});

	it('shows bug state ON when active', () => {
		render(OAStatusBar, { props: { ...defaultProps(), bugActive: true } });
		const bugEl = screen.getByText('ON');
		expect(bugEl).toBeTruthy();
		expect(bugEl.classList.contains('bug-on')).toBe(true);
	});

	it('shows bug state OFF when inactive', () => {
		render(OAStatusBar, { props: { ...defaultProps(), bugActive: false } });
		const bugEl = screen.getByText('OFF');
		expect(bugEl).toBeTruthy();
		expect(bugEl.classList.contains('bug-off')).toBe(true);
	});

	it('shows SCTE state CONTENT in content mode', () => {
		render(OAStatusBar, { props: { ...defaultProps(), scteState: 'content' as const } });
		expect(screen.getByText('CONTENT')).toBeTruthy();
	});

	it('shows SCTE state IN BREAK with countdown', () => {
		render(OAStatusBar, {
			props: { ...defaultProps(), scteState: 'in_break' as const, scteCountdown: 82 },
		});
		expect(screen.getByText(/IN BREAK/)).toBeTruthy();
		expect(screen.getByText(/01:22/)).toBeTruthy();
	});

	it('shows SCTE state IDLE', () => {
		render(OAStatusBar, { props: { ...defaultProps(), scteState: 'idle' as const } });
		expect(screen.getByText('IDLE')).toBeTruthy();
	});

	it('shows engine dots in dual-engine mode', () => {
		const { container } = render(OAStatusBar, {
			props: {
				...defaultProps(),
				isDualEngine: true,
				engineAHealthy: true,
				engineBHealthy: true,
				syncStatus: 'synced',
			},
		});
		expect(container.querySelector('.engine-group')).toBeTruthy();
		const dots = container.querySelectorAll('.engine-dot');
		expect(dots.length).toBe(2);
	});

	it('hides engine dots in single-engine mode', () => {
		const { container } = render(OAStatusBar, {
			props: { ...defaultProps(), isDualEngine: false },
		});
		expect(container.querySelector('.engine-group')).toBeFalsy();
	});

	it('shows single engine indicator in single-engine mode', () => {
		render(OAStatusBar, { props: defaultProps() });
		expect(screen.getByText('ENGINE A')).toBeTruthy();
	});

	it('bug toggle calls callback on click', async () => {
		const toggleFn = vi.fn();
		render(OAStatusBar, {
			props: { ...defaultProps(), onBugToggle: toggleFn },
		});
		const bugButton = screen.getByTitle('Toggle bug overlay');
		await fireEvent.click(bugButton);
		expect(toggleFn).toHaveBeenCalledOnce();
	});

	it('renders clock in HH:MM:SS format', () => {
		const { container } = render(OAStatusBar, { props: defaultProps() });
		const text = container.textContent ?? '';
		expect(text).toMatch(/14:32:07/);
	});

	it('shows sync indicator in dual-engine mode', () => {
		render(OAStatusBar, {
			props: {
				...defaultProps(),
				isDualEngine: true,
				engineAHealthy: true,
				engineBHealthy: false,
				syncStatus: 'synced',
			},
		});
		expect(screen.getByText('SYNC')).toBeTruthy();
	});

	it('sync indicator is clickable', async () => {
		const syncFn = vi.fn();
		render(OAStatusBar, {
			props: {
				...defaultProps(),
				isDualEngine: true,
				engineAHealthy: true,
				engineBHealthy: true,
				syncStatus: 'synced',
				onSyncClick: syncFn,
			},
		});
		const syncBtn = screen.getByTitle('Engine sync status');
		await fireEvent.click(syncBtn);
		expect(syncFn).toHaveBeenCalledOnce();
	});

	it('shows segment timing when elapsed and duration provided', () => {
		render(OAStatusBar, {
			props: { ...defaultProps(), segmentElapsed: 135, segmentDuration: 180 },
		});
		expect(screen.getByText(/02:15/)).toBeTruthy();
		expect(screen.getByText(/03:00/)).toBeTruthy();
	});

	it('shows uptime when showUptime is true', () => {
		render(OAStatusBar, {
			props: { ...defaultProps(), showUptime: true, uptimeSeconds: 7384 },
		});
		expect(screen.getByText(/UPTIME/)).toBeTruthy();
		expect(screen.getByText(/02:03:04/)).toBeTruthy();
	});

	it('mode menu button fires callback', async () => {
		const modeFn = vi.fn();
		render(OAStatusBar, {
			props: { ...defaultProps(), onModeMenu: modeFn },
		});
		const modeBtn = screen.getByTitle('Switch layout mode');
		await fireEvent.click(modeBtn);
		expect(modeFn).toHaveBeenCalledOnce();
	});

	it('engine A dot is green when healthy', () => {
		const { container } = render(OAStatusBar, {
			props: {
				...defaultProps(),
				isDualEngine: true,
				engineAHealthy: true,
				engineBHealthy: true,
				syncStatus: 'synced',
			},
		});
		const dots = container.querySelectorAll('.engine-dot');
		expect(dots[0].classList.contains('dot-healthy')).toBe(true);
	});

	it('engine B dot is red when offline', () => {
		const { container } = render(OAStatusBar, {
			props: {
				...defaultProps(),
				isDualEngine: true,
				engineAHealthy: true,
				engineBHealthy: false,
				syncStatus: 'diverged',
			},
		});
		const dots = container.querySelectorAll('.engine-dot');
		expect(dots[1].classList.contains('dot-offline')).toBe(true);
	});

	it('SCTE in_break shows red styling', () => {
		const { container } = render(OAStatusBar, {
			props: { ...defaultProps(), scteState: 'in_break' as const, scteCountdown: 30 },
		});
		const scteEl = container.querySelector('.scte-in_break');
		expect(scteEl).toBeTruthy();
	});

	it('shows bypass badge when bypassCount > 0', () => {
		const { container } = render(OAStatusBar, {
			props: { ...defaultProps(), bypassCount: 2 },
		});
		const badge = container.querySelector('.bypass-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toContain('BYPASS');
		expect(badge?.textContent).toContain('2');
	});

	it('hides bypass badge when bypassCount is 0', () => {
		const { container } = render(OAStatusBar, {
			props: { ...defaultProps(), bypassCount: 0 },
		});
		const badge = container.querySelector('.bypass-badge');
		expect(badge).toBeNull();
	});

	it('hides bypass badge when bypassCount is not provided', () => {
		const { container } = render(OAStatusBar, {
			props: defaultProps(),
		});
		const badge = container.querySelector('.bypass-badge');
		expect(badge).toBeNull();
	});

	it('bypass badge click calls onBypassClick', async () => {
		const bypassClickFn = vi.fn();
		const { container } = render(OAStatusBar, {
			props: { ...defaultProps(), bypassCount: 3, onBypassClick: bypassClickFn },
		});
		const badge = container.querySelector('.bypass-badge') as HTMLElement;
		expect(badge).toBeTruthy();
		await fireEvent.click(badge);
		expect(bypassClickFn).toHaveBeenCalledOnce();
	});
});
