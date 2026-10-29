import { describe, it, expect, beforeEach, vi } from 'vitest';

// The monitor's state lives in the module; each test loads it anew.
async function load() {
	vi.resetModules();
	return import('./monitor.svelte');
}

describe('local monitor', () => {
	beforeEach(() => localStorage.clear());

	it('volume and mute set the monitor gain; mute silences without losing the volume', async () => {
		const m = await load();
		expect(m.monitorGain()).toBe(1);
		m.setMonitorVolume(0.5);
		expect(m.monitorGain()).toBe(0.5);
		m.setMonitorMuted(true);
		expect(m.monitorGain()).toBe(0);
		m.setMonitorMuted(false);
		expect(m.monitorGain()).toBe(0.5);
	});

	it('DIM and incoming comms duck the monitor', async () => {
		const m = await load();
		m.setMonitorDimmed(true);
		expect(m.monitorGain()).toBe(m.DUCK_GAIN);
		m.setMonitorDimmed(false);
		m.setMonitorCommsActive(true);
		expect(m.monitorGain()).toBe(m.DUCK_GAIN);
	});

	it('a solo takes program off the monitor only', async () => {
		const m = await load();
		m.setMonitorSolo(true);
		expect(m.programGain()).toBe(0);
		expect(m.monitorGain()).toBe(1);
		m.setMonitorSolo(false);
		expect(m.programGain()).toBe(1);
	});

	it('clamps the volume to 0..1', async () => {
		const m = await load();
		m.setMonitorVolume(3);
		expect(m.monitor.volume).toBe(1);
		m.setMonitorVolume(-1);
		expect(m.monitor.volume).toBe(0);
	});

	it('remembers volume and mute across reloads, not DIM or solo', async () => {
		let m = await load();
		m.setMonitorVolume(0.3);
		m.setMonitorMuted(true);
		m.setMonitorDimmed(true);
		m.setMonitorSolo(true);
		m = await load();
		expect(m.monitor.volume).toBe(0.3);
		expect(m.monitor.muted).toBe(true);
		expect(m.monitor.dimmed).toBe(false);
		expect(m.monitor.solo).toBe(false);
	});

	it('notifies subscribers of every change until unsubscribed', async () => {
		const m = await load();
		const fn = vi.fn();
		const off = m.subscribeMonitor(fn);
		m.setMonitorMuted(true);
		m.setMonitorSolo(true);
		expect(fn).toHaveBeenCalledTimes(2);
		off();
		m.setMonitorMuted(false);
		expect(fn).toHaveBeenCalledTimes(2);
	});
});
