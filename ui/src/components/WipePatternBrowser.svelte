<script lang="ts">
	import type { WipeConfig, WipePattern } from '$lib/api/types';
	import { wipeListPatterns, wipeGetPreview, wipeUploadCustom, wipeDeleteCustom, apiCall } from '$lib/api/switch-api';

	interface Props {
		config: WipeConfig;
		onConfigChange: (config: WipeConfig) => void;
	}

	let { config, onConfigChange }: Props = $props();

	let patterns = $state<WipePattern[]>([]);
	let categories = $state<string[]>([]);
	let activeCategory = $state('basic');
	let previewCache = $state<Record<string, string>>({});
	let uploading = $state(false);
	let fileInput = $state<HTMLInputElement>();
	let deleteConfirmId = $state('');
	let showModifiers = $state(false);

	// Hover animation state
	let hoveredId = $state('');
	let animFrameId = 0;
	let animStartTime = 0;
	const ANIM_DURATION = 2000; // 2s full cycle
	// Source A = dark navy, Source B = warm amber (broadcast-style contrast)
	const COLOR_A = [20, 30, 50];   // RGB dark
	const COLOR_B = [240, 180, 60]; // RGB warm

	// Decoded gradient pixel data cache: patternId → Uint8Array (grayscale pixels)
	const gradientDataCache = new Map<string, Uint8Array>();
	// Canvas refs for animated thumbs
	const canvasRefs = new Map<string, HTMLCanvasElement>();

	const filteredPatterns = $derived(
		patterns.filter(p => p.category === activeCategory)
	);

	// Load patterns on mount
	$effect(() => {
		wipeListPatterns().then(result => {
			patterns = result.categories.flatMap(c => c.patterns);
			const cats = [...new Set(patterns.map(p => p.category))];
			const order = ['basic', 'iris', 'clock', 'matrix', 'shape', 'specialty', 'mask', 'dissolve', 'custom'];
			cats.sort((a, b) => {
				const ai = order.indexOf(a);
				const bi = order.indexOf(b);
				return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi);
			});
			categories = cats;
			if (cats.length > 0 && !cats.includes(activeCategory)) {
				activeCategory = cats[0];
			}
		}).catch(err => {
			console.error('Failed to load wipe patterns:', err);
		});
	});

	// Lazy-load preview images for visible category. Each image that
	// arrives re-runs this effect; one already requested is not requested
	// again.
	const previewRequested = new Set<string>();
	$effect(() => {
		const pats = filteredPatterns;
		for (const p of pats) {
			if (!previewCache[p.id] && !previewRequested.has(p.id)) {
				previewRequested.add(p.id);
				wipeGetPreview(p.id).then(blob => {
					const url = URL.createObjectURL(blob);
					previewCache = { ...previewCache, [p.id]: url };
				}).catch(() => { previewRequested.delete(p.id); });
			}
		}
	});

	// Decode gradient PNG into raw pixel data for animation
	async function ensureGradientData(patternId: string): Promise<Uint8Array | null> {
		if (gradientDataCache.has(patternId)) return gradientDataCache.get(patternId)!;
		const blobUrl = previewCache[patternId];
		if (!blobUrl) return null;

		return new Promise((resolve) => {
			const img = new Image();
			img.onload = () => {
				const c = document.createElement('canvas');
				c.width = img.width;
				c.height = img.height;
				const ctx = c.getContext('2d')!;
				ctx.drawImage(img, 0, 0);
				const data = ctx.getImageData(0, 0, img.width, img.height).data;
				// Extract just the R channel (grayscale — R=G=B)
				const gray = new Uint8Array(img.width * img.height);
				for (let i = 0; i < gray.length; i++) {
					gray[i] = data[i * 4];
				}
				gradientDataCache.set(patternId, gray);
				resolve(gray);
			};
			img.onerror = () => resolve(null);
			img.src = blobUrl;
		});
	}

	function startHoverAnim(patternId: string) {
		hoveredId = patternId;
		animStartTime = performance.now();

		ensureGradientData(patternId).then(gray => {
			if (!gray || hoveredId !== patternId) return;
			animLoop(patternId, gray);
		});
	}

	function animLoop(patternId: string, gray: Uint8Array) {
		if (hoveredId !== patternId) return;

		const canvas = canvasRefs.get(patternId);
		if (!canvas) return;

		const ctx = canvas.getContext('2d', { alpha: false });
		if (!ctx) return;

		const w = canvas.width;
		const h = canvas.height;
		const elapsed = performance.now() - animStartTime;
		// Ping-pong: 0→1→0 over ANIM_DURATION
		const cycle = (elapsed % ANIM_DURATION) / ANIM_DURATION;
		const position = cycle < 0.5 ? cycle * 2 : 2 - cycle * 2;
		const threshold = position * 255;
		const softEdge = 12; // pixels of soft blend

		const imgData = ctx.createImageData(w, h);
		const pixels = imgData.data;

		for (let i = 0; i < gray.length; i++) {
			const g = gray[i];
			const dist = g - threshold;
			let t: number; // 0 = color A, 1 = color B
			if (dist < -softEdge) {
				t = 1;
			} else if (dist > softEdge) {
				t = 0;
			} else {
				t = (softEdge - dist) / (2 * softEdge);
			}
			const j = i * 4;
			pixels[j]     = COLOR_A[0] + (COLOR_B[0] - COLOR_A[0]) * t;
			pixels[j + 1] = COLOR_A[1] + (COLOR_B[1] - COLOR_A[1]) * t;
			pixels[j + 2] = COLOR_A[2] + (COLOR_B[2] - COLOR_A[2]) * t;
			pixels[j + 3] = 255;
		}

		ctx.putImageData(imgData, 0, 0);
		animFrameId = requestAnimationFrame(() => animLoop(patternId, gray));
	}

	function stopHoverAnim() {
		hoveredId = '';
		if (animFrameId) {
			cancelAnimationFrame(animFrameId);
			animFrameId = 0;
		}
	}

	function selectPattern(id: string) {
		onConfigChange({ ...config, pattern: id });
	}

	function updateConfig<K extends keyof WipeConfig>(key: K, value: WipeConfig[K]) {
		onConfigChange({ ...config, [key]: value });
	}

	async function handleUpload() {
		const file = fileInput?.files?.[0];
		if (!file) return;
		uploading = true;
		try {
			const pat = await wipeUploadCustom(file);
			const result = await wipeListPatterns();
			patterns = result.categories.flatMap(c => c.patterns);
			onConfigChange({ ...config, pattern: pat.id });
			if (!categories.includes('custom')) {
				categories = [...categories, 'custom'];
			}
			activeCategory = 'custom';
		} catch (err) {
			apiCall(Promise.reject(err), 'Upload wipe pattern');
		} finally {
			uploading = false;
			if (fileInput) fileInput.value = '';
		}
	}

	async function handleDelete(id: string) {
		try {
			await wipeDeleteCustom(id);
			patterns = patterns.filter(p => p.id !== id);
			if (config.pattern === id) {
				onConfigChange({ ...config, pattern: 'h-left' });
			}
		} catch (err) {
			apiCall(Promise.reject(err), 'Delete wipe pattern');
		}
		deleteConfirmId = '';
	}

	function bindCanvas(el: HTMLCanvasElement, patternId: string) {
		const id = patternId;
		canvasRefs.set(id, el);
		// Set canvas size to match the preview image
		if (previewCache[id]) {
			const img = new Image();
			img.onload = () => {
				el.width = img.width;
				el.height = img.height;
			};
			img.src = previewCache[id];
		}
		return {
			destroy() {
				canvasRefs.delete(id);
			}
		};
	}
