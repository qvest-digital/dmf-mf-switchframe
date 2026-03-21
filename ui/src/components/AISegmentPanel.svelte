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
		{#if inferenceMs !== undefined}
			<span class="latency-badge">{inferenceMs}ms</span>
		{/if}
	</div>

	<div class="source-select">
		<label class="field-label"
			>Source
			<select
				class="ai-select"
				value={activeSource}
				onchange={(e) => selectSource((e.target as HTMLSelectElement).value)}
			>
				{#each sourceKeys as key}
					<option value={key}>{crState.sources[key]?.label || key}</option>
				{/each}
			</select>
		</label>
	</div>

	<label class="enable-label">
		<input type="checkbox" bind:checked={enabled} />
		Enable AI Background Removal
	</label>

	{#if enabled}
		<div class="controls">
			<div class="slider-group">
				<label class="slider-label"
					>Sensitivity: {sensitivity}%
					<input
						type="range"
						min="0"
						max="100"
						step="1"
						bind:value={sensitivity}
						class="slider slider--purple"
					/>
				</label>
			</div>

			<div class="slider-group">
				<label class="slider-label"
					>Edge Smoothing: {edgeSmooth}%
					<input
						type="range"
						min="0"
						max="100"
						step="1"
						bind:value={edgeSmooth}
						class="slider slider--purple"
					/>
				</label>
			</div>

			<div class="bg-mode-group">
				<span class="field-label">Background</span>
				<div class="mode-buttons">
					<button
						class="mode-btn"
						class:active={bgMode === 'transparent'}
						onclick={() => (bgMode = 'transparent')}
					>
						Transparent
					</button>
					<button
						class="mode-btn"
						class:active={bgMode === 'blur'}
						onclick={() => (bgMode = 'blur')}
					>
						Blur
					</button>
					<button
						class="mode-btn"
						class:active={bgMode === 'color'}
						onclick={() => (bgMode = 'color')}
					>
						Color
					</button>
				</div>
			</div>

			{#if bgMode === 'blur'}
				<div class="slider-group">
					<label class="slider-label"
						>Blur Radius: {blurRadius}
						<input
							type="range"
							min="1"
							max="50"
							step="1"
							bind:value={blurRadius}
							class="slider slider--purple"
						/>
					</label>
				</div>
			{/if}

			{#if bgMode === 'color'}
				<div class="color-group">
					<span class="field-label">Background Color</span>
					<div class="color-row">
						<button
							class="preset-btn"
							onclick={() => (bgColor = '#00ff00')}
							style="background: #00ff00; color: #000;"
						>
							Green
						</button>
						<button
							class="preset-btn"
							onclick={() => (bgColor = '#0000ff')}
							style="background: #0000ff; color: #fff;"
						>
							Blue
						</button>
						<input
							type="color"
							bind:value={bgColor}
							class="color-swatch"
							title="Pick background color"
						/>
					</div>
				</div>
			{/if}
		</div>
	{/if}

	<div class="action-row">
		<button class="apply-btn" onclick={applySegment}>
			{enabled ? 'Apply' : 'Apply (disabled)'}
		</button>
		{#if activeSource}
			<button class="remove-btn" onclick={removeSegment}>Remove</button>
		{/if}
	</div>
</div>

<style>
	.ai-panel {
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
		gap: 6px;
		padding: 0 2px;
	}

	.panel-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
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
		padding: 1px 5px;
	}

	.latency-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		margin-left: auto;
	}

	.field-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-tertiary);
		margin-bottom: 2px;
		display: block;
	}

	.source-select {
		display: flex;
		flex-direction: column;
	}

	.ai-select {
		width: 100%;
		padding: 4px 6px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
	}

	.enable-label {
		display: flex;
		align-items: center;
		gap: 6px;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		cursor: pointer;
	}

	.controls {
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
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.slider {
		width: 100%;
		height: 14px;
	}

	.slider--purple {
		accent-color: #a855f7;
	}

	.bg-mode-group {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.mode-buttons {
		display: flex;
		gap: 2px;
	}

	.mode-btn {
		flex: 1;
		padding: 4px 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.mode-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.mode-btn.active {
		background: rgba(168, 85, 247, 0.15);
		color: #a855f7;
		border-color: rgba(168, 85, 247, 0.4);
	}

	.color-group {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.color-row {
		display: flex;
		gap: 4px;
		align-items: center;
	}

	.preset-btn {
		padding: 4px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition: opacity var(--transition-fast);
	}

	.preset-btn:hover {
		opacity: 0.85;
	}

	.color-swatch {
		width: 28px;
		height: 28px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 0;
		cursor: pointer;
		background: none;
	}

	.action-row {
		display: flex;
		gap: 4px;
		margin-top: 2px;
	}

	.apply-btn {
		flex: 1;
		padding: 6px;
		background: rgba(168, 85, 247, 0.15);
		color: #a855f7;
		border: 1px solid rgba(168, 85, 247, 0.4);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.apply-btn:hover {
		background: rgba(168, 85, 247, 0.25);
	}

	.remove-btn {
		padding: 6px 10px;
		background: rgba(239, 68, 68, 0.15);
		color: var(--color-error);
		border: 1px solid rgba(239, 68, 68, 0.3);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.remove-btn:hover {
		background: rgba(239, 68, 68, 0.25);
	}
</style>
