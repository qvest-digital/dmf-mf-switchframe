import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import ProgramCompare from './ProgramCompare.svelte';

describe('ProgramCompare', () => {
	it('renders both engine labels', () => {
		const { container } = render(ProgramCompare);
		const text = container.textContent ?? '';
		expect(text).toContain('ENGINE A');
		expect(text).toContain('ENGINE B');
	});

	it('shows RTT and seq metadata', () => {
		const { container } = render(ProgramCompare, {
			props: {
				engineAMeta: { rtt: 12, seq: 48291, healthy: true },
				engineBMeta: { rtt: 45, seq: 48291, healthy: true },
			},
		});
		const text = container.textContent ?? '';
		expect(text).toContain('12ms');
		expect(text).toContain('45ms');
		expect(text).toContain('48291');
	});

	it('shows health dots with correct classes', () => {
		const { container } = render(ProgramCompare, {
			props: {
				engineAMeta: { rtt: 10, seq: 100, healthy: true },
				engineBMeta: { rtt: 20, seq: 100, healthy: false },
			},
		});
		const dots = container.querySelectorAll('.health-dot');
		expect(dots).toHaveLength(2);
		expect(dots[0].classList.contains('healthy')).toBe(true);
		expect(dots[1].classList.contains('unhealthy')).toBe(true);
	});

	it('exit button calls onExit', async () => {
		const onExit = vi.fn();
		const { container } = render(ProgramCompare, {
			props: { onExit },
		});
		const btn = container.querySelector('.exit-btn') as HTMLButtonElement;
		expect(btn).toBeTruthy();
		await fireEvent.click(btn);
		expect(onExit).toHaveBeenCalledOnce();
	});

	it('handles missing metadata gracefully', () => {
		const { container } = render(ProgramCompare);
		const metaValues = container.querySelectorAll('.meta-value');
		// 6 values: RTT, Seq, health for each engine
		// RTT and Seq should show "--" when undefined
		const dashes = Array.from(metaValues).filter(el => el.textContent?.trim() === '--');
		expect(dashes.length).toBeGreaterThanOrEqual(4); // At least RTT and Seq for both engines
	});

	it('renders canvas elements for both engines', () => {
		const { container } = render(ProgramCompare);
		const canvases = container.querySelectorAll('.compare-canvas');
		expect(canvases).toHaveLength(2);
		// Verify dimensions
		const canvasA = canvases[0] as HTMLCanvasElement;
		const canvasB = canvases[1] as HTMLCanvasElement;
		expect(canvasA.width).toBe(960);
		expect(canvasA.height).toBe(540);
		expect(canvasB.width).toBe(960);
		expect(canvasB.height).toBe(540);
	});

	it('calls onCanvasAReady when canvas A mounts', async () => {
		const onCanvasAReady = vi.fn();
		render(ProgramCompare, {
			props: { onCanvasAReady },
		});
		// Svelte 5 effects run synchronously on mount in test environment,
		// but wait a tick to be safe.
		await new Promise(r => setTimeout(r, 0));
		expect(onCanvasAReady).toHaveBeenCalledWith(expect.any(HTMLCanvasElement));
	});

	it('calls onCanvasBReady when canvas B mounts', async () => {
		const onCanvasBReady = vi.fn();
		render(ProgramCompare, {
			props: { onCanvasBReady },
		});
		await new Promise(r => setTimeout(r, 0));
		expect(onCanvasBReady).toHaveBeenCalledWith(expect.any(HTMLCanvasElement));
	});
});
