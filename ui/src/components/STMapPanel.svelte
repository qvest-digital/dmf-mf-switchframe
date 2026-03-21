<script lang="ts">
	import type { ControlRoomState, STMapGeneratorInfo } from '$lib/api/types';
	import { onMount } from 'svelte';
	import { notify } from '$lib/state/notifications.svelte';
	import {
		stmapGenerators,
		stmapGenerate,
		stmapUpload,
		stmapDelete,
		stmapDownload,
		stmapAssignSource,
		stmapRemoveSource,
		stmapAssignProgram,
		stmapRemoveProgram,
	} from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	const stmap = $derived(crState.stmap);
	const sources = $derived(
		Object.entries(crState.sources || {})
			.filter(([, s]) => !s.isVirtual)
			.sort(([, a], [, b]) => (a.position ?? 0) - (b.position ?? 0)),
	);
	const available = $derived(stmap?.available ?? []);
	const programMap = $derived(stmap?.program);

	let generators = $state<STMapGeneratorInfo[]>([]);
	let selectedSource = $state<string | null>(null);
	let selectedEffect = $state<string | null>(null);
	let libraryExpanded = $state(false);
	let sourceParams = $state<Record<string, number>>({});
	let sourceGenerator = $state('barrel');
	let effectParams = $state<Record<string, number>>({});
	let previewCanvas: HTMLCanvasElement | undefined = $state();
	let fileInput: HTMLInputElement | undefined = $state();

	// Effect definitions mapped to generator names
	const programEffects = [
		{ label: 'Shimmer', generator: 'heat_shimmer' },
		{ label: 'Dream', generator: 'dream' },
		{ label: 'Ripple', generator: 'ripple' },
		{ label: 'Breathe', generator: 'lens_breathe' },
		{ label: 'Vortex', generator: 'vortex' },
	] as const;

	// Static generators only (for per-source lens correction)
	const staticGenerators = $derived(generators.filter((g) => g.type === 'static'));

	// Current generator info for source detail
	const currentSourceGenerator = $derived(generators.find((g) => g.name === sourceGenerator));

	// Current generator info for effect detail
	const currentEffectGenerator = $derived(
		selectedEffect ? generators.find((g) => g.name === selectedEffect) : null,
	);

	onMount(async () => {
		try {
			const resp = await stmapGenerators();
			generators = resp.generators;
		} catch {
			/* silent -- generators just won't show */
		}

		libraryExpanded = localStorage.getItem('sf-stmap-library-expanded') === 'true';
	});

	function toggleLibrary() {
		libraryExpanded = !libraryExpanded;
		localStorage.setItem('sf-stmap-library-expanded', String(libraryExpanded));
	}

	async function handleUpload() {
		const file = fileInput?.files?.[0];
		if (!file) return;
		const name = file.name.replace(/\.(exr|png|stmap)$/i, '');
		try {
			await stmapUpload(name, file);
			notify('info', `Map "${name}" uploaded`);
		} catch (err) {
			notify('error', `Upload failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
		if (fileInput) fileInput.value = '';
	}

	async function handleDelete(name: string) {
		try {
			await stmapDelete(name);
			notify('info', 'Map deleted');
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	function selectSource(key: string) {
		if (selectedSource === key) {
			selectedSource = null;
		} else {
			selectedSource = key;
			// Reset params to defaults for current generator
			resetSourceParams();
		}
	}

	function resetSourceParams() {
		const gen = generators.find((g) => g.name === sourceGenerator);
		if (gen) {
			const defaults: Record<string, number> = {};
			for (const [k, v] of Object.entries(gen.params)) {
				defaults[k] = v.default;
			}
			sourceParams = defaults;
		}
	}

	function handleSourceGeneratorChange(name: string) {
		sourceGenerator = name;
		const gen = generators.find((g) => g.name === name);
		if (gen) {
			const defaults: Record<string, number> = {};
			for (const [k, v] of Object.entries(gen.params)) {
				defaults[k] = v.default;
			}
			sourceParams = defaults;
		}
	}

	async function applySourceMap() {
		if (!selectedSource) return;
		const sourceInfo = crState.sources[selectedSource];
		const label = sourceInfo?.label || selectedSource;
		const mapName = `${sourceGenerator}_${label.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
		try {
			await stmapGenerate({
				type: sourceGenerator,
				params: sourceParams,
				name: mapName,
				assign_source: selectedSource,
			});
			notify('info', `Applied ${sourceGenerator} to ${label}`);
		} catch (err) {
			notify('error', `Generate failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function assignExistingToSource(mapName: string) {
		if (!selectedSource) return;
		try {
			await stmapAssignSource(selectedSource, mapName);
			notify('info', `Assigned "${mapName}" to source`);
		} catch (err) {
			notify('error', `Assign failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function removeSourceMap() {
		if (!selectedSource) return;
		try {
			await stmapRemoveSource(selectedSource);
			notify('info', 'Source map removed');
		} catch (err) {
			notify('error', `Remove failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	function selectEffect(generator: string) {
		if (selectedEffect === generator) {
			selectedEffect = null;
		} else {
			selectedEffect = generator;
			// Reset effect params to defaults
			const gen = generators.find((g) => g.name === generator);
			if (gen) {
				const defaults: Record<string, number> = {};
				for (const [k, v] of Object.entries(gen.params)) {
					defaults[k] = v.default;
				}
				effectParams = defaults;
			}
		}
	}

	async function toggleProgramEffect(generator: string) {
		const isActive = programMap?.map?.startsWith(generator);
		if (isActive) {
			try {
				await stmapRemoveProgram();
				notify('info', 'Program effect removed');
				selectedEffect = null;
			} catch (err) {
				notify('error', `Remove failed: ${err instanceof Error ? err.message : 'unknown'}`);
			}
		} else {
			try {
				await stmapGenerate({
					type: generator,
					params: effectParams,
					name: `${generator}_program`,
					assign_program: true,
				});
				notify('info', `Applied ${generator} to program`);
			} catch (err) {
				notify('error', `Generate failed: ${err instanceof Error ? err.message : 'unknown'}`);
			}
		}
	}

	async function regenerateEffect() {
		if (!selectedEffect) return;
		try {
			await stmapGenerate({
				type: selectedEffect,
				params: effectParams,
				name: `${selectedEffect}_program`,
				assign_program: true,
			});
			notify('info', 'Effect regenerated');
		} catch (err) {
			notify('error', `Regenerate failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function removeProgramEffect() {
		try {
			await stmapRemoveProgram();
			notify('info', 'Program effect removed');
			selectedEffect = null;
		} catch (err) {
			notify('error', `Remove failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	// Preview canvas rendering
	$effect(() => {
		if (!previewCanvas || !selectedSource) return;
		const mapName = stmap?.sources?.[selectedSource];
		if (!mapName) {
			// Clear canvas
			const ctx = previewCanvas.getContext('2d');
			if (ctx) {
				ctx.fillStyle = '#111';
				ctx.fillRect(0, 0, 80, 60);
				ctx.fillStyle = '#555';
				ctx.font = '9px sans-serif';
				ctx.textAlign = 'center';
				ctx.fillText('No map', 40, 34);
			}
			return;
		}
		stmapDownload(mapName)
			.then((buf) => {
				renderPreview(buf);
			})
			.catch(() => {
				if (!previewCanvas) return;
				const ctx = previewCanvas.getContext('2d');
				if (ctx) {
					ctx.fillStyle = '#111';
					ctx.fillRect(0, 0, 80, 60);
					ctx.fillStyle = '#833';
					ctx.font = '9px sans-serif';
					ctx.textAlign = 'center';
					ctx.fillText('Error', 40, 34);
				}
			});
	});

	function renderPreview(buf: ArrayBuffer) {
		if (!previewCanvas) return;
		const ctx = previewCanvas.getContext('2d');
		if (!ctx) return;

		const view = new DataView(buf);
		if (buf.byteLength < 8) return;

		const w = view.getUint32(0, false); // BE
		const h = view.getUint32(4, false); // BE

		const floatCount = w * h * 2; // S and T planes
		if (buf.byteLength < 8 + floatCount * 4) return;

		const floats = new Float32Array(buf, 8, floatCount);
		const sPlane = floats.subarray(0, w * h);
		const tPlane = floats.subarray(w * h, w * h * 2);

		const cw = 80;
		const ch = 60;
		const imgData = ctx.createImageData(cw, ch);
		const data = imgData.data;
		const divisions = 8;

		for (let py = 0; py < ch; py++) {
			for (let px = 0; px < cw; px++) {
				// Map preview pixel to source map coordinates
				const sx = Math.floor((px / cw) * w);
				const sy = Math.floor((py / ch) * h);
				const idx = sy * w + sx;

				const s = sPlane[idx] ?? 0;
				const t = tPlane[idx] ?? 0;

				// Sample checkerboard
				const checker =
					(Math.floor(s * divisions) + Math.floor(t * divisions)) % 2 === 0 ? 200 : 55;

				const pi = (py * cw + px) * 4;
				data[pi] = checker;
				data[pi + 1] = checker;
				data[pi + 2] = checker;
				data[pi + 3] = 255;
			}
		}

		ctx.putImageData(imgData, 0, 0);
	}
</script>

<div class="stmap-panel">
	<!-- Zone 1: Collapsible Library -->
	<div class="zone library-zone">
		<div class="zone-header" role="button" tabindex="0" onclick={toggleLibrary} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') toggleLibrary(); }}>
			<span class="toggle-arrow">{libraryExpanded ? '\u25BE' : '\u25B8'}</span>
			<span class="zone-title">Library ({available.length})</span>
			<button
				class="upload-btn"
				onclick={(e) => { e.stopPropagation(); fileInput?.click(); }}
			>
				Upload
			</button>
			<input
				bind:this={fileInput}
				type="file"
				accept=".exr,.png,.stmap"
				class="hidden-input"
				onchange={handleUpload}
			/>
		</div>
		{#if libraryExpanded}
			<div class="library-scroll">
				{#if available.length === 0}
					<span class="empty-text">No maps stored</span>
				{:else}
					{#each available as name}
						<div class="map-card">
							<span class="map-name" title={name}>{name}</span>
							<button class="delete-btn" onclick={() => handleDelete(name)} title="Delete map">&times;</button>
						</div>
					{/each}
				{/if}
			</div>
		{/if}
	</div>

	<!-- Zone 2: Sources -->
	<div class="zone sources-zone">
		<div class="zone-label">SOURCES</div>
		<div class="source-pills">
			{#each sources as [key, info]}
				{@const hasMap = !!stmap?.sources?.[key]}
				<button
					class="source-pill"
					class:selected={selectedSource === key}
					class:has-map={hasMap}
					onclick={() => selectSource(key)}
				>
					<span class="pill-label">{info.label || key}</span>
					{#if hasMap}
						<span class="pill-map-name">{stmap?.sources?.[key]}</span>
					{/if}
				</button>
			{/each}
		</div>

		{#if selectedSource}
			{@const sourceMapName = stmap?.sources?.[selectedSource]}
			<div class="source-detail">
				<div class="detail-left">
					{#if sourceMapName}
						<!-- Map assigned: show info + remove -->
						<div class="assigned-row">
							<span class="map-badge">{sourceMapName}</span>
							<button class="action-btn remove-btn" onclick={removeSourceMap}>Remove</button>
						</div>
					{:else}
						<!-- No map: show generator + available maps -->
						{#if staticGenerators.length > 0}
							<div class="generator-row">
								<select
									class="gen-select"
									value={sourceGenerator}
									onchange={(e) => handleSourceGeneratorChange((e.target as HTMLSelectElement).value)}
								>
									{#each staticGenerators as gen}
										<option value={gen.name}>{gen.name}</option>
									{/each}
								</select>
								<button class="action-btn apply-btn" onclick={applySourceMap}>Apply</button>
							</div>
							{#if currentSourceGenerator?.params}
								<div class="param-sliders">
									{#each Object.entries(currentSourceGenerator.params) as [pname, pinfo]}
										<div class="param-row">
											<span class="param-label" title={pinfo.description}>{pname}</span>
											<input
												type="range"
												class="param-slider"
												min={pinfo.min}
												max={pinfo.max}
												step={(pinfo.max - pinfo.min) / 100}
												value={sourceParams[pname] ?? pinfo.default}
												oninput={(e) => { sourceParams = { ...sourceParams, [pname]: parseFloat((e.target as HTMLInputElement).value) }; }}
											/>
											<span class="param-value">{(sourceParams[pname] ?? pinfo.default).toFixed(2)}</span>
										</div>
									{/each}
								</div>
							{/if}
						{/if}

						{#if available.length > 0}
							<div class="existing-row">
								<select
									class="gen-select"
									onchange={(e) => { const v = (e.target as HTMLSelectElement).value; if (v) assignExistingToSource(v); (e.target as HTMLSelectElement).value = ''; }}
								>
									<option value="">Assign existing map...</option>
									{#each available as name}
										<option value={name}>{name}</option>
									{/each}
								</select>
							</div>
						{/if}
					{/if}
				</div>
				<div class="detail-right">
					<canvas bind:this={previewCanvas} class="preview-canvas" width="80" height="60"></canvas>
				</div>
			</div>
		{/if}
	</div>

	<!-- Zone 3: Program Effects -->
	<div class="zone effects-zone">
		<div class="zone-label">PROGRAM EFFECTS</div>
		<div class="effect-grid">
			{#each programEffects as eff}
				{@const isActive = programMap?.map?.startsWith(eff.generator)}
				<button
					class="effect-btn"
					class:active={isActive}
					class:selected={selectedEffect === eff.generator}
					onclick={() => {
						if (selectedEffect === eff.generator) {
							// Toggle off
							toggleProgramEffect(eff.generator);
						} else {
							selectEffect(eff.generator);
							if (!isActive) {
								toggleProgramEffect(eff.generator);
							}
						}
					}}
				>
					{eff.label}
				</button>
			{/each}
		</div>

		{#if selectedEffect && currentEffectGenerator}
			<div class="effect-detail">
				{#if currentEffectGenerator.params}
					<div class="param-sliders">
						{#each Object.entries(currentEffectGenerator.params) as [pname, pinfo]}
							<div class="param-row">
								<span class="param-label" title={pinfo.description}>{pname}</span>
								<input
									type="range"
									class="param-slider"
									min={pinfo.min}
									max={pinfo.max}
									step={(pinfo.max - pinfo.min) / 100}
									value={effectParams[pname] ?? pinfo.default}
									oninput={(e) => { effectParams = { ...effectParams, [pname]: parseFloat((e.target as HTMLInputElement).value) }; }}
								/>
								<span class="param-value">{(effectParams[pname] ?? pinfo.default).toFixed(2)}</span>
							</div>
						{/each}
					</div>
				{/if}
				<div class="effect-actions">
					<button class="action-btn" onclick={regenerateEffect}>Regenerate</button>
					<button class="action-btn remove-btn" onclick={removeProgramEffect}>Remove</button>
				</div>
			</div>
		{/if}
	</div>
</div>

<style>
	.stmap-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 6px;
		height: 100%;
		overflow-y: auto;
	}

	.zone {
		background: var(--bg-elevated);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		padding: 4px 6px;
	}

	.zone-header {
		display: flex;
		align-items: center;
		gap: 6px;
		cursor: pointer;
		user-select: none;
		padding: 2px 0;
	}

	.toggle-arrow {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		width: 10px;
	}

	.zone-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-primary);
		flex: 1;
	}

	.zone-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.05em;
		margin-bottom: 4px;
	}

	.upload-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 1px 6px;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.upload-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.hidden-input {
		display: none;
	}

	.library-scroll {
		display: flex;
		gap: 4px;
		overflow-x: auto;
		padding: 4px 0;
	}

	.empty-text {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		padding: 2px 4px;
	}

	.map-card {
		display: flex;
		align-items: center;
		gap: 4px;
		background: var(--bg-control);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		padding: 2px 6px;
		flex-shrink: 0;
	}

	.map-name {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		max-width: 100px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.delete-btn {
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		background: none;
		border: none;
		cursor: pointer;
		padding: 0 2px;
		line-height: 1;
		transition: color var(--transition-fast);
	}

	.delete-btn:hover {
		color: var(--color-error);
	}

	/* Source pills */
	.source-pills {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
	}

	.source-pill {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 1px;
		padding: 3px 8px;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast);
		min-width: 50px;
	}

	.source-pill:hover {
		background: var(--bg-hover);
	}

	.source-pill.selected {
		border-color: var(--text-secondary);
		background: var(--bg-hover);
	}

	.source-pill.has-map {
		border-color: var(--accent-blue);
	}

	.source-pill.has-map.selected {
		border-color: var(--accent-blue);
		box-shadow: 0 0 4px var(--accent-blue-dim);
	}

	.pill-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-primary);
		white-space: nowrap;
	}

	.pill-map-name {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--accent-blue);
		max-width: 60px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Source detail pane */
	.source-detail {
		display: flex;
		gap: 8px;
		margin-top: 6px;
		padding-top: 6px;
		border-top: 1px solid var(--border-subtle);
	}

	.detail-left {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;
	}

	.detail-right {
		flex-shrink: 0;
	}

	.assigned-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.map-badge {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 1px 6px;
		border-radius: var(--radius-sm);
	}

	.generator-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.existing-row {
		margin-top: 2px;
	}

	.gen-select {
		flex: 1;
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
	}

	.action-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 500;
		color: var(--text-primary);
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 8px;
		cursor: pointer;
		transition: background var(--transition-fast);
		white-space: nowrap;
	}

	.action-btn:hover {
		background: var(--bg-hover);
	}

	.action-btn.apply-btn {
		color: var(--accent-blue);
		border-color: var(--accent-blue);
	}

	.action-btn.remove-btn {
		color: var(--color-error);
		border-color: var(--color-error);
	}

	/* Param sliders */
	.param-sliders {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.param-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.param-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		min-width: 60px;
		cursor: help;
	}

	.param-slider {
		flex: 1;
		height: 12px;
		accent-color: var(--accent-blue);
	}

	.param-value {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		min-width: 36px;
		text-align: right;
	}

	/* Preview canvas */
	.preview-canvas {
		width: 80px;
		height: 60px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		image-rendering: pixelated;
	}

	/* Effect grid */
	.effect-grid {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
	}

	.effect-btn {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-secondary);
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 4px 10px;
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast),
			color var(--transition-fast);
	}

	.effect-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.effect-btn.active {
		color: var(--accent-gold);
		border-color: var(--accent-gold);
		background: var(--accent-gold-dim);
	}

	.effect-btn.selected {
		box-shadow: 0 0 4px var(--accent-gold-dim);
	}

	/* Effect detail */
	.effect-detail {
		margin-top: 6px;
		padding-top: 6px;
		border-top: 1px solid var(--border-subtle);
	}

	.effect-actions {
		display: flex;
		gap: 4px;
		margin-top: 4px;
	}
</style>
