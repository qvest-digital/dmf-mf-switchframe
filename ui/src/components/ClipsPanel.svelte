<script lang="ts">
	import type { ControlRoomState, ClipInfo, ClipPlayerState, RecordingFileInfo, ClipUploadProgress } from '$lib/api/types';
	import {
		listClips, uploadClip, deleteClip, pinClip,
		listRecordings, importRecording,
		clipPlayerLoad, clipPlayerEject, clipPlayerPlay, clipPlayerPause, clipPlayerStop, clipPlayerSeek,
		apiCall,
	} from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── State ──
	let clips = $state<ClipInfo[]>([]);
	let recordings = $state<RecordingFileInfo[]>([]);
	let uploading = $state(false);
	let uploadPercent = $state(0);
	let fileInput: HTMLInputElement | undefined = $state();
	let playerSpeeds = $state<Record<number, number>>({ 1: 1.0, 2: 1.0, 3: 1.0, 4: 1.0 });
	let playerLoops = $state<Record<number, boolean>>({ 1: false, 2: false, 3: false, 4: false });

	// ── Derived ──
	const players = $derived<ClipPlayerState[]>(crState.clipPlayers ?? [
		{ id: 1, state: 'empty' as const },
		{ id: 2, state: 'empty' as const },
		{ id: 3, state: 'empty' as const },
		{ id: 4, state: 'empty' as const },
	]);

	const uploadedClips = $derived(clips.filter(c => c.source === 'upload'));
	const replayClips = $derived(clips.filter(c => c.source === 'replay'));
	const uploadProgress = $derived<ClipUploadProgress | undefined>(crState.clipUpload);

	// Map 4 stages to overall 0-100 (weighted toward transcode which is longest).
	const stageWeights: Record<string, [number, number]> = {
		uploading: [0, 25],
		analyzing: [25, 50],
		transcoding: [50, 90],
		validating: [90, 100],
	};

	const overallPercent = $derived.by(() => {
		if (!uploading && !uploadProgress) return 0;
		const stage = uploadProgress?.stage ?? 'uploading';
		const stagePct = uploadProgress?.percent ?? uploadPercent;
		const [lo, hi] = stageWeights[stage] ?? [0, 25];
		// During uploading stage, use client-side uploadPercent if no server stage yet
		if (stage === 'uploading') {
			return lo + Math.round((uploadPercent / 100) * (hi - lo));
		}
		return lo + Math.round((stagePct / 100) * (hi - lo));
	});

	const stageLabel = $derived.by(() => {
		const stage = uploadProgress?.stage;
		if (!stage) return uploading ? 'Uploading...' : '';
		switch (stage) {
			case 'uploading': return 'Uploading...';
			case 'analyzing': return 'Analyzing...';
			case 'transcoding': return `Transcoding... ${uploadProgress?.percent ?? 0}%`;
			case 'validating': return 'Validating...';
			default: return 'Processing...';
		}
	});

	// ── Effects ──
	let prevClipCount: number | undefined;
	$effect(() => {
		const count = crState.clipCount;
		if (count !== prevClipCount) {
			prevClipCount = count;
			refreshClips();
		}
	});

	// Fetch recordings once on mount (no reactive dependency needed).
	let recordingsLoaded = false;
	$effect(() => {
		if (!recordingsLoaded) {
			recordingsLoaded = true;
			refreshRecordings();
		}
	});

	// Sync server-side loop/speed into local UI state
	$effect(() => {
		for (const p of players) {
			if (p.speed != null) playerSpeeds[p.id] = p.speed;
			if (p.loop != null) playerLoops[p.id] = p.loop;
		}
	});

	// ── Helpers ──
	function formatDuration(ms: number): string {
		const totalSecs = Math.floor(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	function formatBytes(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
		return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
	}

	const emptyPlayerIds = $derived(players.filter(p => p.state === 'empty').map(p => p.id));

	// ── Handlers ──
	async function refreshClips() {
		try {
			clips = await listClips();
		} catch {
			// ignore on load
		}
	}

	async function refreshRecordings() {
		try {
			recordings = await listRecordings();
		} catch {
			// ignore on load
		}
	}

	function handleUpload() {
		fileInput?.click();
	}

	async function onFileSelected(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		uploading = true;
		uploadPercent = 0;
		try {
			await uploadClip(file, (pct) => { uploadPercent = pct; });
			notify('info', `Uploaded "${file.name}"`);
			await refreshClips();
		} catch (err) {
			notify('error', `Upload failed: ${err instanceof Error ? err.message : 'unknown'}`);
		} finally {
			uploading = false;
			uploadPercent = 0;
			input.value = '';
		}
	}

	async function handleDelete(clipId: string) {
		try {
			await deleteClip(clipId);
			await refreshClips();
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function handlePin(clipId: string) {
		try {
			await pinClip(clipId);
			notify('info', 'Clip pinned');
			await refreshClips();
		} catch (err) {
			notify('error', `Pin failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	function handleLoad(playerID: number, clipId: string) {
		apiCall(clipPlayerLoad(playerID, clipId), 'Load clip');
	}

	function handlePlay(playerID: number) {
		const speed = playerSpeeds[playerID] ?? 1.0;
		const loop = playerLoops[playerID] ?? false;
		apiCall(clipPlayerPlay(playerID, speed, loop), 'Play clip');
	}

	function handlePause(playerID: number) {
		apiCall(clipPlayerPause(playerID), 'Pause clip');
	}

	function handleStop(playerID: number) {
		apiCall(clipPlayerStop(playerID), 'Stop clip');
	}

	function handleEject(playerID: number) {
		apiCall(clipPlayerEject(playerID), 'Eject clip');
	}

	function handleSeek(playerID: number, e: MouseEvent) {
		const bar = e.currentTarget as HTMLElement;
		const rect = bar.getBoundingClientRect();
		const pos = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
		apiCall(clipPlayerSeek(playerID, pos), 'Seek');
	}

	function handleSeekDelta(playerID: number, delta: number) {
		const player = players.find(p => p.id === playerID);
		const pos = Math.max(0, Math.min(1, (player?.position ?? 0) + delta));
		apiCall(clipPlayerSeek(playerID, pos), 'Seek');
	}

	async function handleImport(path: string) {
		try {
			await importRecording(path);
			notify('info', 'Recording imported');
			await refreshClips();
			await refreshRecordings();
		} catch (err) {
			notify('error', `Import failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}
</script>

<div class="clips-panel">
	<div class="clips-layout">
		<!-- Left: Clip Library -->
		<div class="clip-library">
			<div class="library-header">
				<h3>CLIPS</h3>
				<div class="library-actions">
					<button class="action-btn upload-btn" onclick={handleUpload} disabled={uploading}>
						{uploading ? 'Uploading...' : 'Upload'}
					</button>
					<input
						bind:this={fileInput}
						type="file"
						accept=".ts,.mp4,.mov,.m4v,.mkv,.webm,.avi,.flv,.mxf,.wmv,.mpg,.mpeg,.ogv"
						class="file-input"
						onchange={onFileSelected}
					/>
				</div>
			</div>

			{#if uploading || uploadProgress}
				<div class="upload-progress">
					<div class="upload-progress-label">
						<span class="upload-stage">{stageLabel}</span>
						{#if uploadProgress?.filename}
							<span class="upload-filename" title={uploadProgress.filename}>{uploadProgress.filename}</span>
						{/if}
						<span class="upload-pct">{overallPercent}%</span>
					</div>
					<div class="upload-progress-bar">
						<div class="upload-progress-fill" style="width: {overallPercent}%"></div>
					</div>
					<div class="upload-stages">
						<span class="stage-dot" class:active={!uploadProgress || uploadProgress.stage === 'uploading'} class:done={uploadProgress && uploadProgress.stage !== 'uploading'}>Upload</span>
						<span class="stage-dot" class:active={uploadProgress?.stage === 'analyzing'} class:done={uploadProgress && ['transcoding','validating'].includes(uploadProgress.stage)}>Analyze</span>
						<span class="stage-dot" class:active={uploadProgress?.stage === 'transcoding'} class:done={uploadProgress?.stage === 'validating'}>Transcode</span>
						<span class="stage-dot" class:active={uploadProgress?.stage === 'validating'}>Validate</span>
					</div>
				</div>
			{/if}

			<div class="library-sections">
				<!-- Uploaded -->
				<div class="library-section">
					<div class="section-header">Uploaded</div>
					{#if uploadedClips.length === 0}
						<div class="empty-hint">No uploaded clips</div>
					{:else}
						{#each uploadedClips as clip (clip.id)}
							<div class="clip-item">
								<div class="clip-info">
									<span class="clip-name" title={clip.filename}>{clip.name}</span>
									<span class="clip-meta">{formatDuration(clip.durationMs)}</span>
								</div>
								<div class="clip-actions">
									{#each emptyPlayerIds as pid}
										<button
											class="load-btn"
											title={`Load into Player ${pid}`}
											onclick={() => handleLoad(pid, clip.id)}
										>&rarr;{pid}</button>
									{/each}
									<button class="del-btn" title="Delete" onclick={() => handleDelete(clip.id)}>&times;</button>
								</div>
							</div>
						{/each}
					{/if}
				</div>

				<!-- Replay Clips -->
				<div class="library-section">
					<div class="section-header">Replay Clips</div>
					{#if replayClips.length === 0}
						<div class="empty-hint">No replay clips</div>
					{:else}
						{#each replayClips as clip (clip.id)}
							<div class="clip-item">
								<div class="clip-info">
									<span class="clip-name">{clip.name}</span>
									<span class="clip-meta">{formatDuration(clip.durationMs)}</span>
								</div>
								<div class="clip-actions">
									{#each emptyPlayerIds as pid}
										<button
											class="load-btn"
											title={`Load into Player ${pid}`}
											onclick={() => handleLoad(pid, clip.id)}
										>&rarr;{pid}</button>
									{/each}
									{#if clip.ephemeral}
										<button class="pin-btn" title="Pin (make permanent)" onclick={() => handlePin(clip.id)}>Pin</button>
									{/if}
									<button class="del-btn" title="Delete" onclick={() => handleDelete(clip.id)}>&times;</button>
								</div>
							</div>
						{/each}
					{/if}
				</div>

				<!-- Recordings -->
				<div class="library-section">
					<div class="section-header">Recordings</div>
					{#if recordings.length === 0}
						<div class="empty-hint">No recordings</div>
					{:else}
						{#each recordings as rec (rec.path)}
							<div class="clip-item">
								<div class="clip-info">
									<span class="clip-name">{rec.filename}</span>
									<span class="clip-meta">{formatBytes(rec.byteSize)}</span>
								</div>
								<div class="clip-actions">
									<button class="import-btn" onclick={() => handleImport(rec.path)}>Import</button>
								</div>
							</div>
						{/each}
					{/if}
				</div>
			</div>
		</div>

		<!-- Right: Players -->
		<div class="player-strip">
			{#each players as player (player.id)}
				<div class="player-slot" class:playing={player.state === 'playing'} class:paused={player.state === 'paused'}>
					<div class="player-header">
						<span class="player-number">Player {player.id}</span>
						{#if player.state !== 'empty'}
							<span class="player-clip-name">{player.clipName ?? 'Unknown'}</span>
						{/if}
						{#if player.state === 'playing'}
							<span class="player-state-badge playing-badge">PLAYING</span>
						{:else if player.state === 'paused'}
							<span class="player-state-badge paused-badge">PAUSED</span>
						{:else if player.state === 'holding'}
							<span class="player-state-badge holding-badge">HOLD</span>
						{:else if player.state === 'loaded'}
							<span class="player-state-badge loaded-badge">READY</span>
						{/if}
					</div>

					{#if player.state === 'empty'}
						<div class="player-empty">Load a clip from the library</div>
					{:else}
						<!-- Speed + Loop -->
						<div class="player-controls-row">
							<select
								class="speed-select"
								bind:value={playerSpeeds[player.id]}
								disabled={player.state === 'playing'}
							>
								<option value={0.25}>0.25x</option>
								<option value={0.5}>0.5x</option>
								<option value={1.0}>1.0x</option>
								<option value={1.5}>1.5x</option>
								<option value={2.0}>2.0x</option>
							</select>
							<label class="loop-label">
								<input
									type="checkbox"
									bind:checked={playerLoops[player.id]}
									disabled={player.state === 'playing'}
								/>
								Loop
							</label>
						</div>

						<!-- Transport buttons -->
						<div class="transport-row">
							{#if player.state === 'playing'}
								<button class="transport-btn pause-btn" onclick={() => handlePause(player.id)} title="Pause">
									&#x23F8;
								</button>
							{:else}
								<button class="transport-btn play-btn" onclick={() => handlePlay(player.id)} title="Play">
									&#x25B6;
								</button>
							{/if}
							<button class="transport-btn stop-btn" onclick={() => handleStop(player.id)} title="Stop"
								disabled={player.state === 'loaded'}
							>
								&#x25A0;
							</button>
							<button class="transport-btn eject-btn" onclick={() => handleEject(player.id)} title="Eject">
								&#x23CF;
							</button>
						</div>

						<!-- Progress bar -->
						<div
							class="progress-bar"
							role="slider"
							tabindex="0"
							aria-valuenow={Math.round((player.position ?? 0) * 100)}
							aria-valuemin={0}
							aria-valuemax={100}
							aria-label="Playback position"
							onclick={(e) => handleSeek(player.id, e)}
							onkeydown={(e) => {
								if (e.key === 'ArrowRight') { e.preventDefault(); handleSeekDelta(player.id, 0.05); }
								else if (e.key === 'ArrowLeft') { e.preventDefault(); handleSeekDelta(player.id, -0.05); }
							}}
						>
							<div class="progress-fill" style="width: {(player.position ?? 0) * 100}%"></div>
						</div>
					{/if}
				</div>
			{/each}
		</div>
	</div>
</div>

<style>
	.clips-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
		padding: 6px;
	}

	.clips-layout {
		display: flex;
		gap: 8px;
		height: 100%;
		overflow: hidden;
	}

	/* ── Clip Library (left column) ── */
	.clip-library {
		flex: 0 0 40%;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.library-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 6px;
	}

	h3 {
		margin: 0;
		padding: 0 2px;
		font-size: var(--text-xs);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	.library-actions {
		display: flex;
		gap: 4px;
	}

	.file-input {
		display: none;
	}

	.action-btn {
		padding: 3px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		cursor: pointer;
	}

	.action-btn:hover:not(:disabled) {
		background: var(--bg-base);
	}

	.action-btn:disabled {
		opacity: 0.5;
		cursor: default;
	}

	.library-sections {
		flex: 1;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.library-section {
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		padding: 4px;
	}

	.section-header {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		padding: 2px 4px;
		margin-bottom: 2px;
	}

	.empty-hint {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		padding: 4px;
		text-align: center;
		font-style: italic;
	}

	.clip-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 3px 4px;
		border-radius: var(--radius-sm);
		gap: 4px;
	}

	.clip-item:hover {
		background: var(--bg-base);
	}

	.clip-info {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
		flex: 1;
	}

	.clip-name {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.clip-meta {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.clip-actions {
		display: flex;
		gap: 2px;
		flex-shrink: 0;
	}

	.load-btn, .del-btn, .pin-btn, .import-btn {
		padding: 1px 5px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		cursor: pointer;
		line-height: 1.4;
	}

	.load-btn:hover {
		background: var(--accent-blue-dim);
		color: var(--accent-blue);
		border-color: var(--accent-blue-medium);
	}

	.del-btn:hover {
		background: rgba(220, 38, 38, 0.15);
		color: var(--tally-program);
		border-color: var(--tally-program-medium);
	}

	.pin-btn:hover {
		background: rgba(22, 163, 74, 0.15);
		color: var(--tally-preview);
		border-color: var(--tally-preview-medium);
	}

	.import-btn:hover {
		background: var(--accent-blue-dim);
		color: var(--accent-blue);
		border-color: var(--accent-blue-medium);
	}

	/* ── Upload Progress ── */
	.upload-progress {
		margin-bottom: 6px;
		padding: 6px;
		border: 1px solid var(--accent-blue-medium, var(--border-default));
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
	}

	.upload-progress-label {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 4px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
	}

	.upload-stage {
		color: var(--accent-blue);
		font-weight: 600;
	}

	.upload-filename {
		color: var(--text-secondary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		flex: 1;
		min-width: 0;
	}

	.upload-pct {
		color: var(--text-tertiary);
		font-family: var(--font-mono);
		flex-shrink: 0;
	}

	.upload-progress-bar {
		height: 4px;
		background: var(--bg-base);
		border-radius: 2px;
		overflow: hidden;
		margin-bottom: 4px;
	}

	.upload-progress-fill {
		height: 100%;
		background: var(--accent-blue);
		border-radius: 2px;
		transition: width 0.2s ease;
	}

	.upload-stages {
		display: flex;
		gap: 8px;
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.stage-dot {
		opacity: 0.4;
	}

	.stage-dot.active {
		opacity: 1;
		color: var(--accent-blue);
		font-weight: 700;
	}

	.stage-dot.done {
		opacity: 0.7;
		color: var(--tally-preview, #16a34a);
	}

	/* ── Player Strip (right column) ── */
	.player-strip {
		flex: 0 0 60%;
		display: flex;
		flex-direction: column;
		gap: 4px;
		overflow-y: auto;
	}

	.player-slot {
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		padding: 6px;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.player-slot.playing {
		border-color: var(--tally-program-medium);
	}

	.player-slot.paused {
		border-color: var(--accent-amber-medium, var(--border-default));
	}

	.player-header {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.player-number {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.player-clip-name {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		flex: 1;
	}

	.player-state-badge {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		padding: 1px 4px;
		border-radius: 2px;
		flex-shrink: 0;
	}

	.playing-badge {
		background: rgba(220, 38, 38, 0.2);
		color: var(--tally-program);
	}

	.paused-badge {
		background: rgba(245, 158, 11, 0.2);
		color: var(--accent-amber, #f59e0b);
	}

	.holding-badge {
		background: rgba(139, 92, 246, 0.2);
		color: var(--accent-purple, #8b5cf6);
	}

	.loaded-badge {
		background: rgba(59, 130, 246, 0.15);
		color: var(--accent-blue);
	}

	.player-empty {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		text-align: center;
		padding: 6px;
		font-style: italic;
	}

	.player-controls-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.speed-select {
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
	}

	.speed-select:disabled {
		opacity: 0.5;
	}

	.loop-label {
		display: flex;
		align-items: center;
		gap: 3px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		margin-left: auto;
	}

	.transport-row {
		display: flex;
		gap: 3px;
	}

	.transport-btn {
		flex: 1;
		padding: 3px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-primary);
		font-size: 12px;
		cursor: pointer;
		text-align: center;
	}

	.transport-btn:hover:not(:disabled) {
		background: var(--bg-surface);
	}

	.transport-btn:disabled {
		opacity: 0.4;
		cursor: default;
	}

	.play-btn:hover:not(:disabled) {
		background: rgba(59, 130, 246, 0.15);
		color: var(--accent-blue);
		border-color: var(--accent-blue-medium);
	}

	.pause-btn:hover:not(:disabled) {
		background: rgba(245, 158, 11, 0.15);
		color: var(--accent-amber, #f59e0b);
	}

	.stop-btn:hover:not(:disabled) {
		background: rgba(220, 38, 38, 0.15);
		color: var(--tally-program);
		border-color: var(--tally-program-medium);
	}

	.eject-btn:hover:not(:disabled) {
		color: var(--text-secondary);
	}

	.progress-bar {
		height: 6px;
		background: var(--bg-base);
		border-radius: 3px;
		cursor: pointer;
		overflow: hidden;
	}

	.progress-fill {
		height: 100%;
		background: var(--accent-blue);
		border-radius: 3px;
		transition: width 0.1s linear;
	}

	.player-slot.playing .progress-fill {
		background: var(--tally-program);
	}
</style>
