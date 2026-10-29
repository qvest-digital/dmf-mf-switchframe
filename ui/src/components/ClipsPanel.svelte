<script lang="ts">
	import type { ControlRoomState, ClipInfo, ClipPlayerState, RecordingFileInfo, EngineRecordingGroup, ClipUploadProgress } from '$lib/api/types';
	import {
		listClipsOnEveryEngine, uploadClip, deleteClip, pinClip,
		listRecordingsDualEngine, importRecordingFromEngine, recordingDownloadUrl,
		clipPlayerLoadByName, clipPlayerEject, clipPlayerPlay, clipPlayerPause, clipPlayerStop, clipPlayerSeek,
		apiCall,
	} from '$lib/api/switch-api';
	import { resolveApiUrl } from '$lib/api/base-url';
	import { deleteConfirmation } from '$lib/clips/delete-confirm';
	import ConfirmDialog from './ConfirmDialog.svelte';
	import { notify } from '$lib/state/notifications.svelte';
	import {
		ICON_PLAY, ICON_PAUSE, ICON_STOP, ICON_EJECT, ICON_UPLOAD, ICON_TRASH, ICON_CHEVRON_DOWN, ICON_CHEVRON_RIGHT,
		ICON_PIN, ICON_FILM, renderIcon,
	} from '$lib/icons';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── State ──
	let clips = $state<ClipInfo[]>([]);
	let recordingGroups = $state<EngineRecordingGroup[]>([]);
	let uploading = $state(false);
	let uploadPercent = $state(0);
	let fileInput: HTMLInputElement | undefined = $state();
	let playerSpeeds = $state<Record<number, number>>({ 1: 1.0, 2: 1.0, 3: 1.0, 4: 1.0 });
	let playerLoops = $state<Record<number, boolean>>({ 1: false, 2: false, 3: false, 4: false });

	let sectionOpen = $state({ uploaded: true, replay: true, recordings: true });
	let uploadZoneDragDepth = $state(0);
	let dragOverPlayerId = $state<number | null>(null);

	// ── Derived ──
	const players = $derived<ClipPlayerState[]>(crState.clipPlayers ?? [
		{ id: 1, state: 'empty' as const },
		{ id: 2, state: 'empty' as const },
		{ id: 3, state: 'empty' as const },
		{ id: 4, state: 'empty' as const },
	]);

	const uploadedClips = $derived(clips.filter(c => c.source === 'upload'));
	const replayClips = $derived(clips.filter(c => c.source === 'replay'));
	const totalRecordings = $derived(recordingGroups.reduce((sum, g) => sum + g.recordings.length, 0));
	const isDualEngine = $derived(recordingGroups.length > 1);
	const uploadProgress = $derived<ClipUploadProgress | undefined>(crState.clipUpload);

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
		if (stage === 'uploading') {
			// Only the uploading browser counts the bytes it sends; any
			// other one shows the engine's figure.
			const pct = uploading ? uploadPercent : stagePct;
			return lo + Math.round((pct / 100) * (hi - lo));
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

	// The list is read again whenever a recording starts or stops, and every
	// RECORDING_POLL_MS while a file is being written or finalized, so sizes
	// and states follow the recording rather than the moment the panel opened.
	const RECORDING_POLL_MS = 2000;
	const recordingKey = $derived(
		`${crState.recording?.active ?? false}|${crState.recording?.filename ?? ''}|${crState.recording?.error ?? ''}`,
	);
	$effect(() => {
		void recordingKey;
		refreshRecordings();
	});
	const recordingsInProgress = $derived(
		recordingGroups.some((g) => g.recordings.some((r) => r.state === 'recording' || r.state === 'finalizing')),
	);
	$effect(() => {
		if (!recordingsInProgress) return;
		const id = setInterval(refreshRecordings, RECORDING_POLL_MS);
		return () => clearInterval(id);
	});

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

	function slotDragOver(playerId: number): boolean {
		return dragOverPlayerId === playerId;
	}

	function onPlayerSlotDragEnter(e: DragEvent, playerId: number) {
		e.preventDefault();
		const cur = e.currentTarget as HTMLElement;
		const rel = e.relatedTarget as Node | null;
		if (rel && cur.contains(rel)) return;
		dragOverPlayerId = playerId;
	}

	function onPlayerSlotDragLeave(e: DragEvent, playerId: number) {
		const cur = e.currentTarget as HTMLElement;
		const rel = e.relatedTarget as Node | null;
		if (rel && cur.contains(rel)) return;
		if (dragOverPlayerId === playerId) dragOverPlayerId = null;
	}

	function resetPlayerDropHighlight() {
		dragOverPlayerId = null;
	}

	// ── Handlers ──
	async function refreshClips() {
		try {
			clips = await listClipsOnEveryEngine();
		} catch {
			// ignore on load
		}
	}

	async function refreshRecordings() {
		try {
			recordingGroups = await listRecordingsDualEngine();
		} catch {
			// ignore on load
		}
	}

	function handleUpload() {
		fileInput?.click();
	}

	async function uploadSelectedFile(file: File) {
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
		}
	}

	async function onFileSelected(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		await uploadSelectedFile(file);
		input.value = '';
	}

	function onUploadZoneDragEnter(e: DragEvent) {
		e.preventDefault();
		if (e.dataTransfer?.types.includes('Files')) uploadZoneDragDepth += 1;
	}

	function onUploadZoneDragLeave(e: DragEvent) {
		e.preventDefault();
		const related = e.relatedTarget as Node | null;
		if (related && e.currentTarget instanceof HTMLElement && e.currentTarget.contains(related)) return;
		uploadZoneDragDepth = Math.max(0, uploadZoneDragDepth - 1);
	}

	function onUploadZoneDragOver(e: DragEvent) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
	}

	async function onUploadZoneDrop(e: DragEvent) {
		e.preventDefault();
		uploadZoneDragDepth = 0;
		const file = e.dataTransfer?.files?.[0];
		if (!file) return;
		await uploadSelectedFile(file);
	}

	function onClipDragStart(e: DragEvent, clipName: string) {
		e.dataTransfer?.setData('text/plain', clipName);
		e.dataTransfer!.effectAllowed = 'copy';
	}

	function onPlayerDragOver(e: DragEvent) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
	}

	function handleLoadClip(clipName: string, playerId: number) {
		apiCall(clipPlayerLoadByName(playerId, clipName), 'Load clip');
	}

	function onPlayerDrop(e: DragEvent, playerId: number) {
		e.preventDefault();
		const clipName = e.dataTransfer?.getData('text/plain')?.trim();
		resetPlayerDropHighlight();
		if (clipName) handleLoadClip(clipName, playerId);
	}

	function onPlayerDragEnd() {
		resetPlayerDropHighlight();
		uploadZoneDragDepth = 0;
	}

	// A library clip waiting for the operator to confirm its deletion.
	let pendingDelete = $state<ClipInfo | null>(null);
	const pendingConfirm = $derived(pendingDelete ? deleteConfirmation(pendingDelete) : null);

	function requestDelete(clip: ClipInfo) {
		if (deleteConfirmation(clip)) pendingDelete = clip;
		else handleDelete(clip.id);
	}

	function confirmDelete() {
		const clip = pendingDelete;
		pendingDelete = null;
		if (clip) handleDelete(clip.id);
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

	const recordingStateLabel: Record<string, string> = {
		recording: 'Recording',
		finalizing: 'Finalizing',
		ready: 'Ready',
		failed: 'Failed',
	};

	async function handleDownload(engineUrl: string, rec: RecordingFileInfo) {
		try {
			const a = document.createElement('a');
			a.href = await recordingDownloadUrl(engineUrl, rec.path);
			a.download = rec.filename;
			a.click();
		} catch (err) {
			notify('error', `Download failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function handleImport(engineUrl: string, path: string) {
		try {
			await importRecordingFromEngine(engineUrl, path);
			notify('info', 'Recording imported');
			await refreshClips();
			await refreshRecordings();
		} catch (err) {
			notify('error', `Import failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	function toggleSection(key: keyof typeof sectionOpen) {
		sectionOpen = { ...sectionOpen, [key]: !sectionOpen[key] };
	}
</script>

<svelte:window ondragend={onPlayerDragEnd} />

<div class="clips-panel">
	<div class="clips-layout">
		<!-- Left: Clip library -->
		<div class="clip-library">
			<div
				class="upload-zone"
				class:drag-over={uploadZoneDragDepth > 0}
				role="button"
				tabindex="0"
				ondragenter={onUploadZoneDragEnter}
				ondragleave={onUploadZoneDragLeave}
				ondragover={onUploadZoneDragOver}
				ondrop={onUploadZoneDrop}
				onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleUpload(); } }}
				onclick={handleUpload}
			>
				<span class="upload-zone-icon" aria-hidden="true">{@html renderIcon(ICON_UPLOAD, 14)}</span>
				<div class="upload-zone-text">
					<span class="upload-zone-title">Add clips</span>
					<span class="upload-zone-hint">Drop files or click to browse</span>
				</div>
				<input
					bind:this={fileInput}
					type="file"
					accept=".ts,.mp4,.mov,.m4v,.mkv,.webm,.avi,.flv,.mxf,.wmv,.mpg,.mpeg,.ogv"
					class="file-input"
					onchange={onFileSelected}
				/>
				{#if uploading || uploadProgress}
					<div class="upload-progress upload-progress-inline" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()} role="presentation">
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
			</div>

			<div class="library-scroll">
				<!-- Uploaded -->
				<div class="library-section">
					<button type="button" class="section-toggle" onclick={() => toggleSection('uploaded')}>
						<span class="section-chevron" aria-hidden="true">
							{@html renderIcon(sectionOpen.uploaded ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
						</span>
						<span class="section-toggle-label">Uploaded</span>
						<span class="section-badge">{uploadedClips.length}</span>
					</button>
					{#if sectionOpen.uploaded}
						<div class="section-body">
							{#if uploadedClips.length === 0}
								<div class="empty-hint">No uploaded clips</div>
							{:else}
								<div class="section-list" role="list">
								{#each uploadedClips as clip (clip.id)}
									<div
										class="clip-item"
										role="listitem"
										draggable="true"
										ondragstart={(e) => onClipDragStart(e, clip.name)}
									>
										<div class="clip-info">
											<span class="clip-drag-hint" aria-hidden="true">{@html renderIcon(ICON_FILM, 12)}</span>
											<span class="clip-name" title={clip.filename}>{clip.name}</span>
											<span class="clip-meta">{formatDuration(clip.durationMs)}</span>
										</div>
										<div class="clip-actions">
											{#each emptyPlayerIds as pid}
												<button
													type="button"
													class="load-btn"
													draggable="false"
													ondragstart={(e) => e.stopPropagation()}
													title={`Load into Player ${pid}`}
													onclick={() => handleLoadClip(clip.name, pid)}
												>{pid}</button>
											{/each}
											<button type="button" class="icon-btn danger" draggable="false" title="Delete" ondragstart={(e) => e.stopPropagation()} onclick={() => requestDelete(clip)}>
												{@html renderIcon(ICON_TRASH, 12)}
											</button>
										</div>
									</div>
								{/each}
								</div>
							{/if}
						</div>
					{/if}
				</div>

				<!-- Replay -->
				<div class="library-section">
					<button type="button" class="section-toggle" onclick={() => toggleSection('replay')}>
						<span class="section-chevron" aria-hidden="true">
							{@html renderIcon(sectionOpen.replay ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
						</span>
						<span class="section-toggle-label">Replay</span>
						<span class="section-badge">{replayClips.length}</span>
					</button>
					{#if sectionOpen.replay}
						<div class="section-body">
							{#if replayClips.length === 0}
								<div class="empty-hint">No replay clips</div>
							{:else}
								<div class="section-list" role="list">
								{#each replayClips as clip (clip.id)}
									<div
										class="clip-item"
										role="listitem"
										draggable="true"
										ondragstart={(e) => onClipDragStart(e, clip.name)}
									>
										<div class="clip-info">
											<span class="clip-drag-hint" aria-hidden="true">{@html renderIcon(ICON_FILM, 12)}</span>
											<span class="clip-name" title={clip.filename}>{clip.name}</span>
											<span class="clip-meta">{formatDuration(clip.durationMs)}</span>
										</div>
										<div class="clip-actions">
											{#each emptyPlayerIds as pid}
												<button
													type="button"
													class="load-btn"
													draggable="false"
													ondragstart={(e) => e.stopPropagation()}
													title={`Load into Player ${pid}`}
													onclick={() => handleLoadClip(clip.name, pid)}
												>{pid}</button>
											{/each}
											{#if clip.ephemeral}
												<button type="button" class="icon-btn pin" draggable="false" title="Pin (make permanent)" ondragstart={(e) => e.stopPropagation()} onclick={() => handlePin(clip.id)}>
													{@html renderIcon(ICON_PIN, 12)}
												</button>
											{/if}
											<button type="button" class="icon-btn danger" draggable="false" title="Delete" ondragstart={(e) => e.stopPropagation()} onclick={() => requestDelete(clip)}>
												{@html renderIcon(ICON_TRASH, 12)}
											</button>
										</div>
									</div>
								{/each}
								</div>
							{/if}
						</div>
					{/if}
				</div>

				<!-- Recordings -->
				<div class="library-section">
					<button type="button" class="section-toggle" onclick={() => toggleSection('recordings')}>
						<span class="section-chevron" aria-hidden="true">
							{@html renderIcon(sectionOpen.recordings ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
						</span>
						<span class="section-toggle-label">Recordings</span>
						<span class="section-badge">{totalRecordings}</span>
					</button>
					{#if sectionOpen.recordings}
						<div class="section-body">
							{#if totalRecordings === 0}
								<div class="empty-hint">No recordings</div>
							{:else}
								{#each recordingGroups as group (group.engineId)}
									{#if isDualEngine}
										<div class="engine-group-label">Engine {group.engineId}</div>
									{/if}
									{#if isDualEngine && group.recordings.length === 0}
										<div class="empty-hint">No recordings</div>
									{/if}
									{#each group.recordings as rec (rec.path)}
										<div class="clip-item recording-item" data-state={rec.state}>
											<div class="clip-info">
												<span class="clip-name">{rec.filename}</span>
												<span class="clip-meta">
													<span class="rec-state rec-state-{rec.state}" title={rec.error ?? ''}>{recordingStateLabel[rec.state] ?? rec.state}</span>
													{formatBytes(rec.byteSize)}
												</span>
												{#if rec.state === 'failed' && rec.error}
													<span class="rec-error">{rec.error}</span>
												{/if}
											</div>
											{#if rec.state === 'ready'}
												<div class="clip-actions">
													<button type="button" class="download-btn" title="Download recording" onclick={() => handleDownload(group.engineUrl, rec)}>DL</button>
													<button type="button" class="import-btn" onclick={() => handleImport(group.engineUrl, rec.path)}>Import</button>
												</div>
											{/if}
										</div>
									{/each}
								{/each}
							{/if}
						</div>
					{/if}
				</div>
			</div>
		</div>

		<!-- Right: Player slots -->
		<div class="players-pane">
			<div class="players-header">
				<h3 class="players-title">Players</h3>
			</div>
			<div class="player-grid">
				{#each players as player (player.id)}
					<div
						class="player-card"
						role="group"
						aria-label="Player {player.id} clip slot"
						class:playing={player.state === 'playing'}
						class:paused={player.state === 'paused'}
						class:drag-over={slotDragOver(player.id)}
						ondragenter={(e) => onPlayerSlotDragEnter(e, player.id)}
						ondragleave={(e) => onPlayerSlotDragLeave(e, player.id)}
						ondragover={onPlayerDragOver}
						ondrop={(e) => onPlayerDrop(e, player.id)}
					>
						<div class="player-card-top">
							<span class="player-slot-id">P{player.id}</span>
							{#if player.state !== 'empty'}
								<span class="player-clip-title" title={player.clipName ?? ''}>{player.clipName ?? 'Unknown'}</span>
							{/if}
							{#if player.state === 'playing'}
								<span class="state-pill playing">Play</span>
							{:else if player.state === 'paused'}
								<span class="state-pill paused">Pause</span>
							{:else if player.state === 'holding'}
								<span class="state-pill hold">Hold</span>
							{:else if player.state === 'loaded'}
								<span class="state-pill ready">Ready</span>
							{/if}
						</div>

						{#if player.state === 'empty'}
							<div class="player-empty-drop">Drop clip here</div>
						{:else}
							<div
								class="mini-progress"
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
								<div class="mini-progress-fill" style="width: {(player.position ?? 0) * 100}%"></div>
							</div>

							<div class="player-toolbar">
								<div class="transport-icons">
									{#if player.state === 'playing'}
										<button type="button" class="transport-icon" onclick={() => handlePause(player.id)} title="Pause">
											{@html renderIcon(ICON_PAUSE, 13)}
										</button>
									{:else}
										<button type="button" class="transport-icon" onclick={() => handlePlay(player.id)} title="Play">
											{@html renderIcon(ICON_PLAY, 13, 'fill')}
										</button>
									{/if}
									<button
										type="button"
										class="transport-icon"
										onclick={() => handleStop(player.id)}
										title="Stop"
										disabled={player.state === 'loaded'}
									>
										{@html renderIcon(ICON_STOP, 13, 'fill')}
									</button>
									<button type="button" class="transport-icon" onclick={() => handleEject(player.id)} title="Eject">
										{@html renderIcon(ICON_EJECT, 13)}
									</button>
								</div>
								<select
									class="speed-select"
									bind:value={playerSpeeds[player.id]}
									disabled={player.state === 'playing'}
								>
									<option value={0.25}>0.25×</option>
									<option value={0.5}>0.5×</option>
									<option value={1.0}>1×</option>
									<option value={1.5}>1.5×</option>
									<option value={2.0}>2×</option>
								</select>
								<label class="loop-tick">
									<input
										type="checkbox"
										bind:checked={playerLoops[player.id]}
										disabled={player.state === 'playing'}
									/>
									<span>Loop</span>
								</label>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		</div>
	</div>
</div>

<ConfirmDialog
	open={pendingConfirm !== null}
	title={pendingConfirm?.title ?? ''}
	message={pendingConfirm?.message ?? ''}
	confirmLabel="Delete"
	onconfirm={confirmDelete}
	oncancel={() => { pendingDelete = null; }}
/>

<style>
	.clips-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		padding: 6px;
		font-family: var(--font-ui);
		box-sizing: border-box;
	}

	.clips-layout {
		display: flex;
		gap: 8px;
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}

	.clip-library {
		flex: 0 0 40%;
		min-width: 0;
		display: flex;
		flex-direction: column;
		min-height: 0;
		overflow: hidden;
	}

	.upload-zone {
		flex-shrink: 0;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 8px;
		padding: 8px 10px;
		border: 1px dashed var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast);
		position: relative;
	}

	.upload-zone:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	.upload-zone.drag-over {
		border-color: var(--accent-blue);
		background: var(--bg-elevated);
	}

	.upload-zone-icon {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.upload-zone.drag-over .upload-zone-icon {
		color: var(--accent-blue);
	}

	.upload-zone-text {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
		flex: 1;
	}

	.upload-zone-title {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-primary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.upload-zone-hint {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.file-input {
		display: none;
	}

	.upload-progress-inline {
		width: 100%;
		flex-basis: 100%;
		margin-top: 4px;
		padding-top: 8px;
		border-top: 1px solid var(--border-subtle);
	}

	.upload-progress-label {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 4px;
		font-size: var(--text-2xs);
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
		height: 3px;
		background: var(--bg-base);
		border-radius: var(--radius-xs);
		overflow: hidden;
		margin-bottom: 4px;
	}

	.upload-progress-fill {
		height: 100%;
		background: var(--accent-blue);
		border-radius: var(--radius-xs);
		transition: width var(--transition-normal);
	}

	.upload-stages {
		display: flex;
		gap: 6px;
		font-size: var(--text-2xs);
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
		opacity: 0.75;
		color: var(--color-success);
	}

	.library-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		display: flex;
		flex-direction: column;
		gap: 6px;
		margin-top: 6px;
	}

	.library-section {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		overflow: hidden;
	}

	.section-toggle {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 5px 8px;
		border: none;
		background: var(--bg-control);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		cursor: pointer;
		text-align: left;
		transition: background var(--transition-fast);
	}

	.section-toggle:hover {
		background: var(--bg-hover);
	}

	.section-chevron {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.section-toggle-label {
		flex: 1;
	}

	.section-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		background: var(--bg-base);
		padding: 1px 6px;
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-subtle);
	}

	.section-body {
		padding: 4px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.section-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.empty-hint {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		padding: 6px;
		text-align: center;
		font-style: italic;
	}

	.engine-group-label {
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-secondary);
		padding: 4px 6px 2px;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.clip-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 6px;
		padding: 4px 6px;
		border-radius: var(--radius-xs);
		border: 1px solid transparent;
	}

	.clip-item:hover {
		background: var(--bg-hover);
	}

	.clip-item[draggable='true'] {
		cursor: grab;
	}

	.clip-item[draggable='true']:active {
		cursor: grabbing;
	}

	.clip-drag-hint {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.clip-info {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
		flex: 1;
	}

	.clip-name {
		font-size: var(--text-xs);
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.clip-meta {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.clip-actions {
		display: flex;
		align-items: center;
		gap: 3px;
		flex-shrink: 0;
	}

	.load-btn {
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		background: var(--bg-base);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		cursor: pointer;
		line-height: 1.3;
	}

	.load-btn:hover {
		background: var(--bg-hover);
		border-color: var(--accent-blue);
		color: var(--accent-blue);
	}

	.download-btn {
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		background: var(--bg-base);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		cursor: pointer;
		line-height: 1.3;
		text-decoration: none;
	}

	.download-btn:hover {
		background: var(--bg-hover);
		border-color: var(--accent-green);
		color: var(--accent-green);
	}

	.icon-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 22px;
		height: 22px;
		padding: 0;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
	}

	.icon-btn:hover {
		background: var(--bg-hover);
	}

	.icon-btn.danger:hover {
		border-color: var(--color-error);
		color: var(--color-error);
	}

	.icon-btn.pin:hover {
		border-color: var(--accent-yellow);
		color: var(--accent-yellow);
	}

	.rec-state {
		margin-right: 6px;
		font-family: var(--font-mono);
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.rec-state-recording {
		color: var(--tally-program);
	}

	.rec-state-finalizing {
		color: var(--accent-orange);
	}

	.rec-state-failed {
		color: var(--tally-program);
	}

	.rec-error {
		display: block;
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		white-space: normal;
	}

	.import-btn {
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		background: var(--bg-base);
		color: var(--text-secondary);
		font-size: var(--text-2xs);
		cursor: pointer;
	}

	.import-btn:hover {
		border-color: var(--accent-orange);
		color: var(--accent-orange);
	}

	.players-pane {
		flex: 0 0 60%;
		min-width: 0;
		display: flex;
		flex-direction: column;
		min-height: 0;
		overflow: hidden;
	}

	.players-header {
		flex-shrink: 0;
		margin-bottom: 6px;
	}

	.players-title {
		margin: 0;
		padding: 0 2px;
		font-size: var(--text-xs);
		color: var(--text-secondary);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	.player-grid {
		flex: 1;
		min-height: 0;
		display: grid;
		grid-template-columns: 1fr 1fr;
		grid-template-rows: 1fr 1fr;
		gap: 6px;
		overflow: hidden;
	}

	.player-card {
		display: flex;
		flex-direction: column;
		gap: 6px;
		min-height: 0;
		padding: 6px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		transition: border-color var(--transition-fast), background var(--transition-fast);
	}

	.player-card.drag-over {
		border-color: var(--accent-blue);
		background: var(--bg-elevated);
		box-shadow: 0 0 0 1px var(--accent-blue);
	}

	.player-card.playing {
		border-color: var(--color-error);
	}

	.player-card.paused {
		border-color: var(--accent-yellow);
	}

	.player-card-top {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}

	.player-slot-id {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.player-clip-title {
		font-size: var(--text-xs);
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		flex: 1;
		min-width: 0;
	}

	.state-pill {
		flex-shrink: 0;
		font-size: var(--text-2xs);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		padding: 1px 5px;
		border-radius: var(--radius-xs);
	}

	.state-pill.playing {
		background: color-mix(in srgb, var(--color-error) 18%, transparent);
		color: var(--color-error);
	}

	.state-pill.paused {
		background: color-mix(in srgb, var(--accent-yellow) 20%, transparent);
		color: var(--accent-orange);
	}

	.state-pill.hold {
		background: color-mix(in srgb, var(--accent-blue) 18%, transparent);
		color: var(--accent-blue);
	}

	.state-pill.ready {
		background: color-mix(in srgb, var(--color-success) 18%, transparent);
		color: var(--color-success);
	}

	.player-empty-drop {
		flex: 1;
		min-height: 36px;
		display: flex;
		align-items: center;
		justify-content: center;
		border: 1px dashed var(--border-subtle);
		border-radius: var(--radius-xs);
		background: var(--bg-base);
		font-size: var(--text-2xs);
		font-style: italic;
		color: var(--text-tertiary);
		text-align: center;
		padding: 4px;
	}

	.player-card.drag-over .player-empty-drop {
		border-color: var(--accent-blue);
		background: var(--bg-hover);
	}

	.mini-progress {
		height: 4px;
		background: var(--bg-base);
		border-radius: var(--radius-xs);
		cursor: pointer;
		overflow: hidden;
		flex-shrink: 0;
	}

	.mini-progress-fill {
		height: 100%;
		background: var(--accent-blue);
		border-radius: var(--radius-xs);
		transition: width 0.1s linear;
	}

	.player-card.playing .mini-progress-fill {
		background: var(--color-error);
	}

	.player-toolbar {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
		margin-top: auto;
	}

	.transport-icons {
		display: flex;
		align-items: center;
		gap: 2px;
	}

	.transport-icon {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 26px;
		height: 26px;
		padding: 0;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		background: var(--bg-control);
		color: var(--text-primary);
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.transport-icon:hover:not(:disabled) {
		background: var(--bg-hover);
		border-color: var(--border-default);
	}

	.transport-icon:disabled {
		opacity: 0.35;
		cursor: default;
	}

	.speed-select {
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		padding: 2px 4px;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		margin-left: auto;
	}

	.speed-select:disabled {
		opacity: 0.5;
	}

	.loop-tick {
		display: flex;
		align-items: center;
		gap: 4px;
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		cursor: pointer;
		user-select: none;
	}
</style>
