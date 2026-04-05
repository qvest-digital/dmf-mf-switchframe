<script lang="ts">
	interface Props {
		x: number;
		y: number;
		xMin?: number;
		xMax?: number;
		yMin?: number;
		yMax?: number;
		xStep?: number;
		yStep?: number;
		size?: number;
		label?: string;
		onScrub?: (x: number, y: number) => void;
		onCommit?: (x: number, y: number) => void;
	}

	let {
		x,
		y,
		xMin = 0,
		xMax = 1920,
		yMin = 0,
		yMax = 1080,
		xStep = 1,
		yStep = 1,
		size = 64,
		label,
		onScrub,
		onCommit,
	}: Props = $props();

	let padEl = $state<HTMLDivElement | null>(null);
	let isDragging = $state(false);
	let constrainAxis = $state<'x' | 'y' | null>(null);
	let dragOrigin = $state<{ mx: number; my: number; x: number; y: number } | null>(null);

	const CONSTRAIN_THRESHOLD = 6;

	// Map value to pixel position
	function xToPx(val: number): number {
		return ((val - xMin) / (xMax - xMin)) * size;
	}

	function yToPx(val: number): number {
		return ((val - yMin) / (yMax - yMin)) * size;
	}

	// Map pixel position to value
	function pxToX(px: number): number {
		return xMin + (px / size) * (xMax - xMin);
	}

	function pxToY(px: number): number {
		return yMin + (px / size) * (yMax - yMin);
	}

	function clampX(v: number): number {
		return Math.max(xMin, Math.min(xMax, v));
	}

	function clampY(v: number): number {
		return Math.max(yMin, Math.min(yMax, v));
	}

	function roundTo(v: number, s: number): number {
		return Math.round(v / s) * s;
	}

	let dotPx = $derived({ x: xToPx(x), y: yToPx(y) });

	function valuesFromEvent(e: PointerEvent): { vx: number; vy: number } {
		if (!padEl) return { vx: x, vy: y };
		const rect = padEl.getBoundingClientRect();
		let px = e.clientX - rect.left;
		let py = e.clientY - rect.top;
		px = Math.max(0, Math.min(size, px));
		py = Math.max(0, Math.min(size, py));
		let vx = clampX(roundTo(pxToX(px), xStep));
		let vy = clampY(roundTo(pxToY(py), yStep));
		return { vx, vy };
	}

	function handlePointerDown(e: PointerEvent) {
		if (e.button !== 0) return;
		e.preventDefault();
		isDragging = true;
		constrainAxis = null;
		padEl?.setPointerCapture(e.pointerId);

		if (e.shiftKey) {
			dragOrigin = { mx: e.clientX, my: e.clientY, x, y };
		} else {
			dragOrigin = null;
		}

		const { vx, vy } = valuesFromEvent(e);
		if (!e.shiftKey) {
			onScrub?.(vx, vy);
		}
	}

	function handlePointerMove(e: PointerEvent) {
		if (!isDragging) return;

		if (e.shiftKey && dragOrigin && !constrainAxis) {
			const dx = Math.abs(e.clientX - dragOrigin.mx);
			const dy = Math.abs(e.clientY - dragOrigin.my);
			if (dx > CONSTRAIN_THRESHOLD || dy > CONSTRAIN_THRESHOLD) {
				constrainAxis = dx > dy ? 'x' : 'y';
			}
			return;
		}

		const { vx, vy } = valuesFromEvent(e);
		let outX = vx;
		let outY = vy;

		if (constrainAxis === 'x') {
			outY = dragOrigin?.y ?? y;
		} else if (constrainAxis === 'y') {
			outX = dragOrigin?.x ?? x;
		}

		onScrub?.(outX, outY);
	}

	function handlePointerUp(e: PointerEvent) {
		if (!isDragging) return;
		isDragging = false;
		try { padEl?.releasePointerCapture(e.pointerId); } catch { /* noop */ }

		const { vx, vy } = valuesFromEvent(e);
		let outX = vx;
		let outY = vy;

		if (constrainAxis === 'x') {
			outY = dragOrigin?.y ?? y;
		} else if (constrainAxis === 'y') {
			outX = dragOrigin?.x ?? x;
		}

		constrainAxis = null;
		dragOrigin = null;
		onCommit?.(outX, outY);
	}

	function handleDblClick() {
		// Reset to center of range
		const centerX = roundTo((xMin + xMax) / 2, xStep);
		const centerY = roundTo((yMin + yMax) / 2, yStep);
		onScrub?.(centerX, centerY);
		onCommit?.(centerX, centerY);
	}

	let displayX = $derived(Math.round(x));
	let displayY = $derived(Math.round(y));
