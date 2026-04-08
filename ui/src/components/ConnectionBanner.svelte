<script lang="ts">
	import { getAuthToken } from '$lib/api/switch-api';

	interface EngineStatus {
		mode: 'dual' | 'single';
		engineDown?: 'A' | 'B';
		reason?: 'unreachable' | 'offline';
		provisioning?: boolean;
		restored?: boolean;
	}

	interface PerEngineState {
		A: 'webtransport' | 'polling' | 'disconnected';
		B: 'webtransport' | 'polling' | 'disconnected';
	}

	interface Props {
		connectionState: 'webtransport' | 'polling' | 'disconnected';
		syncStatus: 'ok' | 'resyncing' | 'disconnected';
		// New optional props for dual-engine mode:
		engineFallbackUrl?: string | null;  // URL of the other engine (null = single-engine mode)
		servingEngine?: string | null;      // ID of the engine that served this page ('A' or 'B')
		engineStatus?: EngineStatus;
		perEngineState?: PerEngineState;
		sessionFallbackUrl?: string | null; // Control plane session recovery URL
		inTransition?: boolean;             // Block auto-redirect during active transition
	}

	let { connectionState, syncStatus, engineFallbackUrl = null, servingEngine = null, engineStatus, perEngineState, sessionFallbackUrl = null, inTransition = false }: Props = $props();

	// Track whether WebTransport was ever connected so we only show
	// the polling banner as a degradation, not on initial fallback.
	let hadWebTransport = $state(false);
	$effect(() => {
		if (connectionState === 'webtransport') hadWebTransport = true;
	});

	let isDisconnected = $derived(
		connectionState === 'disconnected' || syncStatus === 'disconnected'
	);
	let isResyncing = $derived(!isDisconnected && syncStatus === 'resyncing');
	let isPolling = $derived(
		!isDisconnected && !isResyncing && hadWebTransport && connectionState === 'polling' && syncStatus === 'ok'
	);

	const otherEngine = $derived(servingEngine === 'A' ? 'B' : 'A');
	const showEngineFallback = $derived(
		engineFallbackUrl != null && connectionState === 'disconnected'
	);

	// Build full fallback URL preserving current query params and hash.
	// Inject auth token so Engine B can authenticate without re-login
	// (localStorage is origin-specific, so the token would be lost).
	const fallbackHref = $derived((() => {
		if (!engineFallbackUrl) return '';
		try {
			const url = new URL(engineFallbackUrl);
			url.search = window.location.search;
			const token = getAuthToken();
			if (token) {
				url.hash = `token=${token}`;
			} else {
				url.hash = window.location.hash;
			}
			return url.toString();
		} catch {
			return engineFallbackUrl;
		}
	})());

	// Dual-engine status
	const isDualMode = $derived(engineStatus?.mode === 'dual');
	const showPerEngineDots = $derived(isDualMode && perEngineState != null);
	const survivingEngine = $derived(engineStatus?.engineDown === 'A' ? 'B' : 'A');

	const showEngineWarning = $derived(
		isDualMode && engineStatus?.engineDown != null && engineStatus?.reason != null && !engineStatus?.restored
	);

	let disconnectedDuration = $state(0);
	let disconnectTimer: ReturnType<typeof setInterval> | undefined;

	$effect(() => {
		if (isDisconnected && !showEngineFallback && !showEngineWarning) {
			if (!disconnectTimer) {
				disconnectedDuration = 0;
				disconnectTimer = setInterval(() => {
					disconnectedDuration++;
				}, 1000);
			}
		} else {
			if (disconnectTimer) {
				clearInterval(disconnectTimer);
				disconnectTimer = undefined;
			}
			disconnectedDuration = 0;
		}
		return () => {
			if (disconnectTimer) clearInterval(disconnectTimer);
		};
	});

	const showControlPlaneFallback = $derived(
		disconnectedDuration >= 8 && sessionFallbackUrl != null
	);

	let showRestoredBanner = $state(false);
	$effect(() => {
		if (isDualMode && engineStatus?.restored) {
			showRestoredBanner = true;
			const timer = setTimeout(() => {
				showRestoredBanner = false;
			}, 3000);
			return () => clearTimeout(timer);
		} else {
			showRestoredBanner = false;
		}
	});

	// Auto-redirect loop prevention — uses reactive $state flag so cancelRedirect()
	// immediately prevents re-triggering even if connection flaps.
	let userCancelledRedirect = $state(false);

	const hasRedirectedBefore = $derived.by(() => {
		if (userCancelledRedirect) return true;
		try {
			return (parseInt(sessionStorage.getItem('sf:redirect-attempts') || '0', 10) > 0);
		} catch {
			return true; // Default to true to prevent infinite redirect loops in private browsing
		}
	});

	const isSelfRedirect = $derived.by(() => {
		if (!fallbackHref) return false;
		try {
			const fallbackOrigin = new URL(fallbackHref).origin;
			return fallbackOrigin === window.location.origin;
		} catch {
			return false;
		}
	});

	const shouldAutoRedirect = $derived(
		showEngineFallback && !hasRedirectedBefore && !isSelfRedirect && !inTransition
	);

	let redirectCountdown = $state(0);
	let redirectTimer: ReturnType<typeof setInterval> | undefined;

	$effect(() => {
		if (shouldAutoRedirect && fallbackHref) {
			redirectCountdown = 10;
			redirectTimer = setInterval(() => {
				redirectCountdown--;
				if (redirectCountdown <= 0) {
					if (redirectTimer) {
						clearInterval(redirectTimer);
						redirectTimer = undefined;
					}
					try { sessionStorage.setItem('sf:redirect-attempts', '1'); } catch {}
					window.location.replace(fallbackHref);
				}
			}, 1000);
		} else {
			if (redirectTimer) {
				clearInterval(redirectTimer);
				redirectTimer = undefined;
			}
			redirectCountdown = 0;
		}
		return () => {
			if (redirectTimer) {
				clearInterval(redirectTimer);
				redirectTimer = undefined;
			}
		};
	});

	function cancelRedirect() {
		if (redirectTimer) {
			clearInterval(redirectTimer);
			redirectTimer = undefined;
		}
		userCancelledRedirect = true;
		try { sessionStorage.setItem('sf:redirect-attempts', '1'); } catch {}
		redirectCountdown = 0;
	}

	const engineWarningText = $derived.by(() => {
		if (!engineStatus?.engineDown) return '';
		const engine = engineStatus.engineDown;
		if (engineStatus.reason === 'unreachable') {
			return `Engine ${engine} unreachable \u2014 commands routing to Engine ${survivingEngine} only`;
		}
		if (engineStatus.provisioning) {
			return `Engine ${engine} offline \u2014 provisioning replacement...`;
		}
		if (engineStatus.reason === 'offline') {
			return `Engine ${engine} offline \u2014 redundancy degraded. Restoring...`;
		}
		return '';
	});
