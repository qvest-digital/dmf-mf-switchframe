import fs from 'fs';
import path from 'path';
import { describe, it, expect } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import SRTOutputModal from './SRTOutputModal.svelte';

const baseState = {
	programSource: 'cam1',
	previewSource: 'cam2',
	transitionType: 'cut',
	transitionDurationMs: 0,
	transitionPosition: 0,
	inTransition: false,
	ftbActive: false,
	audioChannels: undefined,
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

	it('should disable Start button when caller mode and address is empty', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const startBtn = container.querySelector('.start-btn') as HTMLButtonElement;
		expect(startBtn.disabled).toBe(true);
	});

	it('should enable Start button in listener mode even without address', async () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		// Switch to listener mode
		const listenerRadio = container.querySelector('input[value="listener"]') as HTMLInputElement;
		await fireEvent.click(listenerRadio);
		const startBtn = container.querySelector('.start-btn') as HTMLButtonElement;
		expect(startBtn.disabled).toBe(false);
	});

	it('should show confirmation dialog when stop is clicked', async () => {
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
		const stopBtn = container.querySelector('.stop-btn') as HTMLButtonElement;
		await fireEvent.click(stopBtn);
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeTruthy();
		expect(container.textContent).toContain('Disconnect SRT output?');
	});

	it('should dismiss SRT confirmation dialog on cancel', async () => {
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
		const stopBtn = container.querySelector('.stop-btn') as HTMLButtonElement;
		await fireEvent.click(stopBtn);
		const cancelBtn = container.querySelector('.cancel-btn') as HTMLButtonElement;
		await fireEvent.click(cancelBtn);
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeFalsy();
	});

	it('should have role="dialog" when visible', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const dialog = container.querySelector('[role="dialog"]');
		expect(dialog).toBeTruthy();
	});

	it('should have aria-modal="true" when visible', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const dialog = container.querySelector('[role="dialog"]');
		expect(dialog?.getAttribute('aria-modal')).toBe('true');
	});

	it('should have aria-labelledby pointing to the title element', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const dialog = container.querySelector('[role="dialog"]');
		const labelledBy = dialog?.getAttribute('aria-labelledby');
		expect(labelledBy).toBe('srt-modal-title');
		const title = container.querySelector(`#${labelledBy}`);
		expect(title).toBeTruthy();
		expect(title?.textContent).toBe('SRT Output');
	});

	it('should close on Escape key', async () => {
		let closed = false;
		const { container } = render(SRTOutputModal, {
			props: { state: baseState, visible: true, onclose: () => { closed = true; } },
		});
		const dialog = container.querySelector('[role="dialog"]') as HTMLElement;
		await fireEvent.keyDown(dialog, { key: 'Escape' });
		expect(closed).toBe(true);
	});

	it('should have role="presentation" on the backdrop', () => {
		const { container } = render(SRTOutputModal, { props: { state: baseState, visible: true } });
		const backdrop = container.querySelector('.srt-modal-backdrop');
		expect(backdrop?.getAttribute('role')).toBe('presentation');
	});

	it('should not contain svelte-ignore a11y comments in source', () => {
		const source = fs.readFileSync(
			path.resolve(__dirname, 'SRTOutputModal.svelte'),
			'utf-8',
		);
		expect(source).not.toContain('svelte-ignore a11y');
	});
});
