<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import RecordingControl from './RecordingControl.svelte';
	import SRTOutputModal from './SRTOutputModal.svelte';

	interface Props { state: ControlRoomState; }
	let { state: crState }: Props = $props();

	let showSRTModal = $state(false);

	const srtActive = $derived(crState.srtOutput?.active ?? false);
</script>

<div class="output-controls">
	<RecordingControl state={crState} />
	<button
		class="srt-btn"
		class:srt-active={srtActive}
		onclick={() => showSRTModal = !showSRTModal}
	>SRT</button>
</div>

<SRTOutputModal state={crState} visible={showSRTModal} onclose={() => showSRTModal = false} />

<style>
	.output-controls {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.25rem 0.75rem;
		font-family: monospace;
	}

	.srt-btn {
		padding: 0.4rem 0.75rem;
		border: 2px solid #444;
		border-radius: 4px;
		background: #1a1a1a;
		color: #ccc;
		cursor: pointer;
		font-family: monospace;
		font-weight: bold;
		font-size: 0.85rem;
	}

	.srt-btn:hover {
		border-color: #4488ff;
		background: #1a2a44;
	}

	.srt-active {
		border-color: #4488ff;
		background: #1a2a44;
		color: #88bbff;
	}
</style>
