<script lang="ts">
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
	}

	let { connectionState, syncStatus, engineFallbackUrl = null, servingEngine = null, engineStatus, perEngineState }: Props = $props();

	const showPerEngineDots = $derived(isDualMode && perEngineState != null);

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

	// Dual-engine status
	const isDualMode = $derived(engineStatus?.mode === 'dual');
	const survivingEngine = $derived(engineStatus?.engineDown === 'A' ? 'B' : 'A');

	const showEngineWarning = $derived(
		isDualMode && engineStatus?.engineDown != null && engineStatus?.reason != null && !engineStatus?.restored
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
{:else if showEngineFallback}
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

	.engine-status-strip {
		position: fixed;
		top: 6px;
		right: 8px;
		z-index: var(--z-banner);
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
</style>
