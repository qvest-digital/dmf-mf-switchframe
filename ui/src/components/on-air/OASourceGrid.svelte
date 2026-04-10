<script lang="ts">
	import type { SourceInfo, TallyStatus, SourceHealthStatus } from '$lib/api/types';
	import { sortedSourceKeys } from '$lib/util/sort-sources';
	import OASourceTile from './OASourceTile.svelte';

	interface Props {
		sources: Record<string, SourceInfo>;
		tallyState: Record<string, TallyStatus>;
		isDualEngine?: boolean;
		onSourceClick: (sourceKey: string) => void;
		onAddSRT?: () => void;
		onOpenIO?: () => void;
	}

	let {
		sources,
		tallyState,
		isDualEngine = false,
		onSourceClick,
		onAddSRT,
		onOpenIO,
	}: Props = $props();

	let sortedKeys = $derived(sortedSourceKeys(sources));
</script>

<div class="oa-source-grid-container">
	<!-- Header -->
	<div class="grid-header">
		<h3 class="grid-title">SOURCES</h3>
		<div class="grid-actions">
			{#if onAddSRT}
				<button class="grid-action-btn" onclick={onAddSRT} title="Add SRT source">
					+ Add SRT
				</button>
			{/if}
			{#if onOpenIO}
				<button class="grid-action-btn" onclick={onOpenIO} title="Open I/O panel">
					I/O
				</button>
			{/if}
		</div>
	</div>

	<!-- Grid -->
	<div class="source-grid">
		{#each sortedKeys as key (key)}
			{@const src = sources[key]}
			<OASourceTile
				sourceKey={key}
				label={src.label || key}
				tallyState={tallyState[key] ?? 'idle'}
				health={src.status}
				{isDualEngine}
				onClick={() => onSourceClick(key)}
			/>
		{/each}
	</div>
</div>

<style>
	.oa-source-grid-container {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
	}

	.grid-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 12px;
		flex-shrink: 0;
	}

	.grid-title {
		margin: 0;
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
		text-transform: uppercase;
	}

	.grid-actions {
		display: flex;
		gap: 6px;
	}

	.grid-action-btn {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 500;
		padding: 3px 10px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		cursor: pointer;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast),
			color var(--transition-fast);
	}

	.grid-action-btn:hover {
		border-color: var(--border-strong);
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.source-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
		gap: 8px;
		padding: 0 12px 12px;
		overflow-y: auto;
		flex: 1;
	}
</style>
