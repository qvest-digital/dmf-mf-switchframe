<script lang="ts">
	import type { PlayoutChannelState } from '$lib/api/types';
	import {
		playoutStart, playoutStop, playoutNext, playoutPrevious,
		playoutTakeManual, playoutReturnToAuto, apiCall,
	} from '$lib/api/switch-api';
	import {
		ICON_PLAY, ICON_STOP, ICON_FRAME_BACK, ICON_FRAME_FORWARD,
		renderIcon,
	} from '$lib/icons';

	interface Props {
		channel: PlayoutChannelState;
	}
	let { channel }: Props = $props();

	const isOnAir = $derived(channel.state === 'on_air');
	const isCued = $derived(channel.state === 'cued');
	const isStopped = $derived(channel.state === 'stopped' || channel.state === 'completed');
	const isAuto = $derived(channel.mode === 'automated' || !channel.mode);
	const isManual = $derived(channel.mode === 'manual');
	const isBreak = $derived(channel.mode === 'break');

	function handlePlay() {
		apiCall(playoutStart(channel.id), 'Playout start');
	}

	function handleStop() {
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
			{@html renderIcon(ICON_PLAY, 16, 'fill')}
		</button>
		<button
			class="t-btn t-stop"
			onclick={handleStop}
			disabled={isStopped}
			title="Stop playout"
		>
			{@html renderIcon(ICON_STOP, 13, 'fill')}
		</button>
		<button class="t-btn" onclick={handleNext} disabled={isStopped} title="Next item">
			{@html renderIcon(ICON_FRAME_FORWARD, 13)}
		</button>
	</div>

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

	.t-stop:hover:not(:disabled) { color: var(--tally-program); border-color: rgba(220, 38, 38, 0.3); background: rgba(220, 38, 38, 0.1); }

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
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.06em;
	}

	.mode-pill.auto {
		background: rgba(22, 163, 74, 0.12);
		color: var(--tally-preview);
	}

	.mode-pill.manual {
		background: var(--accent-orange-dim);
		color: var(--accent-orange);
	}

	.mode-pill.break-mode {
		background: rgba(220, 38, 38, 0.12);
		color: var(--tally-program);
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
