<script lang="ts">
	interface Props {
		top: number;
		bottom: number;
		left: number;
		right: number;
		frameW?: number;
		frameH?: number;
		size?: number;
		onScrub?: (field: 'cropTop' | 'cropBottom' | 'cropLeft' | 'cropRight', value: number) => void;
		onCommit?: (field: 'cropTop' | 'cropBottom' | 'cropLeft' | 'cropRight', value: number) => void;
	}

	let {
		top,
		bottom,
		left,
		right,
		frameW = 1920,
		frameH = 1080,
		size = 120,
		onScrub,
		onCommit,
	}: Props = $props();

	// Maintain 16:9 aspect ratio
	const aspectRatio = 16 / 9;
	let displayH = $derived(Math.round(size / aspectRatio));

	// Scale factors: frame pixels to display pixels
	let scaleX = $derived(size / frameW);
	let scaleY = $derived(displayH / frameH);

	// Crop regions in display pixels
	let cropTopPx = $derived(top * scaleY);
	let cropBottomPx = $derived(bottom * scaleY);
	let cropLeftPx = $derived(left * scaleX);
	let cropRightPx = $derived(right * scaleX);

	// Inner visible rect in display pixels
	let innerX = $derived(cropLeftPx);
	let innerY = $derived(cropTopPx);
	let innerW = $derived(size - cropLeftPx - cropRightPx);
	let innerH = $derived(displayH - cropTopPx - cropBottomPx);

	type Edge = 'top' | 'bottom' | 'left' | 'right';

	let dragEdge = $state<Edge | null>(null);
	let dragStart = $state<{ pos: number; value: number } | null>(null);
	let hoveredEdge = $state<Edge | null>(null);
	let boxEl = $state<HTMLDivElement | null>(null);

	const HANDLE_SIZE = 6;

	function fieldName(edge: Edge): 'cropTop' | 'cropBottom' | 'cropLeft' | 'cropRight' {
		const map = { top: 'cropTop', bottom: 'cropBottom', left: 'cropLeft', right: 'cropRight' } as const;
		return map[edge];
	}

	function edgeValue(edge: Edge): number {
		const map = { top, bottom, left, right };
		return map[edge];
	}

	function handleEdgePointerDown(edge: Edge, e: PointerEvent) {
		if (e.button !== 0) return;
		e.preventDefault();
		e.stopPropagation();
		dragEdge = edge;
		const isVertical = edge === 'left' || edge === 'right';
		dragStart = {
			pos: isVertical ? e.clientX : e.clientY,
			value: edgeValue(edge),
		};
		(e.target as HTMLElement).setPointerCapture(e.pointerId);
	}

	function handleEdgePointerMove(e: PointerEvent) {
		if (!dragEdge || !dragStart) return;

		const isVertical = dragEdge === 'left' || dragEdge === 'right';
		const currentPos = isVertical ? e.clientX : e.clientY;
		const deltaPx = currentPos - dragStart.pos;
		const fineMode = e.shiftKey;

		let scale: number;
		if (isVertical) {
			scale = frameW / size;
		} else {
			scale = frameH / displayH;
		}

		if (fineMode) scale *= 0.1;

		// Direction: dragging top handle downward increases cropTop, dragging bottom upward increases cropBottom
		let direction: number;
		if (dragEdge === 'top' || dragEdge === 'left') {
			direction = 1;
		} else {
			direction = -1;
		}

		const raw = dragStart.value + deltaPx * scale * direction;
		const clamped = Math.max(0, Math.round(raw));

		onScrub?.(fieldName(dragEdge), clamped);
	}

	function handleEdgePointerUp(e: PointerEvent) {
		if (!dragEdge || !dragStart) return;
		const edge = dragEdge;
		try { (e.target as HTMLElement).releasePointerCapture(e.pointerId); } catch { /* noop */ }

		const isVertical = edge === 'left' || edge === 'right';
		const currentPos = isVertical ? e.clientX : e.clientY;
		const deltaPx = currentPos - dragStart.pos;
		const fineMode = e.shiftKey;

		let scale = isVertical ? frameW / size : frameH / displayH;
		if (fineMode) scale *= 0.1;

		const direction = (edge === 'top' || edge === 'left') ? 1 : -1;
		const raw = dragStart.value + deltaPx * scale * direction;
		const clamped = Math.max(0, Math.round(raw));

		dragEdge = null;
		dragStart = null;
		onCommit?.(fieldName(edge), clamped);
	}

	function formatVal(v: number): string {
		return v > 0 ? String(v) : '';
	}
</script>

