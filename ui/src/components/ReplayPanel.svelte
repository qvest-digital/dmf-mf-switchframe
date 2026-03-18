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

	interface Props {
		state: ControlRoomState;
		pipeline?: MediaPipeline | null;
	}

	let { state: crState, pipeline = null }: Props = $props();

	// ── Component state ──
	let selectedSource = $state('');
	let selectedSpeed = $state(0.5);
	let loopEnabled = $state(false);
	let replayCanvas: HTMLCanvasElement | undefined = $state();
	let progressBarEl: HTMLDivElement | undefined = $state();
	let isDragging = $state(false);
	let clipWorkspaceOpen = $state(false);

	// Quick replay presets (from localStorage)
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

	// ── Derived state ──
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

	// Speed options for segmented control
	const speedOptions = [0.25, 0.5, 0.75, 1.0];

	// ── Auto-select source ──
	$effect(() => {
		if (!selectedSource) {
			selectedSource = programSource || (sources.length > 0 ? sources[0] : '');
		}
	});

	// ── Canvas effects ──
	// When active (playing/paused/loading), show replay relay.
	// When idle, show selected source's live feed as preview.
	$effect(() => {
		if (!pipeline || !replayCanvas) return;

		if (isActive) {
			// Attach to the replay relay for playback output.
			pipeline.attachCanvas('replay', 'replay-monitor', replayCanvas);
			return () => {
				pipeline.detachCanvas('replay', 'replay-monitor');
			};
		}

		// Idle: show selected source's live feed.
		const src = selectedSource || programSource;
		if (src) {
			pipeline.attachCanvas(src, 'replay-monitor', replayCanvas);
			return () => {
				pipeline.detachCanvas(src, 'replay-monitor');
			};
		}

		// No source available — clear to black.
		const ctx = replayCanvas.getContext('2d');
		if (ctx) {
			ctx.clearRect(0, 0, replayCanvas.width, replayCanvas.height);
		}
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

	// ── Handlers ──
	function handlePlay() {
		if (!selectedSource) return;
		apiCall(replayPlay(selectedSource, selectedSpeed, loopEnabled), 'Replay play');
	}

	function handlePause() {
		apiCall(replayPause(), 'Replay pause');
	}

	function handleResume() {
		apiCall(replayResume(), 'Replay resume');
	}

	function handleStop() {
		apiCall(replayStop(), 'Replay stop');
	}

	function handleSpeedChange(speed: number) {
		selectedSpeed = speed;
		if (isPlaying || isPaused) {
			apiCall(replaySetSpeed(speed), 'Set replay speed');
		}
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
		if (isPlaying) {
			handlePause();
		} else if (isPaused) {
			handleResume();
		} else {
			handlePlay();
		}
	}

	// ── Frame stepping ──
	function handleFrameBack() {
		const step = 0.002;
		const pos = Math.max(0, (replay?.position ?? 0) - step);
		apiCall(replaySeek(pos), 'Frame back');
	}

	function handleFrameForward() {
		const step = 0.002;
		const pos = Math.min(1, (replay?.position ?? 0) + step);
		apiCall(replaySeek(pos), 'Frame forward');
	}

	function handleJumpToIn() {
		apiCall(replaySeek(0), 'Jump to IN');
	}

	function handleJumpToOut() {
		apiCall(replaySeek(1), 'Jump to OUT');
	}

	// ── Progress bar seek ──
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

	function handleProgressPointerUp() {
		isDragging = false;
	}

	function seekToPointerPosition(e: PointerEvent) {
		if (!progressBarEl) return;
		const rect = progressBarEl.getBoundingClientRect();
		const position = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
		apiCall(replaySeek(position), 'Seek');
	}

	// ── Helpers ──
	function getSourceLabel(key: string): string {
		return crState.sources[key]?.label || key;
	}

	function formatDuration(secs: number): string {
		const m = Math.floor(secs / 60);
		const s = Math.floor(secs % 60);
		return `${m}:${s.toString().padStart(2, '0')}`;
	}
</script>

<div class="replay-panel">
  <div class="replay-columns">
	<!-- Left column: Source + Monitor + Progress + Timecodes -->
	<div class="left-col">
		<!-- Source selector -->
		<div class="source-row">
			<select bind:value={selectedSource} class="source-select">
				{#each sources as src}
					<option value={src}>{getSourceLabel(src)}</option>
				{/each}
			</select>
			<button class="mark-btn mark-in" onclick={handleMarkIn} disabled={!selectedSource}>
				IN
			</button>
			<button
				class="mark-btn mark-out"
				onclick={handleMarkOut}
				disabled={!selectedSource || !replay?.markIn}
			>
				OUT
			</button>
		</div>

		<!-- Replay monitor -->
		<div class="replay-monitor" class:active={isActive}>
			<canvas bind:this={replayCanvas}></canvas>
			{#if !isActive && (selectedSource || programSource)}
				<div class="monitor-source-badge">
					{getSourceLabel(selectedSource || programSource)}
				</div>
			{/if}
			{#if isPaused}
				<div class="paused-badge">PAUSED</div>
			{/if}
		</div>

		<!-- Progress / seek bar -->
		<div
			class="progress-bar"
			class:active={isActive}
			bind:this={progressBarEl}
			onpointerdown={handleProgressPointerDown}
			onpointermove={handleProgressPointerMove}
			onpointerup={handleProgressPointerUp}
			role="slider"
			aria-valuenow={replay?.position ?? 0}
			aria-valuemin={0}
			aria-valuemax={1}
			tabindex={isActive ? 0 : -1}
		>
			<div class="progress-track">
				<div class="progress-fill" style:width="{progressPct}%"></div>
				{#if isActive}
					<div class="progress-playhead" style:left="{progressPct}%"></div>
				{/if}
			</div>
		</div>

		<!-- Timecode display -->
		<div class="timecode-row">
			{#if replay?.markIn}
				<span class="tc-mark tc-in">IN {formatTimecode(replay.markIn)}</span>
			{:else}
				<span class="tc-mark tc-placeholder">IN --:--:--.---</span>
			{/if}
			{#if clipDurationMs > 0}
				<span class="tc-duration">{formatClipDuration(clipDurationMs)}</span>
			{/if}
			{#if replay?.markOut}
				<span class="tc-mark tc-out">OUT {formatTimecode(replay.markOut)}</span>
			{:else}
				<span class="tc-mark tc-placeholder">OUT --:--:--.---</span>
			{/if}
		</div>
	</div>

	<!-- Right column: Transport + Speed + Quick Replay -->
	<div class="right-col">
		<!-- Transport controls -->
		<div class="transport-row">
			<button
				class="transport-btn"
				onclick={handleJumpToIn}
				disabled={!isActive}
				title="Jump to IN"
			>
				<span class="transport-icon">|&#9665;</span>
			</button>
			<button
				class="transport-btn"
				onclick={handleFrameBack}
				disabled={!isActive}
				title="Frame back"
			>
				<span class="transport-icon">&#9665;</span>
			</button>
			<button
				class="transport-btn main-btn"
				class:playing={isPlaying}
				class:paused={isPaused}
				class:stopped={!isActive}
				onclick={isActive && !isPaused ? (isPlaying ? handlePause : handleStop) : (isPaused ? handleResume : handlePlay)}
				disabled={!isActive && !hasMarks}
				title={isPlaying ? 'Pause' : isPaused ? 'Resume' : 'Play'}
			>
				{#if isPlaying}
					<span class="transport-icon main-icon">&#10074;&#10074;</span>
				{:else if isPaused}
					<span class="transport-icon main-icon">&#9654;</span>
				{:else}
					<span class="transport-icon main-icon">&#9654;</span>
				{/if}
			</button>
			<button
				class="transport-btn stop-btn"
				onclick={handleStop}
				disabled={!isActive}
				title="Stop"
			>
				<span class="transport-icon">&#9632;</span>
			</button>
			<button
				class="transport-btn"
				onclick={handleFrameForward}
				disabled={!isActive}
				title="Frame forward"
			>
				<span class="transport-icon">&#9655;</span>
			</button>
			<button
				class="transport-btn"
				onclick={handleJumpToOut}
				disabled={!isActive}
				title="Jump to OUT"
			>
				<span class="transport-icon">&#9655;|</span>
			</button>
		</div>

		<!-- Speed segmented control -->
		<div class="speed-row">
			<span class="speed-label">SPEED</span>
			<div class="speed-segmented">
				{#each speedOptions as speed}
					<button
						class="speed-btn"
						class:active={currentSpeed === speed}
						onclick={() => handleSpeedChange(speed)}
					>
						{speed}x
					</button>
				{/each}
			</div>
		</div>

		<!-- Loop toggle -->
		<div class="loop-row">
			<label class="loop-toggle">
				<input
					type="checkbox"
					bind:checked={loopEnabled}
				/>
				<span class="loop-label">LOOP</span>
			</label>
			{#if isActive}
				<span class="playback-info">
					{getSourceLabel(replay?.source ?? '')} @ {currentSpeed}x
					{#if replay?.loop}
						<span class="loop-indicator">LOOP</span>
					{/if}
				</span>
			{/if}
		</div>

		<!-- Quick replay presets -->
		<div class="quick-row">
			{#each replayPresets as preset}
				<button
					class="quick-btn"
					onclick={() => handleQuickReplay(preset.seconds)}
					disabled={!selectedSource}
					title="Quick replay last {preset.label}"
				>
					&#x21BA; {preset.label}
				</button>
			{/each}
		</div>

		<!-- Buffer info (compact) -->
		{#if buffers.length > 0}
			<div class="buffer-row">
				{#each buffers as buf}
					<span
						class="buffer-chip"
						class:active={buf.source === selectedSource}
						title="{getSourceLabel(buf.source)}: {Math.floor(buf.durationSecs)}s buffer"
					>
						{getSourceLabel(buf.source)}
						<span class="buffer-dur">{Math.floor(buf.durationSecs)}s</span>
					</span>
				{/each}
			</div>
		{/if}
	</div>
  </div>

	<!-- Clip Workspace (collapsible) -->
	<div class="clip-workspace">
		<button class="workspace-toggle" onclick={() => clipWorkspaceOpen = !clipWorkspaceOpen}>
			<span class="toggle-arrow">{clipWorkspaceOpen ? '\u25BE' : '\u25B8'}</span>
			CLIP WORKSPACE
		</button>

		{#if clipWorkspaceOpen}
			<div class="workspace-content">
				<!-- Mark controls + timecodes -->
				<div class="mark-row">
					<button class="mark-btn mark-in-btn" onclick={handleMarkIn} disabled={!selectedSource && !programSource}>
						MARK IN
					</button>
					<button class="mark-btn mark-out-btn" onclick={handleMarkOut}
						disabled={!replay?.markIn}>
						MARK OUT
					</button>
					<div class="mark-info">
						{#if replay?.markIn}
							<span class="mark-label">IN</span>
							<span class="mark-time">{formatTimecode(replay.markIn)}</span>
						{/if}
						{#if replay?.markOut}
							<span class="mark-sep">/</span>
							<span class="mark-label">OUT</span>
							<span class="mark-time">{formatTimecode(replay.markOut)}</span>
							<span class="clip-dur">({formatClipDuration((replay.markOut ?? 0) - (replay.markIn ?? 0))})</span>
						{/if}
					</div>
				</div>

				<!-- Buffer status -->
				<div class="buffer-strip">
					{#each buffers as buf}
						<button
							class="buffer-chip-ws"
							class:active={buf.source === selectedSource}
							onclick={() => selectedSource = buf.source}
						>
							<span class="chip-label">{crState.sources[buf.source]?.label || buf.source}</span>
							<div class="chip-bar">
								<div class="chip-fill" style="width: {Math.min(100, (buf.durationSecs / 300) * 100)}%"></div>
							</div>
							<span class="chip-dur">{formatDuration(buf.durationSecs)}</span>
						</button>
					{/each}
				</div>
			</div>
		{/if}
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

	/* ── Left column: monitor area ── */
	.left-col {
		display: flex;
		flex-direction: column;
		gap: 4px;
		width: 340px;
		min-width: 260px;
		flex-shrink: 0;
	}

	.source-row {
		display: flex;
		gap: 4px;
		align-items: center;
	}

	.source-select {
		flex: 1;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 3px 6px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
	}

	.mark-btn {
		padding: 3px 8px;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		cursor: pointer;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		white-space: nowrap;
	}

	.mark-btn:disabled {
		opacity: 0.35;
		cursor: default;
	}

	.mark-in {
		background: rgba(22, 163, 74, 0.12);
		border-color: rgba(22, 163, 74, 0.3);
		color: var(--tally-preview);
	}

	.mark-in:hover:not(:disabled) {
		background: rgba(22, 163, 74, 0.22);
	}

	.mark-out {
		background: rgba(220, 38, 38, 0.12);
		border-color: rgba(220, 38, 38, 0.3);
		color: var(--tally-program);
	}

	.mark-out:hover:not(:disabled) {
		background: rgba(220, 38, 38, 0.22);
	}

	/* ── Monitor ── */
	.replay-monitor {
		aspect-ratio: 16 / 9;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		overflow: hidden;
		position: relative;
		flex-shrink: 0;
	}

	.replay-monitor.active {
		border-color: var(--accent-orange-medium);
	}

	.replay-monitor canvas {
		width: 100%;
		height: 100%;
		display: block;
	}

	.monitor-source-badge {
		position: absolute;
		bottom: 4px;
		left: 4px;
		padding: 1px 5px;
		background: rgba(0, 0, 0, 0.6);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 600;
		letter-spacing: 0.04em;
		border-radius: 2px;
	}

	.paused-badge {
		position: absolute;
		top: 6px;
		right: 6px;
		padding: 2px 6px;
		background: rgba(245, 158, 11, 0.7);
		color: #000;
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.06em;
		border-radius: var(--radius-sm);
	}

	/* ── Progress bar ── */
	.progress-bar {
		height: 14px;
		cursor: default;
		padding: 4px 0;
		touch-action: none;
	}

	.progress-bar.active {
		cursor: pointer;
	}

	.progress-track {
		position: relative;
		height: 6px;
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

	.progress-playhead {
		position: absolute;
		top: 50%;
		width: 10px;
		height: 10px;
		background: var(--accent-orange);
		border: 2px solid var(--text-primary);
		border-radius: 50%;
		transform: translate(-50%, -50%);
		pointer-events: none;
		box-shadow: 0 0 4px rgba(0, 0, 0, 0.5);
	}

	/* ── Timecode ── */
	.timecode-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 0 2px;
	}

	.tc-mark {
		font-family: var(--font-mono);
		font-size: 9px;
		letter-spacing: 0.02em;
	}

	.tc-in {
		color: var(--tally-preview);
	}

	.tc-out {
		color: var(--tally-program);
	}

	.tc-placeholder {
		color: var(--text-tertiary);
		opacity: 0.4;
	}

	.tc-duration {
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 600;
		color: var(--accent-orange);
	}

	/* ── Right column: controls ── */
	.right-col {
		display: flex;
		flex-direction: column;
		gap: 6px;
		flex: 1;
		min-width: 0;
	}

	/* ── Transport controls ── */
	.transport-row {
		display: flex;
		gap: 3px;
		align-items: center;
	}

	.transport-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		min-width: 32px;
		height: 30px;
		padding: 0 6px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		cursor: pointer;
		transition: background 0.1s, border-color 0.1s;
	}

	.transport-btn:hover:not(:disabled) {
		background: rgba(255, 255, 255, 0.06);
		border-color: rgba(255, 255, 255, 0.15);
		color: var(--text-primary);
	}

	.transport-btn:active:not(:disabled) {
		background: rgba(255, 255, 255, 0.03);
		box-shadow: inset 0 1px 3px rgba(0, 0, 0, 0.4);
	}

	.transport-btn:disabled {
		opacity: 0.3;
		cursor: default;
	}

	.transport-icon {
		font-size: 11px;
		line-height: 1;
	}

	/* Main play/pause button */
	.main-btn {
		min-width: 44px;
		height: 34px;
		font-weight: 700;
	}

	.main-btn.stopped {
		border-color: var(--accent-orange-medium);
		color: var(--accent-orange);
	}

	.main-btn.stopped:hover:not(:disabled) {
		background: var(--accent-orange-dim);
		border-color: var(--accent-orange);
	}

	.main-btn.playing {
		background: var(--accent-orange-dim);
		border-color: var(--accent-orange);
		color: var(--accent-orange);
	}

	.main-btn.playing:hover:not(:disabled) {
		background: var(--accent-orange-light);
	}

	.main-btn.paused {
		background: var(--accent-orange-dim);
		border-color: var(--accent-orange-medium);
		color: var(--accent-orange);
		animation: pause-pulse 1.5s ease-in-out infinite;
	}

	.main-icon {
		font-size: 14px;
	}

	/* Stop button */
	.stop-btn {
		color: var(--text-secondary);
	}

	.stop-btn:hover:not(:disabled) {
		color: var(--tally-program);
		border-color: rgba(220, 38, 38, 0.3);
		background: rgba(220, 38, 38, 0.1);
	}

	@keyframes pause-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
	}

	/* ── Speed segmented control ── */
	.speed-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.speed-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		color: var(--text-tertiary);
		letter-spacing: 0.06em;
		white-space: nowrap;
	}

	.speed-segmented {
		display: flex;
		flex: 1;
	}

	.speed-btn {
		flex: 1;
		padding: 4px 0;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition: background 0.1s, color 0.1s;
	}

	.speed-btn:first-child {
		border-radius: var(--radius-sm) 0 0 var(--radius-sm);
	}

	.speed-btn:last-child {
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
	}

	.speed-btn:not(:first-child) {
		border-left: none;
	}

	.speed-btn:hover:not(.active) {
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-primary);
	}

	.speed-btn.active {
		background: var(--accent-orange);
		border-color: var(--accent-orange);
		color: #000;
		font-weight: 700;
	}

	/* Adjacent active buttons need left border */
	.speed-btn.active + .speed-btn {
		border-left: 1px solid var(--border-default);
	}

	/* ── Loop toggle ── */
	.loop-row {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.loop-toggle {
		display: flex;
		align-items: center;
		gap: 4px;
		cursor: pointer;
	}

	.loop-toggle input[type="checkbox"] {
		accent-color: var(--accent-orange);
		width: 13px;
		height: 13px;
	}

	.loop-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		color: var(--text-tertiary);
		letter-spacing: 0.06em;
	}

	.playback-info {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--accent-orange);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.loop-indicator {
		padding: 0 3px;
		background: var(--accent-orange-dim);
		border-radius: 2px;
		font-size: 8px;
		font-weight: 700;
	}

	/* ── Quick replay presets ── */
	.quick-row {
		display: flex;
		gap: 4px;
	}

	.quick-btn {
		flex: 1;
		padding: 5px 4px;
		background: var(--accent-orange-dim);
		border: 1px solid var(--accent-orange-medium);
		border-radius: var(--radius-sm);
		color: var(--accent-orange);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		cursor: pointer;
		white-space: nowrap;
		transition: background 0.1s;
	}

	.quick-btn:hover:not(:disabled) {
		background: var(--accent-orange-light);
	}

	.quick-btn:active:not(:disabled) {
		background: var(--accent-orange-medium);
		box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.3);
	}

	.quick-btn:disabled {
		opacity: 0.35;
		cursor: default;
	}

	/* ── Buffer chips ── */
	.buffer-row {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
	}

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
	}

	.buffer-chip.active {
		border-color: var(--accent-orange-medium);
		color: var(--accent-orange);
	}

	.buffer-dur {
		font-family: var(--font-mono);
		font-size: 8px;
		opacity: 0.7;
	}

	/* ── Clip Workspace ── */
	.clip-workspace {
		border-top: 1px solid var(--border-default);
		margin-top: 4px;
	}

	.workspace-toggle {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 4px 6px;
		background: none;
		border: none;
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		cursor: pointer;
	}

	.workspace-toggle:hover {
		color: var(--text-secondary);
	}

	.toggle-arrow {
		font-size: 10px;
	}

	.workspace-content {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 0 6px 6px;
	}

	.mark-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.mark-in-btn {
		background: rgba(22, 163, 74, 0.12);
		border-color: var(--tally-preview-medium);
		color: var(--tally-preview);
	}

	.mark-in-btn:hover:not(:disabled) {
		background: rgba(22, 163, 74, 0.2);
	}

	.mark-out-btn {
		background: rgba(220, 38, 38, 0.12);
		border-color: var(--tally-program-medium);
		color: var(--tally-program);
	}

	.mark-out-btn:hover:not(:disabled) {
		background: rgba(220, 38, 38, 0.2);
	}

	.mark-info {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.mark-label {
		color: var(--text-secondary);
		font-weight: 600;
	}

	.mark-sep {
		opacity: 0.4;
	}

	.clip-dur {
		color: var(--accent-orange);
		margin-left: 4px;
	}

	.buffer-strip {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
	}

	.buffer-chip-ws {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 6px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		cursor: pointer;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.buffer-chip-ws.active {
		border-color: var(--accent-orange-medium);
		background: var(--accent-orange-dim);
		color: var(--text-primary);
	}

	.chip-label {
		min-width: 48px;
	}

	.chip-bar {
		width: 40px;
		height: 4px;
		background: rgba(255, 255, 255, 0.06);
		border-radius: 2px;
		overflow: hidden;
	}

	.chip-fill {
		height: 100%;
		background: var(--accent-orange);
		border-radius: 2px;
	}

	.chip-dur {
		min-width: 28px;
		text-align: right;
	}
</style>
