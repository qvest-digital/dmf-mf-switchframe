<script lang="ts">
	import type { ControlRoomState, GraphicsLayerState } from '$lib/api/types';
	import {
		graphicsAddLayer, graphicsRemoveLayer,
		graphicsOn, graphicsOff, graphicsAutoOn, graphicsAutoOff,
		graphicsAnimate, graphicsAnimateStop,
		graphicsSetZOrder,
		graphicsFlyIn, graphicsFlyOut, graphicsFlyOn,
		apiCall,
	} from '$lib/api/switch-api';
	import { GraphicsPublisher } from '$lib/graphics/publisher';
	import { notify } from '$lib/state/notifications.svelte';
	import { builtinTemplates } from '$lib/graphics/templates';
	import GraphicsLayerRail from './GraphicsLayerRail.svelte';
	import GraphicsDetail from './GraphicsDetail.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── Per-layer state maps ──
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

	// Auto-select first layer if none selected
	$effect(() => {
		if (layers.length > 0 && (selectedLayerId === null || !layers.some(l => l.id === selectedLayerId))) {
			selectedLayerId = layers[0].id;
		}
		if (layers.length === 0) {
			selectedLayerId = null;
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
		return layerTemplates[id] ?? 'lower-third';
	}

	function getLayerFields(id: number): Record<string, string> {
		return layerFields[id] ?? getDefaultValues(getLayerTemplate(id));
	}

	function getLayerAnimConfig(id: number) {
		return layerAnimConfigs[id] ?? { mode: 'pulse', minAlpha: 0.3, maxAlpha: 1.0, speedHz: 1.0, toAlpha: 0.5, durationMs: 500 };
	}

	function getLayerFlyConfig(id: number) {
		return layerFlyConfigs[id] ?? { direction: 'left', durationMs: 500 };
	}

	// Clean up local state for layers removed externally
	$effect(() => {
		const currentIds = new Set(layers.map(l => l.id));
		for (const key of Object.keys(layerTemplates).map(Number)) {
			if (!currentIds.has(key)) {
				delete layerTemplates[key];
				delete layerFields[key];
				delete layerAnimConfigs[key];
				delete layerFlyConfigs[key];
			}
		}
	});

	// ── Handlers ──
	async function handleAddLayer() {
		try {
			const result = await graphicsAddLayer();
			layerTemplates[result.id] = 'lower-third';
			layerFields[result.id] = getDefaultValues('lower-third');
			selectedLayerId = result.id;
		} catch {
			notify('error', 'Failed to add graphics layer');
		}
	}

	function handleRemoveLayer(id: number) {
		apiCall(graphicsRemoveLayer(id), 'Remove layer failed');
		delete layerTemplates[id];
		delete layerFields[id];
		delete layerAnimConfigs[id];
		delete layerFlyConfigs[id];
	}

	async function handlePublishAndOn(id: number) {
		const tplId = getLayerTemplate(id);
		const tpl = builtinTemplates[tplId];
		if (!tpl) return;
		try {
			await publisher.publish(id, tpl, getLayerFields(id));
			apiCall(graphicsOn(id), 'Graphics failed');
		} catch {
			notify('error', 'Graphics publish failed');
		}
	}

	async function handlePublishAndAutoOn(id: number) {
		const tplId = getLayerTemplate(id);
		const tpl = builtinTemplates[tplId];
		if (!tpl) return;
		try {
			await publisher.publish(id, tpl, getLayerFields(id));
			apiCall(graphicsAutoOn(id), 'Graphics failed');
		} catch {
			notify('error', 'Graphics publish failed');
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
		const tplId = getLayerTemplate(id);
		const tpl = builtinTemplates[tplId];
		if (!tpl) return;
		try {
			await publisher.publish(id, tpl, getLayerFields(id));
			const cfg = getLayerFlyConfig(id);
			apiCall(graphicsFlyOn(id, cfg.direction, cfg.durationMs), 'Fly on failed');
		} catch {
			notify('error', 'Fly on failed');
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
			<GraphicsDetail
				layer={selectedLayer}
				templateId={getLayerTemplate(selectedLayer.id)}
				fields={getLayerFields(selectedLayer.id)}
				animConfig={getLayerAnimConfig(selectedLayer.id)}
				flyConfig={getLayerFlyConfig(selectedLayer.id)}
				{publisher}
				onTemplateChange={(tplId) => handleTemplateChange(selectedLayer.id, tplId)}
				onFieldChange={(key, val) => handleFieldChange(selectedLayer.id, key, val)}
				onAnimConfigChange={(key, val) => handleAnimConfigChange(selectedLayer.id, key, val)}
				onFlyConfigChange={(key, val) => handleFlyConfigChange(selectedLayer.id, key, val)}
				onCutOn={() => handlePublishAndOn(selectedLayer.id)}
				onAutoOn={() => handlePublishAndAutoOn(selectedLayer.id)}
				onCutOff={() => handleOff(selectedLayer.id)}
				onAutoOff={() => handleAutoOff(selectedLayer.id)}
				onFlyIn={() => handleFlyIn(selectedLayer.id)}
				onFlyOut={() => handleFlyOut(selectedLayer.id)}
				onAnimate={() => handleAnimate(selectedLayer.id)}
				onAnimateStop={() => handleAnimateStop(selectedLayer.id)}
			/>
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
