<script lang="ts">
	import type { ControlRoomState, PlayoutChannelState, Playlist, PlaylistItem, SampleClip } from '$lib/api/types';
	import {
		playoutPlaylist, playoutLoadPlaylist, playoutTemplateNames,
		playoutTemplate, playoutSamples, apiCall,
	} from '$lib/api/switch-api';
	import PlayoutTimeline from './PlayoutTimeline.svelte';
	import PlayoutItemEditor from './PlayoutItemEditor.svelte';
	import PlayoutSettings from './PlayoutSettings.svelte';
	import PlayoutTransport from './PlayoutTransport.svelte';
	import CacheStatus from './CacheStatus.svelte';
	import PodBoard from './PodBoard.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── Channel state ──
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

	// ── Working playlist (local editable copy) ──
	let workingPlaylist = $state<Playlist>({
		id: '', name: '', default_transition: { type: 'cut' }, items: [],
	});
	let selectedIndex = $state(-1);
	let sampleClips = $state<SampleClip[]>([]);
	let templateNames = $state<string[]>([]);
	let dirty = $state(false);

	// ── Source names for the item editor ──
	const sourceNames = $derived(Object.keys(crState.sources ?? {}));

	// ── Fetch playlist items on mount / channel change ──
	$effect(() => {
		const ch = selectedChannel;
		const count = channel.itemCount;
		let cancelled = false;
		if (count <= 0) {
			workingPlaylist = { id: '', name: channel.playlistName ?? '', default_transition: { type: 'cut' }, items: [] };
			return;
		}
		playoutPlaylist(ch).then(items => {
			if (!cancelled) {
				workingPlaylist = {
					...workingPlaylist,
					name: channel.playlistName ?? workingPlaylist.name,
					items,
				};
				dirty = false;
			}
		}).catch(() => {
			if (!cancelled) workingPlaylist = { ...workingPlaylist, items: [] };
		});
		return () => { cancelled = true; };
	});

	// ── Fetch template names + sample clips on mount ──
	$effect(() => {
		playoutTemplateNames().then(names => { templateNames = names; }).catch(() => { templateNames = []; });
		playoutSamples().then(clips => { sampleClips = clips; }).catch(() => { sampleClips = []; });
	});

	// ── Helper functions ──

	function reorderItem(from: number, to: number) {
		const items = [...workingPlaylist.items];
		const [moved] = items.splice(from, 1);
		items.splice(to, 0, moved);
		workingPlaylist = { ...workingPlaylist, items };
		if (selectedIndex === from) selectedIndex = to;
		dirty = true;
	}

	function deleteItem(index: number) {
		workingPlaylist = {
			...workingPlaylist,
			items: workingPlaylist.items.filter((_, i) => i !== index),
		};
		if (selectedIndex === index) selectedIndex = -1;
		else if (selectedIndex > index) selectedIndex--;
		dirty = true;
	}

	function addItem(atIndex: number) {
		const newItem: PlaylistItem = {
			id: `item-${Date.now()}`,
			type: 'clip',
			title: 'New Item',
		};
		const items = [...workingPlaylist.items];
		items.splice(atIndex, 0, newItem);
		workingPlaylist = { ...workingPlaylist, items };
		selectedIndex = atIndex;
		dirty = true;
	}

	function updateItem(updated: PlaylistItem) {
		workingPlaylist = {
			...workingPlaylist,
			items: workingPlaylist.items.map((item, i) =>
				i === selectedIndex ? updated : item
			),
		};
		dirty = true;
	}

	function updatePlaylistSettings(updated: Playlist) {
		workingPlaylist = updated;
		dirty = true;
	}

	async function savePlaylist() {
		await apiCall(playoutLoadPlaylist(selectedChannel, workingPlaylist), 'Save playlist');
		dirty = false;
	}

	async function loadTemplate(name: string) {
		try {
			const pl = await playoutTemplate(name);
			if (pl) {
				workingPlaylist = pl;
				selectedIndex = -1;
				dirty = true;
			}
		} catch (e) {
			console.error('Failed to load template:', e);
		}
	}

	function formatCountdown(ms: number | undefined): string {
		if (!ms || ms <= 0) return '--:--';
		const totalSecs = Math.ceil(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}
</script>

<div class="playout-panel">
	<!-- ═══ Header ═══ -->
	<div class="header-row">
		<select bind:value={selectedChannel} class="channel-select">
			<option value={1}>CH 1</option>
			<option value={2}>CH 2</option>
		</select>
		<span class="playlist-name">{workingPlaylist.name || channel.playlistName || 'No playlist loaded'}</span>
		<span class="item-counter">
			{channel.itemIndex >= 0 ? channel.itemIndex + 1 : 0}/{channel.itemCount}
		</span>

		{#if templateNames.length > 0}
			<select
				class="template-select"
				value=""
				onchange={(e) => {
					const name = (e.target as HTMLSelectElement).value;
					if (name) { loadTemplate(name); (e.target as HTMLSelectElement).value = ''; }
				}}
			>
				<option value="" disabled>Templates</option>
				{#each templateNames as name}
					<option value={name}>{name}</option>
				{/each}
			</select>
		{/if}

		<button
			class="save-btn"
			class:dirty
			onclick={savePlaylist}
			disabled={!dirty && workingPlaylist.items.length === 0}
			title="Save playlist to server"
		>
			Save
		</button>
	</div>

	<!-- ═══ Timeline (full width) ═══ -->
	<div class="timeline-zone">
		<PlayoutTimeline
			items={workingPlaylist.items}
			currentIndex={channel.itemIndex}
			{selectedIndex}
			progress={channel.itemProgress ?? 0}
			onSelect={(i) => selectedIndex = i}
			onReorder={reorderItem}
			onDelete={deleteItem}
			onAddItem={addItem}
		/>
	</div>

	<!-- ═══ Bottom: Editor + Transport ═══ -->
	<div class="bottom-columns">
		<!-- Left: Item Editor -->
		<div class="left-col">
			{#if selectedIndex >= 0 && selectedIndex < workingPlaylist.items.length}
				<div class="editor-wrap">
					<div class="editor-header">
						<span class="editor-title">Item {selectedIndex + 1}</span>
						<button class="editor-close" onclick={() => selectedIndex = -1} title="Close editor">&times;</button>
					</div>
					<PlayoutItemEditor
						item={workingPlaylist.items[selectedIndex]}
						onChange={updateItem}
						sources={sourceNames}
					/>
				</div>
			{:else}
				<div class="no-selection">Select an item in the timeline to edit</div>
			{/if}
		</div>

		<!-- Right: Transport + Status -->
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

			<PodBoard
				channel={selectedChannel}
				activePod={channel.activePod}
				pods={channel.pods ?? []}
				podsArmed={channel.podsArmed ?? 0}
				podTemplates={workingPlaylist.pods}
			/>

			<PlayoutSettings
				playlist={workingPlaylist}
				onChange={updatePlaylistSettings}
				{sampleClips}
			/>

			<CacheStatus />
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
		gap: 4px;
	}

	/* ── Header ── */
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

	.template-select {
		background: var(--bg-base);
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		flex-shrink: 0;
		max-width: 100px;
	}

	.save-btn {
		padding: 2px 8px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--text-secondary);
		letter-spacing: 0.04em;
		cursor: pointer;
		flex-shrink: 0;
		transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast);
	}

	.save-btn:hover:not(:disabled) {
		background: var(--bg-hover);
		border-color: var(--border-strong);
		color: var(--text-primary);
	}

	.save-btn:disabled {
		opacity: 0.3;
		cursor: default;
	}

	.save-btn.dirty {
		color: var(--accent-blue, #3b82f6);
		border-color: rgba(59, 130, 246, 0.5);
		background: rgba(59, 130, 246, 0.08);
	}

	.save-btn.dirty:hover {
		background: rgba(59, 130, 246, 0.15);
	}

	/* ── Timeline zone ── */
	.timeline-zone {
		flex-shrink: 0;
	}

	/* ── Bottom columns ── */
	.bottom-columns {
		display: flex;
		gap: 10px;
		flex: 1;
		min-height: 0;
	}

	/* ── Left: Item Editor ── */
	.left-col {
		display: flex;
		flex-direction: column;
		width: 40%;
		min-width: 180px;
		flex-shrink: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.left-col::-webkit-scrollbar { width: 3px; }
	.left-col::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	.editor-wrap {
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		overflow: hidden;
	}

	.editor-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 4px 8px;
		background: var(--bg-control);
		border-bottom: 1px solid var(--border-default);
	}

	.editor-title {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.editor-close {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 18px;
		height: 18px;
		border: none;
		background: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 14px;
		line-height: 1;
		border-radius: var(--radius-sm);
		padding: 0;
		transition: color var(--transition-fast), background var(--transition-fast);
	}

	.editor-close:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
	}

	.no-selection {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 100%;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		opacity: 0.5;
	}

	/* ── Right: controls column ── */
	.right-col {
		display: flex;
		flex-direction: column;
		gap: 6px;
		flex: 1;
		min-width: 0;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.right-col::-webkit-scrollbar { width: 3px; }
	.right-col::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	/* ── State pill ── */
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

	/* ── Now playing ── */
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

	/* ── Progress ── */
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

	/* ── Time ── */
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

	/* ── Next ── */
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
