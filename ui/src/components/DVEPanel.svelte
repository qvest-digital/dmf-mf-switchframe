<script lang="ts">
	import type { ControlRoomState, LayoutSlotState, DVESlotState } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import {
		DVE_PARAM_POSITION_X,
		DVE_PARAM_POSITION_Y,
		DVE_PARAM_WIDTH,
		DVE_PARAM_HEIGHT,
		DVE_PARAM_ROTATION_DEG,
		DVE_PARAM_PERSPECTIVE_X,
		DVE_PARAM_PERSPECTIVE_Y,
		DVE_PARAM_CROP_TOP,
		DVE_PARAM_CROP_BOTTOM,
		DVE_PARAM_CROP_LEFT,
		DVE_PARAM_CROP_RIGHT,
		DVE_PARAM_OPACITY,
		DVE_PARAM_DEFOCUS_RADIUS,
		DVE_PARAM_BORDER_WIDTH,
		DVE_PARAM_CORNER_RADIUS,
	} from '$lib/transport/fast-control';
	import SlotEffectsEditor from './dve/SlotEffectsEditor.svelte';
	import {
		updateDVESlot,
		dveSlotOn,
		dveSlotOff,
		setDVELayout,
		deleteDVELayout,
		saveDVEPreset,
		recallDVEPreset,
		deleteDVEPreset,
		apiCall,
	} from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
		fastControl?: FastControl | null;
	}
	let { state: crState, fastControl = null }: Props = $props();

	let selectedSlotId = $state<number | null>(null);
	let expandedSections = $state<Record<string, boolean>>({
		position: true,
		transform: false,
		crop: false,
		border: false,
		shadow: false,
		effects: false,
	});

	// Drag state for numeric inputs
	let dragging = $state<{
		field: string;
		startX: number;
		startValue: number;
		step: number;
		min?: number;
		max?: number;
	} | null>(null);

	let slots = $derived(crState.layout?.slots ?? []);
	let sourceKeys = $derived(Object.keys(crState.sources).sort());
	let selectedSlot = $derived(
		selectedSlotId == null ? null : slots.find((s) => s.id === selectedSlotId) ?? null,
	);
	let selectedDve = $derived(selectedSlot?.dve ?? null);

	// Deselect if slot disappears
	$effect(() => {
		const ids = new Set(slots.map((s) => s.id));
		if (selectedSlotId != null && !ids.has(selectedSlotId)) {
			selectedSlotId = null;
		}
	});

	// Auto-select first slot if none selected
	$effect(() => {
		if (selectedSlotId == null && slots.length > 0) {
			selectedSlotId = slots[0].id;
		}
	});

	// ── Fast-control parameter mapping ──
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

	function sendFastParam(field: string, value: number) {
		if (selectedSlotId == null) return;
		const paramId = paramIdMap[field];
		if (paramId !== undefined && fastControl) {
			fastControl.sendDVESingleParam(selectedSlotId, paramId, value);
		}
	}

	// ── REST API updates ──
	function updateSlotField(field: string, value: number | boolean) {
		if (selectedSlotId == null || !selectedDve) return;
		// Build a full transform from current state + the changed field,
		// then send as {"transform": {...}} for the server's merge logic.
		const cur = selectedDve;
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
		};

		// Apply the changed field (supports dot-separated like "border.width")
		const parts = field.split('.');
		if (parts.length === 2) {
			const section = parts[0] as 'border' | 'shadow';
			const existing = (cur as unknown as Record<string, unknown>)[section] as Record<string, unknown> | undefined;
			xf[section] = { ...existing, [parts[1]]: value };
		} else {
			xf[field] = value;
		}

		apiCall(updateDVESlot(selectedSlotId, { transform: xf }), 'DVE update');
	}

	function toggleSlotEnabled(slot: LayoutSlotState) {
		if (slot.enabled) {
			apiCall(dveSlotOff(slot.id), 'DVE slot off');
		} else {
			apiCall(dveSlotOn(slot.id), 'DVE slot on');
		}
	}

	// ── Drag-to-adjust for numeric inputs ──
	// Uses a threshold so clicks still focus the input for typing.
	let pendingDrag = $state<{
		field: string;
		startX: number;
		startValue: number;
		step: number;
		min?: number;
		max?: number;
		target: HTMLElement;
		pointerId: number;
	} | null>(null);

	const DRAG_THRESHOLD = 3; // px before drag activates

	function startDrag(
		e: PointerEvent,
		field: string,
		currentValue: number,
		step: number,
		min?: number,
		max?: number,
	) {
		if (e.button !== 0) return;
		// Don't capture yet — wait for threshold
		pendingDrag = {
			field, startX: e.clientX, startValue: currentValue,
			step, min, max,
			target: e.target as HTMLElement,
			pointerId: e.pointerId,
		};
	}

	function handlePointerMove(e: PointerEvent) {
		// Check if pending drag should activate
		if (pendingDrag && !dragging) {
			const dx = Math.abs(e.clientX - pendingDrag.startX);
			if (dx >= DRAG_THRESHOLD) {
				// Activate drag — capture pointer, prevent input focus
				pendingDrag.target.setPointerCapture(pendingDrag.pointerId);
				dragging = {
					field: pendingDrag.field,
					startX: pendingDrag.startX,
					startValue: pendingDrag.startValue,
					step: pendingDrag.step,
					min: pendingDrag.min,
					max: pendingDrag.max,
				};
				pendingDrag = null;
			}
			return;
		}

		if (!dragging) return;
		const deltaX = e.clientX - dragging.startX;
		const speed = e.shiftKey ? 0.1 : 1;
		let newValue = dragging.startValue + deltaX * dragging.step * speed;
		if (dragging.min !== undefined) newValue = Math.max(dragging.min, newValue);
		if (dragging.max !== undefined) newValue = Math.min(dragging.max, newValue);
		newValue = Math.round(newValue * 100) / 100;
		sendFastParam(dragging.field, newValue);
	}

	function handlePointerUp() {
		pendingDrag = null;
		if (!dragging) return;
		dragging = null;
	}

	function handleInputChange(field: string, e: Event, step: number, min?: number, max?: number) {
		const target = e.target as HTMLInputElement;
		let val = parseFloat(target.value);
		if (isNaN(val)) return;
		if (min !== undefined) val = Math.max(min, val);
		if (max !== undefined) val = Math.min(max, val);
		val = Math.round(val / step) * step;
		val = Math.round(val * 1000) / 1000;
		updateSlotField(field, val);
	}

	function handleSliderChange(field: string, e: Event) {
		const target = e.target as HTMLInputElement;
		const val = parseFloat(target.value);
		if (isNaN(val)) return;
		updateSlotField(field, val);
	}

	function handleSliderInput(field: string, e: Event) {
		const target = e.target as HTMLInputElement;
		const val = parseFloat(target.value);
		if (isNaN(val)) return;
		sendFastParam(field, val);
	}

	function handleCheckbox(field: string, e: Event) {
		const target = e.target as HTMLInputElement;
		updateSlotField(field, target.checked);
	}

	function toggleSection(name: string) {
		expandedSections = { ...expandedSections, [name]: !expandedSections[name] };
	}

	// ── Anchor grid ──
	type AnchorPoint = [number, number];
	const anchorPoints: { label: string; value: AnchorPoint }[] = [
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

	function isActiveAnchor(dve: DVESlotState | null, ax: number, ay: number): boolean {
		if (!dve) return ax === 0.5 && ay === 0.5;
		return (dve.anchorX ?? 0.5) === ax && (dve.anchorY ?? 0.5) === ay;
	}

	function setAnchor(ax: number, ay: number) {
		if (selectedSlotId == null) return;
		apiCall(
			updateDVESlot(selectedSlotId, { anchorX: ax, anchorY: ay }),
			'DVE anchor',
		);
	}

	// ── Border color helpers ──
	function handleColorInput(component: 'colorY' | 'colorCb' | 'colorCr', e: Event) {
		const target = e.target as HTMLInputElement;
		const val = parseInt(target.value);
		if (isNaN(val)) return;
		const clamped = Math.max(0, Math.min(255, val));
		updateSlotField(`border.${component}`, clamped);
	}

	// ── Reset helpers ──
	function resetPosition() {
		if (selectedSlotId == null) return;
		apiCall(
			updateDVESlot(selectedSlotId, {
				positionX: 0,
				positionY: 0,
				width: 1920,
				height: 1080,
				anchorX: 0.5,
				anchorY: 0.5,
			}),
			'DVE reset position',
		);
	}

	function resetTransform() {
		if (selectedSlotId == null) return;
		apiCall(
			updateDVESlot(selectedSlotId, {
				rotationDeg: 0,
				perspectiveX: 0,
				perspectiveY: 0,
				opacity: 1,
			}),
			'DVE reset transform',
		);
	}

	function resetCrop() {
		if (selectedSlotId == null) return;
		apiCall(
			updateDVESlot(selectedSlotId, {
				cropTop: 0,
				cropBottom: 0,
				cropLeft: 0,
				cropRight: 0,
				cropSoftness: 0,
			}),
			'DVE reset crop',
		);
	}

	function resetBorder() {
		if (selectedSlotId == null) return;
		apiCall(
			updateDVESlot(selectedSlotId, {
				border: { width: 0, colorY: 235, colorCb: 128, colorCr: 128, cornerRadius: 0, softness: 0 },
			}),
			'DVE reset border',
		);
	}

	function resetShadow() {
		if (selectedSlotId == null) return;
		apiCall(
			updateDVESlot(selectedSlotId, {
				shadow: { enabled: false, offsetX: 4, offsetY: 4, blur: 8, opacity: 0.5 },
			}),
			'DVE reset shadow',
		);
	}

	function resetEffects() {
		if (selectedSlotId == null) return;
		apiCall(
			updateDVESlot(selectedSlotId, { defocusRadius: 0 }),
			'DVE reset effects',
		);
	}

	// ── Preset management ──
	const BUILTIN_PRESETS = [
		{ id: 'pip-top-right', label: 'PIP TR' },
		{ id: 'pip-top-left', label: 'PIP TL' },
		{ id: 'pip-bottom-right', label: 'PIP BR' },
		{ id: 'pip-bottom-left', label: 'PIP BL' },
		{ id: 'side-by-side', label: 'Side' },
		{ id: 'quad', label: 'Quad' },
		{ id: 'three-box', label: '3-Box' },
		{ id: 'over-shoulder', label: 'OTS' },
	];

	let showSaveInput = $state(false);
	let savePresetName = $state('');
	let deleteConfirmId = $state<string | null>(null);

	let activePreset = $derived(crState.layout?.activePreset ?? '');
	let userPresets = $derived(
		(crState.layout?.dvePresets ?? []).filter((p) => !p.builtin),
	);

	function activateBuiltinPreset(presetId: string) {
		apiCall(setDVELayout({ preset: presetId }), 'Activate preset');
	}

	function recallUserPreset(presetId: string) {
		apiCall(recallDVEPreset(presetId, 300, 'ease-in-out'), 'Recall preset');
	}

	function handleSavePreset() {
		const name = savePresetName.trim();
		if (!name) return;
		apiCall(saveDVEPreset(name), 'Save preset');
		savePresetName = '';
		showSaveInput = false;
	}

	function handleSaveKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') handleSavePreset();
		if (e.key === 'Escape') { showSaveInput = false; savePresetName = ''; }
	}

	function confirmDeletePreset(presetId: string) {
		if (deleteConfirmId === presetId) {
			apiCall(deleteDVEPreset(presetId), 'Delete preset');
			deleteConfirmId = null;
		} else {
			deleteConfirmId = presetId;
			setTimeout(() => { deleteConfirmId = null; }, 3000);
		}
	}

	function clearLayout() {
		apiCall(deleteDVELayout(), 'Clear layout');
	}

	// ── Display helpers ──
	function fmtNum(v: number | undefined, fallback: number): string {
		return (v ?? fallback).toFixed(1);
	}

	function selectSlotCard(slotId: number, e: MouseEvent) {
		const t = e.target as HTMLElement;
		if (t.closest('button')) return;
		selectedSlotId = slotId;
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="dve-panel" onpointermove={handlePointerMove} onpointerup={handlePointerUp}>
	<!-- Preset strip -->
	<div class="preset-strip">
		<span class="preset-label">DVE</span>
		<div class="preset-buttons">
			{#each BUILTIN_PRESETS as preset (preset.id)}
				<button
					class="preset-btn"
					class:active={activePreset === preset.id}
					onclick={() => activateBuiltinPreset(preset.id)}
					title={preset.id}
				>{preset.label}</button>
			{/each}
			{#each userPresets as preset (preset.id)}
				<button
					class="preset-btn user-preset"
					class:active={activePreset === preset.id}
					onclick={() => recallUserPreset(preset.id)}
					title={preset.name}
				>
					{preset.name}
					<span
						class="preset-delete"
						role="button"
						tabindex="-1"
						onclick={(e: MouseEvent) => { e.stopPropagation(); confirmDeletePreset(preset.id); }}
					>{deleteConfirmId === preset.id ? '?' : '\u00d7'}</span>
				</button>
			{/each}
		</div>
		<div class="preset-actions">
			{#if showSaveInput}
				<input
					class="save-name-input"
					type="text"
					placeholder="Name..."
					bind:value={savePresetName}
					onkeydown={handleSaveKeydown}
					autofocus
				/>
				<button class="preset-action-btn" onclick={handleSavePreset}>OK</button>
				<button class="preset-action-btn" onclick={() => { showSaveInput = false; savePresetName = ''; }}>X</button>
			{:else}
				<button class="preset-action-btn" onclick={() => showSaveInput = true} title="Save current as preset">+ Save</button>
			{/if}
			<button class="preset-action-btn clear-btn" onclick={clearLayout} title="Clear DVE layout">Clear</button>
		</div>
	</div>

	<!-- Slot strip -->
	<div class="slot-strip">
		{#each slots as slot (slot.id)}
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<div
				class="slot-card"
				class:selected={selectedSlotId === slot.id}
				class:enabled={slot.enabled}
				onclick={(e) => selectSlotCard(slot.id, e)}
			>
				<div class="slot-card__header">
					<button
						type="button"
						class="slot-toggle"
						class:on={slot.enabled}
						onclick={(e) => { e.stopPropagation(); toggleSlotEnabled(slot); }}
					>
						{slot.enabled ? 'ON' : 'OFF'}
					</button>
					<span class="slot-card__id">S{slot.id}</span>
				</div>
				<span class="slot-card__source">
					{slot.sourceKey || '(none)'}
				</span>
				<span class="slot-card__size">
					{slot.dve ? `${Math.round(slot.dve.width)}x${Math.round(slot.dve.height)}` : `${slot.width}x${slot.height}`}
				</span>
			</div>
		{/each}
		{#if slots.length === 0}
			<span class="empty-msg">No layout slots. Select a layout preset first.</span>
		{/if}
	</div>

	<!-- Property sections -->
	{#if selectedSlot && selectedDve}
		<div class="properties">
			<!-- Section: Position -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="section-header" onclick={() => toggleSection('position')}>
				<span class="section-arrow">{expandedSections.position ? '\u25BC' : '\u25B6'}</span>
				<span class="section-title">Position</span>
				<button class="reset-btn" type="button" onclick={(e) => { e.stopPropagation(); resetPosition(); }} title="Reset position">
					\u27F2
				</button>
			</div>
			{#if expandedSections.position}
				<div class="section-content">
					<label class="field-label">X</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.positionX, 0)}
						onchange={(e) => handleInputChange('positionX', e, 1)}
						onpointerdown={(e) => startDrag(e, 'positionX', selectedDve!.positionX ?? 0, 1)}
					/>

					<label class="field-label">Y</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.positionY, 0)}
						onchange={(e) => handleInputChange('positionY', e, 1)}
						onpointerdown={(e) => startDrag(e, 'positionY', selectedDve!.positionY ?? 0, 1)}
					/>

					<label class="field-label">Width</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.width, 1920)}
						onchange={(e) => handleInputChange('width', e, 1, 1)}
						onpointerdown={(e) => startDrag(e, 'width', selectedDve!.width ?? 1920, 1, 1)}
					/>

					<label class="field-label">Height</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.height, 1080)}
						onchange={(e) => handleInputChange('height', e, 1, 1)}
						onpointerdown={(e) => startDrag(e, 'height', selectedDve!.height ?? 1080, 1, 1)}
					/>

					<label class="field-label">Anchor</label>
					<div class="anchor-grid">
						{#each anchorPoints as ap}
							<button
								type="button"
								class="anchor-dot"
								class:active={isActiveAnchor(selectedDve, ap.value[0], ap.value[1])}
								onclick={() => setAnchor(ap.value[0], ap.value[1])}
								title={ap.label}
							></button>
						{/each}
					</div>

					<label class="field-label">Source</label>
					<select
						class="dve-select"
						value={selectedSlot.sourceKey}
						onchange={(e) => {
							if (selectedSlotId == null) return;
							apiCall(updateDVESlot(selectedSlotId, { sourceKey: (e.target as HTMLSelectElement).value }), 'DVE source');
						}}
					>
						<option value="">-- none --</option>
						{#each sourceKeys as key}
							<option value={key}>{crState.sources[key]?.label || key}</option>
						{/each}
					</select>
				</div>
			{/if}

			<!-- Section: Transform -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="section-header" onclick={() => toggleSection('transform')}>
				<span class="section-arrow">{expandedSections.transform ? '\u25BC' : '\u25B6'}</span>
				<span class="section-title">Transform</span>
				<button class="reset-btn" type="button" onclick={(e) => { e.stopPropagation(); resetTransform(); }} title="Reset transform">
					\u27F2
				</button>
			</div>
			{#if expandedSections.transform}
				<div class="section-content">
					<label class="field-label">Rotation</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.rotationDeg, 0)}
						onchange={(e) => handleInputChange('rotationDeg', e, 0.5, -360, 360)}
						onpointerdown={(e) => startDrag(e, 'rotationDeg', selectedDve!.rotationDeg ?? 0, 0.5, -360, 360)}
					/>

					<label class="field-label">Tilt X</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.perspectiveX, 0)}
						onchange={(e) => handleInputChange('perspectiveX', e, 0.5, -60, 60)}
						onpointerdown={(e) => startDrag(e, 'perspectiveX', selectedDve!.perspectiveX ?? 0, 0.5, -60, 60)}
					/>

					<label class="field-label">Tilt Y</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.perspectiveY, 0)}
						onchange={(e) => handleInputChange('perspectiveY', e, 0.5, -60, 60)}
						onpointerdown={(e) => startDrag(e, 'perspectiveY', selectedDve!.perspectiveY ?? 0, 0.5, -60, 60)}
					/>

					<label class="field-label">Opacity</label>
					<div class="slider-row">
						<input
							type="range"
							class="dve-slider"
							min="0"
							max="1"
							step="0.01"
							value={selectedDve.opacity ?? 1}
							oninput={(e) => handleSliderInput('opacity', e)}
							onchange={(e) => handleSliderChange('opacity', e)}
						/>
						<span class="slider-value">{((selectedDve.opacity ?? 1) * 100).toFixed(0)}%</span>
					</div>
				</div>
			{/if}

			<!-- Section: Crop -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="section-header" onclick={() => toggleSection('crop')}>
				<span class="section-arrow">{expandedSections.crop ? '\u25BC' : '\u25B6'}</span>
				<span class="section-title">Crop</span>
				<button class="reset-btn" type="button" onclick={(e) => { e.stopPropagation(); resetCrop(); }} title="Reset crop">
					\u27F2
				</button>
			</div>
			{#if expandedSections.crop}
				<div class="section-content">
					<label class="field-label">Top</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.cropTop, 0)}
						onchange={(e) => handleInputChange('cropTop', e, 1, 0)}
						onpointerdown={(e) => startDrag(e, 'cropTop', selectedDve!.cropTop ?? 0, 1, 0)}
					/>

					<label class="field-label">Bottom</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.cropBottom, 0)}
						onchange={(e) => handleInputChange('cropBottom', e, 1, 0)}
						onpointerdown={(e) => startDrag(e, 'cropBottom', selectedDve!.cropBottom ?? 0, 1, 0)}
					/>

					<label class="field-label">Left</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.cropLeft, 0)}
						onchange={(e) => handleInputChange('cropLeft', e, 1, 0)}
						onpointerdown={(e) => startDrag(e, 'cropLeft', selectedDve!.cropLeft ?? 0, 1, 0)}
					/>

					<label class="field-label">Right</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.cropRight, 0)}
						onchange={(e) => handleInputChange('cropRight', e, 1, 0)}
						onpointerdown={(e) => startDrag(e, 'cropRight', selectedDve!.cropRight ?? 0, 1, 0)}
					/>

					<label class="field-label">Softness</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.cropSoftness, 0)}
						onchange={(e) => handleInputChange('cropSoftness', e, 1, 0, 100)}
						onpointerdown={(e) => startDrag(e, 'cropSoftness', selectedDve!.cropSoftness ?? 0, 1, 0, 100)}
					/>
				</div>
			{/if}

			<!-- Section: Border -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="section-header" onclick={() => toggleSection('border')}>
				<span class="section-arrow">{expandedSections.border ? '\u25BC' : '\u25B6'}</span>
				<span class="section-title">Border</span>
				<button class="reset-btn" type="button" onclick={(e) => { e.stopPropagation(); resetBorder(); }} title="Reset border">
					\u27F2
				</button>
			</div>
			{#if expandedSections.border}
				<div class="section-content">
					<label class="field-label">Width</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.border?.width, 0)}
						onchange={(e) => handleInputChange('border.width', e, 1, 0, 100)}
						onpointerdown={(e) => startDrag(e, 'border.width', selectedDve!.border?.width ?? 0, 1, 0, 100)}
					/>

					<label class="field-label">Radius</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.border?.cornerRadius, 0)}
						onchange={(e) => handleInputChange('border.cornerRadius', e, 1, 0, 200)}
						onpointerdown={(e) => startDrag(e, 'border.cornerRadius', selectedDve!.border?.cornerRadius ?? 0, 1, 0, 200)}
					/>

					<label class="field-label">Softness</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.border?.softness, 0)}
						onchange={(e) => handleInputChange('border.softness', e, 1, 0, 50)}
						onpointerdown={(e) => startDrag(e, 'border.softness', selectedDve!.border?.softness ?? 0, 1, 0, 50)}
					/>

					<label class="field-label">Color Y</label>
					<input
						type="number"
						class="dve-input"
						value={selectedDve.border?.colorY ?? 235}
						min="0"
						max="255"
						onchange={(e) => handleColorInput('colorY', e)}
					/>

					<label class="field-label">Color Cb</label>
					<input
						type="number"
						class="dve-input"
						value={selectedDve.border?.colorCb ?? 128}
						min="0"
						max="255"
						onchange={(e) => handleColorInput('colorCb', e)}
					/>

					<label class="field-label">Color Cr</label>
					<input
						type="number"
						class="dve-input"
						value={selectedDve.border?.colorCr ?? 128}
						min="0"
						max="255"
						onchange={(e) => handleColorInput('colorCr', e)}
					/>
				</div>
			{/if}

			<!-- Section: Shadow -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="section-header" onclick={() => toggleSection('shadow')}>
				<span class="section-arrow">{expandedSections.shadow ? '\u25BC' : '\u25B6'}</span>
				<span class="section-title">Shadow</span>
				<button class="reset-btn" type="button" onclick={(e) => { e.stopPropagation(); resetShadow(); }} title="Reset shadow">
					\u27F2
				</button>
			</div>
			{#if expandedSections.shadow}
				<div class="section-content">
					<label class="field-label">Enabled</label>
					<input
						type="checkbox"
						class="dve-checkbox"
						checked={selectedDve.shadow?.enabled ?? false}
						onchange={(e) => handleCheckbox('shadow.enabled', e)}
					/>

					<label class="field-label">Offset X</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.shadow?.offsetX, 4)}
						onchange={(e) => handleInputChange('shadow.offsetX', e, 1, -100, 100)}
						onpointerdown={(e) => startDrag(e, 'shadow.offsetX', selectedDve!.shadow?.offsetX ?? 4, 1, -100, 100)}
					/>

					<label class="field-label">Offset Y</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.shadow?.offsetY, 4)}
						onchange={(e) => handleInputChange('shadow.offsetY', e, 1, -100, 100)}
						onpointerdown={(e) => startDrag(e, 'shadow.offsetY', selectedDve!.shadow?.offsetY ?? 4, 1, -100, 100)}
					/>

					<label class="field-label">Blur</label>
					<input
						type="number"
						class="dve-input"
						value={fmtNum(selectedDve.shadow?.blur, 8)}
						onchange={(e) => handleInputChange('shadow.blur', e, 1, 0, 100)}
						onpointerdown={(e) => startDrag(e, 'shadow.blur', selectedDve!.shadow?.blur ?? 8, 1, 0, 100)}
					/>

					<label class="field-label">Opacity</label>
					<div class="slider-row">
						<input
							type="range"
							class="dve-slider"
							min="0"
							max="1"
							step="0.01"
							value={selectedDve.shadow?.opacity ?? 0.5}
							oninput={(e) => handleSliderInput('shadow.opacity', e)}
							onchange={(e) => handleSliderChange('shadow.opacity', e)}
						/>
						<span class="slider-value">{((selectedDve.shadow?.opacity ?? 0.5) * 100).toFixed(0)}%</span>
					</div>
				</div>
			{/if}

			<!-- Section: Effects (extracted sub-component) -->
			<SlotEffectsEditor
				dve={selectedDve}
				expanded={expandedSections.effects}
				onToggle={() => toggleSection('effects')}
				onReset={resetEffects}
				onInputChange={handleInputChange}
				onDragStart={startDrag}
			/>
		</div>
	{:else if selectedSlotId != null && selectedSlot && !selectedDve}
		<div class="empty-state">
			DVE not active on this slot. Enable the slot to configure DVE properties.
		</div>
	{:else}
		<div class="empty-state">
			Select a slot above to edit DVE properties.
		</div>
	{/if}
