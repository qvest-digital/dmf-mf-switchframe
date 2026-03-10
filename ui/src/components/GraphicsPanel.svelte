<script lang="ts">
	import type { ControlRoomState, GraphicsLayerState } from '$lib/api/types';
	import {
		graphicsAddLayer, graphicsRemoveLayer,
		graphicsOn, graphicsOff, graphicsAutoOn, graphicsAutoOff,
		graphicsAnimate, graphicsAnimateStop,
		graphicsSetZOrder,
		apiCall,
	} from '$lib/api/switch-api';
	import { GraphicsPublisher } from '$lib/graphics/publisher';
	import { templateList, builtinTemplates } from '$lib/graphics/templates';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// Per-layer template + field state, keyed by layer ID.
	let layerTemplates = $state<Record<number, string>>({});
	let layerFields = $state<Record<number, Record<string, string>>>({});
	let previewCanvases = $state<Record<number, HTMLCanvasElement | null>>({});

	const publisher = new GraphicsPublisher();

	$effect(() => {
		return () => publisher.destroy();
	});

	const layers = $derived<GraphicsLayerState[]>(
		(crState.graphics?.layers ?? []).slice().sort((a, b) => a.zOrder - b.zOrder)
	);

	const anyActive = $derived(layers.some((l) => l.active));

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

	// Re-render previews when fields change.
	$effect(() => {
		for (const layer of layers) {
			const canvas = previewCanvases[layer.id];
			const tplId = getLayerTemplate(layer.id);
			const tpl = builtinTemplates[tplId];
			const vals = getLayerFields(layer.id);
			if (!tpl || !canvas) continue;
			try {
				publisher.renderPreview(canvas, tpl, vals);
			} catch {
				// Canvas rendering may fail in test environments.
			}
		}
	});

	async function handleAddLayer() {
		try {
			const result = await graphicsAddLayer();
			layerTemplates[result.id] = 'lower-third';
			layerFields[result.id] = getDefaultValues('lower-third');
		} catch (err) {
			console.warn('Failed to add layer:', err);
		}
	}

	function handleRemoveLayer(id: number) {
		apiCall(graphicsRemoveLayer(id), 'Remove layer failed');
		delete layerTemplates[id];
		delete layerFields[id];
		delete previewCanvases[id];
	}

	async function handlePublishAndOn(id: number) {
		const tplId = getLayerTemplate(id);
		const tpl = builtinTemplates[tplId];
		if (!tpl) return;
		try {
			await publisher.publish(id, tpl, getLayerFields(id));
			apiCall(graphicsOn(id), 'Graphics failed');
		} catch (err) {
			console.warn('Graphics publish failed:', err);
		}
	}

	async function handlePublishAndAutoOn(id: number) {
		const tplId = getLayerTemplate(id);
		const tpl = builtinTemplates[tplId];
		if (!tpl) return;
		try {
			await publisher.publish(id, tpl, getLayerFields(id));
			apiCall(graphicsAutoOn(id), 'Graphics failed');
		} catch (err) {
			console.warn('Graphics publish failed:', err);
		}
	}

	function handleOff(id: number) {
		apiCall(graphicsOff(id), 'Graphics failed');
	}

	function handleAutoOff(id: number) {
		apiCall(graphicsAutoOff(id), 'Graphics failed');
	}

	function handleAnimate(id: number) {
		apiCall(graphicsAnimate(id, { mode: 'pulse', minAlpha: 0.3, maxAlpha: 1.0, speedHz: 1.0 }), 'Animation failed');
	}

	function handleAnimateStop(id: number) {
		apiCall(graphicsAnimateStop(id), 'Animation stop failed');
	}

	function handleZOrderUp(id: number, currentZ: number) {
		apiCall(graphicsSetZOrder(id, currentZ + 1), 'Z-order failed');
	}

	function handleZOrderDown(id: number, currentZ: number) {
		apiCall(graphicsSetZOrder(id, Math.max(0, currentZ - 1)), 'Z-order failed');
	}

	function handleTemplateChange(id: number, e: Event) {
		const tplId = (e.target as HTMLSelectElement).value;
		layerTemplates[id] = tplId;
		layerFields[id] = getDefaultValues(tplId);
	}

	function handleFieldChange(id: number, key: string, value: string) {
		layerFields = {
			...layerFields,
			[id]: { ...getLayerFields(id), [key]: value },
		};
	}