<div class="crop-box-wrap" tabindex="0">
	<div
		bind:this={boxEl}
		class="crop-box"
		style:width="{size}px"
		style:height="{displayH}px"
	>
		<!-- Full frame background -->
		<div class="frame-bg"></div>

		<!-- Crop overlays -->
		{#if top > 0}
			<div class="crop-overlay crop-top" style:height="{cropTopPx}px"></div>
		{/if}
		{#if bottom > 0}
			<div class="crop-overlay crop-bottom" style:height="{cropBottomPx}px"></div>
		{/if}
		{#if left > 0}
			<div class="crop-overlay crop-left" style:width="{cropLeftPx}px" style:top="{cropTopPx}px" style:height="{innerH}px"></div>
		{/if}
		{#if right > 0}
			<div class="crop-overlay crop-right" style:width="{cropRightPx}px" style:top="{cropTopPx}px" style:height="{innerH}px"></div>
		{/if}

		<!-- Inner visible rect -->
		<div class="inner-rect" style:left="{innerX}px" style:top="{innerY}px" style:width="{innerW}px" style:height="{innerH}px"></div>

		<!-- Edge handles -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="handle handle-top"
			class:hovered={hoveredEdge === 'top'}
			class:active={dragEdge === 'top'}
			style:top="{cropTopPx - HANDLE_SIZE / 2}px"
			style:left="{cropLeftPx}px"
			style:width="{innerW}px"
			onpointerdown={(e) => handleEdgePointerDown('top', e)}
			onpointermove={handleEdgePointerMove}
			onpointerup={handleEdgePointerUp}
			onpointerenter={() => hoveredEdge = 'top'}
			onpointerleave={() => { if (!dragEdge) hoveredEdge = null; }}
		></div>
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="handle handle-bottom"
			class:hovered={hoveredEdge === 'bottom'}
			class:active={dragEdge === 'bottom'}
			style:top="{displayH - cropBottomPx - HANDLE_SIZE / 2}px"
			style:left="{cropLeftPx}px"
			style:width="{innerW}px"
			onpointerdown={(e) => handleEdgePointerDown('bottom', e)}
			onpointermove={handleEdgePointerMove}
			onpointerup={handleEdgePointerUp}
			onpointerenter={() => hoveredEdge = 'bottom'}
			onpointerleave={() => { if (!dragEdge) hoveredEdge = null; }}
		></div>
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="handle handle-left"
			class:hovered={hoveredEdge === 'left'}
			class:active={dragEdge === 'left'}
			style:left="{cropLeftPx - HANDLE_SIZE / 2}px"
			style:top="{cropTopPx}px"
			style:height="{innerH}px"
			onpointerdown={(e) => handleEdgePointerDown('left', e)}
			onpointermove={handleEdgePointerMove}
			onpointerup={handleEdgePointerUp}
			onpointerenter={() => hoveredEdge = 'left'}
			onpointerleave={() => { if (!dragEdge) hoveredEdge = null; }}
		></div>
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="handle handle-right"
			class:hovered={hoveredEdge === 'right'}
			class:active={dragEdge === 'right'}
			style:left="{size - cropRightPx - HANDLE_SIZE / 2}px"
			style:top="{cropTopPx}px"
			style:height="{innerH}px"
			onpointerdown={(e) => handleEdgePointerDown('right', e)}
			onpointermove={handleEdgePointerMove}
			onpointerup={handleEdgePointerUp}
			onpointerenter={() => hoveredEdge = 'right'}
			onpointerleave={() => { if (!dragEdge) hoveredEdge = null; }}
		></div>

		<!-- Edge value labels -->
		{#if top > 0}
			<span class="edge-label top-label">{formatVal(top)}</span>
		{/if}
		{#if bottom > 0}
			<span class="edge-label bottom-label">{formatVal(bottom)}</span>
		{/if}
		{#if left > 0}
			<span class="edge-label left-label">{formatVal(left)}</span>
		{/if}
		{#if right > 0}
			<span class="edge-label right-label">{formatVal(right)}</span>
		{/if}
	</div>
</div>

<style>
	.crop-box-wrap {
		display: inline-flex;
		flex-direction: column;
		align-items: center;
		outline: none;
	}

	.crop-box-wrap:focus-visible .crop-box {
		outline: 1px solid var(--accent-gold, #d4a017);
		outline-offset: 1px;
	}

	.crop-box {
		position: relative;
		border: 1px solid var(--border-subtle, #2a2a2e);
		border-radius: 2px;
		overflow: hidden;
		user-select: none;
	}

	.frame-bg {
		position: absolute;
		inset: 0;
		background: var(--bg-control, #1e1e22);
	}

	.inner-rect {
		position: absolute;
		background: rgba(255, 255, 255, 0.06);
		border: 0.5px solid rgba(255, 255, 255, 0.12);
		pointer-events: none;
	}

	.crop-overlay {
		position: absolute;
		background: rgba(212, 160, 23, 0.12);
		pointer-events: none;
	}

	.crop-top { top: 0; left: 0; right: 0; }
	.crop-bottom { bottom: 0; left: 0; right: 0; }

	.handle {
		position: absolute;
		z-index: 2;
	}

	.handle-top, .handle-bottom {
		height: 6px;
		cursor: ns-resize;
	}

	.handle-left, .handle-right {
		width: 6px;
		cursor: ew-resize;
	}

	.handle::after {
		content: '';
		position: absolute;
		background: transparent;
		transition: background 0.1s;
	}

	.handle-top::after, .handle-bottom::after {
		left: 0; right: 0;
		height: 1.5px;
		top: 50%;
		transform: translateY(-50%);
	}

	.handle-left::after, .handle-right::after {
		top: 0; bottom: 0;
		width: 1.5px;
		left: 50%;
		transform: translateX(-50%);
	}

	.handle.hovered::after, .handle.active::after {
		background: var(--accent-gold, #d4a017);
	}

	.handle.active::after {
		box-shadow: 0 0 4px rgba(212, 160, 23, 0.4);
	}

	.edge-label {
		position: absolute;
		font-size: 7px;
		font-family: var(--font-mono);
		color: var(--text-tertiary, #666);
		pointer-events: none;
		user-select: none;
		z-index: 1;
	}

	.top-label { top: 2px; left: 50%; transform: translateX(-50%); }
	.bottom-label { bottom: 2px; left: 50%; transform: translateX(-50%); }
	.left-label { left: 2px; top: 50%; transform: translateY(-50%); }
	.right-label { right: 2px; top: 50%; transform: translateY(-50%); }
</style>
