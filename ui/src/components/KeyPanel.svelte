<script lang="ts">
	import type { ControlRoomState, KeyConfig } from '$lib/api/types';
	import { setSourceKey, deleteSourceKey, getSourceKey, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import { rgbToYCbCr, ycbcrToHex, hexToRgb, KEY_PRESETS } from '$lib/util/color';
	import {
		ICON_KEY,
		ICON_CHEVRON_DOWN,
		ICON_CHEVRON_RIGHT,
		ICON_CHECK,
		ICON_CLOSE,
		renderIcon,
	} from '$lib/icons';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();

	let selectedSource = $derived(Object.keys(crState.sources).sort()[0] ?? '');
	let keyType = $state<'none' | 'chroma' | 'luma' | 'neural'>('none');
	let enabled = $state(true);

	// Chroma params (defaults match Green Screen preset)
	let keyColorY = $state(173);
	let keyColorCb = $state(42);
	let keyColorCr = $state(26);
	let similarity = $state(0.4);
	let smoothness = $state(0.1);
	let spillSuppress = $state(0.5);

	// Luma params
	let lowClip = $state(0.0);
	let highClip = $state(0.8);
	let softness = $state(0.1);

	// Color swatch derived from current YCbCr values
	let colorHex = $derived(ycbcrToHex(keyColorY, keyColorCb, keyColorCr));

	let advancedOpen = $state(false);

	function applyPreset(preset: (typeof KEY_PRESETS)[0]) {
		keyColorY = preset.y;
		keyColorCb = preset.cb;
		keyColorCr = preset.cr;
	}

	function handleColorInput(hex: string) {
		const { r, g, b } = hexToRgb(hex);
		const ycbcr = rgbToYCbCr(r, g, b);
		keyColorY = ycbcr.y;
		keyColorCb = ycbcr.cb;
		keyColorCr = ycbcr.cr;
	}

	let sourceKeys = $derived(Object.keys(crState.sources).sort());
	let activeSource = $state('');
	let loadGeneration = 0;

	// Initialize activeSource when sources become available
	$effect(() => {
		if (!activeSource && selectedSource) {
			activeSource = selectedSource;
		}
	});

	// Load existing key config when source changes
	$effect(() => {
		const source = activeSource;
		if (!source) return;
		const gen = ++loadGeneration;

		getSourceKey(source)
			.then((config) => {
				if (gen !== loadGeneration) return; // stale response
				keyType = config.type ?? 'none';
				enabled = config.enabled ?? true;
				if (config.type === 'chroma' || config.type === 'neural') {
					keyColorY = config.keyColorY ?? 173;
					keyColorCb = config.keyColorCb ?? 42;
					keyColorCr = config.keyColorCr ?? 26;
					similarity = config.similarity ?? 0.4;
					smoothness = config.smoothness ?? 0.1;
					spillSuppress = config.spillSuppress ?? 0.5;
				} else if (config.type === 'luma') {
					lowClip = config.lowClip ?? 0.0;
					highClip = config.highClip ?? 0.8;
					softness = config.softness ?? 0.1;
				}
			})
			.catch(() => {
				if (gen !== loadGeneration) return; // stale response
				// No key config for this source (404) — reset to defaults
				keyType = 'none';
				enabled = true;
				keyColorY = 173;
				keyColorCb = 42;
				keyColorCr = 26;
				similarity = 0.4;
				smoothness = 0.1;
				spillSuppress = 0.5;
				lowClip = 0.0;
				highClip = 0.8;
				softness = 0.1;
			});
	});

	function selectSource(key: string) {
		activeSource = key;
	}

	function applyKey() {
		if (!activeSource) return;

		if (keyType === 'none') {
			const label = crState.sources[activeSource]?.label || activeSource;
			apiCall(deleteSourceKey(activeSource), 'Remove key');
			notify('info', `Key removed from ${label}`);
			return;
		}

		const hasChromaParams = keyType === 'chroma' || keyType === 'neural';
		const config: KeyConfig = {
			type: keyType,
			enabled,
			keyColorY: hasChromaParams ? keyColorY : undefined,
			keyColorCb: hasChromaParams ? keyColorCb : undefined,
			keyColorCr: hasChromaParams ? keyColorCr : undefined,
			similarity: hasChromaParams ? similarity : undefined,
			smoothness: hasChromaParams ? smoothness : undefined,
			spillSuppress: hasChromaParams ? spillSuppress : undefined,
			lowClip: keyType === 'luma' ? lowClip : undefined,
			highClip: keyType === 'luma' ? highClip : undefined,
			softness: keyType === 'luma' ? softness : undefined,
		};

		apiCall(setSourceKey(activeSource, config), 'Set key');
		const label = crState.sources[activeSource]?.label || activeSource;
		if (enabled) {
			notify('info', `${keyType} key enabled on ${label} — composites onto program`);
		} else {
			notify('info', `${keyType} key configured on ${label} (disabled)`);
		}
	}
</script>

<div class="key-panel">
	<header class="key-panel__header">
		<span class="key-panel__header-icon" aria-hidden="true">{@html renderIcon(ICON_KEY, 14, 'fill')}</span>
		<span class="key-panel__title">UPSTREAM KEY</span>
	</header>

	<div class="key-panel__body">
		<nav class="key-panel__rail" aria-label="Sources">
			{#each sourceKeys as key (key)}
				<button
					type="button"
					class="source-pill"
					class:source-pill--active={activeSource === key}
					onclick={() => selectSource(key)}
				>
					{crState.sources[key]?.label || key}
				</button>
			{/each}
		</nav>

		<div class="key-panel__main">
			<div class="key-panel__config">
				{#if !sourceKeys.length}
					<p class="key-panel__empty">No sources available</p>
				{:else}
					<div class="config-row config-row--type">
						<span class="config-label">Type</span>
						<div class="segmented" role="group" aria-label="Key type">
							<button
								type="button"
								class="segmented__btn"
								class:segmented__btn--active={keyType === 'none'}
								onclick={() => (keyType = 'none')}
							>
								None
							</button>
							<button
								type="button"
								class="segmented__btn"
								class:segmented__btn--active={keyType === 'chroma'}
								onclick={() => (keyType = 'chroma')}
							>
								Chroma
							</button>
							<button
								type="button"
								class="segmented__btn"
								class:segmented__btn--active={keyType === 'luma'}
								onclick={() => (keyType = 'luma')}
							>
								Luma
							</button>
							{#if crState.neuralKey?.available}
								<button
									type="button"
									class="segmented__btn"
									class:segmented__btn--active={keyType === 'neural'}
									onclick={() => (keyType = 'neural')}
								>
									Neural
								</button>
							{/if}
						</div>
					</div>

					{#if keyType !== 'none'}
						<label class="enable-row">
							<input type="checkbox" bind:checked={enabled} />
							<span>Enabled</span>
						</label>
					{/if}

					{#if keyType === 'chroma' || keyType === 'neural'}
						<div class="key-block">
							<span class="config-label">Key color</span>
							<div class="chroma-top-row">
								<input
									type="color"
									value={colorHex}
									oninput={(e) => handleColorInput((e.target as HTMLInputElement).value)}
									class="color-swatch"
									title="Pick key color"
								/>
								<div class="preset-pills">
									{#each KEY_PRESETS as preset}
										<button type="button" class="preset-pill" onclick={() => applyPreset(preset)}>
											{preset.label}
										</button>
									{/each}
								</div>
							</div>

							<div class="slider-grid slider-grid--3">
								<label class="slider-cell">
									<span class="slider-cell__head">
										<span>Similarity</span>
										<span class="slider-cell__val">{similarity.toFixed(2)}</span>
									</span>
									<input type="range" min="0" max="1" step="0.01" bind:value={similarity} class="slider" />
								</label>
								<label class="slider-cell">
									<span class="slider-cell__head">
										<span>Smooth</span>
										<span class="slider-cell__val">{smoothness.toFixed(2)}</span>
									</span>
									<input type="range" min="0" max="1" step="0.01" bind:value={smoothness} class="slider" />
								</label>
								<label class="slider-cell">
									<span class="slider-cell__head">
										<span>Spill</span>
										<span class="slider-cell__val">{spillSuppress.toFixed(2)}</span>
									</span>
									<input
										type="range"
										min="0"
										max="1"
										step="0.01"
										bind:value={spillSuppress}
										class="slider"
									/>
								</label>
							</div>

							<button
								type="button"
								class="advanced-disclosure"
								aria-expanded={advancedOpen}
								onclick={() => (advancedOpen = !advancedOpen)}
							>
								<span class="advanced-disclosure__chev" aria-hidden="true">
									{#if advancedOpen}
										{@html renderIcon(ICON_CHEVRON_DOWN, 12)}
									{:else}
										{@html renderIcon(ICON_CHEVRON_RIGHT, 12)}
									{/if}
								</span>
								<span>Advanced (Y / Cb / Cr)</span>
							</button>
							{#if advancedOpen}
								<div class="advanced-grid">
									<label class="num-field">
										<span>Y</span>
										<input type="number" min="0" max="255" bind:value={keyColorY} class="num-input" />
									</label>
									<label class="num-field">
										<span>Cb</span>
										<input type="number" min="0" max="255" bind:value={keyColorCb} class="num-input" />
									</label>
									<label class="num-field">
										<span>Cr</span>
										<input type="number" min="0" max="255" bind:value={keyColorCr} class="num-input" />
									</label>
								</div>
							{/if}
						</div>
					{/if}

					{#if keyType === 'luma'}
						<div class="key-block">
							<div class="slider-grid slider-grid--3">
								<label class="slider-cell">
									<span class="slider-cell__head">
										<span>Low clip</span>
										<span class="slider-cell__val">{lowClip.toFixed(2)}</span>
									</span>
									<input type="range" min="0" max="1" step="0.01" bind:value={lowClip} class="slider" />
								</label>
								<label class="slider-cell">
									<span class="slider-cell__head">
										<span>High clip</span>
										<span class="slider-cell__val">{highClip.toFixed(2)}</span>
									</span>
									<input type="range" min="0" max="1" step="0.01" bind:value={highClip} class="slider" />
								</label>
								<label class="slider-cell">
									<span class="slider-cell__head">
										<span>Softness</span>
										<span class="slider-cell__val">{softness.toFixed(2)}</span>
									</span>
									<input type="range" min="0" max="1" step="0.01" bind:value={softness} class="slider" />
								</label>
							</div>
						</div>
					{/if}
				{/if}
			</div>

			<footer class="key-panel__footer">
				{#if keyType !== 'none'}
					<button type="button" class="footer-btn footer-btn--apply" onclick={applyKey}>
						<span class="footer-btn__icon" aria-hidden="true">{@html renderIcon(ICON_CHECK, 14)}</span>
						Apply Key
					</button>
				{:else if activeSource}
					<button type="button" class="footer-btn footer-btn--remove" onclick={applyKey}>
						<span class="footer-btn__icon" aria-hidden="true">{@html renderIcon(ICON_CLOSE, 14)}</span>
						Remove Key
					</button>
				{/if}
			</footer>
		</div>
	</div>
</div>

<style>
	.key-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		padding: 6px;
		font-family: var(--font-ui);
		background: var(--bg-base);
		box-sizing: border-box;
	}

	.key-panel__header {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
		padding: 0 2px 6px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.key-panel__header-icon {
		display: flex;
		color: var(--text-tertiary);
	}

	.key-panel__title {
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
	}

	.key-panel__body {
		display: flex;
		flex: 1;
		min-height: 0;
		gap: 8px;
		margin-top: 6px;
		overflow: hidden;
	}

	.key-panel__rail {
		width: 100px;
		min-width: 100px;
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 4px;
		overflow-y: auto;
		overflow-x: hidden;
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		box-sizing: border-box;
	}

	.source-pill {
		width: 100%;
		padding: 5px 6px;
		text-align: left;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		line-height: 1.2;
		color: var(--text-secondary);
		background: var(--bg-control);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		cursor: pointer;
		transition:
			background var(--transition-fast),
			color var(--transition-fast),
			border-color var(--transition-fast);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.source-pill:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.source-pill--active {
		background: var(--bg-elevated);
		color: var(--tally-preview);
		border-color: color-mix(in srgb, var(--tally-preview) 45%, var(--border-default));
	}

	.key-panel__main {
		display: flex;
		flex-direction: column;
		flex: 1;
		min-width: 0;
		min-height: 0;
		overflow: hidden;
	}

	.key-panel__config {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding-right: 2px;
	}

	.key-panel__empty {
		margin: 0;
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.config-label {
		display: block;
		font-size: var(--text-2xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-tertiary);
		margin-bottom: 4px;
	}

	.config-row {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.config-row--type .config-label {
		margin-bottom: 0;
	}

	.segmented {
		display: inline-flex;
		align-self: flex-start;
		border-radius: var(--radius-xs);
		border: 1px solid var(--border-default);
		overflow: hidden;
		background: var(--bg-surface);
	}

	.segmented__btn {
		padding: 4px 10px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		background: transparent;
		border: none;
		border-right: 1px solid var(--border-subtle);
		cursor: pointer;
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.segmented__btn:last-child {
		border-right: none;
	}

	.segmented__btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.segmented__btn--active {
		background: var(--bg-control);
		color: var(--accent-blue);
	}

	.enable-row {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: var(--text-xs);
		color: var(--text-secondary);
		cursor: pointer;
		user-select: none;
	}

	.key-block {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.chroma-top-row {
		display: flex;
		flex-direction: row;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}

	.color-swatch {
		width: 28px;
		height: 28px;
		flex-shrink: 0;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		padding: 0;
		cursor: pointer;
		background: none;
	}

	.preset-pills {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		flex: 1;
		min-width: 0;
	}

	.preset-pill {
		padding: 3px 8px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-secondary);
		background: var(--bg-control);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		cursor: pointer;
		transition:
			background var(--transition-fast),
			color var(--transition-fast);
		white-space: nowrap;
	}

	.preset-pill:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.slider-grid {
		display: grid;
		gap: 6px 8px;
	}

	.slider-grid--3 {
		grid-template-columns: repeat(3, minmax(0, 1fr));
	}

	.slider-cell {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}

	.slider-cell__head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 4px;
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.slider-cell__val {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		font-variant-numeric: tabular-nums;
	}

	.slider {
		width: 100%;
		height: 12px;
		margin: 0;
		accent-color: var(--accent-blue);
	}

	.advanced-disclosure {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-top: 2px;
		padding: 4px 2px;
		width: 100%;
		text-align: left;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		background: none;
		border: none;
		border-radius: var(--radius-xs);
		cursor: pointer;
		transition:
			color var(--transition-fast),
			background var(--transition-fast);
	}

	.advanced-disclosure:hover {
		color: var(--text-secondary);
		background: var(--bg-hover);
	}

	.advanced-disclosure__chev {
		display: flex;
		flex-shrink: 0;
		color: var(--text-tertiary);
	}

	.advanced-grid {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 6px;
	}

	.num-field {
		display: flex;
		flex-direction: column;
		gap: 2px;
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		min-width: 0;
	}

	.num-input {
		width: 100%;
		min-width: 0;
		padding: 3px 4px;
		box-sizing: border-box;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
	}

	.key-panel__footer {
		flex-shrink: 0;
		padding-top: 8px;
		margin-top: auto;
		border-top: 1px solid var(--border-subtle);
	}

	.footer-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 6px;
		width: 100%;
		padding: 7px 10px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		border-radius: var(--radius-sm);
		cursor: pointer;
		transition: background var(--transition-fast);
		border: 1px solid transparent;
	}

	.footer-btn__icon {
		display: flex;
		flex-shrink: 0;
	}

	.footer-btn--apply {
		background: color-mix(in srgb, var(--color-success) 18%, transparent);
		color: var(--color-success);
		border-color: color-mix(in srgb, var(--color-success) 40%, transparent);
	}

	.footer-btn--apply:hover {
		background: color-mix(in srgb, var(--color-success) 28%, transparent);
	}

	.footer-btn--remove {
		background: color-mix(in srgb, var(--color-error) 14%, transparent);
		color: var(--color-error);
		border-color: color-mix(in srgb, var(--color-error) 32%, transparent);
	}

	.footer-btn--remove:hover {
		background: color-mix(in srgb, var(--color-error) 24%, transparent);
	}
</style>
