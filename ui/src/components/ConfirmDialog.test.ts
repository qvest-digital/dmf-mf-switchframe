import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import ConfirmDialog from './ConfirmDialog.svelte';

describe('ConfirmDialog', () => {
	it('should not render dialog when open is false', () => {
		const { container } = render(ConfirmDialog, {
			props: {
				open: false,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel: () => {},
			},
		});
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeFalsy();
	});

	it('should show title and message when open', () => {
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Stop Recording',
				message: 'The current file will be finalized.',
				onconfirm: () => {},
				oncancel: () => {},
			},
		});
		expect(container.textContent).toContain('Stop Recording');
		expect(container.textContent).toContain('The current file will be finalized.');
	});

	it('should have role="alertdialog" and aria-modal="true"', () => {
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel: () => {},
			},
		});
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeTruthy();
		expect(dialog?.getAttribute('aria-modal')).toBe('true');
	});

	it('should have aria-labelledby and aria-describedby', () => {
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel: () => {},
			},
		});
		const dialog = container.querySelector('[role="alertdialog"]');
		const labelledBy = dialog?.getAttribute('aria-labelledby');
		const describedBy = dialog?.getAttribute('aria-describedby');
		expect(labelledBy).toBeTruthy();
		expect(describedBy).toBeTruthy();
		// Verify the referenced elements exist
		expect(container.querySelector(`#${labelledBy}`)).toBeTruthy();
		expect(container.querySelector(`#${describedBy}`)).toBeTruthy();
	});

	it('should call onconfirm when confirm button clicked', async () => {
		const onconfirm = vi.fn();
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm,
				oncancel: () => {},
			},
		});
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		expect(confirmBtn).toBeTruthy();
		await fireEvent.click(confirmBtn);
		expect(onconfirm).toHaveBeenCalledOnce();
	});

	it('should call oncancel when cancel button clicked', async () => {
		const oncancel = vi.fn();
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel,
			},
		});
		const cancelBtn = container.querySelector('.cancel-btn') as HTMLButtonElement;
		expect(cancelBtn).toBeTruthy();
		await fireEvent.click(cancelBtn);
		expect(oncancel).toHaveBeenCalledOnce();
	});

	it('should close on Escape key', async () => {
		const oncancel = vi.fn();
		render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel,
			},
		});
		await fireEvent.keyDown(window, { code: 'Escape' });
		expect(oncancel).toHaveBeenCalledOnce();
	});

	it('should have destructive styling on confirm button', () => {
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel: () => {},
			},
		});
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		expect(confirmBtn).toBeTruthy();
		expect(confirmBtn.classList.contains('confirm-btn')).toBe(true);
	});

	it('should use custom confirmLabel when provided', () => {
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				confirmLabel: 'Delete',
				onconfirm: () => {},
				oncancel: () => {},
			},
		});
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		expect(confirmBtn.textContent).toContain('Delete');
	});

	it('should default confirmLabel to "Confirm"', () => {
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel: () => {},
			},
		});
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLButtonElement;
		expect(confirmBtn.textContent).toContain('Confirm');
	});

	it('should not close when backdrop is clicked', async () => {
		const oncancel = vi.fn();
		const { container } = render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Confirm',
				message: 'Are you sure?',
				onconfirm: () => {},
				oncancel,
			},
		});
		const backdrop = container.querySelector('.confirm-backdrop') as HTMLElement;
		expect(backdrop).toBeTruthy();
		await fireEvent.click(backdrop);
		expect(oncancel).not.toHaveBeenCalled();
	});
});
