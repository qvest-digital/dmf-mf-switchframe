<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import type { MediaPipeline } from '$lib/transport/media-pipeline';
	import {
		replayMarkIn, replayMarkOut, replayPlay, replayStop,
		replayQuick, replayPause, replayResume, replaySeek, replaySetSpeed,
		apiCall,
	} from '$lib/api/switch-api';
	import { formatTimecode, formatClipDuration } from '$lib/util/timecode';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import { coalesce } from '$lib/util/coalesce';
	import {
		ICON_PLAY, ICON_PAUSE, ICON_STOP, ICON_FRAME_BACK, ICON_FRAME_FORWARD,
		ICON_JUMP_START, ICON_JUMP_END, ICON_CLOCK, renderIcon,
	} from '$lib/icons';

	interface Props {
		state: ControlRoomState;
		pipeline?: MediaPipeline | null;
	}

	let { state: crState, pipeline = null }: Props = $props();

	let selectedSource = $state('');
	let selectedSpeed = $state(0.5);
	let loopEnabled = $state(false);
	let replayCanvas: HTMLCanvasElement | undefined = $state();
	let progressBarEl: HTMLDivElement | undefined = $state();
	let isDragging = $state(false);

	const defaultPresets = [
		{ seconds: 15, label: '15s' },
		{ seconds: 30, label: '30s' },
		{ seconds: 60, label: '60s' },
	];
	let replayPresets = $state(loadPresets());

	function loadPresets() {
		try {
			const saved = localStorage.getItem('switchframe:replay-presets');
			return saved ? JSON.parse(saved) : defaultPresets;
		} catch { return defaultPresets; }
	}

	const replay = $derived(crState.replay);
	const isPlaying = $derived(replay?.state === 'playing');
	const isPaused = $derived(replay?.state === 'paused');
	const isActive = $derived(isPlaying || isPaused || replay?.state === 'loading');
	const sources = $derived(Object.keys(crState.sources).filter(k => !crState.sources[k]?.isVirtual));
	const buffers = $derived(replay?.buffers ?? []);
	const programSource = $derived(crState.programSource ?? '');
	const currentSpeed = $derived(replay?.speed ?? selectedSpeed);
	const progressPct = $derived((replay?.position ?? 0) * 100);
	const hasMarks = $derived(!!replay?.markIn && !!replay?.markOut);
	const clipDurationMs = $derived(
		(replay?.markIn && replay?.markOut) ? (replay.markOut - replay.markIn) : 0
	);

	const speedOptions = [0.25, 0.5, 0.75, 1.0];

	$effect(() => {
		if (!selectedSource) {
			selectedSource = programSource || (sources.length > 0 ? sources[0] : '');
		}
	});

	$effect(() => {
		if (!pipeline || !replayCanvas) return;
		if (isActive) {
			pipeline.attachCanvas('replay', 'replay-monitor', replayCanvas);
			return () => { pipeline.detachCanvas('replay', 'replay-monitor'); };
		}
		const src = selectedSource || programSource;
		if (src) {
			pipeline.attachCanvas(src, 'replay-monitor', replayCanvas);
			return () => { pipeline.detachCanvas(src, 'replay-monitor'); };
		}
		const ctx = replayCanvas.getContext('2d');
		if (ctx) ctx.clearRect(0, 0, replayCanvas.width, replayCanvas.height);
	});

	$effect(() => {
		if (!replayCanvas?.parentElement) return;
		const obs = new ResizeObserver(([entry]) => {
			const { width, height } = entry.contentRect;
			if (width > 0 && height > 0) setupHiDPICanvas(replayCanvas!, width, height);
		});
		obs.observe(replayCanvas.parentElement);
		return () => obs.disconnect();
	});

	function handlePlay() {
		if (!selectedSource) return;
		apiCall(replayPlay(selectedSource, selectedSpeed, loopEnabled), 'Replay play');
	}
	function handlePause() { apiCall(replayPause(), 'Replay pause'); }
	function handleResume() { apiCall(replayResume(), 'Replay resume'); }
	function handleStop() { apiCall(replayStop(), 'Replay stop'); }

	function handleSpeedChange(speed: number) {
		selectedSpeed = speed;
		if (isPlaying || isPaused) apiCall(replaySetSpeed(speed), 'Set replay speed');
	}

	function handleQuickReplay(seconds: number) {
		apiCall(replayQuick(seconds, selectedSpeed, selectedSource || ''), 'Quick replay');
	}

	function handleMarkIn() {
		const src = selectedSource || programSource;
		if (!src) return;
		selectedSource = src;
		apiCall(replayMarkIn(src), 'Mark IN');
	}

	function handleMarkOut() {
		const src = selectedSource || programSource;
		if (!src) return;
		apiCall(replayMarkOut(src), 'Mark OUT');
	}

	function handleMainButton() {
		if (isPlaying) handlePause();
		else if (isPaused) handleResume();
		else handlePlay();
	}

	function handleFrameBack() {
		apiCall(replaySeek(Math.max(0, (replay?.position ?? 0) - 0.002)), 'Frame back');
	}
	function handleFrameForward() {
		apiCall(replaySeek(Math.min(1, (replay?.position ?? 0) + 0.002)), 'Frame forward');
	}
	function handleJumpToIn() { apiCall(replaySeek(0), 'Jump to IN'); }
	function handleJumpToOut() { apiCall(replaySeek(1), 'Jump to OUT'); }

	function handleProgressPointerDown(e: PointerEvent) {
		if (!isActive) return;
		isDragging = true;
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
		seekToPointerPosition(e);
	}
	function handleProgressPointerMove(e: PointerEvent) {
		if (!isDragging) return;
		seekToPointerPosition(e);
	}
	function handleProgressPointerUp() { isDragging = false; }

	// A drag seeks to its latest position only, not once per pointer move.
	const seek = coalesce((position: number) => {
		const p = replaySeek(position);
		apiCall(p, 'Seek');
		return p;
	}, 50);

	function seekToPointerPosition(e: PointerEvent) {
		if (!progressBarEl) return;
		const rect = progressBarEl.getBoundingClientRect();
		seek(Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width)));
	}

	function getSourceLabel(key: string): string {
		return crState.sources[key]?.label || key;
	}
