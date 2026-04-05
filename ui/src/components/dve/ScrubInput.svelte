<script lang="ts">
	interface Props {
		value: number;
		min?: number;
		max?: number;
		step?: number;
		precision?: number;
		label?: string;
		suffix?: string;
		disabled?: boolean;
		onScrub?: (value: number) => void;
		onCommit?: (value: number) => void;
	}

	let {
		value,
		min,
		max,
		step = 1,
		precision = 0,
		label,
		suffix,
		disabled = false,
		onScrub,
		onCommit,
	}: Props = $props();

	const DRAG_THRESHOLD = 3;

	let inputEl = $state<HTMLInputElement | null>(null);
	let isDragging = $state(false);
	let isEditing = $state(false);
	let editText = $state('');

	// Pending drag state — not yet activated (waiting for threshold)
	let pending = $state<{
		startX: number;
		startValue: number;
		pointerId: number;
	} | null>(null);

	// Active drag state
	let drag = $state<{
		startX: number;
		startValue: number;
	} | null>(null);

	let displayValue = $derived(
		isEditing ? editText : value.toFixed(precision),
	);

	function clamp(v: number): number {
		if (min !== undefined) v = Math.max(min, v);
		if (max !== undefined) v = Math.min(max, v);
		return v;
	}

	function roundToStep(v: number): number {
		// Round to step increments then to precision to avoid float artifacts
		const rounded = Math.round(v / step) * step;
		const factor = Math.pow(10, precision);
		return Math.round(rounded * factor) / factor;
	}

	function commitValue(v: number) {
		const clamped = clamp(roundToStep(v));
		onCommit?.(clamped);
	}

	// ── Pointer drag system ──
	function handlePointerDown(e: PointerEvent) {
		if (disabled || e.button !== 0) return;
		if (isEditing) return; // don't intercept clicks while editing

		pending = {
			startX: e.clientX,
			startValue: value,
			pointerId: e.pointerId,
		};
	}

	function handlePointerMove(e: PointerEvent) {
		if (disabled) return;

		// Check if pending drag should activate
		if (pending && !drag) {
			const dx = Math.abs(e.clientX - pending.startX);
			if (dx >= DRAG_THRESHOLD) {
				// Activate drag — capture pointer, prevent input focus
				inputEl?.setPointerCapture(pending.pointerId);
				inputEl?.blur();
				drag = {
					startX: pending.startX,
					startValue: pending.startValue,
				};
				isDragging = true;
				isEditing = false;
				pending = null;
			}
			return;
		}

		if (!drag) return;

		const deltaX = e.clientX - drag.startX;
		const speed = e.shiftKey ? 0.1 : 1;
		const raw = drag.startValue + deltaX * step * speed;
		const clamped = clamp(roundToStep(raw));
		onScrub?.(clamped);
	}

	function handlePointerUp(_e: PointerEvent) {
		if (pending) {
			// Threshold was never reached — this was a click. Focus for editing.
			pending = null;
			startEditing();
			return;
		}

		if (drag) {
			// Compute final value and commit
			const el = inputEl;
			if (el) {
				try { el.releasePointerCapture(_e.pointerId); } catch { /* noop */ }
			}
			// Recompute final value from last known position
			const deltaX = _e.clientX - drag.startX;
			const speed = _e.shiftKey ? 0.1 : 1;
			const raw = drag.startValue + deltaX * step * speed;
			const clamped = clamp(roundToStep(raw));
			drag = null;
			isDragging = false;
			commitValue(clamped);
		}
	}

	// ── Click-to-edit ──
	function startEditing() {
		if (disabled) return;
		isEditing = true;
		editText = value.toFixed(precision);
		// Need to wait a tick for the input to switch to text mode
		requestAnimationFrame(() => {
			inputEl?.select();
		});
	}

	function handleKeyDown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			const parsed = parseFloat(editText);
			if (!isNaN(parsed)) {
				const clamped = clamp(roundToStep(parsed));
				isEditing = false;
				inputEl?.blur();
				commitValue(clamped);
			} else {
				// Invalid input — revert
				isEditing = false;
				inputEl?.blur();
			}
		} else if (e.key === 'Escape') {
			isEditing = false;
			inputEl?.blur();
		}
	}

	function handleBlur() {
		if (isEditing) {
			// Commit on blur if valid, otherwise revert
			const parsed = parseFloat(editText);
			if (!isNaN(parsed)) {
				const clamped = clamp(roundToStep(parsed));
				commitValue(clamped);
			}
			isEditing = false;
		}
	}

	function handleInput(e: Event) {
		const target = e.target as HTMLInputElement;
		editText = target.value;
	}

	// ── Scroll wheel adjustment ──
	function handleWheel(e: WheelEvent) {
		if (disabled) return;
		e.preventDefault();
		const direction = e.deltaY > 0 ? -1 : 1;
		const speed = e.shiftKey ? 0.1 : 1;
		const raw = value + direction * step * speed;
		const clamped = clamp(roundToStep(raw));
		onScrub?.(clamped);
		commitValue(clamped);
	}
</script>

<div class="scrub-input-wrap" class:disabled>
	{#if label}
		<span class="scrub-label">{label}</span>
	{/if}
	<div class="scrub-field" class:dragging={isDragging}>
		<input
			bind:this={inputEl}
			type="text"
			class="scrub-input"
			class:dragging={isDragging}
			class:has-suffix={!!suffix && !isEditing}
			value={displayValue}
			{disabled}
			readonly={!isEditing}
			inputmode={isEditing ? 'decimal' : undefined}
			onpointerdown={handlePointerDown}
			onpointermove={handlePointerMove}
			onpointerup={handlePointerUp}
			onkeydown={handleKeyDown}
			onblur={handleBlur}
			oninput={handleInput}
			onwheel={handleWheel}
		/>
		{#if suffix && !isEditing}
			<span class="scrub-suffix">{suffix}</span>
		{/if}
	</div>
</div>

<style>
	.scrub-input-wrap {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
	}

	.scrub-input-wrap.disabled {
		opacity: 0.4;
		pointer-events: none;
	}

	.scrub-label {
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		line-height: 1;
		user-select: none;
	}

	.scrub-field {
		position: relative;
		display: inline-flex;
		align-items: center;
		width: 64px;
	}

	.scrub-input {
		width: 100%;
		padding: 2px 4px;
		background: var(--bg-control, #242429);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs, 0.5rem);
		text-align: right;
		cursor: ew-resize;
		appearance: textfield;
		-moz-appearance: textfield;
		box-sizing: border-box;
		user-select: none;
		outline: none;
	}

	.scrub-input::-webkit-inner-spin-button,
	.scrub-input::-webkit-outer-spin-button {
		-webkit-appearance: none;
		margin: 0;
	}

	.scrub-input.has-suffix {
		padding-right: 16px;
	}

	.scrub-input:focus {
		cursor: text;
		border-color: var(--border-strong, #555);
		user-select: text;
	}

	.scrub-input.dragging {
		border-color: var(--accent-gold, #d4a017);
		cursor: ew-resize;
		user-select: none;
	}

	.scrub-suffix {
		position: absolute;
		right: 5px;
		top: 50%;
		transform: translateY(-50%);
		font-size: var(--text-2xs, 0.5rem);
		font-family: var(--font-mono);
		color: var(--text-tertiary);
		pointer-events: none;
		user-select: none;
	}

	.scrub-field.dragging {
		/* subtle glow during drag */
		filter: drop-shadow(0 0 2px rgba(212, 160, 23, 0.25));
	}
</style>
