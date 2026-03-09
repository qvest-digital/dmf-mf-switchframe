import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import KeyboardOverlay from './KeyboardOverlay.svelte';

describe('KeyboardOverlay', () => {
	it('should render Keyboard Shortcuts heading', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const heading = container.querySelector('h2');
		expect(heading?.textContent).toBe('Keyboard Shortcuts');
	});

	it('should render all 15 shortcuts in table rows', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const rows = container.querySelectorAll('tbody tr');
		expect(rows.length).toBe(15);
	});

	it('should render expected shortcut keys', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const kbdElements = container.querySelectorAll('tbody kbd');
		const keys = Array.from(kbdElements).map((el) => el.textContent);
		expect(keys).toContain('1-9');
		expect(keys).toContain('Shift + 1-9');
		expect(keys).toContain('Space');
		expect(keys).toContain('Enter');
		expect(keys).toContain('F1');
		expect(keys).toContain('F2');
		expect(keys).toContain('Ctrl + 1-9');
		expect(keys).toContain('Ctrl+Shift + 1-7');
		expect(keys).toContain('` (backtick)');
		expect(keys).toContain('?');
		expect(keys).toContain('Esc');
	});

	it('should render expected shortcut actions', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const text = container.textContent;
		expect(text).toContain('Select preview source');
		expect(text).toContain('Hot-punch to program');
		expect(text).toContain('Cut (swap preview');
		expect(text).toContain('Auto transition');
		expect(text).toContain('Fade to black');
		expect(text).toContain('Toggle DSK');
		expect(text).toContain('Run macro');
		expect(text).toContain('Switch bottom tab');
		expect(text).toContain('Toggle fullscreen');
		expect(text).toContain('Toggle this overlay');
		expect(text).toContain('Close overlay');
	});

	it('should have role="dialog" on the overlay', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const dialog = container.querySelector('[role="dialog"]');
		expect(dialog).toBeTruthy();
	});

	it('should have aria-modal="true" on the dialog', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const dialog = container.querySelector('[role="dialog"]');
		expect(dialog?.getAttribute('aria-modal')).toBe('true');
	});

	it('should have aria-label="Keyboard shortcuts" on the dialog', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const dialog = container.querySelector('[role="dialog"]');
		expect(dialog?.getAttribute('aria-label')).toBe('Keyboard shortcuts');
	});

	it('should call onclose callback on Escape keydown', async () => {
		const handleClose = vi.fn();
		render(KeyboardOverlay, { props: { onclose: handleClose } });

		await fireEvent.keyDown(window, { code: 'Escape' });

		expect(handleClose).toHaveBeenCalledTimes(1);
	});

	it('should call onclose callback on Slash keydown', async () => {
		const handleClose = vi.fn();
		render(KeyboardOverlay, { props: { onclose: handleClose } });

		await fireEvent.keyDown(window, { code: 'Slash' });

		expect(handleClose).toHaveBeenCalledTimes(1);
	});

	it('should call onclose when backdrop is clicked', async () => {
		const handleClose = vi.fn();
		const { container } = render(KeyboardOverlay, { props: { onclose: handleClose } });
		const backdrop = container.querySelector('.overlay-backdrop') as HTMLElement;

		await fireEvent.click(backdrop);

		expect(handleClose).toHaveBeenCalledTimes(1);
	});

	it('should render dismiss text with kbd elements', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const dismiss = container.querySelector('.dismiss');
		expect(dismiss).toBeTruthy();
		const kbds = dismiss?.querySelectorAll('kbd');
		expect(kbds?.length).toBe(2);
		expect(kbds?.[0]?.textContent).toBe('?');
		expect(kbds?.[1]?.textContent).toBe('Esc');
	});

	it('should render table headers for Key and Action', () => {
		const { container } = render(KeyboardOverlay, { props: { onclose: vi.fn() } });
		const headers = container.querySelectorAll('th');
		expect(headers.length).toBe(2);
		expect(headers[0]?.textContent).toBe('Key');
		expect(headers[1]?.textContent).toBe('Action');
	});
});
