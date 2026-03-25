<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import type { ControlRoomState, PlayoutChannelState, Playlist, PlaylistItem, SampleClip } from '$lib/api/types';
	import {
		playoutPlaylist, playoutLoadPlaylist, playoutTemplateNames,
		playoutTemplate, playoutSamples,
		playoutStart, playoutStop, playoutNext,
		playoutTakeManual, playoutReturnToAuto, playoutJump,
		apiCall,
	} from '$lib/api/switch-api';
	import PlayoutTimeline from './PlayoutTimeline.svelte';
	import PlayoutItemEditor from './PlayoutItemEditor.svelte';
	import PlayoutSettings from './PlayoutSettings.svelte';
	import PlayoutTransport from './PlayoutTransport.svelte';
	import PlayoutRundown from './PlayoutRundown.svelte';
	import PlayoutOutputBadge from './PlayoutOutputBadge.svelte';
	import CacheStatus from './CacheStatus.svelte';
	import PodBoard from './PodBoard.svelte';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// ── Panel mode: edit vs monitor ──
	let panelMode = $state<'edit' | 'monitor'>('edit');

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
	const isStopped = $derived(channel.state === 'stopped' || channel.state === 'completed');

	// ── Auto-switch mode ──
	$effect(() => {
		if (isOnAir && !dirty && selectedIndex < 0) {
			panelMode = 'monitor';
		} else if (channel.state === 'stopped' || channel.state === 'completed') {
			panelMode = 'edit';
		}
	});

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

	// ── Derived items for sub-components ──
	const currentItem = $derived<PlaylistItem | undefined>(
		channel.itemIndex >= 0 && channel.itemIndex < workingPlaylist.items.length
			? workingPlaylist.items[channel.itemIndex]
			: undefined
	);

	const isIdle = $derived(
		channel.state === 'stopped' || channel.state === 'cued' ||
		(channel.state === 'on_air' && channel.itemIndex < 0)
	);

	// ── On-air protection: lock current and next item indices ──
	const lockedIndices = $derived(
		isOnAir ? [channel.itemIndex, channel.itemIndex + 1].filter(i => i >= 0 && i < workingPlaylist.items.length) : []
	);

	// ── Progress zone for color coding ──
	const progressZone = $derived(
		(channel.itemProgress ?? 0) >= 0.95 ? 'critical' :
		(channel.itemProgress ?? 0) >= 0.80 ? 'warning' : 'normal'
	);

	// ── Save confirmation state ──
	let savePending = $state(false);
	let saveTimer: ReturnType<typeof setTimeout> | null = null;

	// ── Template load confirmation state ──
	let templatePending = $state<string | null>(null);
	let templateTimer: ReturnType<typeof setTimeout> | null = null;

	// ── Fetch playlist items on channel switch only (not on every broadcast tick) ──
	let lastFetchKey = $state('');
	$effect(() => {
		const ch = selectedChannel;
		const count = channel.itemCount;
		const fetchKey = `${ch}:${count}`;
		if (fetchKey === lastFetchKey || dirty) return;
		lastFetchKey = fetchKey;
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
		if (isOnAir && !savePending) {
			savePending = true;
			saveTimer = setTimeout(() => { savePending = false; }, 3000);
			return;
		}
		if (savePending) {
			savePending = false;
			if (saveTimer) { clearTimeout(saveTimer); saveTimer = null; }
		}
		await apiCall(playoutLoadPlaylist(selectedChannel, workingPlaylist), 'Save playlist');
		dirty = false;
	}

	async function loadTemplate(name: string) {
		if (isOnAir && templatePending !== name) {
			if (templateTimer) clearTimeout(templateTimer);
			templatePending = name;
			templateTimer = setTimeout(() => { templatePending = null; }, 3000);
			return;
		}
		if (templatePending) {
			templatePending = null;
			if (templateTimer) { clearTimeout(templateTimer); templateTimer = null; }
		}
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
		if (ms < 10000) {
			const secs = (ms / 1000).toFixed(1);
			return `0:${secs.padStart(4, '0')}`;
		}
		const totalSecs = Math.ceil(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	function formatElapsedFromProgress(progress: number | undefined, remainMs: number | undefined): string {
		if (progress == null || progress <= 0) return '0:00';
		if (remainMs != null && remainMs > 0 && progress > 0 && progress < 1) {
			const totalMs = remainMs / (1 - progress);
			const elapsedMs = totalMs - remainMs;
			const totalSec = Math.max(0, Math.floor(elapsedMs / 1000));
			const m = Math.floor(totalSec / 60);
			const s = totalSec % 60;
			return `${m}:${String(s).padStart(2, '0')}`;
		}
		return '--:--';
	}

	function handleRundownNext() {
		apiCall(playoutNext(selectedChannel), 'Next');
	}

	function handleRundownSkipTo(index: number) {
		const item = workingPlaylist.items[index];
		if (item?.id) {
			apiCall(playoutJump(selectedChannel, item.id), 'Jump');
		}
	}

	// ── Next item info for monitor mode ──
	const nextItem = $derived<PlaylistItem | undefined>(
		channel.itemIndex >= 0 && channel.itemIndex + 1 < workingPlaylist.items.length
			? workingPlaylist.items[channel.itemIndex + 1]
			: undefined
	);

	function nextItemDuration(): string {
		if (!nextItem) return '';
		if (nextItem.type === 'live') return 'LIVE';
		if (nextItem.duration_ms) {
			const s = Math.floor(nextItem.duration_ms / 1000);
			const m = Math.floor(s / 60);
			const sec = s % 60;
			return `${m}:${String(sec).padStart(2, '0')}`;
		}
		return '--:--';
	}

	// ── Keyboard shortcuts ──
	function handleKeydown(e: KeyboardEvent) {
		const tag = (e.target as HTMLElement)?.tagName;
		if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
		if ((e.target as HTMLElement)?.isContentEditable) return;

		switch (e.key) {
			case 'F1':
				e.preventDefault();
				if (isStopped) apiCall(playoutStart(selectedChannel), 'Start');
				break;
			case 'n':
			case 'N':
			case 'F2':
				e.preventDefault();
				apiCall(playoutNext(selectedChannel), 'Next');
				break;
			case 'm':
			case 'M':
				e.preventDefault();
				if (isOnAir) {
					if (channel.mode === 'automated' || !channel.mode) apiCall(playoutTakeManual(selectedChannel), 'Manual');
					else apiCall(playoutReturnToAuto(selectedChannel), 'Auto');
				}
				break;
			case 'Escape':
				e.preventDefault();
				if (isOnAir) apiCall(playoutStop(selectedChannel), 'Stop');
				break;
		}
	}

	onMount(() => {
		document.addEventListener('keydown', handleKeydown);
	});

	onDestroy(() => {
		document.removeEventListener('keydown', handleKeydown);
		if (saveTimer) clearTimeout(saveTimer);
		if (templateTimer) clearTimeout(templateTimer);
	});
</script>

<div class="playout-panel" data-testid="playout-panel">
	<!-- Header (both modes) -->
	<div class="header-row">
		<!-- Channel tabs -->
		<div class="channel-tabs">
			<button
				class="channel-tab"
				class:active={selectedChannel === 1}
				class:on-air={selectedChannel === 1 && channels.find(c => c.id === 1)?.state === 'on_air'}
				onclick={() => selectedChannel = 1}
			>CH1</button>
			<button
				class="channel-tab"
				class:active={selectedChannel === 2}
				class:on-air={selectedChannel === 2 && channels.find(c => c.id === 2)?.state === 'on_air'}
				onclick={() => selectedChannel = 2}
			>CH2</button>
		</div>

		{#if panelMode === 'edit'}
			<span class="playlist-name">{workingPlaylist.name || channel.playlistName || 'No playlist loaded'}</span>
		{/if}

		<PlayoutOutputBadge {channel} {currentItem} {isIdle} />

		{#if panelMode === 'edit'}
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

			{#if templatePending}
				<button class="save-btn on-air-warn" onclick={() => loadTemplate(templatePending!)}>
					LOAD (ON AIR)?
				</button>
			{/if}

			<button
				class="save-btn"
				class:dirty
				class:on-air-warn={savePending}
				onclick={savePlaylist}
				disabled={!dirty && workingPlaylist.items.length === 0 && !savePending}
				title={savePending ? 'Click again to confirm save while on-air' : 'Save playlist to server'}
			>
				{#if savePending}SAVE (ON AIR){:else}Save{/if}
			</button>
		{:else}
			<!-- Monitor mode: inline transport in header -->
			<PlayoutTransport {channel} />
		{/if}

		<!-- Mode toggle -->
		<div class="mode-toggle" data-testid="mode-toggle">
			<button
				class="mode-btn-toggle"
				class:active={panelMode === 'edit'}
				onclick={() => panelMode = 'edit'}
			>EDIT</button>
			<button
				class="mode-btn-toggle"
				class:active={panelMode === 'monitor'}
				onclick={() => panelMode = 'monitor'}
			>MONITOR</button>
		</div>
	</div>

	{#if panelMode === 'edit'}
		<!-- ═══ EDIT MODE ═══ -->

		<!-- Timeline (full width) -->
		<div class="timeline-zone" data-testid="timeline-zone">
			<PlayoutTimeline
				items={workingPlaylist.items}
				currentIndex={channel.itemIndex}
				{selectedIndex}
				progress={channel.itemProgress ?? 0}
				{lockedIndices}
				onSelect={(i) => selectedIndex = i}
				onReorder={reorderItem}
				onDelete={deleteItem}
				onAddItem={addItem}
			/>
		</div>

		<!-- Bottom: Editor + Transport/Settings -->
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
								<div class="progress-fill {progressZone}" style:width="{(channel.itemProgress ?? 0) * 100}%"></div>
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

	{:else}
		<!-- ═══ MONITOR MODE ═══ -->
		<div class="monitor-layout" data-testid="monitor-layout">
			<!-- NOW/NEXT block -->
			<div class="now-next-block">
				{#if isOnAir || isCued}
					<div class="now-block">
						<div class="now-header">
							<span class="now-tag">NOW</span>
							{#if currentItem}
								<span class="now-type-badge" class:clip={currentItem.type === 'clip'} class:live={currentItem.type === 'live'} class:slate={currentItem.type === 'slate'}>
									{currentItem.type.toUpperCase()}
								</span>
							{/if}
							<span class="now-item-title">{channel.currentTitle ?? '--'}</span>
							<span class="now-remain">
								{#if channel.itemRemainMs != null && channel.itemRemainMs > 0}
									REMAIN {formatCountdown(channel.itemRemainMs)}
								{:else}
									REMAIN --:--
								{/if}
							</span>
						</div>

						<div class="monitor-progress-row">
							<div class="monitor-progress-track">
								<div class="monitor-progress-fill {progressZone}" style:width="{(channel.itemProgress ?? 0) * 100}%"></div>
							</div>
						</div>

						<div class="monitor-time-row">
							<span class="elapsed-label">elapsed: {formatElapsedFromProgress(channel.itemProgress, channel.itemRemainMs)}</span>
							<span class="monitor-countdown" class:critical={progressZone === 'critical'} class:warning={progressZone === 'warning'}>
								{formatCountdown(channel.itemRemainMs)}
							</span>
						</div>
					</div>

					{#if channel.nextTitle || nextItem}
						<div class="next-block">
							<span class="next-tag">NEXT</span>
							{#if nextItem}
								<span class="next-type-badge" class:clip={nextItem.type === 'clip'} class:live={nextItem.type === 'live'} class:slate={nextItem.type === 'slate'}>
									{nextItem.type.toUpperCase()}
								</span>
							{/if}
							<span class="next-item-title">{channel.nextTitle ?? nextItem?.title ?? '--'}</span>
							<span class="next-dur">({nextItemDuration()})</span>
						</div>
					{/if}
				{:else}
					<div class="monitor-idle">
						<span class="monitor-idle-text">
							{channel.state === 'completed' ? 'Playlist complete' : 'Playout stopped'}
						</span>
					</div>
				{/if}
			</div>

			<!-- Bottom split: rundown + pod board -->
			<div class="monitor-bottom">
				<div class="rundown-zone" data-testid="rundown-zone">
					<PlayoutRundown
						items={workingPlaylist.items}
						currentIndex={channel.itemIndex}
						progress={channel.itemProgress ?? 0}
						itemRemainMs={channel.itemRemainMs}
						onNext={handleRundownNext}
						onSkipTo={handleRundownSkipTo}
					/>
				</div>
				<div class="pod-zone">
					<PodBoard
						channel={selectedChannel}
						activePod={channel.activePod}
						pods={channel.pods ?? []}
						podsArmed={channel.podsArmed ?? 0}
						podTemplates={workingPlaylist.pods}
					/>
				</div>
			</div>
		</div>
	{/if}
</div>

<style>
	.playout-panel {
		display: flex;
		flex-direction: column;
		padding: 6px;
		height: 100%;
		overflow-y: auto;
		overflow-x: hidden;
		gap: 4px;
	}
	.playout-panel::-webkit-scrollbar { width: 3px; }
	.playout-panel::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	/* ── Header ── */
	.header-row {
		display: flex;
		gap: 4px;
		align-items: center;
		flex-shrink: 0;
		flex-wrap: wrap;
	}

	/* ── Channel tabs ── */
	.channel-tabs {
		display: flex;
		gap: 1px;
		flex-shrink: 0;
	}

	.channel-tab {
		padding: 2px 6px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.04em;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
	}

	.channel-tab:first-child {
		border-radius: var(--radius-sm) 0 0 var(--radius-sm);
	}

	.channel-tab:last-child {
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
	}

	.channel-tab.active {
		background: var(--bg-hover);
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.channel-tab.on-air {
		background: rgba(220, 38, 38, 0.15);
		border-color: var(--tally-program);
		color: var(--tally-program);
		animation: pulse-on-air 2s ease-in-out infinite;
	}

	.playlist-name {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 40px;
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

	.save-btn.on-air-warn {
		color: var(--accent-orange, #f97316);
		border-color: rgba(249, 115, 22, 0.6);
		background: rgba(249, 115, 22, 0.12);
		animation: save-warn-pulse 1s ease-in-out infinite;
	}

	@keyframes save-warn-pulse {
		0%, 100% { background: rgba(249, 115, 22, 0.12); }
		50% { background: rgba(249, 115, 22, 0.25); }
	}

	/* ── Mode toggle ── */
	.mode-toggle {
		display: flex;
		gap: 1px;
		flex-shrink: 0;
		margin-left: auto;
	}

	.mode-btn-toggle {
		padding: 2px 6px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 700;
		letter-spacing: 0.06em;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.mode-btn-toggle:first-child {
		border-radius: var(--radius-sm) 0 0 var(--radius-sm);
	}

	.mode-btn-toggle:last-child {
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
	}

	.mode-btn-toggle.active {
		background: var(--bg-hover);
		color: var(--text-primary);
		border-color: var(--accent-blue, #3b82f6);
	}

	/* ── Timeline zone ── */
	.timeline-zone {
		flex-shrink: 0;
	}

	/* ── Bottom columns (edit mode) ── */
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

	/* ── Now playing (edit mode) ── */
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

	/* ── Progress (edit mode) ── */
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
		border-radius: 3px 0 0 3px;
		transition: width 0.25s linear;
	}

	.progress-fill.normal { background: var(--accent-blue, #3b82f6); }
	.progress-fill.warning { background: var(--accent-orange, #f97316); }
	.progress-fill.critical { background: var(--tally-program); animation: progress-pulse 1s ease-in-out infinite; }

	@keyframes progress-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.6; }
	}

	/* ── Time (edit mode) ── */
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

	/* ── Next (edit mode) ── */
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

	/* ═══════════════════════════════ */
	/* ── MONITOR MODE STYLES ──      */
	/* ═══════════════════════════════ */

	.monitor-layout {
		display: flex;
		flex-direction: column;
		flex: 1;
		min-height: 0;
		gap: 4px;
	}

	/* ── NOW/NEXT block ── */
	.now-next-block {
		flex-shrink: 0;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		overflow: hidden;
	}

	.now-block {
		padding: 6px 8px 4px;
	}

	.now-header {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 4px;
	}

	.now-tag {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 800;
		color: var(--tally-program, #dc2626);
		letter-spacing: 0.08em;
		flex-shrink: 0;
	}

	.now-type-badge, .next-type-badge {
		padding: 0 3px;
		border-radius: 2px;
		font-family: var(--font-ui);
		font-size: 7px;
		font-weight: 700;
		letter-spacing: 0.06em;
		flex-shrink: 0;
		line-height: 1.6;
	}

	.now-type-badge.clip, .next-type-badge.clip {
		background: rgba(59, 130, 246, 0.15);
		color: rgba(96, 165, 250, 1);
	}

	.now-type-badge.live, .next-type-badge.live {
		background: rgba(220, 38, 38, 0.15);
		color: rgba(248, 113, 113, 1);
	}

	.now-type-badge.slate, .next-type-badge.slate {
		background: rgba(107, 114, 128, 0.2);
		color: var(--text-tertiary);
	}

	.now-item-title {
		flex: 1;
		font-family: var(--font-ui);
		font-size: 15px;
		font-weight: 700;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.now-remain {
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 600;
		color: var(--text-secondary);
		flex-shrink: 0;
		letter-spacing: 0.02em;
	}

	/* ── Monitor progress bar ── */
	.monitor-progress-row {
		margin-bottom: 3px;
	}

	.monitor-progress-track {
		height: 6px;
		background: var(--bg-base);
		border-radius: 3px;
		border: 1px solid var(--border-default);
		overflow: hidden;
	}

	.monitor-progress-fill {
		height: 100%;
		border-radius: 3px 0 0 3px;
		transition: width 0.25s linear;
	}

	.monitor-progress-fill.normal { background: var(--accent-blue, #3b82f6); }
	.monitor-progress-fill.warning { background: var(--accent-orange, #f97316); }
	.monitor-progress-fill.critical { background: var(--tally-program); animation: progress-pulse 1s ease-in-out infinite; }

	/* ── Monitor time row ── */
	.monitor-time-row {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
	}

	.elapsed-label {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-tertiary);
		letter-spacing: 0.02em;
	}

	.monitor-countdown {
		font-family: var(--font-mono);
		font-size: 28px;
		font-weight: 700;
		color: var(--text-primary);
		letter-spacing: 0.02em;
	}

	.monitor-countdown.warning {
		color: var(--accent-orange, #f97316);
	}

	.monitor-countdown.critical {
		color: var(--tally-program);
		animation: pulse-warn 1s ease-in-out infinite;
	}

	/* ── NEXT block ── */
	.next-block {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 8px;
		border-top: 1px solid var(--border-default);
		background: rgba(59, 130, 246, 0.04);
	}

	.next-tag {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 800;
		color: rgba(96, 165, 250, 0.9);
		letter-spacing: 0.08em;
		flex-shrink: 0;
	}

	.next-item-title {
		flex: 1;
		font-family: var(--font-ui);
		font-size: 13px;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.next-dur {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	/* ── Monitor idle ── */
	.monitor-idle {
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 16px;
	}

	.monitor-idle-text {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		opacity: 0.5;
	}

	/* ── Monitor bottom split ── */
	.monitor-bottom {
		display: flex;
		gap: 6px;
		flex: 1;
		min-height: 0;
	}

	.rundown-zone {
		flex: 3;
		min-width: 0;
		min-height: 0;
		overflow: hidden;
	}

	.pod-zone {
		flex: 2;
		min-width: 0;
		min-height: 0;
		overflow-y: auto;
	}

	.pod-zone::-webkit-scrollbar { width: 3px; }
	.pod-zone::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }
</style>
