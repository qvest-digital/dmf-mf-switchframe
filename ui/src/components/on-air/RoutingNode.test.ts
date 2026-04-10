import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import RoutingNode from './RoutingNode.svelte';

function defaultProps() {
	return {
		x: 40,
		y: 60,
		name: 'cam1',
		category: 'source' as const,
		latencyNs: 120000,
		health: 'healthy' as const,
		bypassed: false,
		isProgram: false,
		onBypassToggle: vi.fn(),
		onclick: vi.fn(),
	};
}

describe('RoutingNode', () => {
	it('renders node name', () => {
		const { container } = render(RoutingNode, { props: defaultProps() });
		const nameText = container.querySelector('.node-name');
		expect(nameText).toBeTruthy();
		expect(nameText?.textContent).toBe('cam1');
	});

	it('shows health dot with correct color for healthy', () => {
		const { container } = render(RoutingNode, { props: defaultProps() });
		const dot = container.querySelector('.node-health-dot');
		expect(dot).toBeTruthy();
		expect(dot?.getAttribute('fill')).toBe('#22c55e');
	});

	it('shows health dot with amber for degraded', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), health: 'degraded' as const },
		});
		const dot = container.querySelector('.node-health-dot');
		expect(dot?.getAttribute('fill')).toBe('#eab308');
	});

	it('shows health dot with red for error', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), health: 'error' as const },
		});
		const dot = container.querySelector('.node-health-dot');
		expect(dot?.getAttribute('fill')).toBe('#ef4444');
	});

	it('shows bypass toggle', () => {
		const { container } = render(RoutingNode, { props: defaultProps() });
		const toggle = container.querySelector('.bypass-toggle');
		expect(toggle).toBeTruthy();
	});

	it('bypassed state shows dashed border', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), bypassed: true },
		});
		const bg = container.querySelector('.node-bg');
		expect(bg?.classList.contains('node-bypassed')).toBe(true);
	});

	it('click bypass toggle calls callback', async () => {
		const onBypassToggle = vi.fn();
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), onBypassToggle },
		});
		const toggle = container.querySelector('.bypass-toggle') as Element;
		expect(toggle).toBeTruthy();
		await fireEvent.click(toggle);
		expect(onBypassToggle).toHaveBeenCalledOnce();
	});

	it('click node body calls onclick', async () => {
		const onclick = vi.fn();
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), onclick },
		});
		const bg = container.querySelector('.node-bg') as Element;
		expect(bg).toBeTruthy();
		await fireEvent.click(bg);
		expect(onclick).toHaveBeenCalledOnce();
	});

	it('formats latency in ms', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), latencyNs: 1500000 },
		});
		const latency = container.querySelector('.node-latency');
		expect(latency?.textContent).toBe('1.500ms');
	});

	it('shows displayName when provided', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), displayName: 'Camera 1' },
		});
		const nameText = container.querySelector('.node-name');
		expect(nameText?.textContent).toBe('Camera 1');
	});

	it('program path has program border class', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), isProgram: true },
		});
		const bg = container.querySelector('.node-bg');
		expect(bg?.classList.contains('node-program')).toBe(true);
	});

	it('shows BYPASSED overlay when bypassed', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), bypassed: true },
		});
		const overlay = container.querySelector('.bypass-overlay-text');
		expect(overlay).toBeTruthy();
		expect(overlay?.textContent).toBe('BYPASSED');
	});

	it('renders thumbnail image when provided', () => {
		const { container } = render(RoutingNode, {
			props: { ...defaultProps(), thumbnail: 'data:image/jpeg;base64,abc123' },
		});
		const img = container.querySelector('.node-thumb-image');
		expect(img).toBeTruthy();
		expect(img?.getAttribute('href')).toBe('data:image/jpeg;base64,abc123');
	});
});
