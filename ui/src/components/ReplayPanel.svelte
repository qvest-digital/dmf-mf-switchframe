<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import type { MediaPipeline } from '$lib/transport/media-pipeline';
	import { replayMarkIn, replayMarkOut, replayPlay, replayStop, apiCall } from '$lib/api/switch-api';
	import { formatTimecode, formatClipDuration } from '$lib/util/timecode';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';

	interface Props {
		state: ControlRoomState;
		pipeline?: MediaPipeline | null;
	}

	let { state: crState, pipeline = null }: Props = $props();

	let selectedSource = $state('');
	let selectedSpeed = $state(1.0);
	let loopEnabled = $state(false);
	let replayCanvas: HTMLCanvasElement | undefined = $state();

	const replay = $derived(crState.replay);
	const isPlaying = $derived(replay?.state === 'playing' || replay?.state === 'loading');
	const sources = $derived(Object.keys(crState.sources).filter(k => !crState.sources[k]?.isVirtual));
	const buffers = $derived(replay?.buffers ?? []);

	// Auto-select first source if none selected.
	$effect(() => {
		if (!selectedSource && sources.length > 0) {
			selectedSource = sources[0];
		}
	});

	// Attach/detach replay monitor canvas. Return cleanup handles unmount + dep changes.
	$effect(() => {
		if (!pipeline || !isPlaying || !replayCanvas) return;
		pipeline.attachCanvas('replay', 'replay-monitor', replayCanvas);
		return () => {
			pipeline.detachCanvas('replay', 'replay-monitor');
		};
	});

	// HiDPI canvas sizing for replay monitor
	$effect(() => {
		if (!replayCanvas?.parentElement) return;
		const obs = new ResizeObserver(([entry]) => {
			const { width, height } = entry.contentRect;
			if (width > 0 && height > 0) setupHiDPICanvas(replayCanvas!, width, height);
		});
		obs.observe(replayCanvas.parentElement);
		return () => obs.disconnect();
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
			<span class="mark-source">{crState.sources[replay.markSource ?? '']?.label || replay.markSource}</span>
			<span class="mark-times">
				IN {formatTimecode(replay.markIn)}
				{#if replay.markOut}
					&nbsp;/ OUT {formatTimecode(replay.markOut)}
					<span class="clip-duration">({formatClipDuration((replay.markOut ?? 0) - (replay.markIn ?? 0))})</span>
				{/if}
			</span>
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

	{#if isPlaying}
		<div class="replay-monitor">
			<canvas bind:this={replayCanvas}></canvas>
		</div>
	{/if}

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
		background: var(--bg-elevated);
		border-radius: var(--radius-sm);
	}

	h3 {
		margin: 0;
		font-size: 11px;
		color: var(--text-secondary);
		font-family: var(--font-ui);
		text-transform: uppercase;
		letter-spacing: 1px;
	}

	.replay-monitor {
		aspect-ratio: 16 / 9;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		overflow: hidden;
		position: relative;
	}

	.replay-monitor canvas {
		width: 100%;
		height: 100%;
		display: block;
	}

	.source-row {
		display: flex;
	}

	.source-select, .speed-select {
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 4px 6px;
		font-family: var(--font-ui);
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
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
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
		background: rgba(22, 163, 74, 0.2);
		color: var(--tally-preview);
	}

	.mark-in:hover:not(:disabled) {
		background: rgba(22, 163, 74, 0.3);
	}

	.mark-out {
		background: rgba(220, 38, 38, 0.2);
		color: var(--tally-program);
	}

	.mark-out:hover:not(:disabled) {
		background: rgba(220, 38, 38, 0.3);
	}

	.mark-info {
		font-family: var(--font-ui);
		font-size: 10px;
		color: var(--text-tertiary);
		text-align: center;
	}

	.speed-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.speed-label, .loop-label {
		font-family: var(--font-ui);
		font-size: 11px;
		color: var(--text-tertiary);
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
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 13px;
		font-weight: bold;
		cursor: pointer;
		text-transform: uppercase;
	}

	.play-btn {
		background: rgba(59, 130, 246, 0.2);
		color: var(--accent-blue);
	}

	.play-btn:hover:not(:disabled) {
		background: rgba(59, 130, 246, 0.3);
	}

	.play-btn:disabled {
		opacity: 0.4;
		cursor: default;
	}

	.stop-btn {
		background: rgba(220, 38, 38, 0.25);
		color: var(--tally-program);
	}

	.stop-btn:hover {
		background: rgba(220, 38, 38, 0.35);
	}

	.playback-info {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--accent-blue);
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
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-secondary);
		border-radius: var(--radius-sm);
	}

	.buffer-item.active {
		background: var(--accent-blue-dim);
		color: var(--text-tertiary);
	}

	.buf-source {
		flex: 1;
	}

	.buf-duration, .buf-size {
		margin-left: 8px;
	}

	.mark-source {
		font-weight: 600;
		color: var(--text-primary);
	}

	.mark-times {
		font-family: var(--font-mono);
		font-size: 10px;
	}

	.clip-duration {
		color: var(--accent-blue);
		margin-left: 4px;
	}
</style>
