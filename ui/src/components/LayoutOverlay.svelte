<script lang="ts">
	import type { ControlRoomState, LayoutSlotState } from '$lib/api/types';
	import { updateLayoutSlot, apiCall } from '$lib/api/switch-api';
	import { throttle } from '$lib/util/throttle';

	interface Props {
		state: ControlRoomState;
		containerWidth: number;
		containerHeight: number;
	}

	let { state: crState, containerWidth, containerHeight }: Props = $props();

	let slots = $derived(crState.layout?.slots ?? []);
	let format = $derived(crState.pipelineFormat);
	let frameW = $derived(format?.width ?? 1920);
	let frameH = $derived(format?.height ?? 1080);

	// Scale from frame coords to container coords
	let scaleX = $derived(containerWidth / frameW);
	let scaleY = $derived(containerHeight / frameH);

	// Dragging state
	let dragging = $state<{ slotId: number; type: 'move' | 'resize'; startX: number; startY: number; origX: number; origY: number; origW: number; origH: number } | null>(null);

	const SNAP_GRID = 0.05; // 5% snap

	function snapToGrid(val: number, total: number): number {
		const step = total * SNAP_GRID;
		return Math.round(val / step) * step;
	}

	function evenAlign(v: number): number {
		return v & ~1;
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
			let newX = evenAlign(Math.round(snapToGrid(dragging.origX + dx, frameW)));
			let newY = evenAlign(Math.round(snapToGrid(dragging.origY + dy, frameH)));
			newX = Math.max(0, Math.min(frameW - dragging.origW, newX));
			newY = Math.max(0, Math.min(frameH - dragging.origH, newY));
			throttledUpdate(dragging.slotId, { x: newX, y: newY });
		} else {
			let newW = evenAlign(Math.round(snapToGrid(dragging.origW + dx, frameW)));
			let newH = evenAlign(Math.round(snapToGrid(dragging.origH + dy, frameH)));
			newW = Math.max(64, Math.min(frameW - dragging.origX, newW));
			newH = Math.max(36, Math.min(frameH - dragging.origY, newH));
			throttledUpdate(dragging.slotId, { width: newW, height: newH });
		}
	}

	function handlePointerUp() {
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
			{@const left = slot.x * scaleX}
			{@const top = slot.y * scaleY}
			{@const width = slot.width * scaleX}
			{@const height = slot.height * scaleY}
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="slot-outline"
				class:animating={slot.animating}
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
		z-index: 5;
	}

	.slot-outline {
		position: absolute;
		border: 2px solid rgba(212, 160, 23, 0.8);
		border-radius: 2px;
		cursor: move;
		box-sizing: border-box;
		transition: border-color 0.15s;
	}

	.slot-outline:hover {
		border-color: rgba(212, 160, 23, 1);
		background: rgba(212, 160, 23, 0.05);
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
		font-size: 0.55rem;
		color: rgba(212, 160, 23, 0.9);
		background: rgba(0, 0, 0, 0.6);
		padding: 1px 4px;
		border-radius: 2px;
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
		border-radius: 2px;
		cursor: nwse-resize;
	}

	.resize-handle:hover {
		background: rgba(212, 160, 23, 1);
		transform: scale(1.2);
	}
</style>
