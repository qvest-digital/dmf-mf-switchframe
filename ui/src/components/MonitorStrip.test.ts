import { describe, it, expect, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import MonitorStrip from './MonitorStrip.svelte';
import { monitor, monitorGain, setMonitorMuted, setMonitorVolume } from '$lib/audio/monitor.svelte';

describe('MonitorStrip', () => {
	afterEach(() => {
		setMonitorMuted(false);
		setMonitorVolume(1);
	});

	it('MUTE silences the local monitor and toggles back', async () => {
		const { container } = render(MonitorStrip);
		const btn = container.querySelector('.monitor-mute-btn') as HTMLButtonElement;
		await fireEvent.click(btn);
		expect(monitor.muted).toBe(true);
		expect(monitorGain()).toBe(0);
		expect(btn.getAttribute('aria-pressed')).toBe('true');
		await fireEvent.click(btn);
		expect(monitor.muted).toBe(false);
		expect(monitorGain()).toBe(1);
	});

	it('the fader sets the monitor volume in dB, the bottom being silence', async () => {
		const { container } = render(MonitorStrip);
		const fader = container.querySelector('input[aria-label="Monitor volume"]') as HTMLInputElement;
		await fireEvent.input(fader, { target: { value: '-6' } });
		expect(monitor.volume).toBeCloseTo(0.501, 2);
		await fireEvent.input(fader, { target: { value: '-60' } });
		expect(monitor.volume).toBe(0);
		expect(container.textContent).toContain('-inf');
	});

	it('names the soloed channel, else program', () => {
		const solo = render(MonitorStrip, { props: { soloSource: 'CAM 1', soloMode: 'afl' } });
		expect(solo.container.querySelector('[data-testid="monitor-solo"]')?.textContent).toBe('AFL CAM 1');
		solo.unmount();
		const idle = render(MonitorStrip);
		expect(idle.container.textContent).toContain('PGM');
	});
});
