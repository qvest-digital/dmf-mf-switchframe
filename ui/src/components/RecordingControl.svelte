<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { startRecording, stopRecording, fireAndForget } from '$lib/api/switch-api';

	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();

	const isActive = $derived(state.recording?.active ?? false);
	const hasError = $derived(!isActive && !!state.recording?.error);

	const duration = $derived.by(() => {
		const secs = state.recording?.durationSecs ?? 0;
		const mins = Math.floor(secs / 60);
		const remainSecs = Math.floor(secs % 60);
		return `${String(mins).padStart(2, '0')}:${String(remainSecs).padStart(2, '0')}`;
	});

	function handleStart() {
		fireAndForget(startRecording());
	}

	function handleStop() {
		fireAndForget(stopRecording());
	}
</script>

{#if isActive}
	<div class="recording-control rec-active">
		<span class="rec-dot"></span>
		<span class="rec-label">REC</span>
		<span class="rec-duration">{duration}</span>
		<button class="rec-stop" onclick={handleStop}>STOP</button>
	</div>
{:else if hasError}
	<div class="recording-control rec-error">
		<span class="rec-label">REC</span>
		<span class="rec-error-text">{state.recording?.error}</span>
		<button class="rec-start" onclick={handleStart}>REC</button>
	</div>
{:else}
	<div class="recording-control">
		<button class="rec-start" onclick={handleStart}>REC</button>
	</div>
{/if}

<style>
	.recording-control {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.25rem 0.5rem;
		font-family: monospace;
		font-size: 0.85rem;
	}

	.rec-active {
		background: rgba(204, 0, 0, 0.15);
		border: 1px solid #cc0000;
		border-radius: 4px;
	}

	.rec-dot {
		display: inline-block;
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: #cc0000;
		animation: pulse 1s infinite;
	}

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.3; }
	}

	.rec-label {
		color: #cc0000;
		font-weight: bold;
	}

	.rec-duration {
		color: #ccc;
	}

	.rec-start {
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

	.rec-start:hover {
		border-color: #cc0000;
		background: #2a0000;
	}

	.rec-stop {
		padding: 0.3rem 0.6rem;
		border: 2px solid #cc0000;
		border-radius: 4px;
		background: #2a0000;
		color: #ff4444;
		cursor: pointer;
		font-family: monospace;
		font-weight: bold;
		font-size: 0.75rem;
	}

	.rec-stop:hover {
		background: #440000;
	}

	.rec-error {
		border: 1px solid #cc0000;
		border-radius: 4px;
	}

	.rec-error-text {
		color: #ff4444;
		font-size: 0.75rem;
	}
</style>
