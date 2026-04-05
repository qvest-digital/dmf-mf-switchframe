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
	import RotaryKnob from './RotaryKnob.svelte';
	import XYPad from './XYPad.svelte';
	import CropBox from './CropBox.svelte';
	import ScaleModeSelect from './ScaleModeSelect.svelte';

	interface Props {
		slot: LayoutSlotState;
		sources: Record<string, SourceInfo>;
		fastControl?: FastControl | null;
		onUpdate: (slotId: number, update: Record<string, unknown>) => void;
		onSourceChange: (slotId: number, source: string) => void;
		onScaleModeChange: (slotId: number, mode: string) => void;
		onCropAnchorChange: (slotId: number, anchor: [number, number]) => void;
	}

	let { slot, sources, fastControl, onUpdate, onSourceChange, onScaleModeChange, onCropAnchorChange }: Props = $props();

	let dve = $derived(slot.dve);

	const paramIdMap: Record<string, number> = {
		positionX: DVE_PARAM_POSITION_X, positionY: DVE_PARAM_POSITION_Y,
		width: DVE_PARAM_WIDTH, height: DVE_PARAM_HEIGHT,
		rotationDeg: DVE_PARAM_ROTATION_DEG,
		perspectiveX: DVE_PARAM_PERSPECTIVE_X, perspectiveY: DVE_PARAM_PERSPECTIVE_Y,
		cropTop: DVE_PARAM_CROP_TOP, cropBottom: DVE_PARAM_CROP_BOTTOM,
		cropLeft: DVE_PARAM_CROP_LEFT, cropRight: DVE_PARAM_CROP_RIGHT,
		opacity: DVE_PARAM_OPACITY, defocusRadius: DVE_PARAM_DEFOCUS_RADIUS,
		'border.width': DVE_PARAM_BORDER_WIDTH, 'border.cornerRadius': DVE_PARAM_CORNER_RADIUS,
	};

	function buildTransformUpdate(field: string, value: number | boolean): Record<string, unknown> {
		const cur = slot.dve;
		if (!cur) return {};
		const xf: Record<string, unknown> = {
			positionX: cur.positionX ?? 0, positionY: cur.positionY ?? 0,
			width: cur.width ?? 1920, height: cur.height ?? 1080,
			rotationDeg: cur.rotationDeg ?? 0,
			perspectiveX: cur.perspectiveX ?? 0, perspectiveY: cur.perspectiveY ?? 0,
			opacity: cur.opacity ?? 1,
			cropTop: cur.cropTop ?? 0, cropBottom: cur.cropBottom ?? 0,
			cropLeft: cur.cropLeft ?? 0, cropRight: cur.cropRight ?? 0,
			cropSoftness: cur.cropSoftness ?? 0, defocusRadius: cur.defocusRadius ?? 0,
			anchorX: cur.anchorX ?? 0.5, anchorY: cur.anchorY ?? 0.5,
			saturation: cur.saturation ?? 1, brightness: cur.brightness ?? 0, contrast: cur.contrast ?? 1,
		};
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

	function handleScrub(field: string, value: number) {
		const paramId = paramIdMap[field];
		if (paramId !== undefined && fastControl) fastControl.sendDVESingleParam(slot.id, paramId, value);
	}

	function handleCommit(field: string, value: number) { onUpdate(slot.id, buildTransformUpdate(field, value)); }
	function handleToggle(field: string, value: boolean) { onUpdate(slot.id, buildTransformUpdate(field, value)); }

	function handleSliderScrub(field: string, value: number) {
		const paramId = paramIdMap[field];
		if (paramId !== undefined && fastControl) fastControl.sendDVESingleParam(slot.id, paramId, value);
	}
	function handleSliderCommit(field: string, value: number) { onUpdate(slot.id, buildTransformUpdate(field, value)); }

	let sortedSources = $derived(Object.values(sources).sort((a, b) => (a.position ?? 0) - (b.position ?? 0)));

	const anchorPositions: { label: string; value: [number, number] }[] = [
		{ label: 'TL', value: [0, 0] }, { label: 'T', value: [0.5, 0] }, { label: 'TR', value: [1, 0] },
		{ label: 'L', value: [0, 0.5] }, { label: 'C', value: [0.5, 0.5] }, { label: 'R', value: [1, 0.5] },
		{ label: 'BL', value: [0, 1] }, { label: 'B', value: [0.5, 1] }, { label: 'BR', value: [1, 1] },
	];

	function isActiveAnchor(anchor: [number, number]): boolean {
		const current = slot.cropAnchor ?? [0.5, 0.5];
		return current[0] === anchor[0] && current[1] === anchor[1];
	}

	function ycbcrToRgbHex(y: number, cb: number, cr: number): string {
		const yf = (y - 16) * 255 / 219;
		const cbf = (cb - 128) * 255 / 224;
		const crf = (cr - 128) * 255 / 224;
		const r = Math.round(Math.max(0, Math.min(255, yf + 1.5748 * crf)));
		const g = Math.round(Math.max(0, Math.min(255, yf - 0.1873 * cbf - 0.4681 * crf)));
		const b = Math.round(Math.max(0, Math.min(255, yf + 1.8556 * cbf)));
		return `#${r.toString(16).padStart(2, '0')}${g.toString(16).padStart(2, '0')}${b.toString(16).padStart(2, '0')}`;
	}

	const colorPresets = [
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
		border.colorCb = cb; border.colorCr = cr;
		onUpdate(slot.id, xf);
	}

	let opacityPct = $derived(Math.round((dve?.opacity ?? 1) * 100));

	function resetPosition() {
		const cur = slot.dve; if (!cur) return;
		const xf: Record<string, unknown> = {
			positionX: 0, positionY: 0, width: 1920, height: 1080, rotationDeg: 0,
			perspectiveX: cur.perspectiveX ?? 0, perspectiveY: cur.perspectiveY ?? 0,
			opacity: cur.opacity ?? 1, cropTop: cur.cropTop ?? 0, cropBottom: cur.cropBottom ?? 0,
			cropLeft: cur.cropLeft ?? 0, cropRight: cur.cropRight ?? 0,
			cropSoftness: cur.cropSoftness ?? 0, defocusRadius: cur.defocusRadius ?? 0,
			anchorX: cur.anchorX ?? 0.5, anchorY: cur.anchorY ?? 0.5,
			saturation: cur.saturation ?? 1, brightness: cur.brightness ?? 0, contrast: cur.contrast ?? 1,
		};
		onUpdate(slot.id, { transform: xf });
	}
</script>

<div class="inspector">
	<!-- ═══════════ Card grid ═══════════ -->
	<div class="card-grid">

		<!-- ─── SOURCE & OPACITY ─── -->
		<div class="card">
			<div class="card-head">Source</div>
			<div class="card-body">
				<select class="sel" value={slot.sourceKey}
					onchange={(e) => onSourceChange(slot.id, (e.target as HTMLSelectElement).value)}>
					<option value="">— none —</option>
					{#each sortedSources as src (src.key)}
						<option value={src.key}>{src.label || src.key}</option>
					{/each}
				</select>
				<div class="pair">
					<ScaleModeSelect value={slot.scaleMode ?? 'stretch'}
						onChange={(mode) => onScaleModeChange(slot.id, mode)} />
					{#if slot.scaleMode === 'fill'}
						<div class="anchor-grid">
							{#each anchorPositions as pos (pos.label)}
								<button class="anchor-dot" class:active={isActiveAnchor(pos.value)} type="button"
									title={pos.label} onclick={() => onCropAnchorChange(slot.id, pos.value)}></button>
							{/each}
						</div>
					{/if}
				</div>
				<div class="slider-row">
					<span class="lbl">Opacity</span>
					<input type="range" class="slider" min="0" max="100" step="1" value={opacityPct}
						style:--fill="{opacityPct}%"
						oninput={(e) => handleSliderScrub('opacity', parseInt((e.target as HTMLInputElement).value) / 100)}
						onchange={(e) => handleSliderCommit('opacity', parseInt((e.target as HTMLInputElement).value) / 100)} />
					<span class="readout">{opacityPct}%</span>
				</div>
			</div>
		</div>

		<!-- ─── POSITION ─── -->
		<div class="card">
			<div class="card-head">Position<button class="reset" onclick={resetPosition}>Reset</button></div>
			<div class="card-body">
				<div class="pos-top-row">
					<XYPad
						x={dve?.positionX ?? slot.x}
						y={dve?.positionY ?? slot.y}
						xMin={0} xMax={1920}
						yMin={0} yMax={1080}
						size={48}
						label="X/Y"
						onScrub={(vx, vy) => { handleScrub('positionX', vx); handleScrub('positionY', vy); }}
						onCommit={(vx, vy) => { handleCommit('positionX', vx); handleCommit('positionY', vy); }}
					/>
					<div class="field-2col field-2col--compact">
						<span class="lbl">W</span><ScrubInput value={dve?.width ?? slot.width} step={1} min={2} suffix="px" onScrub={(v) => handleScrub('width', v)} onCommit={(v) => handleCommit('width', v)} />
						<span class="lbl">H</span><ScrubInput value={dve?.height ?? slot.height} step={1} min={2} suffix="px" onScrub={(v) => handleScrub('height', v)} onCommit={(v) => handleCommit('height', v)} />
					</div>
				</div>
				<div class="rot-z-row">
					<div class="rot-knob-group">
						<span class="lbl">Rot</span>
						<RotaryKnob
							value={dve?.rotationDeg ?? 0}
							min={-360} max={360}
							size={34}
							onScrub={(v) => handleScrub('rotationDeg', v)}
							onCommit={(v) => handleCommit('rotationDeg', v)}
						/>
					</div>
					<div class="field-2col field-2col--compact">
						<span class="lbl">Z</span><ScrubInput value={slot.zOrder} step={1} min={0} onCommit={(v) => onUpdate(slot.id, { zOrder: v })} />
					</div>
				</div>
			</div>
		</div>

		<!-- ─── BORDER ─── -->
		<div class="card">
			<div class="card-head">Border</div>
			<div class="card-body">
				<div class="field-2col">
					<span class="lbl">W</span><ScrubInput value={dve?.border?.width ?? 0} step={1} min={0} onScrub={(v) => handleScrub('border.width', v)} onCommit={(v) => handleCommit('border.width', v)} />
					<span class="lbl">R</span><ScrubInput value={dve?.border?.cornerRadius ?? 0} step={1} min={0} onScrub={(v) => handleScrub('border.cornerRadius', v)} onCommit={(v) => handleCommit('border.cornerRadius', v)} />
				</div>
				<div class="color-row">
					<span class="swatch" style:background={ycbcrToRgbHex(dve?.border?.colorY ?? 235, dve?.border?.colorCb ?? 128, dve?.border?.colorCr ?? 128)}></span>
					{#each colorPresets as c (c.label)}
						<button class="cdot" type="button" title={c.label}
							style:background={ycbcrToRgbHex(c.y, c.cb, c.cr)}
							onclick={() => applyBorderColor(c.y, c.cb, c.cr)}></button>
					{/each}
				</div>
			</div>
		</div>

		<!-- ─── SHADOW ─── -->
		<div class="card" class:dim={!(dve?.shadow?.enabled)}>
			<div class="card-head">
				Shadow
				<label class="chk"><input type="checkbox" checked={dve?.shadow?.enabled ?? false}
					onchange={(e) => handleToggle('shadow.enabled', (e.target as HTMLInputElement).checked)} /></label>
			</div>
			<div class="card-body">
				<div class="field-2col">
					<span class="lbl">X</span><ScrubInput value={dve?.shadow?.offsetX ?? 4} step={1} suffix="px" disabled={!(dve?.shadow?.enabled)} onCommit={(v) => handleCommit('shadow.offsetX', v)} />
					<span class="lbl">Y</span><ScrubInput value={dve?.shadow?.offsetY ?? 4} step={1} suffix="px" disabled={!(dve?.shadow?.enabled)} onCommit={(v) => handleCommit('shadow.offsetY', v)} />
					<span class="lbl">Blur</span><ScrubInput value={dve?.shadow?.blur ?? 8} step={1} min={0} disabled={!(dve?.shadow?.enabled)} onCommit={(v) => handleCommit('shadow.blur', v)} />
					<span class="lbl">Op</span><ScrubInput value={dve?.shadow?.opacity ?? 0.5} step={0.01} min={0} max={1} precision={2} disabled={!(dve?.shadow?.enabled)} onCommit={(v) => handleCommit('shadow.opacity', v)} />
				</div>
			</div>
		</div>

		<!-- ─── CROP ─── -->
		<div class="card">
			<div class="card-head">Crop</div>
			<div class="card-body">
				<div class="crop-center">
					<CropBox
						top={dve?.cropTop ?? 0}
						bottom={dve?.cropBottom ?? 0}
						left={dve?.cropLeft ?? 0}
						right={dve?.cropRight ?? 0}
						size={80}
						onScrub={(field, v) => handleScrub(field, v)}
						onCommit={(field, v) => handleCommit(field, v)}
					/>
				</div>
				<div class="field-2col">
					<span class="lbl">Soft</span><ScrubInput value={dve?.cropSoftness ?? 0} step={0.1} min={0} precision={1} onCommit={(v) => handleCommit('cropSoftness', v)} />
				</div>
			</div>
		</div>

		<!-- ─── PERSPECTIVE ─── -->
		<div class="card">
			<div class="card-head">Perspective</div>
			<div class="card-body">
				<div class="persp-top-row">
					<XYPad
						x={dve?.perspectiveX ?? 0}
						y={dve?.perspectiveY ?? 0}
						xMin={-60} xMax={60}
						yMin={-60} yMax={60}
						size={48}
						label="Tilt"
						onScrub={(vx, vy) => { handleScrub('perspectiveX', vx); handleScrub('perspectiveY', vy); }}
						onCommit={(vx, vy) => { handleCommit('perspectiveX', vx); handleCommit('perspectiveY', vy); }}
					/>
					<div class="field-2col field-2col--compact">
						<span class="lbl">Dfcs</span><ScrubInput value={dve?.defocusRadius ?? 0} step={0.5} min={0} onScrub={(v) => handleScrub('defocusRadius', v)} onCommit={(v) => handleCommit('defocusRadius', v)} />
					</div>
				</div>
			</div>
		</div>

		<!-- ─── COLOR ─── -->
		<div class="card">
			<div class="card-head">Color</div>
			<div class="card-body">
				<div class="slider-row">
					<span class="lbl">Sat</span>
					<input type="range" class="slider" min="0" max="200" step="1" value={Math.round((dve?.saturation ?? 1) * 100)}
						style:--fill="{Math.round(((dve?.saturation ?? 1) / 2) * 100)}%"
						oninput={(e) => handleSliderScrub('saturation', parseInt((e.target as HTMLInputElement).value) / 100)}
						onchange={(e) => handleSliderCommit('saturation', parseInt((e.target as HTMLInputElement).value) / 100)} />
					<span class="readout">{(dve?.saturation ?? 1).toFixed(2)}</span>
				</div>
				<div class="slider-row">
					<span class="lbl">Brt</span>
					<input type="range" class="slider" min="-100" max="100" step="1" value={Math.round((dve?.brightness ?? 0) * 100)}
						style:--fill="{Math.round(((dve?.brightness ?? 0) + 1) / 2 * 100)}%"
						oninput={(e) => handleSliderScrub('brightness', parseInt((e.target as HTMLInputElement).value) / 100)}
						onchange={(e) => handleSliderCommit('brightness', parseInt((e.target as HTMLInputElement).value) / 100)} />
					<span class="readout">{(dve?.brightness ?? 0).toFixed(2)}</span>
				</div>
				<div class="slider-row">
					<span class="lbl">Con</span>
					<input type="range" class="slider" min="0" max="300" step="1" value={Math.round((dve?.contrast ?? 1) * 100)}
						style:--fill="{Math.round(((dve?.contrast ?? 1) / 3) * 100)}%"
						oninput={(e) => handleSliderScrub('contrast', parseInt((e.target as HTMLInputElement).value) / 100)}
						onchange={(e) => handleSliderCommit('contrast', parseInt((e.target as HTMLInputElement).value) / 100)} />
					<span class="readout">{(dve?.contrast ?? 1).toFixed(2)}</span>
				</div>
			</div>
		</div>

		<!-- ─── EFFECTS ─── -->
		<div class="card">
			<div class="card-head">Effects</div>
			<div class="card-body">
				<div class="field-2col">
					<span class="lbl">Mosa</span><ScrubInput value={dve?.mosaicSize ?? 0} step={1} min={0} max={256} onCommit={(v) => handleCommit('mosaicSize', v)} />
				</div>
				<div class="toggle-row">
					<label class="chk"><input type="checkbox" checked={dve?.mirrorH ?? false}
						onchange={(e) => handleToggle('mirrorH', (e.target as HTMLInputElement).checked)} /><span>Mirror H</span></label>
					<label class="chk"><input type="checkbox" checked={dve?.mirrorV ?? false}
						onchange={(e) => handleToggle('mirrorV', (e.target as HTMLInputElement).checked)} /><span>Mirror V</span></label>
				</div>
			</div>
		</div>

	</div>
</div>

<style>
	.inspector {
		overflow-y: auto;
		overflow-x: hidden;
		min-height: 0;
		padding: 6px;
	}

	/* ═══ Grid of cards ═══ */
	.card-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(155px, 1fr));
		gap: 4px;
	}

	/* ═══ Card ═══ */
	.card {
		background: rgba(255, 255, 255, 0.025);
		border: 1px solid var(--border-subtle, #2a2a2e);
		border-radius: 5px;
		overflow: hidden;
	}

	.card.dim .card-body { opacity: 0.35; pointer-events: none; }

	.card-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 3px 7px;
		font-size: 9px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-tertiary, #666);
		border-bottom: 1px solid var(--border-subtle, #2a2a2e);
		background: rgba(255, 255, 255, 0.015);
		user-select: none;
	}

	.card-body {
		padding: 4px 6px 5px;
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	/* ═══ Field 2-col grid ═══ */
	.field-2col {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 2px 5px;
		align-items: center;
	}

	.field-2col--compact {
		flex: 1;
		min-width: 0;
	}

	/* ═══ Position card layout ═══ */
	.pos-top-row {
		display: flex;
		gap: 8px;
		align-items: flex-start;
	}

	.rot-z-row {
		display: flex;
		gap: 8px;
		align-items: center;
	}

	.rot-knob-group {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 1px;
	}

	/* ═══ Perspective card layout ═══ */
	.persp-top-row {
		display: flex;
		gap: 8px;
		align-items: flex-start;
	}

	/* ═══ Crop card layout ═══ */
	.crop-center {
		display: flex;
		justify-content: center;
	}

	/* ═══ Labels ═══ */
	.lbl {
		font-size: 9px;
		color: var(--text-tertiary, #666);
		text-transform: uppercase;
		letter-spacing: 0.03em;
		user-select: none;
		white-space: nowrap;
		text-align: right;
	}

	/* ═══ Selects ═══ */
	.sel {
		width: 100%;
		padding: 3px 4px;
		background: var(--bg-control, #1e1e22);
		border: 1px solid var(--border-subtle, #2a2a2e);
		border-radius: 3px;
		color: var(--text-primary, #e0e0e0);
		font-family: var(--font-ui);
		font-size: 10px;
		cursor: pointer;
		outline: none;
	}
	.sel:focus { border-color: var(--border-strong, #555); }

	/* ═══ Pair (scale + anchor inline) ═══ */
	.pair {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	/* ═══ Anchor grid ═══ */
	.anchor-grid {
		display: grid;
		grid-template-columns: repeat(3, 11px);
		grid-template-rows: repeat(3, 11px);
		gap: 1px;
	}
	.anchor-dot {
		width: 11px; height: 11px; padding: 0;
		border: 1px solid var(--border-subtle, #2a2a2e);
		border-radius: 2px;
		background: var(--bg-control, #1e1e22);
		cursor: pointer;
		transition: background 0.1s, border-color 0.1s;
	}
	.anchor-dot:hover { border-color: var(--text-tertiary, #666); }
	.anchor-dot.active { background: var(--accent-gold, #d4a017); border-color: var(--accent-gold, #d4a017); }

	/* ═══ Range slider ═══ */
	.slider-row {
		display: flex;
		align-items: center;
		gap: 5px;
	}

	.slider {
		-webkit-appearance: none;
		appearance: none;
		flex: 1;
		height: 6px;
		border-radius: 3px;
		background: linear-gradient(to right,
			var(--accent-gold, #d4a017) 0%,
			var(--accent-gold, #d4a017) var(--fill, 50%),
			var(--bg-control, #1e1e22) var(--fill, 50%));
		outline: none;
		cursor: pointer;
	}
	.slider::-webkit-slider-thumb {
		-webkit-appearance: none;
		width: 12px; height: 12px;
		border-radius: 50%;
		background: var(--text-primary, #e0e0e0);
		border: 2px solid var(--bg-control, #1e1e22);
		cursor: pointer;
		box-shadow: 0 1px 3px rgba(0,0,0,0.4);
		transition: background 0.15s;
	}
	.slider::-webkit-slider-thumb:hover { background: var(--accent-gold, #d4a017); }
	.slider::-moz-range-thumb {
		width: 12px; height: 12px;
		border-radius: 50%;
		background: var(--text-primary, #e0e0e0);
		border: 2px solid var(--bg-control, #1e1e22);
		cursor: pointer;
		box-shadow: 0 1px 3px rgba(0,0,0,0.4);
	}
	.slider::-moz-range-thumb:hover { background: var(--accent-gold, #d4a017); }

	.readout {
		font-size: 9px;
		font-family: var(--font-mono);
		color: var(--text-secondary, #999);
		min-width: 32px;
		text-align: right;
		user-select: none;
		flex-shrink: 0;
		white-space: nowrap;
	}

	/* ═══ Color swatch & dots ═══ */
	.color-row {
		display: flex;
		align-items: center;
		gap: 3px;
		padding-top: 2px;
	}
	.swatch {
		width: 16px; height: 16px;
		border: 1px solid var(--border-subtle, #2a2a2e);
		border-radius: 3px;
		flex-shrink: 0;
	}
	.cdot {
		width: 14px; height: 14px;
		padding: 0;
		border: 1.5px solid rgba(255,255,255,0.1);
		border-radius: 50%;
		cursor: pointer;
		flex-shrink: 0;
		transition: transform 0.12s, border-color 0.12s;
	}
	.cdot:hover { border-color: rgba(255,255,255,0.4); transform: scale(1.15); }

	/* ═══ Checkboxes / toggles ═══ */
	.chk {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		font-size: 9px;
		color: var(--text-secondary, #999);
		cursor: pointer;
		user-select: none;
	}
	.chk input[type='checkbox'] {
		width: 12px; height: 12px; margin: 0;
		accent-color: var(--accent-gold, #d4a017);
		cursor: pointer;
	}

	.toggle-row {
		display: flex;
		gap: 10px;
		padding-top: 2px;
	}

	/* ═══ Reset button ═══ */
	.reset {
		padding: 0 5px;
		border: none;
		background: transparent;
		color: var(--text-tertiary, #666);
		font-size: 8px;
		font-family: var(--font-ui);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		cursor: pointer;
		transition: color 0.15s;
	}
	.reset:hover { color: #e53e3e; }
</style>
