<script lang="ts">
	import type { PlayoutChannelState, PlaylistItem } from '$lib/api/types';

	interface Props {
		channel: PlayoutChannelState;
		currentItem?: PlaylistItem;
		nextItem?: PlaylistItem;
		items: PlaylistItem[];
	}
	let { channel, currentItem, nextItem, items }: Props = $props();

	const isOnAir = $derived(channel.state === 'on_air');
	const isLiveItem = $derived(currentItem?.type === 'live');

	// ── Live elapsed timer (counts up from item start) ──
	let lastItemIndex = $state(-1);
	let itemStartedAt = $state(0);
	let liveElapsed = $state('0:00:00');

	// Reset timer when item index changes
	$effect(() => {
		const idx = channel.itemIndex;
		if (idx !== lastItemIndex) {
			lastItemIndex = idx;
			itemStartedAt = Date.now();
		}
	});

	$effect(() => {
		if (!isOnAir || !isLiveItem) {
			liveElapsed = '0:00:00';
			return;
		}
		const interval = setInterval(() => {
			const ms = Date.now() - itemStartedAt;
			const totalSec = Math.max(0, Math.floor(ms / 1000));
			const h = Math.floor(totalSec / 3600);
			const m = Math.floor((totalSec % 3600) / 60);
			const s = totalSec % 60;
			liveElapsed = `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
		}, 200);
		return () => clearInterval(interval);
	});

	// ── Clip countdown ──
	function formatCountdown(ms: number | undefined): string {
		if (!ms || ms <= 0) return '0:00';
		const totalSecs = Math.ceil(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${String(s).padStart(2, '0')}`;
	}

	// ── Playlist progress ──
	const itemProgress = $derived(
		channel.itemIndex >= 0 && items.length > 0
			? `${channel.itemIndex + 1} of ${items.length}`
			: '--'
	);

	// ── Total remaining time ──
	const totalRemainingMs = $derived.by(() => {
		if (channel.itemIndex < 0 || items.length === 0) return 0;
		let remaining = channel.itemRemainMs ?? 0;
		for (let i = channel.itemIndex + 1; i < items.length; i++) {
			if (items[i].type !== 'live') {
				remaining += items[i].duration_ms ?? 0;
			}
		}
		return remaining;
	});

	function formatTotalRemaining(ms: number): string {
		if (ms <= 0) return '0:00';
		const totalSecs = Math.ceil(ms / 1000);
		const h = Math.floor(totalSecs / 3600);
		const m = Math.floor((totalSecs % 3600) / 60);
		const s = totalSecs % 60;
		if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
		return `${m}:${String(s).padStart(2, '0')}`;
	}

	// ── Wall clock ──
	let clockTime = $state('');
	$effect(() => {
		function tick() {
			const now = new Date();
			clockTime = now.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
		}
		tick();
		const interval = setInterval(tick, 1000);
		return () => clearInterval(interval);
	});

	// ── Upcoming transition ──
	const nextTransition = $derived(
		nextItem?.transition?.type && nextItem.transition.type !== 'cut'
			? `${nextItem.transition.type} ${nextItem.transition.duration_ms ? (nextItem.transition.duration_ms / 1000).toFixed(1) + 's' : ''}`
			: 'cut'
	);
</script>

<div class="status-dashboard" data-testid="status-dashboard">
	<!-- Primary: Big timer -->
	<div class="timer-section">
		{#if isOnAir && isLiveItem}
			<div class="big-timer live-timer">{liveElapsed}</div>
			<div class="timer-label">ELAPSED</div>
		{:else if isOnAir}
			<div class="big-timer countdown-timer" class:warning={channel.itemRemainMs != null && channel.itemRemainMs < 10000}>
				{formatCountdown(channel.itemRemainMs)}
			</div>
			<div class="timer-label">REMAINING</div>
		{:else}
			<div class="big-timer idle-timer">--:--</div>
			<div class="timer-label">{channel.state === 'completed' ? 'COMPLETE' : 'STOPPED'}</div>
		{/if}
	</div>

	<!-- Info grid -->
	<div class="info-grid">
		<div class="info-row">
			<span class="info-label">Item</span>
			<span class="info-value">{itemProgress}</span>
		</div>
		<div class="info-row">
			<span class="info-label">Show remaining</span>
			<span class="info-value">{formatTotalRemaining(totalRemainingMs)}</span>
		</div>
		<div class="info-row">
			<span class="info-label">Next transition</span>
			<span class="info-value">{nextTransition}</span>
		</div>
		{#if channel.mode}
			<div class="info-row">
				<span class="info-label">Mode</span>
				<span class="info-value mode-value" class:auto={channel.mode === 'automated'} class:manual={channel.mode === 'manual'}>
					{channel.mode?.toUpperCase() ?? '--'}
				</span>
			</div>
		{/if}
	</div>

	<!-- Wall clock -->
	<div class="wall-clock">{clockTime}</div>
</div>

<style>
	.status-dashboard {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 12px;
		height: 100%;
		padding: 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
	}

	.timer-section {
		text-align: center;
	}

	.big-timer {
		font-family: var(--font-mono);
		font-size: 36px;
		font-weight: 700;
		letter-spacing: 0.02em;
		line-height: 1.1;
	}

	.live-timer { color: var(--tally-program, #dc2626); }
	.countdown-timer { color: var(--text-primary); }
	.countdown-timer.warning { color: var(--tally-program); animation: pulse-warn 1s ease-in-out infinite; }
	.idle-timer { color: var(--text-tertiary); opacity: 0.4; }

	@keyframes pulse-warn {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}

	.timer-label {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.1em;
		color: var(--text-tertiary);
		margin-top: 2px;
	}

	.info-grid {
		display: flex;
		flex-direction: column;
		gap: 4px;
		width: 100%;
		max-width: 200px;
	}

	.info-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.info-label {
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-tertiary);
		letter-spacing: 0.02em;
	}

	.info-value {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-secondary);
		font-weight: 600;
	}

	.mode-value.auto { color: var(--tally-preview); }
	.mode-value.manual { color: var(--accent-orange); }

	.wall-clock {
		font-family: var(--font-mono);
		font-size: 14px;
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.04em;
	}
</style>
