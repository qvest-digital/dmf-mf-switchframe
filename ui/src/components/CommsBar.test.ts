import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import CommsBar from './CommsBar.svelte';
import { CommsAudioManager } from '$lib/audio/comms';
import type { CommsState } from '$lib/api/types';

const mocks = vi.hoisted(() => {
	const instances: Array<{
		start: ReturnType<typeof vi.fn>;
		stop: ReturnType<typeof vi.fn>;
	}> = [];
	return { instances };
});

vi.mock('$lib/audio/comms', () => {
	class MockCommsAudioManager {
		start = vi.fn().mockResolvedValue(undefined);
		stop = vi.fn().mockResolvedValue(undefined);
		reconnect = vi.fn().mockResolvedValue(undefined);
		setVolume = vi.fn();
		setMuted = vi.fn();
		get isActive() {
			return true;
		}
		constructor(public opts: unknown) {
			mocks.instances.push(this);
		}
	}
	return { CommsAudioManager: MockCommsAudioManager };
});

vi.mock('$lib/api/switch-api', () => ({
	commsJoin: vi.fn().mockResolvedValue({}),
	commsLeave: vi.fn().mockResolvedValue({}),
	commsMute: vi.fn().mockResolvedValue({}),
	apiCall: vi.fn((p: Promise<unknown>) => p.catch(() => {})),
	getAuthToken: vi.fn(() => 'test-token'),
}));

vi.mock('$lib/state/notifications.svelte', () => ({
	notify: vi.fn(),
}));

function makeProps(overrides: Record<string, unknown> = {}) {
	return {
		commsState: { active: false, participants: [] } as unknown as CommsState,
		operatorId: 'op-1',
		operatorName: 'Operator One',
		visible: true,
		onToggle: vi.fn(),
		getTransport: () => ({}) as WebTransport,
		...overrides,
	};
}

describe('CommsBar', () => {
	beforeEach(() => {
		mocks.instances.length = 0;
		vi.clearAllMocks();
	});

	it('starts a CommsAudioManager when joining', async () => {
		const { getByText } = render(CommsBar, { props: makeProps() });

		await fireEvent.click(getByText('JOIN'));
		await waitFor(() => expect(mocks.instances.length).toBe(1));
		expect(mocks.instances[0].start).toHaveBeenCalledOnce();
	});

	it('stops the CommsAudioManager when the component is destroyed (layout switch)', async () => {
		const { getByText, unmount } = render(CommsBar, { props: makeProps() });

		await fireEvent.click(getByText('JOIN'));
		await waitFor(() => expect(mocks.instances.length).toBe(1));
		const manager = mocks.instances[0];
		expect(manager.start).toHaveBeenCalledOnce();
		expect(manager.stop).not.toHaveBeenCalled();

		// Destroying the component (e.g. switching traditional -> simple layout)
		// must release the microphone / AudioContexts / bidi stream.
		unmount();

		expect(manager.stop).toHaveBeenCalledOnce();
	});

	it('does not double-stop a manager already stopped via Leave', async () => {
		const participants = [
			{ operatorId: 'op-1', name: 'Operator One', muted: true, speaking: false },
		];
		const props = makeProps();
		const { getByText, rerender, unmount } = render(CommsBar, { props });

		await fireEvent.click(getByText('JOIN'));
		await waitFor(() => expect(mocks.instances.length).toBe(1));
		const manager = mocks.instances[0];

		// Server confirms the join so the LEAVE button renders.
		await rerender({
			...props,
			commsState: { active: true, participants } as unknown as CommsState,
		});

		await fireEvent.click(getByText('LEAVE'));
		await waitFor(() => expect(manager.stop).toHaveBeenCalledOnce());

		unmount();
		expect(manager.stop).toHaveBeenCalledOnce();
	});

	it('uses the mocked CommsAudioManager', () => {
		// Sanity: the import above resolves to the mock class.
		const inst = new CommsAudioManager({} as never);
		expect(mocks.instances).toContain(inst);
	});
});
