<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import STMapPanel from '../STMapPanel.svelte';
	import AISegmentPanel from '../AISegmentPanel.svelte';
	import { ICON_INFO, renderIcon } from '$lib/icons';

	let { state: crState }: { state: ControlRoomState } = $props();

	let aiAvailable = $derived(crState.aiSegmentation?.available ?? false);
</script>

<div class="effects-tab">
	<div class="col stmap-col">
		<div class="col-header">
			<span class="col-title">ST MAPS</span>
		</div>
		<div class="col-body">
			<STMapPanel state={crState} />
		</div>
	</div>
	<div class="col ai-col">
		<div class="col-header">
			<span class="col-title">AI BACKGROUND</span>
			{#if aiAvailable}
				<span class="avail-badge">Available</span>
			{/if}
		</div>
		<div class="col-body">
			{#if aiAvailable}
				<AISegmentPanel state={crState} />
			{:else}
				<div class="not-available">
					<span class="not-available-icon" aria-hidden="true">{@html renderIcon(ICON_INFO, 13)}</span>
					<span>Not available. Requires GPU + segmentation model.</span>
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.effects-tab {
		display: grid;
		grid-template-columns: 1fr 1fr;
		height: 100%;
		gap: 0;
		font-family: var(--font-ui, sans-serif);
		min-height: 0;
		overflow: hidden;
	}

	.col {
		display: flex;
		flex-direction: column;
		min-height: 0;
		overflow: hidden;
	}

	.stmap-col {
		border-right: 1px solid var(--border-subtle, #2a2a2a);
	}

	.col-header {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 8px;
		background: var(--bg-elevated, #222);
		border-bottom: 1px solid var(--border-subtle, #2a2a2a);
		flex-shrink: 0;
	}

	.col-title {
		font-size: var(--text-2xs, 10px);
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-secondary, #999);
	}

	.avail-badge {
		margin-left: auto;
		font-family: var(--font-mono, monospace);
		font-size: var(--text-2xs, 10px);
		border-radius: var(--radius-sm, 3px);
		padding: 0 4px;
		background: color-mix(in srgb, var(--accent-purple, #a855f7) 15%, transparent);
		color: var(--accent-purple, #a855f7);
		border: 1px solid color-mix(in srgb, var(--accent-purple, #a855f7) 30%, transparent);
	}

	.col-body {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.col-body :global(.stmap-panel) {
		height: auto;
		min-height: 0;
	}

	.col-body :global(.ai-panel) {
		height: auto;
		min-height: 0;
	}

	.col-body :global(.ai-panel .panel-header) {
		display: none;
	}

	.not-available {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 10px 8px;
		color: var(--text-tertiary, #666);
		font-size: var(--text-xs, 11px);
		line-height: 1.4;
	}

	.not-available-icon {
		display: flex;
		flex-shrink: 0;
		color: var(--text-tertiary, #666);
		opacity: 0.75;
	}

	.not-available-icon :global(svg) {
		display: block;
	}
</style>
