<script lang="ts">
	import type { ControlRoomState, PlayoutChannelState, PlaylistItem } from '$lib/api/types';
	import PlayoutItem from './PlayoutItem.svelte';
	import PlayoutTransport from './PlayoutTransport.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	let selectedChannel = $state(1);

	const channels = $derived(crState.playout?.channels ?? []);
	const channel = $derived<PlayoutChannelState>(
		channels.find(c => c.id === selectedChannel) ?? {
			id: selectedChannel,
			state: 'stopped',
			itemIndex: -1,
			itemCount: 0,
		}
	);

	const isOnAir = $derived(channel.state === 'on_air');
	const isCued = $derived(channel.state === 'cued');

	// Demo playlist items for UI scaffolding — replaced by real data from state when available
	const playlistItems = $derived<PlaylistItem[]>([]);

	function formatCountdown(ms: number | undefined): string {
		if (!ms || ms <= 0) return '--:--';
		const totalSecs = Math.ceil(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	function getItemState(index: number): 'on-air' | 'next' | 'completed' | 'upcoming' {
		if (channel.itemIndex < 0) return 'upcoming';
		if (index < channel.itemIndex) return 'completed';
		if (index === channel.itemIndex && (isOnAir || isCued)) return 'on-air';
		if (index === channel.itemIndex + 1) return 'next';
		return 'upcoming';
	}
</script>

<div class="playout-panel">
	<div class="playout-columns">
		<!-- Left: Playlist -->
		<div class="left-col">
			<div class="header-row">
				<select bind:value={selectedChannel} class="channel-select">
					<option value={1}>CH 1</option>
					<option value={2}>CH 2</option>
				</select>
				<span class="playlist-name">{channel.playlistName ?? 'No playlist loaded'}</span>
				<span class="item-counter">
					{channel.itemIndex >= 0 ? channel.itemIndex + 1 : 0}/{channel.itemCount}
				</span>
			</div>

			<div class="playlist-list">
				{#if playlistItems.length === 0 && channel.itemCount === 0}
					<div class="empty-state">No playlist loaded</div>
				{:else}
					{#each playlistItems as item, i}
						<PlayoutItem
							{item}
							index={i}
							isOnAir={getItemState(i) === 'on-air'}
							isNext={getItemState(i) === 'next'}
							isCompleted={getItemState(i) === 'completed'}
						/>
					{/each}
				{/if}
			</div>
		</div>

		<!-- Right: Transport + Info -->
		<div class="right-col">
			<div class="state-row">
				<span class="state-pill" class:on-air={isOnAir} class:cued={isCued} class:stopped={channel.state === 'stopped'} class:completed={channel.state === 'completed'}>
					{channel.state.replace('_', ' ').toUpperCase()}
				</span>
			</div>

			<PlayoutTransport {channel} />

			{#if isOnAir || isCued}
				<div class="now-playing">
					<span class="now-label">NOW</span>
					<span class="now-title">{channel.currentTitle ?? '--'}</span>
				</div>

				{#if channel.itemProgress != null}
					<div class="progress-bar">
						<div class="progress-track">
							<div class="progress-fill" style:width="{(channel.itemProgress ?? 0) * 100}%"></div>
						</div>
					</div>
				{/if}

				<div class="time-row">
					<span class="countdown" class:warning={channel.itemRemainMs != null && channel.itemRemainMs < 10000}>
						{formatCountdown(channel.itemRemainMs)}
					</span>
				</div>

				{#if channel.nextTitle}
					<div class="next-row">
						<span class="next-label">NEXT</span>
						<span class="next-title">{channel.nextTitle}</span>
					</div>
				{/if}
			{:else}
				<div class="idle-message">
					{channel.state === 'completed' ? 'Playlist complete' : 'Playout stopped'}
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.playout-panel {
		display: flex;
		flex-direction: column;
		padding: 6px;
		height: 100%;
		overflow: hidden;
	}

	.playout-columns {
		display: flex;
		gap: 10px;
		flex: 1;
		min-height: 0;
	}

	/* -- Left: playlist column -- */
	.left-col {
		display: flex;
		flex-direction: column;
		gap: 3px;
		width: 40%;
		min-width: 180px;
		flex-shrink: 0;
	}

	.header-row {
		display: flex;
		gap: 4px;
		align-items: center;
		flex-shrink: 0;
	}

	.channel-select {
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		flex-shrink: 0;
	}

	.playlist-name {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.item-counter {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.playlist-list {
		flex: 1;
		overflow-y: auto;
		overflow-x: hidden;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		padding: 2px;
	}

	.playlist-list::-webkit-scrollbar { width: 3px; }
	.playlist-list::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	.empty-state {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 100%;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		opacity: 0.5;
	}

	/* -- Right: controls column -- */
	.right-col {
		display: flex;
		flex-direction: column;
		gap: 6px;
		flex: 1;
		min-width: 0;
		min-height: 0;
		justify-content: center;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.right-col::-webkit-scrollbar { width: 3px; }
	.right-col::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	/* -- State pill -- */
	.state-row {
		display: flex;
		align-items: center;
	}

	.state-pill {
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		background: var(--bg-surface);
		color: var(--text-tertiary);
		border: 1px solid var(--border-default);
	}

	.state-pill.on-air {
		background: rgba(220, 38, 38, 0.15);
		border-color: var(--tally-program);
		color: var(--tally-program);
		animation: pulse-on-air 2s ease-in-out infinite;
	}

	.state-pill.cued {
		background: rgba(59, 130, 246, 0.1);
		border-color: rgba(59, 130, 246, 0.5);
		color: rgba(96, 165, 250, 1);
	}

	.state-pill.stopped {
		background: var(--bg-surface);
		color: var(--text-tertiary);
	}

	.state-pill.completed {
		background: rgba(22, 163, 74, 0.1);
		color: var(--tally-preview);
	}

	@keyframes pulse-on-air {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.7; }
	}

	/* -- Now playing -- */
	.now-playing {
		display: flex;
		align-items: center;
		gap: 5px;
	}

	.now-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--tally-program);
		letter-spacing: 0.06em;
		flex-shrink: 0;
	}

	.now-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-primary);
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* -- Progress -- */
	.progress-bar {
		padding: 2px 0;
	}

	.progress-track {
		position: relative;
		height: 5px;
		background: var(--bg-base);
		border-radius: 3px;
		border: 1px solid var(--border-default);
		overflow: hidden;
	}

	.progress-fill {
		height: 100%;
		background: var(--tally-program);
		border-radius: 3px 0 0 3px;
		transition: width 0.25s linear;
	}

	/* -- Time -- */
	.time-row {
		display: flex;
		align-items: center;
	}

	.countdown {
		font-family: var(--font-mono);
		font-size: 18px;
		font-weight: 700;
		color: var(--text-primary);
		letter-spacing: 0.02em;
	}

	.countdown.warning {
		color: var(--tally-program);
		animation: pulse-warn 1s ease-in-out infinite;
	}

	@keyframes pulse-warn {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}

	/* -- Next -- */
	.next-row {
		display: flex;
		align-items: center;
		gap: 5px;
	}

	.next-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: rgba(96, 165, 250, 0.8);
		letter-spacing: 0.06em;
		flex-shrink: 0;
	}

	.next-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.idle-message {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		opacity: 0.5;
		padding: 8px 0;
	}
</style>
