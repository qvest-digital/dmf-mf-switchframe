<script lang="ts">
	interface Props {
		label: string;
		value: [number, number, number];
		onchange: (rgb: [number, number, number]) => void;
		min?: number;
		max?: number;
		size?: number;
	}

	let { label, value, onchange, min = -1, max = 1, size = 80 }: Props = $props();

	// Scale all dimensions proportionally to the size prop
	const SIZE = $derived(size);
	const RADIUS = $derived(Math.round(size * 34 / 80));
	const CENTER = $derived(size / 2);
	const DOT_RADIUS = $derived(Math.max(3, Math.round(size * 5 / 80)));

	let dragging = $state(false);
	let svgEl: SVGSVGElement;

	// Map RGB values to XY position within the wheel.
	// X axis = Red (left=min, right=max), Y axis = Blue (top=max, bottom=min).
	// Green is derived from the complement to keep balance.
	let dotX = $derived.by(() => {
		const mid = (min + max) / 2;
		const range = (max - min) / 2;
		if (range === 0) return CENTER;
		return CENTER + ((value[0] - mid) / range) * RADIUS;
	});

	let dotY = $derived.by(() => {
		const mid = (min + max) / 2;
		const range = (max - min) / 2;
		if (range === 0) return CENTER;
		return CENTER - ((value[2] - mid) / range) * RADIUS;
	});

	// Clamp a value to range
	function clamp(v: number, lo: number, hi: number): number {
		return Math.max(lo, Math.min(hi, v));
	}

	function posToRGB(clientX: number, clientY: number): [number, number, number] {
		const rect = svgEl.getBoundingClientRect();
		const x = clientX - rect.left;
		const y = clientY - rect.top;

		// Offset from center, clamped to circle
		let dx = x - CENTER;
		let dy = y - CENTER;
		const dist = Math.sqrt(dx * dx + dy * dy);
		if (dist > RADIUS) {
			dx = (dx / dist) * RADIUS;
			dy = (dy / dist) * RADIUS;
		}

		const mid = (min + max) / 2;
		const range = (max - min) / 2;

		const r = clamp(mid + (dx / RADIUS) * range, min, max);
		const b = clamp(mid - (dy / RADIUS) * range, min, max);
		// Green: inverse of average of R and B offsets to keep balance
		const rOff = r - mid;
		const bOff = b - mid;
		const g = clamp(mid - (rOff + bOff) / 2, min, max);

		// Round to 3 decimal places
		return [
			Math.round(r * 1000) / 1000,
			Math.round(g * 1000) / 1000,
			Math.round(b * 1000) / 1000,
		];
	}

	function handlePointerDown(e: PointerEvent) {
		dragging = true;
		svgEl.setPointerCapture(e.pointerId);
		onchange(posToRGB(e.clientX, e.clientY));
	}

	function handlePointerMove(e: PointerEvent) {
		if (!dragging) return;
		onchange(posToRGB(e.clientX, e.clientY));
	}

	function handlePointerUp(e: PointerEvent) {
		dragging = false;
		svgEl.releasePointerCapture(e.pointerId);
	}

	function handleDblClick() {
		const mid = (min + max) / 2;
		onchange([
			Math.round(mid * 1000) / 1000,
			Math.round(mid * 1000) / 1000,
			Math.round(mid * 1000) / 1000,
		]);
	}

	// Format a value compactly
	function fmt(v: number): string {
		return v.toFixed(2);
	}

	// Dot fill: tint based on current RGB offset
	let dotColor = $derived.by(() => {
		const mid = (min + max) / 2;
		const range = max - min || 1;
		const r = Math.round(128 + ((value[0] - mid) / range) * 200);
		const g = Math.round(128 + ((value[1] - mid) / range) * 200);
		const b = Math.round(128 + ((value[2] - mid) / range) * 200);
		return `rgb(${clamp(r, 40, 255)}, ${clamp(g, 40, 255)}, ${clamp(b, 40, 255)})`;
	});
</script>

<div class="color-wheel" ondblclick={handleDblClick} role="group" aria-label="Color wheel">
	<svg
		bind:this={svgEl}
		width={SIZE}
		height={SIZE}
		viewBox="0 0 {SIZE} {SIZE}"
		onpointerdown={handlePointerDown}
		onpointermove={handlePointerMove}
		onpointerup={handlePointerUp}
		class="wheel-svg"
		role="application"
		aria-label="Color wheel control"
	>
		<!-- Background circle -->
		<circle cx={CENTER} cy={CENTER} r={RADIUS + 2} fill="none" stroke="#555" stroke-width="1" />
		<circle cx={CENTER} cy={CENTER} r={RADIUS} fill="#1e1e1e" stroke="#666" stroke-width="0.5" />

		<!-- Cross hairs -->
		<line x1={CENTER - RADIUS} y1={CENTER} x2={CENTER + RADIUS} y2={CENTER} stroke="#333" stroke-width="0.5" />
		<line x1={CENTER} y1={CENTER - RADIUS} x2={CENTER} y2={CENTER + RADIUS} stroke="#333" stroke-width="0.5" />

		<!-- Color axis labels (tiny) -->
		<text x={CENTER + RADIUS + 3} y={CENTER + 3} fill="#c44" font-size="7" font-family="sans-serif">R</text>
		<text x={CENTER - RADIUS - 8} y={CENTER + 3} fill="#666" font-size="7" font-family="sans-serif">R</text>
		<text x={CENTER - 2} y={CENTER - RADIUS - 3} fill="#44c" font-size="7" font-family="sans-serif">B</text>
		<text x={CENTER - 2} y={CENTER + RADIUS + 9} fill="#666" font-size="7" font-family="sans-serif">B</text>

		<!-- Draggable dot -->
		<circle
			cx={dotX}
			cy={dotY}
			r={DOT_RADIUS}
			fill={dotColor}
			stroke="#eee"
			stroke-width="1.5"
			class="dot"
		/>
	</svg>
	<span class="wheel-label">{label}</span>
	<span class="wheel-values">
		<span class="val-r">{fmt(value[0])}</span>
		<span class="val-g">{fmt(value[1])}</span>
		<span class="val-b">{fmt(value[2])}</span>
	</span>
</div>

<style>
	.color-wheel {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 2px;
		user-select: none;
	}

	.wheel-svg {
		cursor: crosshair;
		touch-action: none;
	}

	.dot {
		cursor: grab;
		filter: drop-shadow(0 0 2px rgba(255, 255, 255, 0.3));
	}

	.wheel-label {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-secondary, #999);
		text-transform: uppercase;
		letter-spacing: 0.5px;
	}

	.wheel-values {
		display: flex;
		gap: 6px;
		font-size: 9px;
		font-variant-numeric: tabular-nums;
		color: var(--text-secondary, #666);
	}

	.val-r { color: #c66; }
	.val-g { color: #6c6; }
	.val-b { color: #66c; }
</style>
