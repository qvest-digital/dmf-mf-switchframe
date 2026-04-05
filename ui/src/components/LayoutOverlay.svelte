<script lang="ts">
	import type { ControlRoomState, LayoutSlotState } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import { updateDVESlot, apiCall } from '$lib/api/switch-api';
	import { throttle } from '$lib/util/throttle';

	interface Props {
		state: ControlRoomState;
		containerWidth: number;
		containerHeight: number;
		fastControl?: FastControl | null;
	}

	let { state: crState, containerWidth, containerHeight, fastControl = null }: Props = $props();

	let slots = $derived(crState.layout?.slots ?? []);
	let format = $derived(crState.pipelineFormat);
	let frameW = $derived(format?.width ?? 1920);
	let frameH = $derived(format?.height ?? 1080);

	// Scale from frame coords to container coords
	let scaleX = $derived(containerWidth / frameW);
	let scaleY = $derived(containerHeight / frameH);

	// Dragging state
	let dragging = $state<{
		slotId: number;
		type: 'move' | 'resize' | 'rotate';
		corner?: 'tl' | 'tr' | 'bl' | 'br';
		startX: number;
		startY: number;
		origX: number;
		origY: number;
		origW: number;
		origH: number;
		origRotation: number;
		startAngle?: number;
		// Track axis constraint for shift-move
		constrainAxis?: 'h' | 'v' | null;
	} | null>(null);

	// Optimistic local overrides -- instant visual feedback while drag in progress.
	// Keyed by slot ID. Cleared on pointer-up when server state catches up.
	let localOverrides = $state<Record<number, { x: number; y: number; w: number; h: number; rotation: number; opacity: number }>>({});

	const SNAP_GRID = 2; // snap to even-aligned 2px grid (YUV420 minimum)

	function snapEven(val: number): number {
		return Math.round(val / 2) * 2;
	}

	/** Extract transform from slot, preferring DVE state when available. */
	function getSlotTransform(slot: LayoutSlotState): { x: number; y: number; w: number; h: number; rotation: number; opacity: number } {
		if (slot.dve) {
			return {
				x: slot.dve.positionX,
				y: slot.dve.positionY,
				w: slot.dve.width,
				h: slot.dve.height,
				rotation: slot.dve.rotationDeg ?? 0,
				opacity: slot.dve.opacity ?? 1,
			};
		}
		return { x: slot.x, y: slot.y, w: slot.width, h: slot.height, rotation: 0, opacity: 1 };
	}

	/** Get slot rect including local overrides for instant feedback. */
	function slotRect(slot: LayoutSlotState): { x: number; y: number; w: number; h: number; rotation: number; opacity: number } {
		const override = localOverrides[slot.id];
		if (override) return override;
		return getSlotTransform(slot);
	}

	const throttledUpdate = throttle((slotId: number, update: Record<string, unknown>) => {
		apiCall(updateDVESlot(slotId, update), 'Update slot');
	}, 50);

	function handlePointerDown(e: PointerEvent, slot: LayoutSlotState, type: 'move' | 'resize' | 'rotate', corner?: 'tl' | 'tr' | 'bl' | 'br') {
		e.preventDefault();
		e.stopPropagation();
		(e.target as HTMLElement).setPointerCapture(e.pointerId);

		const transform = getSlotTransform(slot);

		dragging = {
			slotId: slot.id,
			type,
			corner,
			startX: e.clientX,
			startY: e.clientY,
			origX: transform.x,
			origY: transform.y,
			origW: transform.w,
			origH: transform.h,
			origRotation: transform.rotation,
			constrainAxis: null,
		};

		if (type === 'rotate') {
			// Compute starting angle from slot center to pointer
			const overlay = (e.target as HTMLElement).closest('.layout-overlay');
			if (overlay) {
				const rect = overlay.getBoundingClientRect();
				const centerX = (transform.x + transform.w / 2) * scaleX;
				const centerY = (transform.y + transform.h / 2) * scaleY;
				const pointerX = e.clientX - rect.left;
				const pointerY = e.clientY - rect.top;
				dragging.startAngle = Math.atan2(pointerY - centerY, pointerX - centerX) * 180 / Math.PI + 90;
			}
		}
	}

	function handlePointerMove(e: PointerEvent) {
		if (!dragging) return;

		const slot = slots.find(s => s.id === dragging!.slotId);
		if (!slot) return;
		const transform = getSlotTransform(slot);

		if (dragging.type === 'move') {
			handleMoveDrag(e, transform);
		} else if (dragging.type === 'resize') {
			handleResizeDrag(e, transform);
		} else if (dragging.type === 'rotate') {
			handleRotateDrag(e, transform);
		}
	}

	function handleMoveDrag(e: PointerEvent, transform: ReturnType<typeof getSlotTransform>) {
		if (!dragging) return;

		let dx = (e.clientX - dragging.startX) / scaleX;
		let dy = (e.clientY - dragging.startY) / scaleY;

		// Shift: constrain to horizontal or vertical axis
		if (e.shiftKey) {
			if (!dragging.constrainAxis) {
				// Pick axis once displacement exceeds threshold
				if (Math.abs(dx) > 5 || Math.abs(dy) > 5) {
					dragging.constrainAxis = Math.abs(dx) >= Math.abs(dy) ? 'h' : 'v';
				}
			}
			if (dragging.constrainAxis === 'h') dy = 0;
			else if (dragging.constrainAxis === 'v') dx = 0;
		} else {
			dragging.constrainAxis = null;
		}

		let newX = snapEven(dragging.origX + dx);
		let newY = snapEven(dragging.origY + dy);
		newX = Math.max(0, Math.min(frameW - dragging.origW, newX));
		newY = Math.max(0, Math.min(frameH - dragging.origH, newY));

		localOverrides[dragging.slotId] = {
			x: newX, y: newY, w: dragging.origW, h: dragging.origH,
			rotation: dragging.origRotation, opacity: transform.opacity,
		};

		if (fastControl) {
			fastControl.sendDVESlotTransform(
				dragging.slotId, newX, newY, dragging.origW, dragging.origH,
				dragging.origRotation, transform.opacity,
			);
		} else {
			throttledUpdate(dragging.slotId, { x: newX, y: newY });
		}
	}

	function handleResizeDrag(e: PointerEvent, transform: ReturnType<typeof getSlotTransform>) {
		if (!dragging) return;

		const dx = (e.clientX - dragging.startX) / scaleX;
		const dy = (e.clientY - dragging.startY) / scaleY;
		const corner = dragging.corner ?? 'br';

		let newX = dragging.origX;
		let newY = dragging.origY;
		let newW = dragging.origW;
		let newH = dragging.origH;

		// Apply resize based on which corner is being dragged
		if (corner === 'br') {
			newW = dragging.origW + dx;
			newH = dragging.origH + dy;
		} else if (corner === 'bl') {
			newX = dragging.origX + dx;
			newW = dragging.origW - dx;
			newH = dragging.origH + dy;
		} else if (corner === 'tr') {
			newY = dragging.origY + dy;
			newW = dragging.origW + dx;
			newH = dragging.origH - dy;
		} else if (corner === 'tl') {
			newX = dragging.origX + dx;
			newY = dragging.origY + dy;
			newW = dragging.origW - dx;
			newH = dragging.origH - dy;
		}

		// Shift: maintain aspect ratio
		if (e.shiftKey && dragging.origW > 0 && dragging.origH > 0) {
			const aspect = dragging.origW / dragging.origH;
			// Use the larger delta to determine the resize, then derive the other
			if (Math.abs(newW - dragging.origW) >= Math.abs(newH - dragging.origH) * aspect) {
				newH = newW / aspect;
			} else {
				newW = newH * aspect;
			}
			// For tl/bl/tr corners, re-derive position from size
			if (corner === 'tl') {
				newX = dragging.origX + dragging.origW - newW;
				newY = dragging.origY + dragging.origH - newH;
			} else if (corner === 'bl') {
				newX = dragging.origX + dragging.origW - newW;
			} else if (corner === 'tr') {
				newY = dragging.origY + dragging.origH - newH;
			}
		}

		// Clamp minimums
		newW = Math.max(64, newW);
		newH = Math.max(36, newH);

		// Clamp to frame bounds
		newX = Math.max(0, newX);
		newY = Math.max(0, newY);
		newW = Math.min(frameW - newX, newW);
		newH = Math.min(frameH - newY, newH);

		// Snap to even
		newX = snapEven(newX);
		newY = snapEven(newY);
		newW = snapEven(newW);
		newH = snapEven(newH);

		localOverrides[dragging.slotId] = {
			x: newX, y: newY, w: newW, h: newH,
			rotation: dragging.origRotation, opacity: transform.opacity,
		};

		if (fastControl) {
			fastControl.sendDVESlotTransform(
				dragging.slotId, newX, newY, newW, newH,
				dragging.origRotation, transform.opacity,
			);
		} else {
			throttledUpdate(dragging.slotId, { x: newX, y: newY, width: newW, height: newH });
		}
	}

	function handleRotateDrag(e: PointerEvent, transform: ReturnType<typeof getSlotTransform>) {
		if (!dragging || dragging.startAngle === undefined) return;

		const overlay = (e.target as HTMLElement).closest('.layout-overlay');
		if (!overlay) return;
		const rect = overlay.getBoundingClientRect();

		const centerX = (dragging.origX + dragging.origW / 2) * scaleX;
		const centerY = (dragging.origY + dragging.origH / 2) * scaleY;
		const pointerX = e.clientX - rect.left;
		const pointerY = e.clientY - rect.top;

		const currentAngle = Math.atan2(pointerY - centerY, pointerX - centerX) * 180 / Math.PI + 90;
		let rotation = currentAngle - dragging.startAngle + dragging.origRotation;

		// Normalize to -180..180 range
		rotation = ((rotation % 360) + 540) % 360 - 180;

		// Shift: snap to 15 degree increments
		if (e.shiftKey) {
			rotation = Math.round(rotation / 15) * 15;
		}

		localOverrides[dragging.slotId] = {
			x: dragging.origX, y: dragging.origY, w: dragging.origW, h: dragging.origH,
			rotation, opacity: transform.opacity,
		};

		fastControl?.sendDVESlotTransform(
			dragging.slotId, dragging.origX, dragging.origY,
			dragging.origW, dragging.origH, rotation, transform.opacity,
		);
	}

	function handlePointerUp() {
		if (dragging) {
			const override = localOverrides[dragging.slotId];
			const slot = slots.find(s => s.id === dragging!.slotId);

			if (override) {
				// Confirm final transform via REST — send complete DVE transform
				// to preserve border/shadow/etc from current state.
				const curDve = slot?.dve;
				apiCall(updateDVESlot(dragging.slotId, {
					transform: {
						positionX: override.x,
						positionY: override.y,
						width: override.w,
						height: override.h,
						rotationDeg: override.rotation,
						opacity: override.opacity,
						perspectiveX: curDve?.perspectiveX ?? 0,
						perspectiveY: curDve?.perspectiveY ?? 0,
						anchorX: curDve?.anchorX ?? 0.5,
						anchorY: curDve?.anchorY ?? 0.5,
						cropTop: curDve?.cropTop ?? 0,
						cropBottom: curDve?.cropBottom ?? 0,
						cropLeft: curDve?.cropLeft ?? 0,
						cropRight: curDve?.cropRight ?? 0,
						cropSoftness: curDve?.cropSoftness ?? 0,
						defocusRadius: curDve?.defocusRadius ?? 0,
						border: curDve?.border ?? {},
						shadow: curDve?.shadow ?? {},
					},
				}), 'Confirm DVE slot transform');
			}
			delete localOverrides[dragging.slotId];
		}
		dragging = null;
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="layout-overlay"
	onpointermove={handlePointerMove}
	onpointerup={handlePointerUp}
	onpointercancel={handlePointerUp}
>
	{#each slots as slot (slot.id)}
		{#if slot.enabled || slot.animating}
			{@const r = slotRect(slot)}
			{@const left = r.x * scaleX}
			{@const top = r.y * scaleY}
			{@const width = r.w * scaleX}
			{@const height = r.h * scaleY}
			{@const rotation = r.rotation}
			{@const opacity = r.opacity}
			{@const hasDVE = !!slot.dve}
			{@const rotateCSS = rotation !== 0 ? `rotate(${rotation}deg)` : ''}
			<!-- Wrapper for rotation transform — positions the slot and applies rotation around its center -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="slot-transform"
				style="left:{left}px;top:{top}px;width:{width}px;height:{height}px;transform:{rotateCSS};opacity:{opacity}"
			>
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div
					class="slot-outline"
					class:animating={slot.animating}
					class:dragging={dragging?.slotId === slot.id}
					onpointerdown={(e) => handlePointerDown(e, slot, 'move')}
				>
					<span class="slot-label">{slot.id}: {crState.sources[slot.sourceKey]?.label || slot.sourceKey || '\u2014'}</span>

					<!-- Resize handles: all 4 corners for DVE slots, bottom-right only for legacy -->
					{#if hasDVE}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="resize-handle corner-tl"
							onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, slot, 'resize', 'tl'); }}
						></div>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="resize-handle corner-tr"
							onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, slot, 'resize', 'tr'); }}
						></div>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="resize-handle corner-bl"
							onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, slot, 'resize', 'bl'); }}
						></div>
					{/if}
					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<div
						class="resize-handle corner-br"
						onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, slot, 'resize', 'br'); }}
					></div>
				</div>

				<!-- Rotation handle (DVE slots only) -->
				{#if hasDVE}
					<div class="rotation-handle-line"></div>
					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<div
						class="rotation-handle"
						onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, slot, 'rotate'); }}
					></div>
				{/if}
			</div>
		{/if}
	{/each}
