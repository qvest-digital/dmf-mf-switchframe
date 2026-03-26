<script lang="ts">
	import type { PlaylistItem, SecondaryEvent } from '$lib/api/types';

	interface Props {
		items: PlaylistItem[];
		currentIndex: number;
		selectedIndex: number;
		onSelect: (index: number) => void;
		onReorder: (fromIndex: number, toIndex: number) => void;
		onDelete: (index: number) => void;
		onAddItem: (atIndex: number) => void;
		progress?: number;
		lockedIndices?: number[];
		onJumpTo?: (index: number) => void;
	}
	let { items, currentIndex, selectedIndex, onSelect, onReorder, onDelete, onAddItem, progress = 0, lockedIndices = [], onJumpTo }: Props = $props();

	function isLocked(index: number): boolean {
		return lockedIndices.includes(index);
	}

	// ── Drag state ──
	let dragIndex = $state<number | null>(null);
	let dropTarget = $state<number | null>(null);

	// ── Context menu state ──
	let contextMenu = $state<{ x: number; y: number; index: number } | null>(null);

	// ── Container measurement & zoom ──
	let containerEl = $state<HTMLDivElement | null>(null);
	let containerWidth = $state(600);
	let zoomMode = $state<'auto' | 'manual'>('auto');
	let manualPxPerSec = $state(4);

	$effect(() => {
		if (!containerEl) return;
		const ro = new ResizeObserver(entries => {
			for (const entry of entries) {
				containerWidth = entry.contentRect.width - 8; // subtract padding
			}
		});
		ro.observe(containerEl);
		return () => ro.disconnect();
	});

	// ── Constants ──
	const MIN_ITEM_WIDTH = 32;
	const LIVE_EQUIV_SECONDS = 20;

	// ── Derived ──
	const totalDuration = $derived(
		items.reduce((sum, it) => sum + (it.type === 'live' ? 0 : (it.duration_ms ?? 0)), 0)
	);

	const totalSeconds = $derived(
		items.reduce((sum, it) => {
			if (it.type === 'live') return sum + LIVE_EQUIV_SECONDS;
			return sum + Math.max(1, (it.duration_ms ?? 0) / 1000);
		}, 0)
	);

	const gapWidth = $derived(
		Math.max(0, items.length - 1) * 2 +
		items.filter((_, i) => i + 1 < items.length && items[i + 1]?.transition?.type && items[i + 1].transition!.type !== 'cut').length * 18
	);

	const autoPxPerSec = $derived(
		totalSeconds > 0 && items.length > 0
			? Math.max(0.5, (containerWidth - gapWidth) / totalSeconds)
			: 4
	);

	const effectivePxPerSec = $derived(zoomMode === 'auto' ? autoPxPerSec : manualPxPerSec);

	// ── Helpers ──

	function blockWidth(item: PlaylistItem): number {
		if (item.type === 'live') return Math.max(MIN_ITEM_WIDTH, LIVE_EQUIV_SECONDS * effectivePxPerSec);
		const durSec = Math.max(1, (item.duration_ms ?? 0) / 1000);
		return Math.max(MIN_ITEM_WIDTH, durSec * effectivePxPerSec);
	}

	function formatDuration(ms: number | undefined): string {
		if (!ms || ms <= 0) return '--:--';
		const totalSecs = Math.floor(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	function transitionAbbrev(type: string): string {
		switch (type) {
			case 'dissolve': return 'D';
			case 'wipe': return 'W';
			case 'dip': return 'DIP';
			case 'stinger': return 'S';
			default: return type.charAt(0).toUpperCase();
		}
	}

	/** Classify secondary events into color categories. */
	function eventDotColor(evt: SecondaryEvent): string {
		if (evt.type.startsWith('graphics')) return 'var(--accent-green, #22c55e)';
		if (evt.type.startsWith('audio')) return 'var(--accent-blue, #3b82f6)';
		if (evt.type.startsWith('caption')) return 'var(--accent-orange, #f97316)';
		if (evt.type === 'scte35_cue') return 'var(--tally-program, #dc2626)';
		return 'var(--text-tertiary)';
	}

	/** Check if there is a non-cut transition between two items. */
	function hasTransition(index: number): boolean {
		if (index + 1 >= items.length) return false;
		const nextItem = items[index + 1];
		return !!nextItem.transition && nextItem.transition.type !== 'cut';
	}

	function getTransitionType(index: number): string {
		if (index + 1 >= items.length) return '';
		const nextItem = items[index + 1];
		return nextItem.transition?.type ?? '';
	}

	// ── Drag handlers ──

	function handleDragStart(e: DragEvent, index: number) {
		if (isLocked(index)) { e.preventDefault(); return; }
		dragIndex = index;
		if (e.dataTransfer) {
			e.dataTransfer.effectAllowed = 'move';
			e.dataTransfer.setData('text/plain', String(index));
		}
	}

	function handleDragOver(e: DragEvent, index: number) {
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
		dropTarget = index;
	}

	function handleDragLeave() {
		dropTarget = null;
	}

	function handleDrop(e: DragEvent, toIndex: number) {
		e.preventDefault();
		if (dragIndex !== null && dragIndex !== toIndex) {
			onReorder(dragIndex, toIndex);
		}
		dragIndex = null;
		dropTarget = null;
	}

	function handleDragEnd() {
		dragIndex = null;
		dropTarget = null;
	}

	// ── Context menu handlers ──

	function handleContextMenu(e: MouseEvent, index: number) {
		e.preventDefault();
		contextMenu = { x: e.clientX, y: e.clientY, index };
	}

	function closeContextMenu() {
		contextMenu = null;
	}

	function ctxInsertBefore() {
		if (contextMenu) {
			onAddItem(contextMenu.index);
			closeContextMenu();
		}
	}

	function ctxInsertAfter() {
		if (contextMenu) {
			onAddItem(contextMenu.index + 1);
			closeContextMenu();
		}
	}

	function ctxDuplicate() {
		if (contextMenu) {
			// Insert a copy after the selected item
			onAddItem(contextMenu.index + 1);
			closeContextMenu();
		}
	}

	function ctxDelete() {
		if (contextMenu && !isLocked(contextMenu.index)) {
			onDelete(contextMenu.index);
			closeContextMenu();
		}
	}
</script>

<svelte:window onclick={closeContextMenu} />

<div class="timeline-container" bind:this={containerEl} role="list" aria-label="Playlist timeline">
	<div class="zoom-bar">
		<input
			type="range"
			class="zoom-slider"
			min="0.5"
			max="12"
			step="0.1"
			value={effectivePxPerSec}
			oninput={(e) => {
				zoomMode = 'manual';
				manualPxPerSec = parseFloat((e.target as HTMLInputElement).value);
			}}
			title="Zoom: {effectivePxPerSec.toFixed(1)}px/s"
		/>
		{#if zoomMode === 'manual'}
			<button class="zoom-fit-btn" onclick={() => zoomMode = 'auto'} title="Fit all items">Fit</button>
		{/if}
	</div>
	{#if items.length === 0}
		<div class="timeline-empty">No items in playlist</div>
	{:else}
		<div class="timeline-track">
			{#each items as item, i}
				<!-- Drop indicator before first item -->
				{#if i === 0 && dropTarget === 0 && dragIndex !== 0}
					<div class="drop-indicator"></div>
				{/if}

				<!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
				<div
					class="timeline-block"
					class:clip={item.type === 'clip'}
					class:live={item.type === 'live'}
					class:slate={item.type === 'slate'}
					class:on-air={i === currentIndex}
					class:selected={i === selectedIndex}
					class:dragging={i === dragIndex}
					style="width: {blockWidth(item)}px"
					draggable="true"
					role="listitem"
					tabindex="0"
					aria-label="{item.type} {item.title}"
					ondblclick={(e) => { e.stopPropagation(); onJumpTo?.(i); }}
					onclick={() => onSelect(i)}
					onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(i); } }}
					oncontextmenu={(e) => handleContextMenu(e, i)}
					ondragstart={(e) => handleDragStart(e, i)}
					ondragover={(e) => handleDragOver(e, i)}
					ondragleave={handleDragLeave}
					ondrop={(e) => handleDrop(e, i)}
					ondragend={handleDragEnd}
				>
					<!-- Playhead -->
					{#if i === currentIndex && progress != null && progress > 0}
						<div class="playhead" style="left: {progress * 100}%"></div>
					{/if}

					<div class="block-header">
						<span class="block-type-badge" class:clip={item.type === 'clip'} class:live={item.type === 'live'} class:slate={item.type === 'slate'}>
							{item.type.toUpperCase()}
						</span>
					</div>
					<span class="block-title">{item.title}</span>
					<span class="block-duration">
						{#if item.type === 'live'}
							&#x221E;
						{:else}
							{formatDuration(item.duration_ms)}
						{/if}
					</span>

					<!-- Secondary event dots -->
					{#if item.secondary_events && item.secondary_events.length > 0}
						<div class="event-dots">
							{#each item.secondary_events as evt}
								<span class="event-dot" style="background: {eventDotColor(evt)}" title="{evt.type}"></span>
							{/each}
						</div>
					{/if}
				</div>

				<!-- Transition indicator between items -->
				{#if hasTransition(i)}
					<div class="transition-indicator" title="{getTransitionType(i)} transition">
						<span class="transition-diamond">{transitionAbbrev(getTransitionType(i))}</span>
					</div>
				{/if}

				<!-- Drop indicator after each item -->
				{#if dropTarget === i + 1 || (dropTarget === i && dragIndex !== null && dragIndex < i)}
					<div class="drop-indicator"></div>
				{/if}
			{/each}
		</div>
	{/if}
</div>

<!-- Context menu -->
{#if contextMenu}
	<div
		class="context-menu"
		style="left: {contextMenu.x}px; top: {contextMenu.y}px"
		role="menu"
	>
		<button class="ctx-item" role="menuitem" onclick={ctxInsertBefore}>Insert Before</button>
		<button class="ctx-item" role="menuitem" onclick={ctxInsertAfter}>Insert After</button>
		<button class="ctx-item" role="menuitem" onclick={ctxDuplicate}>Duplicate</button>
		<div class="ctx-divider"></div>
		<button class="ctx-item ctx-danger" role="menuitem" onclick={ctxDelete}>Delete</button>
	</div>
{/if}

<style>
	.timeline-container {
		width: 100%;
		overflow-x: auto;
		overflow-y: hidden;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		padding: 4px;
	}

	.timeline-container::-webkit-scrollbar { height: 3px; }
	.timeline-container::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	.zoom-bar {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-bottom: 3px;
		padding: 0 2px;
	}

	.zoom-slider {
		flex: 1;
		height: 2px;
		appearance: none;
		-webkit-appearance: none;
		background: var(--border-default);
		border-radius: 1px;
		outline: none;
		max-width: 120px;
	}

	.zoom-slider::-webkit-slider-thumb {
		-webkit-appearance: none;
		appearance: none;
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--text-tertiary);
		cursor: pointer;
		border: 1px solid var(--border-strong);
	}

	.zoom-slider::-webkit-slider-thumb:hover {
		background: var(--text-secondary);
	}

	.zoom-fit-btn {
		padding: 1px 5px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 7px;
		font-weight: 700;
		color: var(--text-tertiary);
		cursor: pointer;
		letter-spacing: 0.04em;
	}

	.zoom-fit-btn:hover {
		background: var(--bg-hover);
		color: var(--text-secondary);
	}

	.timeline-empty {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 56px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		opacity: 0.5;
	}

	.timeline-track {
		display: flex;
		align-items: stretch;
		gap: 2px;
		min-height: 56px;
	}

	/* ── Item blocks ── */

	.timeline-block {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 3px 5px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		cursor: pointer;
		flex-shrink: 0;
		min-width: 0;
		overflow: hidden;
		text-align: left;
		color: inherit;
		font: inherit;
		transition: border-color var(--transition-fast), background var(--transition-fast);
	}

	.timeline-block:hover {
		background: var(--bg-hover);
	}

	.timeline-block.clip {
		border-color: rgba(59, 130, 246, 0.3);
		background: rgba(59, 130, 246, 0.06);
	}

	.timeline-block.live {
		border-color: rgba(220, 38, 38, 0.3);
		background: rgba(220, 38, 38, 0.06);
	}

	.timeline-block.slate {
		border-color: rgba(107, 114, 128, 0.3);
		background: rgba(107, 114, 128, 0.08);
	}

	.timeline-block.on-air {
		border-left: 3px solid var(--tally-program);
		animation: pulse-on-air 2s ease-in-out infinite;
	}

	.timeline-block.selected {
		border-color: var(--accent-blue);
		background: rgba(59, 130, 246, 0.12);
		box-shadow: 0 0 0 1px rgba(59, 130, 246, 0.3);
	}

	.timeline-block.dragging {
		opacity: 0.4;
	}

	@keyframes pulse-on-air {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.8; }
	}

	/* ── Playhead ── */

	.playhead {
		position: absolute;
		top: 0;
		bottom: 0;
		width: 2px;
		background: var(--tally-program);
		z-index: 2;
		pointer-events: none;
	}

	/* ── Block content ── */

	.block-header {
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.block-type-badge {
		padding: 0 3px;
		border-radius: 2px;
		font-family: var(--font-ui);
		font-size: 7px;
		font-weight: 700;
		letter-spacing: 0.06em;
		flex-shrink: 0;
		line-height: 1.6;
	}

	.block-type-badge.clip {
		background: rgba(59, 130, 246, 0.15);
		color: rgba(96, 165, 250, 1);
	}

	.block-type-badge.live {
		background: rgba(220, 38, 38, 0.15);
		color: rgba(248, 113, 113, 1);
	}

	.block-type-badge.slate {
		background: rgba(107, 114, 128, 0.2);
		color: var(--text-tertiary);
	}

	.block-title {
		font-family: var(--font-ui);
		font-size: var(--text-2xs, 9px);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		line-height: 1.3;
	}

	.block-duration {
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-tertiary);
	}

	/* ── Secondary event dots ── */

	.event-dots {
		display: flex;
		gap: 2px;
		margin-top: 1px;
	}

	.event-dot {
		width: 4px;
		height: 4px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	/* ── Transition indicators ── */

	.transition-indicator {
		display: flex;
		align-items: center;
		justify-content: center;
		flex-shrink: 0;
		width: 16px;
		align-self: center;
	}

	.transition-diamond {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 14px;
		height: 14px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: 2px;
		transform: rotate(45deg);
		font-family: var(--font-ui);
		font-size: 6px;
		font-weight: 700;
		color: var(--text-tertiary);
		line-height: 1;
	}

	.transition-diamond > :global(*) {
		transform: rotate(-45deg);
	}

	/* ── Drop indicator ── */

	.drop-indicator {
		width: 2px;
		min-height: 48px;
		background: var(--accent-blue);
		border-radius: 1px;
		flex-shrink: 0;
		align-self: stretch;
	}

	/* ── Context menu ── */

	.context-menu {
		position: fixed;
		z-index: 1000;
		min-width: 140px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
		padding: 3px;
		display: flex;
		flex-direction: column;
	}

	.ctx-item {
		display: block;
		width: 100%;
		padding: 4px 8px;
		background: none;
		border: none;
		border-radius: 2px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		cursor: pointer;
		text-align: left;
	}

	.ctx-item:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.ctx-item.ctx-danger:hover {
		background: rgba(220, 38, 38, 0.15);
		color: var(--tally-program);
	}

	.ctx-divider {
		height: 1px;
		background: var(--border-default);
		margin: 2px 4px;
	}
</style>
