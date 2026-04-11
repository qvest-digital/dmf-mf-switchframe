import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OAPlayoutSection from './OAPlayoutSection.svelte';

function defaultProps(overrides: Record<string, unknown> = {}) {
	return {
		enabled: false,
		onToggle: vi.fn(),
		...overrides,
	};
}

describe('OAPlayoutSection', () => {
	it('toggle renders and click calls onToggle', async () => {
		const fn = vi.fn();
		render(OAPlayoutSection, {
			props: defaultProps({ onToggle: fn }),
		});
		const toggleBtn = screen.getByText('OFF');
		expect(toggleBtn).toBeTruthy();
		await fireEvent.click(toggleBtn);
		expect(fn).toHaveBeenCalledOnce();
		expect(fn).toHaveBeenCalledWith(true);
	});

	it('toggle shows ON when enabled', () => {
		render(OAPlayoutSection, {
			props: defaultProps({ enabled: true }),
		});
		expect(screen.getByText('ON')).toBeTruthy();
	});

	it('disabled shows message', () => {
		render(OAPlayoutSection, {
			props: defaultProps({ enabled: false }),
		});
		expect(screen.getByText(/Playout not enabled/)).toBeTruthy();
	});

	it('enabled shows schedule controls', () => {
		render(OAPlayoutSection, {
			props: defaultProps({
				enabled: true,
				onNewSchedule: vi.fn(),
				onLoadFile: vi.fn(),
			}),
		});
		expect(screen.queryByText(/Playout not enabled/)).toBeNull();
	});

	it('New Schedule button renders when enabled', () => {
		render(OAPlayoutSection, {
			props: defaultProps({
				enabled: true,
				onNewSchedule: vi.fn(),
			}),
		});
		expect(screen.getByText('New Schedule')).toBeTruthy();
	});

	it('New Schedule button fires callback', async () => {
		const fn = vi.fn();
		render(OAPlayoutSection, {
			props: defaultProps({
				enabled: true,
				onNewSchedule: fn,
			}),
		});
		await fireEvent.click(screen.getByText('New Schedule'));
		expect(fn).toHaveBeenCalledOnce();
	});

	it('Load File button renders when enabled', () => {
		render(OAPlayoutSection, {
			props: defaultProps({
				enabled: true,
				onLoadFile: vi.fn(),
			}),
		});
		expect(screen.getByText('Load File')).toBeTruthy();
	});

	it('toggle calls onToggle with false when disabling', async () => {
		const fn = vi.fn();
		render(OAPlayoutSection, {
			props: defaultProps({ enabled: true, onToggle: fn }),
		});
		const toggleBtn = screen.getByText('ON');
		await fireEvent.click(toggleBtn);
		expect(fn).toHaveBeenCalledWith(false);
	});

	it('Load Saved dropdown renders with saved playlists', () => {
		render(OAPlayoutSection, {
			props: defaultProps({
				enabled: true,
				savedPlaylists: ['Morning Show', 'Evening News'],
				onLoadSaved: vi.fn(),
			}),
		});
		const select = screen.getByRole('combobox');
		expect(select).toBeTruthy();
	});
});
