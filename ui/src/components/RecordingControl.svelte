<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { startRecording, stopRecording, apiCall } from '$lib/api/switch-api';
	import ConfirmDialog from './ConfirmDialog.svelte';

	interface Props { state: ControlRoomState; }
	let { state: crState }: Props = $props();

	const isActive = $derived(crState.recording?.active ?? false);
	const hasError = $derived(!isActive && !!crState.recording?.error);
	let confirmingStop = $state(false);

	const duration = $derived.by(() => {
		const secs = crState.recording?.durationSecs ?? 0;
		const mins = Math.floor(secs / 60);
		const remainSecs = Math.floor(secs % 60);
		return `${String(mins).padStart(2, '0')}:${String(remainSecs).padStart(2, '0')}`;
	});

	function handleStart() {
		apiCall(startRecording(), 'Recording failed');
	}

	function handleStop() {
		confirmingStop = true;
	}

	function confirmStop() {
		apiCall(stopRecording(), 'Stop recording failed');
		confirmingStop = false;
	}

	function cancelStop() {
		confirmingStop = false;
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
		<span class="rec-error-text">{crState.recording?.error}</span>
		<button class="rec-start" onclick={handleStart}>REC</button>
	</div>
{:else}
	<div class="recording-control">
		<button class="rec-start" onclick={handleStart}>REC</button>
	</div>
{/if}

<ConfirmDialog
	open={confirmingStop}
	title="Stop Recording"
	message="Stop recording? The current file will be finalized."
	confirmLabel="Stop"
	onconfirm={confirmStop}
	oncancel={cancelStop}
/>

<style>
	.recording-control {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 6px;
		font-family: var(--font-ui);
		font-size: 0.75rem;
		border-radius: var(--radius-md);
	}

	.rec-active {
		background: var(--tally-program-dim);
		border: 1px solid rgba(220, 38, 38, 0.3);
	}

	.rec-dot {
		display: inline-block;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--tally-program);
		box-shadow: 0 0 6px rgba(220, 38, 38, 0.5);
		animation: pulse 1.2s ease-in-out infinite;
	}

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.25; }
	}

	.rec-label {
		color: var(--tally-program);
		font-weight: 700;
		font-size: 0.7rem;
		letter-spacing: 0.06em;
	}

	.rec-duration {
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-weight: 500;
		font-size: 0.75rem;
	}

	.rec-start {
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

	.rec-start:hover {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		color: var(--tally-program);
	}

	.rec-stop {
		padding: 3px 10px;
		border: 1px solid var(--tally-program);
		border-radius: var(--radius-md);
		background: var(--tally-program-dim);
		color: var(--tally-program);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 0.65rem;
		letter-spacing: 0.04em;
		transition: background var(--transition-fast);
	}

	.rec-stop:hover {
		background: rgba(220, 38, 38, 0.25);
	}

	.rec-error {
		border: 1px solid rgba(220, 38, 38, 0.3);
		border-radius: var(--radius-md);
	}

	.rec-error-text {
		color: var(--tally-program);
		font-size: 0.65rem;
	}
</style>
