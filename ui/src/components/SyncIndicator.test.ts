import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SyncIndicator from './SyncIndicator.svelte';
import type { SyncHealth, SyncStatus } from '$lib/state/reconciler.svelte';

function makeHealth(status: SyncStatus): SyncHealth {
	return {
		status,
		divergences: [],
		authoritative: 'A',
		engineA: { lastSeen: Date.now(), lastCommandSeq: 1, connected: true },
		engineB: { lastSeen: Date.now(), lastCommandSeq: 1, connected: true },
	};
}

describe('SyncIndicator', () => {
	const labelCases: [SyncStatus, string][] = [
		['synced', 'SYNCED'],
		['lagging', 'LAGGING'],
		['diverged', 'DIVERGED'],
		['mismatch', 'MISMATCH'],
		['recovering', 'RECOVERING'],
		['single-engine', '1 ENGINE'],
		['disconnected', 'OFFLINE'],
	];

	it.each(labelCases)('renders label "%s" as "%s"', (status, expectedLabel) => {
		const { getByRole } = render(SyncIndicator, {
			props: { health: makeHealth(status) },
		});
		const button = getByRole('button');
		expect(button.textContent?.trim()).toBe(expectedLabel);
	});

	const cssCases: [SyncStatus, string][] = [
		['synced', 'sync-synced'],
		['lagging', 'sync-lagging'],
		['diverged', 'sync-diverged'],
		['mismatch', 'sync-mismatch'],
		['recovering', 'sync-recovering'],
		['single-engine', 'sync-single'],
		['disconnected', 'sync-disconnected'],
	];

	it.each(cssCases)('has CSS class "%s" for status "%s"', (status, expectedClass) => {
		const { getByRole } = render(SyncIndicator, {
			props: { health: makeHealth(status) },
		});
		const button = getByRole('button');
		expect(button.classList.contains(expectedClass)).toBe(true);
	});

	it('is hidden when health is null', () => {
		const { container } = render(SyncIndicator, {
			props: { health: null },
		});
		expect(container.querySelector('button')).toBeNull();
	});

	it('calls onclick when clicked', async () => {
		const handler = vi.fn();
		const { getByRole } = render(SyncIndicator, {
			props: { health: makeHealth('synced'), onclick: handler },
		});
		await fireEvent.click(getByRole('button'));
		expect(handler).toHaveBeenCalledOnce();
	});
});
