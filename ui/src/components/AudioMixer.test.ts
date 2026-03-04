import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import AudioMixer from './AudioMixer.svelte';

describe('AudioMixer', () => {
	const state = {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		audioChannels: {
			cam1: { level: 0, muted: false, afv: true },
			cam2: { level: -6, muted: true, afv: false },
		},
		masterLevel: 0,
		programPeak: [-12, -14] as [number, number],
		tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const, lastFrameTime: 0 },
			cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const, lastFrameTime: 0 },
		},
		seq: 1,
		timestamp: Date.now(),
	};

	it('should render a channel strip per source', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const strips = container.querySelectorAll('.channel-strip');
		expect(strips.length).toBe(2);
	});

	it('should show source labels', () => {
		const { container } = render(AudioMixer, { props: { state } });
		expect(container.textContent).toContain('Camera 1');
		expect(container.textContent).toContain('Camera 2');
	});

	it('should show mute state', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const muteButtons = container.querySelectorAll('.mute-btn');
		expect(muteButtons.length).toBe(2);
	});

	it('should show AFV state', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const afvButtons = container.querySelectorAll('.afv-btn');
		expect(afvButtons.length).toBe(2);
	});

	it('should render master fader', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const master = container.querySelector('.master-strip');
		expect(master).toBeTruthy();
	});

	it('should render program peak meter', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const meter = container.querySelector('.program-meter');
		expect(meter).toBeTruthy();
	});

	it('should show PFL active state for matching source', () => {
		const { container } = render(AudioMixer, { props: { state, pflActiveSource: 'cam1' } });
		const pflButtons = container.querySelectorAll('.pfl-btn');
		// cam1 is first (sorted), cam2 is second
		expect(pflButtons[0].classList.contains('active')).toBe(true);
		expect(pflButtons[1].classList.contains('active')).toBe(false);
	});

	it('should not show any PFL active when pflActiveSource is null', () => {
		const { container } = render(AudioMixer, { props: { state, pflActiveSource: null } });
		const pflButtons = container.querySelectorAll('.pfl-btn');
		expect(pflButtons[0].classList.contains('active')).toBe(false);
		expect(pflButtons[1].classList.contains('active')).toBe(false);
	});

	it('should call onPFLToggle when PFL button is clicked', async () => {
		const onPFLToggle = vi.fn();
		const { container } = render(AudioMixer, { props: { state, onPFLToggle } });
		const pflButtons = container.querySelectorAll('.pfl-btn');
		await fireEvent.click(pflButtons[0]);
		expect(onPFLToggle).toHaveBeenCalledWith('cam1');
	});
});
