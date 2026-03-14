<script lang="ts">
	import type { ControlRoomState, GraphicsLayerState } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import { graphicsSetRect, apiCall } from '$lib/api/switch-api';
	import { throttle } from '$lib/util/throttle';

	interface Props {
		state: ControlRoomState;
		containerWidth: number;
		containerHeight: number;
		fastControl?: FastControl | null;
		onSelect?: (id: number) => void;
	}

	let { state: crState, containerWidth, containerHeight, fastControl = null, onSelect }: Props = $props();

	let layers = $derived((crState.graphics?.layers ?? []).filter(l => l.active));
	// Use the compositor's actual program dimensions (what ProcessYUV receives),
	// not pipelineFormat which may differ from the source frame resolution.
	let gfx = $derived(crState.graphics);
	let format = $derived(crState.pipelineFormat);
	let frameW = $derived(gfx?.programWidth || format?.width || 1920);
	let frameH = $derived(gfx?.programHeight || format?.height || 1080);

	let scaleX = $derived(containerWidth / frameW);
	let scaleY = $derived(containerHeight / frameH);

	type Corner = 'tl' | 'tr' | 'bl' | 'br';

	let dragging = $state<{
		layerId: number;
		type: 'move' | 'resize';
		corner?: Corner;
		startX: number;
		startY: number;
		origX: number;
		origY: number;
		origW: number;
		origH: number;
	} | null>(null);

	let localOverrides = $state<Record<number, { x: number; y: number; w: number; h: number }>>({});

	function snapEven(val: number): number {
		return Math.round(val / 2) * 2;
	}

	const throttledUpdate = throttle((layerId: number, rect: { x: number; y: number; width: number; height: number }) => {
		apiCall(graphicsSetRect(layerId, rect), 'Update layer');
	}, 50);

	function handlePointerDown(e: PointerEvent, layer: GraphicsLayerState, type: 'move' | 'resize', corner?: Corner) {
		e.preventDefault();
		e.stopPropagation();
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
		onSelect?.(layer.id);
		dragging = {
			layerId: layer.id,
			type,
			corner,
			startX: e.clientX,
			startY: e.clientY,
			origX: layer.x,
			origY: layer.y,
			origW: layer.width,
			origH: layer.height,
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

			localOverrides[dragging.layerId] = { x: newX, y: newY, w: dragging.origW, h: dragging.origH };

			if (fastControl) {
				fastControl.sendGraphicsLayerPosition(dragging.layerId, newX, newY, dragging.origW, dragging.origH);
			} else {
				throttledUpdate(dragging.layerId, { x: newX, y: newY, width: dragging.origW, height: dragging.origH });
			}
		} else {
			const c = dragging.corner ?? 'br';
			let newX = dragging.origX;
			let newY = dragging.origY;
			let newW = dragging.origW;
			let newH = dragging.origH;

			// Horizontal: right edge moves for br/tr, left edge moves for bl/tl
			if (c === 'br' || c === 'tr') {
				newW = snapEven(dragging.origW + dx);
				newW = Math.max(32, Math.min(frameW - dragging.origX, newW));
			} else {
				newX = snapEven(dragging.origX + dx);
				newX = Math.max(0, Math.min(dragging.origX + dragging.origW - 32, newX));
				newW = dragging.origX + dragging.origW - newX;
			}

			// Vertical: bottom edge moves for br/bl, top edge moves for tr/tl
			if (c === 'br' || c === 'bl') {
				newH = snapEven(dragging.origH + dy);
				newH = Math.max(32, Math.min(frameH - dragging.origY, newH));
			} else {
				newY = snapEven(dragging.origY + dy);
				newY = Math.max(0, Math.min(dragging.origY + dragging.origH - 32, newY));
				newH = dragging.origY + dragging.origH - newY;
			}

			localOverrides[dragging.layerId] = { x: newX, y: newY, w: newW, h: newH };

			if (fastControl) {
				fastControl.sendGraphicsLayerPosition(dragging.layerId, newX, newY, newW, newH);
			} else {
				throttledUpdate(dragging.layerId, { x: newX, y: newY, width: newW, height: newH });
			}
		}
	}

	function handlePointerUp() {
		if (dragging) {
			const override = localOverrides[dragging.layerId];
			if (fastControl && override) {
				apiCall(graphicsSetRect(dragging.layerId, { x: override.x, y: override.y, width: override.w, height: override.h }), 'Confirm layer position');
			}
			delete localOverrides[dragging.layerId];
		}
		dragging = null;
	}

	function layerRect(layer: GraphicsLayerState): { x: number; y: number; w: number; h: number } {
		const override = localOverrides[layer.id];
		if (override) return override;
		return { x: layer.x, y: layer.y, w: layer.width, h: layer.height };
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="graphics-overlay"
	onpointermove={handlePointerMove}
	onpointerup={handlePointerUp}
	onpointercancel={handlePointerUp}
>
	{#each layers as layer (layer.id)}
		{@const r = layerRect(layer)}
		{@const left = r.x * scaleX}
		{@const top = r.y * scaleY}
		{@const width = r.w * scaleX}
		{@const height = r.h * scaleY}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="layer-outline"
			class:dragging={dragging?.layerId === layer.id}
			style="left:{left}px;top:{top}px;width:{width}px;height:{height}px"
			onpointerdown={(e) => handlePointerDown(e, layer, 'move')}
		>
			<span class="layer-label">L{layer.id}{layer.template ? ` ${layer.template}` : ''}</span>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="resize-handle tl" onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, layer, 'resize', 'tl'); }}></div>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="resize-handle tr" onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, layer, 'resize', 'tr'); }}></div>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="resize-handle bl" onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, layer, 'resize', 'bl'); }}></div>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div class="resize-handle br" onpointerdown={(e) => { e.stopPropagation(); handlePointerDown(e, layer, 'resize', 'br'); }}></div>
		</div>
	{/each}
</div>

<style>
	.graphics-overlay {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		pointer-events: auto;
		z-index: var(--z-above);
	}

	.layer-outline {
		position: absolute;
		border: 2px solid rgba(6, 182, 212, 0.8);
		border-radius: var(--radius-xs);
		cursor: move;
		box-sizing: border-box;
	}

	.layer-outline:hover {
		border-color: rgba(6, 182, 212, 1);
		background: rgba(6, 182, 212, 0.05);
	}

	.layer-outline.dragging {
		border-color: rgba(6, 182, 212, 1);
		background: rgba(6, 182, 212, 0.08);
	}

	.layer-label {
		position: absolute;
		top: 2px;
		left: 4px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: rgba(6, 182, 212, 0.9);
		background: var(--overlay-heavy);
		padding: 1px 4px;
		border-radius: var(--radius-xs);
		pointer-events: none;
		white-space: nowrap;
	}

	.resize-handle {
		position: absolute;
		width: 10px;
		height: 10px;
		background: rgba(6, 182, 212, 0.9);
		border-radius: var(--radius-xs);
	}

	.resize-handle:hover {
		background: rgba(6, 182, 212, 1);
		transform: scale(1.2);
	}

	.resize-handle.tl { top: -3px; left: -3px; cursor: nwse-resize; }
	.resize-handle.tr { top: -3px; right: -3px; cursor: nesw-resize; }
	.resize-handle.bl { bottom: -3px; left: -3px; cursor: nesw-resize; }
	.resize-handle.br { bottom: -3px; right: -3px; cursor: nwse-resize; }
</style>
