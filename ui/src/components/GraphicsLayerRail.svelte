<script lang="ts">
	import type { GraphicsLayerState } from '$lib/api/types';

	interface Props {
		layers: GraphicsLayerState[];
		selectedId: number | null;
		/** Map of layer ID → template ID for display */
		layerTemplateNames: Record<number, string>;
		onSelect: (id: number) => void;
		onAdd: () => void;
		onRemove: (id: number) => void;
		onZOrderUp: (id: number, currentZ: number) => void;
		onZOrderDown: (id: number, currentZ: number) => void;
	}

	let {
		layers, selectedId, layerTemplateNames,
		onSelect, onAdd, onRemove, onZOrderUp, onZOrderDown
	}: Props = $props();

	const tplAbbrev: Record<string, string> = {
		'lower-third': 'LwrThd',
		'news-lower-third': 'News',
		'full-screen': 'FullScr',
		'ticker': 'Ticker',
		'network-bug': 'Bug',
		'score-bug': 'Score',
	};

	function abbrev(id: number): string {
		const tplId = layerTemplateNames[id] ?? 'lower-third';
		return tplAbbrev[tplId] ?? tplId.slice(0, 6);
	}

	function isBusy(layer: GraphicsLayerState): boolean {
		return !!layer.animationMode || (layer.active && layer.fadePosition != null && layer.fadePosition > 0 && layer.fadePosition < 1);
	}
</script>

<div class="layer-rail">
	<div class="rail-header">LAYERS</div>
	<div class="rail-list">
		{#each layers as layer (layer.id)}
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="rail-item"
				class:selected={selectedId === layer.id}
				class:active={layer.active}
				onclick={() => onSelect(layer.id)}
				onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(layer.id); } }}
				role="button"
				tabindex="0"
				aria-label="Layer {layer.id}"
				aria-pressed={selectedId === layer.id}
			>
				<span class="status-dot" class:on={layer.active} class:animating={!!layer.animationMode}></span>
				<span class="layer-label">
					<span class="layer-num">L{layer.id}</span>
					<span class="layer-tpl">{abbrev(layer.id)}</span>
				</span>
				{#if layer.active && layer.fadePosition != null && layer.fadePosition < 1}
					<span class="mini-fade">
						<span class="mini-fade-fill" style="width: {(layer.fadePosition ?? 1) * 100}%"></span>
					</span>
				{/if}
				{#if layer.animationMode}
					<span class="mini-anim-badge" title="{layer.animationMode}">A</span>
				{/if}
				<span class="z-badge" title="z-order {layer.zOrder}">z{layer.zOrder}</span>
				<div class="hover-controls">
					<button
						class="micro-btn"
						onclick={(e) => { e.stopPropagation(); onZOrderUp(layer.id, layer.zOrder); }}
						title="Move up"
						aria-label="Z-order up"
					>&#9650;</button>
					<button
						class="micro-btn"
						onclick={(e) => { e.stopPropagation(); onZOrderDown(layer.id, layer.zOrder); }}
						title="Move down"
						aria-label="Z-order down"
					>&#9660;</button>
					<button
						class="micro-btn del"
						onclick={(e) => { e.stopPropagation(); onRemove(layer.id); }}
						title="Delete layer"
						aria-label="Delete layer"
					>&times;</button>
				</div>
			</div>
		{/each}
	</div>
	<button class="add-btn" onclick={onAdd} aria-label="Add layer">+ ADD</button>
</div>

<style>
	.layer-rail {
		display: flex;
		flex-direction: column;
		width: 120px;
		min-width: 120px;
		border-right: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		overflow: hidden;
	}

	.rail-header {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.1em;
		color: var(--text-tertiary);
		padding: 6px 8px 4px;
		text-transform: uppercase;
	}

	.rail-list {
		flex: 1;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 0 4px;
	}

	.rail-item {
		position: relative;
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 4px 6px;
		border: none;
		border-left: 2px solid transparent;
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
		background: transparent;
		color: var(--text-primary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		text-align: left;
		min-height: 28px;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.rail-item:hover {
		background: var(--bg-elevated);
	}

	.rail-item.selected {
		border-left-color: var(--accent-blue);
		background: var(--bg-elevated);
	}

	.rail-item.active.selected {
		border-left-color: var(--tally-program);
	}

	.status-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--bg-control);
		flex-shrink: 0;
		transition: background var(--transition-fast);
	}

	.status-dot.on {
		background: var(--tally-program);
		box-shadow: 0 0 4px rgba(220, 38, 38, 0.5);
	}

	.status-dot.animating {
		animation: dot-pulse 1s ease-in-out infinite;
	}

	@keyframes dot-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	.layer-label {
		display: flex;
		flex-direction: column;
		gap: 0;
		min-width: 0;
		flex: 1;
	}

	.layer-num {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		line-height: 1.1;
	}

	.layer-tpl {
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		line-height: 1.1;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.z-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.mini-fade {
		width: 16px;
		height: 3px;
		background: var(--bg-base);
		border-radius: 1px;
		overflow: hidden;
		flex-shrink: 0;
	}

	.mini-fade-fill {
		display: block;
		height: 100%;
		background: var(--accent-blue);
		border-radius: 1px;
		transition: width 0.1s linear;
	}

	.mini-anim-badge {
		font-family: var(--font-mono);
		font-size: 0.45rem;
		font-weight: 700;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		width: 12px;
		height: 12px;
		display: flex;
		align-items: center;
		justify-content: center;
		border-radius: 2px;
		flex-shrink: 0;
	}

	.hover-controls {
		position: absolute;
		right: 2px;
		top: 50%;
		transform: translateY(-50%);
		display: none;
		gap: 1px;
		background: var(--bg-elevated);
		border-radius: 2px;
		padding: 1px;
	}

	.rail-item:hover .hover-controls,
	.rail-item.selected .hover-controls {
		display: flex;
	}

	.micro-btn {
		width: 14px;
		height: 14px;
		display: flex;
		align-items: center;
		justify-content: center;
		border: 1px solid var(--border-default);
		border-radius: 2px;
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		padding: 0;
		font-size: 0.45rem;
		line-height: 1;
	}

	.micro-btn:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.micro-btn.del:hover {
		border-color: var(--tally-program);
		color: var(--tally-program);
	}

	.add-btn {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--accent-blue);
		background: transparent;
		border: 1px dashed var(--border-default);
		border-radius: var(--radius-sm);
		padding: 4px 8px;
		margin: 4px;
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.add-btn:hover {
		background: var(--accent-blue-dim);
		border-color: var(--accent-blue);
	}
</style>
