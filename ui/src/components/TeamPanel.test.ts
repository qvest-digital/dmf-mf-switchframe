import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import TeamPanel from './TeamPanel.svelte';
import type { ControlRoomState } from '$lib/api/types';

const operatorInviteTokens = vi.fn();
vi.mock('$lib/api/switch-api', () => ({
	operatorInviteTokens: () => operatorInviteTokens(),
}));

let session: { id: string; name: string; role: string; token: string } | null = null;
vi.mock('$lib/state/operator.svelte', () => ({
	getSession: () => session,
}));

const state = { operators: [] } as unknown as ControlRoomState;

async function settle(): Promise<void> {
	for (let i = 0; i < 20; i++) {
		await Promise.resolve();
		await tick();
	}
}

describe('TeamPanel invite tokens', () => {
	beforeEach(() => {
		operatorInviteTokens.mockReset();
		session = { id: 'ext-1', name: 'dave', role: 'director', token: 't' };
	});

	// An engine configured without invite tokens answers with an empty map.
	// Refetching until the map is non-empty flooded the engine until the
	// browser ran out of connections.
	it('fetches once when the engine has no invite tokens', async () => {
		operatorInviteTokens.mockResolvedValue({});
		render(TeamPanel, { props: { state } });
		await settle();
		expect(operatorInviteTokens).toHaveBeenCalledTimes(1);
	});

	it('does not fetch for an operator who is not a director', async () => {
		session = { id: 'ext-2', name: 'erin', role: 'viewer', token: 't' };
		operatorInviteTokens.mockResolvedValue({});
		render(TeamPanel, { props: { state } });
		await settle();
		expect(operatorInviteTokens).not.toHaveBeenCalled();
	});

	it('fetches once when the engine refuses the request', async () => {
		operatorInviteTokens.mockRejectedValue(new Error('403'));
		render(TeamPanel, { props: { state } });
		await settle();
		expect(operatorInviteTokens).toHaveBeenCalledTimes(1);
	});
});
