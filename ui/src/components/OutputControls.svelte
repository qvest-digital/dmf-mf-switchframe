<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import Clock from './Clock.svelte';
	import RecordingControl from './RecordingControl.svelte';
	import SRTOutputModal from './SRTOutputModal.svelte';
	import ConnectionStatus from './ConnectionStatus.svelte';

	type ConnectionIndicatorState = 'webtransport' | 'polling' | 'disconnected';

	interface Props { state: ControlRoomState; connectionState?: ConnectionIndicatorState; switchLayout?: () => void; }
	let { state: crState, connectionState = 'disconnected', switchLayout }: Props = $props();

	let showSRTModal = $state(false);

	const srtActive = $derived(crState.srtOutput?.active ?? false);
</script>

<div class="output-controls">
	<Clock />
	<ConnectionStatus state={connectionState} />
	<RecordingControl state={crState} />
	<button
		class="header-btn"
		class:srt-active={srtActive}
		onclick={() => showSRTModal = !showSRTModal}
	>SRT</button>
	{#if switchLayout}
		<button class="header-btn mode-btn" onclick={switchLayout} title="Switch layout mode">MODE</button>
	{/if}
</div>

<SRTOutputModal state={crState} visible={showSRTModal} onclose={() => showSRTModal = false} />

<style>
	.output-controls {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 5px 10px;
		font-family: var(--font-ui);
	}

	.header-btn {
		padding: 5px 12px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 0.75rem;
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

	.srt-active {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
		color: var(--accent-blue);
	}

	.mode-btn {
		margin-left: auto;
	}
</style>
