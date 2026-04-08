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

	describe('per-engine connection indicators', () => {
		it('shows per-engine dots when dual mode and both healthy', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: { mode: 'dual' },
					perEngineState: { A: 'webtransport', B: 'webtransport' },
				},
			});
			const dots = container.querySelectorAll('.engine-dot');
			expect(dots.length).toBe(2);
			expect(dots[0].textContent).toBe('A');
			expect(dots[1].textContent).toBe('B');
			expect(dots[0].classList.contains('healthy')).toBe(true);
			expect(dots[1].classList.contains('healthy')).toBe(true);
		});

		it('shows degraded dot for engine on polling', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: { mode: 'dual' },
					perEngineState: { A: 'webtransport', B: 'polling' },
				},
			});
			const dots = container.querySelectorAll('.engine-dot');
			expect(dots.length).toBe(2);
			expect(dots[0].classList.contains('healthy')).toBe(true);
			expect(dots[1].classList.contains('degraded')).toBe(true);
		});

		it('shows offline dot for disconnected engine', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: { mode: 'dual' },
					perEngineState: { A: 'webtransport', B: 'disconnected' },
				},
			});
			const dots = container.querySelectorAll('.engine-dot');
			expect(dots.length).toBe(2);
			expect(dots[0].classList.contains('healthy')).toBe(true);
			expect(dots[1].classList.contains('offline')).toBe(true);
		});

		it('does not show dots in single-engine mode', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
				},
			});
			const dots = container.querySelectorAll('.engine-dot');
			expect(dots.length).toBe(0);
		});

		it('does not show dots when engineStatus mode is single', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: { mode: 'single' },
					perEngineState: { A: 'webtransport', B: 'webtransport' },
				},
			});
			const dots = container.querySelectorAll('.engine-dot');
			expect(dots.length).toBe(0);
		});

		it('shows dots alongside engine warning banner', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: {
						mode: 'dual',
						engineDown: 'B',
						reason: 'unreachable',
					},
					perEngineState: { A: 'webtransport', B: 'disconnected' },
				},
			});
			// Warning banner should be visible
			const banner = container.querySelector('.connection-banner.engine-warning');
			expect(banner).toBeTruthy();
			// Per-engine dots should also be visible
			const dots = container.querySelectorAll('.engine-dot');
			expect(dots.length).toBe(2);
		});

		it('handles missing perEngineState gracefully in dual mode', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'webtransport',
					syncStatus: 'ok',
					engineStatus: { mode: 'dual' },
				},
			});
			const dots = container.querySelectorAll('.engine-dot');
			expect(dots.length).toBe(0);
		});
	});

	describe('auto-redirect on engine death', () => {
		beforeEach(() => {
			vi.useFakeTimers();
			sessionStorage.clear();
		});
		afterEach(() => {
			vi.useRealTimers();
		});

		it('shows countdown text when fallback URL is available and disconnected', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					engineFallbackUrl: 'https://engine-b.example.com',
					servingEngine: 'A',
				},
			});
			const banner = container.querySelector('.connection-banner.engine-fallback');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).toContain('redirecting to Engine B');
			expect(banner?.textContent).toContain('5s');
			expect(banner?.textContent).toContain('Go now');
			expect(banner?.textContent).toContain('Cancel');
		});

		it('does NOT auto-redirect if sessionStorage sf:redirect-attempts is already 1 (loop prevention)', () => {
			sessionStorage.setItem('sf:redirect-attempts', '1');
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					engineFallbackUrl: 'https://engine-b.example.com',
					servingEngine: 'A',
				},
			});
			// Should show manual fallback, not countdown
			const banner = container.querySelector('.connection-banner.engine-fallback');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).not.toContain('redirecting');
			expect(banner?.textContent).toContain('Reload from Engine B');
		});

		it('does NOT show redirect for single-engine mode (no engineFallbackUrl)', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
				},
			});
			// Should show standard disconnect overlay, not auto-redirect
			const overlay = container.querySelector('.disconnect-overlay');
			expect(overlay).toBeTruthy();
			const banner = container.querySelector('.connection-banner.engine-fallback');
			expect(banner).toBeFalsy();
		});

		it('does NOT auto-redirect during an active transition', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					engineFallbackUrl: 'https://engine-b.example.com',
					servingEngine: 'A',
					inTransition: true,
				},
			});
			// Should show manual fallback, not countdown — transition blocks auto-redirect
			const banner = container.querySelector('.connection-banner.engine-fallback');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).not.toContain('redirecting');
			expect(banner?.textContent).toContain('Reload from Engine B');
		});

		it('auto-redirects when transition completes', async () => {
			const { container, rerender } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					engineFallbackUrl: 'https://engine-b.example.com',
					servingEngine: 'A',
					inTransition: true,
				},
			});
			// During transition — no auto-redirect
			expect(container.querySelector('.connection-banner.engine-fallback')?.textContent).not.toContain('redirecting');

			// Transition ends
			await rerender({
				connectionState: 'disconnected',
				syncStatus: 'ok',
				engineFallbackUrl: 'https://engine-b.example.com',
				servingEngine: 'A',
				inTransition: false,
			});
			const banner = container.querySelector('.connection-banner.engine-fallback');
			expect(banner?.textContent).toContain('redirecting');
		});

		it('treats sessionStorage exception as already-redirected (private browsing safety)', () => {
			// Simulate private browsing by replacing the global sessionStorage
			// with a proxy that throws on getItem. This ensures the component
			// sees the throwing behavior during its $derived evaluation.
			const realSessionStorage = globalThis.sessionStorage;
			const throwingStorage = {
				...realSessionStorage,
				getItem: () => { throw new DOMException('Access denied'); },
				setItem: () => { throw new DOMException('Access denied'); },
				removeItem: () => { throw new DOMException('Access denied'); },
				clear: () => { throw new DOMException('Access denied'); },
				key: () => null,
				length: 0,
			};
			Object.defineProperty(globalThis, 'sessionStorage', {
				value: throwingStorage,
				writable: true,
				configurable: true,
			});

			try {
				const { container } = render(ConnectionBanner, {
					props: {
						connectionState: 'disconnected',
						syncStatus: 'ok',
						engineFallbackUrl: 'https://engine-b.example.com',
						servingEngine: 'A',
					},
				});
				// Should show manual fallback, not countdown — private browsing blocks auto-redirect
				const banner = container.querySelector('.connection-banner.engine-fallback');
				expect(banner).toBeTruthy();
				expect(banner?.textContent).not.toContain('redirecting');
				expect(banner?.textContent).toContain('Reload from Engine B');
			} finally {
				Object.defineProperty(globalThis, 'sessionStorage', {
					value: realSessionStorage,
					writable: true,
					configurable: true,
				});
			}
		});

		it('does NOT redirect to self (fallbackUrl === window.location.origin)', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					engineFallbackUrl: window.location.origin,
					servingEngine: 'A',
				},
			});
			// Self-redirect blocked, should show manual fallback
			const banner = container.querySelector('.connection-banner.engine-fallback');
			expect(banner).toBeTruthy();
			expect(banner?.textContent).not.toContain('redirecting');
			expect(banner?.textContent).toContain('Reload from Engine B');
		});

		it('includes auth token in fallback URL hash', () => {
			localStorage.setItem('switchframe_operator_token', 'test-token-abc');
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					engineFallbackUrl: 'https://engine-b.example.com',
					servingEngine: 'A',
				},
			});
			const link = container.querySelector('.fallback-link') as HTMLAnchorElement;
			expect(link).toBeTruthy();
			expect(link.href).toContain('#token=test-token-abc');
		});

		it('preserves original hash when no auth token exists', () => {
			localStorage.removeItem('switchframe_operator_token');
			// Set a hash on the window to verify it's preserved
			const origHash = window.location.hash;
			window.location.hash = '#somehash';
			try {
				sessionStorage.setItem('sf:redirect-attempts', '1');
				const { container } = render(ConnectionBanner, {
					props: {
						connectionState: 'disconnected',
						syncStatus: 'ok',
						engineFallbackUrl: 'https://engine-b.example.com',
						servingEngine: 'A',
					},
				});
				const link = container.querySelector('.fallback-link') as HTMLAnchorElement;
				expect(link).toBeTruthy();
				expect(link.href).toContain('#somehash');
			} finally {
				window.location.hash = origHash;
			}
		});
	});

	describe('control plane fallback link', () => {
		beforeEach(() => {
			vi.useFakeTimers();
		});
		afterEach(() => {
			vi.useRealTimers();
		});

		it('does not show fallback link immediately on disconnect', () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					sessionFallbackUrl: 'https://control-plane.switchframe.dev/session/abc123',
				},
			});
			const link = container.querySelector('.cp-fallback-link');
			expect(link).toBeFalsy();
		});

		it('shows fallback link after 15 seconds of disconnect', async () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					sessionFallbackUrl: 'https://control-plane.switchframe.dev/session/abc123',
				},
			});
			await vi.advanceTimersByTimeAsync(15000);
			const link = container.querySelector('.cp-fallback-link');
			expect(link).toBeTruthy();
			expect(link?.getAttribute('href')).toBe('https://control-plane.switchframe.dev/session/abc123');
			expect(link?.textContent).toContain('session recovery');
		});

		it('does not show fallback link when sessionFallbackUrl is null', async () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					sessionFallbackUrl: null,
				},
			});
			await vi.advanceTimersByTimeAsync(15000);
			const link = container.querySelector('.cp-fallback-link');
			expect(link).toBeFalsy();
		});

		it('hides fallback link when connection is restored', async () => {
			const { container, rerender } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					sessionFallbackUrl: 'https://control-plane.switchframe.dev/session/abc123',
				},
			});
			await vi.advanceTimersByTimeAsync(15000);
			expect(container.querySelector('.cp-fallback-link')).toBeTruthy();

			await rerender({ connectionState: 'webtransport', syncStatus: 'ok', sessionFallbackUrl: 'https://control-plane.switchframe.dev/session/abc123' });
			expect(container.querySelector('.cp-fallback-link')).toBeFalsy();
		});

		it('does not show fallback link when engine fallback is active', async () => {
			const { container } = render(ConnectionBanner, {
				props: {
					connectionState: 'disconnected',
					syncStatus: 'ok',
					engineFallbackUrl: 'https://engine-b.example.com',
					servingEngine: 'A',
					sessionFallbackUrl: 'https://control-plane.switchframe.dev/session/abc123',
				},
			});
			await vi.advanceTimersByTimeAsync(15000);
			// Engine fallback takes priority; disconnect timer should not run
			const link = container.querySelector('.cp-fallback-link');
			expect(link).toBeFalsy();
		});
	});
});
