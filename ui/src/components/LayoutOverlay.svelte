<script lang="ts">
	import type { ControlRoomState, LayoutSlotState } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import { updateLayoutSlot, apiCall } from '$lib/api/switch-api';
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
		type: 'move' | 'resize';
		startX: number;
		startY: number;
		origX: number;
		origY: number;
		origW: number;
		origH: number;
	} | null>(null);

	// Optimistic local overrides — instant visual feedback while drag in progress.
	// Keyed by slot ID. Cleared on pointer-up when server state catches up.
	// Uses plain object (not Map) for reliable Svelte 5 $state reactivity.
	let localOverrides = $state<Record<number, { x: number; y: number; w: number; h: number }>>({});

	const SNAP_GRID = 2; // snap to even-aligned 2px grid (YUV420 minimum)

	function snapEven(val: number): number {
		return Math.round(val / 2) * 2;
	}

	const throttledUpdate = throttle((slotId: number, update: Record<string, unknown>) => {
		apiCall(updateLayoutSlot(slotId, update), 'Update slot');
	}, 50);

	function handlePointerDown(e: PointerEvent, slot: LayoutSlotState, type: 'move' | 'resize') {
		e.preventDefault();
		e.stopPropagation();
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
		dragging = {
			slotId: slot.id,
			type,
			startX: e.clientX,
			startY: e.clientY,
			origX: slot.x,
			origY: slot.y,
			origW: slot.width,
			origH: slot.height,
		};
	}

	function handlePointerMove(e: PointerEvent) {
		if (!dragging) return;
		const dx = (e.clientX - dragging.startX) / scaleX;
		const dy = (e.clientY - dragging.startY) / scaleY;

		if (dragging.type === 'move') {
			let newX = snapEven(dragging.origX + dx);
			let newY = snapEven(dragging.origY + dy);
			newX = Math.max(0, Math.min(frameW - dragging.origW, newX));
			newY = Math.max(0, Math.min(frameH - dragging.origH, newY));

			// Instant local preview
			localOverrides[dragging.slotId] = { x: newX, y: newY, w: dragging.origW, h: dragging.origH };

			if (fastControl) {
				fastControl.sendSlotPosition(dragging.slotId, newX, newY, dragging.origW, dragging.origH);
			} else {
				throttledUpdate(dragging.slotId, { x: newX, y: newY });
			}
		} else {
			let newW = snapEven(dragging.origW + dx);
			let newH = snapEven(dragging.origH + dy);
			newW = Math.max(64, Math.min(frameW - dragging.origX, newW));
			newH = Math.max(36, Math.min(frameH - dragging.origY, newH));

			// Instant local preview
			localOverrides[dragging.slotId] = { x: dragging.origX, y: dragging.origY, w: newW, h: newH };

			if (fastControl) {
				fastControl.sendSlotPosition(dragging.slotId, dragging.origX, dragging.origY, newW, newH);
			} else {
				throttledUpdate(dragging.slotId, { width: newW, height: newH });
			}
		}
	}

	function handlePointerUp() {
		if (dragging) {
			const override = localOverrides[dragging.slotId];
			if (fastControl && override) {
				// Confirm final position via REST for authoritative state
				apiCall(updateLayoutSlot(dragging.slotId, { x: override.x, y: override.y, width: override.w, height: override.h }), 'Confirm slot position');
			}
			delete localOverrides[dragging.slotId];
		}
		dragging = null;
	}

	function slotRect(slot: LayoutSlotState): { x: number; y: number; w: number; h: number } {
		const override = localOverrides[slot.id];
		if (override) return override;
		return { x: slot.x, y: slot.y, w: slot.width, h: slot.height };
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
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="slot-outline"
				class:animating={slot.animating}
				class:dragging={dragging?.slotId === slot.id}
				style="left:{left}px;top:{top}px;width:{width}px;height:{height}px"
				onpointerdown={(e) => handlePointerDown(e, slot, 'move')}
			>
				<span class="slot-label">{slot.id}: {crState.sources[slot.sourceKey]?.label || slot.sourceKey || '—'}</span>
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div
					class="resize-handle"
					onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, slot, 'resize'); }}
				></div>
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

	.slot-outline {
		position: absolute;
		border: 2px solid rgba(212, 160, 23, 0.8);
		border-radius: var(--radius-xs);
		cursor: move;
		box-sizing: border-box;
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

	.resize-handle {
		position: absolute;
		bottom: -3px;
		right: -3px;
		width: 10px;
		height: 10px;
		background: rgba(212, 160, 23, 0.9);
		border-radius: var(--radius-xs);
		cursor: nwse-resize;
	}

	.resize-handle:hover {
		background: rgba(212, 160, 23, 1);
		transform: scale(1.2);
	}
</style>
