import { describe, it, expect, vi, beforeEach } from 'vitest';
import { tick } from 'svelte';
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
		const on = state({
			squeezeback: { rect: { x: 480, y: 0, w: 1440, h: 810 }, durationMs: 800, on: true, animating: false },
		});
		const { container } = render(SqueezebackEditor, { props: { state: on } });
		const btn = container.querySelector('.squeeze-btn')!;
		expect(btn.textContent).toContain('Restore');
		await fireEvent.click(btn);
		expect(api.squeezebackOff).toHaveBeenCalledWith({ durationMs: 800 });
		expect(api.setSqueezeback).not.toHaveBeenCalled();
	});

	// A reload must show the live configuration and configure it as is,
	// not replace it with the editor's defaults.
	it('seeds from the live configuration and keeps its easing', async () => {
		const api = await import('$lib/api/switch-api');
		const live = state({
			squeezeback: {
				rect: { x: 480, y: 270, w: 1440, h: 810 },
				background: 'cam2',
				durationMs: 500,
				easing: 'ease-out',
				on: false,
				animating: false,
			},
		});
		const { container } = render(SqueezebackEditor, { props: { state: live } });
		await tick();
		expect((container.querySelector('.squeeze-shape') as HTMLSelectElement).value).toBe('bottom-right');
		expect((container.querySelector('.squeeze-background') as HTMLSelectElement).value).toBe('cam2');
		expect((container.querySelector('.squeeze-duration') as HTMLInputElement).value).toBe('500');

		await fireEvent.click(container.querySelector('.squeeze-btn')!);
		await vi.waitFor(() => expect(api.squeezebackOn).toHaveBeenCalled());
		expect(api.setSqueezeback).toHaveBeenCalledWith({
			rect: { x: 480, y: 270, w: 1440, h: 810 },
			background: 'cam2',
			durationMs: 500,
			easing: 'ease-out',
		});
	});

	it('keeps a rect that matches no shape as custom', async () => {
		const api = await import('$lib/api/switch-api');
		const live = state({ squeezeback: { rect: { x: 0, y: 0, w: 960, h: 540 }, on: false, animating: false } });
		const { container } = render(SqueezebackEditor, { props: { state: live } });
		await tick();
		expect((container.querySelector('.squeeze-shape') as HTMLSelectElement).value).toBe('custom');
		expect((container.querySelector('.squeeze-duration') as HTMLInputElement).value).toBe('0');

		await fireEvent.click(container.querySelector('.squeeze-btn')!);
		await vi.waitFor(() => expect(api.squeezebackOn).toHaveBeenCalled());
		expect(api.setSqueezeback).toHaveBeenCalledWith({
			rect: { x: 0, y: 0, w: 960, h: 540 },
			background: '',
			durationMs: 0,
		});
	});

	it('keeps an unsent edit across a broadcast that leaves the configuration alone', async () => {
		const sq = { rect: { x: 480, y: 0, w: 1440, h: 810 }, background: 'cam2', on: false, animating: false };
		const { container, rerender } = render(SqueezebackEditor, { props: { state: state({ squeezeback: sq }) } });
		await tick();
		const bg = container.querySelector('.squeeze-background') as HTMLSelectElement;
		await fireEvent.change(bg, { target: { value: 'cam1' } });
		await rerender({ state: state({ squeezeback: { ...sq } }) });
		await tick();
		expect(bg.value).toBe('cam1');

		await rerender({ state: state({ squeezeback: { ...sq, background: '' } }) });
		await tick();
		expect(bg.value, 'a changed configuration wins').toBe('');
	});
});
