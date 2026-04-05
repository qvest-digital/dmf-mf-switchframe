<script lang="ts">
	import type { LayoutSlotState, SourceInfo } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import {
		DVE_PARAM_POSITION_X, DVE_PARAM_POSITION_Y, DVE_PARAM_WIDTH, DVE_PARAM_HEIGHT,
		DVE_PARAM_ROTATION_DEG, DVE_PARAM_PERSPECTIVE_X, DVE_PARAM_PERSPECTIVE_Y,
		DVE_PARAM_CROP_TOP, DVE_PARAM_CROP_BOTTOM, DVE_PARAM_CROP_LEFT, DVE_PARAM_CROP_RIGHT,
		DVE_PARAM_OPACITY, DVE_PARAM_DEFOCUS_RADIUS, DVE_PARAM_BORDER_WIDTH, DVE_PARAM_CORNER_RADIUS,
	} from '$lib/transport/fast-control';
	import ScrubInput from './ScrubInput.svelte';

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

	// ── Slider scrub/commit for range inputs ──
	function handleSliderScrub(field: string, value: number) {
		const paramId = paramIdMap[field];
		if (paramId !== undefined && fastControl) {
			fastControl.sendDVESingleParam(slot.id, paramId, value);
		}
	}

	function handleSliderCommit(field: string, value: number) {
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

	// ── YCbCr to RGB hex for border color swatch ──
	function ycbcrToRgbHex(y: number, cb: number, cr: number): string {
		const yf = (y - 16) * 255 / 219;
		const cbf = (cb - 128) * 255 / 224;
		const crf = (cr - 128) * 255 / 224;
		const r = Math.round(Math.max(0, Math.min(255, yf + 1.5748 * crf)));
		const g = Math.round(Math.max(0, Math.min(255, yf - 0.1873 * cbf - 0.4681 * crf)));
		const b = Math.round(Math.max(0, Math.min(255, yf + 1.8556 * cbf)));
		return `#${r.toString(16).padStart(2, '0')}${g.toString(16).padStart(2, '0')}${b.toString(16).padStart(2, '0')}`;
	}

	// ── Border color presets ──
	const borderColorPresets: { label: string; y: number; cb: number; cr: number }[] = [
		{ label: 'White', y: 235, cb: 128, cr: 128 },
		{ label: 'Black', y: 16, cb: 128, cr: 128 },
		{ label: 'Red', y: 81, cb: 90, cr: 240 },
		{ label: 'Blue', y: 41, cb: 240, cr: 109 },
		{ label: 'Yellow', y: 210, cb: 16, cr: 146 },
	];

	function applyBorderColor(y: number, cb: number, cr: number) {
		const cur = slot.dve;
		if (!cur) return;
		const xf = buildTransformUpdate('border.colorY', y);
		const border = (xf.transform as Record<string, unknown>).border as Record<string, unknown>;
		border.colorCb = cb;
		border.colorCr = cr;
		onUpdate(slot.id, xf);
	}

	// ── Computed slider percentages for CSS fill ──
	let opacityPct = $derived(Math.round((dve?.opacity ?? 1) * 100));
	let satPct = $derived(Math.round(((dve?.saturation ?? 1) / 2) * 100));
	let brtPct = $derived(Math.round(((dve?.brightness ?? 0) + 1) / 2 * 100));
	let conPct = $derived(Math.round(((dve?.contrast ?? 1) / 3) * 100));

	// ── Reset functions ──
	function resetPosition() {
		const cur = slot.dve;
		if (!cur) return;
		const xf: Record<string, unknown> = {
			positionX: 0, positionY: 0, width: 1920, height: 1080,
			rotationDeg: 0,
			perspectiveX: cur.perspectiveX ?? 0,
			perspectiveY: cur.perspectiveY ?? 0,
			opacity: cur.opacity ?? 1,
			cropTop: cur.cropTop ?? 0, cropBottom: cur.cropBottom ?? 0,
			cropLeft: cur.cropLeft ?? 0, cropRight: cur.cropRight ?? 0,
			cropSoftness: cur.cropSoftness ?? 0,
			defocusRadius: cur.defocusRadius ?? 0,
			anchorX: cur.anchorX ?? 0.5, anchorY: cur.anchorY ?? 0.5,
			saturation: cur.saturation ?? 1, brightness: cur.brightness ?? 0, contrast: cur.contrast ?? 1,
		};
		onUpdate(slot.id, { transform: xf });
	}
</script>

<div class="slot-inspector">
	<!-- Row 1: Source / Scale / Anchor / Opacity -->
	<div class="row">
		<div class="row-group">
			<span class="field-label">SRC</span>
			<select
				class="inspector-select source-select"
				value={slot.sourceKey}
				onchange={(e) => onSourceChange(slot.id, (e.target as HTMLSelectElement).value)}
			>
				<option value="">-- none --</option>
				{#each sortedSources as src (src.key)}
					<option value={src.key}>{src.label || src.key}</option>
				{/each}
			</select>
		</div>

		<div class="row-group">
			<span class="field-label">SCALE</span>
			<select
				class="inspector-select"
				value={slot.scaleMode ?? 'stretch'}
				onchange={(e) => onScaleModeChange(slot.id, (e.target as HTMLSelectElement).value)}
			>
				<option value="stretch">Stretch</option>
				<option value="fill">Fill</option>
				<option value="fit">Fit</option>
			</select>
		</div>

		{#if slot.scaleMode === 'fill'}
			<div class="row-group">
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

		<div class="row-group slider-group">
			<span class="field-label">OPACITY</span>
			<input type="range" class="param-slider" min="0" max="100" step="1"
				value={opacityPct}
				style:--pct="{opacityPct}%"
				oninput={(e) => handleSliderScrub('opacity', parseInt((e.target as HTMLInputElement).value) / 100)}
				onchange={(e) => handleSliderCommit('opacity', parseInt((e.target as HTMLInputElement).value) / 100)}
			/>
			<span class="slider-readout">{opacityPct}%</span>
		</div>
	</div>

	<!-- Row 2: Position / Size / Rotation / Z / Reset -->
	<div class="row">
		<div class="row-group">
			<span class="field-label">X</span>
			<ScrubInput value={dve?.positionX ?? slot.x} step={1} suffix="px"
				onScrub={(v) => handleScrub('positionX', v)}
				onCommit={(v) => handleCommit('positionX', v)} />
		</div>
		<div class="row-group">
			<span class="field-label">Y</span>
			<ScrubInput value={dve?.positionY ?? slot.y} step={1} suffix="px"
				onScrub={(v) => handleScrub('positionY', v)}
				onCommit={(v) => handleCommit('positionY', v)} />
		</div>
		<div class="row-group">
			<span class="field-label">W</span>
			<ScrubInput value={dve?.width ?? slot.width} step={1} min={2} suffix="px"
				onScrub={(v) => handleScrub('width', v)}
				onCommit={(v) => handleCommit('width', v)} />
		</div>
		<div class="row-group">
			<span class="field-label">H</span>
			<ScrubInput value={dve?.height ?? slot.height} step={1} min={2} suffix="px"
				onScrub={(v) => handleScrub('height', v)}
				onCommit={(v) => handleCommit('height', v)} />
		</div>
		<div class="row-group">
			<span class="field-label">ROT</span>
			<ScrubInput value={dve?.rotationDeg ?? 0} step={1} min={-360} max={360} suffix={'\u00B0'}
				onScrub={(v) => handleScrub('rotationDeg', v)}
				onCommit={(v) => handleCommit('rotationDeg', v)} />
		</div>
		<div class="row-group">
			<span class="field-label">Z</span>
			<ScrubInput value={slot.zOrder} step={1} min={0}
				onCommit={(v) => onUpdate(slot.id, { zOrder: v })} />
		</div>
		<button class="reset-btn" type="button" onclick={resetPosition} title="Reset position to full frame">Reset Pos</button>
	</div>

	<!-- Row 3: Border (left) + Shadow (right) -->
	<div class="row split-row">
		<div class="split-half">
			<span class="section-label">BORDER</span>
			<div class="inline-controls">
				<span class="field-label">W</span>
				<ScrubInput value={dve?.border?.width ?? 0} step={1} min={0} suffix="px"
					onScrub={(v) => handleScrub('border.width', v)}
					onCommit={(v) => handleCommit('border.width', v)} />
				<span class="field-label">R</span>
				<ScrubInput value={dve?.border?.cornerRadius ?? 0} step={1} min={0}
					onScrub={(v) => handleScrub('border.cornerRadius', v)}
					onCommit={(v) => handleCommit('border.cornerRadius', v)} />
				<span
					class="color-swatch"
					style:background={ycbcrToRgbHex(
						dve?.border?.colorY ?? 235,
						dve?.border?.colorCb ?? 128,
						dve?.border?.colorCr ?? 128,
					)}
				></span>
				{#each borderColorPresets as preset (preset.label)}
					<button
						class="color-dot"
						type="button"
						title={preset.label}
						style:background={ycbcrToRgbHex(preset.y, preset.cb, preset.cr)}
						onclick={() => applyBorderColor(preset.y, preset.cb, preset.cr)}
					></button>
				{/each}
			</div>
		</div>
		<div class="split-divider"></div>
		<div class="split-half">
			<span class="section-label">SHADOW</span>
			<div class="inline-controls">
				<label class="toggle-compact">
					<input type="checkbox"
						checked={dve?.shadow?.enabled ?? false}
						onchange={(e) => handleToggle('shadow.enabled', (e.target as HTMLInputElement).checked)}
					/>
				</label>
				<span class="field-label">X</span>
				<ScrubInput value={dve?.shadow?.offsetX ?? 0} step={1} suffix="px"
					disabled={!(dve?.shadow?.enabled)}
					onCommit={(v) => handleCommit('shadow.offsetX', v)} />
				<span class="field-label">Y</span>
				<ScrubInput value={dve?.shadow?.offsetY ?? 0} step={1} suffix="px"
					disabled={!(dve?.shadow?.enabled)}
					onCommit={(v) => handleCommit('shadow.offsetY', v)} />
				<span class="field-label">B</span>
				<ScrubInput value={dve?.shadow?.blur ?? 0} step={1} min={0}
					disabled={!(dve?.shadow?.enabled)}
					onCommit={(v) => handleCommit('shadow.blur', v)} />
			</div>
		</div>
	</div>

	<!-- Row 4: Crop (left) + Perspective (right) -->
	<div class="row split-row">
		<div class="split-half">
			<span class="section-label">CROP</span>
			<div class="crop-grid">
				<span class="field-label">T</span>
				<ScrubInput value={dve?.cropTop ?? 0} step={1} min={0} suffix="px"
					onScrub={(v) => handleScrub('cropTop', v)}
					onCommit={(v) => handleCommit('cropTop', v)} />
				<span class="field-label">B</span>
				<ScrubInput value={dve?.cropBottom ?? 0} step={1} min={0} suffix="px"
					onScrub={(v) => handleScrub('cropBottom', v)}
					onCommit={(v) => handleCommit('cropBottom', v)} />
				<span class="field-label">L</span>
				<ScrubInput value={dve?.cropLeft ?? 0} step={1} min={0} suffix="px"
					onScrub={(v) => handleScrub('cropLeft', v)}
					onCommit={(v) => handleCommit('cropLeft', v)} />
				<span class="field-label">R</span>
				<ScrubInput value={dve?.cropRight ?? 0} step={1} min={0} suffix="px"
					onScrub={(v) => handleScrub('cropRight', v)}
					onCommit={(v) => handleCommit('cropRight', v)} />
			</div>
			<div class="inline-controls" style="margin-top: 2px;">
				<span class="field-label">SOFT</span>
				<ScrubInput value={dve?.cropSoftness ?? 0} step={0.1} min={0} precision={1}
					onCommit={(v) => handleCommit('cropSoftness', v)} />
			</div>
		</div>
		<div class="split-divider"></div>
		<div class="split-half">
			<span class="section-label">PERSPECTIVE</span>
			<div class="inline-controls">
				<span class="field-label">X</span>
				<ScrubInput value={dve?.perspectiveX ?? 0} step={1} min={-60} max={60} suffix={'\u00B0'}
					onScrub={(v) => handleScrub('perspectiveX', v)}
					onCommit={(v) => handleCommit('perspectiveX', v)} />
				<span class="field-label">Y</span>
				<ScrubInput value={dve?.perspectiveY ?? 0} step={1} min={-60} max={60} suffix={'\u00B0'}
					onScrub={(v) => handleScrub('perspectiveY', v)}
					onCommit={(v) => handleCommit('perspectiveY', v)} />
			</div>
			<div class="inline-controls" style="margin-top: 2px;">
				<span class="field-label">DFCS</span>
				<ScrubInput value={dve?.defocusRadius ?? 0} step={0.5} min={0}
					onScrub={(v) => handleScrub('defocusRadius', v)}
					onCommit={(v) => handleCommit('defocusRadius', v)} />
			</div>
		</div>
	</div>

	<!-- Row 5: Color / Mirror / Mosaic -->
	<div class="row color-row">
		<div class="row-group slider-group">
			<span class="field-label">SAT</span>
			<input type="range" class="param-slider" min="0" max="200" step="1"
				value={Math.round((dve?.saturation ?? 1) * 100)}
				style:--pct="{satPct}%"
				oninput={(e) => handleSliderScrub('saturation', parseInt((e.target as HTMLInputElement).value) / 100)}
				onchange={(e) => handleSliderCommit('saturation', parseInt((e.target as HTMLInputElement).value) / 100)}
			/>
			<span class="slider-readout">{(dve?.saturation ?? 1).toFixed(2)}</span>
		</div>
		<div class="row-group slider-group">
			<span class="field-label">BRT</span>
			<input type="range" class="param-slider" min="-100" max="100" step="1"
				value={Math.round((dve?.brightness ?? 0) * 100)}
				style:--pct="{brtPct}%"
				oninput={(e) => handleSliderScrub('brightness', parseInt((e.target as HTMLInputElement).value) / 100)}
				onchange={(e) => handleSliderCommit('brightness', parseInt((e.target as HTMLInputElement).value) / 100)}
			/>
			<span class="slider-readout">{(dve?.brightness ?? 0).toFixed(2)}</span>
		</div>
		<div class="row-group slider-group">
			<span class="field-label">CON</span>
			<input type="range" class="param-slider" min="0" max="300" step="1"
				value={Math.round((dve?.contrast ?? 1) * 100)}
				style:--pct="{conPct}%"
				oninput={(e) => handleSliderScrub('contrast', parseInt((e.target as HTMLInputElement).value) / 100)}
				onchange={(e) => handleSliderCommit('contrast', parseInt((e.target as HTMLInputElement).value) / 100)}
			/>
			<span class="slider-readout">{(dve?.contrast ?? 1).toFixed(2)}</span>
		</div>
		<label class="toggle-compact">
			<input type="checkbox"
				checked={dve?.mirrorH ?? false}
				onchange={(e) => handleToggle('mirrorH', (e.target as HTMLInputElement).checked)}
			/>
			<span class="toggle-text">H</span>
		</label>
		<label class="toggle-compact">
			<input type="checkbox"
				checked={dve?.mirrorV ?? false}
				onchange={(e) => handleToggle('mirrorV', (e.target as HTMLInputElement).checked)}
			/>
			<span class="toggle-text">V</span>
		</label>
		<div class="row-group">
			<span class="field-label">MOSA</span>
			<ScrubInput value={dve?.mosaicSize ?? 0} step={1} min={0} max={256}
				onCommit={(v) => handleCommit('mosaicSize', v)} />
		</div>
	</div>
</div>

<style>
	.slot-inspector {
		display: flex;
		flex-direction: column;
		overflow-y: auto;
		overflow-x: hidden;
		min-height: 0;
	}

	/* ── Rows ── */
	.row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 5px 8px;
		border-bottom: 1px solid var(--border-subtle);
		flex-wrap: wrap;
	}

	/* ── Row groups: label + control inline ── */
	.row-group {
		display: flex;
		align-items: center;
		gap: 4px;
		min-width: 0;
	}

	/* ── Labels ── */
	.field-label {
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		user-select: none;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.section-label {
		font-size: 9px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-tertiary);
		user-select: none;
		margin-bottom: 3px;
		line-height: 1;
	}

	/* ── Selects ── */
	.inspector-select {
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

	.source-select {
		width: 90px;
	}

	/* ── Anchor grid ── */
	.anchor-grid {
		display: grid;
		grid-template-columns: repeat(3, 12px);
		grid-template-rows: repeat(3, 12px);
		gap: 1px;
	}

	.anchor-btn {
		width: 12px;
		height: 12px;
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

	/* ── Range sliders ── */
	.slider-group {
		flex: 1;
		min-width: 80px;
	}

	.param-slider {
		-webkit-appearance: none;
		appearance: none;
		flex: 1;
		height: 4px;
		border-radius: 2px;
		background: linear-gradient(
			to right,
			var(--accent-gold, #d4a017) 0%,
			var(--accent-gold, #d4a017) var(--pct, 50%),
			var(--bg-control, #242429) var(--pct, 50%)
		);
		outline: none;
		cursor: pointer;
		min-width: 40px;
	}

	.param-slider::-webkit-slider-thumb {
		-webkit-appearance: none;
		appearance: none;
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--text-primary, #e0e0e0);
		border: 1px solid var(--border-subtle);
		cursor: pointer;
	}

	.param-slider::-moz-range-thumb {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--text-primary, #e0e0e0);
		border: 1px solid var(--border-subtle);
		cursor: pointer;
	}

	.param-slider::-webkit-slider-thumb:hover {
		background: var(--accent-gold, #d4a017);
	}

	.param-slider::-moz-range-thumb:hover {
		background: var(--accent-gold, #d4a017);
	}

	.slider-readout {
		font-size: var(--text-2xs, 0.5rem);
		font-family: var(--font-mono);
		color: var(--text-secondary);
		min-width: 28px;
		text-align: right;
		user-select: none;
		flex-shrink: 0;
	}

	/* ── Split rows (50/50) ── */
	.split-row {
		gap: 0;
		padding: 0;
	}

	.split-half {
		flex: 1;
		display: flex;
		flex-direction: column;
		padding: 5px 8px;
		min-width: 0;
	}

	.split-divider {
		width: 1px;
		align-self: stretch;
		background: var(--border-subtle);
		flex-shrink: 0;
	}

	.inline-controls {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: wrap;
	}

	/* ── Crop 2x2 grid ── */
	.crop-grid {
		display: grid;
		grid-template-columns: auto 1fr auto 1fr;
		gap: 2px 4px;
		align-items: center;
	}

	/* ── Color swatch + dots ── */
	.color-swatch {
		display: inline-block;
		width: 14px;
		height: 14px;
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		flex-shrink: 0;
	}

	.color-dot {
		width: 12px;
		height: 12px;
		padding: 0;
		border: 1px solid var(--border-subtle);
		border-radius: 50%;
		cursor: pointer;
		flex-shrink: 0;
	}

	.color-dot:hover {
		border-color: var(--text-secondary);
		transform: scale(1.2);
	}

	/* ── Toggle checkbox ── */
	.toggle-compact {
		display: flex;
		align-items: center;
		gap: 2px;
		cursor: pointer;
		user-select: none;
		flex-shrink: 0;
	}

	.toggle-compact input[type='checkbox'] {
		width: 12px;
		height: 12px;
		margin: 0;
		accent-color: var(--accent-gold, #d4a017);
		cursor: pointer;
	}

	.toggle-text {
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-secondary);
		text-transform: uppercase;
		user-select: none;
	}

	/* ── Reset button ── */
	.reset-btn {
		padding: 1px 6px;
		border: none;
		border-radius: var(--radius-xs, 3px);
		background: transparent;
		color: var(--text-tertiary);
		font-size: 9px;
		font-family: var(--font-ui);
		cursor: pointer;
		line-height: 1.4;
		white-space: nowrap;
		flex-shrink: 0;
		margin-left: auto;
	}

	.reset-btn:hover {
		color: #e53e3e;
		background: rgba(229, 62, 62, 0.1);
	}

	/* ── Color row spacing ── */
	.color-row {
		gap: 6px;
	}
</style>
