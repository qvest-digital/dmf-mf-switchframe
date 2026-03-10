<script lang="ts">
	import type { Preset } from '$lib/api/types';
	import { listPresets, createPreset, recallPreset, deletePreset, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		onStateUpdate?: () => void;
	}

	let { onStateUpdate }: Props = $props();

	let presets = $state<Preset[]>([]);
	let saving = $state(false);
	let presetName = $state('');
	let deleteConfirmId = $state<string | null>(null);

	async function loadPresets() {
		try {
			presets = await listPresets();
		} catch {
			// ignore — presets may not be configured
		}
	}

	async function handleSave() {
		const name = presetName.trim();
		if (!name) return;
		try {
			await createPreset(name);
			saving = false;
			presetName = '';
			await loadPresets();
			notify('info', `Preset "${name}" saved`);
			onStateUpdate?.();
		} catch (err) {
			notify('error', `Save failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	async function handleRecall(id: string, name: string) {
		try {
			await recallPreset(id);
			notify('info', `Preset "${name}" recalled`);
			onStateUpdate?.();
		} catch (err) {
			notify('error', `Recall failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	async function handleDelete(id: string) {
		try {
			await deletePreset(id);
			deleteConfirmId = null;
			await loadPresets();
			notify('info', 'Preset deleted');
			onStateUpdate?.();
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	function formatDate(iso: string): string {
		try {
			const d = new Date(iso);
			return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
		} catch {
			return iso;
		}
	}

	function cancelSave() {
		saving = false;
		presetName = '';
	}

	// Load presets on mount
	$effect(() => {
		loadPresets();
	});
</script>

<div class="preset-panel">
	<div class="preset-header">
		<span class="preset-title">PRESETS</span>
		{#if saving}
			<div class="save-form">
				<input
					class="save-input"
					type="text"
					placeholder="Preset name"
					bind:value={presetName}
					onkeydown={(e) => { if (e.key === 'Enter') handleSave(); if (e.key === 'Escape') cancelSave(); }}
				/>
				<button class="form-btn confirm-btn" onclick={handleSave} aria-label="Confirm">Save</button>
				<button class="form-btn cancel-btn" onclick={cancelSave} aria-label="Cancel">Cancel</button>
			</div>
		{:else}
			<button class="save-btn" onclick={() => { saving = true; }} aria-label="Save Preset">Save Preset</button>
		{/if}
	</div>

	<div class="preset-list">
		{#each presets as preset (preset.id)}
			<div class="preset-card">
				{#if deleteConfirmId === preset.id}
					<div class="delete-confirm">
						<span class="delete-text">Delete "{preset.name}"?</span>
						<button class="confirm-yes" onclick={() => handleDelete(preset.id)}>Yes</button>
						<button class="confirm-no" onclick={() => { deleteConfirmId = null; }}>No</button>
					</div>
				{:else}
					<button
						class="preset-recall"
						onclick={() => handleRecall(preset.id, preset.name)}
						title="Recall preset: {preset.name}"
					>
						<span class="preset-name">{preset.name}</span>
						<span class="preset-date">{formatDate(preset.createdAt)}</span>
					</button>
					<button
						class="delete-btn"
						onclick={(e) => { e.stopPropagation(); deleteConfirmId = preset.id; }}
						title="Delete preset"
					>X</button>
				{/if}
			</div>
		{/each}
	</div>

	{#if presets.length === 0 && !saving}
		<div class="empty-state">No presets saved</div>
	{/if}
</div>

<style>
	.preset-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 6px;
		height: 100%;
		overflow-y: auto;
	}

	.preset-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0 2px;
		gap: 6px;
	}

	.preset-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.save-btn {
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 500;
		cursor: pointer;
		padding: 3px 8px;
		border-radius: var(--radius-sm);
	}

	.save-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.save-form {
		display: flex;
		align-items: center;
		gap: 4px;
		flex: 1;
		min-width: 0;
	}

	.save-input {
		flex: 1;
		min-width: 0;
		padding: 3px 6px;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		outline: none;
	}

	.save-input:focus {
		border-color: var(--accent-blue);
	}

	.form-btn {
		padding: 3px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}

	.confirm-btn {
		background: rgba(34, 197, 94, 0.2);
		color: var(--color-success);
		border-color: rgba(34, 197, 94, 0.4);
	}

	.confirm-btn:hover {
		background: rgba(34, 197, 94, 0.3);
	}

	.cancel-btn {
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}

	.cancel-btn:hover {
		background: var(--bg-hover);
	}

	.preset-list {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.preset-card {
		display: flex;
		align-items: stretch;
		gap: 2px;
	}

	.preset-recall {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 6px 8px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		cursor: pointer;
		text-align: left;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast);
	}

	.preset-recall:hover {
		background: var(--bg-hover);
		border-color: var(--accent-blue);
	}

	.preset-name {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-primary);
	}

	.preset-date {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.delete-btn {
		padding: 2px 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		align-self: center;
	}

	.delete-btn:hover {
		color: var(--color-error);
		background: var(--bg-hover);
	}

	.delete-confirm {
		flex: 1;
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 8px;
		background: var(--bg-elevated);
		border: 1px solid rgba(239, 68, 68, 0.4);
		border-radius: var(--radius-sm);
	}

	.delete-text {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-primary);
	}

	.confirm-yes,
	.confirm-no {
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
	}

	.confirm-yes {
		background: rgba(239, 68, 68, 0.2);
		color: var(--color-error);
		border-color: rgba(239, 68, 68, 0.4);
	}

	.confirm-yes:hover {
		background: rgba(239, 68, 68, 0.3);
	}

	.confirm-no {
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}

	.confirm-no:hover {
		background: var(--bg-hover);
	}

	.empty-state {
		text-align: center;
		color: var(--text-tertiary);
		font-size: var(--text-sm);
		padding: 12px 4px;
	}
</style>
