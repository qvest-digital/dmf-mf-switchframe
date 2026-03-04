<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();
</script>

<div class="program-preview">
	<div class="preview-window">
		<div class="window-label preview-label">PREVIEW</div>
		<div class="window-canvas">
			<canvas id="preview-video" width="640" height="360"></canvas>
			<span class="source-name">{state.sources[state.previewSource]?.label || state.previewSource || '—'}</span>
		</div>
	</div>
	<div class="program-window">
		<div class="window-label program-label">PROGRAM</div>
		<div class="window-canvas">
			<canvas id="program-video" width="640" height="360"></canvas>
			<span class="source-name">{state.sources[state.programSource]?.label || state.programSource || '—'}</span>
		</div>
	</div>
</div>

<style>
	.program-preview { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; padding: 0.5rem; }
	.preview-window, .program-window { aspect-ratio: 16/9; background: #0a0a0a; border-radius: 4px; overflow: hidden; position: relative; }
	.window-label { position: absolute; top: 0.5rem; left: 0.5rem; font-family: monospace; font-weight: bold; font-size: 0.75rem; padding: 0.15rem 0.5rem; border-radius: 2px; z-index: 1; }
	.preview-label { background: var(--tally-preview); color: white; }
	.program-label { background: var(--tally-program); color: white; }
	.window-canvas { width: 100%; height: 100%; display: flex; align-items: center; justify-content: center; position: relative; }
	.window-canvas canvas { position: absolute; top: 0; left: 0; width: 100%; height: 100%; object-fit: contain; }
	.source-name { font-family: monospace; font-size: 1.5rem; color: #555; position: relative; z-index: 1; pointer-events: none; }
</style>
