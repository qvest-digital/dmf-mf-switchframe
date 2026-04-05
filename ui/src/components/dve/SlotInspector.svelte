<script lang="ts">
	import type { LayoutSlotState, SourceInfo, DVESlotState } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import {
		DVE_PARAM_POSITION_X, DVE_PARAM_POSITION_Y, DVE_PARAM_WIDTH, DVE_PARAM_HEIGHT,
		DVE_PARAM_ROTATION_DEG, DVE_PARAM_PERSPECTIVE_X, DVE_PARAM_PERSPECTIVE_Y,
		DVE_PARAM_CROP_TOP, DVE_PARAM_CROP_BOTTOM, DVE_PARAM_CROP_LEFT, DVE_PARAM_CROP_RIGHT,
		DVE_PARAM_OPACITY, DVE_PARAM_DEFOCUS_RADIUS, DVE_PARAM_BORDER_WIDTH, DVE_PARAM_CORNER_RADIUS,
	} from '$lib/transport/fast-control';
	import ScrubInput from './ScrubInput.svelte';
	import InspectorSection from './InspectorSection.svelte';

	interface Props {
		slot: LayoutSlotState;
		sources: Record<string, SourceInfo>;
		fastControl?: FastControl | null;
		onUpdate: (slotId: number, update: Record<string, unknown>) => void;
		onSourceChange: (slotId: number, source: string) => void;
		onScaleModeChange: (slotId: number, mode: string) => void;
		onCropAnchorChange: (slotId: number, anchor: [number, number]) => void;
	}

	let {
		slot,
		sources,
		fastControl,
		onUpdate,
		onSourceChange,
		onScaleModeChange,
		onCropAnchorChange,
	}: Props = $props();

	// Section expansion state
	let positionExpanded = $state(true);
	let borderExpanded = $state(false);
	let effectsExpanded = $state(false);
	let cropExpanded = $state(false);
	let shadowExpanded = $state(false);

	// Convenience accessor for DVE state with defaults
	let dve = $derived(slot.dve);

	// ── Fast-control param ID map ──
	const paramIdMap: Record<string, number> = {
		positionX: DVE_PARAM_POSITION_X,
		positionY: DVE_PARAM_POSITION_Y,
		width: DVE_PARAM_WIDTH,
		height: DVE_PARAM_HEIGHT,
		rotationDeg: DVE_PARAM_ROTATION_DEG,
		perspectiveX: DVE_PARAM_PERSPECTIVE_X,
		perspectiveY: DVE_PARAM_PERSPECTIVE_Y,
		cropTop: DVE_PARAM_CROP_TOP,
		cropBottom: DVE_PARAM_CROP_BOTTOM,
		cropLeft: DVE_PARAM_CROP_LEFT,
		cropRight: DVE_PARAM_CROP_RIGHT,
		opacity: DVE_PARAM_OPACITY,
		defocusRadius: DVE_PARAM_DEFOCUS_RADIUS,
		'border.width': DVE_PARAM_BORDER_WIDTH,
		'border.cornerRadius': DVE_PARAM_CORNER_RADIUS,
	};

	// ── Build full transform update from current DVE state + changed field ──
	function buildTransformUpdate(field: string, value: number | boolean): Record<string, unknown> {
		const cur = slot.dve;
		if (!cur) return {};
		const xf: Record<string, unknown> = {
			positionX: cur.positionX ?? 0,
			positionY: cur.positionY ?? 0,
			width: cur.width ?? 1920,
			height: cur.height ?? 1080,
			rotationDeg: cur.rotationDeg ?? 0,
			perspectiveX: cur.perspectiveX ?? 0,
			perspectiveY: cur.perspectiveY ?? 0,
			opacity: cur.opacity ?? 1,
			cropTop: cur.cropTop ?? 0,
			cropBottom: cur.cropBottom ?? 0,
			cropLeft: cur.cropLeft ?? 0,
			cropRight: cur.cropRight ?? 0,
			cropSoftness: cur.cropSoftness ?? 0,
			defocusRadius: cur.defocusRadius ?? 0,
			anchorX: cur.anchorX ?? 0.5,
			anchorY: cur.anchorY ?? 0.5,
			saturation: cur.saturation ?? 1,
			brightness: cur.brightness ?? 0,
			contrast: cur.contrast ?? 1,
		};
		// Apply changed field (supports dot-separated like "border.width")
		const parts = field.split('.');
		if (parts.length === 2) {
			const section = parts[0];
			const existing = (cur as unknown as Record<string, unknown>)[section] as Record<string, unknown> | undefined;
			xf[section] = { ...existing, [parts[1]]: value };
		} else {
			xf[field] = value;
		}
		return { transform: xf };
	}

	// ── Scrub handler: send fast-control datagram for real-time preview ──
	function handleScrub(field: string, value: number) {
		const paramId = paramIdMap[field];
		if (paramId !== undefined && fastControl) {
			fastControl.sendDVESingleParam(slot.id, paramId, value);
		}
	}

	// ── Commit handler: send REST update ──
	function handleCommit(field: string, value: number) {
		onUpdate(slot.id, buildTransformUpdate(field, value));
	}

	// ── Boolean toggle handler ──
	function handleToggle(field: string, value: boolean) {
		onUpdate(slot.id, buildTransformUpdate(field, value));
	}

	// Source list sorted by position
	let sortedSources = $derived(
		Object.values(sources).sort((a, b) => (a.position ?? 0) - (b.position ?? 0)),
	);

	// Crop anchor grid positions
	const anchorPositions: { label: string; value: [number, number] }[] = [
		{ label: 'TL', value: [0, 0] },
		{ label: 'T', value: [0.5, 0] },
		{ label: 'TR', value: [1, 0] },
		{ label: 'L', value: [0, 0.5] },
		{ label: 'C', value: [0.5, 0.5] },
		{ label: 'R', value: [1, 0.5] },
		{ label: 'BL', value: [0, 1] },
		{ label: 'B', value: [0.5, 1] },
		{ label: 'BR', value: [1, 1] },
	];

	function isActiveAnchor(anchor: [number, number]): boolean {
		const current = slot.cropAnchor ?? [0.5, 0.5];
		return current[0] === anchor[0] && current[1] === anchor[1];
	}
