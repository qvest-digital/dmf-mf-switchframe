<script lang="ts">
	import { tick } from 'svelte';
	import type { PlayoutChannelState, PlaylistItem } from '$lib/api/types';
	import OAScheduleItem from './OAScheduleItem.svelte';

	interface Props {
		playoutChannel: PlayoutChannelState;
		items?: PlaylistItem[];
		onEditSchedule?: () => void;
		onShowAsRun?: () => void;
	}

	let {
		playoutChannel,
		items = [],
		onEditSchedule,
		onShowAsRun,
	}: Props = $props();

	let scrollContainer: HTMLElement | undefined = $state();

	// Map PlaylistItem.type to OAScheduleItem itemType
	function mapItemType(item: PlaylistItem): 'live' | 'clip' | 'pod' | 'bug' | 'scte' {
		if (item.secondary_events?.some(e => e.type === 'scte35_cue')) return 'scte';
		if (item.type === 'live') return 'live';
		if (item.type === 'clip') return 'clip';
		if (item.type === 'slate') return 'clip';
		return 'clip';
	}

	// Derive item state from channel state and index
	function deriveState(index: number): 'done' | 'on_air' | 'armed' | 'ready' {
		const current = playoutChannel.itemIndex;
		if (current >= 0 && index < current) return 'done';
		if (index === current && playoutChannel.state === 'on_air') return 'on_air';
		if (index === current + 1 && playoutChannel.state === 'on_air') return 'armed';
		return 'ready';
	}

	// Compute elapsed seconds for the current on-air item
	function deriveElapsed(index: number, item: PlaylistItem): number | undefined {
		if (deriveState(index) !== 'on_air') return undefined;
		if (playoutChannel.itemProgress == null || item.duration_ms == null) return undefined;
		return (playoutChannel.itemProgress * item.duration_ms) / 1000;
	}

	// Auto-scroll to keep the on-air item visible
	$effect(() => {
		const idx = playoutChannel.itemIndex;
		if (idx < 0 || !scrollContainer) return;

		tick().then(() => {
			const items = scrollContainer?.querySelectorAll('[role="listitem"]');
			if (!items || idx >= items.length) return;
			const el = items[idx] as HTMLElement;
			el.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' });
		});
	});
</script>

<div class="schedule-panel">
	<div class="schedule-header">
		<span class="section-title">PLAYOUT SCHEDULE</span>
		<div class="header-actions">
			{#if onEditSchedule}
				<button class="header-btn" onclick={onEditSchedule} title="Edit schedule">Edit</button>
			{/if}
			{#if onShowAsRun}
				<button class="header-btn" onclick={onShowAsRun} title="Show as-run log">As-Run</button>
			{/if}
		</div>
	</div>

	<div class="schedule-list" bind:this={scrollContainer} role="list" aria-label="Playout schedule">
		{#if items.length === 0}
			<div class="empty-state">No items in schedule</div>
		{:else}
			{#each items as item, i (item.id)}
				<OAScheduleItem
					title={item.title}
					scheduledTime={item.scheduled_time}
					duration={item.duration_ms != null ? item.duration_ms / 1000 : undefined}
					elapsed={deriveElapsed(i, item)}
					itemType={mapItemType(item)}
					state={deriveState(i)}
				/>
			{/each}
		{/if}
	</div>
</div>

<style>
	.schedule-panel {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
		background: var(--bg-surface);
		border-radius: var(--radius-sm);
	}

	.schedule-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 12px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.section-title {
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
		text-transform: uppercase;
	}

	.header-actions {
		display: flex;
		gap: 6px;
	}

	.header-btn {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 500;
		color: var(--text-secondary);
		background: rgba(255, 255, 255, 0.05);
		border: 1px solid var(--border-subtle);
		border-radius: 3px;
		padding: 3px 8px;
		cursor: pointer;
		transition: background-color 0.1s ease, color 0.1s ease;
	}

	.header-btn:hover {
		background: rgba(255, 255, 255, 0.1);
		color: var(--text-primary);
	}

	.schedule-list {
		flex: 1;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.empty-state {
		padding: 24px 12px;
		text-align: center;
		font-family: var(--font-ui);
		font-size: 12px;
		color: var(--text-secondary);
	}
</style>
