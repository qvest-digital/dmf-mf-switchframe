<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { setColorGrade, uploadColorGradeLUT, deleteColorGradeLUT } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		state: ControlRoomState;
	}

	let { state }: Props = $props();

	let colorGrade = $derived(state.colorGrade);
	let activeLook = $derived(colorGrade?.lookName ?? '');
	let intensity = $state(100);
	let uploading = $state(false);
	let fileInput: HTMLInputElement;

	// Sync intensity from server state.
	$effect(() => {
		if (colorGrade) {
			intensity = Math.round(colorGrade.intensity * 100);
		}
	});

	const lookDescriptions: Record<string, string> = {
		'broadcast-news': 'Clean, slightly cool',
		'cinematic': 'Orange-teal split',
		'warm-natural': 'Golden warmth',
		'cool-drama': 'Desaturated blue',
		'vivid': 'Punchy saturated',
		'log-to-rec709': 'Log to broadcast',
	};

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

	function isBuiltin(name: string): boolean {
		return name in lookDescriptions;
	}
</script>

<div class="colorgrade-panel">
	<div class="section">
		<div class="section-header">
			<span class="section-title">Color Grade</span>
			{#if colorGrade?.enabled}
				<button class="btn-off" onclick={disable}>Off</button>
			{/if}
		</div>

		<div class="looks-grid">
			{#if colorGrade?.available}
				{#each colorGrade.available as look}
					<button
						class="look-btn"
						class:active={colorGrade?.enabled && activeLook === look}
						onclick={() => applyLook(look)}
						title={lookDescriptions[look] ?? 'Custom look'}
					>
						<span class="look-name">{look}</span>
						{#if colorGrade?.enabled && activeLook === look}
							<span class="checkmark">&#10003;</span>
						{/if}
					</button>
				{/each}
			{:else}
				<span class="no-looks">No looks available</span>
			{/if}
		</div>
	</div>

	{#if colorGrade?.enabled}
		<div class="section">
			<label class="intensity-label">
				<span>Intensity</span>
				<span class="intensity-value">{intensity}%</span>
			</label>
			<input
				type="range"
				min="0"
				max="100"
				step="1"
				bind:value={intensity}
				onchange={updateIntensity}
				class="intensity-slider"
			/>
		</div>
	{/if}

	<div class="section">
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
				<span class="uploading">Uploading...</span>
			{/if}
		</div>

		{#if colorGrade?.available}
			{@const custom = colorGrade.available.filter(n => !isBuiltin(n))}
			{#if custom.length > 0}
				<div class="custom-looks">
					{#each custom as name}
						<div class="custom-look-item">
							<span>{name}</span>
							<button class="btn-delete" onclick={() => handleDelete(name)} title="Delete">&#x2715;</button>
						</div>
					{/each}
				</div>
			{/if}
		{/if}
	</div>
</div>

<style>
	.colorgrade-panel {
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 8px;
		height: 100%;
		overflow-y: auto;
	}

	.section {
		display: flex;
		flex-direction: column;
		gap: 6px;
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

	.looks-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
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

	.look-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
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

	.uploading {
		font-size: 11px;
		color: var(--accent-yellow, #d4a017);
	}

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