</div>

<style>
	.layout-overlay {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		pointer-events: auto;
		z-index: var(--z-above);
	}

	.slot-transform {
		position: absolute;
		transform-origin: center center;
		pointer-events: none;
	}

	.slot-outline {
		position: relative;
		width: 100%;
		height: 100%;
		border: 2px solid rgba(212, 160, 23, 0.8);
		border-radius: var(--radius-xs);
		cursor: move;
		box-sizing: border-box;
		pointer-events: auto;
	}

	.slot-outline:hover {
		border-color: rgba(212, 160, 23, 1);
		background: rgba(212, 160, 23, 0.05);
	}

	.slot-outline.dragging {
		border-color: rgba(212, 160, 23, 1);
		background: rgba(212, 160, 23, 0.08);
	}

	.slot-outline.animating {
		border-color: rgba(59, 130, 246, 0.8);
		border-style: dashed;
	}

	.slot-label {
		position: absolute;
		top: 2px;
		left: 4px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: rgba(212, 160, 23, 0.9);
		background: var(--overlay-heavy);
		padding: 1px 4px;
		border-radius: var(--radius-xs);
		pointer-events: none;
		white-space: nowrap;
	}

	/* Resize handles */
	.resize-handle {
		position: absolute;
		width: 8px;
		height: 8px;
		background: rgba(212, 160, 23, 0.8);
		border: 1px solid rgba(255, 255, 255, 0.5);
		pointer-events: auto;
	}

	.resize-handle:hover {
		transform: scale(1.2);
		background: rgba(212, 160, 23, 1);
	}

	.corner-tl {
		top: -3px;
		left: -3px;
		cursor: nwse-resize;
	}

	.corner-tr {
		top: -3px;
		right: -3px;
		cursor: nesw-resize;
	}

	.corner-bl {
		bottom: -3px;
		left: -3px;
		cursor: nesw-resize;
	}

	.corner-br {
		bottom: -3px;
		right: -3px;
		cursor: nwse-resize;
		border-radius: var(--radius-xs);
	}

	/* Rotation handle and connecting line */
	.rotation-handle-line {
		position: absolute;
		left: calc(50% - 0.5px);
		top: -20px;
		width: 1px;
		height: 20px;
		background: rgba(212, 160, 23, 0.5);
		pointer-events: none;
	}

	.rotation-handle {
		position: absolute;
		left: calc(50% - 6px);
		top: -32px;
		width: 12px;
		height: 12px;
		border-radius: 50%;
		background: rgba(212, 160, 23, 0.9);
		border: 1px solid rgba(255, 255, 255, 0.6);
		cursor: grab;
		z-index: 10;
		pointer-events: auto;
	}

	.rotation-handle:hover {
		transform: scale(1.3);
		background: rgba(212, 160, 23, 1);
	}

	.rotation-handle:active {
		cursor: grabbing;
	}
</style>
