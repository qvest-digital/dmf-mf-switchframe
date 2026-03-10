<script lang="ts">
	import { onMount } from 'svelte';
	import type { ControlRoomState, LayoutSlotState } from '$lib/api/types';
	import {
		setLayout, clearLayout, layoutSlotOn, layoutSlotOff,
		setLayoutSlotSource, updateLayoutSlot,
		listLayoutPresets, saveLayoutPreset, deleteLayoutPreset,
		apiCall
	} from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	let presets = $state<string[]>([]);
	let saving = $state(false);
	let presetName = $state('');
	let deleteConfirmName = $state<string | null>(null);

	let layoutState = $derived(crState.layout);
	let slots = $derived(layoutState?.slots ?? []);
	let sourceKeys = $derived(Object.keys(crState.sources).sort());
	let activePreset = $derived(layoutState?.activePreset ?? '');

	// Built-in presets
	const builtinPresets = [
		{ id: 'pip-top-right', label: 'PIP TR', desc: 'Picture-in-Picture (top right)' },
		{ id: 'pip-top-left', label: 'PIP TL', desc: 'Picture-in-Picture (top left)' },
		{ id: 'pip-bottom-right', label: 'PIP BR', desc: 'Picture-in-Picture (bottom right)' },
		{ id: 'pip-bottom-left', label: 'PIP BL', desc: 'Picture-in-Picture (bottom left)' },
		{ id: 'side-by-side', label: 'SBS', desc: 'Side-by-side split' },
		{ id: 'quad', label: 'QUAD', desc: '2x2 quad view' },
	];

	function tallyClass(sourceKey: string): string {
		if (crState.programSource === sourceKey) return 'tally-program';
		if (crState.previewSource === sourceKey) return 'tally-preview';
		const inPip = slots.some(s => s.enabled && s.sourceKey === sourceKey);
		if (inPip) return 'tally-pip';
		return '';
	}

	async function loadPresets() {
		try {
			presets = await listLayoutPresets();
		} catch {
			// presets may not be configured
		}
	}

	async function applyBuiltinPreset(id: string) {
		apiCall(setLayout({ preset: id }), 'Layout preset');
	}

	async function applyUserPreset(name: string) {
		apiCall(setLayout({ preset: name }), 'Layout preset');
	}

	async function handleClear() {
		apiCall(clearLayout(), 'Clear layout');
	}

	function toggleSlot(slot: LayoutSlotState) {
		if (slot.enabled) {
			apiCall(layoutSlotOff(slot.id), 'Slot off');
		} else {
			apiCall(layoutSlotOn(slot.id), 'Slot on');
		}
	}

	function handleSourceChange(slotId: number, source: string) {
		apiCall(setLayoutSlotSource(slotId, source), 'Set source');
	}

	function handleTransitionChange(slotId: number, type: string) {
		apiCall(
			updateLayoutSlot(slotId, { transition: { type, durationMs: 300 } }),
			'Set transition'
		);
	}

	async function handleSave() {
		const name = presetName.trim();
		if (!name) return;
		try {
			await saveLayoutPreset(name);
			saving = false;
			presetName = '';
			await loadPresets();
			notify('info', `Layout preset "${name}" saved`);
		} catch (err) {
			notify('error', `Save failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	async function handleDelete(name: string) {
		try {
			await deleteLayoutPreset(name);
			deleteConfirmName = null;
			await loadPresets();
			notify('info', 'Layout preset deleted');
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	function cancelSave() {
		saving = false;
		presetName = '';
	}

	onMount(() => {
		loadPresets();
	});
</script>

<div class="layout-panel">
	<!-- Preset strip -->
	<div class="preset-strip">
		<span class="section-title">LAYOUTS</span>
		<div class="preset-buttons">
			{#each builtinPresets as preset}
				<button
					class="preset-btn"
					class:active={activePreset === preset.id}
					onclick={() => applyBuiltinPreset(preset.id)}
					title={preset.desc}
				>
					{preset.label}
				</button>
			{/each}
			{#each presets as name}
				<button
					class="preset-btn user-preset"
					class:active={activePreset === name}
					onclick={() => applyUserPreset(name)}
					title="User preset: {name}"
				>
					{name}
				</button>
			{/each}
		</div>
		<div class="preset-actions">
			{#if saving}
				<input
					class="save-input"
					type="text"
					placeholder="Name"
					bind:value={presetName}
					onkeydown={(e) => { if (e.key === 'Enter') handleSave(); if (e.key === 'Escape') cancelSave(); }}
				/>
				<button class="action-btn save-confirm" onclick={handleSave}>Save</button>
				<button class="action-btn" onclick={cancelSave}>Cancel</button>
			{:else}
				<button class="action-btn" onclick={() => { saving = true; }}>Save</button>
			{/if}
			<button class="action-btn clear-btn" onclick={handleClear} title="Clear layout">Clear</button>
		</div>
	</div>

	<!-- Slot controls -->
	{#if slots.length > 0}
		<div class="slot-grid">
			{#each slots as slot (slot.id)}
				<div class="slot-card" class:slot-enabled={slot.enabled} class:slot-animating={slot.animating}>
					<div class="slot-header">
						<span class="slot-id">Slot {slot.id}</span>
						<button
							class="slot-toggle"
							class:on={slot.enabled}
							onclick={() => toggleSlot(slot)}
						>
							{slot.enabled ? 'ON' : 'OFF'}
						</button>
					</div>

					<div class="slot-source">
						<select
							class="source-select"
							value={slot.sourceKey}
							onchange={(e) => handleSourceChange(slot.id, (e.target as HTMLSelectElement).value)}
						>
							<option value="">— none —</option>
							{#each sourceKeys as key}
								<option value={key} class={tallyClass(key)}>
									{crState.sources[key]?.label || key}
								</option>
							{/each}
						</select>
						<span class="tally-dot {tallyClass(slot.sourceKey)}"></span>
					</div>

					<div class="slot-position">
						<span class="pos-label">{slot.x},{slot.y} {slot.width}x{slot.height}</span>
						<span class="z-label">z:{slot.zOrder}</span>
					</div>

					<div class="slot-transition">
						<select
							class="transition-select"
							onchange={(e) => handleTransitionChange(slot.id, (e.target as HTMLSelectElement).value)}
						>
							<option value="cut">Cut</option>
							<option value="dissolve">Dissolve</option>
							<option value="fly">Fly</option>
						</select>
					</div>
				</div>
			{/each}
		</div>
	{:else}
		<div class="empty-state">No active layout. Select a preset above.</div>
	{/if}

	<!-- User preset management -->
	{#if presets.length > 0}
		<div class="user-presets">
			<span class="section-title">SAVED PRESETS</span>
			<div class="preset-list">
				{#each presets as name}
					<div class="preset-item">
						{#if deleteConfirmName === name}
							<span class="delete-text">Delete "{name}"?</span>
							<button class="confirm-yes" onclick={() => handleDelete(name)}>Yes</button>
							<button class="confirm-no" onclick={() => { deleteConfirmName = null; }}>No</button>
						{:else}
							<span class="preset-item-name">{name}</span>
							<button class="delete-btn" onclick={() => { deleteConfirmName = name; }}>X</button>
						{/if}
					</div>
				{/each}
			</div>
		</div>
	{/if}
</div>

<style>
	.layout-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 4px;
		height: 100%;
		overflow-y: auto;
	}

	.section-title {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 600;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.preset-strip {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 2px 4px;
		flex-wrap: wrap;
	}

	.preset-buttons {
		display: flex;
		gap: 3px;
		flex-wrap: wrap;
	}

	.preset-btn {
		padding: 3px 8px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.preset-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.preset-btn.active {
		background: rgba(59, 130, 246, 0.2);
		border-color: rgba(59, 130, 246, 0.5);
		color: var(--accent-blue);
	}

	.preset-btn.user-preset {
		border-style: dashed;
	}

	.preset-actions {
		display: flex;
		gap: 3px;
		align-items: center;
		margin-left: auto;
	}

	.action-btn {
		padding: 3px 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: 0.6rem;
		font-weight: 600;
		cursor: pointer;
	}

	.action-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.save-confirm {
		background: rgba(34, 197, 94, 0.2);
		color: #22c55e;
		border-color: rgba(34, 197, 94, 0.4);
	}

	.clear-btn:hover {
		color: #ef4444;
	}

	.save-input {
		padding: 3px 6px;
		font-family: var(--font-ui);
		font-size: 0.65rem;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		outline: none;
		width: 90px;
	}

	.save-input:focus {
		border-color: var(--accent-blue);
	}

	/* Slot grid */
	.slot-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
		gap: 4px;
		padding: 0 4px;
	}

	.slot-card {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 6px 8px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		transition: border-color var(--transition-fast);
	}

	.slot-card.slot-enabled {
		border-color: rgba(212, 160, 23, 0.5);
	}

	.slot-card.slot-animating {
		border-color: rgba(59, 130, 246, 0.5);
	}

	.slot-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.slot-id {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 600;
		color: var(--text-secondary);
		letter-spacing: 0.04em;
	}

	.slot-toggle {
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 0.6rem;
		font-weight: 700;
		cursor: pointer;
		background: var(--bg-base);
		color: var(--text-tertiary);
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.slot-toggle.on {
		background: rgba(212, 160, 23, 0.25);
		color: #d4a017;
		border-color: rgba(212, 160, 23, 0.5);
	}

	.slot-source {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.source-select {
		flex: 1;
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: 0.65rem;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.tally-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
		background: transparent;
	}

	.tally-dot.tally-program {
		background: var(--tally-program);
	}

	.tally-dot.tally-preview {
		background: var(--tally-preview);
	}

	.tally-dot.tally-pip {
		background: #d4a017;
	}

	.slot-position {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.pos-label, .z-label {
		font-family: var(--font-mono);
		font-size: 0.55rem;
		color: var(--text-tertiary);
	}

	.slot-transition {
		display: flex;
		gap: 4px;
	}

	.transition-select {
		flex: 1;
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: 0.6rem;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.empty-state {
		text-align: center;
		color: var(--text-tertiary);
		font-size: 0.7rem;
		padding: 16px 4px;
	}

	/* User preset management */
	.user-presets {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 0 4px;
		border-top: 1px solid var(--border-subtle);
		padding-top: 6px;
	}

	.preset-list {
		display: flex;
		flex-wrap: wrap;
		gap: 3px;
	}

	.preset-item {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 3px 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
	}

	.preset-item-name {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		color: var(--text-primary);
	}

	.delete-btn {
		background: transparent;
		border: none;
		color: var(--text-tertiary);
		font-size: 0.55rem;
		font-weight: 700;
		cursor: pointer;
		padding: 0 2px;
	}

	.delete-btn:hover {
		color: #ef4444;
	}

	.delete-text {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		color: var(--text-primary);
	}

	.confirm-yes, .confirm-no {
		padding: 1px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 0.6rem;
		font-weight: 600;
		cursor: pointer;
	}

	.confirm-yes {
		background: rgba(239, 68, 68, 0.2);
		color: #ef4444;
		border-color: rgba(239, 68, 68, 0.4);
	}

	.confirm-no {
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}
</style>
