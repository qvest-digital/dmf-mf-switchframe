<script lang="ts">
	import type { LayoutSlotState, SourceInfo } from '$lib/api/types';

	interface Props {
		slots: LayoutSlotState[];
		sources: Record<string, SourceInfo>;
		selectedSlotId: number | null;
		programSource: string;
		previewSource: string;
		onSelect: (id: number) => void;
		onToggle: (id: number) => void;
	}

	let { slots, sources, selectedSlotId, programSource, previewSource, onSelect, onToggle }: Props =
		$props();

	function tallyColor(slot: LayoutSlotState): string {
		if (slot.sourceKey === programSource) return 'var(--tally-program, #e53e3e)';
		if (slot.sourceKey === previewSource) return 'var(--tally-preview, #38a169)';
		if (slot.enabled) return 'var(--accent-gold, #d4a017)';
		return 'transparent';
	}

	function sourceLabel(slot: LayoutSlotState): string {
		return sources[slot.sourceKey]?.label || slot.sourceKey || 'No source';
	}
</script>

<div class="slot-list">
	{#if slots.length === 0}
		<div class="empty-state">No layout active</div>
	{:else}
		{#each slots as slot (slot.id)}
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div
				class="slot-row"
				class:selected={selectedSlotId === slot.id}
				onclick={() => onSelect(slot.id)}
			>
				<div class="tally-bar" style:background={tallyColor(slot)}></div>
				<span class="slot-label">{sourceLabel(slot)}</span>
				<button
					class="toggle-btn"
					class:on={slot.enabled}
					type="button"
					onclick={(e) => {
						e.stopPropagation();
						onToggle(slot.id);
					}}
				>
					{slot.enabled ? 'ON' : 'OFF'}
				</button>
			</div>
		{/each}
	{/if}
</div>

<style>
	.slot-list {
		display: flex;
		flex-direction: column;
		background: var(--bg-elevated, #1a1a1f);
		border-radius: var(--radius-sm, 4px);
		overflow: hidden;
	}

	.empty-state {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 64px;
		color: var(--text-tertiary);
		font-size: 11px;
		font-family: var(--font-ui);
	}

	.slot-row {
		display: flex;
		align-items: center;
		height: 32px;
		cursor: pointer;
		user-select: none;
		border-bottom: 1px solid var(--border-subtle);
		position: relative;
	}

	.slot-row:last-child {
		border-bottom: none;
	}

	.slot-row:hover {
		background: var(--bg-hover, #2c2c32);
	}

	.slot-row.selected {
		background: rgba(59, 130, 246, 0.12);
		box-shadow: inset 2px 0 0 0 rgba(59, 130, 246, 0.7);
	}

	.tally-bar {
		width: 4px;
		align-self: stretch;
		flex-shrink: 0;
	}

	.slot-label {
		flex: 1;
		min-width: 0;
		padding: 0 8px;
		font-size: 11px;
		font-family: var(--font-ui);
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.toggle-btn {
		flex-shrink: 0;
		padding: 2px 6px;
		margin-right: 6px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		background: transparent;
		color: var(--text-tertiary);
		font-size: 9px;
		font-weight: 600;
		font-family: var(--font-ui);
		letter-spacing: 0.04em;
		cursor: pointer;
		line-height: 1;
	}

	.toggle-btn:hover {
		background: var(--bg-hover, #2c2c32);
		color: var(--text-secondary);
	}

	.toggle-btn.on {
		background: rgba(212, 160, 23, 0.22);
		color: var(--accent-gold, #d4a017);
		border-color: rgba(212, 160, 23, 0.45);
	}
</style>
