<script lang="ts">
	import type { ControlRoomState, SqueezebackRect } from '$lib/api/types';
	import InspectorSection from './InspectorSection.svelte';
	import { setSqueezeback, squeezebackOn, squeezebackOff, apiCall } from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	// L-frames at 75%: the program in one corner, the background in the L.
	const SHAPES = [
		{ id: 'top-right', label: 'L-frame top right 75%', ax: 1, ay: 0 },
		{ id: 'top-left', label: 'L-frame top left 75%', ax: 0, ay: 0 },
		{ id: 'bottom-right', label: 'L-frame bottom right 75%', ax: 1, ay: 1 },
	];

	let expanded = $state(false);
	let shapeId = $state('top-right');
	let background = $state('');
	let durationMs = $state(800);
	let easing: string | undefined;
	let liveRect: SqueezebackRect | undefined;

	let sq = $derived(crState.squeezeback);
	let isOn = $derived(sq?.on ?? false);
	let frameW = $derived(crState.pipelineFormat?.width ?? 1920);
	let frameH = $derived(crState.pipelineFormat?.height ?? 1080);
	let sourceKeys = $derived(Object.keys(crState.sources ?? {}).sort());

	const even = (v: number) => Math.round(v / 2) * 2;

	function shapeRect(id: string, w: number, h: number): SqueezebackRect {
		const s = SHAPES.find((x) => x.id === id) ?? SHAPES[0];
		const rw = even(w * 0.75);
		const rh = even(h * 0.75);
		return { x: s.ax * (w - rw), y: s.ay * (h - rh), w: rw, h: rh };
	}

	const sameRect = (a: SqueezebackRect, b: SqueezebackRect) =>
		a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;

	// The fields follow the live configuration whenever it changes, so a
	// reload shows it and configure() never replaces it with the defaults
	// above. Broadcasts that leave it alone keep unsent local edits.
	let seen = '';
	$effect(() => {
		if (!sq) return;
		const key = JSON.stringify([sq.rect, sq.background, sq.durationMs, sq.easing, frameW, frameH]);
		if (key === seen) return;
		seen = key;
		shapeId = SHAPES.find((s) => sameRect(shapeRect(s.id, frameW, frameH), sq.rect))?.id ?? 'custom';
		liveRect = sq.rect;
		background = sq.background ?? '';
		durationMs = sq.durationMs ?? 0; // omitted is 0, a cut
		easing = sq.easing;
	});

	async function configure() {
		const rect = shapeId === 'custom' && liveRect ? liveRect : shapeRect(shapeId, frameW, frameH);
		await setSqueezeback({ rect, background, durationMs, ...(easing ? { easing } : {}) });
	}

	function toggle() {
		if (isOn) {
			apiCall(squeezebackOff({ durationMs }), 'Squeezeback off');
		} else {
			apiCall(configure().then(() => squeezebackOn()), 'Squeezeback on');
		}
	}

	// While on, a new shape or background moves there at once.
	function changed() {
		if (isOn) apiCall(configure(), 'Squeezeback');
	}
</script>

<InspectorSection title="Squeezeback" bind:expanded badge={isOn ? 'ON' : undefined}>
	<div class="squeeze-editor">
		<select class="squeeze-select squeeze-shape" bind:value={shapeId} onchange={changed}>
			{#each SHAPES as s (s.id)}
				<option value={s.id}>{s.label}</option>
			{/each}
			{#if shapeId === 'custom'}
				<option value="custom">Custom</option>
			{/if}
		</select>
		<select class="squeeze-select squeeze-background" bind:value={background} onchange={changed} title="Background">
			<option value="">Black</option>
			{#each sourceKeys as key (key)}
				<option value={key}>{crState.sources[key]?.label || key}</option>
			{/each}
		</select>
		<div class="squeeze-row">
			<input
				class="squeeze-duration"
				type="number"
				min="0"
				max="5000"
				step="100"
				bind:value={durationMs}
				title="Duration (ms)"
			/>
			<span class="squeeze-unit">ms</span>
			<button type="button" class="squeeze-btn" class:on={isOn} onclick={toggle}>
				{isOn ? 'Restore' : 'Squeeze'}
			</button>
		</div>
	</div>
</InspectorSection>

<style>
	.squeeze-editor {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 6px 4px 8px;
	}

	.squeeze-select,
	.squeeze-duration {
		padding: 3px 5px;
		font-family: var(--font-ui);
		font-size: 10px;
		background: var(--bg-control, #242429);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs, 3px);
		min-width: 0;
	}

	.squeeze-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.squeeze-duration {
		width: 64px;
		font-family: var(--font-mono, monospace);
	}

	.squeeze-unit {
		font-size: 10px;
		color: var(--text-tertiary);
	}

	.squeeze-btn {
		flex: 1;
		padding: 4px 10px;
		background: rgba(34, 197, 94, 0.15);
		border: 1px solid rgba(34, 197, 94, 0.35);
		border-radius: var(--radius-xs, 3px);
		color: var(--color-success, #22c55e);
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 700;
		cursor: pointer;
	}

	.squeeze-btn.on {
		background: rgba(239, 68, 68, 0.15);
		border-color: rgba(239, 68, 68, 0.35);
		color: var(--color-error, #ef4444);
	}
</style>
