import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import AudioMixer from './AudioMixer.svelte';
import type { EQBand, CompressorSettings } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	setLevel: vi.fn().mockResolvedValue({}),
	setTrim: vi.fn().mockResolvedValue({}),
	setMute: vi.fn().mockResolvedValue({}),
	setAFV: vi.fn().mockResolvedValue({}),
	setMasterLevel: vi.fn().mockResolvedValue({}),
	setEQ: vi.fn().mockResolvedValue({}),
	setCompressor: vi.fn().mockResolvedValue({}),
	setSourceDelay: vi.fn().mockResolvedValue({}),
}));

const defaultEQ: [EQBand, EQBand, EQBand] = [
	{ frequency: 250, gain: 0, q: 1.0, enabled: false },
	{ frequency: 1000, gain: 0, q: 1.0, enabled: false },
	{ frequency: 4000, gain: 0, q: 1.0, enabled: false },
];

const defaultCompressor: CompressorSettings = {
	threshold: 0,
	ratio: 1.0,
	attack: 5.0,
	release: 100.0,
	makeupGain: 0,
};

describe('AudioMixer', () => {
	const state = {
		programSource: 'cam1',
		previewSource: 'cam2',
		transitionType: 'cut',
		transitionDurationMs: 0,
		transitionPosition: 0,
		inTransition: false,
		ftbActive: false,
		audioChannels: {
			cam1: { level: 0, trim: 0, muted: false, afv: true, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0 },
			cam2: { level: -6, trim: -3, muted: true, afv: false, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0 },
		},
		masterLevel: 0,
		programPeak: [-12, -14] as [number, number],
		tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const },
			cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const },
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

	it('should render trim slider per channel strip', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const trimKnobs = container.querySelectorAll('.channel-strip .trim-knob');
		expect(trimKnobs.length).toBe(2);
		// Verify trim knob attributes
		const firstTrim = trimKnobs[0] as HTMLInputElement;
		expect(firstTrim.min).toBe('-20');
		expect(firstTrim.max).toBe('20');
		expect(firstTrim.getAttribute('aria-label')).toBe('Trim for Camera 1');
	});

	it('should display trim value for each channel', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const trimValues = container.querySelectorAll('.channel-strip .trim-value');
		expect(trimValues.length).toBe(2);
		// cam1 trim=0, cam2 trim=-3 (sorted order: cam1, cam2)
		expect(trimValues[0].textContent).toBe('0.0');
		expect(trimValues[1].textContent).toBe('-3.0');
	});

	it('should use server-side per-channel peaks when available', () => {
		const stateWithPeaks = {
			...state,
			audioChannels: {
				cam1: { level: 0, trim: 0, muted: false, afv: true, peakL: -12, peakR: -18, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0 },
				cam2: { level: -6, trim: 0, muted: true, afv: false, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0 },
			},
		};
		const { container } = render(AudioMixer, { props: { state: stateWithPeaks } });
		const strips = container.querySelectorAll('.channel-strip');

		// cam1 has server peaks > -96, so meter should use them (non-zero height)
		const cam1Fills = strips[0].querySelectorAll('.peak-fill');
		const lHeight = parseFloat((cam1Fills[0] as HTMLElement).style.height);
		expect(lHeight).toBeGreaterThan(0);

		// cam2 has peaks at -96 (floor), meter should fall back to client-side (0%)
		const cam2Fills = strips[1].querySelectorAll('.peak-fill');
		const cam2LHeight = parseFloat((cam2Fills[0] as HTMLElement).style.height);
		expect(cam2LHeight).toBe(0);
	});

	it('should render EQ toggle button per channel strip', () => {
		const { container } = render(AudioMixer, { props: { state } });
		const eqButtons = container.querySelectorAll('.eq-toggle-btn');
		expect(eqButtons.length).toBe(2);
	});

	it('should show EQ section when expandedKeys has channel', () => {
		const { container } = render(AudioMixer, { props: { state, expandedKeys: { cam1: true } } });

		// eq-comp-section should be visible for cam1
		const section = container.querySelector('.eq-comp-section');
		expect(section).toBeTruthy();
	});

	it('should render 3 EQ bands in the expanded section', () => {
		const { container } = render(AudioMixer, { props: { state, expandedKeys: { cam1: true } } });

		const bands = container.querySelectorAll('.eq-band');
		expect(bands.length).toBe(3);
	});

	it('should render compressor section with GR meter in expanded section', () => {
		const { container } = render(AudioMixer, { props: { state, expandedKeys: { cam1: true } } });

		const compSection = container.querySelector('.comp-section');
		expect(compSection).toBeTruthy();

		const grMeter = container.querySelector('.gr-meter');
		expect(grMeter).toBeTruthy();
	});

	it('should call onExpandToggle when EQ button is clicked', async () => {
		const onExpandToggle = vi.fn();
		const { container } = render(AudioMixer, { props: { state, onExpandToggle } });
		const eqButtons = container.querySelectorAll('.eq-toggle-btn');
		await fireEvent.click(eqButtons[0]);
		expect(onExpandToggle).toHaveBeenCalledWith('cam1');
	});

	it('renders compressor ON toggle when expanded', () => {
		const { container } = render(AudioMixer, { props: { state, expandedKeys: { cam1: true } } });

		const compSection = container.querySelector('.comp-section');
		expect(compSection).toBeTruthy();

		const bypassBtn = compSection!.querySelector('button[aria-label]');
		expect(bypassBtn).toBeTruthy();
		const label = bypassBtn!.getAttribute('aria-label')!.toLowerCase();
		expect(label).toContain('compressor');
		expect(label).toMatch(/on|off/);
	});

	it('renders delay slider when expanded', () => {
		const stateWithDelay = {
			...state,
			sources: {
				...state.sources,
				cam1: { ...state.sources.cam1, delayMs: 100 },
			},
		};
		const { container } = render(AudioMixer, { props: { state: stateWithDelay, expandedKeys: { cam1: true } } });

		const delaySlider = container.querySelector('input[aria-label="Source delay"]') as HTMLInputElement;
		expect(delaySlider).toBeTruthy();
		expect(delaySlider.type).toBe('range');
		expect(delaySlider.min).toBe('0');
		expect(delaySlider.max).toBe('500');
		expect(delaySlider.value).toBe('100');
	});

	it('shows delay value text when expanded', () => {
		const stateWithDelay = {
			...state,
			sources: {
				...state.sources,
				cam1: { ...state.sources.cam1, delayMs: 42 },
			},
		};
		const { container } = render(AudioMixer, { props: { state: stateWithDelay, expandedKeys: { cam1: true } } });

		const delaySection = container.querySelector('.delay-section');
		expect(delaySection).toBeTruthy();
		expect(delaySection!.textContent).toContain('42ms');
	});

	it('dims compressor controls when bypassed', async () => {
		const { container } = render(AudioMixer, { props: { state, expandedKeys: { cam1: true } } });

		const compSection = container.querySelector('.comp-section');
		expect(compSection).toBeTruthy();

		// Should not start bypassed
		expect(compSection!.classList.contains('comp-bypassed')).toBe(false);

		// Click the bypass toggle
		const bypassBtn = compSection!.querySelector('button[aria-label]') as HTMLElement;
		expect(bypassBtn).toBeTruthy();
		await fireEvent.click(bypassBtn);

		// After click, comp-section should have comp-bypassed class
		expect(compSection!.classList.contains('comp-bypassed')).toBe(true);
	});

	describe('ARIA labels', () => {
		it('should have aria-label on each channel fader with source label', () => {
			const { container } = render(AudioMixer, { props: { state } });
			const faders = container.querySelectorAll('.channel-strip .fader');
			expect(faders.length).toBe(2);

			const labels = Array.from(faders).map(f => f.getAttribute('aria-label'));
			expect(labels).toContain('Volume for Camera 1');
			expect(labels).toContain('Volume for Camera 2');
		});

		it('should have aria-label on master fader', () => {
			const { container } = render(AudioMixer, { props: { state } });
			const masterFader = container.querySelector('.master-strip .fader') as HTMLInputElement;
			expect(masterFader.getAttribute('aria-label')).toBe('Master volume');
		});

		it('should have aria-hidden on channel meter wrappers', () => {
			const { container } = render(AudioMixer, { props: { state } });
			const meters = container.querySelectorAll('.channel-strip .meter-wrapper');
			for (const meter of meters) {
				expect(meter.getAttribute('aria-hidden')).toBe('true');
			}
		});

		it('should have aria-hidden on master meter wrapper', () => {
			const { container } = render(AudioMixer, { props: { state } });
			const meter = container.querySelector('.master-strip .meter-wrapper');
			expect(meter?.getAttribute('aria-hidden')).toBe('true');
		});

		it('should use source key as fallback label when no source label is set', () => {
			const stateNoLabels = {
				...state,
				sources: {
					cam1: { key: 'cam1', label: '', status: 'healthy' as const },
				},
				audioChannels: {
					cam1: { level: 0, trim: 0, muted: false, afv: false, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0 },
				},
				tallyState: { cam1: 'program' as const },
			};
			const { container } = render(AudioMixer, { props: { state: stateNoLabels } });
			const fader = container.querySelector('.channel-strip .fader') as HTMLInputElement;
			expect(fader.getAttribute('aria-label')).toBe('Volume for cam1');
		});
	});
});
