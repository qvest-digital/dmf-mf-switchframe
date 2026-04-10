import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import PipelineRoutingOverlay from './PipelineRoutingOverlay.svelte';

describe('PipelineRoutingOverlay', () => {
	it('does not render when not visible', () => {
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: false, onclose: () => {} },
		});
		expect(container.querySelector('.routing-overlay')).toBeNull();
	});

	it('renders overlay when visible', () => {
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: true, onclose: () => {} },
		});
		expect(container.querySelector('.routing-overlay')).toBeTruthy();
	});

	it('shows SIGNAL ROUTING header', () => {
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: true, onclose: () => {} },
		});
		expect(container.textContent).toContain('SIGNAL ROUTING');
	});

	it('shows CLOSE button that calls onclose', async () => {
		const onclose = vi.fn();
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: true, onclose },
		});
		const closeBtn = container.querySelector('.routing-close') as HTMLElement;
		expect(closeBtn).toBeTruthy();
		await fireEvent.click(closeBtn);
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('Escape key closes overlay', async () => {
		const onclose = vi.fn();
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: true, onclose },
		});
		const overlay = container.querySelector('.routing-overlay') as HTMLElement;
		expect(overlay).toBeTruthy();
		await fireEvent.keyDown(overlay, { key: 'Escape' });
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('renders SVG graph area', () => {
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: true, onclose: () => {} },
		});
		const svg = container.querySelector('svg');
		expect(svg).toBeTruthy();
	});

	it('shows bypass count in footer', () => {
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: true, onclose: () => {} },
		});
		const footer = container.querySelector('.routing-footer');
		expect(footer).toBeTruthy();
		expect(footer?.textContent).toContain('bypassed');
	});

	it('shows LIVE badge', () => {
		const { container } = render(PipelineRoutingOverlay, {
			props: { visible: true, onclose: () => {} },
		});
		expect(container.textContent).toContain('LIVE');
	});

	it('starts with zero bypass count when no API data', async () => {
		const bypassCountFn = vi.fn();
		render(PipelineRoutingOverlay, {
			props: { visible: true, onclose: () => {}, onBypassCountChange: bypassCountFn },
		});
		// Without API data, bypass count should be 0
		await vi.waitFor(() => {
			expect(bypassCountFn).toHaveBeenCalledWith(0);
		});
	});
});
