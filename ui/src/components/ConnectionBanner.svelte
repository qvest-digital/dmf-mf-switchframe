<script lang="ts">
	interface Props {
		connectionState: 'webtransport' | 'polling' | 'disconnected';
		syncStatus: 'ok' | 'resyncing' | 'disconnected';
	}

	let { connectionState, syncStatus }: Props = $props();

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
</script>

{#if isDisconnected}
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
