import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/svelte';
import ConnectionBanner from './ConnectionBanner.svelte';

describe('ConnectionBanner', () => {
	it('renders nothing when webtransport + ok', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'webtransport', syncStatus: 'ok' },
		});
		expect(container.querySelector('.connection-banner')).toBeFalsy();
		expect(container.querySelector('.disconnect-overlay')).toBeFalsy();
	});

	it('shows amber banner when polling after prior webtransport', async () => {
		const { container, rerender } = render(ConnectionBanner, {
			props: { connectionState: 'webtransport', syncStatus: 'ok' },
		});
		// Simulate degradation: was connected, now polling
		await rerender({ connectionState: 'polling', syncStatus: 'ok' });
		const banner = container.querySelector('.connection-banner.polling');
		expect(banner).toBeTruthy();
		expect(banner?.textContent).toContain('Low-latency connection lost');
		expect(banner?.textContent).toContain('fallback');
	});

	it('does not show polling banner if webtransport was never connected', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'polling', syncStatus: 'ok' },
		});
		const banner = container.querySelector('.connection-banner.polling');
		expect(banner).toBeFalsy();
	});

	it('shows yellow banner when resyncing', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'webtransport', syncStatus: 'resyncing' },
		});
		const banner = container.querySelector('.connection-banner.resyncing');
		expect(banner).toBeTruthy();
		expect(banner?.textContent).toContain('Resyncing');
	});

	it('shows full overlay when connectionState is disconnected', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'disconnected', syncStatus: 'ok' },
		});
		const overlay = container.querySelector('.disconnect-overlay');
		expect(overlay).toBeTruthy();
		expect(overlay?.textContent).toContain('CONNECTION LOST');
		expect(overlay?.textContent).toContain('Reconnecting');
	});

	it('shows full overlay when syncStatus is disconnected', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'webtransport', syncStatus: 'disconnected' },
		});
		const overlay = container.querySelector('.disconnect-overlay');
		expect(overlay).toBeTruthy();
		expect(overlay?.textContent).toContain('CONNECTION LOST');
	});

	it('disconnected state takes priority over resyncing', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'disconnected', syncStatus: 'resyncing' },
		});
		const overlay = container.querySelector('.disconnect-overlay');
		expect(overlay).toBeTruthy();
		const banner = container.querySelector('.connection-banner');
		expect(banner).toBeFalsy();
	});

	it('resyncing takes priority over polling', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'polling', syncStatus: 'resyncing' },
		});
		const banner = container.querySelector('.connection-banner.resyncing');
		expect(banner).toBeTruthy();
		const polling = container.querySelector('.connection-banner.polling');
		expect(polling).toBeFalsy();
	});

	it('overlay has appropriate ARIA attributes', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'disconnected', syncStatus: 'disconnected' },
		});
		const overlay = container.querySelector('.disconnect-overlay');
		expect(overlay).toBeTruthy();
		expect(overlay?.getAttribute('role')).toBe('alertdialog');
		expect(overlay?.getAttribute('aria-live')).toBe('assertive');
	});

	it('banner has appropriate ARIA attributes', async () => {
		const { container, rerender } = render(ConnectionBanner, {
			props: { connectionState: 'webtransport', syncStatus: 'ok' },
		});
		await rerender({ connectionState: 'polling', syncStatus: 'ok' });
		const banner = container.querySelector('.connection-banner');
		expect(banner).toBeTruthy();
		expect(banner?.getAttribute('role')).toBe('status');
		expect(banner?.getAttribute('aria-live')).toBe('polite');
	});

	describe('dual-engine mode', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});
		afterEach(() => {
			vi.useRealTimers();
		});

		it('shows engine-specific unreachable message', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: {
						mode: 'dual',
						engineDown: 'A',
						reason: 'unreachable',
					},
				},
			});
			const banner = container.querySelector('.connection-banner.engine-warning');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).toContain('Engine A unreachable');
			expect(banner?.textContent).toContain('commands routing to Engine B only');
		});

		it('shows redundancy degraded for offline engine', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: {
						mode: 'dual',
						engineDown: 'B',
						reason: 'offline',
					},
				},
			});
			const banner = container.querySelector('.connection-banner.engine-warning');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).toContain('Engine B offline');
			expect(banner?.textContent).toContain('redundancy degraded');
			expect(banner?.textContent).toContain('Restoring');
		});

		it('shows provisioning message', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: {
						mode: 'dual',
						engineDown: 'A',
						reason: 'offline',
						provisioning: true,
					},
				},
			});
			const banner = container.querySelector('.connection-banner.engine-warning');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).toContain('Engine A offline');
			expect(banner?.textContent).toContain('provisioning replacement');
		});

		it('shows restored banner briefly then auto-dismisses', async () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: {
						mode: 'dual',
						restored: true,
					},
				},
			});
			// Banner should be visible immediately
			let banner = container.querySelector('.connection-banner.engine-restored');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).toContain('Redundancy restored');

			// After 3 seconds, should auto-dismiss
			await vi.advanceTimersByTimeAsync(3000);
			banner = container.querySelector('.connection-banner.engine-restored');
			expect(banner).toBeFalsy();
		});

		it('does not show dual-engine banner when mode is single', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: {
						mode: 'single',
						engineDown: 'A',
						reason: 'unreachable',
					},
				},
			});
			const banner = container.querySelector('.connection-banner.engine-warning');
			expect(banner).toBeFalsy();
		});

		it('does not show dual-engine banner when engineStatus is undefined', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
				},
			});
			const banner = container.querySelector('.connection-banner.engine-warning');
			expect(banner).toBeFalsy();
			const restored = container.querySelector('.connection-banner.engine-restored');
			expect(restored).toBeFalsy();
		});

		it('existing single-engine disconnect still works with engineStatus undefined', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
				},
			});
			const overlay = container.querySelector('.disconnect-overlay');
			expect(overlay).toBeTruthy();
			expect(overlay?.textContent).toContain('CONNECTION LOST');
		});

		it('shows unreachable with Engine B down, routing to A', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: {
						mode: 'dual',
						engineDown: 'B',
						reason: 'unreachable',
					},
				},
			});
			const banner = container.querySelector('.connection-banner.engine-warning');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).toContain('Engine B unreachable');
			expect(banner?.textContent).toContain('commands routing to Engine A only');
		});
	});
});
