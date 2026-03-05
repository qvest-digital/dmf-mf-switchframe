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

	it('should render stereo L/R meter bars per channel strip', () => {
		const sourceLevels = {
			cam1: { peakL: 0.5, peakR: 0.3 },
			cam2: { peakL: 0.1, peakR: 0.8 },
		};
		const { container } = render(AudioMixer, { props: { state, sourceLevels } });
		const strips = container.querySelectorAll('.channel-strip');
		// Each channel strip should have a stereo-meter container with L and R peak bars
		strips.forEach((strip) => {
			const stereoMeter = strip.querySelector('.stereo-meter');
			expect(stereoMeter).toBeTruthy();
			const peakBars = stereoMeter!.querySelectorAll('.peak-bar');
			expect(peakBars.length).toBe(2);
			expect(peakBars[0].classList.contains('left')).toBe(true);
			expect(peakBars[1].classList.contains('right')).toBe(true);
		});
	});

	it('should render L and R meter fills with correct heights from sourceLevels', () => {
		const sourceLevels = {
			cam1: { peakL: 0.5, peakR: 0.0 },
			cam2: { peakL: 0.0, peakR: 0.0 },
		};
		const { container } = render(AudioMixer, { props: { state, sourceLevels } });
		const strips = container.querySelectorAll('.channel-strip');
		// cam1 (first sorted) should have a non-zero L fill and zero R fill
		const cam1Fills = strips[0].querySelectorAll('.peak-fill');
		expect(cam1Fills.length).toBe(2);
		const lHeight = (cam1Fills[0] as HTMLElement).style.height;
		const rHeight = (cam1Fills[1] as HTMLElement).style.height;
		// peakL=0.5 => ~-6dB => ~75% ; peakR=0 => 0%
		expect(parseFloat(lHeight)).toBeGreaterThan(0);
		expect(rHeight).toBe('0%');
	});

	it('should render dB scale markings on channel strip meters', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const strips = container.querySelectorAll('.channel-strip');
		// Each channel strip meter area should contain dB scale markings
		strips.forEach((strip) => {
			const scaleMarks = strip.querySelectorAll('.db-mark');
			expect(scaleMarks.length).toBeGreaterThanOrEqual(4);
		});
		// Verify the expected dB values are rendered as labels
		const firstStrip = strips[0];
		const labels = Array.from(firstStrip.querySelectorAll('.db-label')).map(el => el.textContent);
		expect(labels).toContain('-6');
		expect(labels).toContain('-12');
		expect(labels).toContain('-24');
		expect(labels).toContain('-48');
	});

	it('should still render L/R bars on the master meter', () => {
		const programLevels = { peakL: 0.7, peakR: 0.4 };
		const { container } = render(AudioMixer, { props: { state, programLevels } });
		const master = container.querySelector('.master-strip');
		const peakBars = master!.querySelectorAll('.peak-bar');
		expect(peakBars.length).toBe(2);
		expect(peakBars[0].classList.contains('left')).toBe(true);
		expect(peakBars[1].classList.contains('right')).toBe(true);
	});
});
