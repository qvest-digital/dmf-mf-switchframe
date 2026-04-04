<script lang="ts">
	import type { ControlRoomState, DVESlotState, DVEPresetInfo } from '$lib/api/types';
	import {
		getDVELayout, setDVELayout, updateDVESlot,
		dveSlotOn, dveSlotOff,
		listDVEPresets, saveDVEPreset, recallDVEPreset, deleteDVEPreset,
		apiCall
	} from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	let presets = $state<DVEPresetInfo[]>([]);
	let presetName = $state('');
	let selectedSlotId = $state<number | null>(null);
	let deleteConfirmId = $state('');

	let layoutState = $derived(crState.layout);
	let slots = $derived(layoutState?.slots ?? []);
	let sourceKeys = $derived(Object.keys(crState.sources).sort());

	let selectedSlot = $derived(
		selectedSlotId == null ? null : slots.find(s => s.id === selectedSlotId) ?? null
	);
	let dve = $derived(selectedSlot?.dve);

	$effect(() => {
		listDVEPresets().then(p => { presets = p; }).catch(() => {});
	});

	function handleSavePreset() {
		if (!presetName.trim()) return;
		apiCall(
			saveDVEPreset(presetName.trim()).then(() => listDVEPresets()).then(p => { presets = p; presetName = ''; }),
			'Save DVE preset'
		);
	}

	function handleRecallPreset(id: string) {
		apiCall(recallDVEPreset(id, 500), 'Recall DVE preset');
	}

	function handleDeletePreset(id: string) {
		apiCall(
			deleteDVEPreset(id).then(() => listDVEPresets()).then(p => { presets = p; }),
			'Delete DVE preset'
		);
		deleteConfirmId = '';
	}

	function updateSlotDVE(field: string, value: unknown) {
		if (selectedSlotId == null) return;
		apiCall(updateDVESlot(selectedSlotId, { [field]: value }), 'Update DVE');
	}

	function updateSlotTransform(field: string, value: unknown) {
		if (selectedSlotId == null || !dve) return;
		// For nested transform fields, send as flat update (the server unflattens).
		apiCall(updateDVESlot(selectedSlotId, { [field]: value }), 'Update DVE');
	}
</script>

