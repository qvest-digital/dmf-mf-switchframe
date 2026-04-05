<script lang="ts">
	import type { LayoutState, DVEPresetInfo } from '$lib/api/types';
	import InspectorSection from './InspectorSection.svelte';
	import EasingCurveEditor from './EasingCurveEditor.svelte';
	import { ICON_PLUS, ICON_CLOSE, ICON_PLAY, renderIcon } from '$lib/icons';

	interface Props {
		currentLayout: LayoutState | null;
		presets: DVEPresetInfo[];
		onRecallPreset: (id: string, durationMs: number, easing: string) => void;
	}

	let { currentLayout, presets, onRecallPreset }: Props = $props();

	// Ephemeral state bank (session-level, not persisted)
	interface StoredState {
		id: string;
		name: string;
		presetName: string;
		slotCount: number;
		storedAt: number;
	}

	let states = $state<StoredState[]>([]);
	let expanded = $state(false);
	let sourceStateId = $state<string | null>(null);
	let targetStateId = $state<string | null>(null);
	let durationMs = $state(500);
	let easeX = $state(0.25);
	let easeY = $state(0.75);
	let storeNameInput = $state('');
	let isNaming = $state(false);

	// Combined options: built-in/user presets + manually stored states
	interface TransitionOption {
		id: string;
		label: string;
		presetId: string; // the ID to pass to recallDVEPreset
		isCustom: boolean;
	}

	let allOptions = $derived<TransitionOption[]>([
		...presets.map((p) => ({
			id: `preset:${p.id}`,
			label: p.name,
			presetId: p.id,
			isCustom: false,
		})),
		...states.map((s) => ({
			id: `state:${s.id}`,
			label: s.name,
			presetId: s.presetName,
			isCustom: s.presetName.startsWith('custom-'),
		})),
	]);

	let sourceOption = $derived(allOptions.find((o) => o.id === sourceStateId) ?? null);
	let targetOption = $derived(allOptions.find((o) => o.id === targetStateId) ?? null);

	let isCustomTarget = $derived(targetOption?.isCustom ?? false);
	let canApply = $derived(targetOption !== null && !isCustomTarget);
	let easingString = $derived(`cubic-bezier(0,0,${easeX.toFixed(3)},${easeY.toFixed(3)})`);

	function storeCurrent() {
		if (!currentLayout) return;
		isNaming = true;
		storeNameInput = '';
	}

	function confirmStore() {
		if (!currentLayout) return;
		const name = storeNameInput.trim();
		if (!name) return;

		const presetName = currentLayout.activePreset || `custom-${Date.now()}`;
		const newState: StoredState = {
			id: crypto.randomUUID(),
			name,
			presetName,
			slotCount: currentLayout.slots.length,
			storedAt: Date.now(),
		};
		states = [...states, newState];
		isNaming = false;
		storeNameInput = '';
	}

	function cancelStore() {
		isNaming = false;
		storeNameInput = '';
	}

	function deleteState(id: string) {
		states = states.filter((s) => s.id !== id);
		if (sourceStateId === id) sourceStateId = null;
		if (targetStateId === id) targetStateId = null;
	}

	function selectAsSource(id: string) {
		sourceStateId = sourceStateId === id ? null : id;
	}

	function selectAsTarget(id: string) {
		targetStateId = targetStateId === id ? null : id;
	}

	function applyTransition() {
		if (!targetOption || isCustomTarget) return;
		onRecallPreset(targetOption.presetId, durationMs, easingString);
	}

	function handleEasingChange(x: number, y: number) {
		easeX = x;
		easeY = y;
	}

	function handleDurationInput(e: Event) {
		const input = e.target as HTMLInputElement;
		durationMs = parseInt(input.value, 10);
	}

	function handleStoreKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') confirmStore();
		if (e.key === 'Escape') cancelStore();
	}
</script>

