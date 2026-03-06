<script lang="ts">
	import type { ControlRoomState, KeyConfig } from '$lib/api/types';
	import { setSourceKey, deleteSourceKey, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	let selectedSource = $derived(Object.keys(crState.sources).sort()[0] ?? '');
	let keyType = $state<'none' | 'chroma' | 'luma'>('none');
	let enabled = $state(true);

	// Chroma params
	let keyColorY = $state(182);
	let keyColorCb = $state(30);
	let keyColorCr = $state(12);
	let similarity = $state(0.4);
	let smoothness = $state(0.1);
	let spillSuppress = $state(0.5);

	// Luma params
	let lowClip = $state(0.0);
	let highClip = $state(0.8);
	let softness = $state(0.1);

	let sourceKeys = $derived(Object.keys(crState.sources).sort());
	let activeSource = $state('');

	// Initialize activeSource when sources become available
	$effect(() => {
		if (!activeSource && selectedSource) {
			activeSource = selectedSource;
		}
	});

	function selectSource(key: string) {
		activeSource = key;
	}

	function applyKey() {
		if (!activeSource) return;

		if (keyType === 'none') {
			apiCall(deleteSourceKey(activeSource), 'Remove key');
			notify('info', `Key removed from ${activeSource}`);
			return;
		}

		const config: KeyConfig = {
			type: keyType,
			enabled,
			keyColorY: keyType === 'chroma' ? keyColorY : undefined,
			keyColorCb: keyType === 'chroma' ? keyColorCb : undefined,
			keyColorCr: keyType === 'chroma' ? keyColorCr : undefined,
			similarity: keyType === 'chroma' ? similarity : undefined,
			smoothness: keyType === 'chroma' ? smoothness : undefined,
			spillSuppress: keyType === 'chroma' ? spillSuppress : undefined,
			lowClip: keyType === 'luma' ? lowClip : undefined,
			highClip: keyType === 'luma' ? highClip : undefined,
			softness: keyType === 'luma' ? softness : undefined,
		};

		apiCall(setSourceKey(activeSource, config), 'Set key');
		notify('info', `${keyType} key applied to ${activeSource}`);
	}
</script>

<div class="key-panel">
	<div class="panel-header">
		<span class="panel-title">UPSTREAM KEY</span>
	</div>

	<div class="source-select">
		<label class="field-label">Source
		<select
			class="key-select"
			value={activeSource}
			onchange={(e) => selectSource((e.target as HTMLSelectElement).value)}
		>
			{#each sourceKeys as key}
				<option value={key}>{crState.sources[key]?.label || key}</option>
			{/each}
		</select>
		</label>
	</div>

	<div class="key-type-select">
		<span class="field-label">Type</span>
		<div class="type-buttons">
			<button class="type-btn" class:active={keyType === 'none'} onclick={() => keyType = 'none'}>None</button>
			<button class="type-btn" class:active={keyType === 'chroma'} onclick={() => keyType = 'chroma'}>Chroma</button>
			<button class="type-btn" class:active={keyType === 'luma'} onclick={() => keyType = 'luma'}>Luma</button>
		</div>
	</div>

	{#if keyType !== 'none'}
		<label class="enable-label">
			<input type="checkbox" bind:checked={enabled} />
			Enabled
		</label>
	{/if}

	{#if keyType === 'chroma'}
		<div class="key-controls">
			<div class="slider-group">
				<label class="slider-label">Similarity: {similarity.toFixed(2)}
				<input type="range" min="0" max="1" step="0.01" bind:value={similarity} class="slider" />
				</label>
			</div>
			<div class="slider-group">
				<label class="slider-label">Smoothness: {smoothness.toFixed(2)}
				<input type="range" min="0" max="1" step="0.01" bind:value={smoothness} class="slider" />
				</label>
			</div>
			<div class="slider-group">
				<label class="slider-label">Spill: {spillSuppress.toFixed(2)}
				<input type="range" min="0" max="1" step="0.01" bind:value={spillSuppress} class="slider" />
				</label>
			</div>
			<div class="color-group">
				<span class="slider-label">Key Color (Y/Cb/Cr)</span>
				<div class="color-inputs">
					<input type="number" min="0" max="255" bind:value={keyColorY} class="num-input" />
					<input type="number" min="0" max="255" bind:value={keyColorCb} class="num-input" />
					<input type="number" min="0" max="255" bind:value={keyColorCr} class="num-input" />
				</div>
			</div>
		</div>
	{/if}

	{#if keyType === 'luma'}
		<div class="key-controls">
			<div class="slider-group">
				<label class="slider-label">Low Clip: {lowClip.toFixed(2)}
				<input type="range" min="0" max="1" step="0.01" bind:value={lowClip} class="slider" />
				</label>
			</div>
			<div class="slider-group">
				<label class="slider-label">High Clip: {highClip.toFixed(2)}
				<input type="range" min="0" max="1" step="0.01" bind:value={highClip} class="slider" />
				</label>
			</div>
			<div class="slider-group">
				<label class="slider-label">Softness: {softness.toFixed(2)}
				<input type="range" min="0" max="1" step="0.01" bind:value={softness} class="slider" />
				</label>
			</div>
		</div>
	{/if}

	{#if keyType !== 'none'}
		<button class="apply-btn" onclick={applyKey}>Apply Key</button>
	{:else if activeSource}
		<button class="apply-btn remove-btn" onclick={applyKey}>Remove Key</button>
	{/if}
</div>

<style>
	.key-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 6px;
		height: 100%;
		overflow-y: auto;
	}

	.panel-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.panel-title {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
	}

	.field-label {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 500;
		color: var(--text-tertiary);
		margin-bottom: 2px;
		display: block;
	}

	.source-select {
		display: flex;
		flex-direction: column;
	}

	.key-select {
		width: 100%;
		padding: 4px 6px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: 0.7rem;
	}

	.key-type-select {
		display: flex;
		flex-direction: column;
	}

	.type-buttons {
		display: flex;
		gap: 2px;
	}

	.type-btn {
		flex: 1;
		padding: 4px 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.type-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.type-btn.active {
		background: rgba(59, 130, 246, 0.2);
		color: var(--accent-blue);
		border-color: rgba(59, 130, 246, 0.4);
	}

	.enable-label {
		display: flex;
		align-items: center;
		gap: 6px;
		font-family: var(--font-ui);
		font-size: 0.7rem;
		color: var(--text-secondary);
		cursor: pointer;
	}

	.key-controls {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.slider-group {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.slider-label {
		font-family: var(--font-ui);
		font-size: 0.6rem;
		color: var(--text-tertiary);
	}

	.slider {
		width: 100%;
		height: 14px;
		accent-color: var(--accent-blue);
	}

	.color-group {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.color-inputs {
		display: flex;
		gap: 4px;
	}

	.num-input {
		flex: 1;
		padding: 2px 4px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: 0.65rem;
		width: 100%;
		min-width: 0;
	}

	.apply-btn {
		padding: 6px;
		background: rgba(34, 197, 94, 0.2);
		color: #22c55e;
		border: 1px solid rgba(34, 197, 94, 0.4);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 600;
		cursor: pointer;
		transition:
			background var(--transition-fast);
	}

	.apply-btn:hover {
		background: rgba(34, 197, 94, 0.3);
	}

	.remove-btn {
		background: rgba(239, 68, 68, 0.15);
		color: #ef4444;
		border-color: rgba(239, 68, 68, 0.3);
	}

	.remove-btn:hover {
		background: rgba(239, 68, 68, 0.25);
	}
</style>
