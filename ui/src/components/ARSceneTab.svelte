<script lang="ts">
	import type { ControlRoomState, ARSceneObject, ARSceneLight, ARAssetInfo } from '$lib/api/types';
	import {
		arSceneAddObject, arSceneUpdateObject, arSceneDeleteObject,
		arSceneAddLight, arSceneUpdateLight, arSceneDeleteLight,
		arAssetUpload, arAssetList, arAssetDelete,
		apiCall,
	} from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import { onMount, onDestroy } from 'svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── State ──
	let selectedObjectId = $state<string | null>(null);
	let assets = $state<ARAssetInfo[]>([]);
	let uploading = $state(false);
	let fileInput: HTMLInputElement | undefined = $state();
	let confirmDeleteId = $state<string | null>(null);
	let confirmDeleteLightId = $state<string | null>(null);
	let confirmDeleteTimer: ReturnType<typeof setTimeout> | undefined;
	let confirmDeleteLightTimer: ReturnType<typeof setTimeout> | undefined;
	let dragOverZone = $state(false);

	// ── Derived ──
	const objects = $derived(crState.arScene?.objects ?? []);
	const lights = $derived(crState.arScene?.lights ?? []);
	const selectedObject = $derived(objects.find(o => o.id === selectedObjectId) ?? null);

	// ── Debounced update timers ──
	let updateTimers: Record<string, ReturnType<typeof setTimeout>> = {};

	function debouncedObjectUpdate(objectId: string, updates: Partial<ARSceneObject>) {
		const current = objects.find(o => o.id === objectId);
		if (!current) return;
		if (updateTimers[objectId]) clearTimeout(updateTimers[objectId]);
		updateTimers[objectId] = setTimeout(() => {
			const latest = objects.find(o => o.id === objectId);
			if (!latest) return;
			apiCall(arSceneUpdateObject(objectId, { ...latest, ...updates }), 'Update object');
			delete updateTimers[objectId];
		}, 200);
	}

	function debouncedLightUpdate(lightId: string, updates: Partial<ARSceneLight>) {
		const current = lights.find(l => l.id === lightId);
		if (!current) return;
		const key = `light_${lightId}`;
		if (updateTimers[key]) clearTimeout(updateTimers[key]);
		updateTimers[key] = setTimeout(() => {
			const latest = lights.find(l => l.id === lightId);
			if (!latest) return;
			apiCall(arSceneUpdateLight(lightId, { ...latest, ...updates }), 'Update light');
			delete updateTimers[key];
		}, 200);
	}

	// ── Load assets ──
	onMount(() => {
		refreshAssets();
	});

	onDestroy(() => {
		for (const key of Object.keys(updateTimers)) {
			clearTimeout(updateTimers[key]);
		}
		updateTimers = {};
		if (confirmDeleteTimer) clearTimeout(confirmDeleteTimer);
		if (confirmDeleteLightTimer) clearTimeout(confirmDeleteLightTimer);
	});

	async function refreshAssets() {
		try {
			assets = await arAssetList();
		} catch {
			// ignore
		}
	}

	// Auto-select first object
	$effect(() => {
		if (objects.length > 0 && !selectedObjectId) {
			selectedObjectId = objects[0].id;
		}
		if (selectedObjectId && !objects.some(o => o.id === selectedObjectId)) {
			selectedObjectId = objects.length > 0 ? objects[0].id : null;
		}
	});

	// ── Handlers ──
	async function handleUploadClick() {
		fileInput?.click();
	}

	async function handleFileSelected(e: Event) {
		const target = e.target as HTMLInputElement;
		const file = target.files?.[0];
		if (!file) return;
		await uploadFile(file);
		target.value = '';
	}

	async function uploadFile(file: File) {
		uploading = true;
		try {
			const asset = await arAssetUpload(file);
			notify('info', `Asset "${asset.name}" uploaded`);
			await refreshAssets();
			// Add to scene at origin
			const obj = await arSceneAddObject({ assetId: asset.id });
			selectedObjectId = obj.id;
		} catch {
			notify('error', 'Asset upload failed');
		} finally {
			uploading = false;
		}
	}

	function handleDrop(e: DragEvent) {
		e.preventDefault();
		dragOverZone = false;
		const file = e.dataTransfer?.files?.[0];
		if (file) uploadFile(file);
	}

	function handleDragOver(e: DragEvent) {
		e.preventDefault();
		dragOverZone = true;
	}

	function handleDragLeave() {
		dragOverZone = false;
	}

	async function handleAddFromLibrary(asset: ARAssetInfo) {
		try {
			const obj = await arSceneAddObject({ assetId: asset.id });
			selectedObjectId = obj.id;
			notify('info', `Added "${asset.name}" to scene`);
		} catch {
			notify('error', 'Failed to add object');
		}
	}

	function handleDeleteObject(id: string) {
		if (confirmDeleteId === id) {
			apiCall(arSceneDeleteObject(id), 'Delete object');
			confirmDeleteId = null;
			if (selectedObjectId === id) selectedObjectId = null;
		} else {
			confirmDeleteId = id;
			if (confirmDeleteTimer) clearTimeout(confirmDeleteTimer);
			confirmDeleteTimer = setTimeout(() => { if (confirmDeleteId === id) confirmDeleteId = null; }, 3000);
		}
	}

	function handleToggleVisible(obj: ARSceneObject) {
		apiCall(arSceneUpdateObject(obj.id, { ...obj, visible: !obj.visible }), 'Toggle visibility');
	}

	function handlePositionChange(obj: ARSceneObject, axis: number, value: string) {
		const pos: [number, number, number] = [...obj.position];
		pos[axis] = parseFloat(value) || 0;
		debouncedObjectUpdate(obj.id, { position: pos });
	}

	function handleRotationChange(obj: ARSceneObject, axis: number, value: string) {
		const rot: [number, number, number] = [...obj.rotation];
		rot[axis] = parseFloat(value) || 0;
		debouncedObjectUpdate(obj.id, { rotation: rot });
	}

	function handleScaleChange(obj: ARSceneObject, axis: number, value: string) {
		const sc: [number, number, number] = [...obj.scale];
		sc[axis] = Math.max(0.001, parseFloat(value) || 0.001);
		debouncedObjectUpdate(obj.id, { scale: sc });
	}

	// ── Light handlers ──
	async function handleAddLight() {
		try {
			await arSceneAddLight({ type: 'point', intensity: 1.0, color: [1, 1, 1] });
		} catch {
			notify('error', 'Failed to add light');
		}
	}

	function handleDeleteLight(id: string) {
		if (confirmDeleteLightId === id) {
			apiCall(arSceneDeleteLight(id), 'Delete light');
			confirmDeleteLightId = null;
		} else {
			confirmDeleteLightId = id;
			if (confirmDeleteLightTimer) clearTimeout(confirmDeleteLightTimer);
			confirmDeleteLightTimer = setTimeout(() => { if (confirmDeleteLightId === id) confirmDeleteLightId = null; }, 3000);
		}
	}

	function handleLightTypeChange(light: ARSceneLight, newType: string) {
		debouncedLightUpdate(light.id, { type: newType });
	}

	function handleLightIntensityChange(light: ARSceneLight, value: string) {
		debouncedLightUpdate(light.id, { intensity: parseFloat(value) || 0 });
	}

	function handleLightDirectionChange(light: ARSceneLight, axis: number, value: string) {
		const dir: [number, number, number] = [...light.direction];
		dir[axis] = parseFloat(value) || 0;
		debouncedLightUpdate(light.id, { direction: dir });
	}

	function handleDeleteAsset(id: string) {
		apiCall(arAssetDelete(id), 'Delete asset');
		assets = assets.filter(a => a.id !== id);
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return `${bytes}B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}K`;
		return `${(bytes / (1024 * 1024)).toFixed(1)}M`;
	}

	function formatBadge(fmt: string): string {
		return fmt.toUpperCase().replace('.', '');
	}

	const axisLabels = ['X', 'Y', 'Z'];
