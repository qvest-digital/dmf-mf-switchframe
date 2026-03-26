<script lang="ts">
	import type { PlaylistItem, SecondaryEvent } from '$lib/api/types';

	interface Props {
		items: PlaylistItem[];
		currentIndex: number;
		progress?: number;
		itemRemainMs?: number;
		onNext: () => void;
		onSkipTo: (index: number) => void;
	}
	let { items, currentIndex, progress = 0, itemRemainMs, onNext, onSkipTo }: Props = $props();

	// ── Skip-to confirmation state ──
	let pendingSkip = $state<{ index: number; timer: ReturnType<typeof setTimeout> } | null>(null);

	// ── Auto-scroll on-air item into view ──
	$effect(() => {
		if (currentIndex >= 0) {
			const el = document.getElementById(`rundown-item-${currentIndex}`);
			if (el && typeof el.scrollIntoView === 'function') {
				el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
			}
		}
	});

	// ── Helpers ──

	function formatDuration(ms: number | undefined): string {
		if (!ms || ms <= 0) return '0:00';
		const totalSecs = Math.floor(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	function formatRemaining(ms: number | undefined): string {
		if (ms == null || ms <= 0) return '\u22120:00';
		const totalSecs = Math.ceil(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `\u2212${m}:${s.toString().padStart(2, '0')}`;
	}

	function hasScte35(item: PlaylistItem): boolean {
		return !!item.secondary_events?.some(e => e.type === 'scte35_cue');
	}

	function eventDotColor(evt: SecondaryEvent): string {
		if (evt.type.startsWith('graphics')) return 'var(--accent-green, #22c55e)';
		if (evt.type.startsWith('audio')) return 'var(--accent-blue, #3b82f6)';
		if (evt.type.startsWith('caption')) return 'var(--accent-orange, #f97316)';
		if (evt.type === 'scte35_cue') return 'var(--tally-program, #dc2626)';
		return 'var(--text-tertiary)';
	}

	function handleRowClick(index: number) {
		if (pendingSkip && pendingSkip.index === index) {
			// Second click — execute skip
			clearTimeout(pendingSkip.timer);
			pendingSkip = null;
			onSkipTo(index);
		} else {
			// First click — show confirmation
			if (pendingSkip) clearTimeout(pendingSkip.timer);
			const timer = setTimeout(() => {
				pendingSkip = null;
			}, 2000);
			pendingSkip = { index, timer };
		}
	}
</script>

<div class="rundown-container" role="list" aria-label="Playout rundown">
	{#if items.length === 0}
		<div class="rundown-empty">No items in playlist</div>
	{:else}
		{#each items as item, i}
			{@const isCompleted = currentIndex >= 0 && i < currentIndex}
			{@const isOnAir = i === currentIndex}
			{@const isNext = currentIndex >= 0 && i === currentIndex + 1}
			{@const isFuture = currentIndex < 0 || i > currentIndex}
			<!-- svelte-ignore a11y_no_noninteractive_tabindex a11y_no_noninteractive_element_interactions -->
			<div
				id="rundown-item-{i}"
				class="rundown-row"
				class:completed={isCompleted}
				class:on-air={isOnAir}
				class:is-next={isNext}
				class:future={isFuture && !isNext}
				class:pending-skip={pendingSkip?.index === i}
				role="listitem"
				tabindex="0"
				aria-label="{item.type} {item.title}"
				ondblclick={() => handleRowClick(i)}
				onkeydown={(e) => { if (e.key === 'Enter') handleRowClick(i); }}
			>
				<div class="row-main">
					<!-- Index -->
					<span class="row-index">
						{#if isOnAir}
							<span class="play-icon" aria-label="On air">&#9654;</span>
						{/if}
						{i + 1}
					</span>

					<!-- Type badge -->
					<span
						class="type-badge"
						class:clip={item.type === 'clip'}
						class:live={item.type === 'live'}
						class:slate={item.type === 'slate'}
					>
						{item.type.toUpperCase()}
					</span>

					<!-- Title -->
					<span class="row-title">{item.title}</span>

					<!-- Duration -->
					<span class="row-duration">
						{#if item.type === 'live'}
							<span class="live-label">LIVE</span>
						{:else if item.loop}
							&#8734; {formatDuration(item.duration_ms)}
						{:else}
							{#if isCompleted}
								<span class="duration-struck">{formatDuration(item.duration_ms)}</span>
							{:else}
								{formatDuration(item.duration_ms)}
							{/if}
						{/if}
					</span>

					<!-- Status indicators -->
					<span class="row-indicators">
						{#if isCompleted}
							<span class="indicator-check" title="Completed">&#10003;</span>
						{/if}
						{#if hasScte35(item)}
							<span class="indicator-scte35" title="SCTE-35 event">&#9889;</span>
						{/if}
						{#if item.loop}
							<span class="indicator-loop" title="Loop">&#128257;</span>
						{/if}
					</span>
				</div>

				<!-- Next label -->
				{#if isNext}
					<span class="next-badge">NEXT</span>
				{/if}

				<!-- Skip-to confirmation tooltip -->
				{#if pendingSkip?.index === i}
					<span class="skip-tooltip">{i <= currentIndex ? 'Restart from' : 'Skip to'} "{item.title}"?</span>
				{/if}

				<!-- Secondary event dots -->
				{#if item.secondary_events && item.secondary_events.length > 0}
					<div class="event-dots">
						{#each item.secondary_events as evt}
							<span class="event-dot" style="background: {eventDotColor(evt)}" title={evt.type}></span>
						{/each}
					</div>
				{/if}

				<!-- Progress bar (on-air item only) -->
				{#if isOnAir && progress != null && progress > 0}
					<div class="progress-row">
						<div class="progress-track">
							<div class="progress-fill" style="width: {progress * 100}%"></div>
						</div>
						{#if itemRemainMs != null}
							<span class="remain-time">{formatRemaining(itemRemainMs)}</span>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	{/if}
</div>

<style>
	.rundown-container {
		width: 100%;
		height: 100%;
		overflow-y: auto;
		overflow-x: hidden;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
	}

	.rundown-container::-webkit-scrollbar { width: 3px; }
	.rundown-container::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	.rundown-empty {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 56px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		opacity: 0.5;
	}

	/* ── Row ── */

	.rundown-row {
		position: relative;
		display: flex;
		flex-direction: column;
		padding: 3px 6px;
		min-height: 26px;
		border-bottom: 1px solid var(--border-default);
		cursor: default;
		transition: background var(--transition-fast), opacity var(--transition-fast);
	}

	.rundown-row:last-child {
		border-bottom: none;
	}

	.rundown-row:hover {
		background: var(--bg-hover);
	}

	/* ── Visual states ── */

	.rundown-row.completed {
		opacity: 0.35;
	}

	.rundown-row.on-air {
		border-left: 3px solid var(--tally-program, #dc2626);
		background: rgba(220, 38, 38, 0.12);
		animation: pulse-border 2s ease-in-out infinite;
	}

	.rundown-row.is-next {
		border-left: 3px solid rgba(59, 130, 246, 0.5);
		background: rgba(59, 130, 246, 0.06);
	}

	.rundown-row:not(.completed) {
		cursor: pointer;
	}

	.rundown-row.pending-skip {
		background: rgba(59, 130, 246, 0.12);
	}

	@keyframes pulse-border {
		0%, 100% { border-left-color: var(--tally-program, #dc2626); }
		50% { border-left-color: rgba(220, 38, 38, 0.4); }
	}

	/* ── Main row content ── */

	.row-main {
		display: flex;
		align-items: center;
		gap: 6px;
		min-height: 20px;
	}

	.row-index {
		display: flex;
		align-items: center;
		gap: 2px;
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		min-width: 22px;
		flex-shrink: 0;
		text-align: right;
		justify-content: flex-end;
	}

	.play-icon {
		color: var(--tally-program, #dc2626);
		font-size: 8px;
	}

	/* ── Type badge ── */

	.type-badge {
		padding: 0 3px;
		border-radius: 2px;
		font-family: var(--font-ui);
		font-size: 7px;
		font-weight: 700;
		letter-spacing: 0.06em;
		flex-shrink: 0;
		line-height: 1.6;
	}

	.type-badge.clip {
		background: rgba(59, 130, 246, 0.15);
		color: rgba(96, 165, 250, 1);
	}

	.type-badge.live {
		background: rgba(220, 38, 38, 0.15);
		color: rgba(248, 113, 113, 1);
	}

	.type-badge.slate {
		background: rgba(107, 114, 128, 0.2);
		color: var(--text-tertiary);
	}

	/* ── Title ── */

	.row-title {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-2xs, 9px);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		line-height: 1.3;
	}

	.on-air .row-title {
		color: var(--text-primary);
		font-weight: 600;
	}

	/* ── Duration ── */

	.row-duration {
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-tertiary);
		flex-shrink: 0;
		min-width: 30px;
		text-align: right;
	}

	.live-label {
		color: rgba(248, 113, 113, 1);
		font-weight: 700;
		letter-spacing: 0.04em;
	}

	.duration-struck {
		text-decoration: line-through;
	}

	/* ── Indicators ── */

	.row-indicators {
		display: flex;
		gap: 3px;
		align-items: center;
		flex-shrink: 0;
		min-width: 16px;
	}

	.indicator-check {
		font-size: 9px;
		color: var(--tally-preview, #22c55e);
	}

	.indicator-scte35 {
		font-size: 9px;
		color: rgba(250, 204, 21, 0.9);
	}

	.indicator-loop {
		font-size: 8px;
	}

	/* ── Next badge ── */

	.next-badge {
		position: absolute;
		top: 2px;
		right: 4px;
		padding: 0 3px;
		border-radius: 2px;
		font-family: var(--font-ui);
		font-size: 6px;
		font-weight: 700;
		letter-spacing: 0.06em;
		background: rgba(59, 130, 246, 0.15);
		color: rgba(96, 165, 250, 1);
		line-height: 1.6;
	}

	/* ── Skip tooltip ── */

	.skip-tooltip {
		position: absolute;
		top: -2px;
		right: 4px;
		padding: 1px 5px;
		border-radius: 2px;
		font-family: var(--font-ui);
		font-size: 7px;
		background: rgba(59, 130, 246, 0.9);
		color: white;
		white-space: nowrap;
		z-index: 5;
		pointer-events: none;
	}

	/* ── Event dots ── */

	.event-dots {
		display: flex;
		gap: 2px;
		margin-top: 1px;
		padding-left: 28px;
	}

	.event-dot {
		width: 4px;
		height: 4px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	/* ── Progress bar ── */

	.progress-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 2px 0 1px 28px;
	}

	.progress-track {
		flex: 1;
		height: 4px;
		background: var(--bg-base);
		border-radius: 2px;
		border: 1px solid var(--border-default);
		overflow: hidden;
	}

	.progress-fill {
		height: 100%;
		background: var(--tally-program, #dc2626);
		border-radius: 2px 0 0 2px;
		transition: width 0.25s linear;
	}

	.remain-time {
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 700;
		color: var(--text-primary);
		flex-shrink: 0;
		min-width: 36px;
		text-align: right;
	}
</style>
