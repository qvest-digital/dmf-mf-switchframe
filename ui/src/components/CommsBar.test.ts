import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import CommsBar from './CommsBar.svelte';
import { CommsAudioManager } from '$lib/audio/comms';
import type { CommsState } from '$lib/api/types';
import { reloadHolds } from '$lib/util/activity';

const mocks = vi.hoisted(() => {
	const instances: Array<{
		start: ReturnType<typeof vi.fn>;
		stop: ReturnType<typeof vi.fn>;
		setMuted: ReturnType<typeof vi.fn>;
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

	it('re-creates the audio manager when remounted while still joined (layout switch)', async () => {
		const participants = [
			{ operatorId: 'op-1', name: 'Operator One', muted: true, speaking: false },
		];
		const props = makeProps();
		const { getByText, rerender, unmount } = render(CommsBar, { props });

		await fireEvent.click(getByText('JOIN'));
		await waitFor(() => expect(mocks.instances.length).toBe(1));
		const first = mocks.instances[0];

		// Server confirms the join.
		await rerender({
			...props,
			commsState: { active: true, participants } as unknown as CommsState,
		});

		// Layout switch destroys the component while still joined server-side.
		unmount();
		expect(first.stop).toHaveBeenCalledOnce();

		// Remount (layout switched back): isJoined is server-derived, so the
		// joined bar renders — recovery must re-create and start a fresh manager.
		const remount = render(CommsBar, {
			props: makeProps({
				commsState: { active: true, participants } as unknown as CommsState,
			}),
		});
		await waitFor(() => expect(mocks.instances.length).toBe(2));
		const second = mocks.instances[1];
		expect(second.start).toHaveBeenCalledOnce();
		// Mic state is synced to the server-side participant (muted).
		await waitFor(() => expect(second.setMuted).toHaveBeenCalledWith(true));

		// And the recovered manager is owned: destroy stops it.
		remount.unmount();
		expect(second.stop).toHaveBeenCalledOnce();
	});

	it('does not create an audio manager on mount when not joined', async () => {
		const { unmount } = render(CommsBar, { props: makeProps() });

		// Give any effects a chance to run.
		await new Promise((r) => setTimeout(r, 10));
		expect(mocks.instances.length).toBe(0);
		unmount();
	});

	it('does not re-create the audio manager after Leave while the server still lists us', async () => {
		const participants = [
			{ operatorId: 'op-1', name: 'Operator One', muted: true, speaking: false },
		];
		const props = makeProps();
		const { getByText, rerender } = render(CommsBar, { props });

		await fireEvent.click(getByText('JOIN'));
		await waitFor(() => expect(mocks.instances.length).toBe(1));

		await rerender({
			...props,
			commsState: { active: true, participants } as unknown as CommsState,
		});

		await fireEvent.click(getByText('LEAVE'));
		await waitFor(() => expect(mocks.instances[0].stop).toHaveBeenCalledOnce());

		// The leave is confirmed asynchronously; while the server still lists us
		// as a participant, recovery must not re-join audio.
		await new Promise((r) => setTimeout(r, 10));
		expect(mocks.instances.length).toBe(1);
	});

	// A hidden tab carrying talkback would drop the operator out of comms
	// on a stale-build reload.
	it('holds a reload while this operator is in comms', async () => {
		const joined = { active: true, participants: [{ operatorId: 'op-1', name: 'Operator One', muted: false, speaking: false }] } as unknown as CommsState;
		const { rerender } = render(CommsBar, { props: makeProps() });
		expect(reloadHolds()).not.toContain('comms');
		await rerender(makeProps({ commsState: joined }));
		expect(reloadHolds()).toContain('comms');
		await rerender(makeProps({ commsState: { active: false, participants: [] } }));
		expect(reloadHolds()).not.toContain('comms');
	});

	it('uses the mocked CommsAudioManager', () => {
		// Sanity: the import above resolves to the mock class.
		const inst = new CommsAudioManager({} as never);
		expect(mocks.instances).toContain(inst);
	});
});
