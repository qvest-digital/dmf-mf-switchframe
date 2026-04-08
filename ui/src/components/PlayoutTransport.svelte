<script lang="ts">
	import { onDestroy } from 'svelte';
	import type { PlayoutChannelState, CacheStatus } from '$lib/api/types';
	import {
		playoutStart, playoutStop, playoutNext, playoutPrevious,
		playoutTakeManual, playoutReturnToAuto, apiCall,
	} from '$lib/api/switch-api';
	import {
		ICON_PLAY, ICON_STOP, ICON_FRAME_BACK, ICON_FRAME_FORWARD,
		ICON_RECORD,
		renderIcon,
	} from '$lib/icons';

	interface Props {
		channel: PlayoutChannelState;
		onCueTop?: () => void;
		isDualEngine?: boolean;
		getCacheStatus?: (engine: 'A' | 'B') => Promise<CacheStatus>;
	}
	let { channel, onCueTop, isDualEngine = false, getCacheStatus }: Props = $props();

	const isOnAir = $derived(channel.state === 'on_air');
	const isCued = $derived(channel.state === 'cued');
	const isStopped = $derived(channel.state === 'stopped' || channel.state === 'completed');
	const isAuto = $derived(channel.mode === 'automated' || !channel.mode);
	const isManual = $derived(channel.mode === 'manual');
	const isBreak = $derived(channel.mode === 'break');

	// ── Two-stage stop confirmation ──
	let confirmingStop = $state(false);
	let confirmTimer: ReturnType<typeof setTimeout> | null = null;

	function clearConfirmTimer() {
		if (confirmTimer) {
			clearTimeout(confirmTimer);
			confirmTimer = null;
		}
		confirmingStop = false;
	}

	// ── Dual-engine cache warning ──
	interface CacheWarning {
		engine: 'A' | 'B';
		fetchingCount: number;
	}
	let cacheWarnings: CacheWarning[] = $state([]);

	async function checkDualEngineCache(): Promise<void> {
		if (!isDualEngine || !getCacheStatus) return;
		const warnings: CacheWarning[] = [];
		try {
			const [statusA, statusB] = await Promise.all([
				getCacheStatus('A'),
				getCacheStatus('B'),
			]);
			if (statusA.fetchingCount > 0) {
				warnings.push({ engine: 'A', fetchingCount: statusA.fetchingCount });
			}
			if (statusB.fetchingCount > 0) {
				warnings.push({ engine: 'B', fetchingCount: statusB.fetchingCount });
			}
		} catch {
			// Cache check is best-effort; don't block playback
		}
		cacheWarnings = warnings;
	}

	function handlePlay() {
		checkDualEngineCache();
		apiCall(playoutStart(channel.id), 'Playout start');
	}

	function handleStop() {
		if (isOnAir && !confirmingStop) {
			confirmingStop = true;
			confirmTimer = setTimeout(() => { confirmingStop = false; }, 3000);
			return;
		}
		clearConfirmTimer();
		apiCall(playoutStop(channel.id), 'Playout stop');
	}

	function handleNext() {
		apiCall(playoutNext(channel.id), 'Playout next');
	}

	function handlePrevious() {
		apiCall(playoutPrevious(channel.id), 'Playout previous');
	}

	function handleTakeManual() {
		apiCall(playoutTakeManual(channel.id), 'Take manual');
	}

	function handleReturnToAuto() {
		apiCall(playoutReturnToAuto(channel.id), 'Return to auto');
	}

	// ── Elapsed time display ──
	let onAirSince = $state(0);
	let elapsed = $state('0:00');

	function formatElapsed(ms: number): string {
		const totalSec = Math.max(0, Math.floor(ms / 1000));
		const h = Math.floor(totalSec / 3600);
		const m = Math.floor((totalSec % 3600) / 60);
		const s = totalSec % 60;
		if (h > 0) {
			return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
		}
		return `${m}:${String(s).padStart(2, '0')}`;
	}

	$effect(() => {
		if (!isOnAir) {
			elapsed = '0:00';
			onAirSince = 0;
			return;
		}
		if (onAirSince === 0) {
			onAirSince = Date.now();
		}
		const interval = setInterval(() => {
			elapsed = formatElapsed(Date.now() - onAirSince);
		}, 1000);
		return () => clearInterval(interval);
	});

	// ── Cleanup on unmount ──
	onDestroy(() => {
		clearConfirmTimer();
	});
