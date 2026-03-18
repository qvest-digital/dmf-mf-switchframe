<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { getConfirmMode, setConfirmMode } from '$lib/state/preferences.svelte';
	import Clock from './Clock.svelte';
	import RecordingControl from './RecordingControl.svelte';
	import ConnectionStatus from './ConnectionStatus.svelte';

	type ConnectionIndicatorState = 'webtransport' | 'polling' | 'disconnected';

	interface Props {
		state: ControlRoomState;
		connectionState?: ConnectionIndicatorState;
		switchLayout?: () => void;
		onToggleIOPanel?: () => void;
		ioPanelVisible?: boolean;
		onToggleComms?: () => void;
		commsActive?: boolean;
	}
	let { state: crState, connectionState = 'disconnected', switchLayout, onToggleIOPanel, ioPanelVisible = false, onToggleComms, commsActive = false }: Props = $props();

	let thumbKey = $state(0);
	let thumbInterval: ReturnType<typeof setInterval> | undefined;

	const srtActive = $derived(crState.srtOutput?.active ?? false);
	const recActive = $derived(crState.recording?.active ?? false);
	const showConfidence = $derived(recActive || srtActive);

	// I/O button is active when panel is open OR any SRT output/destination is active
	const hasActiveDestination = $derived(
		(crState.destinations ?? []).some((d) => d.state === 'active' || d.state === 'starting')
	);
	const ioActive = $derived(ioPanelVisible || srtActive || hasActiveDestination);

	// I/O button shows warning when any SRT input is unhealthy or any output has errors
	const hasUnhealthySRTInput = $derived(
		Object.values(crState.sources).some(
			(s) => s.type === 'srt' && s.status !== 'healthy'
		)
	);
	const hasOutputError = $derived(
		(crState.destinations ?? []).some((d) => d.state === 'error')
	);
	const ioWarning = $derived(hasUnhealthySRTInput || hasOutputError);

	$effect(() => {
		if (showConfidence) {
			thumbInterval = setInterval(() => { thumbKey++; }, 1000);
		} else {
			if (thumbInterval) clearInterval(thumbInterval);
			thumbInterval = undefined;
		}
		return () => { if (thumbInterval) clearInterval(thumbInterval); };
	});
</script>

<div class="output-controls">
	<Clock />
	<ConnectionStatus state={connectionState} />
	{#if showConfidence}
		<img
			class="confidence-thumb"
			src="/api/output/confidence?t={thumbKey}"
			alt="Program output"
			width="80"
			height="45"
		/>
	{/if}
	<RecordingControl state={crState} />
	<button
		class="header-btn io-btn"
		class:io-active={ioActive}
		class:io-warning={ioWarning && !ioActive}
		onclick={() => onToggleIOPanel?.()}
	>I/O</button>
	<button
		class="header-btn comms-btn-header"
		class:comms-active={commsActive}
		onclick={() => onToggleComms?.()}
		title="Operator voice comms"
	>COMMS</button>
	<button
		class="header-btn confirm-btn"
		class:confirm-active={getConfirmMode()}
		onclick={() => setConfirmMode(!getConfirmMode())}
		title="Require double-press of Space or Shift+number for hot punches"
	>CONFIRM</button>
	{#if switchLayout}
		<button class="header-btn mode-btn" onclick={switchLayout} title="Switch layout mode">MODE</button>
	{/if}
</div>

<style>
	.output-controls {
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 4px 8px;
		font-family: var(--font-ui);
	}

	.header-btn {
		padding: 4px 10px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: var(--text-sm);
		letter-spacing: 0.04em;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.header-btn:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.io-active {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
		color: var(--accent-blue);
	}

	.io-warning {
		border-color: var(--color-warning);
		background: var(--color-warning-dim);
		color: var(--color-warning);
	}

	.comms-active {
		border-color: var(--color-green, #4ade80);
		background: rgba(74, 222, 128, 0.15);
		color: #fff;
	}

	.confirm-btn {
		font-size: var(--text-xs);
	}

	.confirm-active {
		border-color: var(--accent-orange);
		background: var(--accent-orange-dim);
		color: var(--accent-orange);
	}

	.confidence-thumb {
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		object-fit: cover;
		opacity: 0.85;
	}

	.confidence-thumb:hover {
		opacity: 1;
	}
</style>
