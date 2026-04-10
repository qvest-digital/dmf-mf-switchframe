<script lang="ts">
	interface Props {
		nextItemTitle?: string;
		nextItemDuration?: number;
		nextItemType?: 'live' | 'clip' | 'pod' | 'slate';
	}

	let {
		nextItemTitle = 'No item cued',
		nextItemDuration,
		nextItemType,
	}: Props = $props();

	function formatDuration(totalSeconds: number): string {
		const mins = Math.floor(totalSeconds / 60);
		const secs = Math.floor(totalSeconds % 60);
		return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
	}
</script>

<div class="oa-next-monitor">
	<div class="next-thumbnail">
		<span class="next-thumb-label">NEXT</span>
	</div>

	<div class="next-info">
		<span class="next-label">NEXT</span>
		<span class="next-title">{nextItemTitle}</span>
		{#if nextItemDuration != null}
			<span class="next-duration">{formatDuration(nextItemDuration)}</span>
		{/if}
	</div>
</div>

<style>
	.oa-next-monitor {
		height: 120px;
		background: var(--bg-surface);
		border-top: 1px solid var(--border-default);
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 0 12px;
		flex-shrink: 0;
	}

	/* ── Thumbnail placeholder ── */
	.next-thumbnail {
		width: 160px;
		height: 90px;
		flex-shrink: 0;
		background: var(--bg-canvas);
		border-radius: var(--radius-xs);
		display: flex;
		align-items: center;
		justify-content: center;
	}

	.next-thumb-label {
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 500;
		color: var(--text-secondary);
		letter-spacing: 0.08em;
		opacity: 0.5;
	}

	/* ── Info block ── */
	.next-info {
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;
	}

	.next-label {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.08em;
	}

	.next-title {
		font-family: var(--font-ui);
		font-size: 12px;
		font-weight: 500;
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.next-duration {
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 400;
		color: var(--text-secondary);
	}
</style>
