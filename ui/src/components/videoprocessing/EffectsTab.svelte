<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import STMapPanel from '../STMapPanel.svelte';
	import AISegmentPanel from '../AISegmentPanel.svelte';

	let { state: crState }: { state: ControlRoomState } = $props();

	let stmapsOpen = $state(true);
	let aiOpen = $state(true);

	let aiAvailable = $derived(crState.aiSegmentation?.available ?? false);
</script>

<div class="effects-tab">
	<!-- ST Maps Section -->
	<div class="section">
		<button class="section-toggle" onclick={() => (stmapsOpen = !stmapsOpen)}>
			<span class="toggle-arrow">{stmapsOpen ? '\u25BE' : '\u25B8'}</span>
			<span class="section-title">ST MAPS</span>
		</button>
		{#if stmapsOpen}
			<div class="section-content stmap-content">
				<STMapPanel state={crState} />
			</div>
		{/if}
	</div>

	<!-- AI Background Section -->
	<div class="section">
		<button class="section-toggle" onclick={() => (aiOpen = !aiOpen)}>
			<span class="toggle-arrow">{aiOpen ? '\u25BE' : '\u25B8'}</span>
			<span class="section-title">AI BACKGROUND</span>
			{#if aiAvailable}
				<span class="ai-badge">Available</span>
			{/if}
		</button>
		{#if aiOpen}
			<div class="section-content">
				{#if aiAvailable}
					<AISegmentPanel state={crState} />
				{:else}
					<div class="not-available">
						<span class="not-available-icon">&#9432;</span>
						<span>AI background removal is not available on this server. Requires GPU acceleration and a supported segmentation model.</span>
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
	}

	.section-toggle:hover {
		background: var(--bg-hover, #2a2a2a);
	}

	.toggle-arrow {
		font-size: var(--text-xs, 11px);
		color: var(--text-tertiary, #666);
		width: 10px;
		flex-shrink: 0;
	}

	.section-title {
		font-family: var(--font-ui, sans-serif);
		font-size: var(--text-2xs, 10px);
		font-weight: 600;
		letter-spacing: 0.06em;
		color: var(--text-secondary, #999);
	}

	.ai-badge {
		font-family: var(--font-mono, monospace);
		font-size: var(--text-2xs, 10px);
		background: rgba(168, 85, 247, 0.15);
		color: #a855f7;
		border: 1px solid rgba(168, 85, 247, 0.3);
		border-radius: var(--radius-sm, 3px);
		padding: 0 4px;
		margin-left: auto;
	}

	.section-content {
		min-height: 0;
	}

	/* STMapPanel uses height: 100% internally; give it a reasonable constraint */
	.stmap-content {
		max-height: 400px;
		overflow-y: auto;
	}

	/* Override STMapPanel's own padding/height since it's nested */
	.stmap-content :global(.stmap-panel) {
		height: auto;
	}

	/* Override AISegmentPanel's own height since it's nested */
	.section-content :global(.ai-panel) {
		height: auto;
	}

	.not-available {
		display: flex;
		align-items: flex-start;
		gap: 6px;
		padding: 8px;
		color: var(--text-tertiary, #666);
		font-family: var(--font-ui, sans-serif);
		font-size: var(--text-xs, 11px);
		line-height: 1.4;
	}

	.not-available-icon {
		font-size: 14px;
		flex-shrink: 0;
		opacity: 0.6;
	}
</style>