</script>

<div class="graphics-panel">
	<div class="gfx-header">
		<span class="gfx-label">DSK LAYERS</span>
		<div class="gfx-header-right">
			<span class="gfx-status" class:on-air={anyActive}>
				{anyActive ? 'ON AIR' : 'OFF'}
			</span>
			<button class="add-layer-btn" onclick={handleAddLayer} aria-label="Add layer">+ LAYER</button>
		</div>
	</div>

	{#if layers.length === 0}
		<div class="empty-state">No layers. Click + LAYER to add one.</div>
	{/if}

	{#each layers as layer (layer.id)}
		{@const tplId = getLayerTemplate(layer.id)}
		{@const tpl = builtinTemplates[tplId]}
		{@const fields = getLayerFields(layer.id)}
		{@const supportsAnim = tpl?.supportsAnimation ?? false}
		<div class="layer-card" class:active={layer.active}>
			<div class="layer-header">
				<span class="layer-id">L{layer.id}</span>
				<span class="layer-z" title="Z-order">z{layer.zOrder}</span>
				<div class="z-controls">
					<button class="z-btn" onclick={() => handleZOrderUp(layer.id, layer.zOrder)} title="Move up" aria-label="Z-order up">&#9650;</button>
					<button class="z-btn" onclick={() => handleZOrderDown(layer.id, layer.zOrder)} title="Move down" aria-label="Z-order down">&#9660;</button>
				</div>
				<button class="delete-btn" onclick={() => handleRemoveLayer(layer.id)} title="Remove layer" aria-label="Delete layer">&times;</button>
			</div>

			<select class="template-select" value={tplId} onchange={(e) => handleTemplateChange(layer.id, e)} aria-label="Template">
				{#each templateList as t}
					<option value={t.id}>{t.name}</option>
				{/each}
			</select>

			{#if tpl}
				<div class="fields">
					{#each tpl.fields as field}
						<label class="field-row">
							<span class="field-label">{field.label}</span>
							<input
								type="text"
								class="field-input"
								value={fields[field.key] ?? ''}
								maxlength={field.maxLength}
								oninput={(e) => handleFieldChange(layer.id, field.key, (e.target as HTMLInputElement).value)}
							/>
						</label>
					{/each}
				</div>
			{/if}

			<canvas
				bind:this={previewCanvases[layer.id]}
				class="gfx-preview"
				width={320}
				height={240}
				aria-label="Layer {layer.id} preview"
			></canvas>

			<div class="gfx-buttons">
				<button class="gfx-btn on" onclick={() => handlePublishAndOn(layer.id)} disabled={layer.active}>
					CUT ON
				</button>
				<button class="gfx-btn auto-on" onclick={() => handlePublishAndAutoOn(layer.id)} disabled={layer.active}>
					AUTO ON
				</button>
				<button class="gfx-btn off" onclick={() => handleOff(layer.id)} disabled={!layer.active}>
					CUT OFF
				</button>
				<button class="gfx-btn auto-off" onclick={() => handleAutoOff(layer.id)} disabled={!layer.active}>
					AUTO OFF
				</button>
			</div>

			{#if supportsAnim}
				<div class="gfx-anim-row">
					{#if layer.animationMode}
						<button class="gfx-btn anim-stop" onclick={() => handleAnimateStop(layer.id)}>
							STOP ANIM
						</button>
					{:else}
						<button class="gfx-btn anim-start" onclick={() => handleAnimate(layer.id)} disabled={!layer.active}>
							ANIMATE
						</button>
					{/if}
				</div>
			{/if}
		</div>
	{/each}
</div>

<style>
	.graphics-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 8px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		background: var(--bg-surface);
		max-height: 500px;
		overflow-y: auto;
	}

	.gfx-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0 2px;
	}

	.gfx-header-right {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.gfx-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
		text-transform: uppercase;
	}

	.gfx-status {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-secondary);
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		background: var(--bg-base);
	}

	.gfx-status.on-air {
		color: #fff;
		background: var(--tally-program, #dc2626);
		animation: pulse-glow 1.5s ease-in-out infinite;
	}

	@keyframes pulse-glow {
		0%, 100% { box-shadow: 0 0 4px rgba(220, 38, 38, 0.3); }
		50% { box-shadow: 0 0 8px rgba(220, 38, 38, 0.6); }
	}

	.add-layer-btn {
		font-family: var(--font-ui);
		font-size: 0.6rem;
		font-weight: 700;
		letter-spacing: 0.04em;
		padding: 2px 8px;
		border: 1px solid var(--accent-blue, #3b82f6);
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--accent-blue, #3b82f6);
		cursor: pointer;
	}

	.add-layer-btn:hover {
		background: rgba(59, 130, 246, 0.15);
	}

	.empty-state {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		color: var(--text-secondary);
		text-align: center;
		padding: 12px;
	}

	.layer-card {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 6px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
	}

	.layer-card.active {
		border-color: var(--tally-program, #dc2626);
		box-shadow: 0 0 4px rgba(220, 38, 38, 0.2);
	}

	.layer-header {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.layer-id {
		font-family: var(--font-mono);
		font-size: 0.7rem;
		font-weight: 700;
		color: var(--text-primary);
	}

	.layer-z {
		font-family: var(--font-mono);
		font-size: 0.6rem;
		color: var(--text-secondary);
	}

	.z-controls {
		display: flex;
		gap: 1px;
		margin-left: auto;
	}

	.z-btn {
		font-size: 0.5rem;
		width: 16px;
		height: 16px;
		display: flex;
		align-items: center;
		justify-content: center;
		border: 1px solid var(--border-default);
		border-radius: 2px;
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		padding: 0;
		line-height: 1;
	}

	.z-btn:hover {
		background: var(--bg-elevated);
		color: var(--text-primary);
	}

	.delete-btn {
		font-size: 0.85rem;
		width: 18px;
		height: 18px;
		display: flex;
		align-items: center;
		justify-content: center;
		border: 1px solid var(--border-default);
		border-radius: 2px;
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		padding: 0;
		line-height: 1;
	}

	.delete-btn:hover {
		border-color: var(--tally-program, #dc2626);
		color: var(--tally-program, #dc2626);
	}

	.template-select {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 6px;
		cursor: pointer;
	}

	.fields {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.field-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.field-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		min-width: 40px;
		flex-shrink: 0;
	}

	.field-input {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 6px;
	}

	.field-input:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.gfx-preview {
		width: 100%;
		max-height: 50px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: #111;
		object-fit: contain;
	}

	.gfx-buttons {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 3px;
	}

	.gfx-btn {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.04em;
		padding: 5px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast);
	}

	.gfx-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.gfx-btn.on:not(:disabled):hover,
	.gfx-btn.auto-on:not(:disabled):hover {
		border-color: var(--tally-program, #dc2626);
		background: rgba(220, 38, 38, 0.15);
	}

	.gfx-btn.off:not(:disabled):hover,
	.gfx-btn.auto-off:not(:disabled):hover {
		border-color: var(--tally-preview, #16a34a);
		background: rgba(22, 163, 74, 0.15);
	}

	.gfx-anim-row {
		display: flex;
		gap: 3px;
	}

	.gfx-btn.anim-start {
		flex: 1;
	}

	.gfx-btn.anim-start:not(:disabled):hover {
		border-color: var(--accent-blue, #3b82f6);
		background: rgba(59, 130, 246, 0.15);
	}

	.gfx-btn.anim-stop {
		flex: 1;
		border-color: var(--tally-program, #dc2626);
		background: rgba(220, 38, 38, 0.15);
		color: #fff;
	}

	.gfx-btn.anim-stop:hover {
		background: rgba(220, 38, 38, 0.3);
	}
</style>
