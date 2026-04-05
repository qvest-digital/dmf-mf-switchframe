<script lang="ts">
	interface Props {
		easeX: number; // P2.X of cubic bezier [0-1]
		easeY: number; // P2.Y can overshoot (e.g., bounce)
		onChange: (x: number, y: number) => void;
	}

	let { easeX, easeY, onChange }: Props = $props();

	const W = 120;
	const H = 80;
	const PAD = 4; // padding inside SVG for control point overshoot

	// Drawable area within the padded SVG
	const DW = W - PAD * 2;
	const DH = H - PAD * 2;

	const GRID_COLS = 4;
	const GRID_ROWS = 4;

	const presets = [
		{ label: 'Lin', x: 1, y: 1 },
		{ label: 'In', x: 0.55, y: 0 },
		{ label: 'Out', x: 0, y: 1 },
		{ label: 'I/O', x: 0.25, y: 0.75 },
		{ label: 'Snap', x: 0, y: 0 },
	];

	let svgEl = $state<SVGSVGElement | null>(null);
	let isDragging = $state(false);

	// Convert normalized coords to SVG coords (Y inverted)
	function toSvgX(nx: number): number {
		return PAD + nx * DW;
	}
	function toSvgY(ny: number): number {
		return PAD + (1 - ny) * DH;
	}

	// Convert SVG coords back to normalized
	function fromSvgX(sx: number): number {
		return (sx - PAD) / DW;
	}
	function fromSvgY(sy: number): number {
		return 1 - (sy - PAD) / DH;
	}

	// P0 = (0,0), P1 = (0,0), P2 = (easeX, easeY), P3 = (1,1)
	// SVG cubic bezier: M startX,startY C cp1X,cp1Y cp2X,cp2Y endX,endY
	let curvePath = $derived(
		`M ${toSvgX(0)},${toSvgY(0)} C ${toSvgX(0)},${toSvgY(0)} ${toSvgX(easeX)},${toSvgY(easeY)} ${toSvgX(1)},${toSvgY(1)}`,
	);

	// Diagonal reference line (linear easing)
	let diagLine = $derived({
		x1: toSvgX(0),
		y1: toSvgY(0),
		x2: toSvgX(1),
		y2: toSvgY(1),
	});

	// Control point position
	let cpX = $derived(toSvgX(easeX));
	let cpY = $derived(toSvgY(easeY));

	// Handle line from P1=(0,0) to P2
	let handleLine = $derived({
		x1: toSvgX(0),
		y1: toSvgY(0),
		x2: cpX,
		y2: cpY,
	});

	// Grid lines
	let gridLinesH = $derived(
		Array.from({ length: GRID_ROWS - 1 }, (_, i) => {
			const y = PAD + ((i + 1) / GRID_ROWS) * DH;
			return { x1: PAD, y1: y, x2: PAD + DW, y2: y };
		}),
	);
	let gridLinesV = $derived(
		Array.from({ length: GRID_COLS - 1 }, (_, i) => {
			const x = PAD + ((i + 1) / GRID_COLS) * DW;
			return { x1: x, y1: PAD, x2: x, y2: PAD + DH };
		}),
	);

	// Check if a preset is active
	function isPresetActive(p: { x: number; y: number }): boolean {
		return Math.abs(easeX - p.x) < 0.01 && Math.abs(easeY - p.y) < 0.01;
	}

	function getSvgPoint(e: PointerEvent): { nx: number; ny: number } {
		if (!svgEl) return { nx: easeX, ny: easeY };
		const rect = svgEl.getBoundingClientRect();
		const sx = ((e.clientX - rect.left) / rect.width) * W;
		const sy = ((e.clientY - rect.top) / rect.height) * H;
		const nx = Math.max(0, Math.min(1, fromSvgX(sx)));
		const ny = Math.max(-0.5, Math.min(1.5, fromSvgY(sy)));
		return { nx, ny };
	}

	function handlePointerDown(e: PointerEvent) {
		if (e.button !== 0) return;
		e.preventDefault();
		isDragging = true;
		svgEl?.setPointerCapture(e.pointerId);
	}

	function handlePointerMove(e: PointerEvent) {
		if (!isDragging) return;
		const { nx, ny } = getSvgPoint(e);
		onChange(nx, ny);
	}

	function handlePointerUp(e: PointerEvent) {
		if (!isDragging) return;
		isDragging = false;
		try {
			svgEl?.releasePointerCapture(e.pointerId);
		} catch {
			/* noop */
		}
		const { nx, ny } = getSvgPoint(e);
		onChange(nx, ny);
	}

	function selectPreset(p: { x: number; y: number }) {
		onChange(p.x, p.y);
	}
