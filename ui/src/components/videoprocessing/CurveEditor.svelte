<script lang="ts">
	import type { CurvePoint } from '$lib/api/types';

	interface Props {
		points: CurvePoint[];
		onchange: (points: CurvePoint[]) => void;
		color?: string;
	}

	let { points, onchange, color = '#ccc' }: Props = $props();

	const SIZE = 200;
	const PAD = 4;
	const INNER = SIZE - PAD * 2;
	const GRID_LINES = 4;
	const DOT_RADIUS = 5;
	const MAX_POINTS = 10;

	let draggingIndex = $state<number | null>(null);
	let svgEl: SVGSVGElement;

	// Ensure endpoints always present
	let allPoints = $derived.by(() => {
		const pts = [...points];
		// Sort by x
		pts.sort((a, b) => a.x - b.x);
		return pts;
	});

	// Convert normalized (0-1) to SVG coordinates
	function toSvgX(x: number): number {
		return PAD + x * INNER;
	}
	function toSvgY(y: number): number {
		return PAD + (1 - y) * INNER;
	}
	// Convert SVG coords back to normalized
	function fromSvg(clientX: number, clientY: number): CurvePoint {
		const rect = svgEl.getBoundingClientRect();
		const sx = clientX - rect.left;
		const sy = clientY - rect.top;
		const x = Math.max(0, Math.min(1, (sx - PAD) / INNER));
		const y = Math.max(0, Math.min(1, 1 - (sy - PAD) / INNER));
		return { x: Math.round(x * 1000) / 1000, y: Math.round(y * 1000) / 1000 };
	}

	// Build SVG path through points using monotone cubic interpolation
	let pathD = $derived.by(() => {
		if (allPoints.length < 2) {
			// Just identity line
			return `M ${toSvgX(0)} ${toSvgY(0)} L ${toSvgX(1)} ${toSvgY(1)}`;
		}

		const pts = allPoints;
		let d = `M ${toSvgX(pts[0].x)} ${toSvgY(pts[0].y)}`;

		if (pts.length === 2) {
			d += ` L ${toSvgX(pts[1].x)} ${toSvgY(pts[1].y)}`;
			return d;
		}

		// Monotone cubic Hermite spline (Fritsch-Carlson)
		const n = pts.length;
		const dx: number[] = [];
		const dy: number[] = [];
		const m: number[] = [];

		for (let i = 0; i < n - 1; i++) {
			dx.push(pts[i + 1].x - pts[i].x);
			dy.push(pts[i + 1].y - pts[i].y);
			m.push(dx[i] === 0 ? 0 : dy[i] / dx[i]);
		}

		const tangents: number[] = [m[0]];
		for (let i = 1; i < n - 1; i++) {
			if (m[i - 1] * m[i] <= 0) {
				tangents.push(0);
			} else {
				tangents.push((m[i - 1] + m[i]) / 2);
			}
		}
		tangents.push(m[n - 2]);

		// Adjust tangents for monotonicity
		for (let i = 0; i < n - 1; i++) {
			if (Math.abs(m[i]) < 1e-10) {
				tangents[i] = 0;
				tangents[i + 1] = 0;
			} else {
				const alpha = tangents[i] / m[i];
				const beta = tangents[i + 1] / m[i];
				const s = alpha * alpha + beta * beta;
				if (s > 9) {
					const t = 3 / Math.sqrt(s);
					tangents[i] = t * alpha * m[i];
					tangents[i + 1] = t * beta * m[i];
				}
			}
		}

		// Build cubic bezier segments
		for (let i = 0; i < n - 1; i++) {
			const seg = dx[i] / 3;
			const cp1x = toSvgX(pts[i].x + seg);
			const cp1y = toSvgY(pts[i].y + tangents[i] * seg);
			const cp2x = toSvgX(pts[i + 1].x - seg);
			const cp2y = toSvgY(pts[i + 1].y - tangents[i + 1] * seg);
			d += ` C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${toSvgX(pts[i + 1].x)} ${toSvgY(pts[i + 1].y)}`;
		}

		return d;
	});

	// Identity line path
	let identityD = $derived(`M ${toSvgX(0)} ${toSvgY(0)} L ${toSvgX(1)} ${toSvgY(1)}`);

	function handlePointerDown(e: PointerEvent, index: number) {
		e.stopPropagation();
		// Don't allow dragging endpoints (0,0) and (1,1)
		const pt = allPoints[index];
		if ((pt.x === 0 && pt.y === 0) || (pt.x === 1 && pt.y === 1)) return;

		draggingIndex = index;
		svgEl.setPointerCapture(e.pointerId);
	}

	function handlePointerMove(e: PointerEvent) {
		if (draggingIndex === null) return;
		const pt = fromSvg(e.clientX, e.clientY);
		const newPoints = [...allPoints];
		// Constrain x between neighbors (don't cross other points)
		const prev = draggingIndex > 0 ? newPoints[draggingIndex - 1].x + 0.01 : 0;
		const next = draggingIndex < newPoints.length - 1 ? newPoints[draggingIndex + 1].x - 0.01 : 1;
		pt.x = Math.max(prev, Math.min(next, pt.x));
		newPoints[draggingIndex] = pt;
		onchange(newPoints);
	}

	function handlePointerUp(e: PointerEvent) {
		if (draggingIndex !== null) {
			svgEl.releasePointerCapture(e.pointerId);
			draggingIndex = null;
		}
	}

	function handleSvgClick(e: MouseEvent) {
		if (allPoints.length >= MAX_POINTS) return;
		const pt = fromSvg(e.clientX, e.clientY);
		// Don't add too close to existing points
		const tooClose = allPoints.some(
			(p) => Math.abs(p.x - pt.x) < 0.03 && Math.abs(p.y - pt.y) < 0.03
		);
		if (tooClose) return;
		const newPoints = [...allPoints, pt].sort((a, b) => a.x - b.x);
		onchange(newPoints);
	}

	function handleDblClick(e: MouseEvent, index: number) {
		e.stopPropagation();
		const pt = allPoints[index];
		// Don't remove endpoints
		if ((pt.x === 0 && pt.y === 0) || (pt.x === 1 && pt.y === 1)) return;
		const newPoints = allPoints.filter((_, i) => i !== index);
		onchange(newPoints);
	}
