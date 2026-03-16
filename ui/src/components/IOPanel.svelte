<script lang="ts">
	import type {
		ControlRoomState,
		SourceInfo,
		SRTSourceStats,
		DestinationInfo,
		CreateSRTSourceConfig,
		DestinationConfig,
	} from '$lib/api/types';
	import {
		createSRTSource,
		deleteSRTSource,
		getSRTSourceStats,
		updateSRTLatency,
		setSourceDelay,
		addDestination,
		removeDestination,
		startDestination,
		stopDestination,
		stopSRTOutput,
		apiCall,
	} from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
		visible: boolean;
		onclose?: () => void;
	}

	let { state: crState, visible, onclose }: Props = $props();

	// --- Section collapse state ---
	let inputsExpanded = $state(true);
	let outputsExpanded = $state(true);

	// --- Expanded rows ---
	let expandedSources = $state<Set<string>>(new Set());
	let expandedDests = $state<Set<string>>(new Set());

	// --- SRT stats polling ---
	let srtStats = $state<Record<string, SRTSourceStats>>({});
	let srtPollIntervals = $state<Record<string, ReturnType<typeof setInterval>>>({});

	// --- Add forms ---
	let showAddSource = $state(false);
	let showAddDest = $state(false);

	// --- Add SRT Source form state ---
	let newSourceAddress = $state('');
	let newSourceStreamID = $state('');
	let newSourceLabel = $state('');
	let newSourceLatency = $state(120);

	// --- Add Destination form state ---
	let newDestName = $state('');
	let newDestType = $state<'srt-caller' | 'srt-listener'>('srt-caller');
	let newDestAddress = $state('');
	let newDestPort = $state(6464);
	let newDestStreamID = $state('');
	let newDestLatency = $state(120);
	let newDestSCTE35 = $state(true);

	// --- Inline confirmation ---
	let confirmingDeleteSource = $state<string | null>(null);
	let confirmingDeleteDest = $state<string | null>(null);

	// --- Editable fields ---
	let editLatency = $state<Record<string, number>>({});
	let editDelay = $state<Record<string, number>>({});

	// --- Helpers ---
	function fmtBytes(bytes: number): string {
		if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`;
		if (bytes >= 1e6) return `${(bytes / 1e6).toFixed(1)} MB`;
		if (bytes >= 1e3) return `${(bytes / 1e3).toFixed(0)} KB`;
		return `${bytes} B`;
	}

	function fmtDuration(secs: number): string {
		const h = Math.floor(secs / 3600);
		const m = Math.floor((secs % 3600) / 60);
		const s = Math.floor(secs % 60);
		return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`;
	}

	function fmtBitrate(kbps: number): string {
		if (kbps >= 1000) return `${(kbps / 1000).toFixed(1)} Mbps`;
		return `${kbps.toFixed(0)} Kbps`;
	}

	// --- Sort sources ---
	function sortedSources(sources: Record<string, SourceInfo>): SourceInfo[] {
		return Object.values(sources).sort((a, b) => {
			const posA = a.position ?? Number.MAX_SAFE_INTEGER;
			const posB = b.position ?? Number.MAX_SAFE_INTEGER;
			if (posA !== posB) return posA - posB;
			return a.key.localeCompare(b.key);
		});
	}

	// --- Toggle source row ---
	function toggleSource(key: string) {
		const next = new Set(expandedSources);
		if (next.has(key)) {
			next.delete(key);
			stopPolling(key);
		} else {
			next.add(key);
			const src = crState.sources[key];
			if (src?.type === 'srt') {
				startPolling(key);
			}
		}
		expandedSources = next;
	}

	// --- Toggle dest row ---
	function toggleDest(id: string) {
		const next = new Set(expandedDests);
		if (next.has(id)) {
			next.delete(id);
		} else {
			next.add(id);
		}
		expandedDests = next;
	}

	// --- SRT stats polling ---
	function startPolling(key: string) {
		// Fetch immediately
		fetchSRTStats(key);
		// Poll every 2 seconds
		const id = setInterval(() => fetchSRTStats(key), 2000);
		srtPollIntervals = { ...srtPollIntervals, [key]: id };
	}

	function stopPolling(key: string) {
		const id = srtPollIntervals[key];
		if (id) {
			clearInterval(id);
			const next = { ...srtPollIntervals };
			delete next[key];
			srtPollIntervals = next;
		}
	}

	async function fetchSRTStats(key: string) {
		try {
			const stats = await getSRTSourceStats(key);
			srtStats = { ...srtStats, [key]: stats };
		} catch {
			// Ignore errors silently
		}
	}

	// --- Cleanup polling on component destroy ---
	$effect(() => {
		return () => {
			for (const id of Object.values(srtPollIntervals)) {
				clearInterval(id);
			}
		};
	});

	// --- Escape key handler ---
	$effect(() => {
		if (!visible) return;
		function handleKeyDown(e: KeyboardEvent) {
			if (e.key === 'Escape') {
				onclose?.();
			}
		}
		document.addEventListener('keydown', handleKeyDown);
		return () => document.removeEventListener('keydown', handleKeyDown);
	});

	// --- Source detail text ---
	function sourceDetail(src: SourceInfo): string {
		if (src.type === 'srt' && src.srt) {
			if (src.srt.connected) {
				return fmtBitrate(src.srt.bitrateKbps);
			}
			return 'disconnected';
		}
		return src.status === 'healthy' ? 'active' : src.status;
	}

	// --- Destination type badge ---
	function destTypeBadge(dest: DestinationInfo): string {
		if (dest.type === 'srt-caller') return 'SRT\u2192';
		if (dest.type === 'srt-listener') return 'SRT\u2190';
		return dest.type;
	}

	// --- Destination state class ---
	function destStateClass(destState: string): string {
		switch (destState) {
			case 'connected':
			case 'active':
			case 'listening':
				return 'healthy';
			case 'reconnecting':
			case 'starting':
				return 'stale';
			case 'error':
				return 'offline';
			default:
				return 'inactive';
		}
	}

	// --- Form actions ---
	async function handleCreateSource() {
		const config: CreateSRTSourceConfig = {
			type: 'srt',
			mode: 'caller',
			address: newSourceAddress,
			streamID: newSourceStreamID,
			label: newSourceLabel || undefined,
			latencyMs: newSourceLatency,
		};
		apiCall(createSRTSource(config), 'Create SRT source');
		// Reset form
		newSourceAddress = '';
		newSourceStreamID = '';
		newSourceLabel = '';
		newSourceLatency = 120;
		showAddSource = false;
	}

	async function handleCreateDest() {
		const config: DestinationConfig = {
			type: newDestType,
			address: newDestType === 'srt-caller' ? newDestAddress : undefined,
			port: newDestPort,
			streamID: newDestStreamID || undefined,
			latency: newDestLatency,
			name: newDestName || undefined,
		};
		apiCall(addDestination(config), 'Add destination');
		// Reset form
		newDestName = '';
		newDestType = 'srt-caller';
		newDestAddress = '';
		newDestPort = 6464;
		newDestStreamID = '';
		newDestLatency = 120;
		newDestSCTE35 = true;
		showAddDest = false;
	}

	function handleDeleteSource(key: string) {
		if (confirmingDeleteSource === key) {
			apiCall(deleteSRTSource(key), 'Delete SRT source');
			confirmingDeleteSource = null;
		} else {
			confirmingDeleteSource = key;
		}
	}

	function handleDeleteDest(id: string) {
		if (confirmingDeleteDest === id) {
			apiCall(removeDestination(id), 'Remove destination');
			confirmingDeleteDest = null;
		} else {
			confirmingDeleteDest = id;
		}
	}

	function handleApplyLatency(key: string) {
		const val = editLatency[key];
		if (val != null) {
			apiCall(updateSRTLatency(key, val), 'Update SRT latency');
		}
	}

	function handleApplyDelay(key: string) {
		const val = editDelay[key];
		if (val != null) {
			apiCall(setSourceDelay(key, val), 'Update source delay');
		}
	}

	function handleStartDest(id: string) {
		apiCall(startDestination(id), 'Start destination');
	}

	function handleStopDest(id: string) {
		apiCall(stopDestination(id), 'Stop destination');
	}

	function handleStopLegacySRT() {
		apiCall(stopSRTOutput(), 'Stop legacy SRT output');
	}

	// Derived values
	let sources = $derived(sortedSources(crState.sources));
	let destinations = $derived(crState.destinations ?? []);
	let recording = $derived(crState.recording);
	let legacySRT = $derived(crState.srtOutput);
