<script lang="ts">
	import type { ControlRoomState, LookMetadata } from '$lib/api/types';
	import { setColorGrade, uploadColorGradeLUT, deleteColorGradeLUT } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	let { state: crState }: { state: ControlRoomState } = $props();

	let colorGrade = $derived(crState.colorGrade);
	let activeLook = $derived(colorGrade?.lookName ?? '');
	let intensity = $state(100);
	let selectedGroup = $state<string>('All');
	let uploading = $state(false);
	let fileInput: HTMLInputElement;

	// Get unique groups from available looks, preserving a stable order
	let groups = $derived.by(() => {
		if (!colorGrade?.available) return ['All'];
		const seen = new Set<string>();
		for (const look of colorGrade.available) {
			seen.add(look.group);
		}
		return ['All', ...Array.from(seen)];
	});

	// Filter looks by selected group
	let filteredLooks = $derived.by(() => {
		if (!colorGrade?.available) return [];
		if (selectedGroup === 'All') return colorGrade.available;
		return colorGrade.available.filter(l => l.group === selectedGroup);
	});

	// Custom (non-builtin) looks for the upload section
	let customLooks = $derived.by(() => {
		if (!colorGrade?.available) return [];
		return colorGrade.available.filter(l => !l.builtin);
	});

	// Guard against server state overwriting in-progress user edits.
	let lastUserEdit = 0;
	const SYNC_GUARD_MS = 1000;

	// Sync intensity from server state (skipped while user is editing)
	$effect(() => {
		if (colorGrade) {
			if (Date.now() - lastUserEdit < SYNC_GUARD_MS) return;
			intensity = Math.round(colorGrade.intensity * 100);
		}
	});

	function formatLookName(name: string): string {
		return name
			.replace(/-/g, ' ')
			.replace(/\b\w/g, c => c.toUpperCase());
	}

	async function applyLook(name: string) {
		try {
			await setColorGrade({ lookName: name, intensity: intensity / 100 });
		} catch (err) {
			notify('error', `Apply failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function disable() {
		try {
			await setColorGrade({ enabled: false });
		} catch (err) {
			notify('error', `Disable failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function updateIntensity() {
		lastUserEdit = Date.now();
		if (!colorGrade?.enabled || !activeLook) return;
		try {
			await setColorGrade({ lookName: activeLook, intensity: intensity / 100 });
		} catch (err) {
			notify('error', `Update failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function handleUpload() {
		const file = fileInput?.files?.[0];
		if (!file) return;

		const name = file.name.replace(/\.cube$/i, '').replace(/[^a-zA-Z0-9_-]/g, '-');
		if (!name) {
			notify('error', 'Invalid file name');
			return;
		}

		uploading = true;
		try {
			await uploadColorGradeLUT(name, file);
			notify('info', `Uploaded "${name}"`);
		} catch (err) {
			notify('error', `Upload failed: ${err instanceof Error ? err.message : 'unknown'}`);
		} finally {
			uploading = false;
			if (fileInput) fileInput.value = '';
		}
	}

	async function handleDelete(name: string) {
		try {
			await deleteColorGradeLUT(name);
			notify('info', `Deleted "${name}"`);
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}
</script>

<div class="looks-tab">
	<!-- Header with Off button -->
	<div class="section-header">
		<span class="section-title">Looks</span>
		{#if colorGrade?.enabled}
			<button class="btn-off" onclick={disable}>Off</button>
		{/if}
	</div>

	<!-- Group selector bar -->
	<div class="group-bar">
		{#each groups as group}
			<button
				class="group-btn"
				class:active={selectedGroup === group}
				onclick={() => selectedGroup = group}
			>{group}</button>
		{/each}
	</div>

	<!-- Preset grid -->
	<div class="looks-grid">
		{#if filteredLooks.length > 0}
			{#each filteredLooks as look}
				<button
					class="look-btn"
					class:active={colorGrade?.enabled && activeLook === look.name}
					onclick={() => applyLook(look.name)}
				>
					<div class="look-info">
						<span class="look-name">{formatLookName(look.name)}</span>
						{#if look.description}
							<span class="look-desc">{look.description}</span>
						{/if}
					</div>
					{#if colorGrade?.enabled && activeLook === look.name}
						<span class="checkmark">&#10003;</span>
					{/if}
				</button>
			{/each}
		{:else}
			<span class="no-looks">No looks available</span>
		{/if}
	</div>

	<!-- Intensity slider (shown when a look is active) -->
	{#if colorGrade?.enabled}
		<div class="intensity-section">
			<label class="intensity-label">
				<span>Intensity</span>
				<span class="intensity-value">{intensity}%</span>
				<input
					type="range"
					min="0"
					max="100"
					step="1"
					bind:value={intensity}
					onchange={updateIntensity}
					class="intensity-slider"
				/>
			</label>
		</div>
	{/if}

	<!-- Upload section -->
	<div class="upload-section">
		<div class="section-header">
			<span class="section-title">Upload</span>
		</div>
		<div class="upload-row">
			<input
				type="file"
				accept=".cube"
				bind:this={fileInput}
				onchange={handleUpload}
				class="file-input"
				disabled={uploading}
			/>
			{#if uploading}
				<span class="uploading-label">Uploading...</span>
			{/if}
		</div>

		<!-- Custom looks list -->
		{#if customLooks.length > 0}
			<div class="custom-looks">
				{#each customLooks as look}
					<div class="custom-look-item">
						<span>{formatLookName(look.name)}</span>
						<button class="btn-delete" onclick={() => handleDelete(look.name)} title="Delete">&#x2715;</button>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>

<style>
	.looks-tab {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding: 8px;
		height: 100%;
		overflow-y: auto;
	}

	.section-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.section-title {
		font-size: 11px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-secondary, #999);
	}

	/* Group selector bar */
	.group-bar {
		display: flex;
		gap: 2px;
		overflow-x: auto;
		flex-shrink: 0;
		scrollbar-width: none;
	}

	.group-bar::-webkit-scrollbar {
		display: none;
	}

	.group-btn {
		padding: 3px 8px;
		background: var(--surface-secondary, #2a2a2a);
		border: 1px solid var(--border-color, #444);
		border-radius: 3px;
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-size: 11px;
		white-space: nowrap;
		transition: background 0.15s, border-color 0.15s, color 0.15s;
		flex-shrink: 0;
	}

	.group-btn:hover {
		background: var(--surface-hover, #333);
		color: var(--text-primary, #eee);
	}

	.group-btn.active {
		background: var(--accent-yellow-bg, #3a3520);
		border-color: var(--accent-yellow, #d4a017);
		color: var(--accent-yellow, #d4a017);
	}

	/* Preset grid */
	.looks-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
		gap: 4px;
	}

	.look-btn {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 6px 10px;
		background: var(--surface-secondary, #2a2a2a);
		border: 1px solid var(--border-color, #444);
		border-radius: 4px;
		color: var(--text-primary, #eee);
		cursor: pointer;
		font-size: 12px;
		text-align: left;
		transition: background 0.15s, border-color 0.15s;
	}

	.look-btn:hover {
		background: var(--surface-hover, #333);
		border-color: var(--border-hover, #666);
	}

	.look-btn.active {
		background: var(--accent-yellow-bg, #3a3520);
		border-color: var(--accent-yellow, #d4a017);
		color: var(--accent-yellow, #d4a017);
	}

	.look-info {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
		overflow: hidden;
	}

	.look-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 12px;
	}

	.look-desc {
		font-size: 10px;
		color: var(--text-secondary, #999);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.look-btn.active .look-desc {
		color: var(--accent-yellow, #d4a017);
		opacity: 0.7;
	}

	.checkmark {
		color: var(--accent-yellow, #d4a017);
		font-size: 14px;
		margin-left: 4px;
		flex-shrink: 0;
	}

	.no-looks {
		color: var(--text-secondary, #999);
		font-size: 12px;
		font-style: italic;
	}

	/* Intensity slider */
	.intensity-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.intensity-label {
		display: flex;
		justify-content: space-between;
		font-size: 11px;
		color: var(--text-secondary, #999);
	}

	.intensity-value {
		color: var(--text-primary, #eee);
		font-variant-numeric: tabular-nums;
	}

	.intensity-slider {
		width: 100%;
		accent-color: var(--accent-yellow, #d4a017);
	}

	/* Off button */
	.btn-off {
		padding: 2px 10px;
		background: var(--surface-secondary, #2a2a2a);
		border: 1px solid var(--border-color, #444);
		border-radius: 3px;
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-size: 11px;
	}

	.btn-off:hover {
		background: var(--surface-hover, #333);
		color: var(--text-primary, #eee);
	}

	/* Upload section */
	.upload-section {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.upload-row {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.file-input {
		font-size: 11px;
		color: var(--text-secondary, #999);
		max-width: 200px;
	}

	.file-input::file-selector-button {
		padding: 3px 8px;
		background: var(--surface-secondary, #2a2a2a);
		border: 1px solid var(--border-color, #444);
		border-radius: 3px;
		color: var(--text-primary, #eee);
		cursor: pointer;
		font-size: 11px;
		margin-right: 8px;
	}

	.uploading-label {
		font-size: 11px;
		color: var(--accent-yellow, #d4a017);
	}

	/* Custom looks list */
	.custom-looks {
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin-top: 4px;
	}

	.custom-look-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 3px 8px;
		font-size: 12px;
		color: var(--text-secondary, #999);
		background: var(--surface-secondary, #2a2a2a);
		border-radius: 3px;
	}

	.btn-delete {
		background: none;
		border: none;
		color: var(--text-secondary, #666);
		cursor: pointer;
		font-size: 12px;
		padding: 0 2px;
	}

	.btn-delete:hover {
		color: var(--error, #e74c3c);
	}
</style>
