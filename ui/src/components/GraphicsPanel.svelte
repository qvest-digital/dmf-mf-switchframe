<script lang="ts">
	import type { ControlRoomState, GraphicsLayerState } from '$lib/api/types';
	import {
		graphicsAddLayer, graphicsRemoveLayer,
		graphicsOn, graphicsOff, graphicsAutoOn, graphicsAutoOff,
		graphicsAnimate, graphicsAnimateStop,
		graphicsFlyIn, graphicsFlyOut, graphicsFlyOn,
		graphicsImageUpload, graphicsImageDelete, graphicsSetRect,
		graphicsTickerStart, graphicsTickerStop, graphicsTickerUpdateText,
		graphicsLoadHTML5, graphicsUpdateHTML5Data,
		graphicsHTML5Play, graphicsHTML5Stop, graphicsHTML5Next,
		graphicsUnloadHTML5, graphicsHTML5Custom,
		graphicsSequenceUpload, graphicsSequenceDelete, graphicsSequenceSetSpeed,
		setDSKFadeDuration,
		graphicsSetLayerSettings, graphicsSetLayerOrder,
		graphicsExportLayers, graphicsImportLayers,
		apiCall,
	} from '$lib/api/switch-api';
	import { onDestroy } from 'svelte';
	import {
		type SourceMode, type LayerConfig, type AnimConfig, type FlyConfig, type TickerConfig,
		DEFAULT_ANIM, DEFAULT_FLY, DEFAULT_TICKER, storedConfig, parseLayerExport,
	} from '$lib/graphics/layer-config';
	import { GraphicsPublisher } from '$lib/graphics/publisher';
	import { notify } from '$lib/state/notifications.svelte';
	import { builtinTemplates } from '$lib/graphics/templates';
	import { ICON_LAYERS, renderIcon } from '$lib/icons';
	import GraphicsLayerRail from './GraphicsLayerRail.svelte';
	import GraphicsDetail from './GraphicsDetail.svelte';

	interface Props {
		state: ControlRoomState;
		externalSelectedLayerId?: number | null;
	}
	let { state: crState, externalSelectedLayerId = null }: Props = $props();

	// ── Per-layer settings ──
	// The engine stores each layer's config and broadcasts it, so it outlives
	// this panel and is shared by every session. pendingConfigs holds edits
	// until the broadcast state carries them, so typing never flickers back.
	let pendingConfigs = $state<Record<number, LayerConfig>>({});
	const saveTimers = new Map<number, ReturnType<typeof setTimeout>>();
	const SAVE_DELAY_MS = 300;

	// ── Selection state ──
	let selectedLayerId = $state<number | null>(null);

	const publisher = new GraphicsPublisher();
	$effect(() => {
		return () => publisher.destroy();
	});

	// ── Derived ──
	const layers = $derived<GraphicsLayerState[]>(
		(crState.graphics?.layers ?? []).slice().sort((a, b) => a.zOrder - b.zOrder)
	);
	const anyActive = $derived(layers.some((l) => l.active));
	const selectedLayer = $derived(layers.find((l) => l.id === selectedLayerId) ?? null);

	// Auto-select first layer if none selected, or fall back when selected layer is removed.
	// We check pendingConfigs to avoid resetting selection for layers just added via
	// handleAddLayer whose broadcast hasn't arrived yet.
	$effect(() => {
		if (layers.length === 0) {
			selectedLayerId = null;
			return;
		}
		if (selectedLayerId === null) {
			selectedLayerId = layers[0].id;
			return;
		}
		if (!layers.some(l => l.id === selectedLayerId) && !(selectedLayerId in pendingConfigs)) {
			selectedLayerId = layers[0].id;
		}
	});

	// Sync selection from external source (e.g. overlay click on program monitor)
	$effect(() => {
		if (externalSelectedLayerId !== null && externalSelectedLayerId !== selectedLayerId && layers.some(l => l.id === externalSelectedLayerId)) {
			selectedLayerId = externalSelectedLayerId;
		}
	});

	// ── Helpers ──
	function getDefaultValues(templateId: string): Record<string, string> {
		const tpl = builtinTemplates[templateId];
		if (!tpl) return {};
		const values: Record<string, string> = {};
		for (const field of tpl.fields) {
			values[field.key] = field.defaultValue;
		}
		return values;
	}

	function layerConfig(id: number): LayerConfig {
		return pendingConfigs[id] ?? storedConfig(layers.find(l => l.id === id));
	}

	function saveConfig(id: number) {
		saveTimers.delete(id);
		const cfg = pendingConfigs[id];
		if (!cfg) return;
		graphicsSetLayerSettings(id, { config: cfg as Record<string, unknown> }).catch(() => {
			notify('error', 'Saving layer settings failed');
		});
	}

	function updateConfig(id: number, patch: LayerConfig) {
		pendingConfigs = { ...pendingConfigs, [id]: { ...layerConfig(id), ...patch } };
		const t = saveTimers.get(id);
		if (t) clearTimeout(t);
		saveTimers.set(id, setTimeout(() => saveConfig(id), SAVE_DELAY_MS));
	}

	// Drop an edit once the engine's broadcast carries it.
	$effect(() => {
		for (const key of Object.keys(pendingConfigs)) {
			const id = Number(key);
			if (saveTimers.has(id)) continue;
			const layer = layers.find(l => l.id === id);
			if (layer && JSON.stringify(storedConfig(layer)) === JSON.stringify(pendingConfigs[id])) {
				const { [id]: _, ...rest } = pendingConfigs;
				pendingConfigs = rest;
			}
		}
	});

	// Leaving the tab must not lose the last keystrokes.
	onDestroy(() => {
		for (const [id, t] of saveTimers) {
			clearTimeout(t);
			saveConfig(id);
		}
	});

	function getLayerTemplate(id: number): string {
		const tpl = layerConfig(id).template;
		if (tpl && tpl in builtinTemplates) return tpl;
		const layer = layers.find(l => l.id === id);
		if (layer?.template && layer.template in builtinTemplates) return layer.template;
		return 'lower-third';
	}

	function getLayerFields(id: number): Record<string, string> {
		return layerConfig(id).fields ?? getDefaultValues(getLayerTemplate(id));
	}

	function getLayerAnimConfig(id: number): AnimConfig {
		return { ...DEFAULT_ANIM, ...layerConfig(id).anim };
	}

	function getLayerMode(id: number): SourceMode {
		const mode = layerConfig(id).mode;
		if (mode) return mode;
		const layer = layers.find(l => l.id === id);
		if (layer?.sequenceLoaded || layer?.sequenceProcessing) return 'sequence';
		if (layer?.html5Url) return 'html5';
		if (layer?.imageName) return 'image';
		return 'template';
	}

	function getLayerFlyConfig(id: number): FlyConfig {
		return { ...DEFAULT_FLY, ...layerConfig(id).fly };
	}

	function getLayerTickerConfig(id: number): TickerConfig {
		return { ...DEFAULT_TICKER, ...layerConfig(id).ticker };
	}

	function layerTypeLabel(id: number): string {
		const layer = layers.find(l => l.id === id);
		if (layer?.html5Status) return 'HTML5';
		switch (getLayerMode(id)) {
			case 'image': return 'Image';
			case 'ticker': return 'Ticker';
			case 'html5': return 'HTML5';
			case 'sequence': return 'Sequence';
			default: return builtinTemplates[getLayerTemplate(id)]?.name ?? 'Template';
		}
	}
	const layerTypeLabels = $derived(Object.fromEntries(layers.map(l => [l.id, layerTypeLabel(l.id)])));

	let prevLayerIdSet: Set<number> | null = null;
	$effect(() => {
		const currentIds = new Set(layers.map(l => l.id));
		if (prevLayerIdSet) {
			for (const id of prevLayerIdSet) {
				if (!currentIds.has(id) && id in pendingConfigs) {
					const { [id]: _, ...rest } = pendingConfigs;
					pendingConfigs = rest;
				}
			}
		}
		prevLayerIdSet = currentIds;
	});

	// ── Handlers ──
	async function handleAddLayer() {
		try {
			const result = await graphicsAddLayer();
			// Keeps the selection on the new layer until its broadcast arrives.
			pendingConfigs = { ...pendingConfigs, [result.id]: {} };
			selectedLayerId = result.id;
		} catch {
			notify('error', 'Failed to add graphics layer');
		}
	}

	function handleRemoveLayer(id: number) {
		apiCall(graphicsRemoveLayer(id), 'Remove layer failed');
		const t = saveTimers.get(id);
		if (t) clearTimeout(t);
		saveTimers.delete(id);
	}

	async function handleCutOn(id: number) {
		const mode = getLayerMode(id);
		if (mode === 'template') {
			const tplId = getLayerTemplate(id);
			const tpl = builtinTemplates[tplId];
			if (!tpl) return;
			try {
				await publisher.publish(id, tpl, getLayerFields(id));
				apiCall(graphicsOn(id), 'Graphics failed');
			} catch {
				notify('error', 'Graphics publish failed');
			}
		} else {
			apiCall(graphicsOn(id), 'Graphics failed');
		}
	}

	async function handleAutoOn(id: number) {
		const mode = getLayerMode(id);
		if (mode === 'template') {
			const tplId = getLayerTemplate(id);
			const tpl = builtinTemplates[tplId];
			if (!tpl) return;
			try {
				await publisher.publish(id, tpl, getLayerFields(id));
				apiCall(graphicsAutoOn(id), 'Graphics failed');
			} catch {
				notify('error', 'Graphics publish failed');
			}
		} else {
			apiCall(graphicsAutoOn(id), 'Graphics failed');
		}
	}

	function handleOff(id: number) {
		apiCall(graphicsOff(id), 'Graphics failed');
	}

	function handleAutoOff(id: number) {
		apiCall(graphicsAutoOff(id), 'Graphics failed');
	}

	function handleAnimate(id: number) {
		const cfg = getLayerAnimConfig(id);
		if (cfg.mode === 'transition') {
			apiCall(graphicsAnimate(id, {
				mode: 'transition',
				toAlpha: cfg.toAlpha,
				durationMs: cfg.durationMs,
			}), 'Animation failed');
		} else {
			apiCall(graphicsAnimate(id, {
				mode: 'pulse',
				minAlpha: cfg.minAlpha,
				maxAlpha: cfg.maxAlpha,
				speedHz: cfg.speedHz,
			}), 'Animation failed');
		}
	}

	function handleAnimateStop(id: number) {
		apiCall(graphicsAnimateStop(id), 'Animation stop failed');
	}

	async function handleFlyIn(id: number) {
		const mode = getLayerMode(id);
		const cfg = getLayerFlyConfig(id);
		if (mode === 'template') {
			const tplId = getLayerTemplate(id);
			const tpl = builtinTemplates[tplId];
			if (!tpl) return;
			try {
				await publisher.publish(id, tpl, getLayerFields(id));
				apiCall(graphicsFlyOn(id, cfg.direction, cfg.durationMs), 'Fly on failed');
			} catch {
				notify('error', 'Fly on failed');
			}
		} else {
			const layer = layers.find(l => l.id === id);
			if (layer?.active) {
				apiCall(graphicsFlyIn(id, cfg.direction, cfg.durationMs), 'Fly in failed');
			} else {
				apiCall(graphicsFlyOn(id, cfg.direction, cfg.durationMs), 'Fly on failed');
			}
		}
	}

	function handleFlyOut(id: number) {
		const cfg = getLayerFlyConfig(id);
		apiCall(graphicsFlyOut(id, cfg.direction, cfg.durationMs), 'Fly out failed');
	}

	function handleReorder(bottomToTop: number[]) {
		apiCall(graphicsSetLayerOrder(bottomToTop), 'Reorder failed');
	}

	function handleRename(id: number, name: string) {
		apiCall(graphicsSetLayerSettings(id, { name }), 'Rename failed');
	}

	function handleModeChange(id: number, mode: SourceMode) {
		updateConfig(id, { mode });
	}

	function handleTemplateChange(id: number, tplId: string) {
		updateConfig(id, { template: tplId, fields: getDefaultValues(tplId) });
	}

	function handleFieldChange(id: number, key: string, value: string) {
		updateConfig(id, { fields: { ...getLayerFields(id), [key]: value } });
	}

	function handleAnimConfigChange(id: number, key: string, value: string | number) {
		updateConfig(id, { anim: { ...getLayerAnimConfig(id), [key]: value } });
	}

	function handleFlyConfigChange(id: number, key: string, value: string | number) {
		updateConfig(id, { fly: { ...getLayerFlyConfig(id), [key]: value } });
	}

	function handleTickerConfigChange(id: number, key: keyof TickerConfig, value: string | number | boolean) {
		updateConfig(id, { ticker: { ...getLayerTickerConfig(id), [key]: value } });
	}

	// ── Export / import ──
	async function handleExport() {
		try {
			const doc = await graphicsExportLayers();
			const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
			const a = document.createElement('a');
			a.href = URL.createObjectURL(blob);
			a.download = 'graphics-layers.json';
			a.click();
			URL.revokeObjectURL(a.href);
		} catch {
			notify('error', 'Graphics export failed');
		}
	}

	function handleImport() {
		const input = document.createElement('input');
		input.type = 'file';
		input.accept = '.json,application/json';
		input.onchange = async () => {
			const file = input.files?.[0];
			if (!file) return;
			await importLayersFrom(await file.text());
		};
		input.click();
	}

	async function importLayersFrom(text: string) {
		let doc;
		try {
			doc = parseLayerExport(text);
		} catch (err) {
			notify('error', `Graphics import: ${(err as Error).message}`);
			return;
		}
		try {
			await graphicsImportLayers(doc);
			pendingConfigs = {};
			notify('info', `Imported ${doc.layers.length} graphics layer${doc.layers.length === 1 ? '' : 's'}`);
		} catch (err) {
			notify('error', `Graphics import refused: ${(err as Error).message}`);
		}
	}

	// ── Image handlers ──
	async function handleImageUpload(id: number, file: File) {
		try {
			await graphicsImageUpload(id, file);
			updateConfig(id, { mode: 'image' });
			notify('info', `Image "${file.name}" uploaded`);
		} catch {
			notify('error', 'Image upload failed');
		}
	}

	function handleImageDelete(id: number) {
		apiCall(graphicsImageDelete(id), 'Image delete failed');
		updateConfig(id, { mode: 'template' });
	}

	function handleRectChange(id: number, rect: { x: number; y: number; width: number; height: number }) {
		apiCall(graphicsSetRect(id, rect), 'Position update failed');
	}

	// ── Ticker handlers ──
	let tickerRunning = $state<Record<number, boolean>>({});

	async function handleTickerStart(id: number, config: { text: string; fontSize: number; speed: number; bold: boolean; loop: boolean }) {
		try {
			await graphicsTickerStart(id, config);
			tickerRunning = { ...tickerRunning, [id]: true };
		} catch {
			notify('error', 'Ticker start failed');
		}
	}

	async function handleTickerStop(id: number) {
		try {
			await graphicsTickerStop(id);
			tickerRunning = { ...tickerRunning, [id]: false };
		} catch {
			notify('error', 'Ticker stop failed');
		}
	}

	async function handleTickerUpdateText(id: number, text: string) {
		try {
			await graphicsTickerUpdateText(id, text);
		} catch {
			notify('error', 'Ticker text update failed');
		}
	}

	// ── HTML5 handlers ──
	async function handleHTML5Load(id: number, url: string) {
		try {
			await graphicsLoadHTML5(id, url);
			updateConfig(id, { mode: 'html5' });
		} catch {
			notify('error', 'HTML5 template load failed');
		}
	}

	async function handleHTML5UpdateData(id: number, data: Record<string, unknown>) {
		try {
			await graphicsUpdateHTML5Data(id, data);
		} catch {
			notify('error', 'HTML5 data update failed');
		}
	}

	function handleHTML5Play(id: number) {
		apiCall(graphicsHTML5Play(id), 'HTML5 play failed');
	}

	function handleHTML5Stop(id: number) {
		apiCall(graphicsHTML5Stop(id), 'HTML5 stop failed');
	}

	function handleHTML5Next(id: number) {
		apiCall(graphicsHTML5Next(id), 'HTML5 next failed');
	}

	async function handleHTML5Unload(id: number) {
		try {
			await graphicsUnloadHTML5(id);
			updateConfig(id, { mode: 'template' });
		} catch {
			notify('error', 'HTML5 unload failed');
		}
	}

	async function handleHTML5Custom(id: number, action: string, payload: Record<string, unknown>) {
		try {
			await graphicsHTML5Custom(id, action, payload);
		} catch {
			notify('error', 'HTML5 custom action failed');
		}
	}

	// ── Sequence handlers ──
	async function handleSequenceUpload(id: number, file: File, fps: number) {
		try {
			await graphicsSequenceUpload(id, file, fps);
			updateConfig(id, { mode: 'sequence' });
			notify('info', 'Sequence uploading...');
		} catch {
			notify('error', 'Sequence upload failed');
		}
	}

	async function handleSequenceDelete(id: number) {
		try {
			await graphicsSequenceDelete(id);
			notify('info', 'Sequence removed');
		} catch {
			notify('error', 'Failed to remove sequence');
		}
	}

	async function handleSequenceSpeed(id: number, speed: number) {
		try {
			await graphicsSequenceSetSpeed(id, speed);
		} catch {
			notify('error', 'Failed to set speed');
		}
	}

	let dskFadeDurationInput = $state(1000);
	$effect(() => {
		const v = crState.dskFadeDurationMs;
		if (typeof v === 'number' && v !== dskFadeDurationInput) {
			dskFadeDurationInput = v;
		}
	});

	function commitDSKFadeDuration() {
		if (typeof dskFadeDurationInput !== 'number' || !Number.isFinite(dskFadeDurationInput)) {
			dskFadeDurationInput = crState.dskFadeDurationMs ?? 1000;
			return;
		}
		const clamped = Math.max(100, Math.min(5000, Math.round(dskFadeDurationInput)));
		if (clamped !== dskFadeDurationInput) dskFadeDurationInput = clamped;
		if (clamped === crState.dskFadeDurationMs) return;
		apiCall(setDSKFadeDuration(clamped), 'DSK fade duration failed');
	}
