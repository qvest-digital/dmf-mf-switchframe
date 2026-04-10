<script lang="ts">
	interface Props {
		title: string;
		scheduledTime?: string;
		duration?: number;
		elapsed?: number;
		itemType: 'live' | 'clip' | 'pod' | 'bug' | 'scte';
		state: 'done' | 'on_air' | 'armed' | 'ready';
		spotCount?: number;
		isExpanded?: boolean;
	}

	let {
		title,
		scheduledTime,
		duration,
		elapsed,
		itemType,
		state,
		spotCount,
		isExpanded = false,
	}: Props = $props();

	const typeColorMap: Record<string, string> = {
		live: 'var(--accent-blue)',
		clip: 'var(--item-clip)',
		pod: 'var(--accent-yellow)',
		bug: 'var(--accent-green)',
		scte: 'var(--tally-program)',
	};

	const barColor = $derived(typeColorMap[itemType] ?? 'var(--text-secondary)');

	const progressPct = $derived(
		state === 'on_air' && duration && duration > 0 && elapsed != null
			? Math.min(100, (elapsed / duration) * 100)
			: 0
	);

	function formatDuration(secs: number): string {
		const m = Math.floor(secs / 60);
		const s = Math.floor(secs % 60);
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	const badgeConfig: Record<string, { text: string; cls: string }> = {
		on_air: { text: 'ON AIR', cls: 'badge-on-air' },
		armed: { text: 'ARMED', cls: 'badge-armed' },
		ready: { text: 'READY', cls: 'badge-ready' },
		done: { text: '✓ DONE', cls: 'badge-done' },
	};

	const badge = $derived(badgeConfig[state]);
</script>

<div
	class="schedule-item state-{state}"
	class:is-done={state === 'done'}
	class:is-on-air={state === 'on_air'}
	class:is-armed={state === 'armed'}
	role="listitem"
	data-item-type={itemType}
	data-state={state}
>
	<div class="color-bar" style:background-color={barColor}></div>
	<div class="item-content">
		<div class="item-row">
			<div class="item-left">
				{#if scheduledTime}
					<span class="scheduled-time">{scheduledTime}</span>
				{/if}
				<span class="item-title">{title}</span>
				{#if itemType === 'pod' && spotCount != null}
					<span class="spot-count">{spotCount} spot{spotCount !== 1 ? 's' : ''}</span>
				{/if}
			</div>
			<div class="item-right">
				{#if duration != null}
					<span class="item-duration">{formatDuration(duration)}</span>
				{/if}
				{#if badge}
					<span class="state-badge {badge.cls}">{badge.text}</span>
				{/if}
			</div>
		</div>
	</div>
	{#if state === 'on_air' && duration && duration > 0}
		<div class="progress-bar">
			<div
				class="progress-fill"
				style:width="{progressPct}%"
				style:background-color={barColor}
			></div>
		</div>
	{/if}
</div>

<style>
	.schedule-item {
		display: flex;
		position: relative;
		border-bottom: 1px solid var(--border-subtle);
		transition: opacity 0.15s ease, background-color 0.15s ease;
	}

	.schedule-item.is-done {
		opacity: 0.5;
	}

	.schedule-item.is-on-air {
		background: rgba(255, 255, 255, 0.03);
	}

	.color-bar {
		width: 3px;
		flex-shrink: 0;
		align-self: stretch;
	}

	.schedule-item.is-armed .color-bar {
		box-shadow: 0 0 6px rgba(234, 179, 8, 0.5);
	}

	.item-content {
		flex: 1;
		min-width: 0;
		padding: 6px 12px;
	}

	.item-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
	}

	.item-left {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}

	.item-right {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-shrink: 0;
	}

	.scheduled-time {
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 500;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.item-title {
		font-family: var(--font-ui);
		font-size: 13px;
		font-weight: 500;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.spot-count {
		font-family: var(--font-ui);
		font-size: 11px;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.item-duration {
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 400;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.state-badge {
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		padding: 2px 6px;
		border-radius: 3px;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.badge-on-air {
		background: var(--tally-program-dim);
		color: var(--tally-program);
		border: 1px solid var(--tally-program-medium);
	}

	.badge-armed {
		background: var(--accent-yellow-dim);
		color: var(--accent-yellow);
		border: 1px solid rgba(234, 179, 8, 0.3);
	}

	.badge-ready {
		background: rgba(255, 255, 255, 0.05);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
	}

	.badge-done {
		background: transparent;
		color: var(--text-secondary);
		border: 1px solid transparent;
		opacity: 0.6;
	}

	.progress-bar {
		position: absolute;
		bottom: 0;
		left: 3px;
		right: 0;
		height: 2px;
		background: rgba(255, 255, 255, 0.05);
	}

	.progress-fill {
		height: 100%;
		transition: width 0.3s linear;
	}
</style>
