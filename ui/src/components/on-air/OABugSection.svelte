<script lang="ts">
	interface BugInfo {
		id: string;
		name: string;
	}

	interface Props {
		bugs: BugInfo[];
		activeBugId?: string;
		onSelectBug: (id: string) => void;
		onUploadBug: (files: FileList) => void;
	}

	let { bugs, activeBugId, onSelectBug, onUploadBug }: Props = $props();

	let fileInput: HTMLInputElement | undefined = $state();

	function handleUploadClick() {
		fileInput?.click();
	}

	function handleFileChange(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		if (input.files && input.files.length > 0) {
			onUploadBug(input.files);
			input.value = '';
		}
	}
</script>

<div class="bug-section">
	{#if bugs.length === 0}
		<div class="empty-state">
			<p class="empty-text">No bug loaded — upload a PNG sequence</p>
			<button class="upload-btn" onclick={handleUploadClick}>Upload</button>
		</div>
	{:else}
		<div class="bug-list">
			{#each bugs as bug (bug.id)}
				<label class="bug-row">
					<input
						type="radio"
						name="bug-select"
						class="bug-radio"
						checked={activeBugId === bug.id}
						onchange={() => onSelectBug(bug.id)}
					/>
					<span class="bug-name">{bug.name}</span>
					{#if activeBugId === bug.id}
						<span class="active-badge">Active</span>
					{/if}
				</label>
			{/each}
		</div>
		<div class="bug-actions">
			<button class="upload-btn" onclick={handleUploadClick}>Upload</button>
		</div>
	{/if}
	<input
		bind:this={fileInput}
		type="file"
		accept="image/png"
		multiple
		style="display: none"
		onchange={handleFileChange}
	/>
</div>

<style>
	.bug-section {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.empty-state {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
		padding: 8px 0;
	}

	.empty-text {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		margin: 0;
	}

	.bug-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.bug-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 0;
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
	}

	.bug-radio {
		appearance: none;
		width: 14px;
		height: 14px;
		border: 2px solid var(--border-strong);
		border-radius: 50%;
		cursor: pointer;
		flex-shrink: 0;
		position: relative;
	}

	.bug-radio:checked {
		border-color: var(--accent-green, #22c55e);
	}

	.bug-radio:checked::after {
		content: '';
		position: absolute;
		top: 2px;
		left: 2px;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--accent-green, #22c55e);
	}

	.bug-name {
		color: var(--text-primary);
		flex: 1;
	}

	.active-badge {
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.05em;
		color: var(--accent-green, #22c55e);
		background: rgba(34, 197, 94, 0.1);
		padding: 1px 6px;
		border-radius: var(--radius-sm);
	}

	.bug-actions {
		display: flex;
		gap: 6px;
	}

	.upload-btn {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-primary);
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 4px 10px;
		cursor: pointer;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast);
	}

	.upload-btn:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}
</style>
