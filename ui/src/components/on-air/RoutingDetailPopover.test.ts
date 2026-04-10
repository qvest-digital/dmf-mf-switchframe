import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import RoutingDetailPopover from './RoutingDetailPopover.svelte';

function defaultNode() {
	return {
		name: 'gpu_dsk',
		category: 'gpu',
		health: 'healthy',
		bypassed: false,
		latencyNs: 60000,
		maxNs: 120000,
		error: undefined as string | undefined,
		thumbnail: undefined as string | undefined,
	};
}

function defaultProps() {
	return {
		visible: true,
		x: 200,
		y: 300,
		node: defaultNode(),
		onclose: vi.fn(),
		onBypassToggle: vi.fn(),
	};
}

describe('RoutingDetailPopover', () => {
	it('not visible when visible=false', () => {
		const { container } = render(RoutingDetailPopover, {
			props: { ...defaultProps(), visible: false },
		});
		expect(container.querySelector('.routing-detail-popover')).toBeNull();
	});

	it('shows node name when visible', () => {
		const { container } = render(RoutingDetailPopover, {
			props: defaultProps(),
		});
		const popover = container.querySelector('.routing-detail-popover');
		expect(popover).toBeTruthy();
		expect(popover?.textContent).toContain('gpu_dsk');
	});

	it('shows latency metrics', () => {
		const { container } = render(RoutingDetailPopover, {
			props: defaultProps(),
		});
		const content = container.querySelector('.routing-detail-popover');
		expect(content?.textContent).toContain('0.060ms');
		expect(content?.textContent).toContain('0.120ms');
	});

	it('shows bypass toggle button', () => {
		const { container } = render(RoutingDetailPopover, {
			props: defaultProps(),
		});
		const btn = container.querySelector('.detail-bypass-btn');
		expect(btn).toBeTruthy();
		expect(btn?.textContent).toContain('Bypass');
	});

	it('bypass button shows re-enable when bypassed', () => {
		const props = defaultProps();
		props.node.bypassed = true;
		const { container } = render(RoutingDetailPopover, { props });
		const btn = container.querySelector('.detail-bypass-btn');
		expect(btn?.textContent).toContain('Re-enable');
	});

	it('close button calls onclose', async () => {
		const onclose = vi.fn();
		const { container } = render(RoutingDetailPopover, {
			props: { ...defaultProps(), onclose },
		});
		const closeBtn = container.querySelector('.detail-close-btn');
		expect(closeBtn).toBeTruthy();
		await fireEvent.click(closeBtn!);
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('bypass button calls onBypassToggle', async () => {
		const onBypassToggle = vi.fn();
		const { container } = render(RoutingDetailPopover, {
			props: { ...defaultProps(), onBypassToggle },
		});
		const btn = container.querySelector('.detail-bypass-btn');
		await fireEvent.click(btn!);
		expect(onBypassToggle).toHaveBeenCalledOnce();
	});

	it('shows error message when present', () => {
		const props = defaultProps();
		props.node.error = 'Decode failure: corrupted NAL unit';
		const { container } = render(RoutingDetailPopover, { props });
		const errEl = container.querySelector('.detail-error');
		expect(errEl).toBeTruthy();
		expect(errEl?.textContent).toContain('Decode failure: corrupted NAL unit');
	});

	it('does not show error section when no error', () => {
		const { container } = render(RoutingDetailPopover, {
			props: defaultProps(),
		});
		expect(container.querySelector('.detail-error')).toBeNull();
	});

	it('shows category badge', () => {
		const { container } = render(RoutingDetailPopover, {
			props: defaultProps(),
		});
		const badge = container.querySelector('.detail-category');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toContain('gpu');
	});

	it('shows health status with dot', () => {
		const { container } = render(RoutingDetailPopover, {
			props: defaultProps(),
		});
		const healthDot = container.querySelector('.detail-health-dot');
		expect(healthDot).toBeTruthy();
		const healthLabel = container.querySelector('.detail-health-label');
		expect(healthLabel?.textContent).toContain('healthy');
	});
});