</script>

<div class="easing-editor">
	<svg
		bind:this={svgEl}
		class="curve-svg"
		viewBox="0 0 {W} {H}"
		xmlns="http://www.w3.org/2000/svg"
		onpointermove={handlePointerMove}
		onpointerup={handlePointerUp}
	>
		<!-- Drawable area background -->
		<rect x={PAD} y={PAD} width={DW} height={DH} class="draw-area" />

		<!-- Grid lines -->
		{#each gridLinesH as line}
			<line {...line} class="grid-line" />
		{/each}
		{#each gridLinesV as line}
			<line {...line} class="grid-line" />
		{/each}

		<!-- Diagonal reference (linear) -->
		<line {...diagLine} class="diag-line" />

		<!-- Control handle line from P1 to P2 -->
		<line {...handleLine} class="handle-line" />

		<!-- Bezier curve -->
		<path d={curvePath} class="curve-path" />

		<!-- Control point P2 (draggable) -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<circle
			cx={cpX}
			cy={cpY}
			r={4}
			class="control-point"
			class:dragging={isDragging}
			onpointerdown={handlePointerDown}
		/>
	</svg>

	<div class="preset-row">
		{#each presets as preset}
			<button
				class="preset-btn"
				class:active={isPresetActive(preset)}
				type="button"
				onclick={() => selectPreset(preset)}
			>
				{preset.label}
			</button>
		{/each}
	</div>
</div>

<style>
	.easing-editor {
		display: flex;
		flex-direction: column;
		gap: 4px;
		width: 120px;
	}

	.curve-svg {
		width: 120px;
		height: 80px;
		background: var(--bg-base, #09090b);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		overflow: visible;
	}

	.draw-area {
		fill: none;
		stroke: none;
	}

	.grid-line {
		stroke: var(--border-subtle);
		stroke-width: 0.5;
	}

	.diag-line {
		stroke: var(--border-subtle);
		stroke-width: 0.5;
		stroke-dasharray: 2 2;
	}

	.handle-line {
		stroke: var(--text-tertiary);
		stroke-width: 0.5;
		opacity: 0.5;
	}

	.curve-path {
		fill: none;
		stroke: var(--accent-gold, #d4a017);
		stroke-width: 2;
		stroke-linecap: round;
	}

	.control-point {
		fill: var(--accent-blue, #3b82f6);
		stroke: var(--bg-base, #09090b);
		stroke-width: 1;
		cursor: grab;
	}

	.control-point.dragging {
		cursor: grabbing;
		fill: #60a5fa;
	}

	.preset-row {
		display: flex;
		gap: 2px;
	}

	.preset-btn {
		flex: 1;
		padding: 2px 0;
		background: var(--bg-control, #242429);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 500;
		cursor: pointer;
		user-select: none;
		line-height: 1;
		text-align: center;
	}

	.preset-btn:hover {
		background: var(--bg-hover, #2c2c32);
		color: var(--text-secondary);
	}

	.preset-btn.active {
		background: var(--accent-gold-dim, rgba(212, 160, 23, 0.12));
		border-color: var(--accent-gold, #d4a017);
		color: var(--accent-gold, #d4a017);
	}
</style>
