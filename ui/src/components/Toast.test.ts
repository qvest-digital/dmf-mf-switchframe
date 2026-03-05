import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import Toast from './Toast.svelte';
import { notify, clearAll } from '$lib/state/notifications.svelte';

describe('Toast', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		clearAll();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('renders nothing when no notifications', () => {
		const { container } = render(Toast);
		const items = container.querySelectorAll('.toast-item');
		expect(items).toHaveLength(0);
	});

	it('renders error notifications with red styling', () => {
		notify('error', 'Something failed');
		const { container } = render(Toast);
		const item = container.querySelector('.toast-item.error');
		expect(item).toBeTruthy();
		expect(item?.textContent).toContain('Something failed');
	});

	it('renders warning notifications with amber styling', () => {
		notify('warning', 'Watch out');
		const { container } = render(Toast);
		const item = container.querySelector('.toast-item.warning');
		expect(item).toBeTruthy();
		expect(item?.textContent).toContain('Watch out');
	});

	it('renders info notifications', () => {
		notify('info', 'FYI');
		const { container } = render(Toast);
		const item = container.querySelector('.toast-item.info');
		expect(item).toBeTruthy();
		expect(item?.textContent).toContain('FYI');
	});

	it('dismiss button removes notification', async () => {
		notify('error', 'Dismissable');
		const { container } = render(Toast);
		const button = container.querySelector('.toast-dismiss') as HTMLElement;
		expect(button).toBeTruthy();
		await fireEvent.click(button);
		// After dismiss + animation time
		vi.advanceTimersByTime(300);
		const items = container.querySelectorAll('.toast-item');
		expect(items).toHaveLength(0);
	});

	it('has role="alert" on items and aria-live="polite" on container', () => {
		notify('error', 'Alert test');
		const { container } = render(Toast);
		const item = container.querySelector('[role="alert"]');
		expect(item).toBeTruthy();
		const live = container.querySelector('[aria-live="polite"]');
		expect(live).toBeTruthy();
	});

	it('renders multiple notifications', () => {
		notify('error', 'Error one');
		notify('warning', 'Warning two');
		notify('info', 'Info three');
		const { container } = render(Toast);
		const items = container.querySelectorAll('.toast-item');
		expect(items).toHaveLength(3);
	});
});