<InspectorSection title="TRANSITIONS" bind:expanded badge={states.length > 0 ? String(states.length) : undefined}>
	<div class="transition-editor">
		<!-- State bank -->
		<div class="state-bank">
			<div class="row-label">States</div>
			<div class="state-chips">
				{#each states as st (st.id)}
					<div
						class="state-chip"
						class:source={sourceStateId === st.id}
						class:target={targetStateId === st.id}
						class:custom={st.presetName.startsWith('custom-')}
					>
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<span
							class="state-chip__name"
							onclick={() => selectAsTarget(st.id)}
							oncontextmenu={(e) => { e.preventDefault(); selectAsSource(st.id); }}
							title="Click: set target | Right-click: set source{st.presetName.startsWith('custom-') ? ' | Custom layout (cannot apply)' : ''}"
						>
							{st.name}
						</span>
						<span class="state-chip__count">{st.slotCount}</span>
						<button
							type="button"
							class="state-chip__delete"
							onclick={() => deleteState(st.id)}
							title="Remove state"
						>
							{@html renderIcon(ICON_CLOSE, 9)}
						</button>
					</div>
				{/each}

				{#if isNaming}
					<div class="store-inline">
						<input
							class="store-input"
							type="text"
							placeholder="Name..."
							bind:value={storeNameInput}
							onkeydown={handleStoreKeydown}
						/>
						<button type="button" class="store-confirm" onclick={confirmStore} title="Save state">
							OK
						</button>
						<button type="button" class="store-cancel" onclick={cancelStore} title="Cancel">
							{@html renderIcon(ICON_CLOSE, 10)}
						</button>
					</div>
				{:else}
					<button
						type="button"
						class="store-btn"
						onclick={storeCurrent}
						disabled={!currentLayout}
						title={currentLayout ? 'Store current layout as a named state' : 'No layout active'}
					>
						<span class="store-btn__icon">{@html renderIcon(ICON_PLUS, 10)}</span>
						Store
					</button>
				{/if}
			</div>
		</div>

		<!-- Source -> Target selectors -->
		<div class="transition-pair">
			<div class="row-label">Transition</div>
			<div class="pair-controls">
				<select
					class="state-select"
					bind:value={sourceStateId}
					title="Source state (from)"
				>
					<option value={null}>-- Source --</option>
					{#if presets.length > 0}
						<optgroup label="Presets">
							{#each presets as p (p.id)}
								<option value={`preset:${p.id}`}>{p.name}</option>
							{/each}
						</optgroup>
					{/if}
					{#if states.length > 0}
						<optgroup label="Stored States">
							{#each states as st (st.id)}
								<option value={`state:${st.id}`}>{st.name}</option>
							{/each}
						</optgroup>
					{/if}
				</select>
				<span class="arrow-sep">&rarr;</span>
				<select
					class="state-select"
					bind:value={targetStateId}
					title="Target state (to)"
				>
					<option value={null}>-- Target --</option>
					{#if presets.length > 0}
						<optgroup label="Presets">
							{#each presets as p (p.id)}
								<option value={`preset:${p.id}`}>{p.name}</option>
							{/each}
						</optgroup>
					{/if}
					{#if states.length > 0}
						<optgroup label="Stored States">
							{#each states as st (st.id)}
								<option value={`state:${st.id}`}>{st.name}{st.presetName.startsWith('custom-') ? ' *' : ''}</option>
							{/each}
						</optgroup>
					{/if}
				</select>
			</div>
		</div>

		<!-- Duration slider -->
		<div class="duration-row">
			<div class="row-label">Duration</div>
			<div class="duration-controls">
				<input
					type="range"
					class="duration-slider"
					min="50"
					max="3000"
					step="10"
					value={durationMs}
					oninput={handleDurationInput}
				/>
				<span class="duration-value">{durationMs}ms</span>
			</div>
		</div>

		<!-- Easing -->
		<div class="easing-row">
			<div class="row-label">Easing</div>
			<div class="easing-controls">
				<EasingCurveEditor {easeX} {easeY} onChange={handleEasingChange} />
			</div>
		</div>

		<!-- Apply button -->
		<div class="apply-row">
			{#if isCustomTarget}
				<div class="custom-warning" title="Custom layouts cannot be recalled via preset. Store a layout while a preset is active.">
					Target is a custom layout (no preset to recall)
				</div>
			{/if}
			<button
				type="button"
				class="apply-btn"
				class:disabled={!canApply}
				disabled={!canApply}
				onclick={applyTransition}
				title={canApply ? `Apply transition: ${easingString} over ${durationMs}ms` : 'Select a target state with a valid preset'}
			>
				<span class="apply-btn__icon">{@html renderIcon(ICON_PLAY, 12, 'fill')}</span>
				Apply Transition
			</button>
		</div>
	</div>
</InspectorSection>

<style>
	.transition-editor {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 6px 4px 8px;
	}

	.row-label {
		font-size: 10px;
		font-weight: 600;
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		margin-bottom: 3px;
	}

	/* --- State bank --- */
	.state-bank {
		display: flex;
		flex-direction: column;
	}

	.state-chips {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		align-items: center;
	}

	.state-chip {
		display: inline-flex;
		align-items: center;
		gap: 2px;
		padding: 2px 3px 2px 6px;
		background: var(--bg-control, #242429);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		font-family: var(--font-ui);
		user-select: none;
		transition:
			border-color var(--transition-fast, 150ms),
			background var(--transition-fast, 150ms);
	}

	.state-chip:hover {
		background: var(--bg-hover, #2c2c32);
	}

	.state-chip.source {
		border-color: rgba(59, 130, 246, 0.5);
		background: rgba(59, 130, 246, 0.08);
	}

	.state-chip.target {
		border-color: rgba(34, 197, 94, 0.5);
		background: rgba(34, 197, 94, 0.08);
	}

	.state-chip.custom {
		border-style: dashed;
	}

	.state-chip__name {
		font-size: 10px;
		font-weight: 600;
		color: var(--text-secondary);
		cursor: pointer;
		white-space: nowrap;
	}

	.state-chip:hover .state-chip__name {
		color: var(--text-primary);
	}

	.state-chip__count {
		font-size: 8px;
		color: var(--text-tertiary);
		background: var(--bg-base, #09090b);
		padding: 0 3px;
		border-radius: 2px;
		line-height: 1.4;
	}

	.state-chip__delete {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 14px;
		height: 14px;
		padding: 0;
		border: none;
		border-radius: 2px;
		background: transparent;
		color: var(--text-tertiary);
		cursor: pointer;
		opacity: 0;
		transition: opacity var(--transition-fast, 150ms);
	}

	.state-chip:hover .state-chip__delete {
		opacity: 1;
	}

	.state-chip__delete:hover {
		background: rgba(239, 68, 68, 0.15);
		color: var(--color-error, #ef4444);
	}

	/* Store button */
	.store-btn {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		padding: 3px 7px;
		background: var(--bg-control, #242429);
		border: 1px dashed var(--border-default);
		border-radius: var(--radius-xs, 3px);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		cursor: pointer;
		transition:
			background var(--transition-fast, 150ms),
			color var(--transition-fast, 150ms);
	}

	.store-btn:hover:not(:disabled) {
		background: var(--bg-hover, #2c2c32);
		color: var(--text-primary);
	}

	.store-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}

	.store-btn__icon {
		display: flex;
		color: currentColor;
	}

	/* Inline naming */
	.store-inline {
		display: inline-flex;
		align-items: center;
		gap: 3px;
	}

	.store-input {
		width: 72px;
		padding: 2px 5px;
		font-family: var(--font-ui);
		font-size: 10px;
		background: var(--bg-base, #09090b);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs, 3px);
		outline: none;
	}

	.store-input:focus {
		border-color: var(--border-strong);
	}

	.store-confirm {
		padding: 2px 6px;
		background: rgba(34, 197, 94, 0.15);
		border: 1px solid rgba(34, 197, 94, 0.35);
		border-radius: var(--radius-xs, 3px);
		color: var(--color-success, #22c55e);
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		cursor: pointer;
	}

	.store-confirm:hover {
		background: rgba(34, 197, 94, 0.22);
	}

	.store-cancel {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 18px;
		height: 18px;
		padding: 0;
		border: none;
		border-radius: var(--radius-xs, 3px);
		background: transparent;
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.store-cancel:hover {
		background: var(--bg-hover, #2c2c32);
		color: var(--text-secondary);
	}

	/* --- Transition pair --- */
	.transition-pair {
		display: flex;
		flex-direction: column;
	}

	.pair-controls {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.state-select {
		flex: 1;
		padding: 3px 5px;
		font-family: var(--font-ui);
		font-size: 10px;
		background: var(--bg-control, #242429);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs, 3px);
		cursor: pointer;
		min-width: 0;
	}

	.state-select:hover {
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.arrow-sep {
		flex-shrink: 0;
		font-size: 12px;
		color: var(--text-tertiary);
	}

	/* --- Duration --- */
	.duration-row {
		display: flex;
		flex-direction: column;
	}

	.duration-controls {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.duration-slider {
		flex: 1;
		height: 4px;
		-webkit-appearance: none;
		appearance: none;
		background: var(--bg-control, #242429);
		border-radius: 2px;
		outline: none;
		cursor: pointer;
	}

	.duration-slider::-webkit-slider-thumb {
		-webkit-appearance: none;
		width: 12px;
		height: 12px;
		border-radius: 50%;
		background: var(--accent-gold, #d4a017);
		border: 2px solid var(--bg-base, #09090b);
		cursor: pointer;
	}

	.duration-slider::-moz-range-thumb {
		width: 12px;
		height: 12px;
		border-radius: 50%;
		background: var(--accent-gold, #d4a017);
		border: 2px solid var(--bg-base, #09090b);
		cursor: pointer;
	}

	.duration-slider::-webkit-slider-runnable-track {
		height: 4px;
		border-radius: 2px;
	}

	.duration-value {
		flex-shrink: 0;
		font-size: 10px;
		font-weight: 600;
		font-family: var(--font-mono, monospace);
		color: var(--text-secondary);
		min-width: 44px;
		text-align: right;
	}

	/* --- Easing --- */
	.easing-row {
		display: flex;
		flex-direction: column;
	}

	.easing-controls {
		display: flex;
	}

	/* --- Apply button --- */
	.apply-row {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-top: 2px;
	}

	.custom-warning {
		font-size: 9px;
		font-family: var(--font-ui);
		color: var(--color-warning, #eab308);
		padding: 3px 6px;
		background: rgba(234, 179, 8, 0.08);
		border: 1px solid rgba(234, 179, 8, 0.2);
		border-radius: var(--radius-xs, 3px);
		line-height: 1.3;
	}

	.apply-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 6px;
		width: 100%;
		padding: 6px 12px;
		background: rgba(34, 197, 94, 0.15);
		border: 1px solid rgba(34, 197, 94, 0.35);
		border-radius: var(--radius-xs, 3px);
		color: var(--color-success, #22c55e);
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 700;
		letter-spacing: 0.02em;
		cursor: pointer;
		transition:
			background var(--transition-fast, 150ms),
			border-color var(--transition-fast, 150ms);
	}

	.apply-btn:hover:not(:disabled) {
		background: rgba(34, 197, 94, 0.25);
		border-color: rgba(34, 197, 94, 0.5);
	}

	.apply-btn:active:not(:disabled) {
		background: rgba(34, 197, 94, 0.3);
	}

	.apply-btn.disabled {
		opacity: 0.35;
		cursor: not-allowed;
	}

	.apply-btn__icon {
		display: flex;
		color: currentColor;
	}
</style>
