<script lang="ts">
	import type { PlaylistItem } from '$lib/api/types';

	interface Props {
		item: PlaylistItem;
		index: number;
		isOnAir?: boolean;
		isNext?: boolean;
		isCompleted?: boolean;
	}
	let { item, index, isOnAir = false, isNext = false, isCompleted = false }: Props = $props();

	function formatDuration(ms: number | undefined): string {
		if (!ms || ms <= 0) return '--:--';
		const totalSecs = Math.floor(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}
</script>

<div
	class="playout-item"
	class:on-air={isOnAir}
	class:next={isNext}
	class:completed={isCompleted}
>
	<span class="item-index">{index + 1}</span>
	<span class="item-type-badge" class:clip={item.type === 'clip'} class:live={item.type === 'live'} class:slate={item.type === 'slate'}>
		{item.type.toUpperCase()}
	</span>
	<span class="item-title" class:strikethrough={isCompleted}>{item.title}</span>
	{#if item.hold}
		<span class="hold-badge">HOLD</span>
	{/if}
	<span class="item-duration">{formatDuration(item.duration_ms)}</span>
</div>

<style>
	.playout-item {
		display: flex;
		align-items: center;
		gap: 5px;
		padding: 3px 5px;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		transition: background var(--transition-fast), border-color var(--transition-fast);
	}

	.playout-item:hover {
		background: var(--bg-hover);
	}

	.playout-item.on-air {
		border-color: var(--tally-program);
		background: rgba(220, 38, 38, 0.08);
	}

	.playout-item.next {
		border-color: rgba(59, 130, 246, 0.5);
		background: rgba(59, 130, 246, 0.06);
	}

	.playout-item.completed {
		opacity: 0.4;
	}

	.item-index {
		width: 18px;
		text-align: right;
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.item-type-badge {
		padding: 1px 4px;
		border-radius: 2px;
		font-size: 8px;
		font-weight: 700;
		letter-spacing: 0.06em;
		flex-shrink: 0;
	}

	.item-type-badge.clip {
		background: rgba(59, 130, 246, 0.15);
		color: rgba(96, 165, 250, 1);
	}

	.item-type-badge.live {
		background: rgba(220, 38, 38, 0.15);
		color: rgba(248, 113, 113, 1);
	}

	.item-type-badge.slate {
		background: rgba(107, 114, 128, 0.2);
		color: var(--text-tertiary);
	}

	.item-title {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.item-title.strikethrough {
		text-decoration: line-through;
	}

	.hold-badge {
		padding: 0 3px;
		background: var(--accent-orange-dim);
		border-radius: 2px;
		font-size: 8px;
		font-weight: 700;
		color: var(--accent-orange);
		letter-spacing: 0.04em;
		flex-shrink: 0;
	}

	.item-duration {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		flex-shrink: 0;
		min-width: 32px;
		text-align: right;
	}
</style>
