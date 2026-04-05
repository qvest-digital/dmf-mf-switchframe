<script lang="ts">
	interface Props {
		value: number;
		min?: number;
		max?: number;
		step?: number;
		size?: number;
		onScrub?: (value: number) => void;
		onCommit?: (value: number) => void;
	}

	let {
		value,
		min = -360,
		max = 360,
		step = 1,
		size = 48,
		onScrub,
		onCommit,
	}: Props = $props();

	let svgEl = $state<SVGSVGElement | null>(null);
	let isDragging = $state(false);
	let isEditing = $state(false);
	let editText = $state('');
	let inputEl = $state<HTMLInputElement | null>(null);

	const cx = $derived(size / 2);
	const cy = $derived(size / 2);
	const radius = $derived(size / 2 - 3);
	const innerRadius = $derived(radius - 4);

	// Indicator line endpoint from angle in degrees (0 = top, CW positive)
	function indicatorEnd(deg: number): { x: number; y: number } {
		const rad = ((deg - 90) * Math.PI) / 180;
		return {
			x: cx + Math.cos(rad) * innerRadius,
			y: cy + Math.sin(rad) * innerRadius,
		};
	}

	let indicatorPt = $derived(indicatorEnd(value));

	// Tick mark at 0 degrees (top)
	let tickStart = $derived({ x: cx, y: cy - radius + 1 });
	let tickEnd = $derived({ x: cx, y: cy - radius + 5 });

	function clamp(v: number): number {
		return Math.max(min, Math.min(max, v));
	}

	function roundToStep(v: number, s: number): number {
		return Math.round(v / s) * s;
	}

	function angleFromMouse(e: PointerEvent): number {
		if (!svgEl) return 0;
		const rect = svgEl.getBoundingClientRect();
		const mx = e.clientX - rect.left - cx;
		const my = e.clientY - rect.top - cy;
		// atan2 gives angle from positive X axis; offset so 0 = top
		let deg = (Math.atan2(my, mx) * 180) / Math.PI + 90;
		// Normalize to -180..180
		if (deg > 180) deg -= 360;
		return deg;
	}

	function handlePointerDown(e: PointerEvent) {
		if (e.button !== 0 || isEditing) return;
		e.preventDefault();
		isDragging = true;
		svgEl?.setPointerCapture(e.pointerId);

		const deg = angleFromMouse(e);
		const s = e.shiftKey ? 0.1 : step;
		const clamped = clamp(roundToStep(deg, s));
		onScrub?.(clamped);
	}

	function handlePointerMove(e: PointerEvent) {
		if (!isDragging) return;
		const deg = angleFromMouse(e);
		const s = e.shiftKey ? 0.1 : step;
		const clamped = clamp(roundToStep(deg, s));
		onScrub?.(clamped);
	}

	function handlePointerUp(e: PointerEvent) {
		if (!isDragging) return;
		isDragging = false;
		try { svgEl?.releasePointerCapture(e.pointerId); } catch { /* noop */ }
		const deg = angleFromMouse(e);
		const s = e.shiftKey ? 0.1 : step;
		const clamped = clamp(roundToStep(deg, s));
		onCommit?.(clamped);
	}

	function handleWheel(e: WheelEvent) {
		e.preventDefault();
		const dir = e.deltaY > 0 ? -1 : 1;
		const s = e.shiftKey ? 0.1 : step;
		const raw = value + dir * s;
		const clamped = clamp(roundToStep(raw, s));
		onScrub?.(clamped);
		onCommit?.(clamped);
	}

	function startEditing() {
		isEditing = true;
		editText = String(Math.round(value * 10) / 10);
		requestAnimationFrame(() => inputEl?.select());
	}

	function commitEdit() {
		const parsed = parseFloat(editText);
		if (!isNaN(parsed)) {
			const clamped = clamp(roundToStep(parsed, step));
			onCommit?.(clamped);
		}
		isEditing = false;
	}

	function handleEditKeyDown(e: KeyboardEvent) {
		if (e.key === 'Enter') { e.preventDefault(); commitEdit(); }
		else if (e.key === 'Escape') { isEditing = false; }
	}

	function handleEditBlur() {
		if (isEditing) commitEdit();
	}
</script>

<div class="rotary-knob" style:width="{size}px" style:height="{size}px" tabindex="0">
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<svg
		bind:this={svgEl}
		width={size}
		height={size}
		viewBox="0 0 {size} {size}"
		onpointerdown={handlePointerDown}
		onpointermove={handlePointerMove}
		onpointerup={handlePointerUp}
		onwheel={handleWheel}
	>
		<!-- Track ring -->
		<circle cx={cx} cy={cy} r={radius} class="track" />
		<circle cx={cx} cy={cy} r={radius - 1} class="track-inner" />

		<!-- 0-degree tick (top) -->
		<line x1={tickStart.x} y1={tickStart.y} x2={tickEnd.x} y2={tickEnd.y} class="tick" />

		<!-- Indicator line -->
		<line
			x1={cx} y1={cy}
			x2={indicatorPt.x} y2={indicatorPt.y}
			class="indicator"
			class:active={isDragging}
		/>

		<!-- Center dot -->
		<circle cx={cx} cy={cy} r={2} class="center-dot" />
	</svg>

	<!-- Value display -->
	{#if isEditing}
		<input
			bind:this={inputEl}
			type="text"
			class="value-edit"
			style:width="{size - 12}px"
			value={editText}
			oninput={(e) => editText = (e.target as HTMLInputElement).value}
			onkeydown={handleEditKeyDown}
			onblur={handleEditBlur}
		/>
	{:else}
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<span class="value-display" onclick={startEditing} style:font-size="{Math.max(8, size / 5.5)}px">
			{Math.round(value)}°
		</span>
	{/if}
</div>

<style>
	.rotary-knob {
		position: relative;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: grab;
		user-select: none;
		outline: none;
	}

	.rotary-knob:focus-visible {
		outline: 1px solid var(--accent-gold, #d4a017);
		outline-offset: 2px;
		border-radius: 50%;
	}

	.rotary-knob:active { cursor: grabbing; }

	svg { display: block; }

	.track {
		fill: none;
		stroke: var(--border-subtle, #2a2a2e);
		stroke-width: 1.5;
	}

	.track-inner {
		fill: var(--bg-control, #1e1e22);
		stroke: none;
	}

	.tick {
		stroke: var(--text-tertiary, #666);
		stroke-width: 1;
		stroke-linecap: round;
	}

	.indicator {
		stroke: var(--text-secondary, #999);
		stroke-width: 1.5;
		stroke-linecap: round;
		transition: stroke 0.1s;
	}

	.indicator.active {
		stroke: var(--accent-gold, #d4a017);
		filter: drop-shadow(0 0 3px rgba(212, 160, 23, 0.4));
	}

	.center-dot {
		fill: var(--text-tertiary, #666);
	}

	.value-display {
		position: absolute;
		top: 50%;
		left: 50%;
		transform: translate(-50%, -50%);
		font-family: var(--font-mono);
		color: var(--text-secondary, #999);
		pointer-events: auto;
		cursor: text;
		user-select: none;
		white-space: nowrap;
	}

	.value-edit {
		position: absolute;
		top: 50%;
		left: 50%;
		transform: translate(-50%, -50%);
		background: var(--bg-control, #1e1e22);
		border: 1px solid var(--accent-gold, #d4a017);
		border-radius: 2px;
		color: var(--text-primary, #e0e0e0);
		font-family: var(--font-mono);
		font-size: 9px;
		text-align: center;
		padding: 1px 2px;
		outline: none;
	}
</style>
