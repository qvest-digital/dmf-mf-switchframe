<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { setPreview, cut, startTransition, fireAndForget } from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
		onSwitchLayout?: () => void;
	}

	let { state, onSwitchLayout }: Props = $props();

	let sourceKeys = $derived(Object.keys(state.sources).sort());
	let previewLabel = $derived(
		state.previewSource && state.sources[state.previewSource]
			? state.sources[state.previewSource].label || state.previewSource
			: '—',
	);
	let programLabel = $derived(
		state.programSource && state.sources[state.programSource]
			? state.sources[state.programSource].label || state.programSource
			: '—',
	);
	let canTransition = $derived(
		state.previewSource !== '' && !state.inTransition && !state.ftbActive,
	);
	let canCut = $derived(state.previewSource !== '' && !state.inTransition);

	function handleSourceClick(key: string) {
		fireAndForget(setPreview(key));
	}

	function handleCut() {
		if (!canCut) return;
		fireAndForget(cut(state.previewSource));
	}

	function handleDissolve() {
		if (!canTransition) return;
		fireAndForget(startTransition(state.previewSource, 'mix', 1000));
	}

	function tallyClass(key: string): string {
		const tally = state.tallyState[key];
		if (tally === 'program') return 'tally-program';
		if (tally === 'preview') return 'tally-preview';
		return '';
	}
</script>

<div class="simple-mode">
	<header class="simple-header">
		<button class="gear-btn" onclick={onSwitchLayout} title="Switch to traditional mode">
			&#9881;
		</button>
		<span class="brand">SwitchFrame</span>
	</header>

	<section class="monitors">
		<div class="monitor">
			<div class="monitor-label preview-label">PREVIEW</div>
			<canvas id="preview-video" width="640" height="360"></canvas>
			<div class="monitor-source">{previewLabel}</div>
		</div>
		<div class="monitor">
			<div class="monitor-label program-label">PROGRAM</div>
			<canvas id="program-video" width="640" height="360"></canvas>
			<div class="monitor-source">{programLabel}</div>
		</div>
	</section>

	<section class="source-buttons">
		{#each sourceKeys as key, i}
			<button
				class="source-btn {tallyClass(key)}"
				onclick={() => handleSourceClick(key)}
			>
				<span class="source-number">{i + 1}</span>
				{state.sources[key].label || key}
			</button>
		{/each}
	</section>

	<section class="action-buttons">
		<button class="action-btn cut-btn" onclick={handleCut} disabled={!canCut}>
			CUT
		</button>
		<button
			class="action-btn dissolve-btn"
			onclick={handleDissolve}
			disabled={!canTransition}
		>
			DISSOLVE
		</button>
	</section>
</div>

<style>
	.simple-mode {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: #111;
		color: #eee;
		font-family: monospace;
	}

	.simple-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.5rem 1rem;
		border-bottom: 1px solid #333;
	}

	.gear-btn {
		background: none;
		border: 1px solid #444;
		color: #aaa;
		font-size: 1.4rem;
		cursor: pointer;
		padding: 0.25rem 0.5rem;
		border-radius: 4px;
	}
	.gear-btn:hover {
		color: #fff;
		border-color: #888;
	}

	.brand {
		font-size: 1rem;
		font-weight: bold;
		color: #888;
	}

	.monitors {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1rem;
		padding: 1rem;
		flex: 1;
		min-height: 0;
	}

	.monitor {
		position: relative;
		background: #000;
		border-radius: 4px;
		overflow: hidden;
	}

	.monitor canvas {
		width: 100%;
		height: 100%;
		object-fit: contain;
	}

	.monitor-label {
		position: absolute;
		top: 0.5rem;
		left: 0.5rem;
		padding: 0.15rem 0.5rem;
		font-size: 0.7rem;
		font-weight: bold;
		border-radius: 2px;
		z-index: 1;
	}

	.preview-label {
		background: #0a4;
		color: #fff;
	}

	.program-label {
		background: #c22;
		color: #fff;
	}

	.monitor-source {
		position: absolute;
		bottom: 0.5rem;
		left: 0.5rem;
		font-size: 0.8rem;
		color: #ccc;
	}

	.source-buttons {
		display: flex;
		gap: 0.5rem;
		padding: 0.75rem 1rem;
		border-top: 1px solid #333;
		flex-wrap: wrap;
	}

	.source-btn {
		flex: 1;
		min-width: 100px;
		padding: 0.75rem 1rem;
		background: #222;
		color: #eee;
		border: 2px solid #444;
		border-radius: 4px;
		cursor: pointer;
		font-family: monospace;
		font-size: 0.9rem;
		text-align: center;
	}
	.source-btn:hover {
		border-color: #888;
	}
	.source-btn .source-number {
		font-weight: bold;
		margin-right: 0.5rem;
		color: #888;
	}

	.tally-preview {
		border-color: #0a4;
		background: #0a4111;
	}
	.tally-program {
		border-color: #c22;
		background: #4a1111;
	}

	.action-buttons {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 0.75rem;
		padding: 0.75rem 1rem 1rem;
		border-top: 1px solid #333;
	}

	.action-btn {
		padding: 1rem;
		font-family: monospace;
		font-size: 1.1rem;
		font-weight: bold;
		border: 2px solid;
		border-radius: 4px;
		cursor: pointer;
	}
	.action-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}

	.cut-btn {
		background: #c22;
		color: #fff;
		border-color: #e33;
	}
	.cut-btn:hover:not(:disabled) {
		background: #d44;
	}

	.dissolve-btn {
		background: #1a3a6a;
		color: #fff;
		border-color: #4488ff;
	}
	.dissolve-btn:hover:not(:disabled) {
		background: #2a4a8a;
	}
</style>
