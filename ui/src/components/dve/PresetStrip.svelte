<script lang="ts">
	import { holdReload } from '$lib/util/activity';
	import type { DVEPresetInfo } from '$lib/api/types';
	import {
		ICON_GRID, ICON_PLUS, ICON_SAVE, ICON_CLOSE, ICON_TRASH, ICON_CHECK,
		renderIcon
	} from '$lib/icons';

	interface Props {
		activePreset: string;
		userPresets: DVEPresetInfo[];
		onRecall: (id: string, durationMs: number) => void;
		onSave: (name: string) => void;
		onDelete: (id: string) => void;
		onClear: () => void;
	}

	let { activePreset, userPresets, onRecall, onSave, onDelete, onClear }: Props = $props();

	const builtins = [
		{ id: 'pip-top-right', label: 'PIP TR' },
		{ id: 'pip-top-left', label: 'PIP TL' },
		{ id: 'pip-bottom-right', label: 'PIP BR' },
		{ id: 'pip-bottom-left', label: 'PIP BL' },
		{ id: 'side-by-side', label: 'SBS' },
		{ id: 'quad', label: 'QUAD' },
		{ id: 'three-box', label: '3-BOX' },
		{ id: 'over-shoulder', label: 'O/S' },
	];

	const durationOptions = [
		{ label: 'Instant', value: 0 },
		{ label: '150ms', value: 150 },
		{ label: '300ms', value: 300 },
		{ label: '500ms', value: 500 },
		{ label: '1000ms', value: 1000 },
		{ label: '2000ms', value: 2000 },
	];

	let saving = $state(false);
	let presetName = $state('');
	$effect(() => { if (saving) return holdReload('unsaved preset'); });
	let deleteConfirmId = $state<string | null>(null);
	let transitionMs = $state(300);

	function handleRecall(id: string) {
		onRecall(id, transitionMs);
	}

	function handleSave() {
		const name = presetName.trim();
		if (!name) return;
		onSave(name);
		saving = false;
		presetName = '';
	}

	function cancelSave() {
		saving = false;
		presetName = '';
	}

	function confirmDelete(id: string) {
		onDelete(id);
		deleteConfirmId = null;
	}
</script>

