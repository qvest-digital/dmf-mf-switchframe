<script lang="ts">
	interface Props {
		connectionState: 'webtransport' | 'polling' | 'disconnected';
		syncStatus: 'ok' | 'resyncing' | 'disconnected';
		// New optional props for dual-engine mode:
		engineFallbackUrl?: string | null;  // URL of the other engine (null = single-engine mode)
		servingEngine?: string | null;      // ID of the engine that served this page ('A' or 'B')
	}

	let { connectionState, syncStatus, engineFallbackUrl = null, servingEngine = null }: Props = $props();

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
</script>

{#if showEngineFallback}
	<div class="connection-banner engine-fallback" role="alert" aria-live="assertive">
		Engine {servingEngine} unreachable —
		<a href={engineFallbackUrl} class="fallback-link">Open via Engine {otherEngine}</a>
	</div>
{:else if isDisconnected}
	<div class="disconnect-overlay" role="alertdialog" aria-live="assertive" aria-modal="true">
		<div class="disconnect-content">
			<span class="disconnect-text">CONNECTION LOST</span>
			<span class="disconnect-sub">Reconnecting...</span>
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
		background: #ccaa00;
		color: #000;
	}

	.connection-banner.engine-fallback {
		background: var(--accent-yellow-dim);
		border-bottom: 1px solid rgba(234, 179, 8, 0.3);
		color: var(--accent-yellow);
	}

	.fallback-link {
		color: #fff;
		text-decoration: underline;
		font-weight: 600;
	}

	.fallback-link:hover {
		color: var(--accent-yellow);
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
</style>