</script>

<div class="io-panel" class:visible>
	<!-- Header -->
	<div class="panel-header">
		<div class="title-group">
			<span class="panel-title">I/O Management</span>
		</div>
		<button class="close-btn" onclick={() => onclose?.()} aria-label="Close I/O panel">&times;</button>
	</div>

	<div class="panel-body">
		<!-- INPUTS Section -->
		<div class="section">
			<button
				class="section-header"
				onclick={() => (inputsExpanded = !inputsExpanded)}
			>
				<span class="section-chevron">{inputsExpanded ? '\u25BE' : '\u25B8'}</span>
				<span class="section-label">INPUTS ({sources.length})</span>
			</button>

			{#if inputsExpanded}
				<div class="section-content">
					{#each sources as src (src.key)}
						<div class="source-row">
							<button
								class="row-header"
								onclick={() => toggleSource(src.key)}
							>
								<span class="type-badge type-{src.type}">{
									src.type === 'demo' ? 'Demo' :
									src.type === 'srt' ? 'SRT' :
									src.type === 'mxl' ? 'MXL' :
									src.type === 'clip' ? 'Clip' :
									src.type === 'replay' ? 'Replay' :
									src.type
								}</span>
								<span class="row-label">{src.label || src.key}</span>
								<span class="status-dot {src.status}"></span>
								<span class="row-detail">{sourceDetail(src)}</span>
								<span class="row-chevron">{expandedSources.has(src.key) ? '\u25BE' : '\u25B8'}</span>
							</button>

							{#if expandedSources.has(src.key)}
								<div class="row-detail-panel">
									<div class="detail-row">
										<span class="detail-label">Type</span>
										<span class="detail-value">{src.type}</span>
									</div>

									{#if src.type === 'srt' && src.srt}
										<div class="detail-row">
											<span class="detail-label">Mode</span>
											<span class="detail-value">{src.srt.mode}</span>
										</div>
										{#if src.srt.remoteAddr}
											<div class="detail-row">
												<span class="detail-label">Remote</span>
												<span class="detail-value mono">{src.srt.remoteAddr}</span>
											</div>
										{/if}
										<div class="detail-row">
											<span class="detail-label">Stream ID</span>
											<span class="detail-value mono">{src.srt.streamID}</span>
										</div>

										{#if srtStats[src.key]}
											{@const stats = srtStats[src.key]}
											<div class="detail-row">
												<span class="detail-label">RTT</span>
												<span class="detail-value mono">{stats.rttMs.toFixed(1)} ms</span>
											</div>
											<div class="detail-row">
												<span class="detail-label">Loss</span>
												<span class="detail-value mono">{stats.lossRatePct.toFixed(2)}%</span>
											</div>
											<div class="detail-row">
												<span class="detail-label">Recv Rate</span>
												<span class="detail-value mono">{stats.recvRateMbps.toFixed(1)} Mbps</span>
											</div>
											<div class="detail-row">
												<span class="detail-label">Recv Buf</span>
												<span class="detail-value mono">{stats.recvBufMs} ms ({stats.recvBufPackets} pkts)</span>
											</div>
											<div class="detail-row">
												<span class="detail-label">Flight Size</span>
												<span class="detail-value mono">{stats.flightSize}</span>
											</div>
										{/if}

										<!-- Editable latency -->
										<div class="detail-row editable">
											<span class="detail-label">Latency</span>
											<div class="edit-field">
												<input
													type="number"
													class="edit-input"
													value={editLatency[src.key] ?? src.srt.latencyMs}
													oninput={(e) => {
														editLatency = { ...editLatency, [src.key]: parseInt((e.target as HTMLInputElement).value) || 0 };
													}}
												/>
												<span class="edit-unit">ms</span>
												<button class="apply-btn" onclick={() => handleApplyLatency(src.key)}>Apply</button>
											</div>
										</div>
									{/if}

									<!-- Editable delay (all source types) -->
									<div class="detail-row editable">
										<span class="detail-label">Delay</span>
										<div class="edit-field">
											<input
												type="number"
												class="edit-input"
												value={editDelay[src.key] ?? (src.delayMs ?? 0)}
												oninput={(e) => {
													editDelay = { ...editDelay, [src.key]: parseInt((e.target as HTMLInputElement).value) || 0 };
												}}
											/>
											<span class="edit-unit">ms</span>
											<button class="apply-btn" onclick={() => handleApplyDelay(src.key)}>Apply</button>
										</div>
									</div>

									<!-- Delete button (SRT caller only) -->
									{#if src.type === 'srt' && src.srt?.mode === 'caller'}
										<div class="detail-actions">
											{#if confirmingDeleteSource === src.key}
												<span class="confirm-text">Are you sure?</span>
												<button class="confirm-btn danger" onclick={() => handleDeleteSource(src.key)}>Confirm</button>
												<button class="confirm-btn" onclick={() => (confirmingDeleteSource = null)}>Cancel</button>
											{:else}
												<button class="delete-btn" onclick={() => handleDeleteSource(src.key)}>Delete</button>
											{/if}
										</div>
									{/if}
								</div>
							{/if}
						</div>
					{/each}

					<!-- Add SRT Source -->
					{#if !showAddSource}
						<button class="add-btn" onclick={() => (showAddSource = true)}>+ Add SRT Source</button>
					{:else}
						<div class="add-form">
							<div class="form-title">New SRT Source (Caller)</div>
							<div class="form-row">
								<span class="form-label">Address</span>
								<input
									type="text"
									class="form-input"
									placeholder="srt://host:port"
									bind:value={newSourceAddress}
								/>
							</div>
							<div class="form-row">
								<span class="form-label">Stream ID</span>
								<input
									type="text"
									class="form-input"
									placeholder="live/camera1"
									bind:value={newSourceStreamID}
								/>
							</div>
							<div class="form-row">
								<span class="form-label">Label</span>
								<input
									type="text"
									class="form-input"
									placeholder="(optional)"
									bind:value={newSourceLabel}
								/>
							</div>
							<div class="form-row">
								<span class="form-label">Latency</span>
								<div class="form-input-group">
									<input
										type="number"
										class="form-input short"
										bind:value={newSourceLatency}
									/>
									<span class="form-unit">ms</span>
								</div>
							</div>
							<div class="form-actions">
								<button class="form-btn primary" onclick={handleCreateSource}>Create</button>
								<button class="form-btn" onclick={() => (showAddSource = false)}>Cancel</button>
							</div>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- OUTPUTS Section -->
		<div class="section">
			<button
				class="section-header"
				onclick={() => (outputsExpanded = !outputsExpanded)}
			>
				<span class="section-chevron">{outputsExpanded ? '\u25BE' : '\u25B8'}</span>
				<span class="section-label">OUTPUTS ({destinations.length + (legacySRT?.active ? 1 : 0)})</span>
			</button>

			{#if outputsExpanded}
				<div class="section-content">
					<!-- Legacy SRT Output -->
					{#if legacySRT?.active}
						<div class="dest-row legacy-srt">
							<div class="row-header static">
								<span class="type-badge type-srt">Legacy SRT</span>
								<span class="row-label">{legacySRT.address ?? 'SRT'}:{legacySRT.port ?? ''}</span>
								<span class="status-dot {destStateClass(legacySRT.state ?? 'stopped')}"></span>
								<span class="row-detail">{legacySRT.bytesWritten != null ? fmtBytes(legacySRT.bytesWritten) : ''}</span>
								<button class="action-btn stop" onclick={handleStopLegacySRT} title="Stop">&#x23F9;</button>
							</div>
						</div>
					{/if}

					<!-- Destinations -->
					{#each destinations as dest (dest.id)}
						<div class="dest-row">
							<!-- svelte-ignore a11y_click_events_have_key_events -->
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<div
								class="row-header clickable"
								onclick={() => toggleDest(dest.id)}
							>
								<span class="type-badge type-srt">{destTypeBadge(dest)}</span>
								<span class="row-label">{dest.name || `${dest.address ?? ''}:${dest.port}`}</span>
								<span class="status-dot {destStateClass(dest.state)}"></span>
								<span class="row-detail">{dest.bytesWritten != null ? fmtBytes(dest.bytesWritten) : ''}</span>
								<button
									class="action-btn"
									class:stop={dest.state === 'connected' || dest.state === 'active' || dest.state === 'listening' || dest.state === 'reconnecting'}
									onclick={(e: MouseEvent) => {
										e.stopPropagation();
										if (dest.state === 'connected' || dest.state === 'active' || dest.state === 'listening' || dest.state === 'reconnecting') {
											handleStopDest(dest.id);
										} else {
											handleStartDest(dest.id);
										}
									}}
									title={dest.state === 'connected' || dest.state === 'active' || dest.state === 'listening' ? 'Stop' : 'Start'}
								>
									{#if dest.state === 'connected' || dest.state === 'active' || dest.state === 'listening' || dest.state === 'reconnecting'}
										&#x23F9;
									{:else}
										&#x25B6;
									{/if}
								</button>
								<span class="row-chevron">{expandedDests.has(dest.id) ? '\u25BE' : '\u25B8'}</span>
							</div>

							{#if expandedDests.has(dest.id)}
								<div class="row-detail-panel">
									<div class="detail-row">
										<span class="detail-label">Type</span>
										<span class="detail-value">{dest.type}</span>
									</div>
									{#if dest.address}
										<div class="detail-row">
											<span class="detail-label">Address</span>
											<span class="detail-value mono">{dest.address}:{dest.port}</span>
										</div>
									{:else}
										<div class="detail-row">
											<span class="detail-label">Port</span>
											<span class="detail-value mono">{dest.port}</span>
										</div>
									{/if}
									{#if dest.connections != null}
										<div class="detail-row">
											<span class="detail-label">Connections</span>
											<span class="detail-value mono">{dest.connections}</span>
										</div>
									{/if}
									{#if dest.droppedPackets != null}
										<div class="detail-row">
											<span class="detail-label">Dropped</span>
											<span class="detail-value mono">{dest.droppedPackets} packets</span>
										</div>
									{/if}
									{#if dest.error}
										<div class="detail-row">
											<span class="detail-label">Error</span>
											<span class="detail-value error">{dest.error}</span>
										</div>
									{/if}
									<div class="detail-actions">
										{#if confirmingDeleteDest === dest.id}
											<span class="confirm-text">Are you sure?</span>
											<button class="confirm-btn danger" onclick={() => handleDeleteDest(dest.id)}>Confirm</button>
											<button class="confirm-btn" onclick={() => (confirmingDeleteDest = null)}>Cancel</button>
										{:else}
											<button class="delete-btn" onclick={() => handleDeleteDest(dest.id)}>Delete</button>
										{/if}
									</div>
								</div>
							{/if}
						</div>
					{/each}

					<!-- Add Destination -->
					{#if !showAddDest}
						<button class="add-btn" onclick={() => (showAddDest = true)}>+ Add Destination</button>
					{:else}
						<div class="add-form">
							<div class="form-title">New Destination</div>
							<div class="form-row">
								<span class="form-label">Name</span>
								<input
									type="text"
									class="form-input"
									placeholder="(optional)"
									bind:value={newDestName}
								/>
							</div>
							<div class="form-row">
								<span class="form-label">Type</span>
								<select class="form-input" bind:value={newDestType}>
									<option value="srt-caller">SRT Caller</option>
									<option value="srt-listener">SRT Listener</option>
								</select>
							</div>
							{#if newDestType === 'srt-caller'}
								<div class="form-row">
									<span class="form-label">Address</span>
									<input
										type="text"
										class="form-input"
										placeholder="srt://host"
										bind:value={newDestAddress}
									/>
								</div>
							{/if}
							<div class="form-row">
								<span class="form-label">Port</span>
								<input
									type="number"
									class="form-input short"
									bind:value={newDestPort}
								/>
							</div>
							<div class="form-row">
								<span class="form-label">Stream ID</span>
								<input
									type="text"
									class="form-input"
									placeholder="(optional)"
									bind:value={newDestStreamID}
								/>
							</div>
							<div class="form-row">
								<span class="form-label">Latency</span>
								<div class="form-input-group">
									<input
										type="number"
										class="form-input short"
										bind:value={newDestLatency}
									/>
									<span class="form-unit">ms</span>
								</div>
							</div>
							<div class="form-row">
								<label class="form-label-inline">
									<input type="checkbox" bind:checked={newDestSCTE35} />
									SCTE-35 enabled
								</label>
							</div>
							<div class="form-actions">
								<button class="form-btn primary" onclick={handleCreateDest}>Create</button>
								<button class="form-btn" onclick={() => (showAddDest = false)}>Cancel</button>
							</div>
						</div>
					{/if}

					<!-- Recording Status -->
					<div class="recording-status">
						{#if recording?.active}
							<span class="rec-dot active"></span>
							<span class="rec-label">REC</span>
							<span class="rec-filename">{recording.filename ?? ''}</span>
							{#if recording.durationSecs != null}
								<span class="rec-duration">{fmtDuration(recording.durationSecs)}</span>
							{/if}
							{#if recording.bytesWritten != null}
								<span class="rec-bytes">{fmtBytes(recording.bytesWritten)}</span>
							{/if}
						{:else}
							<span class="rec-dot inactive"></span>
							<span class="rec-inactive">Recording inactive</span>
						{/if}
					</div>
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.io-panel {
		position: fixed;
		top: 0;
		right: 0;
		bottom: 0;
		width: 560px;
		background: rgba(9, 9, 11, 0.96);
		border-left: 1px solid var(--border-subtle);
		z-index: var(--z-fullscreen);
		transform: translateX(100%);
		transition: transform 200ms ease;
		display: flex;
		flex-direction: column;
		font-family: var(--font-ui);
		overflow: hidden;
	}

	.io-panel.visible {
		transform: translateX(0);
	}

	/* --- Header --- */
	.panel-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 10px 16px;
		border-bottom: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.title-group {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.panel-title {
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--text-primary);
		letter-spacing: 0.5px;
	}

	.close-btn {
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: var(--text-base);
		padding: 4px 8px;
		line-height: 1;
		border-radius: 3px;
		transition: color 0.15s, background 0.15s;
	}

	.close-btn:hover {
		color: var(--text-primary);
		background: rgba(255, 255, 255, 0.06);
	}

	.close-btn:focus-visible {
		outline: 1.5px solid var(--accent-blue);
		outline-offset: 2px;
	}

	/* --- Body --- */
	.panel-body {
		flex: 1;
		overflow-y: auto;
		padding: 8px 0;
	}

	/* --- Section --- */
	.section {
		margin-bottom: 4px;
	}

	.section-header {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 6px 16px;
		background: none;
		border: none;
		cursor: pointer;
		font-size: var(--section-header-size);
		font-weight: var(--section-header-weight);
		letter-spacing: var(--section-header-tracking);
		color: var(--section-header-color);
		text-align: left;
		transition: background var(--transition-fast);
	}

	.section-header:hover {
		background: rgba(255, 255, 255, 0.03);
	}

	.section-chevron {
		font-size: var(--text-2xs);
		width: 10px;
		text-align: center;
	}

	.section-label {
		text-transform: uppercase;
	}

	.section-content {
		padding: 2px 0;
	}

	/* --- Source/Dest Row --- */
	.source-row,
	.dest-row {
		border-bottom: 1px solid var(--border-subtle);
	}

	.source-row:last-child,
	.dest-row:last-child {
		border-bottom: none;
	}

	.row-header {
		display: flex;
		align-items: center;
		gap: 8px;
		width: 100%;
		padding: 7px 16px;
		background: none;
		border: none;
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-primary);
		text-align: left;
		transition: background var(--transition-fast);
	}

	.row-header:hover {
		background: var(--bg-hover);
	}

	.row-header.static {
		cursor: default;
	}

	.row-header.static:hover {
		background: none;
	}

	.row-header.clickable {
		cursor: pointer;
	}

	/* --- Type Badge --- */
	.type-badge {
		display: inline-block;
		padding: 1px 6px;
		border-radius: var(--radius-xs);
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.03em;
		flex-shrink: 0;
		text-align: center;
		min-width: 36px;
	}

	.type-demo {
		background: var(--bg-hover);
		color: var(--text-secondary);
	}

	.type-srt {
		background: var(--accent-blue-dim);
		color: var(--accent-blue);
	}

	.type-mxl {
		background: var(--accent-purple-dim);
		color: var(--accent-purple);
	}

	.type-clip {
		background: var(--accent-gold-dim);
		color: var(--accent-gold);
	}

	.type-replay {
		background: var(--accent-orange-dim);
		color: var(--accent-orange);
	}

	/* --- Row elements --- */
	.row-label {
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	.status-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.status-dot.healthy {
		background: var(--color-success);
	}

	.status-dot.stale,
	.status-dot.no_signal {
		background: var(--color-warning);
	}

	.status-dot.offline {
		background: var(--color-error);
	}

	.status-dot.inactive {
		background: var(--text-tertiary);
	}

	.row-detail {
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		flex-shrink: 0;
		font-family: var(--font-mono);
	}

	.row-chevron {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
		width: 10px;
		text-align: center;
	}

	/* --- Action Button --- */
	.action-btn {
		background: var(--bg-control);
		border: var(--btn-border);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		cursor: pointer;
		font-size: var(--text-2xs);
		padding: 2px 6px;
		line-height: 1;
		flex-shrink: 0;
		transition: var(--btn-transition);
	}

	.action-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.action-btn.stop {
		color: var(--color-error);
	}

	/* --- Detail Panel --- */
	.row-detail-panel {
		padding: 6px 16px 10px 44px;
		background: var(--bg-surface);
		border-top: 1px solid var(--border-subtle);
	}

	.detail-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 3px 0;
		font-size: var(--text-2xs);
	}

	.detail-label {
		color: var(--text-secondary);
		min-width: 80px;
	}

	.detail-value {
		color: var(--text-primary);
		text-align: right;
	}

	.detail-value.mono {
		font-family: var(--font-mono);
	}

	.detail-value.error {
		color: var(--color-error);
	}

	/* --- Editable fields --- */
	.detail-row.editable {
		margin-top: 2px;
	}

	.edit-field {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.edit-input {
		width: 60px;
		padding: var(--input-padding);
		background: var(--input-bg);
		border: var(--input-border);
		border-radius: var(--input-radius);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
	}

	.edit-input:focus {
		border: var(--input-border-focus);
		outline: none;
	}

	.edit-unit {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.apply-btn {
		padding: 2px 8px;
		background: var(--bg-control);
		border: var(--btn-border);
		border-radius: var(--radius-sm);
		color: var(--accent-blue);
		cursor: pointer;
		font-size: var(--text-2xs);
		font-family: var(--font-ui);
		font-weight: var(--btn-weight);
		transition: var(--btn-transition);
	}

	.apply-btn:hover {
		background: var(--bg-hover);
	}

	/* --- Detail Actions --- */
	.detail-actions {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-top: 8px;
		padding-top: 6px;
		border-top: 1px solid var(--border-subtle);
	}

	.delete-btn {
		padding: 3px 10px;
		background: var(--color-error-dim);
		border: 1px solid rgba(239, 68, 68, 0.2);
		border-radius: var(--radius-sm);
		color: var(--color-error);
		cursor: pointer;
		font-size: var(--text-2xs);
		font-family: var(--font-ui);
		font-weight: 500;
		transition: var(--btn-transition);
	}

	.delete-btn:hover {
		background: rgba(239, 68, 68, 0.2);
	}

	.confirm-text {
		font-size: var(--text-2xs);
		color: var(--color-warning);
	}

	.confirm-btn {
		padding: 3px 10px;
		background: var(--bg-control);
		border: var(--btn-border);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		cursor: pointer;
		font-size: var(--text-2xs);
		font-family: var(--font-ui);
		font-weight: 500;
		transition: var(--btn-transition);
	}

	.confirm-btn:hover {
		background: var(--bg-hover);
	}

	.confirm-btn.danger {
		background: var(--color-error-dim);
		color: var(--color-error);
		border-color: rgba(239, 68, 68, 0.2);
	}

	.confirm-btn.danger:hover {
		background: rgba(239, 68, 68, 0.2);
	}

	/* --- Add Button --- */
	.add-btn {
		display: block;
		width: 100%;
		padding: 7px 16px;
		background: none;
		border: none;
		border-top: 1px solid var(--border-subtle);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 500;
		color: var(--accent-blue);
		text-align: left;
		transition: background var(--transition-fast);
	}

	.add-btn:hover {
		background: var(--bg-hover);
	}

	/* --- Add Form --- */
	.add-form {
		padding: 10px 16px;
		background: var(--bg-surface);
		border-top: 1px solid var(--border-subtle);
	}

	.form-title {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-primary);
		margin-bottom: 8px;
	}

	.form-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 6px;
	}

	.form-label {
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		min-width: 64px;
		flex-shrink: 0;
	}

	.form-label-inline {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		cursor: pointer;
	}

	.form-input {
		flex: 1;
		padding: var(--input-padding);
		background: var(--input-bg);
		border: var(--input-border);
		border-radius: var(--input-radius);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
	}

	.form-input:focus {
		border: var(--input-border-focus);
		outline: none;
	}

	.form-input.short {
		flex: 0;
		width: 72px;
	}

	select.form-input {
		font-family: var(--font-ui);
		cursor: pointer;
	}

	.form-input-group {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.form-unit {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
	}

	.form-actions {
		display: flex;
		gap: 6px;
		margin-top: 8px;
	}

	.form-btn {
		padding: 4px 12px;
		background: var(--btn-bg);
		border: var(--btn-border);
		border-radius: var(--btn-radius);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: var(--btn-weight);
		letter-spacing: var(--btn-letter-spacing);
		transition: var(--btn-transition);
	}

	.form-btn:hover {
		background: var(--btn-bg-hover);
		color: var(--text-primary);
	}

	.form-btn.primary {
		background: var(--accent-blue-dim);
		color: var(--accent-blue);
		border-color: rgba(59, 130, 246, 0.2);
	}

	.form-btn.primary:hover {
		background: var(--accent-blue-light);
	}

	/* --- Recording Status --- */
	.recording-status {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 8px 16px;
		border-top: 1px solid var(--border-subtle);
		font-size: var(--text-2xs);
	}

	.rec-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.rec-dot.active {
		background: var(--color-error);
		animation: rec-blink 1s ease-in-out infinite;
	}

	.rec-dot.inactive {
		background: var(--text-tertiary);
	}

	@keyframes rec-blink {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.3; }
	}

	.rec-label {
		color: var(--color-error);
		font-weight: 600;
		letter-spacing: 0.05em;
	}

	.rec-filename {
		color: var(--text-secondary);
		font-family: var(--font-mono);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
		flex: 1;
	}

	.rec-duration {
		color: var(--text-primary);
		font-family: var(--font-mono);
		flex-shrink: 0;
	}

	.rec-bytes {
		color: var(--text-secondary);
		font-family: var(--font-mono);
		flex-shrink: 0;
	}

	.rec-inactive {
		color: var(--text-tertiary);
	}
</style>