</script>

<div class="graphics-panel">
	<div class="gfx-header">
		<div class="gfx-title">
			<span class="gfx-icon">{@html renderIcon(ICON_LAYERS, 12, 'stroke')}</span>
			<span>DSK GRAPHICS</span>
		</div>
		<label class="dsk-fade-duration" title="DSK AUTO on/off fade duration (100-5000ms)">
			<span class="dsk-fade-label">FADE</span>
			<input
				type="number"
				min="100"
				max="5000"
				step="100"
				bind:value={dskFadeDurationInput}
				onblur={commitDSKFadeDuration}
				onkeydown={(e) => { if (e.key === 'Enter') (e.currentTarget as HTMLInputElement).blur(); }}
				aria-label="DSK fade duration in milliseconds"
			/>
			<span class="dsk-fade-unit">ms</span>
		</label>
		<button class="gfx-io" onclick={handleExport} disabled={layers.length === 0} title="Export every layer to a JSON file">EXPORT</button>
		<button class="gfx-io" onclick={handleImport} disabled={anyActive} title={anyActive ? 'Take every layer off air to import' : 'Replace every layer with a JSON export'}>IMPORT</button>
		<span class="gfx-status" class:on-air={anyActive}>
			{anyActive ? 'ON AIR' : 'OFF'}
		</span>
	</div>

	<div class="gfx-body">
		<GraphicsLayerRail
			{layers}
			selectedId={selectedLayerId}
			typeLabels={layerTypeLabels}
			onSelect={(id) => selectedLayerId = id}
			onAdd={handleAddLayer}
			onRemove={handleRemoveLayer}
			onReorder={handleReorder}
			onRename={handleRename}
		/>

		{#if selectedLayer}
			{#key selectedLayer.id}
				<GraphicsDetail
					layer={selectedLayer}
					sourceMode={getLayerMode(selectedLayer.id)}
					templateId={getLayerTemplate(selectedLayer.id)}
					fields={getLayerFields(selectedLayer.id)}
					animConfig={getLayerAnimConfig(selectedLayer.id)}
					flyConfig={getLayerFlyConfig(selectedLayer.id)}
					tickerConfig={getLayerTickerConfig(selectedLayer.id)}
					onTickerConfigChange={(key, val) => handleTickerConfigChange(selectedLayer.id, key, val)}
					{publisher}
					onModeChange={(mode) => handleModeChange(selectedLayer.id, mode)}
					onTemplateChange={(tplId) => handleTemplateChange(selectedLayer.id, tplId)}
					onFieldChange={(key, val) => handleFieldChange(selectedLayer.id, key, val)}
					onAnimConfigChange={(key, val) => handleAnimConfigChange(selectedLayer.id, key, val)}
					onFlyConfigChange={(key, val) => handleFlyConfigChange(selectedLayer.id, key, val)}
					onCutOn={() => handleCutOn(selectedLayer.id)}
					onAutoOn={() => handleAutoOn(selectedLayer.id)}
					onCutOff={() => handleOff(selectedLayer.id)}
					onAutoOff={() => handleAutoOff(selectedLayer.id)}
					onFlyIn={() => handleFlyIn(selectedLayer.id)}
					onFlyOut={() => handleFlyOut(selectedLayer.id)}
					onAnimate={() => handleAnimate(selectedLayer.id)}
					onAnimateStop={() => handleAnimateStop(selectedLayer.id)}
					onImageUpload={(file) => handleImageUpload(selectedLayer.id, file)}
					onImageDelete={() => handleImageDelete(selectedLayer.id)}
					onRectChange={(rect) => handleRectChange(selectedLayer.id, rect)}
					onTickerStart={(cfg) => handleTickerStart(selectedLayer.id, cfg)}
					onTickerStop={() => handleTickerStop(selectedLayer.id)}
					onTickerUpdateText={(text) => handleTickerUpdateText(selectedLayer.id, text)}
					tickerActive={tickerRunning[selectedLayer.id] ?? false}
					onHTML5Load={(url) => handleHTML5Load(selectedLayer.id, url)}
					onHTML5UpdateData={(data) => handleHTML5UpdateData(selectedLayer.id, data)}
					onHTML5Play={() => handleHTML5Play(selectedLayer.id)}
					onHTML5Stop={() => handleHTML5Stop(selectedLayer.id)}
					onHTML5Next={() => handleHTML5Next(selectedLayer.id)}
					onHTML5Unload={() => handleHTML5Unload(selectedLayer.id)}
					onHTML5Custom={(action, payload) => handleHTML5Custom(selectedLayer.id, action, payload)}
				onSequenceUpload={(file, fps) => handleSequenceUpload(selectedLayer.id, file, fps)}
				onSequenceDelete={() => handleSequenceDelete(selectedLayer.id)}
				onSequenceSpeed={(speed) => handleSequenceSpeed(selectedLayer.id, speed)}
				/>
			{/key}
		{:else}
			<div class="empty-detail">
				<span class="empty-icon">{@html renderIcon(ICON_LAYERS, 24, 'stroke')}</span>
				<span class="empty-msg">
					{#if layers.length === 0}
						Add a layer to get started
					{:else}
						Select a layer
					{/if}
				</span>
			</div>
		{/if}
	</div>
</div>

<style>
	.graphics-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		background: var(--bg-surface);
		overflow: hidden;
	}

	.gfx-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 3px 8px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
		background: var(--bg-base);
	}

	.gfx-io {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
		background: transparent;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 1px 6px;
		cursor: pointer;
	}
	.gfx-io:hover:not(:disabled) { background: var(--bg-hover); color: var(--text-primary); }
	.gfx-io:disabled { opacity: 0.35; cursor: default; }

	.gfx-title {
		display: flex;
		align-items: center;
		gap: 5px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
	}

	.gfx-icon {
		display: flex;
		color: var(--text-tertiary);
	}

	.dsk-fade-duration {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-left: auto;
		margin-right: 8px;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.dsk-fade-label {
		font-weight: 600;
		letter-spacing: 0.08em;
	}

	.dsk-fade-duration input {
		width: 54px;
		padding: 1px 4px;
		background: var(--bg-surface);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		text-align: right;
	}

	.dsk-fade-duration input:focus {
		outline: none;
		border-color: var(--accent);
	}

	.dsk-fade-unit {
		color: var(--text-tertiary);
	}

	.gfx-status {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
	}

	.gfx-status.on-air {
		color: #fff;
		background: var(--tally-program);
		animation: status-pulse 1.5s ease-in-out infinite;
	}

	@keyframes status-pulse {
		0%, 100% { box-shadow: 0 0 4px rgba(220, 38, 38, 0.3); }
		50% { box-shadow: 0 0 10px rgba(220, 38, 38, 0.6); }
	}

	.gfx-body {
		display: flex;
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}

	.empty-detail {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 6px;
	}

	.empty-icon {
		display: flex;
		color: var(--text-tertiary);
		opacity: 0.4;
	}

	.empty-msg {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}
</style>
