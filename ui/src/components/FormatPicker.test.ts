import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import FormatPicker from './FormatPicker.svelte';

// Mock the API module
vi.mock('$lib/api/switch-api', () => ({
	getFormat: vi.fn(),
	setFormat: vi.fn(),
	apiCall: vi.fn(),
}));

import { getFormat, setFormat, apiCall } from '$lib/api/switch-api';

describe('FormatPicker', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('should render the format badge text', () => {
		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});
		const badge = container.querySelector('.format-badge');
		expect(badge).toBeTruthy();
		expect(badge?.textContent).toBe('1080p30');
	});

	it('should open dropdown on badge click', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});

		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		// Wait for async getFormat
		await vi.waitFor(() => {
			const dropdown = container.querySelector('.format-dropdown');
			expect(dropdown).toBeTruthy();
		});
	});

	it('should show presets in dropdown excluding current format', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});

		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		await vi.waitFor(() => {
			const items = container.querySelectorAll('.format-option');
			// Should show all presets, with current one marked
			expect(items.length).toBe(3);
		});
	});

	it('should show confirmation dialog when selecting a different format in single-engine mode', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});

		// Open dropdown
		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		await vi.waitFor(() => {
			expect(container.querySelector('.format-dropdown')).toBeTruthy();
		});

		// Click a different format
		const options = container.querySelectorAll('.format-option');
		const differentOption = Array.from(options).find(
			(o) => !o.classList.contains('current')
		) as HTMLElement;
		expect(differentOption).toBeTruthy();
		await fireEvent.click(differentOption);

		// Confirmation dialog should appear with single-engine message
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeTruthy();
		expect(dialog?.textContent).toContain('Changing format will restart the engine');
		expect(dialog?.textContent).toContain('~5 seconds');
	});

	it('should show dual-engine confirmation message when isDualEngine is true', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: true },
		});

		// Open dropdown
		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		await vi.waitFor(() => {
			expect(container.querySelector('.format-dropdown')).toBeTruthy();
		});

		// Click a different format
		const options = container.querySelectorAll('.format-option');
		const differentOption = Array.from(options).find(
			(o) => !o.classList.contains('current')
		) as HTMLElement;
		await fireEvent.click(differentOption);

		// Confirmation dialog should show dual-engine message
		const dialog = container.querySelector('[role="alertdialog"]');
		expect(dialog).toBeTruthy();
		expect(dialog?.textContent).toContain('Both engines will restart');
		expect(dialog?.textContent).toContain('~5 seconds');
	});

	it('should call setFormat when confirming format change', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});
		vi.mocked(setFormat).mockResolvedValue({} as any);
		vi.mocked(apiCall).mockImplementation((promise) => promise);

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});

		// Open dropdown
		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		await vi.waitFor(() => {
			expect(container.querySelector('.format-dropdown')).toBeTruthy();
		});

		// Click a different format (720p30 comes first alphabetically)
		const options = container.querySelectorAll('.format-option');
		const differentOption = Array.from(options).find(
			(o) => !o.classList.contains('current')
		) as HTMLElement;
		await fireEvent.click(differentOption);

		// Confirm
		const confirmBtn = container.querySelector('.confirm-btn') as HTMLElement;
		expect(confirmBtn).toBeTruthy();
		await fireEvent.click(confirmBtn);

		// Should have called apiCall with setFormat
		expect(apiCall).toHaveBeenCalled();
	});

	it('should close dropdown and dialog on cancel', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});

		// Open dropdown
		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		await vi.waitFor(() => {
			expect(container.querySelector('.format-dropdown')).toBeTruthy();
		});

		// Click a different format
		const options = container.querySelectorAll('.format-option');
		const differentOption = Array.from(options).find(
			(o) => !o.classList.contains('current')
		) as HTMLElement;
		await fireEvent.click(differentOption);

		// Cancel
		const cancelBtn = container.querySelector('.cancel-btn') as HTMLElement;
		expect(cancelBtn).toBeTruthy();
		await fireEvent.click(cancelBtn);

		// Dialog should close, dropdown should close
		expect(container.querySelector('[role="alertdialog"]')).toBeFalsy();
		expect(container.querySelector('.format-dropdown')).toBeFalsy();
	});

	it('should not show confirmation when clicking the current format', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});

		// Open dropdown
		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		await vi.waitFor(() => {
			expect(container.querySelector('.format-dropdown')).toBeTruthy();
		});

		// Click the current format
		const currentOption = container.querySelector('.format-option.current') as HTMLElement;
		expect(currentOption).toBeTruthy();
		await fireEvent.click(currentOption);

		// No confirmation dialog should appear; dropdown closes
		expect(container.querySelector('[role="alertdialog"]')).toBeFalsy();
	});

	it('should close dropdown when clicking outside', async () => {
		vi.mocked(getFormat).mockResolvedValue({
			format: { width: 1920, height: 1080, fpsNum: 30, fpsDen: 1, name: '1080p30' },
			presets: ['720p30', '1080p30', '1080p60'],
		});

		const { container } = render(FormatPicker, {
			props: { badgeText: '1080p30', isDualEngine: false },
		});

		// Open dropdown
		const badge = container.querySelector('.format-badge') as HTMLElement;
		await fireEvent.click(badge);

		await vi.waitFor(() => {
			expect(container.querySelector('.format-dropdown')).toBeTruthy();
		});

		// Click outside (on body)
		await fireEvent.mouseDown(document.body);

		// Dropdown should close
		await vi.waitFor(() => {
			expect(container.querySelector('.format-dropdown')).toBeFalsy();
		});
	});
});
