/**
 * The local audio monitor: what this browser plays on its own speakers.
 * Nothing here reaches the program mix, which the engine makes; muting or
 * turning the monitor down changes only what this operator hears.
 *
 * Every media pipeline routes its decoders through two gain stages that
 * follow this state: a program bus, silenced while a channel is soloed with
 * PFL or AFL so the operator hears that channel alone, and the monitor bus
 * after it, which carries volume, mute, DIM and the comms duck.
 */

/** Gain while DIM is on or comms audio arrives (about -20 dB). */
export const DUCK_GAIN = 0.1;

const STORAGE_KEY = 'switchframe_monitor';

interface MonitorState {
	/** Monitor volume, linear 0..1. */
	volume: number;
	muted: boolean;
	dimmed: boolean;
	commsActive: boolean;
	/** A channel is soloed with PFL or AFL: program leaves the monitor. */
	solo: boolean;
}

function loadStored(): Pick<MonitorState, 'volume' | 'muted'> {
	try {
		const raw = localStorage.getItem(STORAGE_KEY);
		if (raw) {
			const v = JSON.parse(raw);
			const volume = typeof v.volume === 'number' && v.volume >= 0 && v.volume <= 1 ? v.volume : 1;
			return { volume, muted: v.muted === true };
		}
	} catch {
		// Storage unavailable or unreadable: start at full volume, unmuted.
	}
	return { volume: 1, muted: false };
}

const state = $state<MonitorState>({ ...loadStored(), dimmed: false, commsActive: false, solo: false });
const listeners = new Set<() => void>();

function changed(): void {
	try {
		localStorage.setItem(STORAGE_KEY, JSON.stringify({ volume: state.volume, muted: state.muted }));
	} catch {
		// Not remembered across reloads; the monitor still works.
	}
	for (const fn of listeners) fn();
}

/** The monitor state, reactive for components. */
export const monitor: Readonly<MonitorState> = state;

export function setMonitorVolume(volume: number): void {
	state.volume = Math.min(1, Math.max(0, volume));
	changed();
}

export function setMonitorMuted(muted: boolean): void {
	state.muted = muted;
	changed();
}

export function setMonitorDimmed(dimmed: boolean): void {
	state.dimmed = dimmed;
	changed();
}

/** Comms audio is arriving: duck the monitor so the talkback is heard. */
export function setMonitorCommsActive(active: boolean): void {
	state.commsActive = active;
	changed();
}

export function setMonitorSolo(solo: boolean): void {
	state.solo = solo;
	changed();
}

/** Gain of the monitor bus: volume, mute, DIM and the comms duck. */
export function monitorGain(): number {
	if (state.muted) return 0;
	return state.volume * (state.dimmed || state.commsActive ? DUCK_GAIN : 1);
}

/** Gain of the program bus: off while a channel is soloed. */
export function programGain(): number {
	return state.solo ? 0 : 1;
}

/** How many subscribers are attached; a destroyed pipeline leaves none. */
export function monitorSubscriberCount(): number {
	return listeners.size;
}

/** Calls fn after every change; returns the unsubscribe. */
export function subscribeMonitor(fn: () => void): () => void {
	listeners.add(fn);
	return () => listeners.delete(fn);
}
