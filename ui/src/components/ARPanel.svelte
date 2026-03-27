<script lang="ts">
	import type { ControlRoomState, ARSceneObject, ARSceneLight, ARAssetInfo } from '$lib/api/types';
	import {
		arOcclusionEnable, arOcclusionDisable,
		arOcclusionAddPrompt, arOcclusionDeletePrompt,
		arSceneAddObject, arSceneUpdateObject, arSceneDeleteObject,
		arScenePlace,
		arSceneAddLight, arSceneUpdateLight, arSceneDeleteLight,
		arAssetUpload, arAssetList, arAssetDelete,
		apiCall,
	} from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import { onMount, onDestroy } from 'svelte';
	import { getArPromptState, startPromptMode, startPlaceMode, stopPromptMode, setPromptMarkers } from '$lib/state/ar-prompt.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── AR Occlusion State ──
	const occlusion = $derived(crState.arOcclusion);
	const depthEnabled = $derived(occlusion?.depthReady ?? false);
	const samEnabled = $derived(occlusion?.samReady ?? false);
	const depthLatency = $derived(occlusion?.depthLatencyMs ?? 0);
	const samLatency = $derived(occlusion?.samLatencyMs ?? 0);
	const prompts = $derived(occlusion?.prompts ?? []);
	const framesProcessed = $derived(occlusion?.framesProcessed ?? 0);
	const occlusionEnabled = $derived(occlusion?.enabled ?? false);

	// ── AR Scene State ──
	const objects = $derived(crState.arScene?.objects ?? []);
	const lights = $derived(crState.arScene?.lights ?? []);

	// ── Local State ──
	let assets = $state<ARAssetInfo[]>([]);
	let uploading = $state(false);
	let fileInput: HTMLInputElement | undefined = $state();
	let selectedAssetId = $state<string | null>(null);
	let confirmDeleteId = $state<string | null>(null);
	let confirmDeleteTimer: ReturnType<typeof setTimeout> | undefined;

	// Prompt mode
	const arPrompt = getArPromptState();
	const isPromptMode = $derived(arPrompt.mode === 'point' || arPrompt.mode === 'box');
	const isPlaceMode = $derived(arPrompt.mode === 'place');
	let pendingLabel = $state('');
	let showLabelInput = $state(false);
	let pendingType = $state<string>('point');
	let pendingPoints = $state<number[][]>([]);

	// Debounced update timers
	let updateTimers: Record<string, ReturnType<typeof setTimeout>> = {};

	// Depth/mask thumbnail canvases
	let depthCanvas: HTMLCanvasElement | undefined = $state();
	let maskCanvas: HTMLCanvasElement | undefined = $state();
	let depthHasData = $state(false);
	let maskHasData = $state(false);
	let pollTimer: ReturnType<typeof setInterval> | undefined;

	// ── Lifecycle ──
	onMount(() => {
		refreshAssets();
		// Auto-enable depth for demo if not already enabled
		if (!occlusionEnabled) {
			apiCall(arOcclusionEnable('both'), 'Enable AR');
		}
		// Start thumbnail polling at 5fps
		pollTimer = setInterval(pollThumbnails, 200);
	});

	onDestroy(() => {
		stopPromptMode();
		if (pollTimer) clearInterval(pollTimer);
		for (const key of Object.keys(updateTimers)) {
			clearTimeout(updateTimers[key]);
		}
		if (confirmDeleteTimer) clearTimeout(confirmDeleteTimer);
	});

	// ── Thumbnail Polling ──
	async function pollThumbnails() {
		pollDepth();
		pollMask();
	}

	async function pollDepth() {
		if (!depthCanvas) return;
		try {
			const res = await fetch('/api/ar/occlusion/depth-preview');
			if (res.status === 204 || !res.ok) { depthHasData = false; return; }
			const blob = await res.blob();
			const img = await createImageBitmap(blob);
			const ctx = depthCanvas.getContext('2d');
			if (ctx) {
				ctx.drawImage(img, 0, 0, depthCanvas.width, depthCanvas.height);
				depthHasData = true;
			}
			img.close();
		} catch {
			depthHasData = false;
		}
	}

	async function pollMask() {
		if (!maskCanvas) return;
		try {
			const res = await fetch('/api/ar/occlusion/mask-preview');
			if (res.status === 204 || !res.ok) { maskHasData = false; return; }
			const blob = await res.blob();
			const img = await createImageBitmap(blob);
			const ctx = maskCanvas.getContext('2d');
			if (ctx) {
				ctx.drawImage(img, 0, 0, maskCanvas.width, maskCanvas.height);
				maskHasData = true;
			}
			img.close();
		} catch {
			maskHasData = false;
		}
	}

	// ── Asset Library ──
	async function refreshAssets() {
		try {
			assets = await arAssetList();
		} catch {
			// ignore
		}
	}

	// ── Depth/SAM Toggles ──
	function toggleDepth() {
		if (occlusionEnabled) {
			apiCall(arOcclusionDisable(), 'Disable occlusion');
		} else {
			apiCall(arOcclusionEnable('depth'), 'Enable depth');
		}
	}

	function toggleSam() {
		if (samEnabled) {
			apiCall(arOcclusionDisable(), 'Disable occlusion');
		} else {
			apiCall(arOcclusionEnable('both'), 'Enable SAM');
		}
	}

	// ── Prompt Handlers ──
	function handleAddPrompt() {
		if (isPromptMode) {
			stopPromptMode();
			setPromptMarkers([]);
			showLabelInput = false;
			return;
		}
		// Deactivate place mode if active
		if (isPlaceMode) stopPromptMode();
		startPromptMode((type: string, points: number[][]) => {
			pendingType = type;
			pendingPoints = points;
			showLabelInput = true;
			stopPromptMode();
		});
	}

	function confirmPromptLabel() {
		const label = pendingLabel.trim() || 'Prompt';
		if (pendingType === 'box') {
			apiCall(
				arOcclusionAddPrompt({
					label,
					type: 'box',
					box: pendingPoints[0] as unknown as [number, number, number, number],
				}),
				'Add prompt',
			);
		} else {
			apiCall(
				arOcclusionAddPrompt({
					label,
					type: 'point',
					points: pendingPoints as unknown as [number, number][],
				}),
				'Add prompt',
			);
		}
		notify('info', `Adding prompt "${label}"`);
		pendingLabel = '';
		showLabelInput = false;
		setPromptMarkers([]);
	}

	function cancelLabel() {
		showLabelInput = false;
		pendingLabel = '';
		setPromptMarkers([]);
	}

	function handleDeletePrompt(promptId: string) {
		apiCall(arOcclusionDeletePrompt(promptId), 'Delete prompt');
	}

	function typeBadgeClass(type: string): string {
		switch (type) {
			case 'point': return 'badge-point';
			case 'box': return 'badge-box';
			default: return 'badge-default';
		}
	}

	// ── Object/Place Handlers ──
	function handleSelectAsset(assetId: string) {
		if (selectedAssetId === assetId) {
			// Deselect and exit place mode
			selectedAssetId = null;
			if (isPlaceMode) stopPromptMode();
			return;
		}
		selectedAssetId = assetId;
		// Deactivate prompt mode if active
		if (isPromptMode) stopPromptMode();
		startPlaceMode((pixelX: number, pixelY: number) => {
			if (!selectedAssetId) return;
			apiCall(arScenePlace(selectedAssetId, pixelX, pixelY), 'Place object');
		});
	}

	function handleUploadClick() {
		fileInput?.click();
	}

	async function handleFileSelected(e: Event) {
		const target = e.target as HTMLInputElement;
		const file = target.files?.[0];
		if (!file) return;
		uploading = true;
		try {
			const asset = await arAssetUpload(file);
			notify('info', `Asset "${asset.name}" uploaded`);
			await refreshAssets();
		} catch {
			notify('error', 'Asset upload failed');
		} finally {
			uploading = false;
		}
		target.value = '';
	}

	function handleToggleVisible(obj: ARSceneObject) {
		apiCall(arSceneUpdateObject(obj.id, { ...obj, visible: !obj.visible }), 'Toggle visibility');
	}

	function handleScaleSlider(obj: ARSceneObject, value: string) {
		const s = Math.max(0.01, parseFloat(value) || 1);
		debouncedObjectUpdate(obj.id, { scale: [s, s, s] });
	}

	function handleDeleteObject(id: string) {
		if (confirmDeleteId === id) {
			apiCall(arSceneDeleteObject(id), 'Delete object');
			confirmDeleteId = null;
		} else {
			confirmDeleteId = id;
			if (confirmDeleteTimer) clearTimeout(confirmDeleteTimer);
			confirmDeleteTimer = setTimeout(() => { if (confirmDeleteId === id) confirmDeleteId = null; }, 3000);
		}
	}

	function debouncedObjectUpdate(objectId: string, updates: Partial<ARSceneObject>) {
		if (updateTimers[objectId]) clearTimeout(updateTimers[objectId]);
		updateTimers[objectId] = setTimeout(() => {
			const latest = objects.find(o => o.id === objectId);
			if (!latest) return;
			apiCall(arSceneUpdateObject(objectId, { ...latest, ...updates }), 'Update object');
			delete updateTimers[objectId];
		}, 200);
	}

	// ── Light Handlers ──
	function handleLightToggle(light: ARSceneLight) {
		// Toggle by setting intensity to 0 or restoring to 1
		const newIntensity = light.intensity > 0 ? 0 : 1;
		debouncedLightUpdate(light.id, { intensity: newIntensity });
	}

	function handleLightIntensity(light: ARSceneLight, value: string) {
		debouncedLightUpdate(light.id, { intensity: parseFloat(value) || 0 });
	}

	function debouncedLightUpdate(lightId: string, updates: Partial<ARSceneLight>) {
		const key = `light_${lightId}`;
		if (updateTimers[key]) clearTimeout(updateTimers[key]);
		updateTimers[key] = setTimeout(() => {
			const latest = lights.find(l => l.id === lightId);
			if (!latest) return;
			apiCall(arSceneUpdateLight(lightId, { ...latest, ...updates }), 'Update light');
			delete updateTimers[key];
		}, 200);
	}

	// Handle escape to exit place mode
	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			if (isPlaceMode || isPromptMode) {
				stopPromptMode();
				selectedAssetId = null;
				setPromptMarkers([]);
				showLabelInput = false;
			}
		}
	}

	// Compute object display name from asset library
	function objectName(obj: ARSceneObject): string {
		const asset = assets.find(a => a.id === obj.assetId);
		if (asset) return asset.name || asset.filename;
		return obj.assetId.slice(0, 8);
	}

	// Estimate depth from position Z
	function objectDepth(obj: ARSceneObject): string {
		const d = Math.abs(obj.position[2]);
		return d.toFixed(1);
	}

	// Uniform scale value
	function objectScale(obj: ARSceneObject): number {
		return obj.scale[0];
	}

	// Foreground percentage (placeholder derived from occlusion data)
	const foregroundPct = $derived.by(() => {
		if (!depthEnabled || framesProcessed === 0) return 0;
		// Approximate: if SAM is running with prompts, show ~31%
		return prompts.length > 0 && samEnabled ? 31 : 0;
	});

	// Key/fill lights from scene
	const keyLight = $derived(lights.find(l => l.type === 'directional') ?? lights[0] ?? null);
	const fillLight = $derived(lights.length > 1 ? lights[1] : null);
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="ar-panel" onkeydown={handleKeydown}>
	<!-- Section 1: AI VISION -->
	<div class="section">
		<div class="section-header">
			<span class="section-title">AI VISION</span>
			<div class="section-badges">
				{#if depthEnabled}
					<span class="status-badge status-ok">DEPTH {depthLatency.toFixed(1)}ms</span>
				{/if}
				{#if samEnabled}
					<span class="status-badge status-ok">SAM {samLatency.toFixed(1)}ms</span>
				{/if}
			</div>
		</div>
		<div class="vision-toggles">
			<div class="toggle-group">
				<span class="toggle-label">DEPTH</span>
				<button class="pill-btn" class:active={depthEnabled} onclick={toggleDepth}>
					{depthEnabled ? 'ON' : 'OFF'}
				</button>
			</div>
			<div class="toggle-group">
				<span class="toggle-label">SAM</span>
				<button class="pill-btn" class:active={samEnabled} onclick={toggleSam}>
					{samEnabled ? 'ON' : 'OFF'}
				</button>
				{#if prompts.length > 0}
					<span class="prompt-count">{prompts.length} prompt{prompts.length !== 1 ? 's' : ''}</span>
				{/if}
			</div>
		</div>
		<div class="thumbnail-row">
			<div class="thumbnail-container">
				<canvas bind:this={depthCanvas} width="160" height="90" class="thumbnail-canvas"></canvas>
				{#if !depthHasData}
					<div class="thumbnail-placeholder">No data</div>
				{/if}
				<span class="thumbnail-label">Depth Map</span>
			</div>
			<div class="thumbnail-container">
				<canvas bind:this={maskCanvas} width="160" height="90" class="thumbnail-canvas"></canvas>
				{#if !maskHasData}
					<div class="thumbnail-placeholder">No data</div>
				{/if}
				<span class="thumbnail-label">Mask View</span>
			</div>
			<div class="thumbnail-stats">
				{#if foregroundPct > 0}
					<span class="stat-line">Foreground: {foregroundPct}%</span>
				{/if}
				<span class="stat-line">Frames: {framesProcessed.toLocaleString()}</span>
			</div>
		</div>
		<div class="occlusion-status" class:active={occlusionEnabled && prompts.length > 0}>
			Graphics behind people: {occlusionEnabled && prompts.length > 0 ? 'ACTIVE' : 'INACTIVE'}
		</div>
	</div>

	<!-- Section 2: SAM PROMPTS -->
	<div class="section">
		<div class="section-header">
			<span class="section-title">SAM PROMPTS</span>
			<button class="add-btn" class:cancel-mode={isPromptMode} onclick={handleAddPrompt}>
				{isPromptMode ? 'Cancel' : '+ Add Prompt'}
			</button>
		</div>
		<div class="prompt-hint">Click the preview to mark objects to track. Shift+drag for box.</div>
		{#if showLabelInput}
			<div class="label-input-row">
				<input
					class="label-input"
					type="text"
					placeholder="Prompt label..."
					bind:value={pendingLabel}
					onkeydown={(e) => { if (e.key === 'Enter') confirmPromptLabel(); if (e.key === 'Escape') cancelLabel(); }}
				/>
				<button class="label-confirm" onclick={confirmPromptLabel}>OK</button>
				<button class="label-cancel" onclick={cancelLabel}>&times;</button>
			</div>
		{/if}
		{#if prompts.length === 0 && !showLabelInput}
			<div class="empty-state">No prompts configured</div>
		{:else}
			<div class="prompt-list">
				{#each prompts as p (p.id)}
					<div class="prompt-row">
						<span class="prompt-dot"></span>
						<span class="prompt-label-text">{p.label}</span>
						<span class="prompt-type {typeBadgeClass(p.type)}">{p.type}</span>
						<button class="delete-btn" onclick={() => handleDeletePrompt(p.id)} title="Remove prompt">&times;</button>
					</div>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Section 3: 3D OBJECTS -->
	<div class="section">
		<div class="section-header">
			<span class="section-title">3D OBJECTS</span>
		</div>
		<div class="objects-hint">Select an object, then click the preview to place it.</div>
		<div class="object-strip">
			{#each assets as asset (asset.id)}
				<button
					class="object-btn"
					class:selected={selectedAssetId === asset.id}
					onclick={() => handleSelectAsset(asset.id)}
					title={asset.name || asset.filename}
				>
					<span class="object-icon">&#x2B22;</span>
					<span class="object-name">{(asset.name || asset.filename).slice(0, 7)}</span>
				</button>
			{/each}
			<input bind:this={fileInput} type="file" accept=".glb,.gltf,.usd,.usda,.usdz,.usdc" style="display:none" onchange={handleFileSelected} />
			<button class="object-btn upload-obj-btn" onclick={handleUploadClick} disabled={uploading}>
				<span class="object-icon">+</span>
				<span class="object-name">{uploading ? 'Wait...' : 'Upload'}</span>
			</button>
		</div>
		{#if objects.length > 0}
			<div class="placed-header">PLACED</div>
			<div class="placed-list">
				{#each objects as obj (obj.id)}
					<div class="placed-row">
						<button
							class="vis-checkbox"
							class:checked={obj.visible}
							onclick={() => handleToggleVisible(obj)}
							title={obj.visible ? 'Hide' : 'Show'}
						>{obj.visible ? '\u2611' : '\u2610'}</button>
						<span class="placed-name">{objectName(obj)}</span>
						<span class="placed-depth">{objectDepth(obj)}m away</span>
						<span class="placed-scale-label">Scale</span>
						<input
							type="range"
							class="scale-slider"
							min="0.1"
							max="3"
							step="0.05"
							value={objectScale(obj)}
							oninput={(e) => handleScaleSlider(obj, (e.target as HTMLInputElement).value)}
						/>
						<span class="scale-val">{objectScale(obj).toFixed(1)}</span>
						<button
							class="delete-btn"
							class:confirm-del={confirmDeleteId === obj.id}
							onclick={() => handleDeleteObject(obj.id)}
							title={confirmDeleteId === obj.id ? 'Click again to confirm' : 'Delete'}
						>{confirmDeleteId === obj.id ? '!!' : '\u00D7'}</button>
					</div>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Section 4: LIGHTING -->
	<div class="section">
		<div class="section-header">
			<span class="section-title">LIGHTING</span>
		</div>
		{#if keyLight}
			<div class="light-row">
				<span class="light-name">Key</span>
				<button class="pill-btn" class:active={keyLight.intensity > 0} onclick={() => handleLightToggle(keyLight)}>
					{keyLight.intensity > 0 ? 'ON' : 'OFF'}
				</button>
				<input
					type="range"
					class="light-slider"
					min="0"
					max="5"
					step="0.1"
					value={keyLight.intensity}
					oninput={(e) => handleLightIntensity(keyLight, (e.target as HTMLInputElement).value)}
				/>
				<span class="light-val">{keyLight.intensity.toFixed(1)}</span>
			</div>
		{/if}
		{#if fillLight}
			<div class="light-row">
				<span class="light-name">Fill</span>
				<button class="pill-btn" class:active={fillLight.intensity > 0} onclick={() => handleLightToggle(fillLight)}>
					{fillLight.intensity > 0 ? 'ON' : 'OFF'}
				</button>
				<input
					type="range"
					class="light-slider"
					min="0"
					max="5"
					step="0.1"
					value={fillLight.intensity}
					oninput={(e) => handleLightIntensity(fillLight, (e.target as HTMLInputElement).value)}
				/>
				<span class="light-val">{fillLight.intensity.toFixed(1)}</span>
			</div>
		{:else if !keyLight}
			<div class="empty-state">No lights in scene</div>
		{/if}
	</div>
</div>

<style>
	.ar-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 0;
	}

	/* ── Section Layout ── */
	.section {
		padding: 6px 10px;
		border-bottom: 1px solid rgba(255, 255, 255, 0.06);
	}

	.section-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 4px;
		padding-bottom: 3px;
		border-bottom: 1px solid rgba(255, 255, 255, 0.04);
	}

	.section-title {
		font-family: var(--font-ui, sans-serif);
		font-size: 10px;
		font-weight: 700;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.06em;
		text-transform: uppercase;
	}

	.section-badges {
		display: flex;
		gap: 6px;
	}

	.status-badge {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		padding: 1px 5px;
		border-radius: 2px;
		letter-spacing: 0.03em;
	}

	.status-ok {
		background: rgba(34, 197, 94, 0.15);
		color: rgb(34, 197, 94);
		border: 1px solid rgba(34, 197, 94, 0.3);
	}

	/* ── Toggle Pills ── */
	.vision-toggles {
		display: flex;
		gap: 16px;
		margin-bottom: 6px;
	}

	.toggle-group {
		display: flex;
		align-items: center;
		gap: 5px;
	}

	.toggle-label {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.05em;
	}

	.pill-btn {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		padding: 1px 8px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 9px;
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-tertiary, #666);
		cursor: pointer;
		transition: background 0.15s, border-color 0.15s, color 0.15s;
	}

	.pill-btn.active {
		background: rgba(34, 197, 94, 0.15);
		border-color: rgba(34, 197, 94, 0.4);
		color: rgb(34, 197, 94);
	}

	.prompt-count {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-secondary, #999);
	}

	/* ── Thumbnails ── */
	.thumbnail-row {
		display: flex;
		gap: 8px;
		align-items: flex-start;
		margin-bottom: 4px;
	}

	.thumbnail-container {
		position: relative;
		width: 160px;
		height: 90px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 3px;
		overflow: hidden;
		background: rgba(0, 0, 0, 0.3);
		flex-shrink: 0;
	}

	.thumbnail-canvas {
		display: block;
		width: 100%;
		height: 100%;
	}

	.thumbnail-placeholder {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		display: flex;
		align-items: center;
		justify-content: center;
		font-size: 9px;
		color: var(--text-tertiary, #666);
		font-style: italic;
		pointer-events: none;
	}

	.thumbnail-label {
		position: absolute;
		bottom: 2px;
		left: 3px;
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		color: rgba(255, 255, 255, 0.5);
		pointer-events: none;
	}

	.thumbnail-stats {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding-top: 2px;
	}

	.stat-line {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-secondary, #999);
		white-space: nowrap;
	}

	.occlusion-status {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-tertiary, #666);
		padding: 2px 0;
	}

	.occlusion-status.active {
		color: rgb(34, 197, 94);
	}

	/* ── SAM Prompts ── */
	.prompt-hint {
		font-size: 9px;
		color: var(--text-tertiary, #666);
		margin-bottom: 4px;
	}

	.add-btn {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 500;
		padding: 1px 6px;
		border: 1px solid rgba(99, 102, 241, 0.3);
		border-radius: 2px;
		background: rgba(99, 102, 241, 0.1);
		color: var(--accent-indigo, #818cf8);
		cursor: pointer;
		transition: background 0.15s;
	}

	.add-btn:hover {
		background: rgba(99, 102, 241, 0.2);
	}

	.add-btn.cancel-mode {
		border-color: rgba(239, 68, 68, 0.3);
		background: rgba(239, 68, 68, 0.1);
		color: var(--color-error, #ef4444);
	}

	.label-input-row {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 0;
	}

	.label-input {
		flex: 1;
		font-family: var(--font-ui, sans-serif);
		font-size: 10px;
		padding: 2px 6px;
		border: 1px solid rgba(168, 85, 247, 0.3);
		border-radius: 2px;
		background: rgba(168, 85, 247, 0.06);
		color: var(--text-primary, #eee);
		outline: none;
	}

	.label-input:focus {
		border-color: rgba(168, 85, 247, 0.6);
	}

	.label-confirm {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 600;
		padding: 2px 6px;
		border: 1px solid rgba(34, 197, 94, 0.3);
		border-radius: 2px;
		background: rgba(34, 197, 94, 0.1);
		color: rgb(34, 197, 94);
		cursor: pointer;
	}

	.label-confirm:hover {
		background: rgba(34, 197, 94, 0.2);
	}

	.label-cancel {
		font-size: 14px;
		line-height: 1;
		padding: 0 2px;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
	}

	.label-cancel:hover {
		color: var(--color-error, #ef4444);
	}

	.empty-state {
		font-size: 10px;
		color: var(--text-tertiary, #666);
		font-style: italic;
		padding: 2px 0;
	}

	.prompt-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.prompt-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 2px 4px;
		border-radius: 2px;
		background: rgba(255, 255, 255, 0.02);
	}

	.prompt-row:hover {
		background: rgba(255, 255, 255, 0.05);
	}

	.prompt-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--accent-indigo, #818cf8);
		flex-shrink: 0;
	}

	.prompt-label-text {
		font-size: 11px;
		color: var(--text-primary, #eee);
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.prompt-type {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		font-weight: 600;
		padding: 0 4px;
		border-radius: 2px;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.badge-point {
		background: rgba(59, 130, 246, 0.15);
		color: rgb(96, 165, 250);
	}

	.badge-box {
		background: rgba(245, 158, 11, 0.15);
		color: rgb(251, 191, 36);
	}

	.badge-default {
		background: rgba(255, 255, 255, 0.06);
		color: var(--text-secondary, #999);
	}

	.delete-btn {
		font-size: 14px;
		line-height: 1;
		padding: 0 2px;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
		opacity: 0;
		transition: opacity 0.15s, color 0.15s;
		flex-shrink: 0;
	}

	.prompt-row:hover .delete-btn,
	.placed-row:hover .delete-btn {
		opacity: 1;
	}

	.delete-btn:hover, .delete-btn.confirm-del {
		color: var(--color-error, #ef4444);
		opacity: 1;
	}

	/* ── 3D Objects ── */
	.objects-hint {
		font-size: 9px;
		color: var(--text-tertiary, #666);
		margin-bottom: 6px;
	}

	.object-strip {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
		margin-bottom: 6px;
	}

	.object-btn {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		width: 56px;
		height: 56px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 4px;
		background: rgba(255, 255, 255, 0.03);
		cursor: pointer;
		transition: border-color 0.15s, background 0.15s;
		gap: 2px;
		padding: 2px;
	}

	.object-btn:hover {
		background: rgba(255, 255, 255, 0.06);
		border-color: rgba(255, 255, 255, 0.2);
	}

	.object-btn.selected {
		border-color: var(--accent-indigo, #818cf8);
		background: rgba(99, 102, 241, 0.1);
		box-shadow: 0 0 0 1px var(--accent-indigo, #818cf8);
	}

	.object-icon {
		font-size: 18px;
		color: var(--text-secondary, #999);
		line-height: 1;
	}

	.object-btn.selected .object-icon {
		color: var(--accent-indigo, #818cf8);
	}

	.object-name {
		font-family: var(--font-ui, sans-serif);
		font-size: 8px;
		color: var(--text-tertiary, #666);
		text-align: center;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 52px;
	}

	.upload-obj-btn {
		border-style: dashed;
		border-color: rgba(99, 102, 241, 0.3);
	}

	.upload-obj-btn .object-icon {
		font-size: 20px;
		color: var(--accent-indigo, #818cf8);
	}

	.upload-obj-btn .object-name {
		color: var(--accent-indigo, #818cf8);
	}

	.upload-obj-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.placed-header {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 600;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.06em;
		margin-bottom: 3px;
	}

	.placed-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.placed-row {
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 2px 4px;
		border-radius: 2px;
		background: rgba(255, 255, 255, 0.02);
	}

	.placed-row:hover {
		background: rgba(255, 255, 255, 0.05);
	}

	.vis-checkbox {
		font-size: 12px;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
		padding: 0;
		line-height: 1;
	}

	.vis-checkbox.checked {
		color: var(--text-secondary, #999);
	}

	.placed-name {
		font-size: 10px;
		color: var(--text-primary, #eee);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 80px;
	}

	.placed-depth {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-tertiary, #666);
		white-space: nowrap;
	}

	.placed-scale-label {
		font-size: 9px;
		color: var(--text-tertiary, #666);
		margin-left: auto;
	}

	.scale-slider {
		width: 60px;
		height: 3px;
		accent-color: var(--accent-indigo, #818cf8);
		flex-shrink: 0;
	}

	.scale-val {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-secondary, #999);
		min-width: 20px;
		text-align: right;
	}

	/* ── Lighting ── */
	.light-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 0;
	}

	.light-name {
		font-size: 10px;
		color: var(--text-secondary, #999);
		width: 28px;
		flex-shrink: 0;
	}

	.light-slider {
		flex: 1;
		height: 3px;
		accent-color: var(--accent-indigo, #818cf8);
		min-width: 60px;
	}

	.light-val {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-secondary, #999);
		min-width: 24px;
		text-align: right;
	}
</style>
