import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import PodBoard from '../PodBoard.svelte';
import { SwitchApiError } from '$lib/api/switch-api';
import type { PodStatusBroadcast } from '$lib/api/types';

const mockPodCreate = vi.fn();
const mockPodArm = vi.fn();
const mockPodFire = vi.fn();
const mockPodDump = vi.fn();
const mockPodHold = vi.fn();
const mockPodExtend = vi.fn();

vi.mock('$lib/api/switch-api', async () => {
	const actual = await vi.importActual<typeof import('$lib/api/switch-api')>(
		'$lib/api/switch-api',
	);
	return {
		...actual,
		podCreate: (...a: unknown[]) => mockPodCreate(...a),
		podArm: (...a: unknown[]) => mockPodArm(...a),
		podFire: (...a: unknown[]) => mockPodFire(...a),
		podDump: (...a: unknown[]) => mockPodDump(...a),
		podHold: (...a: unknown[]) => mockPodHold(...a),
		podExtend: (...a: unknown[]) => mockPodExtend(...a),
	};
});

const mockNotify = vi.fn();
vi.mock('$lib/state/notifications.svelte', () => ({
	notify: (...a: unknown[]) => mockNotify(...a),
}));

function armedPod(id: string): PodStatusBroadcast {
	return {
		id,
		type: 'standard',
		armed: true,
		state: 'armed',
		durationMs: 30_000,
		elapsedMs: 0,
		remainingMs: 30_000,
	} as unknown as PodStatusBroadcast;
}

function activePod(id: string): PodStatusBroadcast {
	return {
		id,
		type: 'standard',
		armed: true,
		state: 'break_active',
		durationMs: 30_000,
		elapsedMs: 5_000,
		remainingMs: 25_000,
	} as unknown as PodStatusBroadcast;
}

describe('PodBoard error feedback', () => {
	beforeEach(() => {
		mockPodCreate.mockReset();
		mockPodArm.mockReset();
		mockPodFire.mockReset();
		mockPodDump.mockReset();
		mockPodHold.mockReset();
		mockPodExtend.mockReset();
		mockNotify.mockReset();
	});

	afterEach(() => {
		cleanup();
	});

	it('showsToastOnFireFailure', async () => {
		mockPodFire.mockRejectedValueOnce(new SwitchApiError(409, 'lock held'));

		const { getByTitle } = render(PodBoard, {
			props: { channel: 0, pods: [armedPod('pod-abc123')] },
		});

		await fireEvent.click(getByTitle('Fire now'));
		await vi.waitFor(() => expect(mockNotify).toHaveBeenCalled());

		const [level, msg] = mockNotify.mock.calls[0] as [string, string];
		expect(level).toBe('error');
		expect(msg.toLowerCase()).toMatch(/lock held|409/);
	});

	it('surfaces fire generic failure when SwitchApiError has no message', async () => {
		mockPodFire.mockRejectedValueOnce(new Error('network'));

		const { getByTitle } = render(PodBoard, {
			props: { channel: 0, pods: [armedPod('pod-abc123')] },
		});

		await fireEvent.click(getByTitle('Fire now'));
		await vi.waitFor(() => expect(mockNotify).toHaveBeenCalled());

		expect(mockNotify.mock.calls[0][0]).toBe('error');
		expect(String(mockNotify.mock.calls[0][1]).toLowerCase()).toContain('fire');
	});

	it('shows toast on dump failure', async () => {
		mockPodDump.mockRejectedValueOnce(new SwitchApiError(500, 'boom'));

		const active = activePod('pod-xyz999');
		const { getByText } = render(PodBoard, {
			props: { channel: 0, pods: [active], activePod: active },
		});

		await fireEvent.click(getByText('DUMP'));
		await vi.waitFor(() => expect(mockNotify).toHaveBeenCalled());

		expect(mockNotify.mock.calls[0][0]).toBe('error');
		expect(String(mockNotify.mock.calls[0][1]).toLowerCase()).toContain('dump');
	});

	it('shows toast on hold failure', async () => {
		mockPodHold.mockRejectedValueOnce(new SwitchApiError(500, 'engine down'));

		const active = activePod('pod-xyz999');
		const { getByText } = render(PodBoard, {
			props: { channel: 0, pods: [active], activePod: active },
		});

		await fireEvent.click(getByText('HOLD'));
		await vi.waitFor(() => expect(mockNotify).toHaveBeenCalled());

		expect(mockNotify.mock.calls[0][0]).toBe('error');
		expect(String(mockNotify.mock.calls[0][1]).toLowerCase()).toContain('hold');
	});

	it('shows toast on extend failure', async () => {
		mockPodExtend.mockRejectedValueOnce(new SwitchApiError(409, 'not extendable'));

		const active = activePod('pod-xyz999');
		const { getByText } = render(PodBoard, {
			props: { channel: 0, pods: [active], activePod: active },
		});

		await fireEvent.click(getByText('+30s'));
		await vi.waitFor(() => expect(mockNotify).toHaveBeenCalled());

		expect(mockNotify.mock.calls[0][0]).toBe('error');
		expect(String(mockNotify.mock.calls[0][1]).toLowerCase()).toContain('extend');
	});

	it('shows toast on create failure', async () => {
		mockPodCreate.mockRejectedValueOnce(new SwitchApiError(500, 'template missing'));

		const { getByText } = render(PodBoard, { props: { channel: 0 } });

		await fireEvent.click(getByText('+Short'));
		await vi.waitFor(() => expect(mockNotify).toHaveBeenCalled());

		expect(mockNotify.mock.calls[0][0]).toBe('error');
		expect(String(mockNotify.mock.calls[0][1]).toLowerCase()).toContain('create');
	});
});
