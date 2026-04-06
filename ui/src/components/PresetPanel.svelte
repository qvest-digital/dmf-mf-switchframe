<script lang="ts">
	import type { Preset } from '$lib/api/types';
	import { listPresets, createPreset, recallPresetByName, deletePresetByName } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import {
		ICON_TRASH,
		ICON_SAVE,
		ICON_PLUS,
		ICON_CHECK,
		ICON_CLOSE,
		renderIcon
	} from '$lib/icons';

	interface Props {
		onStateUpdate?: () => void;
	}

	let { onStateUpdate }: Props = $props();

	let presets = $state<Preset[]>([]);
	let saving = $state(false);
	let presetName = $state('');
	let deleteConfirmId = $state<string | null>(null);

	const iconSm = 12;
	const iconMd = 14;

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

	async function handleRecall(name: string) {
		try {
			await recallPresetByName(name);
			notify('info', `Preset "${name}" recalled`);
			onStateUpdate?.();
		} catch (err) {
			notify('error', `Recall failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	async function handleDelete(name: string) {
		try {
			await deletePresetByName(name);
			deleteConfirmId = null;
			await loadPresets();
			notify('info', 'Preset deleted');
			onStateUpdate?.();
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	function formatRelativeTime(iso: string): string {
		try {
			const d = new Date(iso);
			const now = Date.now();
			const diffMs = d.getTime() - now;
			const diffSec = Math.round(diffMs / 1000);
			const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
			const abs = Math.abs(diffSec);
			if (abs < 45) return rtf.format(diffSec, 'second');
			if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute');
			if (abs < 86400) return rtf.format(Math.round(diffSec / 3600), 'hour');
			if (abs < 604800) return rtf.format(Math.round(diffSec / 86400), 'day');
			if (abs < 2629800) return rtf.format(Math.round(diffSec / 604800), 'week');
			if (abs < 31557600) return rtf.format(Math.round(diffSec / 2629800), 'month');
			return rtf.format(Math.round(diffSec / 31557600), 'year');
		} catch {
			return iso;
		}
	}

	function cancelSave() {
		saving = false;
		presetName = '';
	}

	$effect(() => {
		loadPresets();
	});
</script>

<div class="preset-panel">
	<div class="preset-save-bar">
		<div class="preset-save-row">
			<span class="preset-label">Presets</span>
			{#if saving}
				<div class="save-inline">
					<input
						class="save-input"
						type="text"
						placeholder="Name…"
						bind:value={presetName}
						onkeydown={(e) => {
							if (e.key === 'Enter') handleSave();
							if (e.key === 'Escape') cancelSave();
						}}
					/>
					<button
						type="button"
						class="icon-action save-submit"
						onclick={handleSave}
						aria-label="Save preset"
						title="Save"
					>
						{@html renderIcon(ICON_SAVE, iconMd)}
					</button>
					<button
						type="button"
						class="icon-action save-cancel"
						onclick={cancelSave}
						aria-label="Cancel"
						title="Cancel"
					>
						{@html renderIcon(ICON_CLOSE, iconMd)}
					</button>
				</div>
			{:else}
				<button
					type="button"
					class="icon-action add-preset"
					onclick={() => {
						saving = true;
					}}
					aria-label="Save new preset"
					title="Save current state as preset"
				>
					{@html renderIcon(ICON_PLUS, iconMd)}
				</button>
			{/if}
		</div>
	</div>

	<div class="preset-scroll">
		{#if presets.length === 0 && !saving}
			<div class="empty-state">No presets saved</div>
		{:else}
			<div class="preset-grid">
				{#each presets as preset (preset.id)}
					<div class="preset-card">
						{#if deleteConfirmId === preset.id}
							<div class="delete-popover" role="dialog" aria-modal="true" aria-labelledby="del-{preset.id}">
								<span class="delete-popover-text" id="del-{preset.id}">Delete “{preset.name}”?</span>
								<div class="delete-popover-actions">
									<button
										type="button"
										class="icon-action delete-confirm"
										onclick={() => handleDelete(preset.name)}
										aria-label="Confirm delete"
										title="Delete"
									>
										{@html renderIcon(ICON_CHECK, iconMd)}
									</button>
									<button
										type="button"
										class="icon-action delete-dismiss"
										onclick={() => {
											deleteConfirmId = null;
										}}
										aria-label="Cancel delete"
										title="Cancel"
									>
										{@html renderIcon(ICON_CLOSE, iconMd)}
									</button>
								</div>
							</div>
						{:else}
							<button
								type="button"
								class="preset-recall"
								onclick={() => handleRecall(preset.name)}
								title="Recall: {preset.name}"
							>
								<span class="preset-name">{preset.name}</span>
								<span class="preset-time">{formatRelativeTime(preset.createdAt)}</span>
							</button>
							<button
								type="button"
								class="preset-trash icon-action"
								onclick={(e) => {
									e.stopPropagation();
									deleteConfirmId = preset.id;
								}}
								aria-label="Delete preset"
								title="Delete"
							>
								{@html renderIcon(ICON_TRASH, iconSm)}
							</button>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>

<style>
	.preset-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		padding: 4px 6px;
		gap: 4px;
	}

	.preset-save-bar {
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.preset-save-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 6px;
		min-height: 0;
	}

	.preset-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.save-inline {
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
		font-size: var(--text-xs);
		background: var(--bg-control);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		outline: none;
		transition: border-color var(--transition-fast);
	}

	.save-input:focus {
		border-color: var(--accent-blue);
	}

	.save-input::placeholder {
		color: var(--text-tertiary);
	}

	.icon-action {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		border: none;
		background: transparent;
		color: var(--text-secondary);
		cursor: pointer;
		border-radius: var(--radius-xs);
		line-height: 0;
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.icon-action:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.icon-action :global(svg) {
		display: block;
	}

	.add-preset {
		width: 26px;
		height: 26px;
		flex-shrink: 0;
		border: 1px solid var(--border-subtle);
		background: var(--bg-control);
		color: var(--text-secondary);
	}

	.add-preset:hover {
		border-color: var(--accent-blue);
		color: var(--accent-blue);
		background: var(--bg-hover);
	}

	.save-submit {
		width: 26px;
		height: 26px;
		flex-shrink: 0;
		border: 1px solid rgba(34, 197, 94, 0.35);
		background: color-mix(in srgb, var(--color-success) 12%, transparent);
		color: var(--color-success);
	}

	.save-submit:hover {
		background: color-mix(in srgb, var(--color-success) 22%, transparent);
	}

	.save-cancel {
		width: 26px;
		height: 26px;
		flex-shrink: 0;
		border: 1px solid var(--border-subtle);
		background: var(--bg-control);
	}

	.preset-scroll {
		flex: 1;
		min-height: 0;
		overflow-x: hidden;
		overflow-y: auto;
	}

	.preset-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
		gap: 4px;
		align-content: start;
	}

	.preset-card {
		position: relative;
		min-height: 52px;
		border-radius: var(--radius-sm);
		border: 1px solid var(--border-default);
		background: var(--bg-elevated);
		overflow: hidden;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast);
	}

	.preset-card:hover {
		border-color: var(--border-strong);
		background: var(--bg-panel);
	}

	.preset-recall {
		width: 100%;
		height: 100%;
		min-height: 52px;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		justify-content: center;
		gap: 2px;
		padding: 6px 28px 6px 8px;
		margin: 0;
		border: none;
		background: transparent;
		cursor: pointer;
		text-align: left;
		font: inherit;
		transition: background var(--transition-fast);
	}

	.preset-recall:hover {
		background: var(--bg-hover);
	}

	.preset-recall:focus-visible {
		outline: 2px solid var(--accent-blue);
		outline-offset: -2px;
	}

	.preset-name {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-primary);
		line-height: 1.2;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
		word-break: break-word;
	}

	.preset-time {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		line-height: 1.2;
	}

	.preset-trash {
		position: absolute;
		top: 3px;
		right: 3px;
		width: 24px;
		height: 24px;
		z-index: 1;
		color: var(--text-tertiary);
	}

	.preset-trash:hover {
		color: var(--color-error);
		background: color-mix(in srgb, var(--color-error) 10%, transparent);
	}

	.delete-popover {
		position: absolute;
		inset: 0;
		z-index: 2;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 6px;
		padding: 6px;
		background: color-mix(in srgb, var(--bg-panel) 92%, transparent);
		backdrop-filter: blur(4px);
		border-radius: inherit;
	}

	.delete-popover-text {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-primary);
		text-align: center;
		line-height: 1.25;
		display: -webkit-box;
		-webkit-line-clamp: 3;
		line-clamp: 3;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}

	.delete-popover-actions {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.delete-confirm {
		width: 28px;
		height: 28px;
		border: 1px solid rgba(239, 68, 68, 0.45);
		background: color-mix(in srgb, var(--color-error) 14%, transparent);
		color: var(--color-error);
	}

	.delete-confirm:hover {
		background: color-mix(in srgb, var(--color-error) 24%, transparent);
	}

	.delete-dismiss {
		width: 28px;
		height: 28px;
		border: 1px solid var(--border-subtle);
		background: var(--bg-control);
	}

	.empty-state {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 48px;
		padding: 8px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		text-align: center;
	}
</style>
