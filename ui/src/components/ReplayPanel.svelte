<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { replayMarkIn, replayMarkOut, replayPlay, replayStop, apiCall } from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	let selectedSource = $state('');
	let selectedSpeed = $state(1.0);
	let loopEnabled = $state(false);

	const replay = $derived(crState.replay);
	const isPlaying = $derived(replay?.state === 'playing' || replay?.state === 'loading');
	const sources = $derived(Object.keys(crState.sources));
	const buffers = $derived(replay?.buffers ?? []);

	// Auto-select first source if none selected.
	$effect(() => {
		if (!selectedSource && sources.length > 0) {
			selectedSource = sources[0];
		}
	});

	function handleMarkIn() {
		if (!selectedSource) return;
		apiCall(replayMarkIn(selectedSource), 'Mark IN');
	}

	function handleMarkOut() {
		if (!selectedSource) return;
		apiCall(replayMarkOut(selectedSource), 'Mark OUT');
	}

	function handlePlay() {
		if (!selectedSource) return;
		apiCall(replayPlay(selectedSource, selectedSpeed, loopEnabled), 'Replay play');
	}

	function handleStop() {
		apiCall(replayStop(), 'Replay stop');
	}

	function formatDuration(secs: number): string {
		const m = Math.floor(secs / 60);
		const s = Math.floor(secs % 60);
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	function formatBytes(bytes: number): string {
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
		return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
	}
</script>

<div class="replay-panel">
	<h3>REPLAY</h3>

	<div class="source-row">
		<select bind:value={selectedSource} class="source-select">
			{#each sources as src}
				<option value={src}>{crState.sources[src]?.label || src}</option>
			{/each}
		</select>
	</div>

	<div class="mark-row">
		<button class="mark-btn mark-in" onclick={handleMarkIn} disabled={!selectedSource}>
			MARK IN
		</button>
		<button class="mark-btn mark-out" onclick={handleMarkOut} disabled={!selectedSource || !replay?.markIn}>
			MARK OUT
		</button>
	</div>

	{#if replay?.markIn}
		<div class="mark-info">
			{replay.markSource}: IN set
			{#if replay.markOut}
				/ OUT set
			{/if}
		</div>
	{/if}

	<div class="speed-row">
		<span class="speed-label">Speed:</span>
		<select bind:value={selectedSpeed} class="speed-select" disabled={isPlaying}>
			<option value={0.25}>0.25x</option>
			<option value={0.5}>0.5x</option>
			<option value={1.0}>1x</option>
		</select>
		<label class="loop-label">
			<input type="checkbox" bind:checked={loopEnabled} disabled={isPlaying} />
			Loop
		</label>
	</div>

	<div class="transport-row">
		{#if isPlaying}
			<button class="transport-btn stop-btn" onclick={handleStop}>STOP</button>
		{:else}
			<button
				class="transport-btn play-btn"
				onclick={handlePlay}
				disabled={!replay?.markIn || !replay?.markOut}
			>
				PLAY
			</button>
		{/if}
	</div>

	{#if isPlaying}
		<div class="playback-info">
			Playing {replay?.source} @ {replay?.speed}x
			{#if replay?.loop}(loop){/if}
		</div>
	{/if}

	{#if buffers.length > 0}
		<div class="buffer-list">
			{#each buffers as buf}
				<div class="buffer-item" class:active={buf.source === selectedSource}>
					<span class="buf-source">{crState.sources[buf.source]?.label || buf.source}</span>
					<span class="buf-duration">{formatDuration(buf.durationSecs)}</span>
					<span class="buf-size">{formatBytes(buf.bytesUsed)}</span>
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.replay-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 8px;
		background: #1a1a1a;
		border-radius: 4px;
	}

	h3 {
		margin: 0;
		font-size: 11px;
		color: #888;
		text-transform: uppercase;
		letter-spacing: 1px;
	}

	.source-row {
		display: flex;
	}

	.source-select, .speed-select {
		background: #2a2a2a;
		color: #eee;
		border: 1px solid #444;
		border-radius: 3px;
		padding: 4px 6px;
		font-size: 12px;
		flex: 1;
	}

	.mark-row {
		display: flex;
		gap: 4px;
	}

	.mark-btn {
		flex: 1;
		padding: 6px 8px;
		border: none;
		border-radius: 3px;
		font-size: 11px;
		font-weight: bold;
		cursor: pointer;
		text-transform: uppercase;
	}

	.mark-btn:disabled {
		opacity: 0.4;
		cursor: default;
	}

	.mark-in {
		background: #2a5a2a;
		color: #8f8;
	}

	.mark-in:hover:not(:disabled) {
		background: #3a7a3a;
	}

	.mark-out {
		background: #5a2a2a;
		color: #f88;
	}

	.mark-out:hover:not(:disabled) {
		background: #7a3a3a;
	}

	.mark-info {
		font-size: 10px;
		color: #aaa;
		text-align: center;
	}

	.speed-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.speed-label, .loop-label {
		font-size: 11px;
		color: #aaa;
	}

	.loop-label {
		display: flex;
		align-items: center;
		gap: 3px;
		margin-left: auto;
	}

	.transport-row {
		display: flex;
	}

	.transport-btn {
		flex: 1;
		padding: 8px;
		border: none;
		border-radius: 3px;
		font-size: 13px;
		font-weight: bold;
		cursor: pointer;
		text-transform: uppercase;
	}

	.play-btn {
		background: #2a4a6a;
		color: #8af;
	}

	.play-btn:hover:not(:disabled) {
		background: #3a5a7a;
	}

	.play-btn:disabled {
		opacity: 0.4;
		cursor: default;
	}

	.stop-btn {
		background: #6a2a2a;
		color: #f88;
	}

	.stop-btn:hover {
		background: #8a3a3a;
	}

	.playback-info {
		font-size: 10px;
		color: #8af;
		text-align: center;
	}

	.buffer-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin-top: 4px;
	}

	.buffer-item {
		display: flex;
		justify-content: space-between;
		padding: 2px 4px;
		font-size: 10px;
		color: #888;
		border-radius: 2px;
	}

	.buffer-item.active {
		background: #2a2a3a;
		color: #aaa;
	}

	.buf-source {
		flex: 1;
	}

	.buf-duration, .buf-size {
		margin-left: 8px;
	}
</style>
