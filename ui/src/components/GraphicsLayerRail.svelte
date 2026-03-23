<script lang="ts">
	import type { GraphicsLayerState } from '$lib/api/types';
	import {
		ICON_GRIP, ICON_PLUS, ICON_TRASH, renderIcon,
	} from '$lib/icons';

	interface Props {
		layers: GraphicsLayerState[];
		selectedId: number | null;
		layerTemplateNames: Record<number, string>;
		onSelect: (id: number) => void;
		onAdd: () => void;
		onRemove: (id: number) => void;
		onZOrderChange: (id: number, newZ: number) => void;
	}

	let {
		layers, selectedId, layerTemplateNames,
		onSelect, onAdd, onRemove, onZOrderChange,
	}: Props = $props();

	const tplAbbrev: Record<string, string> = {
		'lower-third': 'L3',
		'news-lower-third': 'News',
		'full-screen': 'Full',
		'ticker': 'Tick',
		'network-bug': 'Bug',
		'score-bug': 'Score',
	};

	function abbrev(id: number): string {
		const layer = layers.find(l => l.id === id);
		if (layer?.html5Status) return 'H5';
		const tplId = layerTemplateNames[id] ?? 'lower-third';
		return tplAbbrev[tplId] ?? tplId.slice(0, 5);
	}

	// ── Drag-and-drop reorder ──
	let dragSourceId = $state<number | null>(null);
	let dragOverId = $state<number | null>(null);

	function handleDragStart(e: DragEvent, layer: GraphicsLayerState) {
		dragSourceId = layer.id;
		if (e.dataTransfer) {
			e.dataTransfer.effectAllowed = 'move';
			e.dataTransfer.setData('text/plain', String(layer.id));
		}
	}

	function handleDragOver(e: DragEvent, layer: GraphicsLayerState) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
		dragOverId = layer.id;
	}

	function handleDragLeave() {
		dragOverId = null;
	}

	function handleDrop(e: DragEvent, targetLayer: GraphicsLayerState) {
		e.preventDefault();
		if (dragSourceId !== null && dragSourceId !== targetLayer.id) {
			onZOrderChange(dragSourceId, targetLayer.zOrder);
		}
		dragSourceId = null;
		dragOverId = null;
	}

	function handleDragEnd() {
		dragSourceId = null;
		dragOverId = null;
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
				class:dragging={dragSourceId === layer.id}
				class:drag-over={dragOverId === layer.id && dragSourceId !== layer.id}
				draggable="true"
				ondragstart={(e) => handleDragStart(e, layer)}
				ondragover={(e) => handleDragOver(e, layer)}
				ondragleave={handleDragLeave}
				ondrop={(e) => handleDrop(e, layer)}
				ondragend={handleDragEnd}
				onclick={() => onSelect(layer.id)}
				onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(layer.id); } }}
				role="button"
				tabindex="0"
				aria-label="Layer {layer.id}"
				aria-pressed={selectedId === layer.id}
			>
				<span class="drag-handle">{@html renderIcon(ICON_GRIP, 10, 'fill')}</span>

				<span class="status-dot" class:on={layer.active} class:animating={!!layer.animationMode}></span>

				<span class="layer-info">
					<span class="layer-num">L{layer.id}</span>
					<span class="layer-type">{abbrev(layer.id)}</span>
				</span>

				{#if layer.active && layer.fadePosition != null && layer.fadePosition < 1}
					<span class="mini-fade">
						<span class="mini-fade-fill" style="width: {(layer.fadePosition ?? 1) * 100}%"></span>
					</span>
				{/if}

				{#if layer.animationMode}
					<span class="anim-badge" title="{layer.animationMode}">A</span>
				{/if}

				<span class="z-badge" title="z-order {layer.zOrder}">z{layer.zOrder}</span>

				<button
					class="del-btn"
					onclick={(e) => { e.stopPropagation(); onRemove(layer.id); }}
					title="Delete layer"
					aria-label="Delete layer"
				>
					{@html renderIcon(ICON_TRASH, 10, 'stroke')}
				</button>
			</div>
		{/each}
	</div>

	<button class="add-btn" onclick={onAdd} aria-label="Add layer">
		<span class="add-icon">{@html renderIcon(ICON_PLUS, 10, 'stroke')}</span>
		ADD
	</button>
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
		padding: 4px 8px 3px;
		text-transform: uppercase;
		flex-shrink: 0;
	}

	.rail-list {
		flex: 1;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding: 0 3px;
		min-height: 0;
	}

	.rail-item {
		position: relative;
		display: flex;
		align-items: center;
		gap: 3px;
		padding: 3px 4px;
		border: none;
		border-left: 2px solid transparent;
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
		background: transparent;
		color: var(--text-primary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		text-align: left;
		min-height: 26px;
		transition: background var(--transition-fast), border-color var(--transition-fast);
		user-select: none;
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

	.rail-item.dragging {
		opacity: 0.4;
	}

	.rail-item.drag-over {
		border-top: 2px solid var(--accent-blue);
		border-radius: 0;
	}

	.drag-handle {
		display: flex;
		align-items: center;
		color: var(--text-tertiary);
		cursor: grab;
		flex-shrink: 0;
		padding: 0 1px;
	}

	.drag-handle:active {
		cursor: grabbing;
	}

	.status-dot {
		width: 5px;
		height: 5px;
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

	.layer-info {
		display: flex;
		flex-direction: column;
		gap: 0;
		min-width: 0;
		flex: 1;
	}

	.layer-num {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-weight: 600;
		line-height: 1.1;
	}

	.layer-type {
		font-size: 0.55rem;
		color: var(--text-tertiary);
		line-height: 1.1;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.z-badge {
		font-family: var(--font-mono);
		font-size: 0.5rem;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.mini-fade {
		width: 14px;
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

	.anim-badge {
		font-family: var(--font-mono);
		font-size: 0.4rem;
		font-weight: 700;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		width: 11px;
		height: 11px;
		display: flex;
		align-items: center;
		justify-content: center;
		border-radius: 2px;
		flex-shrink: 0;
	}

	.del-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 16px;
		height: 16px;
		border: none;
		border-radius: var(--radius-xs);
		background: transparent;
		color: var(--text-tertiary);
		cursor: pointer;
		padding: 0;
		flex-shrink: 0;
		transition: color var(--transition-fast), background var(--transition-fast);
	}

	.del-btn:hover {
		color: var(--tally-program);
		background: var(--bg-hover);
	}

	.add-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 4px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--accent-blue);
		background: transparent;
		border: 1px dashed var(--border-default);
		border-radius: var(--radius-sm);
		padding: 3px 8px;
		margin: 3px;
		cursor: pointer;
		flex-shrink: 0;
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.add-btn:hover {
		background: var(--accent-blue-dim);
		border-color: var(--accent-blue);
	}

	.add-icon {
		display: flex;
	}
</style>