<div class="preset-strip">
	<div class="preset-strip__label" title="DVE presets">
		<span class="preset-strip__icon" aria-hidden="true">{@html renderIcon(ICON_GRID, 13, 'fill')}</span>
		<span class="preset-strip__title">PRESETS</span>
	</div>

	<div class="preset-strip__scroll">
		<div class="preset-strip__inner">
			{#each builtins as preset (preset.id)}
				<button
					type="button"
					class="preset-btn preset-btn--builtin"
					class:active={activePreset === preset.id}
					onclick={() => handleRecall(preset.id)}
					title={preset.label}
				>
					{preset.label}
				</button>
			{/each}

			{#each userPresets as preset (preset.id)}
				<div class="user-preset-chip" class:active={activePreset === preset.id}>
					{#if deleteConfirmId === preset.id}
						<span class="user-preset-chip__confirm-label">Delete?</span>
						<button
							type="button"
							class="icon-btn icon-btn--danger"
							title="Confirm delete"
							onclick={() => confirmDelete(preset.id)}
						>
							<span aria-hidden="true">{@html renderIcon(ICON_CHECK, 12)}</span>
						</button>
						<button
							type="button"
							class="icon-btn"
							title="Cancel"
							onclick={() => { deleteConfirmId = null; }}
						>
							<span aria-hidden="true">{@html renderIcon(ICON_CLOSE, 12)}</span>
						</button>
					{:else}
						<button
							type="button"
							class="preset-btn preset-btn--user"
							class:active={activePreset === preset.id}
							onclick={() => handleRecall(preset.id)}
							title="User preset: {preset.name}"
						>
							{preset.name}
						</button>
						<button
							type="button"
							class="icon-btn icon-btn--ghost"
							title="Delete preset"
							onclick={(e) => { e.stopPropagation(); deleteConfirmId = preset.id; }}
						>
							<span aria-hidden="true">{@html renderIcon(ICON_TRASH, 11)}</span>
						</button>
					{/if}
				</div>
			{/each}
		</div>
	</div>

	<div class="preset-strip__actions">
		<select
			class="duration-select"
			bind:value={transitionMs}
			title="Transition duration"
		>
			{#each durationOptions as opt (opt.value)}
				<option value={opt.value}>{opt.label}</option>
			{/each}
		</select>

		{#if saving}
			<input
				class="save-input"
				type="text"
				placeholder="Name"
				bind:value={presetName}
				onkeydown={(e) => { if (e.key === 'Enter') handleSave(); if (e.key === 'Escape') cancelSave(); }}
			/>
			<button type="button" class="action-btn action-btn--primary" onclick={handleSave} title="Save preset">
				<span class="action-btn__ic" aria-hidden="true">{@html renderIcon(ICON_SAVE, 12, 'fill')}</span>
				<span>Save</span>
			</button>
			<button type="button" class="action-btn" onclick={cancelSave} title="Cancel">
				<span class="action-btn__ic" aria-hidden="true">{@html renderIcon(ICON_CLOSE, 12)}</span>
			</button>
		{:else}
			<button type="button" class="action-btn" onclick={() => { saving = true; }} title="Save current layout">
				<span class="action-btn__ic" aria-hidden="true">{@html renderIcon(ICON_PLUS, 12)}</span>
				<span>Save</span>
			</button>
		{/if}

		<button type="button" class="action-btn action-btn--clear" onclick={onClear} title="Clear layout">
			CLR
		</button>
	</div>
</div>

<style>
	.preset-strip {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 0 6px;
		border-bottom: 1px solid var(--border-subtle);
		min-height: 32px;
		max-height: 32px;
	}

	.preset-strip__label {
		display: flex;
		align-items: center;
		gap: 5px;
		flex-shrink: 0;
		color: var(--text-secondary);
	}

	.preset-strip__icon {
		display: flex;
		color: var(--text-tertiary);
	}

	.preset-strip__title {
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
	}

	.preset-strip__scroll {
		flex: 1;
		min-width: 0;
		overflow-x: auto;
		overflow-y: hidden;
		-webkit-overflow-scrolling: touch;
	}

	.preset-strip__scroll::-webkit-scrollbar {
		height: 3px;
	}

	.preset-strip__scroll::-webkit-scrollbar-thumb {
		background: var(--border-subtle);
		border-radius: 2px;
	}

	.preset-strip__inner {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: nowrap;
		width: max-content;
		padding: 1px 0;
	}

	.preset-btn {
		padding: 3px 7px;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast);
		white-space: nowrap;
	}

	.preset-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.preset-btn.active {
		background: var(--accent-blue-light);
		border-color: rgba(59, 130, 246, 0.5);
		color: var(--accent-blue);
	}

	.preset-btn--user {
		border-style: dashed;
		padding: 3px 6px;
	}

	.user-preset-chip {
		display: inline-flex;
		align-items: center;
		gap: 2px;
		padding: 1px 2px 1px 1px;
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		flex-shrink: 0;
	}

	.user-preset-chip.active {
		border-color: rgba(59, 130, 246, 0.45);
	}

	.user-preset-chip__confirm-label {
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		padding: 0 4px 0 6px;
		white-space: nowrap;
	}

	.preset-strip__actions {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-shrink: 0;
	}

	.duration-select {
		padding: 3px 4px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		background: var(--bg-control);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		cursor: pointer;
	}

	.duration-select:hover {
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.action-btn {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 3px 7px;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
	}

	.action-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.action-btn__ic {
		display: flex;
		color: currentColor;
	}

	.action-btn--primary {
		background: rgba(34, 197, 94, 0.15);
		color: var(--color-success);
		border-color: rgba(34, 197, 94, 0.35);
	}

	.action-btn--primary:hover {
		background: rgba(34, 197, 94, 0.22);
		color: var(--color-success);
	}

	.action-btn--clear {
		font-weight: 700;
		letter-spacing: 0.04em;
	}

	.action-btn--clear:hover {
		color: var(--color-error);
		border-color: rgba(239, 68, 68, 0.35);
	}

	.save-input {
		padding: 3px 6px;
		width: 88px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		outline: none;
	}

	.save-input:focus {
		border-color: var(--border-strong);
	}

	.icon-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 22px;
		height: 22px;
		padding: 0;
		border: 1px solid transparent;
		border-radius: var(--radius-xs);
		background: transparent;
		color: var(--text-tertiary);
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.icon-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.icon-btn--ghost {
		color: var(--text-tertiary);
	}

	.icon-btn--danger:hover {
		background: rgba(239, 68, 68, 0.15);
		color: var(--color-error);
		border-color: rgba(239, 68, 68, 0.25);
	}
</style>
