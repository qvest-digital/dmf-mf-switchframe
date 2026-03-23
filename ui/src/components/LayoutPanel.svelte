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
	import {
		ICON_SAVE, ICON_CLOSE, ICON_TRASH, ICON_CHECK, ICON_GRID, ICON_PLUS, renderIcon
	} from '$lib/icons';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	let presets = $state<string[]>([]);
	let saving = $state(false);
	let presetName = $state('');
	let deleteConfirmName = $state<string | null>(null);
	let selectedSlotId = $state<number | null>(null);

	let layoutState = $derived(crState.layout);
	let slots = $derived(layoutState?.slots ?? []);
	let sourceKeys = $derived(Object.keys(crState.sources).sort());
	let activePreset = $derived(layoutState?.activePreset ?? '');
	let selectedSlot = $derived(
		selectedSlotId == null ? null : slots.find((s) => s.id === selectedSlotId) ?? null
	);

	// Built-in presets
	const builtinPresets = [
		{ id: 'pip-top-right', label: 'PIP TR', desc: 'Picture-in-Picture (top right)' },
		{ id: 'pip-top-left', label: 'PIP TL', desc: 'Picture-in-Picture (top left)' },
		{ id: 'pip-bottom-right', label: 'PIP BR', desc: 'Picture-in-Picture (bottom right)' },
		{ id: 'pip-bottom-left', label: 'PIP BL', desc: 'Picture-in-Picture (bottom left)' },
		{ id: 'side-by-side', label: 'SBS', desc: 'Side-by-side split' },
		{ id: 'quad', label: 'QUAD', desc: '2x2 quad view' },
	];

	$effect(() => {
		const ids = new Set(slots.map((s) => s.id));
		if (selectedSlotId != null && !ids.has(selectedSlotId)) {
			selectedSlotId = null;
		}
	});

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

	function handleScaleModeChange(slotId: number, mode: string) {
		apiCall(
			updateLayoutSlot(slotId, { scaleMode: mode }),
			'Set scale mode'
		);
	}

	function handleCropAnchorChange(slotId: number, preset: string) {
		const anchors: Record<string, [number, number]> = {
			'center': [0.5, 0.5],
			'top': [0.5, 0.0],
			'bottom': [0.5, 1.0],
			'left': [0.0, 0.5],
			'right': [1.0, 0.5],
			'top-left': [0.0, 0.0],
			'top-right': [1.0, 0.0],
			'bottom-left': [0.0, 1.0],
			'bottom-right': [1.0, 1.0],
		};
		const anchor = anchors[preset] ?? [0.5, 0.5];
		apiCall(
			updateLayoutSlot(slotId, { cropAnchor: anchor }),
			'Set crop anchor'
		);
	}

	function anchorKey(anchor?: [number, number]): string {
		if (!anchor) return 'center';
		const [x, y] = anchor;
		if (x === 0.5 && y === 0.5) return 'center';
		if (x === 0.5 && y === 0) return 'top';
		if (x === 0.5 && y === 1) return 'bottom';
		if (x === 0 && y === 0.5) return 'left';
		if (x === 1 && y === 0.5) return 'right';
		if (x === 0 && y === 0) return 'top-left';
		if (x === 1 && y === 0) return 'top-right';
		if (x === 0 && y === 1) return 'bottom-left';
		if (x === 1 && y === 1) return 'bottom-right';
		return 'center';
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

	function selectSlotCard(slotId: number, e: MouseEvent) {
		const t = e.target as HTMLElement;
		if (t.closest('button, select, option')) return;
		selectedSlotId = slotId;
	}

	onMount(() => {
		loadPresets();
	});
</script>

<div class="layout-panel">
	<!-- Preset strip: built-ins + user presets (scroll) + save/clear -->
	<div class="preset-strip">
		<div class="preset-strip__label" title="Layout presets">
			<span class="preset-strip__icon" aria-hidden="true">{@html renderIcon(ICON_GRID, 13, 'fill')}</span>
			<span class="preset-strip__title">LAYOUT</span>
		</div>
		<div class="preset-strip__scroll">
			<div class="preset-strip__inner">
				{#each builtinPresets as preset}
					<button
						type="button"
						class="preset-btn preset-btn--builtin"
						class:active={activePreset === preset.id}
						onclick={() => applyBuiltinPreset(preset.id)}
						title={preset.desc}
					>
						{preset.label}
					</button>
				{/each}
				{#each presets as name}
					<div class="user-preset-chip" class:active={activePreset === name}>
						{#if deleteConfirmName === name}
							<span class="user-preset-chip__confirm-label">Delete?</span>
							<button
								type="button"
								class="icon-btn icon-btn--danger"
								title="Confirm delete"
								onclick={() => handleDelete(name)}
							>
								<span aria-hidden="true">{@html renderIcon(ICON_CHECK, 12)}</span>
							</button>
							<button
								type="button"
								class="icon-btn"
								title="Cancel"
								onclick={() => { deleteConfirmName = null; }}
							>
								<span aria-hidden="true">{@html renderIcon(ICON_CLOSE, 12)}</span>
							</button>
						{:else}
							<button
								type="button"
								class="preset-btn preset-btn--user"
								class:active={activePreset === name}
								onclick={() => applyUserPreset(name)}
								title="User preset: {name}"
							>
								{name}
							</button>
							<button
								type="button"
								class="icon-btn icon-btn--ghost"
								title="Delete preset"
								onclick={(e) => { e.stopPropagation(); deleteConfirmName = name; }}
							>
								<span aria-hidden="true">{@html renderIcon(ICON_TRASH, 11)}</span>
							</button>
						{/if}
					</div>
				{/each}
			</div>
		</div>
		<div class="preset-strip__actions">
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
			<button type="button" class="action-btn action-btn--clear" onclick={handleClear} title="Clear layout">
				Clear
			</button>
		</div>
	</div>

	<!-- Slot strip -->
	<div class="slot-strip-wrap">
		{#if slots.length > 0}
			<div class="slot-strip">
				{#each slots as slot (slot.id)}
					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<!-- svelte-ignore a11y_click_events_have_key_events -->
					<div
						class="slot-card"
						class:slot-enabled={slot.enabled}
						class:slot-animating={slot.animating}
						class:slot-selected={selectedSlotId === slot.id}
						onclick={(e) => selectSlotCard(slot.id, e)}
					>
						<button
							type="button"
							class="slot-toggle"
							class:on={slot.enabled}
							onclick={(e) => { e.stopPropagation(); toggleSlot(slot); }}
						>
							{slot.enabled ? 'ON' : 'OFF'}
						</button>
						<span class="slot-card__id">Slot {slot.id}</span>
						<div class="slot-card__row slot-card__row--source">
							<select
								class="slot-select"
								value={slot.sourceKey}
								onchange={(e) => handleSourceChange(slot.id, (e.target as HTMLSelectElement).value)}
								onclick={(e) => e.stopPropagation()}
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
						<div class="slot-card__pos" title="Position, size, z-order">
							<span class="pos-text">{slot.x},{slot.y} {slot.width}×{slot.height}</span>
							<span class="z-text">z:{slot.zOrder}</span>
						</div>
						<select
							class="slot-select slot-select--transition"
							onchange={(e) => handleTransitionChange(slot.id, (e.target as HTMLSelectElement).value)}
							onclick={(e) => e.stopPropagation()}
						>
							<option value="cut">Cut</option>
							<option value="dissolve">Dissolve</option>
							<option value="fly">Fly</option>
						</select>
					</div>
				{/each}
			</div>
		{:else}
			<div class="empty-state">No active layout. Select a preset above.</div>
		{/if}
	</div>

	<!-- Detail: scale + crop (selected slot only) -->
	{#if selectedSlot}
		<div class="detail-bar">
			<span class="detail-bar__label">Slot {selectedSlot.id}</span>
			<label class="detail-field">
				<span class="detail-field__name">Scale</span>
				<select
					class="detail-select"
					value={selectedSlot.scaleMode || 'stretch'}
					onchange={(e) => handleScaleModeChange(selectedSlot!.id, (e.target as HTMLSelectElement).value)}
				>
					<option value="stretch">Stretch</option>
					<option value="fill">Fill (crop)</option>
				</select>
			</label>
			{#if (selectedSlot.scaleMode || 'stretch') === 'fill'}
				<label class="detail-field">
					<span class="detail-field__name">Crop</span>
					<select
						class="detail-select"
						value={anchorKey(selectedSlot.cropAnchor)}
						onchange={(e) => handleCropAnchorChange(selectedSlot!.id, (e.target as HTMLSelectElement).value)}
					>
						<option value="center">Center</option>
						<option value="top">Top</option>
						<option value="bottom">Bottom</option>
						<option value="left">Left</option>
						<option value="right">Right</option>
						<option value="top-left">Top-left</option>
						<option value="top-right">Top-right</option>
						<option value="bottom-left">Bottom-left</option>
						<option value="bottom-right">Bottom-right</option>
					</select>
				</label>
			{/if}
		</div>
	{/if}
</div>

<style>
	.layout-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		padding: 4px 6px;
		gap: 0;
		font-family: var(--font-ui);
	}

	/* ── Preset strip ── */
	.preset-strip {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 0 6px;
		border-bottom: 1px solid var(--border-subtle);
		min-height: 0;
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

	/* ── Slot strip ── */
	.slot-strip-wrap {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		padding: 6px 0 4px;
	}

	.slot-strip {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: row;
		align-items: stretch;
		gap: 6px;
		overflow-x: auto;
		overflow-y: hidden;
		-webkit-overflow-scrolling: touch;
		padding-bottom: 2px;
	}

	.slot-card {
		flex: 0 0 130px;
		width: 130px;
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 5px 6px 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
		cursor: default;
	}

	.slot-card.slot-selected {
		border-color: rgba(59, 130, 246, 0.55);
		box-shadow: 0 0 0 1px rgba(59, 130, 246, 0.2);
	}

	.slot-card.slot-enabled {
		border-color: rgba(212, 160, 23, 0.4);
	}

	.slot-card.slot-animating {
		border-color: rgba(59, 130, 246, 0.45);
	}

	.slot-toggle {
		width: 100%;
		padding: 3px 4px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.04em;
		cursor: pointer;
		background: var(--bg-base);
		color: var(--text-tertiary);
		transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
	}

	.slot-toggle.on {
		background: rgba(212, 160, 23, 0.22);
		color: var(--accent-gold);
		border-color: rgba(212, 160, 23, 0.45);
	}

	.slot-card__id {
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		text-align: center;
		letter-spacing: 0.03em;
	}

	.slot-card__row {
		display: flex;
		align-items: center;
		gap: 4px;
		min-width: 0;
	}

	.slot-select {
		flex: 1;
		min-width: 0;
		padding: 2px 3px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
	}

	.slot-select--transition {
		flex: none;
		width: 100%;
	}

	.tally-dot {
		width: 5px;
		height: 5px;
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
		background: var(--accent-gold);
	}

	.slot-card__pos {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
		gap: 4px;
		padding: 3px 0;
		border-top: 1px solid var(--border-subtle);
		border-bottom: 1px solid var(--border-subtle);
		min-width: 0;
	}

	.pos-text, .z-text {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		line-height: 1.2;
	}

	.pos-text {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.z-text {
		flex-shrink: 0;
	}

	.empty-state {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		text-align: center;
		color: var(--text-tertiary);
		font-size: var(--text-xs);
		padding: 8px;
	}

	/* ── Detail bar ── */
	.detail-bar {
		flex-shrink: 0;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 10px 14px;
		padding: 6px 8px;
		margin-top: 2px;
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.detail-bar__label {
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
		margin-right: 4px;
	}

	.detail-field {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin: 0;
	}

	.detail-field__name {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.detail-select {
		padding: 3px 6px;
		min-width: 120px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		background: var(--bg-control);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
	}
</style>
