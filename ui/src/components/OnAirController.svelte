<script lang="ts">
	import type { ControlRoomState, PlaylistItem } from '$lib/api/types';
	import type { MediaPipeline } from '$lib/transport/media-pipeline';
	import type { FastControl } from '$lib/transport/fast-control';
	import { playoutPlaylist, playoutAsRun, setCaptionMode, engineFetch, authHeaders } from '$lib/api/switch-api';
	import { poll } from '$lib/util/poll';
	import { resolveApiUrl } from '$lib/api/base-url';
	import { setupHiDPICanvas } from '$lib/video/canvas-utils';
	import OAStatusBar from './on-air/OAStatusBar.svelte';
	import OAProgramMonitor from './on-air/OAProgramMonitor.svelte';
	import OANextMonitor from './on-air/OANextMonitor.svelte';
	import OASourceGrid from './on-air/OASourceGrid.svelte';
	import OAOperationsDashboard from './on-air/OAOperationsDashboard.svelte';
	import OAActionControls from './on-air/OAActionControls.svelte';
	import OASlideOut from './on-air/OASlideOut.svelte';
	import OASettingsModal from './on-air/OASettingsModal.svelte';
	import AudioMixer from './AudioMixer.svelte';
	import IOPanel from './IOPanel.svelte';
	import CompositionPanel from './CompositionPanel.svelte';
	import SCTE35Panel from './SCTE35Panel.svelte';
	import CaptionsPanel from './CaptionsPanel.svelte';
	import ReplayPanel from './ReplayPanel.svelte';
	import ClipsPanel from './ClipsPanel.svelte';
	import MacroPanel from './MacroPanel.svelte';
	import CommsBar from './CommsBar.svelte';
	import PipelineRoutingOverlay from './on-air/PipelineRoutingOverlay.svelte';
	import * as operatorState from '$lib/state/operator.svelte';

	interface Props {
		crState: ControlRoomState;
		onCanvasReady: (preview: HTMLCanvasElement, program: HTMLCanvasElement) => void;
		onSwitchLayout: () => void;
		isDualEngine?: boolean;
		engineAHealthy?: boolean;
		engineBHealthy?: boolean;
		activeEngine?: 'A' | 'B';
		authoritativeEngine?: 'A' | 'B';
		onEngineToggle?: (engine: 'A' | 'B') => void;
		syncStatus?: string;
		onSyncClick?: () => void;
		onCut?: (source: string) => void;
		onAuto?: (source: string) => void;
		onBugToggle?: () => void;
		onScteInsert?: () => void;
		onScteReturn?: () => void;
		onFirePod?: () => void;
		onReturnToLive?: () => void;
		onHold?: () => void;
		onSkip?: () => void;
		onTakeNext?: () => void;
		onRecordToggle?: () => void;
		onStateUpdate?: (state: ControlRoomState) => void;
		onTransitionTypeChange?: (type: string) => void;
		onTransitionDurationChange?: (ms: number) => void;
		transitionType?: string;
		transitionDuration?: number;
		pipeline?: MediaPipeline | null;
		fastControl?: FastControl | null;
		getTransport?: () => WebTransport | null;
	}

	let {
		crState,
		onCanvasReady,
		onSwitchLayout,
		isDualEngine = false,
		engineAHealthy = true,
		engineBHealthy = true,
		activeEngine = 'A' as 'A' | 'B',
		authoritativeEngine = 'A' as 'A' | 'B',
		onEngineToggle,
		syncStatus = 'disconnected',
		onSyncClick,
		onCut,
		onAuto,
		onBugToggle,
		onScteInsert,
		onScteReturn,
		onFirePod,
		onReturnToLive,
		onHold,
		onSkip,
		onTakeNext,
		onRecordToggle,
		onStateUpdate,
		onTransitionTypeChange,
		onTransitionDurationChange,
		transitionType = 'mix',
		transitionDuration = 1000,
		pipeline = null,
		fastControl = null,
		getTransport,
	}: Props = $props();

	let previewCanvas: HTMLCanvasElement;
	let programCanvas: HTMLCanvasElement;

	$effect(() => {
		if (previewCanvas && programCanvas && onCanvasReady) {
			onCanvasReady(previewCanvas, programCanvas);
		}
	});

	// High-DPI canvas sizing via ResizeObserver
	$effect(() => {
		if (!programCanvas) return;

		const observers: ResizeObserver[] = [];

		if (programCanvas.parentElement) {
			const obs = new ResizeObserver(([entry]) => {
				const { width, height } = entry.contentRect;
				if (width > 0 && height > 0) setupHiDPICanvas(programCanvas, width, height);
			});
			obs.observe(programCanvas.parentElement);
			observers.push(obs);
		}

		return () => observers.forEach((obs) => obs.disconnect());
	});

	// ── Skip confirmation dialog ──

	let skipConfirmVisible = $state(false);

	function requestSkip() {
		skipConfirmVisible = true;
	}

	function confirmSkip() {
		skipConfirmVisible = false;
		onSkip?.();
	}

	function cancelSkip() {
		skipConfirmVisible = false;
	}

	// ── Playout mode (driven by dashboard toggle) ──

	let playoutEnabled = $state(false);

	// ── Uptime counter ──

	let uptimeStart = $state(Date.now());
	let uptimeNow = $state(Date.now());

	$effect(() => {
		const id = setInterval(() => { uptimeNow = Date.now(); }, 1000);
		return () => clearInterval(id);
	});

	const uptimeSeconds = $derived(Math.floor((uptimeNow - uptimeStart) / 1000));

	// ── Derived state ──

	const playoutChannelState = $derived(
		crState.playout?.channels?.find(c => c.id === 1) ?? crState.playout?.channels?.[0]
	);

	const hasPlaylist = $derived(
		(playoutChannelState?.itemCount ?? 0) > 0
	);

	const programSourceLabel = $derived(
		crState.sources?.[crState.programSource]?.label ?? crState.programSource ?? 'No Source'
	);

	const bugActive = $derived(
		(crState.graphics?.layers ?? []).some((l) => l.active)
	);

	const scteState = $derived.by((): 'content' | 'in_break' | 'idle' => {
		const active = crState.scte35?.activeEvents ?? {};
		if (Object.keys(active).length > 0) return 'in_break';
		// If SCTE-35 is not enabled, show idle
		if (!crState.scte35?.enabled) return 'idle';
		return 'content';
	});

	const scteCountdown = $derived.by((): number | undefined => {
		const active = crState.scte35?.activeEvents ?? {};
		const first = Object.values(active)[0];
		if (!first || first.remainingMs == null) return undefined;
		return Math.max(0, first.remainingMs / 1000);
	});

	const playoutChannel = $derived(playoutChannelState);

	const captionMode = $derived(
		(crState.captions?.mode ?? 'off') as 'off' | 'passthrough' | 'author'
	);

	// ── Segment timing from playout channel ──

	const segmentElapsed = $derived.by((): number | undefined => {
		const ch = playoutChannel;
		if (!ch || ch.itemProgress == null || ch.itemRemainMs == null) return undefined;
		if (ch.itemProgress <= 0 || ch.itemProgress >= 1) return undefined;
		const totalMs = ch.itemRemainMs / (1 - ch.itemProgress);
		return Math.floor((totalMs - ch.itemRemainMs) / 1000);
	});

	const segmentDuration = $derived.by((): number | undefined => {
		const ch = playoutChannel;
		if (!ch || ch.itemProgress == null || ch.itemRemainMs == null) return undefined;
		if (ch.itemProgress <= 0) return undefined;
		const totalMs = ch.itemRemainMs / (1 - ch.itemProgress);
		return Math.floor(totalMs / 1000);
	});

	// ── Recording state ──

	const isRecording = $derived(crState.recording?.active ?? false);
	const recordingDuration = $derived(
		crState.recording?.active ? Math.floor(crState.recording?.durationSecs ?? 0) : 0
	);

	// ── Audio peak levels ──

	const programPeakL = $derived(crState.programPeak?.[0] ?? -60);
	const programPeakR = $derived(crState.programPeak?.[1] ?? -60);

	const podArmed = $derived(
		(playoutChannel?.podsArmed ?? 0) > 0
	);

	const nextItemTitle = $derived(
		playoutChannel?.nextTitle ?? 'No item cued'
	);

	const nextPlaylistItem = $derived.by(() => {
		const nextId = playoutChannel?.nextItemId;
		if (!nextId || playlistItems.length === 0) return undefined;
		return playlistItems.find(item => item.id === nextId);
	});

	const nextItemDuration = $derived(
		nextPlaylistItem?.duration_ms != null ? Math.floor(nextPlaylistItem.duration_ms / 1000) : undefined
	);

	const nextItemType = $derived(
		nextPlaylistItem?.type as 'live' | 'clip' | 'pod' | 'slate' | undefined
	);

	// ── Playlist items fetch ──

	let playlistItems = $state<PlaylistItem[]>([]);
	let lastFetchKey = $state('');
	let activeFetchKey = '';

	$effect(() => {
		const ch = playoutChannel;
		const channelId = ch?.id ?? 1;
		const count = ch?.itemCount ?? 0;
		const fetchKey = `${channelId}:${count}`;
		if (fetchKey === lastFetchKey) return;
		lastFetchKey = fetchKey;
		activeFetchKey = fetchKey;
		if (count <= 0) {
			playlistItems = [];
			return;
		}
		const myFetchKey = fetchKey;
		playoutPlaylist(channelId).then(items => {
			if (activeFetchKey === myFetchKey) {
				playlistItems = items;
			}
		}).catch(() => {
			if (activeFetchKey === myFetchKey) playlistItems = [];
		});
	});

	// ── Slide-out and modal state ──

	let audioSlideOut = $state(false);
	let ioSlideOut = $state(false);
	let graphicsSlideOut = $state(false);
	let scteSlideOut = $state(false);
	let captionsSlideOut = $state(false);
	let replaySlideOut = $state(false);
	let clipsSlideOut = $state(false);
	let macrosSlideOut = $state(false);
	let commsSlideOut = $state(false);
	let asRunSlideOut = $state(false);
	let settingsVisible = $state(false);
	let routingOverlayVisible = $state(false);
	let bypassCount = $state(0);

	// ── As-run log data ──

	let asRunEvents = $state<import('$lib/api/types').AsRunEvent[]>([]);

	function closeAllSlideOuts() {
		audioSlideOut = false;
		ioSlideOut = false;
		graphicsSlideOut = false;
		scteSlideOut = false;
		captionsSlideOut = false;
		replaySlideOut = false;
		clipsSlideOut = false;
		macrosSlideOut = false;
		commsSlideOut = false;
		asRunSlideOut = false;
		routingOverlayVisible = false;
	}

	// ── Bypass count polling (when overlay is closed) ──

	$effect(() => {
		if (routingOverlayVisible) return; // overlay does its own polling
		// A failed read keeps the current count.
		return poll(async (signal) => {
			const res = await engineFetch(resolveApiUrl('/api/pipeline/snapshot'), { headers: authHeaders(), signal });
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const data = await res.json();
			bypassCount = data.bypassCount ?? 0;
		}, 5000, { immediate: false });
	});

	/** Cycle caption mode: off → passthrough → author → off */
	function handleCaptionCycle() {
		const modes = ['off', 'passthrough', 'author'] as const;
		const currentIndex = modes.indexOf(captionMode as typeof modes[number]);
		const nextMode = modes[(currentIndex + 1) % modes.length];
		setCaptionMode(nextMode).catch(() => {});
	}

	// ── Keyboard shortcuts ──

	function handleKeydown(e: KeyboardEvent) {
		if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;

		switch (e.code) {
			case 'Space':
				e.preventDefault();
				if (playoutEnabled) onFirePod?.();
				else onCut?.(crState.previewSource);
				break;
			case 'Enter':
				e.preventDefault();
				if (playoutEnabled) onReturnToLive?.();
				else onAuto?.(crState.previewSource);
				break;
			case 'KeyB':
				e.preventDefault();
				onBugToggle?.();
				break;
			case 'KeyH':
				if (playoutEnabled) {
					e.preventDefault();
					onHold?.();
				}
				break;
			case 'KeyS':
				if (playoutEnabled) {
					e.preventDefault();
					requestSkip();
				}
				break;
			case 'KeyN':
				if (playoutEnabled) {
					e.preventDefault();
					onTakeNext?.();
				}
				break;
			case 'Digit1': case 'Digit2': case 'Digit3': case 'Digit4':
			case 'Digit5': case 'Digit6': case 'Digit7': case 'Digit8': case 'Digit9': {
				if (!playoutEnabled) {
					e.preventDefault();
					const index = parseInt(e.code.replace('Digit', '')) - 1;
					const sourceKeys = Object.keys(crState.sources ?? {});
					if (index < sourceKeys.length) {
						onCut?.(sourceKeys[index]);
					}
				}
				break;
			}
			case 'Escape':
				closeAllSlideOuts();
				settingsVisible = false;
				break;
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="on-air-controller">
	<OAStatusBar
		programSource={programSourceLabel}
		{bugActive}
		{scteState}
		{scteCountdown}
		{segmentElapsed}
		{segmentDuration}
		{isDualEngine}
		{engineAHealthy}
		{engineBHealthy}
		{syncStatus}
		{onSyncClick}
		{onBugToggle}
		onModeMenu={onSwitchLayout}
		showUptime={!playoutEnabled}
		{uptimeSeconds}
		{isRecording}
		{recordingDuration}
		{programPeakL}
		{programPeakR}
		{onRecordToggle}
		{bypassCount}
		onBypassClick={() => routingOverlayVisible = true}
	/>

	<div class="oa-main">
		<section class="oa-monitor-column">
			<OAProgramMonitor
				programSource={programSourceLabel}
				{isDualEngine}
				{activeEngine}
				{authoritativeEngine}
				{onEngineToggle}
				onCanvasReady={(canvas) => {
					programCanvas = canvas;
				}}
			/>
			<!-- Hidden preview canvas for pipeline -->
			<canvas bind:this={previewCanvas} class="hidden-canvas"></canvas>
			<OANextMonitor
				nextItemTitle={nextItemTitle}
				{nextItemDuration}
				{nextItemType}
			/>
		</section>

		<section class="oa-content-column">
			<div class="oa-content-area">
				<!-- Always show compact source strip + operations dashboard -->
				<OASourceGrid
					sources={crState.sources ?? {}}
					tallyState={crState.tallyState ?? {}}
					{isDualEngine}
					onSourceClick={(key) => onCut?.(key)}
					onOpenIO={() => { ioSlideOut = true; }}
					compact={true}
				/>
				<OAOperationsDashboard
					{crState}
					onCut={(key) => onCut?.(key)}
					{onBugToggle}
					playoutChannel={playoutChannel}
					{playlistItems}
					onShowAsRun={() => { asRunSlideOut = true; }}
					onPlayoutToggle={(enabled) => { playoutEnabled = enabled; }}
				/>
			</div>

			<OAActionControls
				mode={playoutEnabled ? 'playout' : 'live-monitor'}
				{bugActive}
				{captionMode}
				{podArmed}
				inBreak={scteState === 'in_break'}
				holdActive={playoutChannel?.mode === 'manual'}
				{transitionType}
				{transitionDuration}
				{onTransitionTypeChange}
				{onTransitionDurationChange}
				{onBugToggle}
				{onScteInsert}
				{onScteReturn}
				{onFirePod}
				{onReturnToLive}
				{onHold}
				onSkip={requestSkip}
				{onTakeNext}
				onCut={() => onCut?.(crState.previewSource)}
				onAuto={() => onAuto?.(crState.previewSource)}
				onAudioToggle={() => { audioSlideOut = !audioSlideOut; }}
				onIOToggle={() => { ioSlideOut = !ioSlideOut; }}
				onSettingsOpen={() => { settingsVisible = true; }}
				onCaptionCycle={handleCaptionCycle}
				onGraphicsToggle={() => { graphicsSlideOut = !graphicsSlideOut; }}
				onScteToggle={() => { scteSlideOut = !scteSlideOut; }}
				onCaptionsToggle={() => { captionsSlideOut = !captionsSlideOut; }}
				onReplayToggle={() => { replaySlideOut = !replaySlideOut; }}
				onClipsToggle={() => { clipsSlideOut = !clipsSlideOut; }}
				onMacrosToggle={() => { macrosSlideOut = !macrosSlideOut; }}
				onCommsToggle={() => { commsSlideOut = !commsSlideOut; }}
			onRoutingToggle={() => { routingOverlayVisible = !routingOverlayVisible; }}
			/>
		</section>
	</div>

	<!-- Slide-outs -->
	<OASlideOut visible={audioSlideOut} title="AUDIO" onclose={() => { audioSlideOut = false; }} width={560}>
		<AudioMixer
			state={crState}
			onStateUpdate={(s) => onStateUpdate?.(s)}
		/>
	</OASlideOut>

	<OASlideOut visible={ioSlideOut} title="I/O" onclose={() => { ioSlideOut = false; }} width={480}>
		<IOPanel
			state={crState}
			visible={ioSlideOut}
			onclose={() => { ioSlideOut = false; }}
			{isDualEngine}
		/>
	</OASlideOut>

	<OASlideOut visible={graphicsSlideOut} title="COMPOSE" onclose={() => { graphicsSlideOut = false; }} width={480}>
		<CompositionPanel
			state={crState}
			{fastControl}
		/>
	</OASlideOut>

	<OASlideOut visible={scteSlideOut} title="SCTE-35" onclose={() => { scteSlideOut = false; }} width={420}>
		{#if crState.scte35?.enabled}
			<SCTE35Panel
				state={crState}
				onStateUpdate={(s) => onStateUpdate?.(s)}
			/>
		{:else}
			<div class="panel-disabled">SCTE-35 not enabled on server</div>
		{/if}
	</OASlideOut>

	<OASlideOut visible={captionsSlideOut} title="CAPTIONS" onclose={() => { captionsSlideOut = false; }} width={420}>
		<CaptionsPanel
			state={crState}
		/>
	</OASlideOut>

	<OASlideOut visible={replaySlideOut} title="REPLAY" onclose={() => { replaySlideOut = false; }} width={480}>
		<ReplayPanel
			state={crState}
			{pipeline}
		/>
	</OASlideOut>

	<OASlideOut visible={clipsSlideOut} title="CLIPS" onclose={() => { clipsSlideOut = false; }} width={420}>
		<ClipsPanel
			state={crState}
		/>
	</OASlideOut>

	<OASlideOut visible={macrosSlideOut} title="MACROS" onclose={() => { macrosSlideOut = false; }} width={420}>
		<MacroPanel
			state={crState}
		/>
	</OASlideOut>

	<OASlideOut visible={commsSlideOut} title="COMMS" onclose={() => { commsSlideOut = false; }} width={350}>
		<CommsBar
			commsState={crState.comms}
			operatorId={operatorState.getSession()?.id ?? ''}
			operatorName={operatorState.getSession()?.name ?? ''}
			visible={commsSlideOut}
			onToggle={() => { commsSlideOut = !commsSlideOut; }}
			getTransport={getTransport}
		/>
	</OASlideOut>

	<OASlideOut visible={asRunSlideOut} title="AS-RUN LOG" onclose={() => { asRunSlideOut = false; }} width={480}>
		<div class="as-run-log">
			{#if asRunEvents.length === 0}
				<div class="as-run-empty">No as-run events recorded</div>
			{:else}
				<div class="as-run-list">
					{#each asRunEvents as event}
						<div class="as-run-event">
							<span class="as-run-time">{new Date(event.ts).toLocaleTimeString()}</span>
							<span class="as-run-type as-run-type-{event.type}">{event.type.replace(/_/g, ' ')}</span>
							{#if event.title}
								<span class="as-run-title">{event.title}</span>
							{/if}
							{#if event.delta_ms != null}
								<span class="as-run-delta" class:as-run-delta-late={event.delta_ms > 500}>
									{event.delta_ms > 0 ? '+' : ''}{event.delta_ms}ms
								</span>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</div>
	</OASlideOut>

	<PipelineRoutingOverlay
		visible={routingOverlayVisible}
		onclose={() => { routingOverlayVisible = false; }}
		onBypassCountChange={(count) => { bypassCount = count; }}
		onSourceCut={(key) => onCut?.(key)}
		programSource={crState.programSource}
		previewSource={crState.previewSource}
		programPeak={crState.programPeak}
	/>

	<OASettingsModal
		visible={settingsVisible}
		onclose={() => { settingsVisible = false; }}
		{crState}
		onOpenGraphics={() => { settingsVisible = false; graphicsSlideOut = true; }}
	/>

	{#if skipConfirmVisible}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="skip-confirm-backdrop" onclick={cancelSkip} onkeydown={(e) => { if (e.code === 'Escape') cancelSkip(); }}>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<div class="skip-confirm-dialog" onclick={(e) => e.stopPropagation()}>
				<p class="skip-confirm-text">Skip to next item?</p>
				<div class="skip-confirm-buttons">
					<button class="btn btn-cancel" onclick={cancelSkip}>Cancel</button>
					<button class="btn btn-confirm" onclick={confirmSkip}>Skip</button>
				</div>
			</div>
		</div>
	{/if}
</div>

<style>
	.on-air-controller {
		display: grid;
		grid-template-rows: auto 1fr;
		height: 100vh;
		background: var(--bg-base);
		color: var(--text-primary);
		font-family: var(--font-ui);
		overflow: hidden;
	}

	.oa-main {
		display: grid;
		grid-template-columns: 40% 60%;
		gap: 1px;
		min-height: 0;
		background: var(--border-subtle);
	}

	.oa-monitor-column {
		display: flex;
		flex-direction: column;
		background: var(--bg-base);
		min-height: 0;
		overflow: hidden;
	}

	.oa-content-column {
		display: flex;
		flex-direction: column;
		background: var(--bg-base);
		min-height: 0;
	}

	.oa-content-area {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	.hidden-canvas {
		position: absolute;
		width: 0;
		height: 0;
		overflow: hidden;
		pointer-events: none;
	}

	/* ── Responsive: Tablet (768-1023px) ── */
	@media (max-width: 1023px) {
		.oa-main {
			grid-template-columns: 1fr;
		}

		.oa-monitor-column {
			border-right: none;
			border-bottom: 1px solid var(--border-default);
			max-height: 40vh;
		}
	}

	/* ── Responsive: Mobile (<768px) ── */
	@media (max-width: 767px) {
		.oa-monitor-column {
			max-height: 30vh;
		}

		.oa-monitor-column :global(.oa-next-monitor) {
			display: none;
		}
	}

	/* ── Touch devices ── */
	@media (pointer: coarse) {
		.oa-main :global(.btn),
		.oa-main :global(.oa-source-tile),
		.oa-main :global(.grid-action-btn) {
			min-height: 44px;
			min-width: 44px;
		}
	}

	/* ── Panel disabled ── */
	.panel-disabled {
		padding: 24px 16px;
		text-align: center;
		color: var(--text-tertiary);
		font-size: var(--text-sm);
	}

	/* ── As-run log ── */
	.as-run-log {
		padding: 8px;
		height: 100%;
		overflow-y: auto;
	}

	.as-run-empty {
		padding: 24px 12px;
		text-align: center;
		font-size: 12px;
		color: var(--text-secondary);
	}

	.as-run-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.as-run-event {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 8px;
		border-radius: var(--radius-xs);
		font-size: 11px;
		background: var(--bg-elevated);
	}

	.as-run-time {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.as-run-type {
		font-weight: 600;
		text-transform: uppercase;
		font-size: 9px;
		letter-spacing: 0.06em;
		padding: 1px 4px;
		border-radius: 2px;
		background: var(--bg-recessed);
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.as-run-type-item_start {
		color: var(--accent-green);
		background: rgba(34, 197, 94, 0.1);
	}

	.as-run-type-item_end {
		color: var(--text-tertiary);
	}

	.as-run-type-error {
		color: var(--color-error);
		background: var(--tally-program-dim);
	}

	.as-run-title {
		color: var(--text-primary);
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.as-run-delta {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.as-run-delta-late {
		color: var(--accent-yellow);
	}

	/* ── Skip confirm dialog ── */
	.skip-confirm-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: var(--z-fullscreen);
	}

	.skip-confirm-dialog {
		background: var(--bg-elevated, #1c1c21);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md, 6px);
		padding: 20px 24px;
		min-width: 260px;
		text-align: center;
	}

	.skip-confirm-text {
		margin: 0 0 16px;
		font-family: var(--font-ui);
		font-size: 14px;
		font-weight: 500;
		color: var(--text-primary);
	}

	.skip-confirm-buttons {
		display: flex;
		gap: 8px;
		justify-content: center;
	}

	.skip-confirm-buttons .btn {
		padding: 6px 16px;
		font-family: var(--font-ui);
		font-size: 12px;
		font-weight: 600;
		border-radius: var(--radius-sm);
		cursor: pointer;
		border: 1px solid var(--border-default);
	}

	.btn-cancel {
		background: var(--bg-control, #2a2a30);
		color: var(--text-secondary);
	}

	.btn-cancel:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.btn-confirm {
		background: var(--tally-program-dim, rgba(220, 38, 38, 0.1));
		border-color: rgba(220, 38, 38, 0.4);
		color: var(--tally-program, #dc2626);
	}

	.btn-confirm:hover {
		background: rgba(220, 38, 38, 0.2);
		border-color: rgba(220, 38, 38, 0.6);
	}
</style>
