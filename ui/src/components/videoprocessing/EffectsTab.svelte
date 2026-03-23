<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import STMapPanel from '../STMapPanel.svelte';
	import AISegmentPanel from '../AISegmentPanel.svelte';
	import { ICON_CHEVRON_DOWN, ICON_CHEVRON_RIGHT, ICON_INFO, renderIcon } from '$lib/icons';

	let { state: crState }: { state: ControlRoomState } = $props();

	let stmapsOpen = $state(true);
	let aiOpen = $state(true);

	let aiAvailable = $derived(crState.aiSegmentation?.available ?? false);
</script>

<div class="effects-tab">
	<!-- ST Maps Section -->
	<div class="section">
		<button type="button" class="section-toggle" onclick={() => (stmapsOpen = !stmapsOpen)}>
			<span class="toggle-chevron" aria-hidden="true">
				{@html renderIcon(stmapsOpen ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
			</span>
			<span class="section-title">ST maps</span>
		</button>
		{#if stmapsOpen}
			<div class="section-content stmap-content">
				<STMapPanel state={crState} />
			</div>
		{/if}
	</div>

	<!-- AI Background Section -->
	<div class="section">
		<button type="button" class="section-toggle" onclick={() => (aiOpen = !aiOpen)}>
			<span class="toggle-chevron" aria-hidden="true">
				{@html renderIcon(aiOpen ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
			</span>
			<span class="section-title">AI background</span>
			{#if aiAvailable}
				<span class="section-badge section-badge--ai">Available</span>
			{/if}
		</button>
		{#if aiOpen}
			<div class="section-content ai-section-content">
				{#if aiAvailable}
					<AISegmentPanel state={crState} />
				{:else}
					<div class="not-available">
						<span class="not-available-icon" aria-hidden="true">{@html renderIcon(ICON_INFO, 14)}</span>
						<span
							>AI background removal is not available on this server. Requires GPU acceleration and a supported segmentation model.</span
						>
					</div>
				{/if}
			</div>
		{/if}
	</div>
</div>

<style>
	.effects-tab {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow-y: auto;
		gap: 2px;
		font-family: var(--font-ui, sans-serif);
		min-height: 0;
	}

	.section {
		display: flex;
		flex-direction: column;
		min-height: 0;
	}

	.section-toggle {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 8px;
		background: var(--bg-elevated, #222);
		border: none;
		border-bottom: 1px solid var(--border-subtle, #2a2a2a);
		cursor: pointer;
		user-select: none;
		width: 100%;
		text-align: left;
		font-family: var(--font-ui, sans-serif);
		transition: background var(--transition-fast, 0.12s ease);
	}

	.section-toggle:hover {
		background: var(--bg-hover, #2a2a2a);
	}

	.toggle-chevron {
		display: flex;
		flex-shrink: 0;
		color: var(--text-tertiary, #666);
	}

	.toggle-chevron :global(svg) {
		display: block;
	}

	.section-title {
		font-size: var(--text-2xs, 10px);
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-secondary, #999);
	}

	.section-badge {
		margin-left: auto;
		font-family: var(--font-mono, monospace);
		font-size: var(--text-2xs, 10px);
		border-radius: var(--radius-sm, 3px);
		padding: 0 4px;
	}

	.section-badge--ai {
		background: color-mix(in srgb, var(--accent-purple, #a855f7) 15%, transparent);
		color: var(--accent-purple, #a855f7);
		border: 1px solid color-mix(in srgb, var(--accent-purple, #a855f7) 30%, transparent);
	}

	.section-content {
		min-height: 0;
		display: flex;
		flex-direction: column;
	}

	.stmap-content {
		overflow: auto;
	}

	/* Override STMapPanel's own padding/height since it's nested */
	.stmap-content :global(.stmap-panel) {
		height: auto;
		min-height: 0;
	}

	.ai-section-content :global(.ai-panel) {
		height: auto;
		min-height: 0;
	}

	.not-available {
		display: flex;
		align-items: flex-start;
		gap: 6px;
		padding: 8px;
		color: var(--text-tertiary, #666);
		font-size: var(--text-xs, 11px);
		line-height: 1.4;
	}

	.not-available-icon {
		display: flex;
		flex-shrink: 0;
		color: var(--text-tertiary, #666);
		opacity: 0.75;
		margin-top: 1px;
	}

	.not-available-icon :global(svg) {
		display: block;
	}
</style>
