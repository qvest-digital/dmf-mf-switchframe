<script lang="ts">
	import type { ControlRoomState, GraphicsLayerState } from '$lib/api/types';
	import {
		graphicsAddLayer, graphicsRemoveLayer,
		graphicsOn, graphicsOff, graphicsAutoOn, graphicsAutoOff,
		graphicsAnimate, graphicsAnimateStop,
		graphicsSetZOrder,
		graphicsFlyIn, graphicsFlyOut, graphicsFlyOn,
		graphicsImageUpload, graphicsImageDelete, graphicsSetRect,
		graphicsTickerStart, graphicsTickerStop, graphicsTickerUpdateText,
		graphicsTextAnimStart, graphicsTextAnimStop,
		apiCall,
	} from '$lib/api/switch-api';
	import { GraphicsPublisher } from '$lib/graphics/publisher';
	import { notify } from '$lib/state/notifications.svelte';
	import { builtinTemplates } from '$lib/graphics/templates';
	import GraphicsLayerRail from './GraphicsLayerRail.svelte';
	import GraphicsDetail from './GraphicsDetail.svelte';

	interface Props {
		state: ControlRoomState;
		externalSelectedLayerId?: number | null;
	}
	let { state: crState, externalSelectedLayerId = null }: Props = $props();

	type SourceMode = 'template' | 'image' | 'ticker' | 'textfx';

	// ── Per-layer state maps ──
	let layerModes = $state<Record<number, SourceMode>>({});
	let layerTemplates = $state<Record<number, string>>({});
	let layerFields = $state<Record<number, Record<string, string>>>({});
	let layerAnimConfigs = $state<Record<number, {
		mode: string;
		minAlpha: number;
		maxAlpha: number;
		speedHz: number;
		toAlpha: number;
		durationMs: number;
	}>>({});
	let layerFlyConfigs = $state<Record<number, { direction: string; durationMs: number }>>({});

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
	// We check layerTemplates to avoid resetting selection for layers just added via
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
		if (!layers.some(l => l.id === selectedLayerId) && !(selectedLayerId in layerTemplates)) {
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

	function getLayerTemplate(id: number): string {
		if (id in layerTemplates) return layerTemplates[id];
		// Fall back to server state for existing layers (page reload, other operator)
		const layer = layers.find(l => l.id === id);
		if (layer?.template && layer.template in builtinTemplates) return layer.template;
		return 'lower-third';
	}

	function getLayerFields(id: number): Record<string, string> {
		return layerFields[id] ?? getDefaultValues(getLayerTemplate(id));
	}

	function getLayerAnimConfig(id: number) {
		return layerAnimConfigs[id] ?? { mode: 'pulse', minAlpha: 0.3, maxAlpha: 1.0, speedHz: 1.0, toAlpha: 0.5, durationMs: 500 };
	}

	function getLayerMode(id: number): SourceMode {
		if (id in layerModes) return layerModes[id];
		// Fall back to server state for existing layers (page reload, other operator)
		const layer = layers.find(l => l.id === id);
		if (layer?.imageName) return 'image';
		return 'template';
	}

	function getLayerFlyConfig(id: number) {
		return layerFlyConfigs[id] ?? { direction: 'left', durationMs: 500 };
	}

	// Clean up local state only for layers that were actually removed from the server
	// (not layers pending broadcast after handleAddLayer).
	let prevLayerIdSet: Set<number> | null = null;
	$effect(() => {
		const currentIds = new Set(layers.map(l => l.id));
		if (prevLayerIdSet) {
			for (const id of prevLayerIdSet) {
				if (!currentIds.has(id)) {
					delete layerModes[id];
					delete layerTemplates[id];
					delete layerFields[id];
					delete layerAnimConfigs[id];
					delete layerFlyConfigs[id];
				}
			}
		}
		prevLayerIdSet = currentIds;
	});

	// ── Handlers ──
	async function handleAddLayer() {
		try {
			const result = await graphicsAddLayer();
			layerModes[result.id] = 'template';
			layerTemplates[result.id] = 'lower-third';
			layerFields[result.id] = getDefaultValues('lower-third');
			selectedLayerId = result.id;
		} catch {
			notify('error', 'Failed to add graphics layer');
		}
	}

	function handleRemoveLayer(id: number) {
		apiCall(graphicsRemoveLayer(id), 'Remove layer failed');
		delete layerModes[id];
		delete layerTemplates[id];
		delete layerFields[id];
		delete layerAnimConfigs[id];
		delete layerFlyConfigs[id];
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
			// For non-template modes, use FlyIn if active, FlyOn if inactive
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

	function handleZOrderUp(id: number, currentZ: number) {
		apiCall(graphicsSetZOrder(id, currentZ + 1), 'Z-order failed');
	}

	function handleZOrderDown(id: number, currentZ: number) {
		apiCall(graphicsSetZOrder(id, Math.max(0, currentZ - 1)), 'Z-order failed');
	}

	function handleModeChange(id: number, mode: SourceMode) {
		layerModes = { ...layerModes, [id]: mode };
	}

	function handleTemplateChange(id: number, tplId: string) {
		layerTemplates[id] = tplId;
		layerFields[id] = getDefaultValues(tplId);
	}

	function handleFieldChange(id: number, key: string, value: string) {
		layerFields = {
			...layerFields,
			[id]: { ...getLayerFields(id), [key]: value },
		};
	}

	function handleAnimConfigChange(id: number, key: string, value: string | number) {
		layerAnimConfigs = {
			...layerAnimConfigs,
			[id]: { ...getLayerAnimConfig(id), [key]: value },
		};
	}

	function handleFlyConfigChange(id: number, key: string, value: string | number) {
		layerFlyConfigs = {
			...layerFlyConfigs,
			[id]: { ...getLayerFlyConfig(id), [key]: value },
		};
	}

	// ── Image handlers ──
	async function handleImageUpload(id: number, file: File) {
		try {
			await graphicsImageUpload(id, file);
			layerModes = { ...layerModes, [id]: 'image' };
			notify('success', `Image "${file.name}" uploaded`);
		} catch {
			notify('error', 'Image upload failed');
		}
	}

	function handleImageDelete(id: number) {
		apiCall(graphicsImageDelete(id), 'Image delete failed');
		layerModes = { ...layerModes, [id]: 'template' };
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

	// ── Text animation handlers ──
	let textAnimRunning = $state<Record<number, boolean>>({});

	async function handleTextAnimStart(id: number, config: { mode: string; text: string; fontSize: number; bold: boolean; charsPerSec?: number; wordDelayMs?: number; fadeDurationMs?: number }) {
		try {
			await graphicsTextAnimStart(id, config);
			textAnimRunning = { ...textAnimRunning, [id]: true };
		} catch {
			notify('error', 'Text animation failed');
		}
	}

	async function handleTextAnimStop(id: number) {
		try {
			await graphicsTextAnimStop(id);
			textAnimRunning = { ...textAnimRunning, [id]: false };
		} catch {
			notify('error', 'Text animation stop failed');
		}
	}
</script>

<div class="graphics-panel">
	<!-- Header -->
	<div class="gfx-header">
		<span class="gfx-title">DSK GRAPHICS</span>
		<span class="gfx-status" class:on-air={anyActive}>
			{anyActive ? 'ON AIR' : 'OFF'}
		</span>
	</div>

	<!-- Two-column body -->
	<div class="gfx-body">
		<GraphicsLayerRail
			{layers}
			selectedId={selectedLayerId}
			layerTemplateNames={layerTemplates}
			onSelect={(id) => selectedLayerId = id}
			onAdd={handleAddLayer}
			onRemove={handleRemoveLayer}
			onZOrderUp={handleZOrderUp}
			onZOrderDown={handleZOrderDown}
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
					onTextAnimStart={(cfg) => handleTextAnimStart(selectedLayer.id, cfg)}
					onTextAnimStop={() => handleTextAnimStop(selectedLayer.id)}
					textAnimActive={textAnimRunning[selectedLayer.id] ?? false}
				/>
			{/key}
		{:else}
			<div class="empty-detail">
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
		padding: 4px 8px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.gfx-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
	}

	.gfx-status {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-tertiary);
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		background: var(--bg-base);
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
		align-items: center;
		justify-content: center;
	}

	.empty-msg {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-tertiary);
	}
</style>
