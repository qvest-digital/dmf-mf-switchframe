<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { cut } from '$lib/api/switch-api';

	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();
</script>

<div class="transition-controls">
	<button class="btn cut" onclick={() => cut(state.previewSource)} disabled={!state.previewSource}>
		CUT
		<span class="shortcut">Space</span>
	</button>
	<button class="btn auto" disabled title="Not implemented (Phase 4)">
		AUTO
		<span class="shortcut">Enter</span>
	</button>
	<button class="btn ftb" disabled title="Not implemented">
		FTB
		<span class="shortcut">F1</span>
	</button>
</div>

<style>
	.transition-controls { display: flex; gap: 0.5rem; padding: 0.5rem; }
	.btn {
		padding: 0.75rem 1.5rem; border: 2px solid #444; border-radius: 4px;
		background: #1a1a1a; color: #ccc; cursor: pointer; font-family: monospace;
		font-weight: bold; font-size: 1rem; position: relative;
	}
	.btn:disabled { opacity: 0.4; cursor: not-allowed; }
	.btn.cut:not(:disabled):hover { border-color: #cc0000; background: #2a0000; }
	.shortcut { display: block; font-size: 0.6rem; opacity: 0.5; margin-top: 0.25rem; }
</style>
