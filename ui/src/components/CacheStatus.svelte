<script lang="ts">
	import { playoutCacheStatus } from '$lib/api/switch-api';
	import type { CacheStatus } from '$lib/api/types';
	import { ICON_CHEVRON_DOWN, ICON_CHEVRON_RIGHT, ICON_REFRESH, renderIcon } from '$lib/icons';

	let expanded = $state(false);
	let status = $state<CacheStatus | null>(null);
	let loading = $state(false);
	let error = $state(false);

	async function refresh() {
		loading = true;
		error = false;
		try {
			status = await playoutCacheStatus();
		} catch {
			status = null;
			error = true;
		} finally {
			loading = false;
		}
	}

	function formatBytes(bytes: number): string {
		if (bytes >= 1e12) return `${(bytes / 1e12).toFixed(1)} TB`;
		if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`;
		if (bytes >= 1e6) return `${(bytes / 1e6).toFixed(1)} MB`;
		return `${(bytes / 1e3).toFixed(0)} KB`;
	}

	const usedPercent = $derived(
		status ? Math.min(100, (status.usedBytes / Math.max(status.maxBytes, 1)) * 100) : 0
	);
	const isCritical = $derived(usedPercent > 90);

	// Auto-refresh when expanded
	$effect(() => {
		if (expanded && !status && !loading) refresh();
	});
</script>

<div class="cache-section">
	<button type="button" class="section-toggle" onclick={() => (expanded = !expanded)}>
		<span class="section-chevron" aria-hidden="true">
			{@html renderIcon(expanded ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
		</span>
		<span class="section-toggle-label">Cache Status</span>
		{#if status}
			<span class="section-badge">{status.itemCount}</span>
		{/if}
	</button>

	{#if expanded}
		<div class="section-body">
			{#if loading && !status}
				<div class="cache-loading">Loading...</div>
			{:else if error && !status}
				<div class="cache-error">Failed to load cache status</div>
			{:else if status}
				<div class="cache-bar">
					<div class="cache-track">
						<div
							class="cache-fill"
							class:critical={isCritical}
							style:width="{usedPercent}%"
						></div>
					</div>
					<span class="cache-usage">{formatBytes(status.usedBytes)} / {formatBytes(status.maxBytes)}</span>
				</div>

				<div class="cache-stats">
					<span>{status.itemCount} cached</span>
					{#if status.fetchingCount > 0}
						<span class="stat-fetching">{status.fetchingCount} fetching</span>
					{/if}
					{#if status.errorCount > 0}
						<span class="stat-error">{status.errorCount} errors</span>
					{/if}
				</div>

				<div class="cache-actions">
					<button
						type="button"
						class="icon-btn"
						title="Refresh"
						disabled={loading}
						onclick={refresh}
					>
						{@html renderIcon(ICON_REFRESH, 12)}
					</button>
				</div>
			{/if}
		</div>
	{/if}
</div>

<style>
	.cache-section {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		overflow: hidden;
		flex-shrink: 0;
	}

	.section-toggle {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 5px 8px;
		border: none;
		background: var(--bg-control);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		cursor: pointer;
		text-align: left;
		transition: background var(--transition-fast);
	}

	.section-toggle:hover {
		background: var(--bg-hover);
	}

	.section-chevron {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.section-toggle-label {
		flex: 1;
	}

	.section-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		background: var(--bg-base);
		padding: 0 4px;
		border-radius: var(--radius-sm);
	}

	.section-body {
		padding: 6px 8px;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.cache-loading,
	.cache-error {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.cache-error {
		color: var(--color-error);
	}

	.cache-bar {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.cache-track {
		height: 4px;
		background: var(--bg-hover);
		border-radius: 2px;
		overflow: hidden;
	}

	.cache-fill {
		height: 100%;
		background: var(--accent-blue);
		border-radius: 2px;
		transition: width 0.3s ease;
	}

	.cache-fill.critical {
		background: var(--color-error);
	}

	.cache-usage {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		text-align: right;
	}

	.cache-stats {
		display: flex;
		gap: 8px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
	}

	.stat-fetching {
		color: var(--accent-blue);
	}

	.stat-error {
		color: var(--color-error);
	}

	.cache-actions {
		display: flex;
		justify-content: flex-end;
	}

	.icon-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 22px;
		height: 22px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-control);
		color: var(--text-secondary);
		cursor: pointer;
		padding: 0;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.icon-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.icon-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
