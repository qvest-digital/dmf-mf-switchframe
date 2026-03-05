import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { throttle } from './throttle';

describe('throttle', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('should call the function immediately on first invocation', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		throttled('a');

		expect(fn).toHaveBeenCalledTimes(1);
		expect(fn).toHaveBeenCalledWith('a');
	});

	it('should not call again within the throttle window', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		throttled('a');
		throttled('b');
		throttled('c');

		// Only the first call should have fired immediately
		expect(fn).toHaveBeenCalledTimes(1);
		expect(fn).toHaveBeenCalledWith('a');
	});

	it('should schedule the last call within the throttle window (trailing edge)', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		throttled('a');  // fires immediately
		throttled('b');  // queued
		throttled('c');  // replaces queued

		expect(fn).toHaveBeenCalledTimes(1);

		vi.advanceTimersByTime(50);

		// Trailing edge should fire with the last value
		expect(fn).toHaveBeenCalledTimes(2);
		expect(fn).toHaveBeenLastCalledWith('c');
	});

	it('should allow another call after the throttle window expires', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		throttled('a');  // fires immediately
		vi.advanceTimersByTime(50);

		throttled('b');  // fires immediately (window expired)
		expect(fn).toHaveBeenCalledTimes(2);
		expect(fn).toHaveBeenLastCalledWith('b');
	});

	it('should pass through multiple arguments', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		throttled('source1', 42);

		expect(fn).toHaveBeenCalledWith('source1', 42);
	});

	it('should ensure final value is always sent (trailing edge guarantee)', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		// Simulate rapid fader drag
		throttled(0);     // fires immediately
		throttled(0.1);   // queued
		throttled(0.2);   // replaces queued
		throttled(0.3);   // replaces queued
		throttled(0.5);   // replaces queued
		throttled(0.8);   // replaces queued
		throttled(1.0);   // replaces queued (final position)

		expect(fn).toHaveBeenCalledTimes(1);
		expect(fn).toHaveBeenCalledWith(0);

		vi.advanceTimersByTime(50);

		// The final value (1.0) must be sent
		expect(fn).toHaveBeenCalledTimes(2);
		expect(fn).toHaveBeenLastCalledWith(1.0);
	});

	it('should not fire trailing edge if only one call was made', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		throttled('a');  // fires immediately

		vi.advanceTimersByTime(100);

		// No trailing edge since there was nothing queued
		expect(fn).toHaveBeenCalledTimes(1);
	});

	it('should handle rapid bursts across multiple windows', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		// Window 1
		throttled('a');    // fires immediately at t=0
		throttled('b');    // queued (inside window)

		vi.advanceTimersByTime(50);
		// 'b' fires as trailing edge at t=50, resets lastCall
		expect(fn).toHaveBeenCalledTimes(2);
		expect(fn).toHaveBeenLastCalledWith('b');

		// Wait for window to fully expire
		vi.advanceTimersByTime(50);

		// Window 2
		throttled('c');    // fires immediately (window expired)
		expect(fn).toHaveBeenCalledTimes(3);
		throttled('d');    // queued (inside window)

		vi.advanceTimersByTime(50);
		expect(fn).toHaveBeenCalledTimes(4);
		expect(fn).toHaveBeenLastCalledWith('d');
	});

	it('should clear pending timer on leading-edge call after window expires', () => {
		const fn = vi.fn();
		const throttled = throttle(fn, 50);

		throttled('a');   // fires immediately at t=0

		vi.advanceTimersByTime(60);  // past the window

		throttled('b');   // fires immediately (window expired)
		expect(fn).toHaveBeenCalledTimes(2);
		expect(fn).toHaveBeenLastCalledWith('b');

		// No trailing edge should fire since 'b' was handled as leading
		vi.advanceTimersByTime(100);
		expect(fn).toHaveBeenCalledTimes(2);
	});
});
