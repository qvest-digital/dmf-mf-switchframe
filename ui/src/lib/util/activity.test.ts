import { describe, it, expect } from 'vitest';
import { changesInFlight, idleForMs, trackChange } from './activity';

describe('activity', () => {
	it('counts a change until each of its own ends, once', () => {
		const a = trackChange();
		const b = trackChange();
		a();
		a();
		expect(changesInFlight()).toBe(true);
		b();
		expect(changesInFlight()).toBe(false);
	});

	it('restarts the idle time on operator input', () => {
		window.dispatchEvent(new Event('keydown'));
		expect(idleForMs()).toBeLessThan(1000);
	});
});