</div>

<style>
	.dve-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		font-family: var(--font-ui);
	}

	/* ── Preset strip ── */
	.preset-strip {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 3px 8px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.preset-label {
		font-size: 10px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.preset-buttons {
		display: flex;
		gap: 2px;
		overflow-x: auto;
		flex: 1;
		min-width: 0;
		-webkit-overflow-scrolling: touch;
	}

	.preset-btn {
		padding: 2px 6px;
		font: 10px var(--font-ui);
		border: 1px solid var(--border-subtle);
		border-radius: 3px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		cursor: pointer;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.preset-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.preset-btn.active {
		background: rgba(59, 130, 246, 0.15);
		border-color: rgba(59, 130, 246, 0.4);
		color: var(--text-primary);
	}

	.preset-btn.user-preset {
		border-style: dashed;
		position: relative;
	}

	.preset-delete {
		margin-left: 3px;
		opacity: 0;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 9px;
	}

	.preset-btn.user-preset:hover .preset-delete {
		opacity: 1;
	}

	.preset-actions {
		display: flex;
		gap: 2px;
		flex-shrink: 0;
	}

	.preset-action-btn {
		padding: 2px 5px;
		font: 10px var(--font-ui);
		border: 1px solid var(--border-subtle);
		border-radius: 3px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		cursor: pointer;
		white-space: nowrap;
	}

	.preset-action-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.clear-btn {
		color: var(--text-tertiary);
	}

	.save-name-input {
		width: 80px;
		padding: 1px 4px;
		font: 10px var(--font-mono);
		background: var(--bg-input);
		border: 1px solid var(--accent-gold);
		border-radius: 2px;
		color: var(--text-primary);
	}

	/* ── Slot strip ── */
	.slot-strip {
		display: flex;
		gap: 4px;
		padding: 4px 8px;
		overflow-x: auto;
		flex-shrink: 0;
		border-bottom: 1px solid var(--border-subtle);
		-webkit-overflow-scrolling: touch;
	}

	.slot-card {
		min-width: 80px;
		max-width: 100px;
		padding: 4px 6px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		cursor: pointer;
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
		transition: border-color 0.12s, box-shadow 0.12s;
	}

	.slot-card.selected {
		border-color: rgba(59, 130, 246, 0.55);
		box-shadow: 0 0 0 1px rgba(59, 130, 246, 0.2);
	}

	.slot-card.enabled {
		border-color: rgba(212, 160, 23, 0.4);
	}

	.slot-card.selected.enabled {
		border-color: rgba(59, 130, 246, 0.55);
	}

	.slot-card__header {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.slot-toggle {
		padding: 1px 4px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs, 3px);
		font-family: var(--font-ui);
		font-size: var(--text-2xs, 0.5rem);
		font-weight: 700;
		letter-spacing: 0.04em;
		cursor: pointer;
		background: var(--bg-base);
		color: var(--text-tertiary);
		transition: background 0.12s, color 0.12s, border-color 0.12s;
	}

	.slot-toggle.on {
		background: rgba(212, 160, 23, 0.22);
		color: var(--accent-gold, #d4a017);
		border-color: rgba(212, 160, 23, 0.45);
	}

	.slot-card__id {
		font-size: var(--text-2xs, 0.5rem);
		font-weight: 600;
		color: var(--text-secondary);
	}

	.slot-card__source {
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.slot-card__size {
		font-family: var(--font-mono);
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
	}

	.empty-msg {
		font-size: var(--text-xs, 0.6rem);
		color: var(--text-tertiary);
		padding: 8px;
	}

	/* ── Properties area ── */
	.properties {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.empty-state {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		text-align: center;
		color: var(--text-tertiary);
		font-size: var(--text-xs, 0.6rem);
		padding: 16px;
	}

	/* ── Section headers ── */
	.section-header {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 4px 8px;
		background: var(--bg-surface, #0f0f12);
		border: none;
		border-bottom: 1px solid var(--border-subtle);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-2xs, 0.5rem);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		width: 100%;
	}

	.section-header:hover {
		background: var(--bg-hover, #2c2c32);
	}

	.section-arrow {
		font-size: 8px;
		width: 10px;
		text-align: center;
		color: var(--text-tertiary);
	}

	.section-title {
		flex: 1;
		text-align: left;
	}

	.reset-btn {
		padding: 0 3px;
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 11px;
		line-height: 1;
		border-radius: var(--radius-xs, 3px);
	}

	.reset-btn:hover {
		color: var(--text-primary);
		background: var(--bg-hover, #2c2c32);
	}

	/* ── Section content ── */
	.section-content {
		padding: 6px 8px;
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 3px 8px;
		align-items: center;
	}

	.field-label {
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		text-align: right;
		padding-right: 2px;
		user-select: none;
	}

	/* ── Numeric input ── */
	.dve-input {
		width: 64px;
		padding: 2px 4px;
		background: var(--bg-control, #242429);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs, 0.5rem);
		text-align: right;
		cursor: ew-resize;
		appearance: textfield;
		-moz-appearance: textfield;
	}

	.dve-input::-webkit-inner-spin-button,
	.dve-input::-webkit-outer-spin-button {
		-webkit-appearance: none;
		margin: 0;
	}

	.dve-input:focus {
		cursor: text;
		border-color: var(--border-strong, #555);
		outline: none;
	}

	/* ── Select ── */
	.dve-select {
		width: 100%;
		padding: 2px 4px;
		background: var(--bg-control, #242429);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs, 0.5rem);
	}

	/* ── Checkbox ── */
	.dve-checkbox {
		width: 14px;
		height: 14px;
		accent-color: var(--accent-gold, #d4a017);
		cursor: pointer;
	}

	/* ── Slider row ── */
	.slider-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.dve-slider {
		flex: 1;
		height: 4px;
		accent-color: var(--accent-gold, #d4a017);
		cursor: pointer;
	}

	.slider-value {
		font-family: var(--font-mono);
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
		min-width: 32px;
		text-align: right;
	}

	/* ── Anchor grid ── */
	.anchor-grid {
		display: grid;
		grid-template-columns: repeat(3, 16px);
		gap: 2px;
	}

	.anchor-dot {
		width: 16px;
		height: 16px;
		border-radius: 50%;
		border: 1px solid var(--border-subtle);
		background: transparent;
		cursor: pointer;
		padding: 0;
		transition: background 0.12s, border-color 0.12s;
	}

	.anchor-dot:hover {
		border-color: var(--text-secondary);
	}

	.anchor-dot.active {
		background: var(--accent-gold, #d4a017);
		border-color: var(--accent-gold, #d4a017);
	}
</style>