</script>

<div class="xy-pad-wrap" tabindex="0">
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		bind:this={padEl}
		class="xy-pad"
		class:active={isDragging}
		style:width="{size}px"
		style:height="{size}px"
		onpointerdown={handlePointerDown}
		onpointermove={handlePointerMove}
		onpointerup={handlePointerUp}
		ondblclick={handleDblClick}
	>
		<!-- Grid lines -->
		<svg class="grid" width={size} height={size} viewBox="0 0 {size} {size}">
			<!-- Center crosshair -->
			<line x1={size / 2} y1={0} x2={size / 2} y2={size} class="grid-line" />
			<line x1={0} y1={size / 2} x2={size} y2={size / 2} class="grid-line" />
			<!-- Quarter lines -->
			<line x1={size / 4} y1={0} x2={size / 4} y2={size} class="grid-line-sub" />
			<line x1={size * 3 / 4} y1={0} x2={size * 3 / 4} y2={size} class="grid-line-sub" />
			<line x1={0} y1={size / 4} x2={size} y2={size / 4} class="grid-line-sub" />
			<line x1={0} y1={size * 3 / 4} x2={size} y2={size * 3 / 4} class="grid-line-sub" />
		</svg>

		<!-- Position dot -->
		<div
			class="dot"
			class:active={isDragging}
			style:left="{dotPx.x}px"
			style:top="{dotPx.y}px"
		></div>
	</div>

	{#if label}
		<span class="pad-label">{label}</span>
	{/if}
	<span class="pad-values">{displayX}, {displayY}</span>
</div>

<style>
	.xy-pad-wrap {
		display: inline-flex;
		flex-direction: column;
		align-items: center;
		gap: 2px;
		outline: none;
	}

	.xy-pad-wrap:focus-visible .xy-pad {
		outline: 1px solid var(--accent-gold, #d4a017);
		outline-offset: 1px;
	}

	.xy-pad {
		position: relative;
		background: var(--bg-control, #1e1e22);
		border: 1px solid var(--border-subtle, #2a2a2e);
		border-radius: 3px;
		cursor: crosshair;
		overflow: hidden;
		user-select: none;
		touch-action: none;
	}

	.xy-pad.active {
		border-color: var(--accent-gold, #d4a017);
	}

	.grid {
		position: absolute;
		inset: 0;
		pointer-events: none;
	}

	.grid-line {
		stroke: rgba(255, 255, 255, 0.08);
		stroke-width: 0.5;
	}

	.grid-line-sub {
		stroke: rgba(255, 255, 255, 0.03);
		stroke-width: 0.5;
	}

	.dot {
		position: absolute;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--text-primary, #e0e0e0);
		transform: translate(-50%, -50%);
		pointer-events: none;
		transition: background 0.1s, box-shadow 0.1s;
	}

	.dot.active {
		background: var(--accent-gold, #d4a017);
		box-shadow: 0 0 6px rgba(212, 160, 23, 0.5);
	}

	.pad-label {
		font-size: 8px;
		color: var(--text-tertiary, #666);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		user-select: none;
	}

	.pad-values {
		font-size: 8px;
		font-family: var(--font-mono);
		color: var(--text-tertiary, #666);
		user-select: none;
	}
</style>
