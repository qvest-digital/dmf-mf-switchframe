import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import SRTOutputModal from './SRTOutputModal.svelte';

const baseState = {
	programSource: 'cam1',
	previewSource: 'cam2',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioLevels: null,
	audioChannels: null,
	masterLevel: 0,
	programPeak: [0, 0] as [number, number],
	tallyState: { cam1: 'program' as const, cam2: 'preview' as const },
	sources: {
		cam1: { key: 'cam1', label: 'Camera 1', status: 'healthy' as const, lastFrameTime: 0 },
		cam2: { key: 'cam2', label: 'Camera 2', status: 'healthy' as const, lastFrameTime: 0 },
	},
	seq: 1,
	timestamp: Date.now(),
};

describe('SRTOutputModal', () => {
	it('should render Caller and Listener text when visible', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		expect(container.textContent).toContain('Caller');
		expect(container.textContent).toContain('Listener');
	});

	it('should show address input field', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const addressInput = container.querySelector('input[name="address"]');
		expect(addressInput).toBeTruthy();
	});

	it('should show port input field', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const portInput = container.querySelector('input[name="port"]');
		expect(portInput).toBeTruthy();
	});

	it('should show Start button when not active', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		expect(container.textContent).toContain('Start');
	});

	it('should show Stop button when active', () => {
		const state = {
			...baseState,
			srtOutput: {
				active: true,
				mode: 'caller' as const,
				address: '192.168.1.1',
				port: 9000,
				state: 'connected',
				connections: 0,
				bytesWritten: 1024,
			},
		};
		const { container } = render(SRTOutputModal, { props: { state, visible: true } });
		expect(container.textContent).toContain('Stop');
	});

	it('should show connection count for listener mode', () => {
		const state = {
			...baseState,
			srtOutput: {
				active: true,
				mode: 'listener' as const,
				port: 9000,
				state: 'listening',
				connections: 3,
				bytesWritten: 2048,
			},
		};
		const { container } = render(SRTOutputModal, { props: { state, visible: true } });
		expect(container.textContent).toContain('3');
	});

	it('should NOT render .srt-modal when visible=false', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: false } });
		const modal = container.querySelector('.srt-modal');
		expect(modal).toBeFalsy();
	});

	it('should render .srt-modal when visible=true', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const modal = container.querySelector('.srt-modal');
		expect(modal).toBeTruthy();
	});
});
