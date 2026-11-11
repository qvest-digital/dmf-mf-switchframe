import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SqueezebackEditor from './SqueezebackEditor.svelte';
import type { ControlRoomState } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	setSqueezeback: vi.fn().mockResolvedValue({}),
	squeezebackOn: vi.fn().mockResolvedValue(undefined),
	squeezebackOff: vi.fn().mockResolvedValue(undefined),
	apiCall: vi.fn((p: Promise<unknown>) => p),
}));

function state(extra: Partial<ControlRoomState> = {}): ControlRoomState {
	return {
		programSource: 'cam1',
		previewSource: 'cam2',
		sources: {
			cam1: { key: 'cam1', label: 'CAM 1', status: 'healthy' },
			cam2: { key: 'cam2', label: 'CAM 2', status: 'healthy' },
		},
		pipelineFormat: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
		...extra,
	} as unknown as ControlRoomState;
}

describe('SqueezebackEditor', () => {
	beforeEach(() => vi.clearAllMocks());

	it('configures an even 75% L-frame over the chosen background, then squeezes', async () => {
		const api = await import('$lib/api/switch-api');
		const { container } = render(SqueezebackEditor, { props: { state: state() } });
		const bg = container.querySelector('.squeeze-background') as HTMLSelectElement;
		await fireEvent.change(bg, { target: { value: 'cam2' } });
		await fireEvent.click(container.querySelector('.squeeze-btn')!);

		await vi.waitFor(() => expect(api.squeezebackOn).toHaveBeenCalled());
		expect(api.setSqueezeback).toHaveBeenCalledWith({
			rect: { x: 480, y: 0, w: 1440, h: 810 },
			background: 'cam2',
			durationMs: 800,
		});
		expect(api.squeezebackOff).not.toHaveBeenCalled();
	});

	it('restores when on', async () => {
		const api = await import('$lib/api/switch-api');
		const on = state({ squeezeback: { rect: { x: 480, y: 0, w: 1440, h: 810 }, on: true, animating: false } });
		const { container } = render(SqueezebackEditor, { props: { state: on } });
		const btn = container.querySelector('.squeeze-btn')!;
		expect(btn.textContent).toContain('Restore');
		await fireEvent.click(btn);
		expect(api.squeezebackOff).toHaveBeenCalledWith({ durationMs: 800 });
		expect(api.setSqueezeback).not.toHaveBeenCalled();
	});
});
