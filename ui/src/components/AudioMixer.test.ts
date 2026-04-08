import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import AudioMixer from './AudioMixer.svelte';
import type { EQBand, CompressorSettings, GateSettings } from '$lib/api/types';

vi.mock('$lib/api/switch-api', () => ({
	setLevel: vi.fn().mockResolvedValue({}),
	setTrim: vi.fn().mockResolvedValue({}),
	setMute: vi.fn().mockResolvedValue({}),
	setAFV: vi.fn().mockResolvedValue({}),
	setMasterLevel: vi.fn().mockResolvedValue({}),
	setEQ: vi.fn().mockResolvedValue({}),
	setCompressor: vi.fn().mockResolvedValue({}),
	setSourceDelay: vi.fn().mockResolvedValue({}),
	setAudioDelay: vi.fn().mockResolvedValue({}),
	setGate: vi.fn().mockResolvedValue({}),
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

const defaultGate: GateSettings = {
	threshold: -40,
	range: -60,
	attack: 0.5,
	release: 100,
	hold: 50,
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
			cam1: { level: 0, trim: 0, muted: false, afv: true, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0, gate: defaultGate },
			cam2: { level: -6, trim: -3, muted: true, afv: false, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0, gate: defaultGate },
		},
		masterLevel: 0,
		programPeak: [-12, -14] as [number, number],
		tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
		sources: {
			cam1: { key: 'cam1', label: 'Camera 1', type: 'demo' as const, status: 'healthy' as const },
			cam2: { key: 'cam2', label: 'Camera 2', type: 'demo' as const, status: 'healthy' as const },
		},
		seq: 1,
		lastCommandSeq: 0,
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
		const { container } = render(AudioMixer, { props: { state, pflActiveSource: 'cam1', pflMode: 'pfl' as const } });
		const pflButtons = container.querySelectorAll('.pfl-btn');
		// cam1 is first (sorted), cam2 is second
		expect(pflButtons[0].classList.contains('pfl-active')).toBe(true);
		expect(pflButtons[1].classList.contains('pfl-active')).toBe(false);
	});

	it('should show AFL active state for matching source', () => {
		const { container } = render(AudioMixer, { props: { state, pflActiveSource: 'cam1', pflMode: 'afl' as const } });
		const pflButtons = container.querySelectorAll('.pfl-btn');
		expect(pflButtons[0].classList.contains('afl-active')).toBe(true);
		expect(pflButtons[0].textContent?.trim()).toBe('AFL');
		expect(pflButtons[1].classList.contains('afl-active')).toBe(false);
	});

	it('should not show any PFL active when pflActiveSource is null', () => {
		const { container } = render(AudioMixer, { props: { state, pflActiveSource: null } });
		const pflButtons = container.querySelectorAll('.pfl-btn');
		expect(pflButtons[0].classList.contains('pfl-active')).toBe(false);
		expect(pflButtons[0].classList.contains('afl-active')).toBe(false);
		expect(pflButtons[1].classList.contains('pfl-active')).toBe(false);
		expect(pflButtons[1].classList.contains('afl-active')).toBe(false);
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
				cam1: { level: 0, trim: 0, muted: false, afv: true, peakL: -12, peakR: -18, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0, gate: defaultGate },
				cam2: { level: -6, trim: 0, muted: true, afv: false, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0, gate: defaultGate },
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

	it('should show channel-detail when selectedChannel is set', () => {
		const { container } = render(AudioMixer, { props: { state, selectedChannel: 'cam1' } });

		const detail = container.querySelector('.channel-detail');
		expect(detail).toBeTruthy();
		expect(detail!.textContent).toContain('Camera 1');
	});

	it('should not show channel-detail when selectedChannel is null', () => {
		const { container } = render(AudioMixer, { props: { state, selectedChannel: null } });

		const detail = container.querySelector('.channel-detail');
		expect(detail).toBeFalsy();
	});

	it('should call onChannelSelect when EQ button is clicked', async () => {
		const onChannelSelect = vi.fn();
		const { container } = render(AudioMixer, { props: { state, onChannelSelect } });
		const eqButtons = container.querySelectorAll('.eq-toggle-btn');
		await fireEvent.click(eqButtons[0]);
		expect(onChannelSelect).toHaveBeenCalledWith('cam1');
	});

	it('should call onChannelSelect with null to deselect when clicking active EQ button', async () => {
		const onChannelSelect = vi.fn();
		const { container } = render(AudioMixer, { props: { state, selectedChannel: 'cam1', onChannelSelect } });
		const eqButtons = container.querySelectorAll('.eq-toggle-btn');
		await fireEvent.click(eqButtons[0]);
		expect(onChannelSelect).toHaveBeenCalledWith(null);
	});

	it('highlights EQ button for selected channel', () => {
		const { container } = render(AudioMixer, { props: { state, selectedChannel: 'cam1' } });

		const eqButtons = container.querySelectorAll('.eq-toggle-btn');
		expect(eqButtons[0].classList.contains('active')).toBe(true);
		expect(eqButtons[1].classList.contains('active')).toBe(false);
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
					cam1: { key: 'cam1', label: '', type: 'demo' as const, status: 'healthy' as const },
				},
				audioChannels: {
					cam1: { level: 0, trim: 0, muted: false, afv: false, peakL: -96, peakR: -96, eq: defaultEQ, compressor: defaultCompressor, gainReduction: 0, gate: defaultGate },
				},
				tallyState: { cam1: 'program' as const },
			};
			const { container } = render(AudioMixer, { props: { state: stateNoLabels } });
			const fader = container.querySelector('.channel-strip .fader') as HTMLInputElement;
			expect(fader.getAttribute('aria-label')).toBe('Volume for cam1');
		});
	});

	describe('audio drift badge', () => {
		it('should show drift badge when audioDrift prop is present', () => {
			const { container } = render(AudioMixer, {
				props: { state, audioDrift: { channel: 'cam1', reason: 'config' } },
			});
			const badge = container.querySelector('.drift-badge');
			expect(badge).toBeTruthy();
			expect(badge!.textContent).toContain('drift');
		});

		it('should hide drift badge when audioDrift is null', () => {
			const { container } = render(AudioMixer, {
				props: { state, audioDrift: null },
			});
			const badge = container.querySelector('.drift-badge');
			expect(badge).toBeFalsy();
		});

		it('should show correct tooltip text on drift badge', () => {
			const { container } = render(AudioMixer, {
				props: { state, audioDrift: { channel: 'cam1', reason: 'config' } },
			});
			const badge = container.querySelector('.drift-badge');
			expect(badge).toBeTruthy();
			expect(badge!.getAttribute('title')).toBe(
				'Engine A and Engine B audio config differs on cam1',
			);
		});
	});
});