</script>

<div class="transport">
	<div class="transport-row">
		<button class="t-btn" onclick={handlePrevious} disabled={isStopped} title="Previous item">
			{@html renderIcon(ICON_FRAME_BACK, 13)}
		</button>
		<button
			class="t-btn t-main"
			class:on-air={isOnAir}
			class:cued={isCued}
			class:stopped={isStopped}
			onclick={handlePlay}
			disabled={isOnAir && isAuto}
			title={isOnAir ? 'On Air' : 'Start playout'}
		>
			{#if isOnAir}
				{@html renderIcon(ICON_RECORD, 16, 'fill')}
			{:else}
				{@html renderIcon(ICON_PLAY, 16, 'fill')}
			{/if}
		</button>
		<button
			class="t-btn t-stop"
			class:confirming={confirmingStop}
			onclick={handleStop}
			disabled={isStopped}
			title={confirmingStop ? 'Click again to confirm stop' : 'Stop playout'}
		>
			{#if confirmingStop}
				CONFIRM STOP
			{:else}
				{@html renderIcon(ICON_STOP, 13, 'fill')}
			{/if}
		</button>
		<button class="t-btn t-next" onclick={handleNext} disabled={isStopped} title="Next item">
			{@html renderIcon(ICON_FRAME_FORWARD, 15)}
		</button>
		{#if (isStopped || channel.state === 'completed') && onCueTop}
			<button class="t-btn t-cue-top" onclick={onCueTop} title="Cue to first item">
				CUE TOP
			</button>
		{/if}
		{#if isOnAir}
			<span class="elapsed" title="Elapsed time since on-air">{elapsed}</span>
		{/if}
	</div>

	{#if cacheWarnings.length > 0}
		<div class="cache-warning" class:cache-warning-on-air={isOnAir}>
			{#if isOnAir}
				<span class="cache-warning-alert">CACHE NOT READY</span>
			{/if}
			{#each cacheWarnings as w}
				<span class="cache-warning-item">Engine {w.engine}: {w.fetchingCount} item{w.fetchingCount !== 1 ? 's' : ''} still caching</span>
			{/each}
		</div>
	{/if}

	<div class="mode-row">
		<span class="row-label">MODE</span>
		<span class="mode-pill" class:auto={isAuto} class:manual={isManual} class:break-mode={isBreak}>
			{#if isAuto}AUTOMATED{:else if isManual}MANUAL{:else if isBreak}BREAK{:else}--{/if}
		</span>
		{#if isAuto}
			<button class="mode-btn" onclick={handleTakeManual}>TAKE MANUAL</button>
		{:else}
			<button class="mode-btn mode-btn-auto" onclick={handleReturnToAuto}>RETURN TO AUTO</button>
		{/if}
	</div>
</div>

<style>
	.transport {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.transport-row {
		display: flex;
		gap: 3px;
		align-items: center;
	}

	.t-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		min-width: 30px;
		height: 28px;
		padding: 0 5px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast);
	}
	.t-btn:hover:not(:disabled) { background: var(--bg-hover); border-color: var(--border-strong); color: var(--text-primary); }
	.t-btn:active:not(:disabled) { background: var(--bg-base); box-shadow: var(--shadow-inset); }
	.t-btn:disabled { opacity: 0.25; cursor: default; }

	.t-main {
		min-width: 42px;
		height: 32px;
	}
	.t-main.stopped { border-color: var(--accent-orange-medium); color: var(--accent-orange); }
	.t-main.stopped:hover:not(:disabled) { background: var(--accent-orange-dim); border-color: var(--accent-orange); }
	.t-main.cued { background: rgba(59, 130, 246, 0.1); border-color: rgba(59, 130, 246, 0.5); color: rgba(96, 165, 250, 1); }
	.t-main.on-air { background: rgba(220, 38, 38, 0.15); border-color: var(--tally-program); color: var(--tally-program); }

	.t-next {
		min-width: 34px;
		height: 30px;
	}

	.t-cue-top {
		padding: 0 8px;
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 700;
		letter-spacing: 0.06em;
		color: rgba(96, 165, 250, 1);
		border-color: rgba(59, 130, 246, 0.4);
		background: rgba(59, 130, 246, 0.08);
	}
	.t-cue-top:hover:not(:disabled) {
		background: rgba(59, 130, 246, 0.15);
		border-color: rgba(59, 130, 246, 0.6);
	}

	.t-stop:hover:not(:disabled):not(.confirming) { color: var(--tally-program); border-color: rgba(220, 38, 38, 0.3); background: rgba(220, 38, 38, 0.1); }

	.t-stop.confirming {
		min-width: auto;
		padding: 0 8px;
		background: rgba(220, 38, 38, 0.25);
		border-color: var(--tally-program);
		color: var(--tally-program);
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 800;
		letter-spacing: 0.06em;
		animation: confirm-pulse 0.6s ease-in-out infinite;
	}

	@keyframes confirm-pulse {
		0%, 100% { background: rgba(220, 38, 38, 0.25); }
		50% { background: rgba(220, 38, 38, 0.45); }
	}

	.elapsed {
		font-family: var(--font-mono, monospace);
		font-size: 11px;
		font-weight: 600;
		color: var(--tally-program);
		margin-left: 4px;
		letter-spacing: 0.04em;
		white-space: nowrap;
	}

	.cache-warning {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 3px 6px;
		background: rgba(234, 168, 23, 0.1);
		border: 1px solid rgba(234, 168, 23, 0.3);
		border-radius: var(--radius-sm);
	}

	.cache-warning-on-air {
		background: rgba(220, 38, 38, 0.12);
		border-color: rgba(220, 38, 38, 0.5);
		animation: cache-alert-pulse 1s ease-in-out infinite;
	}

	@keyframes cache-alert-pulse {
		0%, 100% { background: rgba(220, 38, 38, 0.12); }
		50% { background: rgba(220, 38, 38, 0.25); }
	}

	.cache-warning-alert {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 800;
		color: var(--tally-program);
		letter-spacing: 0.08em;
	}

	.cache-warning-item {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		color: var(--accent-orange);
		letter-spacing: 0.02em;
	}

	.cache-warning-on-air .cache-warning-item {
		color: var(--tally-program);
	}

	.mode-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.row-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--text-tertiary);
		letter-spacing: 0.08em;
		white-space: nowrap;
		text-transform: uppercase;
	}

	.mode-pill {
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 700;
		letter-spacing: 0.08em;
	}

	.mode-pill.auto {
		background: rgba(22, 163, 74, 0.15);
		color: var(--tally-preview);
		border: 1px solid rgba(22, 163, 74, 0.3);
	}

	.mode-pill.manual {
		background: rgba(234, 168, 23, 0.15);
		color: var(--accent-orange);
		border: 1px solid rgba(234, 168, 23, 0.4);
		animation: mode-pulse 1.5s ease-in-out infinite;
	}

	.mode-pill.break-mode {
		background: rgba(220, 38, 38, 0.15);
		color: var(--tally-program);
		border: 1px solid rgba(220, 38, 38, 0.4);
		animation: mode-pulse 1.5s ease-in-out infinite;
	}

	@keyframes mode-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.7; }
	}

	.mode-btn {
		padding: 2px 6px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--text-secondary);
		letter-spacing: 0.04em;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast);
	}
	.mode-btn:hover { background: var(--bg-hover); border-color: var(--border-strong); color: var(--text-primary); }
	.mode-btn:active { background: var(--bg-base); box-shadow: var(--shadow-inset); }

	.mode-btn-auto {
		border-color: rgba(22, 163, 74, 0.3);
		color: var(--tally-preview);
	}
	.mode-btn-auto:hover { background: rgba(22, 163, 74, 0.1); border-color: rgba(22, 163, 74, 0.5); }
</style>
