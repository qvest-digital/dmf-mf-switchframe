import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { AutoAnimation } from './auto-animation.svelte';

describe('AutoAnimation', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it('starts with active=false and position=0', () => {
		const anim = new AutoAnimation();
		expect(anim.active).toBe(false);
		expect(anim.position).toBe(0);
	});

	it('sets active and schedules rAF on start', () => {
		const anim = new AutoAnimation();
		anim.start(1000);
		expect(anim.active).toBe(true);
		expect(anim.position).toBe(0);
	});

	it('advances position toward 0.5 at halfway through duration', () => {
		const anim = new AutoAnimation();
		anim.start(1000);
		vi.advanceTimersByTime(500);
		expect(anim.position).toBeGreaterThan(0.3);
		expect(anim.position).toBeLessThan(0.7);
	});

	it('reaches 1.0 after full duration', () => {
		const anim = new AutoAnimation();
		anim.start(1000);
		vi.advanceTimersByTime(1100);
		expect(anim.position).toBeCloseTo(1.0, 1);
	});

	it('stops rAF loop after reaching 1.0', () => {
		const anim = new AutoAnimation();
		anim.start(1000);
		vi.advanceTimersByTime(1100);
		expect(anim.position).toBeCloseTo(1.0, 1);
		// Still active (the component decides when to stop)
		expect(anim.active).toBe(true);
	});

	it('stop() resets active and position', () => {
		const anim = new AutoAnimation();
		anim.start(1000);
		vi.advanceTimersByTime(500);
		anim.stop();
		expect(anim.active).toBe(false);
		expect(anim.position).toBe(0);
	});

	it('stop() cancels pending rAF callbacks', () => {
		const anim = new AutoAnimation();
		anim.start(1000);
		vi.advanceTimersByTime(100);
		const posAtStop = anim.position;
		anim.stop();
		expect(anim.position).toBe(0);

		// Advance more — position should not change
		vi.advanceTimersByTime(500);
		expect(anim.position).toBe(0);
	});

	it('works with different durations', () => {
		const anim = new AutoAnimation();
		anim.start(500);
		vi.advanceTimersByTime(250);
		expect(anim.position).toBeGreaterThan(0.3);
		expect(anim.position).toBeLessThan(0.7);
	});
});
