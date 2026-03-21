<script lang="ts">
	import type { SRTSourceInfo } from '$lib/api/types';
	import { computeSRTHealth, formatUptime, formatBitrate } from '$lib/util/srt-health';
	import SRTHealthDot from './SRTHealthDot.svelte';

	interface Props {
		srt: SRTSourceInfo;
		sourceLabel: string;
		onclose: () => void;
	}

	let { srt, sourceLabel, onclose }: Props = $props();

	let health = $derived(computeSRTHealth(srt));

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

<div class="srt-popover" role="dialog" aria-label="SRT statistics for {sourceLabel}">
	<!-- Header -->
	<div class="popover-header">
		{#if health}
			<SRTHealthDot level={health} />
		{/if}
		<span class="popover-title">{sourceLabel}</span>
		<span class="popover-mode">{srt.mode}</span>
		<button class="popover-close" onclick={onclose} aria-label="Close">&times;</button>
	</div>

	<!-- Connection -->
	<div class="popover-section">
		<div class="section-title">Connection</div>
		{#if srt.remoteAddr}
			<div class="stat-row">
				<span class="stat-label">Remote</span>
				<span class="stat-value mono">{srt.remoteAddr}</span>
			</div>
		{/if}
		{#if srt.streamID}
			<div class="stat-row">
				<span class="stat-label">Stream ID</span>
				<span class="stat-value mono">{srt.streamID}</span>
			</div>
		{/if}
		<div class="stat-row">
			<span class="stat-label">State</span>
			<span class="stat-value {srt.connected ? 'val-green' : 'val-red'}">
				{srt.connected ? 'Connected' : 'Disconnected'}
			</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Latency</span>
			<span class="stat-value mono">{srt.latencyMs}ms / {srt.negotiatedLatencyMs}ms</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Uptime</span>
			<span class="stat-value">{formatUptime(srt.uptimeMs)}</span>
		</div>
		{#if (srt.reconnectCount ?? 0) > 0}
			<div class="stat-row">
				<span class="stat-label">Reconnects</span>
				<span class="stat-value val-yellow">{srt.reconnectCount}</span>
			</div>
		{/if}
	</div>

	<!-- Network -->
	<div class="popover-section">
		<div class="section-title">Network</div>
		<div class="stat-row">
			<span class="stat-label">Bitrate</span>
			<span class="stat-value mono">{formatBitrate(srt.bitrateKbps)}</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">RTT</span>
			<span class="stat-value mono {rttClass(srt.rttMs)}"
				>{srt.rttMs.toFixed(1)}ms (&plusmn;{srt.rttVarMs.toFixed(1)}ms)</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Loss Rate</span>
			<span class="stat-value mono {lossClass(srt.lossRate)}">{srt.lossRate.toFixed(2)}%</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Recv Buffer</span>
			<span class="stat-value mono {bufClass(srt.recvBufMs)}"
				>{srt.recvBufMs}ms ({srt.recvBufPackets} pkts)</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Flight Size</span>
			<span class="stat-value mono">{srt.flightSize} pkts</span>
		</div>
	</div>

	<!-- Packets -->
	<div class="popover-section">
		<div class="section-title">Packets</div>
		<div class="stat-row">
			<span class="stat-label">Received</span>
			<span class="stat-value mono">{srt.packetsReceived.toLocaleString()}</span>
		</div>
		<div class="stat-row">
			<span class="stat-label">Lost</span>
			<span class="stat-value mono {countClass(srt.packetsLost)}"
				>{srt.packetsLost.toLocaleString()}</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Dropped</span>
			<span class="stat-value mono {countClass(srt.packetsDropped)}"
				>{srt.packetsDropped.toLocaleString()}</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Retransmitted</span>
			<span class="stat-value mono {countClass(srt.packetsRetransmitted)}"
				>{srt.packetsRetransmitted.toLocaleString()}</span
			>
		</div>
		<div class="stat-row">
			<span class="stat-label">Belated</span>
			<span class="stat-value mono {countClass(srt.packetsBelated)}"
				>{srt.packetsBelated.toLocaleString()}</span
			>
		</div>
	</div>
</div>

<style>
	.popover-backdrop {
		position: fixed;
		inset: 0;
		z-index: 99;
	}

	.srt-popover {
		position: fixed;
		z-index: 100;
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
</style>
