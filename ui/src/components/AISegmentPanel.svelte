<script lang="ts">
	import { untrack } from 'svelte';
	import type { ControlRoomState, AISegmentConfig } from '$lib/api/types';
	import { setAISegment, deleteAISegment, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	let sourceKeys = $derived(Object.keys(crState.sources).sort());
	let activeSource = $state('');

	// Per-source controls
	let enabled = $state(false);
	let sensitivity = $state(50);
	let edgeSmooth = $state(30);
	let bgMode = $state<'transparent' | 'blur' | 'color'>('transparent');
	let blurRadius = $state(10);
	let bgColor = $state('#00ff00');

	// Derived background string for the config
	let backgroundValue = $derived(
		bgMode === 'transparent'
			? 'transparent'
			: bgMode === 'blur'
				? `blur:${blurRadius}`
				: `color:${bgColor.replace('#', '')}`,
	);

	// Initialize activeSource when sources become available
	$effect(() => {
		if (!activeSource && sourceKeys.length > 0) {
			activeSource = sourceKeys[0];
		}
	});

	// Load config from MoQ state broadcast when source changes.
	// No REST polling — config comes via the control track like all other state.
	$effect(() => {
		const source = activeSource;
		if (!source) return;

		// Read state broadcast for this source (untracked so edits aren't overwritten)
		const stateConfig = untrack(() => crState.aiSegmentation?.sources?.[source]);
		if (stateConfig) {
			applyConfig(stateConfig);
		} else {
			resetDefaults();
		}
	});

	function applyConfig(config: AISegmentConfig) {
		enabled = config.enabled ?? false;
		sensitivity = Math.round((config.sensitivity ?? 0.5) * 100);
		edgeSmooth = Math.round((config.edgeSmooth ?? 0.3) * 100);

		const bg = config.background ?? 'transparent';
		if (bg === 'transparent') {
			bgMode = 'transparent';
		} else if (bg.startsWith('blur:')) {
			bgMode = 'blur';
			blurRadius = parseInt(bg.slice(5)) || 10;
		} else if (bg.startsWith('color:')) {
			bgMode = 'color';
			bgColor = '#' + bg.slice(6);
		} else {
			bgMode = 'transparent';
		}
	}

	function resetDefaults() {
		enabled = false;
		sensitivity = 50;
		edgeSmooth = 30;
		bgMode = 'transparent';
		blurRadius = 10;
		bgColor = '#00ff00';
	}

	function selectSource(key: string) {
		activeSource = key;
	}

	function applySegment() {
		if (!activeSource) return;

		const label = crState.sources[activeSource]?.label || activeSource;

		if (!enabled) {
			// Disable = DELETE (server treats PUT as enable regardless of body)
			apiCall(
				deleteAISegment(activeSource).then((r) => {
					if (!r.ok) throw new Error(`HTTP ${r.status}`);
				}),
				'AI Segment',
			);
			notify('info', `AI background disabled on ${label}`);
			return;
		}

		const config: Partial<AISegmentConfig> = {
			enabled: true,
			sensitivity: sensitivity / 100,
			edgeSmooth: edgeSmooth / 100,
			background: backgroundValue,
		};

		apiCall(
			setAISegment(activeSource, config).then((r) => {
				if (!r.ok) throw new Error(`HTTP ${r.status}`);
			}),
			'AI Segment',
		);
		notify('info', `AI background enabled on ${label}`);
	}

	function removeSegment() {
		if (!activeSource) return;

		apiCall(
			deleteAISegment(activeSource).then((r) => {
				if (!r.ok) throw new Error(`HTTP ${r.status}`);
			}),
			'Remove AI Segment',
		);

		const label = crState.sources[activeSource]?.label || activeSource;
		notify('info', `AI background removed from ${label}`);
		resetDefaults();
	}

	// Inference latency from state
	let inferenceMs = $derived(
		activeSource && crState.aiSegmentation?.sources?.[activeSource]
			? undefined // server doesn't expose latency yet
			: undefined,
	);
</script>

<div class="ai-panel">
	<div class="panel-header">
		<span class="panel-title">AI BACKGROUND</span>
		{#if crState.aiSegmentation?.modelName}
			<span class="model-badge">{crState.aiSegmentation.modelName}</span>
		{/if}
	</div>

	<div class="top-row">
		<select class="ai-select" value={activeSource} onchange={(e) => selectSource((e.target as HTMLSelectElement).value)}>
			{#each sourceKeys as key}
				<option value={key}>{crState.sources[key]?.label || key}</option>
			{/each}
		</select>
		<label class="enable-toggle">
			<input type="checkbox" bind:checked={enabled} />
			<span class="enable-text">{enabled ? 'ON' : 'OFF'}</span>
		</label>
	</div>

	{#if enabled}
		<div class="controls">
			<div class="sliders-row">
				<div class="param-row">
					<span class="param-label">Sens</span>
					<input type="range" min="0" max="100" step="1" bind:value={sensitivity} class="param-slider" />
					<span class="param-val">{sensitivity}</span>
				</div>
				<div class="param-row">
					<span class="param-label">Edge</span>
					<input type="range" min="0" max="100" step="1" bind:value={edgeSmooth} class="param-slider" />
					<span class="param-val">{edgeSmooth}</span>
				</div>
			</div>

			<div class="bg-row">
				<span class="param-label">BG</span>
				<div class="mode-seg">
					<button class="seg-btn" class:active={bgMode === 'transparent'} onclick={() => (bgMode = 'transparent')}>None</button>
					<button class="seg-btn" class:active={bgMode === 'blur'} onclick={() => (bgMode = 'blur')}>Blur</button>
					<button class="seg-btn" class:active={bgMode === 'color'} onclick={() => (bgMode = 'color')}>Color</button>
				</div>
				{#if bgMode === 'blur'}
					<input type="range" min="1" max="50" step="1" bind:value={blurRadius} class="param-slider bg-slider" />
					<span class="param-val">{blurRadius}</span>
				{/if}
				{#if bgMode === 'color'}
					<input type="color" bind:value={bgColor} class="color-swatch" title="Pick color" />
				{/if}
			</div>
		</div>
	{/if}

	<div class="action-row">
		<button class="apply-btn" onclick={applySegment}>{enabled ? 'Apply' : 'Disable'}</button>
		{#if activeSource}
			<button class="remove-btn" onclick={removeSegment}>Remove</button>
		{/if}
	</div>
</div>

<style>
	.ai-panel {
		display: flex;
		flex-direction: column;
		gap: 5px;
		padding: 6px;
		height: 100%;
	}

	.panel-header {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.panel-title {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
	}

	.model-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		background: rgba(168, 85, 247, 0.15);
		color: #a855f7;
		border: 1px solid rgba(168, 85, 247, 0.3);
		border-radius: var(--radius-sm);
		padding: 0 4px;
	}

	.top-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.ai-select {
		flex: 1;
		padding: 2px 4px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
	}

	.enable-toggle {
		display: flex;
		align-items: center;
		gap: 4px;
		cursor: pointer;
		flex-shrink: 0;
	}

	.enable-toggle input { accent-color: #a855f7; width: 12px; height: 12px; }

	.enable-text {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.04em;
		color: var(--text-secondary);
	}

	.controls {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.sliders-row {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 3px 8px;
	}

	.param-row {
		display: grid;
		grid-template-columns: 28px 1fr 22px;
		align-items: center;
		gap: 3px;
	}

	.param-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		text-align: right;
	}

	.param-slider {
		width: 100%;
		min-width: 0;
		height: 2px;
		accent-color: #a855f7;
	}

	.param-val {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		text-align: right;
	}

	.bg-row {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: wrap;
	}

	.mode-seg {
		display: flex;
	}

	.seg-btn {
		padding: 2px 6px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}
	.seg-btn:first-child { border-radius: var(--radius-sm) 0 0 var(--radius-sm); }
	.seg-btn:last-child { border-radius: 0 var(--radius-sm) var(--radius-sm) 0; }
	.seg-btn:not(:first-child) { border-left: none; }
	.seg-btn:hover:not(.active) { background: var(--bg-hover); color: var(--text-primary); }
	.seg-btn.active { background: rgba(168, 85, 247, 0.15); color: #a855f7; border-color: rgba(168, 85, 247, 0.4); }

	.bg-slider {
		flex: 1;
		min-width: 40px;
		max-width: 80px;
	}

	.color-swatch {
		width: 20px;
		height: 20px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 0;
		cursor: pointer;
		background: none;
	}

	.action-row {
		display: flex;
		gap: 3px;
	}

	.apply-btn {
		flex: 1;
		padding: 3px 8px;
		background: rgba(168, 85, 247, 0.12);
		color: #a855f7;
		border: 1px solid rgba(168, 85, 247, 0.35);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.apply-btn:hover {
		background: rgba(168, 85, 247, 0.22);
	}

	.remove-btn {
		padding: 3px 8px;
		background: rgba(239, 68, 68, 0.1);
		color: var(--color-error);
		border: 1px solid rgba(239, 68, 68, 0.25);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.remove-btn:hover {
		background: rgba(239, 68, 68, 0.2);
	}
</style>
