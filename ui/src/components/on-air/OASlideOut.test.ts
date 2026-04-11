import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import OASlideOut from './OASlideOut.svelte';

describe('OASlideOut', () => {
	it('renders nothing when visible is false', () => {
		const { container } = render(OASlideOut, {
			props: { visible: false, title: 'Audio', onclose: vi.fn() },
		});
		expect(container.querySelector('.oa-slideout')).toBeNull();
		expect(container.querySelector('.oa-slideout-backdrop')).toBeNull();
	});

	it('renders panel with title when visible', () => {
		render(OASlideOut, {
			props: { visible: true, title: 'Audio', onclose: vi.fn() },
		});
		expect(screen.getByText('Audio')).toBeTruthy();
		const panel = document.querySelector('.oa-slideout');
		expect(panel).toBeTruthy();
	});

	it('close button calls onclose', async () => {
		const onclose = vi.fn();
		render(OASlideOut, {
			props: { visible: true, title: 'Graphics', onclose },
		});
		const closeBtn = document.querySelector('.oa-slideout-close');
		expect(closeBtn).toBeTruthy();
		await fireEvent.click(closeBtn!);
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('backdrop click calls onclose', async () => {
		const onclose = vi.fn();
		render(OASlideOut, {
			props: { visible: true, title: 'I/O', onclose },
		});
		const backdrop = document.querySelector('.oa-slideout-backdrop');
		expect(backdrop).toBeTruthy();
		await fireEvent.click(backdrop!);
		expect(onclose).toHaveBeenCalledOnce();
	});

	it('renders content area', () => {
		render(OASlideOut, {
			props: { visible: true, title: 'Captions', onclose: vi.fn() },
		});
		const content = document.querySelector('.oa-slideout-content');
		expect(content).toBeTruthy();
	});

	it('applies custom width via style', () => {
		render(OASlideOut, {
			props: { visible: true, title: 'Wide', onclose: vi.fn(), width: 500 },
		});
		const panel = document.querySelector('.oa-slideout') as HTMLElement;
		expect(panel).toBeTruthy();
		expect(panel.style.width).toBe('500px');
	});

	it('uses default 400px width when not specified', () => {
		render(OASlideOut, {
			props: { visible: true, title: 'Default', onclose: vi.fn() },
		});
		const panel = document.querySelector('.oa-slideout') as HTMLElement;
		expect(panel).toBeTruthy();
		expect(panel.style.width).toBe('400px');
	});

	it('escape key is handled by parent (not by OASlideOut)', async () => {
		const onclose = vi.fn();
		render(OASlideOut, {
			props: { visible: true, title: 'Test', onclose },
		});
		await fireEvent.keyDown(window, { key: 'Escape' });
		// Escape is now handled by the parent OnAirController, not by OASlideOut
		expect(onclose).not.toHaveBeenCalled();
	});
});
