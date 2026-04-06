<script lang="ts">
	import type { SRTSourceInfo } from '$lib/api/types';
	import { computeSRTHealth, formatUptime, formatBitrate } from '$lib/util/srt-health';
	import SRTHealthDot from './SRTHealthDot.svelte';

	interface Props {
		srt: SRTSourceInfo;
		sourceLabel: string;
		x?: number;
		y?: number;
		onclose: () => void;
		engineBStats?: SRTSourceInfo | null;
	}

	let { srt, sourceLabel, x = 0, y = 0, onclose, engineBStats = null }: Props = $props();

	let isDual = $derived(!!engineBStats);

	const SINGLE_WIDTH = 330;
	const DUAL_WIDTH = 580;

	// Clamp position to keep popover in viewport
	let popoverStyle = $derived.by(() => {
		const popoverWidth = isDual ? DUAL_WIDTH : SINGLE_WIDTH;
		const maxX = typeof window !== 'undefined' ? window.innerWidth - popoverWidth : x;
		const maxY = typeof window !== 'undefined' ? window.innerHeight - 400 : y;
		const clampedX = Math.max(8, Math.min(x, maxX));
		const clampedY = Math.max(8, Math.min(y, maxY));
		return `top: ${clampedY}px; left: ${clampedX}px;`;
	});

	let healthA = $derived(computeSRTHealth(srt));
	let healthB = $derived(engineBStats ? computeSRTHealth(engineBStats) : undefined);

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') onclose();
	}

	function lossClass(rate: number): string {
		if (rate > 1.0) return 'val-red';
		if (rate > 0.1) return 'val-yellow';
		return '';
	}

	function rttClass(ms: number): string {
		if (ms > 200) return 'val-red';
		if (ms > 100) return 'val-yellow';
		return '';
	}

	function bufClass(ms: number): string {
		if (ms < 20) return 'val-yellow';
		return '';
	}

	function countClass(count: number): string {
		if (count > 0) return 'val-yellow';
		return '';
	}
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="popover-backdrop" onclick={onclose} onkeydown={() => {}}></div>

