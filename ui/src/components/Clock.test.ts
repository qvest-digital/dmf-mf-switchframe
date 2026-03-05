import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import Clock from './Clock.svelte';

describe('Clock', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it('renders a time string matching HH:MM:SS pattern', () => {
		vi.setSystemTime(new Date(2026, 2, 5, 14, 30, 45));
		const { container } = render(Clock);
		const text = container.textContent ?? '';
		expect(text).toMatch(/\d{2}:\d{2}:\d{2}/);
	});

	it('displays the correct time from Date', () => {
		vi.setSystemTime(new Date(2026, 2, 5, 9, 5, 3));
		const { container } = render(Clock);
		expect(container.textContent).toContain('09:05:03');
	});

	it('updates the displayed time when the interval fires', async () => {
		vi.setSystemTime(new Date(2026, 2, 5, 12, 0, 0));
		const { container } = render(Clock);
		expect(container.textContent).toContain('12:00:0');

		// advanceTimersByTime both fires the interval AND advances the fake Date
		vi.advanceTimersByTime(3000);
		await tick();
		const text = container.textContent ?? '';
		// After advancing 3s from 12:00:00, the clock should show a time > 12:00:00
		expect(text).not.toBe('12:00:00');
		expect(text).toMatch(/12:00:0[1-9]/);
	});

	it('uses monospace font class', () => {
		const { container } = render(Clock);
		const el = container.querySelector('.clock');
		expect(el).toBeTruthy();
	});

	it('shows 24-hour format for afternoon times', () => {
		vi.setSystemTime(new Date(2026, 2, 5, 22, 15, 30));
		const { container } = render(Clock);
		expect(container.textContent).toContain('22:15:30');
	});

	it('shows midnight as 00:00:00', () => {
		vi.setSystemTime(new Date(2026, 2, 5, 0, 0, 0));
		const { container } = render(Clock);
		expect(container.textContent).toContain('00:00:00');
	});
});
