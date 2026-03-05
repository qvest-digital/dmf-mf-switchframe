import { describe, it, expect } from 'vitest';
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

	it('shows amber banner when polling + ok', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'polling', syncStatus: 'ok' },
		});
		const banner = container.querySelector('.connection-banner.polling');
		expect(banner).toBeTruthy();
		expect(banner?.textContent).toContain('Low-latency connection lost');
		expect(banner?.textContent).toContain('fallback');
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
		// Should show overlay, not banner
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

	it('banner has appropriate ARIA attributes', () => {
		const { container } = render(ConnectionBanner, {
			props: { connectionState: 'polling', syncStatus: 'ok' },
		});
		const banner = container.querySelector('.connection-banner');
		expect(banner).toBeTruthy();
		expect(banner?.getAttribute('role')).toBe('status');
		expect(banner?.getAttribute('aria-live')).toBe('polite');
	});
});