<div class="srt-popover" class:dual-engine={isDual} role="dialog" aria-label="SRT statistics for {sourceLabel}" style={popoverStyle}>
	<!-- Header -->
	<div class="popover-header">
		{#if !isDual && healthA}
			<SRTHealthDot level={healthA} />
		{/if}
		<span class="popover-title">{sourceLabel}</span>
		<span class="popover-mode">{srt.mode}</span>
		<button class="popover-close" onclick={onclose} aria-label="Close">&times;</button>
	</div>

	{#if isDual && engineBStats}
		<!-- Dual-engine column layout -->
		<div class="engine-columns">
			<!-- Engine A column -->
			<div class="engine-column">
				<div class="engine-header">
					{#if healthA}<SRTHealthDot level={healthA} />{/if}
					<span>Engine A</span>
				</div>
				{@render statsColumn(srt)}
			</div>
			<!-- Engine B column -->
			<div class="engine-column">
				<div class="engine-header">
					{#if healthB}<SRTHealthDot level={healthB} />{/if}
					<span>Engine B</span>
				</div>
				{@render statsColumn(engineBStats)}
			</div>
		</div>
	{:else}
		<!-- Single-engine layout (original) -->
		{@render statsColumn(srt)}
	{/if}
</div>

{#snippet statsColumn(s: SRTSourceInfo)}
	<!-- Connection -->
	<div class="popover-section">
		<div class="section-title">Connection</div>
		{#if s.remoteAddr}
			<div class="stat-row">
				<span class="stat-label">Remote</span>
				<span class="stat-value mono">{s.remoteAddr}</span>
			</div>
		{/if}
		{#if s.streamID}
			<div class="stat-row">
				<span class="stat-label">Stream ID</span>
				<span class="stat-value mono">{s.streamID}</span>
			</div>
		{/if}
		<div class="stat-row">
			<span class="stat-label">State</span>
			<span class="stat-value {s.connected ? 'val-green' : 'val-red'}">
				{s.connected ? 'Connected' : 'Disconnected'}
			</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Latency</span>
			<span class="stat-value mono">{s.latencyMs}ms / {s.negotiatedLatencyMs}ms</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Uptime</span>
			<span class="stat-value">{formatUptime(s.uptimeMs)}</span>
		</div>
		{#if (s.reconnectCount ?? 0) > 0}
			<div class="stat-row">
				<span class="stat-label">Reconnects</span>
				<span class="stat-value val-yellow">{s.reconnectCount}</span>
			</div>
		{/if}
	</div>

	<!-- Network -->
	<div class="popover-section">
		<div class="section-title">Network</div>
		<div class="stat-row">
			<span class="stat-label">Bitrate</span>
			<span class="stat-value mono">{formatBitrate(s.bitrateKbps)}</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">RTT</span>
			<span class="stat-value mono {rttClass(s.rttMs)}"
				>{s.rttMs.toFixed(1)}ms (&plusmn;{s.rttVarMs.toFixed(1)}ms)</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Loss Rate</span>
			<span class="stat-value mono {lossClass(s.lossRate)}">{s.lossRate.toFixed(2)}%</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Recv Buffer</span>
			<span class="stat-value mono {bufClass(s.recvBufMs)}"
				>{s.recvBufMs.toFixed(1)}ms ({s.recvBufPackets} pkts)</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Flight Size</span>
			<span class="stat-value mono">{s.flightSize} pkts</span>
		</div>
	</div>

	<!-- Packets -->
	<div class="popover-section">
		<div class="section-title">Packets</div>
		<div class="stat-row">
			<span class="stat-label">Received</span>
			<span class="stat-value mono">{s.packetsReceived.toLocaleString()}</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Lost</span>
			<span class="stat-value mono {countClass(s.packetsLost)}"
				>{s.packetsLost.toLocaleString()}</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Dropped</span>
			<span class="stat-value mono {countClass(s.packetsDropped)}"
				>{s.packetsDropped.toLocaleString()}</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Retransmitted</span>
			<span class="stat-value mono {countClass(s.packetsRetransmitted)}"
				>{s.packetsRetransmitted.toLocaleString()}</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Belated</span>
			<span class="stat-value mono {countClass(s.packetsBelated)}"
				>{s.packetsBelated.toLocaleString()}</span
			>
		</div>
	</div>
{/snippet}

<style>
	.popover-backdrop {
		position: fixed;
		inset: 0;
		z-index: var(--z-overlay);
	}

	.srt-popover {
		position: fixed;
		z-index: calc(var(--z-overlay) + 1);
		min-width: 260px;
		max-width: 320px;
		background: var(--bg-panel);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-md);
		box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
		overflow: hidden;
	}

	.popover-header {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 8px 10px;
		border-bottom: 1px solid var(--border-strong);
	}

	.popover-title {
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--text-primary);
		flex: 1;
	}

	.popover-mode {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.5px;
		padding: 1px 6px;
		border-radius: 3px;
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}

	.popover-close {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 16px;
		line-height: 1;
		padding: 0 2px;
	}

	.popover-close:hover {
		color: var(--text-primary);
	}

	.popover-section {
		padding: 6px 10px 8px;
	}

	.section-title {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.8px;
		color: var(--text-tertiary);
		margin-bottom: 4px;
	}

	.stat-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		padding: 1px 0;
	}

	.stat-label {
		font-size: var(--text-xs);
		color: var(--text-secondary);
	}

	.stat-value {
		font-size: var(--text-xs);
		color: var(--text-primary);
		text-align: right;
	}

	.mono {
		font-family: var(--font-mono);
	}

	.val-green {
		color: #22c55e;
	}

	.val-yellow {
		color: #eab308;
	}

	.val-red {
		color: #ef4444;
	}

	/* Dual-engine layout */
	.srt-popover.dual-engine {
		max-width: 580px;
	}

	.engine-columns {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 0;
	}

	.engine-column {
		min-width: 0;
	}

	.engine-column + .engine-column {
		border-left: 1px solid var(--border-strong);
	}

	.engine-header {
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 4px 10px;
		font-size: var(--text-xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-secondary);
		background: var(--bg-elevated);
		border-bottom: 1px solid var(--border-strong);
	}
</style>
