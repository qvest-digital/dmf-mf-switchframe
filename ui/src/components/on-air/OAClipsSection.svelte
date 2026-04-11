<script lang="ts">
	interface ClipItem {
		id: string;
		name: string;
		durationMs?: number;
	}

	interface Props {
		clips: ClipItem[];
		onUploadClip: (files: FileList) => void;
		onMakeSlate: (clipId: string, clipName: string) => void;
		onDeleteClip: (clipId: string) => void;
		uploadProgress?: { filename: string; percent: number } | null;
	}

	let { clips, onUploadClip, onMakeSlate, onDeleteClip, uploadProgress }: Props = $props();

	let fileInput: HTMLInputElement | undefined = $state();

	function formatDuration(ms: number | undefined): string {
		if (ms == null) return '--:--';
		const totalSeconds = Math.floor(ms / 1000);
		const minutes = Math.floor(totalSeconds / 60);
		const seconds = totalSeconds % 60;
		return `${minutes}:${seconds.toString().padStart(2, '0')}`;
	}

	function handleZoneClick() {
		fileInput?.click();
	}

	function handleFileChange(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		if (input.files && input.files.length > 0) {
			onUploadClip(input.files);
			input.value = '';
		}
	}
</script>

<div class="clips-section">
	<!-- Upload zone -->
	<button class="upload-zone" onclick={handleZoneClick} type="button">
		<span class="upload-text">Drop clips here or click to upload</span>
	</button>
	<input
		bind:this={fileInput}
		type="file"
		accept="video/*,.ts,.mp4,.mov"
		multiple
		style="display: none"
		onchange={handleFileChange}
	/>

	<!-- Upload progress -->
	{#if uploadProgress}
		<div class="progress-bar-container">
			<div class="progress-info">
				<span class="progress-filename">{uploadProgress.filename}</span>
				<span class="progress-percent">{uploadProgress.percent}%</span>
			</div>
			<div class="progress-track">
				<div class="progress-fill" style="width: {uploadProgress.percent}%"></div>
			</div>
		</div>
	{/if}

	<!-- Clip list -->
	{#if clips.length > 0}
		<div class="clip-list">
			{#each clips as clip (clip.id)}
				<div class="clip-row">
					<span class="clip-name">{clip.name}</span>
					<span class="clip-duration">{formatDuration(clip.durationMs)}</span>
					<button
						class="clip-action slate-action"
						onclick={() => onMakeSlate(clip.id, clip.name)}
						title="Add to slates"
					>
						Slate
					</button>
					<button
						class="clip-action delete-action"
						onclick={() => onDeleteClip(clip.id)}
						title="Delete clip"
					>
						&times;
					</button>
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.clips-section {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.upload-zone {
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 12px;
		border: 1px dashed var(--border-strong);
		border-radius: var(--radius-sm);
		background: transparent;
		cursor: pointer;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast);
	}

	.upload-zone:hover {
		background: var(--bg-elevated);
		border-color: var(--text-secondary);
	}

	.upload-text {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-secondary);
	}

	/* ── Progress bar ── */
	.progress-bar-container {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.progress-info {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.progress-filename {
		font-family: var(--font-ui);
		font-size: 11px;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		flex: 1;
	}

	.progress-percent {
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 600;
		color: var(--text-primary);
		margin-left: 8px;
	}

	.progress-track {
		height: 3px;
		background: var(--bg-elevated);
		border-radius: 2px;
		overflow: hidden;
	}

	.progress-fill {
		height: 100%;
		background: var(--accent-green, #22c55e);
		border-radius: 2px;
		transition: width 0.2s ease;
	}

	/* ── Clip list ── */
	.clip-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.clip-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 0;
	}

	.clip-name {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.clip-duration {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.clip-action {
		background: none;
		border: none;
		cursor: pointer;
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: 11px;
		transition: color var(--transition-fast);
	}

	.slate-action {
		color: var(--accent-yellow, #eab308);
		font-weight: 600;
	}

	.slate-action:hover {
		color: var(--text-primary);
	}

	.delete-action {
		color: var(--text-secondary);
		font-size: 14px;
		line-height: 1;
	}

	.delete-action:hover {
		color: var(--tally-program);
	}
</style>