</script>

<div class="scene-tab">
	<!-- Left rail -->
	<div class="scene-rail">
		<!-- Objects list -->
		<div class="rail-section">
			<div class="rail-header">
				<span class="header-label">OBJECTS</span>
				<span class="header-count">{objects.length}</span>
			</div>
			<div class="object-list">
				{#each objects as obj (obj.id)}
					<div class="object-row" class:selected={selectedObjectId === obj.id} role="button" tabindex="0" onclick={() => selectedObjectId = obj.id} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') selectedObjectId = obj.id; }}>
						<button
							class="vis-toggle"
							class:hidden={!obj.visible}
							onclick={(e) => { e.stopPropagation(); handleToggleVisible(obj); }}
							title={obj.visible ? 'Hide' : 'Show'}
						>{obj.visible ? '\u25C9' : '\u25CB'}</button>
						<span class="object-name">{obj.assetId.slice(0, 8)}</span>
						<button
							class="del-btn"
							class:confirm={confirmDeleteId === obj.id}
							onclick={(e) => { e.stopPropagation(); handleDeleteObject(obj.id); }}
							title={confirmDeleteId === obj.id ? 'Click again to confirm' : 'Delete'}
						>{confirmDeleteId === obj.id ? '!!' : '\u00D7'}</button>
					</div>
				{/each}
				{#if objects.length === 0}
					<div class="empty-hint">No objects in scene</div>
				{/if}
			</div>
		</div>

		<!-- Upload -->
		<div class="rail-section">
			<input bind:this={fileInput} type="file" accept=".glb,.gltf,.usd,.usda,.usdz,.usdc" style="display:none" onchange={handleFileSelected} />
			<button class="upload-btn" onclick={handleUploadClick} disabled={uploading}>
				{uploading ? 'Uploading...' : '+ Upload'}
			</button>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="drop-zone"
				class:drag-over={dragOverZone}
				ondrop={handleDrop}
				ondragover={handleDragOver}
				ondragleave={handleDragLeave}
			>
				Drop GLB/USD here
			</div>
		</div>

		<!-- Library -->
		<div class="rail-section library-section">
			<div class="rail-header">
				<span class="header-label">LIBRARY</span>
			</div>
			<div class="asset-list">
				{#each assets as asset (asset.id)}
					<div class="asset-row">
						<div class="asset-info" role="button" tabindex="0" onclick={() => handleAddFromLibrary(asset)} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') handleAddFromLibrary(asset); }}>
							<span class="asset-name">{asset.name || asset.filename}</span>
							<span class="asset-meta">
								<span class="format-badge">{formatBadge(asset.format)}</span>
								<span class="asset-size">{formatSize(asset.byteSize)}</span>
							</span>
						</div>
						<button class="del-btn-sm" onclick={() => handleDeleteAsset(asset.id)} title="Delete asset">&times;</button>
					</div>
				{/each}
				{#if assets.length === 0}
					<div class="empty-hint">No assets uploaded</div>
				{/if}
			</div>
		</div>
	</div>

	<!-- Right detail -->
	<div class="scene-detail">
		{#if selectedObject}
			<div class="detail-section">
				<div class="detail-header">TRANSFORM</div>

				<!-- Position -->
				<div class="transform-row">
					<span class="transform-label">Position</span>
					{#each axisLabels as axis, i}
						<span class="axis-label">{axis}</span>
						<input
							type="number"
							class="num-input"
							step="0.1"
							value={selectedObject.position[i]}
							oninput={(e) => handlePositionChange(selectedObject, i, (e.target as HTMLInputElement).value)}
						/>
					{/each}
				</div>

				<!-- Rotation -->
				<div class="transform-row">
					<span class="transform-label">Rotation</span>
					{#each axisLabels as axis, i}
						<span class="axis-label">{axis}</span>
						<input
							type="number"
							class="num-input"
							step="1"
							value={selectedObject.rotation[i]}
							oninput={(e) => handleRotationChange(selectedObject, i, (e.target as HTMLInputElement).value)}
						/>
					{/each}
				</div>

				<!-- Scale -->
				<div class="transform-row">
					<span class="transform-label">Scale</span>
					{#each axisLabels as axis, i}
						<span class="axis-label">{axis}</span>
						<input
							type="number"
							class="num-input"
							step="0.1"
							value={selectedObject.scale[i]}
							oninput={(e) => handleScaleChange(selectedObject, i, (e.target as HTMLInputElement).value)}
						/>
					{/each}
				</div>

				<!-- Visible toggle -->
				<div class="toggle-row">
					<span class="toggle-label">Visible</span>
					<button
						class="toggle-btn"
						class:active={selectedObject.visible}
						onclick={() => handleToggleVisible(selectedObject)}
					>{selectedObject.visible ? 'ON' : 'OFF'}</button>
				</div>
			</div>
		{:else}
			<div class="empty-detail">Select an object</div>
		{/if}

		<!-- Lights — always visible -->
		<div class="detail-section lights-section">
			<div class="detail-header">
				<span>LIGHTS</span>
				<button class="add-light-btn" onclick={handleAddLight}>+ Add</button>
			</div>
			{#each lights as light (light.id)}
				<div class="light-row">
					<select
						class="light-type-select"
						value={light.type}
						onchange={(e) => handleLightTypeChange(light, (e.target as HTMLSelectElement).value)}
					>
						<option value="point">Point</option>
						<option value="directional">Directional</option>
						<option value="area">Area</option>
					</select>
					<div class="light-dir">
						{#each axisLabels as axis, i}
							<span class="axis-label-sm">{axis}</span>
							<input
								type="number"
								class="num-input-sm"
								step="0.1"
								value={light.direction[i]}
								oninput={(e) => handleLightDirectionChange(light, i, (e.target as HTMLInputElement).value)}
							/>
						{/each}
					</div>
					<div class="light-intensity">
						<input
							type="range"
							class="intensity-slider"
							min="0"
							max="10"
							step="0.1"
							value={light.intensity}
							oninput={(e) => handleLightIntensityChange(light, (e.target as HTMLInputElement).value)}
						/>
						<span class="intensity-val">{light.intensity.toFixed(1)}</span>
					</div>
					<button
						class="del-btn-sm"
						class:confirm={confirmDeleteLightId === light.id}
						onclick={() => handleDeleteLight(light.id)}
						title={confirmDeleteLightId === light.id ? 'Click again to confirm' : 'Delete'}
					>{confirmDeleteLightId === light.id ? '!!' : '\u00D7'}</button>
				</div>
			{/each}
			{#if lights.length === 0}
				<div class="empty-hint">No lights</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.scene-tab {
		display: flex;
		height: 100%;
		overflow: hidden;
	}

	/* ── Rail ── */
	.scene-rail {
		width: 160px;
		min-width: 160px;
		border-right: 1px solid var(--border-subtle, rgba(255,255,255,0.08));
		display: flex;
		flex-direction: column;
		overflow-y: auto;
	}

	.rail-section {
		padding: 6px 8px;
		border-bottom: 1px solid rgba(255,255,255,0.04);
	}

	.rail-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 4px;
	}

	.header-label {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 700;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.08em;
	}

	.header-count {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-tertiary, #666);
	}

	.object-list, .asset-list {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.object-row {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 4px;
		border-radius: 2px;
		cursor: pointer;
		transition: background 0.1s;
	}

	.object-row:hover {
		background: rgba(255,255,255,0.04);
	}

	.object-row.selected {
		background: rgba(99, 102, 241, 0.12);
		border-left: 2px solid var(--accent-indigo, #818cf8);
	}

	.vis-toggle {
		font-size: 10px;
		border: none;
		background: none;
		color: var(--text-secondary, #999);
		cursor: pointer;
		padding: 0;
		line-height: 1;
	}

	.vis-toggle.hidden {
		color: var(--text-tertiary, #666);
		opacity: 0.5;
	}

	.object-name {
		font-family: var(--font-mono, monospace);
		font-size: 10px;
		color: var(--text-primary, #eee);
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.del-btn {
		font-size: 12px;
		line-height: 1;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
		padding: 0 2px;
		opacity: 0;
		transition: opacity 0.1s, color 0.1s;
	}

	.object-row:hover .del-btn, .del-btn.confirm {
		opacity: 1;
	}

	.del-btn:hover, .del-btn.confirm {
		color: var(--color-error, #ef4444);
	}

	.upload-btn {
		width: 100%;
		font-family: var(--font-ui, sans-serif);
		font-size: 10px;
		font-weight: 600;
		padding: 3px 8px;
		border: 1px dashed rgba(99, 102, 241, 0.3);
		border-radius: 2px;
		background: rgba(99, 102, 241, 0.06);
		color: var(--accent-indigo, #818cf8);
		cursor: pointer;
		transition: background 0.15s;
	}

	.upload-btn:hover {
		background: rgba(99, 102, 241, 0.12);
	}

	.upload-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.drop-zone {
		margin-top: 4px;
		padding: 8px 4px;
		border: 1px dashed rgba(255,255,255,0.1);
		border-radius: 2px;
		font-size: 9px;
		color: var(--text-tertiary, #666);
		text-align: center;
		transition: border-color 0.15s, background 0.15s;
	}

	.drop-zone.drag-over {
		border-color: var(--accent-indigo, #818cf8);
		background: rgba(99, 102, 241, 0.06);
	}

	.library-section {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
	}

	.library-section .asset-list {
		flex: 1;
		overflow-y: auto;
	}

	.asset-row {
		display: flex;
		align-items: center;
		gap: 2px;
		padding: 2px 2px;
	}

	.asset-info {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 0;
		cursor: pointer;
		min-width: 0;
	}

	.asset-info:hover {
		background: rgba(255,255,255,0.03);
	}

	.asset-name {
		font-size: 10px;
		color: var(--text-primary, #eee);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.asset-meta {
		display: flex;
		gap: 4px;
		align-items: center;
	}

	.format-badge {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		font-weight: 600;
		padding: 0 3px;
		border-radius: 2px;
		background: rgba(99, 102, 241, 0.12);
		color: var(--accent-indigo, #818cf8);
		letter-spacing: 0.04em;
	}

	.asset-size {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		color: var(--text-tertiary, #666);
	}

	.del-btn-sm {
		font-size: 12px;
		line-height: 1;
		border: none;
		background: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
		padding: 0 2px;
		flex-shrink: 0;
	}

	.del-btn-sm:hover, .del-btn-sm.confirm {
		color: var(--color-error, #ef4444);
	}

	.empty-hint {
		font-size: 9px;
		color: var(--text-tertiary, #666);
		font-style: italic;
		padding: 4px 0;
	}

	/* ── Detail ── */
	.scene-detail {
		flex: 1;
		display: flex;
		flex-direction: column;
		overflow-y: auto;
		padding: 6px 10px;
		gap: 8px;
	}

	.detail-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.detail-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 700;
		color: var(--text-tertiary, #666);
		letter-spacing: 0.08em;
		padding-bottom: 2px;
		border-bottom: 1px solid rgba(255,255,255,0.04);
	}

	.transform-row {
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.transform-label {
		font-size: 10px;
		color: var(--text-secondary, #999);
		width: 52px;
		flex-shrink: 0;
	}

	.axis-label {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-tertiary, #666);
		width: 10px;
		text-align: right;
	}

	.num-input {
		width: 52px;
		font-family: var(--font-mono, monospace);
		font-size: 10px;
		padding: 1px 4px;
		border: 1px solid rgba(255,255,255,0.1);
		border-radius: 2px;
		background: rgba(255,255,255,0.04);
		color: var(--text-primary, #eee);
		outline: none;
	}

	.num-input:focus {
		border-color: rgba(99, 102, 241, 0.5);
	}

	.toggle-row {
		display: flex;
		align-items: center;
		gap: 8px;
		padding-top: 2px;
	}

	.toggle-label {
		font-size: 10px;
		color: var(--text-secondary, #999);
	}

	.toggle-btn {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		font-weight: 600;
		padding: 1px 8px;
		border: 1px solid rgba(255, 255, 255, 0.1);
		border-radius: 2px;
		background: rgba(255, 255, 255, 0.04);
		color: var(--text-tertiary, #666);
		cursor: pointer;
	}

	.toggle-btn.active {
		background: rgba(34, 197, 94, 0.15);
		border-color: rgba(34, 197, 94, 0.4);
		color: rgb(34, 197, 94);
	}

	.empty-detail {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		font-size: 10px;
		color: var(--text-tertiary, #666);
		font-style: italic;
	}

	/* ── Lights ── */
	.lights-section {
		border-top: 1px solid rgba(255,255,255,0.06);
		padding-top: 6px;
	}

	.add-light-btn {
		font-family: var(--font-ui, sans-serif);
		font-size: 9px;
		font-weight: 500;
		padding: 1px 6px;
		border: 1px solid rgba(99, 102, 241, 0.3);
		border-radius: 2px;
		background: rgba(99, 102, 241, 0.1);
		color: var(--accent-indigo, #818cf8);
		cursor: pointer;
	}

	.add-light-btn:hover {
		background: rgba(99, 102, 241, 0.2);
	}

	.light-row {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 3px 0;
		flex-wrap: wrap;
	}

	.light-type-select {
		font-family: var(--font-ui, sans-serif);
		font-size: 10px;
		padding: 1px 2px;
		border: 1px solid rgba(255,255,255,0.1);
		border-radius: 2px;
		background: rgba(255,255,255,0.04);
		color: var(--text-primary, #eee);
		outline: none;
	}

	.light-dir {
		display: flex;
		align-items: center;
		gap: 2px;
	}

	.axis-label-sm {
		font-family: var(--font-mono, monospace);
		font-size: 8px;
		color: var(--text-tertiary, #666);
	}

	.num-input-sm {
		width: 36px;
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		padding: 1px 2px;
		border: 1px solid rgba(255,255,255,0.1);
		border-radius: 2px;
		background: rgba(255,255,255,0.04);
		color: var(--text-primary, #eee);
		outline: none;
	}

	.num-input-sm:focus {
		border-color: rgba(99, 102, 241, 0.5);
	}

	.light-intensity {
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.intensity-slider {
		width: 50px;
		height: 3px;
		accent-color: var(--accent-indigo, #818cf8);
	}

	.intensity-val {
		font-family: var(--font-mono, monospace);
		font-size: 9px;
		color: var(--text-secondary, #999);
		min-width: 24px;
	}
</style>