</script>

<div class="slot-inspector">
	<!-- Section 1: Source & Scale (always visible, no collapsible header) -->
	<div class="source-section">
		<div class="field-row">
			<span class="field-label">Source</span>
			<select
				class="inspector-select"
				value={slot.sourceKey}
				onchange={(e) => onSourceChange(slot.id, (e.target as HTMLSelectElement).value)}
			>
				{#each sortedSources as src (src.key)}
					<option value={src.key}>{src.label || src.key}</option>
				{/each}
			</select>
		</div>
		<div class="field-row">
			<span class="field-label">Scale</span>
			<select
				class="inspector-select"
				value={slot.scaleMode ?? 'fit'}
				onchange={(e) => onScaleModeChange(slot.id, (e.target as HTMLSelectElement).value)}
			>
				<option value="stretch">Stretch</option>
				<option value="fill">Fill</option>
				<option value="fit">Fit</option>
			</select>
		</div>
		{#if slot.scaleMode === 'fill'}
			<div class="anchor-section">
				<span class="field-label">Anchor</span>
				<div class="anchor-grid">
					{#each anchorPositions as pos (pos.label)}
						<button
							class="anchor-btn"
							class:active={isActiveAnchor(pos.value)}
							type="button"
							title={pos.label}
							onclick={() => onCropAnchorChange(slot.id, pos.value)}
						></button>
					{/each}
				</div>
			</div>
		{/if}
	</div>

	<!-- Section 2: Position & Size -->
	<InspectorSection title="Position & Size" bind:expanded={positionExpanded}>
		<div class="field-grid">
			<span class="field-label">X</span>
			<ScrubInput
				value={dve?.positionX ?? slot.x}
				step={1}
				onScrub={(v) => handleScrub('positionX', v)}
				onCommit={(v) => handleCommit('positionX', v)}
			/>
			<span class="field-label">Y</span>
			<ScrubInput
				value={dve?.positionY ?? slot.y}
				step={1}
				onScrub={(v) => handleScrub('positionY', v)}
				onCommit={(v) => handleCommit('positionY', v)}
			/>
			<span class="field-label">W</span>
			<ScrubInput
				value={dve?.width ?? slot.width}
				step={1}
				min={2}
				onScrub={(v) => handleScrub('width', v)}
				onCommit={(v) => handleCommit('width', v)}
			/>
			<span class="field-label">H</span>
			<ScrubInput
				value={dve?.height ?? slot.height}
				step={1}
				min={2}
				onScrub={(v) => handleScrub('height', v)}
				onCommit={(v) => handleCommit('height', v)}
			/>
		</div>
	</InspectorSection>

	<!-- Section 3: Border -->
	<InspectorSection title="Border" bind:expanded={borderExpanded}>
		<div class="field-grid">
			<span class="field-label">Width</span>
			<ScrubInput
				value={dve?.border?.width ?? 0}
				step={1}
				min={0}
				suffix="px"
				onScrub={(v) => handleScrub('border.width', v)}
				onCommit={(v) => handleCommit('border.width', v)}
			/>
			<span class="field-label">Radius</span>
			<ScrubInput
				value={dve?.border?.cornerRadius ?? 0}
				step={1}
				min={0}
				onScrub={(v) => handleScrub('border.cornerRadius', v)}
				onCommit={(v) => handleCommit('border.cornerRadius', v)}
			/>
			<span class="field-label">Y</span>
			<ScrubInput
				value={dve?.border?.colorY ?? 235}
				step={1}
				min={0}
				max={255}
				onCommit={(v) => handleCommit('border.colorY', v)}
			/>
			<span class="field-label">Cb</span>
			<ScrubInput
				value={dve?.border?.colorCb ?? 128}
				step={1}
				min={0}
				max={255}
				onCommit={(v) => handleCommit('border.colorCb', v)}
			/>
			<span class="field-label">Cr</span>
			<ScrubInput
				value={dve?.border?.colorCr ?? 128}
				step={1}
				min={0}
				max={255}
				onCommit={(v) => handleCommit('border.colorCr', v)}
			/>
		</div>
	</InspectorSection>

	<!-- Section 4: Effects -->
	<InspectorSection title="Effects" bind:expanded={effectsExpanded}>
		<div class="field-grid">
			<span class="field-label">Rotation</span>
			<ScrubInput
				value={dve?.rotationDeg ?? 0}
				step={1}
				min={-360}
				max={360}
				suffix="°"
				onScrub={(v) => handleScrub('rotationDeg', v)}
				onCommit={(v) => handleCommit('rotationDeg', v)}
			/>
			<span class="field-label">Opacity</span>
			<ScrubInput
				value={dve?.opacity ?? 1}
				step={0.01}
				min={0}
				max={1}
				precision={2}
				onScrub={(v) => handleScrub('opacity', v)}
				onCommit={(v) => handleCommit('opacity', v)}
			/>
			<span class="field-label">Persp X</span>
			<ScrubInput
				value={dve?.perspectiveX ?? 0}
				step={1}
				min={-60}
				max={60}
				suffix="°"
				onScrub={(v) => handleScrub('perspectiveX', v)}
				onCommit={(v) => handleCommit('perspectiveX', v)}
			/>
			<span class="field-label">Persp Y</span>
			<ScrubInput
				value={dve?.perspectiveY ?? 0}
				step={1}
				min={-60}
				max={60}
				suffix="°"
				onScrub={(v) => handleScrub('perspectiveY', v)}
				onCommit={(v) => handleCommit('perspectiveY', v)}
			/>
			<span class="field-label">Defocus</span>
			<ScrubInput
				value={dve?.defocusRadius ?? 0}
				step={0.5}
				min={0}
				onScrub={(v) => handleScrub('defocusRadius', v)}
				onCommit={(v) => handleCommit('defocusRadius', v)}
			/>
			<span class="field-label">Saturation</span>
			<ScrubInput
				value={dve?.saturation ?? 1}
				step={0.01}
				min={0}
				max={2}
				precision={2}
				onCommit={(v) => handleCommit('saturation', v)}
			/>
			<span class="field-label">Brightness</span>
			<ScrubInput
				value={dve?.brightness ?? 0}
				step={0.01}
				min={-1}
				max={1}
				precision={2}
				onCommit={(v) => handleCommit('brightness', v)}
			/>
			<span class="field-label">Contrast</span>
			<ScrubInput
				value={dve?.contrast ?? 1}
				step={0.01}
				min={0}
				max={3}
				precision={2}
				onCommit={(v) => handleCommit('contrast', v)}
			/>
		</div>
		<div class="toggle-row">
			<label class="toggle-label">
				<input
					type="checkbox"
					checked={dve?.mirrorH ?? false}
					onchange={(e) => handleToggle('mirrorH', (e.target as HTMLInputElement).checked)}
				/>
				<span>Mirror H</span>
			</label>
			<label class="toggle-label">
				<input
					type="checkbox"
					checked={dve?.mirrorV ?? false}
					onchange={(e) => handleToggle('mirrorV', (e.target as HTMLInputElement).checked)}
				/>
				<span>Mirror V</span>
			</label>
		</div>
	</InspectorSection>

	<!-- Section 5: Crop -->
	<InspectorSection title="Crop" bind:expanded={cropExpanded}>
		<div class="field-grid">
			<span class="field-label">Top</span>
			<ScrubInput
				value={dve?.cropTop ?? 0}
				step={1}
				min={0}
				suffix="px"
				onScrub={(v) => handleScrub('cropTop', v)}
				onCommit={(v) => handleCommit('cropTop', v)}
			/>
			<span class="field-label">Bottom</span>
			<ScrubInput
				value={dve?.cropBottom ?? 0}
				step={1}
				min={0}
				suffix="px"
				onScrub={(v) => handleScrub('cropBottom', v)}
				onCommit={(v) => handleCommit('cropBottom', v)}
			/>
			<span class="field-label">Left</span>
			<ScrubInput
				value={dve?.cropLeft ?? 0}
				step={1}
				min={0}
				suffix="px"
				onScrub={(v) => handleScrub('cropLeft', v)}
				onCommit={(v) => handleCommit('cropLeft', v)}
			/>
			<span class="field-label">Right</span>
			<ScrubInput
				value={dve?.cropRight ?? 0}
				step={1}
				min={0}
				suffix="px"
				onScrub={(v) => handleScrub('cropRight', v)}
				onCommit={(v) => handleCommit('cropRight', v)}
			/>
			<span class="field-label">Softness</span>
			<ScrubInput
				value={dve?.cropSoftness ?? 0}
				step={0.1}
				min={0}
				precision={1}
				onCommit={(v) => handleCommit('cropSoftness', v)}
			/>
		</div>
	</InspectorSection>

	<!-- Section 6: Shadow -->
	<InspectorSection title="Shadow" bind:expanded={shadowExpanded}>
		<div class="field-grid">
			<span class="field-label">Enable</span>
			<label class="toggle-inline">
				<input
					type="checkbox"
					checked={dve?.shadow?.enabled ?? false}
					onchange={(e) => handleToggle('shadow.enabled', (e.target as HTMLInputElement).checked)}
				/>
			</label>
			<span class="field-label">Offset X</span>
			<ScrubInput
				value={dve?.shadow?.offsetX ?? 0}
				step={1}
				suffix="px"
				disabled={!(dve?.shadow?.enabled)}
				onCommit={(v) => handleCommit('shadow.offsetX', v)}
			/>
			<span class="field-label">Offset Y</span>
			<ScrubInput
				value={dve?.shadow?.offsetY ?? 0}
				step={1}
				suffix="px"
				disabled={!(dve?.shadow?.enabled)}
				onCommit={(v) => handleCommit('shadow.offsetY', v)}
			/>
			<span class="field-label">Blur</span>
			<ScrubInput
				value={dve?.shadow?.blur ?? 0}
				step={1}
				min={0}
				disabled={!(dve?.shadow?.enabled)}
				onCommit={(v) => handleCommit('shadow.blur', v)}
			/>
			<span class="field-label">Opacity</span>
			<ScrubInput
				value={dve?.shadow?.opacity ?? 0.5}
				step={0.01}
				min={0}
				max={1}
				precision={2}
				disabled={!(dve?.shadow?.enabled)}
				onCommit={(v) => handleCommit('shadow.opacity', v)}
			/>
		</div>
	</InspectorSection>
</div>

<style>
	.slot-inspector {
		display: flex;
		flex-direction: column;
		overflow-y: auto;
		overflow-x: hidden;
		min-height: 0;
	}

	/* Source & Scale section — always visible */
	.source-section {
		padding: 6px 8px;
		border-bottom: 1px solid var(--border-subtle);
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.field-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.inspector-select {
		flex: 1;
		min-width: 0;
		padding: 2px 4px;
		background: var(--bg-control, #242429);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs, 0.5rem);
		cursor: pointer;
		outline: none;
	}

	.inspector-select:focus {
		border-color: var(--border-strong, #555);
	}

	/* Crop anchor grid */
	.anchor-section {
		display: flex;
		align-items: flex-start;
		gap: 6px;
	}

	.anchor-grid {
		display: grid;
		grid-template-columns: repeat(3, 14px);
		grid-template-rows: repeat(3, 14px);
		gap: 2px;
	}

	.anchor-btn {
		width: 14px;
		height: 14px;
		padding: 0;
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		background: var(--bg-control, #242429);
		cursor: pointer;
	}

	.anchor-btn:hover {
		border-color: var(--border-strong, #555);
	}

	.anchor-btn.active {
		background: var(--accent-gold, #d4a017);
		border-color: var(--accent-gold, #d4a017);
	}

	/* Field grid (2-column label+input) */
	.field-grid {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 3px 8px;
		align-items: center;
		padding: 4px 8px;
	}

	.field-label {
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		text-align: right;
		padding-right: 2px;
		user-select: none;
		white-space: nowrap;
	}

	/* Toggle checkboxes inline row */
	.toggle-row {
		display: flex;
		gap: 12px;
		padding: 4px 8px 6px;
	}

	.toggle-label {
		display: flex;
		align-items: center;
		gap: 4px;
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-secondary);
		cursor: pointer;
		user-select: none;
	}

	.toggle-label input[type='checkbox'] {
		width: 12px;
		height: 12px;
		margin: 0;
		accent-color: var(--accent-gold, #d4a017);
		cursor: pointer;
	}

	/* Shadow enable toggle inline in field-grid */
	.toggle-inline {
		display: flex;
		align-items: center;
		cursor: pointer;
	}

	.toggle-inline input[type='checkbox'] {
		width: 12px;
		height: 12px;
		margin: 0;
		accent-color: var(--accent-gold, #d4a017);
		cursor: pointer;
	}
</style>
