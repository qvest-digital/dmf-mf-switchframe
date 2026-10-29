import { describe, it, expect, vi, afterEach } from 'vitest';
import { hiddenForMs, holdReload, idleForMs, reloadHolds, reloadWouldLoseWork, trackChange } from './activity';

describe('activity', () => {
	afterEach(() => vi.useRealTimers());

	it('counts a change until each of its own ends, once', () => {
		const a = trackChange();
		const b = trackChange();
		a();
		a();
		expect(reloadWouldLoseWork()).toBe(true);
		b();
		expect(reloadWouldLoseWork()).toBe(false);
	});

	it('holds a reload until every hold is released', () => {
		const edit = holdReload('unsaved playlist');
		const comms = holdReload('comms');
		expect(reloadHolds().sort()).toEqual(['comms', 'unsaved playlist']);
		edit();
		edit();
		expect(reloadWouldLoseWork()).toBe(true);
		comms();
		expect(reloadWouldLoseWork()).toBe(false);
	});

	it('restarts the idle time on operator input', () => {
		window.dispatchEvent(new Event('keydown'));
		expect(idleForMs()).toBeLessThan(1000);
	});

	// Sampling document.hidden once a minute counted a tab hidden at each
	// sample as hidden throughout.
	it('measures hidden time from the visibilitychange, and resets when shown', () => {
		vi.useFakeTimers();
		let hidden = false;
		Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
		const change = () => document.dispatchEvent(new Event('visibilitychange'));
		try {
			expect(hiddenForMs()).toBe(0);
			hidden = true;
			change();
			vi.advanceTimersByTime(4 * 60_000);
			expect(hiddenForMs()).toBe(4 * 60_000);
			hidden = false;
			change();
			expect(hiddenForMs()).toBe(0);
			hidden = true;
			change();
			vi.advanceTimersByTime(1000);
			expect(hiddenForMs()).toBe(1000);
		} finally {
			hidden = false;
			change();
			delete (document as unknown as Record<string, unknown>).hidden;
		}
	});
});