</script>

<div class="replay-panel">
	<div class="replay-columns">
		<!-- Left: Monitor -->
		<div class="left-col">
			<div class="source-row">
				<select bind:value={selectedSource} class="source-select">
					{#each sources as src}
						<option value={src}>{getSourceLabel(src)}</option>
					{/each}
				</select>
				<button class="mark-btn mark-in" onclick={handleMarkIn} disabled={!selectedSource}>IN</button>
				<button class="mark-btn mark-out" onclick={handleMarkOut} disabled={!selectedSource || !replay?.markIn}>OUT</button>
			</div>

			<div class="replay-monitor" class:active={isActive}>
				<canvas bind:this={replayCanvas}></canvas>
				{#if !isActive && (selectedSource || programSource)}
					<div class="monitor-badge">{getSourceLabel(selectedSource || programSource)}</div>
				{/if}
				{#if isPaused}
					<div class="paused-badge">PAUSED</div>
				{/if}
			</div>

			<div
				class="progress-bar" class:active={isActive}
				bind:this={progressBarEl}
				onpointerdown={handleProgressPointerDown}
				onpointermove={handleProgressPointerMove}
				onpointerup={handleProgressPointerUp}
				role="slider" aria-valuenow={replay?.position ?? 0} aria-valuemin={0} aria-valuemax={1}
				tabindex={isActive ? 0 : -1}
			>
				<div class="progress-track">
					<div class="progress-fill" style:width="{progressPct}%"></div>
					{#if isActive}
						<div class="progress-head" style:left="{progressPct}%"></div>
					{/if}
				</div>
			</div>

			<div class="tc-row">
				{#if replay?.markIn}
					<span class="tc tc-in">IN {formatTimecode(replay.markIn)}</span>
				{:else}
					<span class="tc tc-dim">IN --:--:--.---</span>
				{/if}
				{#if clipDurationMs > 0}
					<span class="tc tc-dur">{formatClipDuration(clipDurationMs)}</span>
				{/if}
				{#if replay?.markOut}
					<span class="tc tc-out">OUT {formatTimecode(replay.markOut)}</span>
				{:else}
					<span class="tc tc-dim">OUT --:--:--.---</span>
				{/if}
			</div>
		</div>

		<!-- Right: Controls -->
		<div class="right-col">
			<div class="transport-row">
				<button class="t-btn" onclick={handleJumpToIn} disabled={!isActive} title="Jump to IN">
					{@html renderIcon(ICON_JUMP_START, 13)}
				</button>
				<button class="t-btn" onclick={handleFrameBack} disabled={!isActive} title="Frame back">
					{@html renderIcon(ICON_FRAME_BACK, 13)}
				</button>
				<button
					class="t-btn t-main" class:playing={isPlaying} class:paused={isPaused} class:stopped={!isActive}
					onclick={handleMainButton}
					disabled={!isActive && !hasMarks}
					title={isPlaying ? 'Pause' : isPaused ? 'Resume' : 'Play'}
				>
					{#if isPlaying}
						{@html renderIcon(ICON_PAUSE, 16)}
					{:else}
						{@html renderIcon(ICON_PLAY, 16, 'fill')}
					{/if}
				</button>
				<button class="t-btn t-stop" onclick={handleStop} disabled={!isActive} title="Stop">
					{@html renderIcon(ICON_STOP, 13, 'fill')}
				</button>
				<button class="t-btn" onclick={handleFrameForward} disabled={!isActive} title="Frame forward">
					{@html renderIcon(ICON_FRAME_FORWARD, 13)}
				</button>
				<button class="t-btn" onclick={handleJumpToOut} disabled={!isActive} title="Jump to OUT">
					{@html renderIcon(ICON_JUMP_END, 13)}
				</button>
			</div>

			<div class="speed-row">
				<span class="row-label">SPEED</span>
				<div class="speed-seg">
					{#each speedOptions as speed}
						<button class="seg-btn" class:active={currentSpeed === speed} onclick={() => handleSpeedChange(speed)}>
							{speed}x
						</button>
					{/each}
				</div>
			</div>

			<div class="loop-row">
				<label class="loop-toggle">
					<input type="checkbox" bind:checked={loopEnabled} />
					<span class="row-label">LOOP</span>
				</label>
				{#if isActive}
					<span class="playback-info">
						{getSourceLabel(replay?.source ?? '')} @ {currentSpeed}x
						{#if replay?.loop}<span class="loop-badge">LOOP</span>{/if}
					</span>
				{/if}
			</div>

			<div class="quick-row">
				{#each replayPresets as preset}
					<button class="quick-btn" onclick={() => handleQuickReplay(preset.seconds)} disabled={!selectedSource} title="Quick replay last {preset.label}">
						<span class="quick-icon">{@html renderIcon(ICON_CLOCK, 11)}</span>
						{preset.label}
					</button>
				{/each}
			</div>

			{#if buffers.length > 0}
				<div class="buffer-row">
					{#each buffers as buf}
						<button
							class="buffer-chip" class:active={buf.source === selectedSource}
							onclick={() => selectedSource = buf.source}
							title="{getSourceLabel(buf.source)}: {Math.floor(buf.durationSecs)}s"
						>
							{getSourceLabel(buf.source)}
							<span class="buf-dur">{Math.floor(buf.durationSecs)}s</span>
						</button>
					{/each}
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.replay-panel {
		display: flex;
		flex-direction: column;
		padding: 6px;
		height: 100%;
		overflow: hidden;
	}

	.replay-columns {
		display: flex;
		gap: 10px;
		flex: 1;
		min-height: 0;
	}

	/* ── Left: monitor column ── */
	.left-col {
		display: flex;
		flex-direction: column;
		gap: 3px;
		width: clamp(220px, 30%, 360px);
		min-width: 200px;
		flex-shrink: 0;
	}

	.source-row {
		display: flex;
		gap: 3px;
		align-items: center;
		flex-shrink: 0;
	}

	.source-select {
		flex: 1;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
	}

	.mark-btn {
		padding: 2px 7px;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		cursor: pointer;
		letter-spacing: 0.04em;
		white-space: nowrap;
	}
	.mark-btn:disabled { opacity: 0.3; cursor: default; }
	.mark-in { background: rgba(22, 163, 74, 0.12); border-color: rgba(22, 163, 74, 0.3); color: var(--tally-preview); }
	.mark-in:hover:not(:disabled) { background: rgba(22, 163, 74, 0.22); }
	.mark-out { background: rgba(220, 38, 38, 0.12); border-color: rgba(220, 38, 38, 0.3); color: var(--tally-program); }
	.mark-out:hover:not(:disabled) { background: rgba(220, 38, 38, 0.22); }

	/* ── Monitor ── */
	.replay-monitor {
		flex: 1;
		min-height: 0;
		aspect-ratio: 16 / 9;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		overflow: hidden;
		position: relative;
	}
	.replay-monitor.active { border-color: var(--accent-orange-medium); }
	.replay-monitor canvas { width: 100%; height: 100%; display: block; }

	.monitor-badge {
		position: absolute;
		bottom: 3px; left: 3px;
		padding: 1px 4px;
		background: rgba(0, 0, 0, 0.65);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		letter-spacing: 0.04em;
		border-radius: 2px;
	}

	.paused-badge {
		position: absolute;
		top: 4px; right: 4px;
		padding: 1px 5px;
		background: rgba(245, 158, 11, 0.75);
		color: #000;
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.06em;
		border-radius: var(--radius-xs);
	}

	/* ── Progress ── */
	.progress-bar {
		height: 12px;
		cursor: default;
		padding: 3px 0;
		touch-action: none;
		flex-shrink: 0;
	}
	.progress-bar.active { cursor: pointer; }
	.progress-track {
		position: relative;
		height: 5px;
		background: var(--bg-base);
		border-radius: 3px;
		border: 1px solid var(--border-default);
		overflow: visible;
	}
	.progress-fill {
		height: 100%;
		background: var(--accent-orange);
		border-radius: 3px 0 0 3px;
		transition: width 0.05s linear;
	}
	.progress-head {
		position: absolute;
		top: 50%;
		width: 9px; height: 9px;
		background: var(--accent-orange);
		border: 2px solid var(--text-primary);
		border-radius: 50%;
		transform: translate(-50%, -50%);
		pointer-events: none;
		box-shadow: 0 0 3px rgba(0, 0, 0, 0.5);
	}

	/* ── Timecodes ── */
	.tc-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 0 1px;
		flex-shrink: 0;
	}
	.tc { font-family: var(--font-mono); font-size: 9px; letter-spacing: 0.02em; }
	.tc-in { color: var(--tally-preview); }
	.tc-out { color: var(--tally-program); }
	.tc-dim { color: var(--text-tertiary); opacity: 0.35; }
	.tc-dur { font-size: 10px; font-weight: 600; color: var(--accent-orange); }

	/* ── Right: controls column ── */
	.right-col {
		display: flex;
		flex-direction: column;
		gap: 4px;
		flex: 1;
		min-width: 0;
		min-height: 0;
		justify-content: center;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.right-col::-webkit-scrollbar { width: 3px; }
	.right-col::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	/* ── Transport ── */
	.transport-row { display: flex; gap: 3px; align-items: center; }

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
	.t-main.playing { background: var(--accent-orange-dim); border-color: var(--accent-orange); color: var(--accent-orange); }
	.t-main.paused { background: var(--accent-orange-dim); border-color: var(--accent-orange-medium); color: var(--accent-orange); animation: pulse-pause 1.5s ease-in-out infinite; }

	.t-stop:hover:not(:disabled) { color: var(--tally-program); border-color: rgba(220, 38, 38, 0.3); background: rgba(220, 38, 38, 0.1); }

	@keyframes pulse-pause {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.55; }
	}

	/* ── Speed ── */
	.speed-row { display: flex; align-items: center; gap: 6px; }
	.row-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--text-tertiary);
		letter-spacing: 0.08em;
		white-space: nowrap;
		text-transform: uppercase;
	}
	.speed-seg { display: flex; flex: 1; }
	.seg-btn {
		flex: 1;
		padding: 3px 0;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}
	.seg-btn:first-child { border-radius: var(--radius-sm) 0 0 var(--radius-sm); }
	.seg-btn:last-child { border-radius: 0 var(--radius-sm) var(--radius-sm) 0; }
	.seg-btn:not(:first-child) { border-left: none; }
	.seg-btn:hover:not(.active) { background: var(--bg-hover); color: var(--text-primary); }
	.seg-btn.active { background: var(--accent-orange); border-color: var(--accent-orange); color: #000; font-weight: 700; }
	.seg-btn.active + .seg-btn { border-left: 1px solid var(--border-default); }

	/* ── Loop ── */
	.loop-row { display: flex; align-items: center; gap: 8px; }
	.loop-toggle { display: flex; align-items: center; gap: 4px; cursor: pointer; }
	.loop-toggle input[type="checkbox"] { accent-color: var(--accent-orange); width: 12px; height: 12px; }
	.playback-info {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--accent-orange);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.loop-badge {
		padding: 0 3px;
		background: var(--accent-orange-dim);
		border-radius: 2px;
		font-size: 8px;
		font-weight: 700;
	}

	/* ── Quick replay ── */
	.quick-row { display: flex; gap: 3px; }
	.quick-btn {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 3px;
		padding: 4px 4px;
		background: var(--accent-orange-dim);
		border: 1px solid var(--accent-orange-medium);
		border-radius: var(--radius-sm);
		color: var(--accent-orange);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		cursor: pointer;
		white-space: nowrap;
		transition: background var(--transition-fast);
	}
	.quick-btn:hover:not(:disabled) { background: var(--accent-orange-light); }
	.quick-btn:active:not(:disabled) { background: var(--accent-orange-medium); box-shadow: var(--shadow-inset); }
	.quick-btn:disabled { opacity: 0.3; cursor: default; }
	.quick-icon { display: flex; align-items: center; }

	/* ── Buffer chips ── */
	.buffer-row { display: flex; gap: 3px; flex-wrap: wrap; }
	.buffer-chip {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		padding: 2px 5px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-tertiary);
		cursor: pointer;
		transition: border-color var(--transition-fast), color var(--transition-fast);
	}
	.buffer-chip:hover { border-color: var(--border-strong); color: var(--text-secondary); }
	.buffer-chip.active { border-color: var(--accent-orange-medium); color: var(--accent-orange); }
	.buf-dur { font-family: var(--font-mono); font-size: 8px; opacity: 0.7; }
</style>