</script>

<div class="wipe-browser">
	<!-- Category tabs -->
	<div class="wipe-categories">
		{#each categories as cat}
			<button
				class="cat-tab"
				class:active={activeCategory === cat}
				onclick={() => activeCategory = cat}
			>{cat}</button>
		{/each}
	</div>

	<!-- Pattern grid -->
	<div class="wipe-grid">
		{#each filteredPatterns as pat (pat.id)}
			<button
				class="wipe-thumb"
				class:selected={config.pattern === pat.id}
				onclick={() => selectPattern(pat.id)}
				onmouseenter={() => startHoverAnim(pat.id)}
				onmouseleave={stopHoverAnim}
				title={pat.description || pat.name}
			>
				{#if previewCache[pat.id]}
					<canvas
						class="wipe-canvas"
						class:hidden={hoveredId !== pat.id}
						use:bindCanvas={pat.id}
					></canvas>
					<img
						src={previewCache[pat.id]}
						alt={pat.name}
						class="wipe-img"
						class:hidden={hoveredId === pat.id}
					/>
				{:else}
					<span class="wipe-placeholder">{pat.name.slice(0, 3)}</span>
				{/if}
				{#if !pat.builtin}
					{#if deleteConfirmId === pat.id}
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<span class="delete-overlay" role="none">
							<span class="del-yes" role="button" tabindex="-1" onclick={(e) => { e.stopPropagation(); handleDelete(pat.id); }}>Y</span>
							<span class="del-no" role="button" tabindex="-1" onclick={(e) => { e.stopPropagation(); deleteConfirmId = ''; }}>N</span>
						</span>
					{:else}
						<!-- svelte-ignore a11y_click_events_have_key_events -->
						<span
							class="delete-badge"
							role="button"
							tabindex="-1"
							onclick={(e) => { e.stopPropagation(); deleteConfirmId = pat.id; }}
						>x</span>
					{/if}
				{/if}
			</button>
		{/each}
	</div>

	<!-- Modifier toggle + Upload -->
	<div class="wipe-actions">
		<button class="mod-toggle" onclick={() => showModifiers = !showModifiers}>
			{showModifiers ? 'Modifiers \u25B4' : 'Modifiers \u25BE'}
		</button>
		<button class="upload-btn" onclick={() => fileInput?.click()} disabled={uploading}>
			{uploading ? '...' : '+ Custom'}
		</button>
		<input bind:this={fileInput} type="file" accept=".png,.jpg,.jpeg" onchange={handleUpload} style="display:none" />
	</div>

	<!-- Modifier controls (collapsible) -->
	{#if showModifiers}
		<div class="wipe-modifiers">
			<div class="mod-row">
				<label class="mod-label">Edge</label>
				<input class="mod-range" type="range" min="0" max="0.5" step="0.005"
					value={config.soft_edge}
					oninput={(e) => updateConfig('soft_edge', parseFloat((e.target as HTMLInputElement).value))} />
				<span class="mod-value">{config.soft_edge.toFixed(3)}</span>
			</div>
			<div class="mod-row">
				<label class="mod-label">Border</label>
				<input class="mod-range" type="range" min="0" max="0.1" step="0.002"
					value={config.border_width}
					oninput={(e) => updateConfig('border_width', parseFloat((e.target as HTMLInputElement).value))} />
				<span class="mod-value">{config.border_width.toFixed(3)}</span>
			</div>
			<div class="mod-row mod-row-compact">
				<label class="mod-check">
					<input type="checkbox" checked={config.reverse}
						onchange={(e) => updateConfig('reverse', (e.target as HTMLInputElement).checked)} />
					Rev
				</label>
				<label class="mod-inline">
					Mult
					<select class="mod-select" value={config.multiply}
						onchange={(e) => updateConfig('multiply', parseInt((e.target as HTMLSelectElement).value))}>
						<option value={1}>1</option><option value={2}>2</option><option value={3}>3</option><option value={4}>4</option>
					</select>
				</label>
				<label class="mod-inline">
					X <input class="mod-num" type="number" min="-0.5" max="0.5" step="0.01" value={config.offset_x}
						oninput={(e) => updateConfig('offset_x', parseFloat((e.target as HTMLInputElement).value) || 0)} />
				</label>
				<label class="mod-inline">
					Y <input class="mod-num" type="number" min="-0.5" max="0.5" step="0.01" value={config.offset_y}
						oninput={(e) => updateConfig('offset_y', parseFloat((e.target as HTMLInputElement).value) || 0)} />
				</label>
			</div>
		</div>
	{/if}
</div>

<style>
	.wipe-browser {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.wipe-categories {
		display: flex;
		gap: 2px;
		flex-wrap: wrap;
		background: var(--bg-base);
		border-radius: var(--radius-sm);
		padding: 2px;
		border: 1px solid var(--border-subtle);
	}

	.cat-tab {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 500;
		text-transform: capitalize;
		padding: 2px 6px;
		border: none;
		border-radius: 2px;
		background: transparent;
		color: var(--text-tertiary);
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.cat-tab:hover {
		color: var(--text-secondary);
	}

	.cat-tab.active {
		background: var(--bg-elevated);
		color: var(--accent-yellow);
	}

	.wipe-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(48px, 1fr));
		gap: 3px;
		max-height: 180px;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 2px;
		scrollbar-width: thin;
	}

	.wipe-thumb {
		position: relative;
		aspect-ratio: 16 / 9;
		border: 1.5px solid var(--border-subtle);
		border-radius: 3px;
		background: var(--bg-base);
		cursor: pointer;
		padding: 0;
		overflow: hidden;
		transition: border-color var(--transition-fast);
	}

	.wipe-thumb:hover {
		border-color: var(--accent-yellow);
	}

	.wipe-thumb.selected {
		border-color: var(--accent-blue);
		box-shadow: 0 0 4px rgba(59, 130, 246, 0.3);
	}

	.wipe-img, .wipe-canvas {
		width: 100%;
		height: 100%;
		object-fit: cover;
		display: block;
	}

	.wipe-img.hidden, .wipe-canvas.hidden {
		display: none;
	}

	.wipe-placeholder {
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-tertiary);
		display: flex;
		align-items: center;
		justify-content: center;
		width: 100%;
		height: 100%;
	}

	.delete-badge {
		position: absolute;
		top: 1px;
		right: 1px;
		width: 12px;
		height: 12px;
		font-size: 8px;
		line-height: 12px;
		text-align: center;
		background: rgba(0, 0, 0, 0.6);
		color: var(--text-tertiary);
		border-radius: 50%;
		cursor: pointer;
		display: none;
	}

	.wipe-thumb:hover .delete-badge {
		display: block;
	}

	.delete-badge:hover {
		background: var(--tally-program);
		color: #fff;
	}

	.delete-overlay {
		position: absolute;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 4px;
		background: rgba(0, 0, 0, 0.7);
	}

	.del-yes, .del-no {
		font-size: 9px;
		font-weight: 600;
		padding: 2px 6px;
		border-radius: 2px;
		border: 1px solid;
		cursor: pointer;
	}

	.del-yes {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		color: var(--tally-program);
	}

	.del-no {
		border-color: var(--border-default);
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}

	.wipe-actions {
		display: flex;
		gap: 4px;
	}

	.mod-toggle {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 500;
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-tertiary);
		cursor: pointer;
		flex: 1;
	}

	.mod-toggle:hover {
		color: var(--text-secondary);
		border-color: var(--border-strong);
	}

	.wipe-modifiers {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 2px 0;
	}

	.mod-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.mod-row-compact {
		gap: 8px;
	}

	.mod-label {
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-tertiary);
		min-width: 48px;
		flex-shrink: 0;
	}

	.mod-range {
		flex: 1;
		height: 12px;
		accent-color: var(--accent-blue);
	}

	.mod-value {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		min-width: 36px;
		text-align: right;
	}

	.mod-check {
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-secondary);
		display: flex;
		align-items: center;
		gap: 3px;
		cursor: pointer;
	}

	.mod-check input {
		accent-color: var(--accent-blue);
	}

	.mod-inline {
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-tertiary);
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.mod-select {
		font-family: var(--font-mono);
		font-size: 9px;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		padding: 1px 3px;
		cursor: pointer;
	}

	.mod-num {
		width: 48px;
		font-family: var(--font-mono);
		font-size: 9px;
		background: var(--bg-elevated);
		color: var(--text-secondary);
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		padding: 1px 3px;
	}

	.mod-num:focus, .mod-select:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.upload-btn {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 500;
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: border-color var(--transition-fast), color var(--transition-fast);
	}

	.upload-btn:hover:not(:disabled) {
		border-color: var(--border-strong);
		color: var(--text-primary);
	}

	.upload-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
