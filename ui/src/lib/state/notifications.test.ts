import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { notify, dismiss, getNotifications, clearAll, type Notification } from './notifications.svelte';

describe('notifications store', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		clearAll();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('adds notification and retrieves it', () => {
		notify('error', 'Something went wrong');
		const items = getNotifications();
		expect(items).toHaveLength(1);
		expect(items[0].type).toBe('error');
		expect(items[0].message).toBe('Something went wrong');
	});

	it('assigns unique incrementing ids', () => {
		notify('error', 'First');
		notify('warning', 'Second');
		notify('info', 'Third');
		const items = getNotifications();
		expect(items).toHaveLength(3);
		expect(items[0].id).toBeLessThan(items[1].id);
		expect(items[1].id).toBeLessThan(items[2].id);
	});

	it('dismiss removes notification', () => {
		notify('error', 'To dismiss');
		const items = getNotifications();
		expect(items).toHaveLength(1);
		dismiss(items[0].id);
		// After dismiss is called, item is marked dismissed immediately
		// and removed after 300ms animation time
		vi.advanceTimersByTime(300);
		expect(getNotifications()).toHaveLength(0);
	});

	it('auto-dismisses warnings after 5 seconds', () => {
		notify('warning', 'Will auto-dismiss');
		expect(getNotifications()).toHaveLength(1);
		vi.advanceTimersByTime(4999);
		expect(getNotifications()).toHaveLength(1);
		vi.advanceTimersByTime(1);
		// After 5s the dismiss is triggered, then 300ms animation
		vi.advanceTimersByTime(300);
		expect(getNotifications()).toHaveLength(0);
	});

	it('errors persist until manually dismissed', () => {
		notify('error', 'Persistent error');
		vi.advanceTimersByTime(60000);
		expect(getNotifications()).toHaveLength(1);
		// Must be manually dismissed
		const items = getNotifications();
		dismiss(items[0].id);
		vi.advanceTimersByTime(300);
		expect(getNotifications()).toHaveLength(0);
	});

	it('getNotifications filters dismissed items', () => {
		notify('error', 'Keep');
		notify('warning', 'Remove');
		const items = getNotifications();
		dismiss(items[1].id);
		// Immediately after dismiss, item is marked dismissed and filtered
		const filtered = getNotifications();
		expect(filtered).toHaveLength(1);
		expect(filtered[0].message).toBe('Keep');
	});

	it('clearAll removes everything', () => {
		notify('error', 'One');
		notify('warning', 'Two');
		notify('info', 'Three');
		expect(getNotifications()).toHaveLength(3);
		clearAll();
		expect(getNotifications()).toHaveLength(0);
	});

	it('auto-dismisses info after 5 seconds', () => {
		notify('info', 'Informational');
		expect(getNotifications()).toHaveLength(1);
		vi.advanceTimersByTime(5000);
		vi.advanceTimersByTime(300);
		expect(getNotifications()).toHaveLength(0);
	});

	it('sets timestamp on notification', () => {
		const before = Date.now();
		notify('info', 'Timed');
		const items = getNotifications();
		expect(items[0].timestamp).toBeGreaterThanOrEqual(before);
		expect(items[0].timestamp).toBeLessThanOrEqual(Date.now());
	});

	it('clearAll cancels pending auto-dismiss timers', () => {
		notify('warning', 'Will be cleared');
		notify('info', 'Also cleared');
		clearAll();
		expect(getNotifications()).toHaveLength(0);
		// Advance past auto-dismiss time — timers should have been cancelled
		// so no errors from trying to dismiss already-cleared notifications
		vi.advanceTimersByTime(6000);
		expect(getNotifications()).toHaveLength(0);
	});

	it('dismiss cancels auto-dismiss timer for that notification', () => {
		notify('warning', 'Manual dismiss');
		const items = getNotifications();
		dismiss(items[0].id);
		// Immediately filtered out
		expect(getNotifications()).toHaveLength(0);
		// Advance past auto-dismiss — should not cause issues
		vi.advanceTimersByTime(6000);
		expect(getNotifications()).toHaveLength(0);
	});
});