</script>

<svg
	bind:this={svgEl}
	width={SIZE}
	height={SIZE}
	viewBox="0 0 {SIZE} {SIZE}"
	class="curve-editor"
	role="application"
	aria-label="Tone curve editor"
	onclick={handleSvgClick}
	onpointermove={handlePointerMove}
	onpointerup={handlePointerUp}
>
	<!-- Background -->
	<rect x="0" y="0" width={SIZE} height={SIZE} fill="#1a1a1a" rx="3" />

	<!-- Grid lines -->
	{#each Array(GRID_LINES - 1) as _, i}
		{@const pos = PAD + ((i + 1) / GRID_LINES) * INNER}
		<line x1={PAD} y1={pos} x2={PAD + INNER} y2={pos} stroke="#2a2a2a" stroke-width="0.5" />
		<line x1={pos} y1={PAD} x2={pos} y2={PAD + INNER} stroke="#2a2a2a" stroke-width="0.5" />
	{/each}

	<!-- Border -->
	<rect x={PAD} y={PAD} width={INNER} height={INNER} fill="none" stroke="#333" stroke-width="0.5" />

	<!-- Identity line (diagonal) -->
	<path d={identityD} fill="none" stroke="#333" stroke-width="1" stroke-dasharray="3 3" />

	<!-- Curve -->
	<path d={pathD} fill="none" stroke={color} stroke-width="1.5" />

	<!-- Control points -->
	{#each allPoints as pt, i}
		{@const isEndpoint = (pt.x === 0 && pt.y === 0) || (pt.x === 1 && pt.y === 1)}
		<circle
			cx={toSvgX(pt.x)}
			cy={toSvgY(pt.y)}
			r={draggingIndex === i ? DOT_RADIUS + 1 : DOT_RADIUS}
			fill={isEndpoint ? '#555' : color}
			stroke="#eee"
			stroke-width="1"
			class="curve-dot"
			class:endpoint={isEndpoint}
			role="slider"
			aria-label="Curve point {i + 1}"
			aria-valuenow={Math.round(pt.y * 100)}
			onpointerdown={(e) => handlePointerDown(e, i)}
			ondblclick={(e) => handleDblClick(e, i)}
		/>
	{/each}
</svg>

<style>
	.curve-editor {
		cursor: crosshair;
		touch-action: none;
		border-radius: 3px;
	}

	.curve-dot {
		cursor: grab;
	}

	.curve-dot.endpoint {
		cursor: default;
	}

	.curve-dot:hover {
		filter: brightness(1.3);
	}
</style>