</script>

{#if showPerEngineDots}
	<div class="engine-status-strip" role="status" aria-label="Per-engine connection status">
		<span
			class="engine-dot"
			class:healthy={perEngineState?.A === 'webtransport'}
			class:degraded={perEngineState?.A === 'polling'}
			class:offline={perEngineState?.A === 'disconnected'}
			title="Engine A: {perEngineState?.A}"
		>A</span>
		<span
			class="engine-dot"
			class:healthy={perEngineState?.B === 'webtransport'}
			class:degraded={perEngineState?.B === 'polling'}
			class:offline={perEngineState?.B === 'disconnected'}
			title="Engine B: {perEngineState?.B}"
		>B</span>
	</div>
{/if}

{#if showRestoredBanner}
	<div class="connection-banner engine-restored" role="status" aria-live="polite">
		Redundancy restored
	</div>
{:else if showEngineWarning}
	<div class="connection-banner engine-warning" role="alert" aria-live="assertive">
		{engineWarningText}
	</div>
{:else if shouldAutoRedirect}
	<div class="connection-banner engine-fallback" role="alert" aria-live="assertive">
		Engine {servingEngine} unreachable — redirecting to Engine {otherEngine} in {redirectCountdown}s
		<a href={fallbackHref} class="fallback-link" onclick={() => { try { sessionStorage.setItem('sf:redirect-attempts', '1'); } catch {} }}>Go now</a>
		<button class="cancel-redirect" onclick={cancelRedirect}>Cancel</button>
	</div>
{:else if showEngineFallback}
	<div class="connection-banner engine-fallback" role="alert" aria-live="assertive">
		Engine {servingEngine} unreachable —
		<a href={fallbackHref} class="fallback-link">
			Reload from Engine {otherEngine}
			<span class="fallback-hint">(opens new page)</span>
		</a>
	</div>
{:else if isDisconnected}
	<div class="disconnect-overlay" role="alertdialog" aria-live="assertive" aria-modal="true">
		<div class="disconnect-content">
			<span class="disconnect-text">CONNECTION LOST</span>
			<span class="disconnect-sub">Reconnecting...</span>
			{#if showControlPlaneFallback}
				<a href={sessionFallbackUrl} class="cp-fallback-link">Open session recovery page</a>
			{/if}
		</div>
	</div>
{:else if isResyncing}
	<div class="connection-banner resyncing" role="status" aria-live="polite">
		Resyncing with server...
	</div>
{:else if isPolling}
	<div class="connection-banner polling" role="status" aria-live="polite">
		Low-latency connection lost — using fallback
	</div>
{/if}

<style>
	.connection-banner {
		position: fixed;
		top: 0;
		left: 0;
		right: 0;
		z-index: var(--z-banner);
		padding: 8px;
		text-align: center;
		font-weight: bold;
		font-size: 0.875rem;
	}

	.connection-banner.polling {
		background: var(--color-warning);
		color: #fff;
	}

	.connection-banner.resyncing {
		background: var(--accent-yellow);
		color: #000;
	}

	.connection-banner.engine-fallback {
		background: var(--accent-yellow-dim);
		border-bottom: 1px solid rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
	}

	.connection-banner.engine-warning {
		background: var(--accent-yellow-dim, rgba(234, 179, 8, 0.15));
		border-bottom: 1px solid rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow, #eab308);
	}

	.connection-banner.engine-restored {
		background: var(--tally-preview-dim);
		border-bottom: 1px solid var(--tally-preview-medium);
		color: var(--tally-preview);
	}

	.fallback-link {
		color: #fff;
		text-decoration: underline;
		font-weight: 600;
	}

	.fallback-link:hover {
		color: var(--accent-yellow);
	}

	.fallback-hint {
		font-size: 0.75em;
		opacity: 0.7;
	}

	.cancel-redirect {
		background: none;
		border: none;
		color: inherit;
		text-decoration: underline;
		cursor: pointer;
		font-size: inherit;
		font-weight: inherit;
		padding: 0;
		margin-left: 8px;
		opacity: 0.8;
	}

	.cancel-redirect:hover {
		opacity: 1;
	}

	.engine-status-strip {
		position: fixed;
		top: 6px;
		right: 8px;
		z-index: calc(var(--z-banner) + 1);
		display: flex;
		gap: 4px;
		align-items: center;
	}

	.engine-dot {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 20px;
		height: 20px;
		border-radius: 4px;
		font-size: 0.65rem;
		font-weight: 700;
		line-height: 1;
		color: #fff;
		background: #555;
	}

	.engine-dot.healthy {
		background: var(--tally-preview, #22c55e);
	}

	.engine-dot.degraded {
		background: var(--accent-yellow, #eab308);
	}

	.engine-dot.offline {
		background: #ef4444;
	}

	.disconnect-overlay {
		position: fixed;
		inset: 0;
		background: var(--overlay-opaque);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: var(--z-fullscreen);
	}

	.disconnect-content {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
	}

	.disconnect-text {
		color: #ff4444;
		font-size: var(--text-2xl);
		font-weight: bold;
	}

	.disconnect-sub {
		color: #aaa;
		font-size: var(--text-base);
	}

	.cp-fallback-link {
		color: #6ea8fe;
		text-decoration: underline;
		font-size: var(--text-sm);
		margin-top: 12px;
	}
</style>
