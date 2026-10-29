/**
 * Sends only the latest value of a continuously changing control, such as a
 * fader being dragged.
 *
 * Calls are grouped by key (a channel, a slot). Per key, at most one request
 * is in flight and requests start at least `minIntervalMs` apart. A call
 * made meanwhile replaces any earlier one still waiting, so the last value
 * always goes out once the request before it settles, and a value is never
 * overtaken by an older one. Keys do not wait on each other: moving a
 * second channel never drops the first one's final value.
 *
 * Unlike a plain throttle, a slow engine slows the requests down instead of
 * having them pile up behind it. A request still unanswered after
 * `STALL_MS` no longer holds the next one back, so one lost request cannot
 * freeze the control. When such an overtaken request settles at last, the
 * newest value is sent once more: the engine may have applied the old one
 * after the new one, and aborting it on this side would not stop that.
 */
export const STALL_MS = 2000;

export function coalesce<A extends unknown[]>(
	send: (...args: A) => Promise<unknown> | void,
	minIntervalMs: number,
	keyOf: (...args: A) => string = () => '',
): (...args: A) => void {
	interface Slot {
		busy: boolean;
		gen: number;
		/** Arguments of the newest request started. */
		newest: A | null;
		lastStart: number;
		pending: A | null;
		timer: ReturnType<typeof setTimeout> | null;
	}
	const slots = new Map<string, Slot>();

	function start(slot: Slot, args: A) {
		slot.busy = true;
		slot.lastStart = Date.now();
		slot.newest = args;
		const gen = ++slot.gen;
		const settle = () => {
			if (slot.gen !== gen || !slot.busy) return;
			slot.busy = false;
			pump(slot);
		};
		const stall = setTimeout(settle, STALL_MS);
		let p: Promise<unknown> | void;
		try {
			p = send(...args);
		} catch {
			p = undefined;
		}
		Promise.resolve(p)
			.catch(() => {})
			.finally(() => {
				clearTimeout(stall);
				if (slot.gen !== gen && !slot.pending && slot.newest) {
					// Overtaken: put the newest value back on top.
					slot.pending = slot.newest;
					pump(slot);
					return;
				}
				settle();
			});
	}

	function pump(slot: Slot) {
		if (slot.busy || slot.timer || !slot.pending) return;
		const wait = slot.lastStart + minIntervalMs - Date.now();
		if (wait > 0) {
			slot.timer = setTimeout(() => {
				slot.timer = null;
				pump(slot);
			}, wait);
			return;
		}
		const args = slot.pending;
		slot.pending = null;
		start(slot, args);
	}

	return (...args: A) => {
		const key = keyOf(...args);
		let slot = slots.get(key);
		if (!slot) {
			slot = { busy: false, gen: 0, newest: null, lastStart: 0, pending: null, timer: null };
			slots.set(key, slot);
		}
		slot.pending = args;
		pump(slot);
	};
}
