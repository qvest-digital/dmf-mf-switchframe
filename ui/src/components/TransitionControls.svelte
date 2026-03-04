<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { cut, startTransition, setTransitionPosition, fadeToBlack, fireAndForget } from '$lib/api/switch-api';

	interface Props { state: ControlRoomState; }
	let { state }: Props = $props();

	let transType: 'mix' | 'dip' = 'mix';
	let durationMs: number = 1000;

	const autoDisabled = $derived(
		!state.previewSource || state.inTransition || state.ftbActive
	);

	const ftbDisabled = $derived(
		state.inTransition && !state.ftbActive
	);

	const tbarValue = $derived(
		state.inTransition ? state.transitionPosition : 0
	);

	function handleAuto() {
		if (autoDisabled) return;
		fireAndForget(startTransition(state.previewSource, transType, durationMs));
	}

	function handleFTB() {
		if (ftbDisabled) return;
		fireAndForget(fadeToBlack());
	}

	function handleTbarInput(e: Event) {
		const value = parseFloat((e.target as HTMLInputElement).value);
		if (!state.inTransition && value > 0 && state.previewSource) {
			fireAndForget(startTransition(state.previewSource, transType, durationMs));
		}
		fireAndForget(setTransitionPosition(value));
	}
</script>

<div class="transition-controls">
	<div class="transition-buttons">
		<button class="btn cut" onclick={() => fireAndForget(cut(state.previewSource))} disabled={!state.previewSource}>
			CUT
			<span class="shortcut">Space</span>
		</button>
		<button class="btn auto" onclick={handleAuto} disabled={autoDisabled}>
			AUTO
			<span class="shortcut">Enter</span>
		</button>
		<button class="btn ftb" class:active={state.ftbActive} onclick={handleFTB} disabled={ftbDisabled}>
			FTB
			<span class="shortcut">F1</span>
		</button>
	</div>

	<div class="tbar-container">
		<input
			type="range"
			class="tbar-slider"
			min="0"
			max="1"
			step="0.01"
			value={tbarValue}
			oninput={handleTbarInput}
		/>
	</div>

	<div class="transition-options">
		<div class="type-selector">
			<label class="type-option">
				<input type="radio" name="transType" value="mix" bind:group={transType} />
				Mix
			</label>
			<label class="type-option">
				<input type="radio" name="transType" value="dip" bind:group={transType} />
				Dip
			</label>
		</div>

		<select class="duration-select" bind:value={durationMs}>
			<option value={500}>0.5s</option>
			<option value={1000}>1.0s</option>
			<option value={1500}>1.5s</option>
			<option value={2000}>2.0s</option>
			<option value={3000}>3.0s</option>
		</select>
	</div>
</div>

<style>
	.transition-controls { display: flex; flex-direction: column; gap: 0.5rem; padding: 0.5rem; }
	.transition-buttons { display: flex; gap: 0.5rem; }
	.btn {
		padding: 0.75rem 1.5rem; border: 2px solid #444; border-radius: 4px;
		background: #1a1a1a; color: #ccc; cursor: pointer; font-family: monospace;
		font-weight: bold; font-size: 1rem; position: relative;
	}
	.btn:disabled { opacity: 0.4; cursor: not-allowed; }
	.btn.cut:not(:disabled):hover { border-color: #cc0000; background: #2a0000; }
	.btn.auto:not(:disabled):hover { border-color: #cccc00; background: #2a2a00; }
	.btn.ftb:not(:disabled):hover { border-color: #cc8800; background: #2a1a00; }
	.btn.ftb.active { background: #cc8800; color: #000; border-color: #cc8800; }
	.shortcut { display: block; font-size: 0.6rem; opacity: 0.5; margin-top: 0.25rem; }

	.tbar-container { padding: 0.25rem 0; }
	.tbar-slider { width: 100%; height: 24px; accent-color: #cccc00; cursor: pointer; }

	.transition-options { display: flex; gap: 1rem; align-items: center; }
	.type-selector { display: flex; gap: 0.5rem; }
	.type-option {
		font-family: monospace; font-size: 0.8rem; color: #aaa; cursor: pointer;
		display: flex; align-items: center; gap: 0.25rem;
	}
	.type-option input { accent-color: #cccc00; }
	.duration-select {
		font-family: monospace; font-size: 0.8rem; background: #222; color: #ccc;
		border: 1px solid #444; border-radius: 4px; padding: 0.25rem 0.5rem;
	}
</style>