<div class="dve-editor">
	<!-- Preset strip -->
	<div class="preset-strip">
		{#each presets as preset}
			<div class="preset-item">
				{#if deleteConfirmId === preset.id}
					<span class="preset-del-label">Del?</span>
					<button class="preset-del-yes" onclick={() => handleDeletePreset(preset.id)}>Y</button>
					<button class="preset-del-no" onclick={() => deleteConfirmId = ''}>N</button>
				{:else}
					<button
						class="preset-btn"
						onclick={() => handleRecallPreset(preset.id)}
						title={preset.name}
					>{preset.name}</button>
					{#if !preset.builtin}
						<button class="preset-x" onclick={() => deleteConfirmId = preset.id}>x</button>
					{/if}
				{/if}
			</div>
		{/each}
		<div class="preset-save">
			<input
				class="preset-name-input"
				type="text"
				placeholder="Name..."
				bind:value={presetName}
				onkeydown={(e) => { if (e.key === 'Enter') handleSavePreset(); }}
			/>
			<button class="preset-save-btn" onclick={handleSavePreset} disabled={!presetName.trim()}>Save</button>
		</div>
	</div>

	<!-- Slot list -->
	<div class="slot-list">
		{#each slots as slot (slot.id)}
			<button
				class="slot-chip"
				class:selected={selectedSlotId === slot.id}
				class:enabled={slot.enabled}
				onclick={() => selectedSlotId = slot.id}
			>
				<span class="slot-id">S{slot.id}</span>
				<span class="slot-src">{slot.sourceKey || '---'}</span>
			</button>
		{/each}
	</div>

	<!-- Selected slot detail -->
	{#if selectedSlot && dve}
		<div class="slot-detail">
			<div class="detail-header">
				<span class="detail-title">Slot {selectedSlot.id}</span>
				{#if selectedSlot.enabled}
					<button class="detail-toggle off" onclick={() => apiCall(dveSlotOff(selectedSlot.id), 'DVE off')}>OFF</button>
				{:else}
					<button class="detail-toggle on" onclick={() => apiCall(dveSlotOn(selectedSlot.id), 'DVE on')}>ON</button>
				{/if}
			</div>

			<!-- Source -->
			<div class="prop-row">
				<label class="prop-label">Source</label>
				<select
					class="prop-select"
					value={selectedSlot.sourceKey}
					onchange={(e) => updateSlotDVE('sourceKey', (e.target as HTMLSelectElement).value)}
				>
					<option value="">---</option>
					{#each sourceKeys as key}
						<option value={key}>{crState.sources[key]?.label || key}</option>
					{/each}
				</select>
			</div>

			<!-- Geometry -->
			<div class="prop-section">
				<h4 class="prop-section-title">Geometry</h4>
				<div class="prop-grid">
					<label class="prop-inline">X <input class="prop-num" type="number" step="0.01" value={dve.positionX} oninput={(e) => updateSlotTransform('positionX', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
					<label class="prop-inline">Y <input class="prop-num" type="number" step="0.01" value={dve.positionY} oninput={(e) => updateSlotTransform('positionY', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
					<label class="prop-inline">W <input class="prop-num" type="number" step="0.01" min="0" max="1" value={dve.width} oninput={(e) => updateSlotTransform('width', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
					<label class="prop-inline">H <input class="prop-num" type="number" step="0.01" min="0" max="1" value={dve.height} oninput={(e) => updateSlotTransform('height', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
				</div>
				<div class="prop-grid">
					<label class="prop-inline">Rot <input class="prop-num" type="number" step="1" min="-360" max="360" value={dve.rotationDeg ?? 0} oninput={(e) => updateSlotTransform('rotationDeg', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
					<label class="prop-inline">Opacity <input class="prop-range" type="range" min="0" max="1" step="0.01" value={dve.opacity ?? 1} oninput={(e) => updateSlotTransform('opacity', parseFloat((e.target as HTMLInputElement).value))} /></label>
				</div>
			</div>

			<!-- Crop -->
			<div class="prop-section">
				<h4 class="prop-section-title">Crop</h4>
				<div class="prop-grid">
					<label class="prop-inline">T <input class="prop-num" type="number" step="0.01" min="0" max="1" value={dve.cropTop ?? 0} oninput={(e) => updateSlotTransform('cropTop', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
					<label class="prop-inline">B <input class="prop-num" type="number" step="0.01" min="0" max="1" value={dve.cropBottom ?? 0} oninput={(e) => updateSlotTransform('cropBottom', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
					<label class="prop-inline">L <input class="prop-num" type="number" step="0.01" min="0" max="1" value={dve.cropLeft ?? 0} oninput={(e) => updateSlotTransform('cropLeft', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
					<label class="prop-inline">R <input class="prop-num" type="number" step="0.01" min="0" max="1" value={dve.cropRight ?? 0} oninput={(e) => updateSlotTransform('cropRight', parseFloat((e.target as HTMLInputElement).value) || 0)} /></label>
				</div>
				<label class="prop-inline">Softness <input class="prop-range" type="range" min="0" max="0.2" step="0.005" value={dve.cropSoftness ?? 0} oninput={(e) => updateSlotTransform('cropSoftness', parseFloat((e.target as HTMLInputElement).value))} /></label>
			</div>

			<!-- Mirror -->
			<div class="prop-section">
				<h4 class="prop-section-title">Mirror</h4>
				<div class="prop-row">
					<button class="toggle-btn" class:active={dve.mirrorH} onclick={() => updateSlotTransform('mirrorH', !dve?.mirrorH)}>H</button>
					<button class="toggle-btn" class:active={dve.mirrorV} onclick={() => updateSlotTransform('mirrorV', !dve?.mirrorV)}>V</button>
				</div>
			</div>

			<!-- Effects -->
			<div class="prop-section">
				<h4 class="prop-section-title">Effects</h4>
				<label class="prop-slider-row">
					<span class="prop-slider-label">Mosaic</span>
					<input class="prop-range" type="range" min="0" max="64" step="1" value={dve.mosaicSize ?? 0} oninput={(e) => updateSlotTransform('mosaicSize', parseInt((e.target as HTMLInputElement).value))} />
					<span class="prop-slider-value">{dve.mosaicSize ?? 0}</span>
				</label>
				<label class="prop-slider-row">
					<span class="prop-slider-label">Saturation</span>
					<input class="prop-range" type="range" min="0" max="2" step="0.01" value={dve.saturation ?? 1} oninput={(e) => updateSlotTransform('saturation', parseFloat((e.target as HTMLInputElement).value))} />
					<span class="prop-slider-value">{(dve.saturation ?? 1).toFixed(2)}</span>
				</label>
				<label class="prop-slider-row">
					<span class="prop-slider-label">Brightness</span>
					<input class="prop-range" type="range" min="-1" max="1" step="0.01" value={dve.brightness ?? 0} oninput={(e) => updateSlotTransform('brightness', parseFloat((e.target as HTMLInputElement).value))} />
					<span class="prop-slider-value">{(dve.brightness ?? 0).toFixed(2)}</span>
				</label>
				<label class="prop-slider-row">
					<span class="prop-slider-label">Contrast</span>
					<input class="prop-range" type="range" min="0" max="3" step="0.01" value={dve.contrast ?? 1} oninput={(e) => updateSlotTransform('contrast', parseFloat((e.target as HTMLInputElement).value))} />
					<span class="prop-slider-value">{(dve.contrast ?? 1).toFixed(2)}</span>
				</label>
				<label class="prop-slider-row">
					<span class="prop-slider-label">Defocus</span>
					<input class="prop-range" type="range" min="0" max="20" step="0.5" value={dve.defocusRadius ?? 0} oninput={(e) => updateSlotTransform('defocusRadius', parseFloat((e.target as HTMLInputElement).value))} />
					<span class="prop-slider-value">{(dve.defocusRadius ?? 0).toFixed(1)}</span>
				</label>
			</div>

			<!-- Freeze / Strobe -->
			<div class="prop-section">
				<h4 class="prop-section-title">Playback</h4>
				<div class="prop-row">
					<label class="prop-check">
						<input type="checkbox" checked={dve.freeze ?? false} onchange={(e) => updateSlotDVE('freeze', (e.target as HTMLInputElement).checked)} />
						Freeze
					</label>
					<label class="prop-inline">
						Strobe
						<input class="prop-num" type="number" min="0" max="30" step="1" value={dve.strobeFrames ?? 0} oninput={(e) => updateSlotDVE('strobeFrames', parseInt((e.target as HTMLInputElement).value) || 0)} />
					</label>
				</div>
			</div>

			<!-- Border -->
			<div class="prop-section">
				<h4 class="prop-section-title">Border</h4>
				<label class="prop-slider-row">
					<span class="prop-slider-label">Width</span>
					<input class="prop-range" type="range" min="0" max="20" step="1" value={dve.border?.width ?? 0} oninput={(e) => updateSlotTransform('border.width', parseInt((e.target as HTMLInputElement).value))} />
					<span class="prop-slider-value">{dve.border?.width ?? 0}</span>
				</label>
				<label class="prop-slider-row">
					<span class="prop-slider-label">Radius</span>
					<input class="prop-range" type="range" min="0" max="50" step="1" value={dve.border?.cornerRadius ?? 0} oninput={(e) => updateSlotTransform('border.cornerRadius', parseInt((e.target as HTMLInputElement).value))} />
					<span class="prop-slider-value">{dve.border?.cornerRadius ?? 0}</span>
				</label>
			</div>
		</div>
	{:else}
		<div class="no-selection">Select a slot to edit DVE properties</div>
	{/if}
</div>

<style>
	.dve-editor {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 4px;
		font-family: var(--font-ui);
	}

	.preset-strip {
		display: flex;
		flex-wrap: wrap;
		gap: 3px;
		padding: 2px 0;
		border-bottom: 1px solid var(--border-subtle);
	}

	.preset-item {
		display: flex;
		align-items: center;
		gap: 1px;
	}

	.preset-btn {
		font-size: 9px;
		font-weight: 500;
		padding: 2px 6px;
		border: 1px solid var(--border-subtle);
		border-radius: 3px;
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		transition: border-color var(--transition-fast), color var(--transition-fast);
	}

	.preset-btn:hover {
		border-color: var(--accent-blue);
		color: var(--text-primary);
	}

	.preset-x {
		font-size: 8px;
		padding: 1px 3px;
		border: none;
		background: transparent;
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.preset-x:hover {
		color: var(--tally-program);
	}

	.preset-del-label {
		font-size: 9px;
		color: var(--text-tertiary);
	}

	.preset-del-yes, .preset-del-no {
		font-size: 9px;
		font-weight: 600;
		padding: 1px 5px;
		border: 1px solid;
		border-radius: 2px;
		cursor: pointer;
	}

	.preset-del-yes {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		color: var(--tally-program);
	}

	.preset-del-no {
		border-color: var(--border-default);
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}

	.preset-save {
		display: flex;
		gap: 2px;
		align-items: center;
		margin-left: auto;
	}

	.preset-name-input {
		width: 60px;
		font-size: 9px;
		padding: 2px 4px;
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		outline: none;
	}

	.preset-name-input:focus {
		border-color: var(--accent-blue);
	}

	.preset-save-btn {
		font-size: 9px;
		font-weight: 600;
		padding: 2px 6px;
		border: 1px solid var(--accent-blue);
		border-radius: 2px;
		background: rgba(59, 130, 246, 0.1);
		color: var(--accent-blue);
		cursor: pointer;
	}

	.preset-save-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.slot-list {
		display: flex;
		gap: 3px;
		flex-wrap: wrap;
	}

	.slot-chip {
		display: flex;
		align-items: center;
		gap: 3px;
		padding: 3px 6px;
		border: 1px solid var(--border-subtle);
		border-radius: 3px;
		background: var(--bg-base);
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 9px;
		transition: border-color var(--transition-fast);
	}

	.slot-chip.enabled {
		border-color: var(--border-default);
		color: var(--text-secondary);
	}

	.slot-chip.selected {
		border-color: var(--accent-blue);
		background: rgba(59, 130, 246, 0.08);
		color: var(--text-primary);
	}

	.slot-id {
		font-weight: 700;
		font-family: var(--font-mono);
		font-size: 9px;
	}

	.slot-src {
		font-size: 9px;
		max-width: 50px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.slot-detail {
		display: flex;
		flex-direction: column;
		gap: 2px;
		max-height: 350px;
		overflow-y: auto;
	}

	.detail-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 2px 0;
		border-bottom: 1px solid var(--border-subtle);
	}

	.detail-title {
		font-size: 10px;
		font-weight: 700;
		color: var(--text-secondary);
	}

	.detail-toggle {
		font-size: 9px;
		font-weight: 700;
		padding: 2px 8px;
		border: 1px solid;
		border-radius: 2px;
		cursor: pointer;
	}

	.detail-toggle.on {
		border-color: rgba(34, 197, 94, 0.4);
		background: rgba(34, 197, 94, 0.1);
		color: rgb(34, 197, 94);
	}

	.detail-toggle.off {
		border-color: rgba(239, 68, 68, 0.4);
		background: rgba(239, 68, 68, 0.1);
		color: rgb(239, 68, 68);
	}

	.prop-section {
		display: flex;
		flex-direction: column;
		gap: 3px;
		padding: 3px 0;
	}

	.prop-section-title {
		font-size: 9px;
		font-weight: 700;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
		margin: 0;
		padding: 0;
	}

	.prop-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.prop-grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 3px;
	}

	.prop-label {
		font-size: 9px;
		color: var(--text-tertiary);
		min-width: 40px;
	}

	.prop-select {
		font-size: 9px;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		padding: 2px 4px;
		flex: 1;
		outline: none;
	}

	.prop-select:focus {
		border-color: var(--accent-blue);
	}

	.prop-inline {
		font-size: 9px;
		color: var(--text-tertiary);
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.prop-num {
		width: 52px;
		font-family: var(--font-mono);
		font-size: 9px;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		padding: 2px 3px;
		outline: none;
	}

	.prop-num:focus {
		border-color: var(--accent-blue);
	}

	.prop-range {
		flex: 1;
		height: 12px;
		accent-color: var(--accent-blue);
		min-width: 60px;
	}

	.prop-slider-row {
		display: flex;
		align-items: center;
		gap: 4px;
		font-size: 9px;
		color: var(--text-tertiary);
	}

	.prop-slider-label {
		min-width: 52px;
		flex-shrink: 0;
	}

	.prop-slider-value {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		min-width: 32px;
		text-align: right;
	}

	.prop-check {
		font-size: 9px;
		color: var(--text-secondary);
		display: flex;
		align-items: center;
		gap: 3px;
		cursor: pointer;
	}

	.prop-check input {
		accent-color: var(--accent-blue);
	}

	.toggle-btn {
		font-size: 9px;
		font-weight: 700;
		padding: 3px 10px;
		border: 1px solid var(--border-subtle);
		border-radius: 3px;
		background: var(--bg-base);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
	}

	.toggle-btn:hover {
		border-color: var(--border-default);
		color: var(--text-secondary);
	}

	.toggle-btn.active {
		border-color: var(--accent-blue);
		background: rgba(59, 130, 246, 0.15);
		color: var(--accent-blue);
	}

	.no-selection {
		font-size: 10px;
		color: var(--text-tertiary);
		text-align: center;
		padding: 16px 8px;
	}
</style>
