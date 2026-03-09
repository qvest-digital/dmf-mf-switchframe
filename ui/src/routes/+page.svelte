<script lang="ts">
	import { onMount, onDestroy, tick } from 'svelte';
	import ProgramPreview from '../components/ProgramPreview.svelte';
	import Multiview from '../components/Multiview.svelte';
	import PreviewBus from '../components/PreviewBus.svelte';
	import ProgramBus from '../components/ProgramBus.svelte';
	import TransitionControls from '../components/TransitionControls.svelte';
	import AudioMixer from '../components/AudioMixer.svelte';
	import OutputControls from '../components/OutputControls.svelte';
	import KeyboardOverlay from '../components/KeyboardOverlay.svelte';
	import LoadingOverlay from '../components/LoadingOverlay.svelte';
	import ConnectionBanner from '../components/ConnectionBanner.svelte';
	import ProgramHealthBanner from '../components/ProgramHealthBanner.svelte';
	import SimpleMode from '../components/SimpleMode.svelte';
	import ErrorBoundary from '../components/ErrorBoundary.svelte';
	import Toast from '../components/Toast.svelte';
	import ServerPipelineOverlay from '../components/ServerPipelineOverlay.svelte';
	import GraphicsPanel from '../components/GraphicsPanel.svelte';
	import MacroPanel from '../components/MacroPanel.svelte';
	import KeyPanel from '../components/KeyPanel.svelte';
	import ReplayPanel from '../components/ReplayPanel.svelte';
	import PresetPanel from '../components/PresetPanel.svelte';
	import SCTE35Panel from '../components/SCTE35Panel.svelte';
	import OperatorRegistration from '../components/OperatorRegistration.svelte';
	import OperatorBadge from '../components/OperatorBadge.svelte';
	import LockIndicator from '../components/LockIndicator.svelte';
	import BottomTabs from '../components/BottomTabs.svelte';
	import { createControlRoomStore } from '$lib/state/control-room.svelte';
	import { cut, setPreview, setLabel, startTransition, fadeToBlack, graphicsOn, graphicsOff, apiCall, setAuthToken, SwitchApiError, listMacros, runMacro } from '$lib/api/switch-api';
	import { setApiBaseUrl, resolveApiUrl } from '$lib/api/base-url';
	import { wtBaseURL, fetchServerInfo } from '$lib/prism/transport-utils';
	import * as operatorState from '$lib/state/operator.svelte';
	import { notify } from '$lib/state/notifications.svelte';
	import { getSourceError } from '$lib/transport/source-errors.svelte';
	import { KeyboardHandler } from '$lib/keyboard/handler';
	import { ConnectionManager } from '$lib/transport/connection-manager';
	import { createMediaPipeline } from '$lib/transport/media-pipeline';
	import { PipelineManager } from '$lib/pipeline/manager';
	import { createPFLManager } from '$lib/audio/pfl';
	import { createPFLToggle } from '$lib/audio/pfl-toggle';
	import { getLayoutMode, setLayoutMode, type LayoutMode } from '$lib/layout/preferences';
	import type { ControlRoomState, Macro } from '$lib/api/types';

	const store = createControlRoomStore();
	let showOverlay = $state(false);
	let layoutMode = $state<LayoutMode>(getLayoutMode());
	let mounted = $state(false);
	let connectionState = $state<'webtransport' | 'polling' | 'disconnected'>('disconnected');
	let initialLoading = $state(true);
	let connectionError: string | null = $state(null);
	let tokenRequired = $state(false);
	let tokenInput = $state('');
	let showOperatorRegistration = $state(false);

	// ARIA live region for screen reader announcements
	let announcement = $state('');
	let announcementTimer: ReturnType<typeof setTimeout> | undefined;

	// Periodic tick for sync status detection (drives time-based re-evaluation)
	let now = $state(Date.now());
	let syncInterval: ReturnType<typeof setInterval> | undefined;

	let syncStatus = $derived.by(() => {
		// When MoQ is delivering state via the media pipeline (connectionState
		// is 'webtransport'), trust it — state updates are event-driven, so
		// gaps between updates during idle periods are normal and expected.
		if (connectionState === 'webtransport') return 'ok' as const;
		// Fallback: time-based detection for polling mode.
		const elapsed = now - store.lastServerUpdate;
		if (elapsed > 5000) return 'disconnected' as const;
		if (elapsed > 2000) return 'resyncing' as const;
		return 'ok' as const;
	});

	function announce(msg: string) {
		announcement = msg;
		clearTimeout(announcementTimer);
		announcementTimer = setTimeout(() => { announcement = ''; }, 3000);
	}

	// Canvas refs passed up from ProgramPreview / SimpleMode via onCanvasReady
	let programCanvas: HTMLCanvasElement | null = $state(null);
	let previewCanvas: HTMLCanvasElement | null = $state(null);

	function onCanvasReady(preview: HTMLCanvasElement, program: HTMLCanvasElement) {
		previewCanvas = preview;
		programCanvas = program;
	}

	function switchLayout() {
		layoutMode = layoutMode === 'traditional' ? 'simple' : 'traditional';
		setLayoutMode(layoutMode);
	}
	let transitionType: 'mix' | 'dip' = 'mix';
	let transitionDuration = 1000;
	let eqExpandedKeys: Record<string, boolean> = $state({});

	const keyboard = new KeyboardHandler({
		onCut: () => {
			if (store.state.previewSource) {
				store.optimisticCut(store.state.previewSource);
				apiCall(cut(store.state.previewSource), 'Cut failed');
			}
		},
		onSetPreview: (key) => {
			store.optimisticPreview(key);
			apiCall(setPreview(key), 'Preview failed');
		},
		onHotPunch: (key) => {
			store.optimisticCut(key);
			apiCall(cut(key), 'Cut failed');
		},
		onAutoTransition: () => {
			if (store.state.previewSource && !store.state.inTransition && !store.state.ftbActive) {
				apiCall(startTransition(store.state.previewSource, transitionType, transitionDuration), 'Transition failed');
			}
		},
		onFadeToBlack: () => {
			if (!store.state.inTransition || store.state.ftbActive) {
				apiCall(fadeToBlack(), 'FTB failed');
			}
		},
		onToggleFullscreen: () => {
			document.fullscreenElement
				? document.exitFullscreen()
				: document.documentElement.requestFullscreen();
		},
		onToggleOverlay: () => { showOverlay = !showOverlay; },
		onToggleDSK: () => {
			if (store.state.graphics?.active) {
				apiCall(graphicsOff(), 'Graphics failed');
			} else {
				apiCall(graphicsOn(), 'Graphics failed');
			}
		},
		onSetTransitionType: (type) => {
			if (type === 'mix' || type === 'dip') {
				transitionType = type;
			}
		},
		onRunMacro: (slotIndex) => {
			if (slotIndex < macroList.length) {
				apiCall(runMacro(macroList[slotIndex].name), 'Macro failed');
			}
		},
		getSourceKeys: () => store.sourceKeys,
	});

	// URL is the page origin — in production (embedded UI on :8080), WebTransport
	// connects same-origin. In dev (Vite :5173), the connection.ts WebTransport
	// path will fail (Vite doesn't speak QUIC), which is fine: MoQ state arrives
	// via the media pipeline's per-source MoQTransport instead.
	const connectionManager = new ConnectionManager({
		url: window.location.origin,
		onStateUpdate: (update) => {
			if (update instanceof Uint8Array) {
				store.applyFromMoQ(update);
			} else {
				store.applyUpdate(update as ControlRoomState);
			}
		},
		onConnectionStateChange: (state) => {
			connectionState = state;
		},
		onInitialLoadComplete: () => {
			initialLoading = false;
			connectionError = null;
		},
		onInitialLoadError: (error, rawError) => {
			console.warn('Failed to fetch initial state:', error);
			if (rawError instanceof SwitchApiError && rawError.status === 401) {
				tokenRequired = true;
				initialLoading = false;
			}
			connectionError = error;
		},
	});

	// Media pipeline for MoQ video/audio decode
	const pipeline = createMediaPipeline({
		onControlState: (data) => {
			connectionManager.handleControlData(data);
		},
		onRawSourceReady: (sourceKey: string) => {
			// Raw YUV source catalog arrived — re-sync canvases so the pipeline
			// manager switches from PrismRenderer to YUVRenderer.
			// Only react to program-raw (not replay-raw or other raw sources).
			if (mounted && sourceKey === 'program-raw') {
				pipelineManager.resetProgramCanvas();
				pipelineManager.syncProgramPreviewCanvases(store.state.previewSource, programCanvas, previewCanvas);
			}
		},
	});
	// Pre-register "program" source so ProgramPreview's $effect can attach
	// the canvas renderer before onMount (which connects the MoQ transport).
	pipeline.setSourceMuted('program', false);
	pipeline.addSource('program');
	pipeline.addSource('program-raw');

	const pipelineManager = new PipelineManager(pipeline, () => store.sourceKeys, (src, pgm) => {
		sourceLevels = src;
		programLevels = pgm;
	});

	// PFL (Pre-Fade Listen) manager for client-side per-source audio monitoring
	const pflManager = createPFLManager();
	let pflActiveSource = $state<string | null>(null);
	const pflToggle = createPFLToggle({ pflManager, pipeline });

	// Macro list for keyboard trigger (Ctrl+1-9)
	let macroList = $state<Macro[]>([]);
	async function refreshMacros() {
		try { macroList = await listMacros(); } catch { /* ignore */ }
	}

	function handleLabelChange(key: string, label: string) {
		apiCall(setLabel(key, label), 'Label update failed');
	}

	function handlePFLToggle(sourceKey: string) {
		pflActiveSource = pflToggle.toggle(sourceKey);
	}

	// Per-source audio levels sampled from media pipeline decoders (linear 0..1)
	let sourceLevels = $state<Record<string, { peakL: number; peakR: number }>>({});
	// Program output peak levels sampled from program audio decoder (linear 0..1)
	let programLevels = $state<{ peakL: number; peakR: number }>({ peakL: 0, peakR: 0 });

	function handleDebugDump(e: KeyboardEvent) {
		if (e.ctrlKey && e.shiftKey && (e.key === 'd' || e.key === 'D')) {
			e.preventDefault();
			exportDebugSnapshot();
		}
	}

	async function exportDebugSnapshot() {
		const frontend = { sources: await pipeline.getAllDiagnostics() };

		let backend: Record<string, unknown> | null = null;
		try {
			const resp = await fetch(resolveApiUrl('/api/debug/snapshot'));
			if (resp.ok) backend = await resp.json();
		} catch { /* ignore */ }

		const snapshot = {
			timestamp: new Date().toISOString(),
			frontend,
			backend,
		};

		const json = JSON.stringify(snapshot, null, 2);
		try {
			await navigator.clipboard.writeText(json);
			notify('info', 'Debug snapshot copied to clipboard');
		} catch {
			console.log('=== SWITCHFRAME DEBUG SNAPSHOT ===');
			console.log(json);
			notify('info', 'Debug snapshot logged to console');
		}
	}

	// React to source list changes — delegate to PipelineManager
	$effect(() => {
		// Access sourceKeys to create the reactive dependency
		const _keys = store.sourceKeys;
		if (!mounted) return;
		tick().then(() => {
			pipelineManager.syncSources(store.state.sources);
			pipelineManager.syncProgramPreviewCanvases(store.state.previewSource, programCanvas, previewCanvas);
		});
	});

	// React to program/preview changes and canvas ref updates
	let prevProgramSource: string | undefined;
	$effect(() => {
		const _program = store.state.programSource;
		const _preview = store.state.previewSource;
		const _pgmCanvas = programCanvas;
		const _pvwCanvas = previewCanvas;
		if (!mounted) return;

		// Reset A/V sync tracking on program renderer when source changes
		// (transition completed). Prevents stale PTS from old source causing
		// transient sync swings with the new source's PTS.
		if (prevProgramSource !== undefined && _program !== prevProgramSource) {
			pipelineManager.notifyProgramSourceChange();
		}
		prevProgramSource = _program;

		pipelineManager.syncProgramPreviewCanvases(store.state.previewSource, programCanvas, previewCanvas);
	});

	// Re-attach canvases when layout mode changes (DOM is replaced).
	// Skip the initial run — ProgramPreview's onCanvasReady sets the canvas
	// refs during mount, and resetting them here would permanently null them
	// (ProgramPreview's $effect won't re-fire since its bind:this refs are stable).
	let prevLayoutMode: LayoutMode | undefined;
	$effect(() => {
		const mode = layoutMode;
		if (prevLayoutMode !== undefined && mode !== prevLayoutMode) {
			pipelineManager.onLayoutChange();
			// Reset canvas refs — new DOM elements will be provided by onCanvasReady
			programCanvas = null;
			previewCanvas = null;
			// Re-sync after DOM updates
			tick().then(() => {
				if (!mounted) return;
				pipelineManager.syncSources(store.state.sources);
				pipelineManager.syncProgramPreviewCanvases(store.state.previewSource, programCanvas, previewCanvas);
			});
		}
		prevLayoutMode = mode;
	});

	// Sync PFL manager sources with pipeline sources
	$effect(() => {
		const keys = store.sourceKeys;
		for (const key of keys) {
			const decoder = pipeline.getAudioDecoder(key);
			if (decoder && !pflManager.getDecoder(key)) {
				pflManager.addSource(key, 'mp4a.40.2', 48000, 2);
			}
		}
	});

	// Track previous values for state change announcements
	let prevRecording: boolean | undefined;
	let prevFtb: boolean | undefined;

	$effect(() => {
		const isRecording = store.state.recording?.active === true;
		const isFtb = store.state.ftbActive;

		if (prevRecording !== undefined && isRecording !== prevRecording) {
			announce(isRecording ? 'Recording started' : 'Recording stopped');
		}
		if (prevFtb !== undefined && isFtb !== prevFtb && isFtb) {
			announce('Fade to black active');
		}

		prevRecording = isRecording;
		prevFtb = isFtb;
	});

	// Notify operator when the program source has a decoder error
	let prevProgramError: string | null = null;
	$effect(() => {
		const programKey = store.state.programSource;
		const error = programKey ? getSourceError(programKey) : null;
		if (error && error !== prevProgramError) {
			notify('error', `Program source error: ${error}`);
		}
		prevProgramError = error;
	});

	async function submitToken() {
		if (!tokenInput.trim()) return;
		setAuthToken(tokenInput.trim());
		tokenRequired = false;
		connectionError = null;
		initialLoading = true;
		await connectionManager.start();
	}

	onMount(async () => {
		keyboard.attach();
		document.addEventListener('keydown', handleDebugDump);
		mounted = true;
		syncInterval = setInterval(() => { now = Date.now(); }, 1000);

		// Bootstrap: discover QUIC server address and set API base URL.
		// In production (same-origin), this is a no-op (base URL stays '').
		// In dev with mkcert (trusted cert), point API calls directly to the QUIC server.
		// In dev with self-signed cert, keep relative URLs (Vite proxy handles them).
		try {
			const info = await fetchServerInfo();
			const serverOrigin = wtBaseURL(info);
			if (serverOrigin !== window.location.origin && info.trusted) {
				setApiBaseUrl(serverOrigin);
			}
		} catch {
			// Will retry via connection manager
		}

		// Connect the "program" MoQ stream (source was added during init
		// so the canvas can attach before onMount via ProgramPreview's $effect).
		pipeline.connectSource('program');
		pipeline.connectSource('program-raw');

		// Resume AudioContexts on first user gesture (browser autoplay policy).
		const resumeAudio = () => {
			pipeline.resumeAllAudio();
			pflManager.resumeContext();
			document.removeEventListener('click', resumeAudio);
			document.removeEventListener('keydown', resumeAudio);
		};
		document.addEventListener('click', resumeAudio, { once: true });
		document.addEventListener('keydown', resumeAudio, { once: true });

		// Start audio metering rAF loop
		pipelineManager.startMetering();

		// Load macro list for keyboard shortcuts
		refreshMacros();

		// Attempt operator reconnection from stored token
		if (operatorState.hasStoredToken()) {
			await operatorState.reconnect();
		}

		// Fetch initial state, start polling, and attempt WebTransport connection
		await connectionManager.start();
	});

	onDestroy(() => {
		keyboard.detach();
		document.removeEventListener('keydown', handleDebugDump);
		clearInterval(syncInterval);
		pflManager.destroy();
		pipelineManager.destroy();
		connectionManager.stop();
		pipeline.destroy();
		operatorState.destroy();
		mounted = false;
	});
</script>

<LoadingOverlay loading={initialLoading} error={connectionError} />

{#if tokenRequired}
	<div class="token-prompt">
		<div class="token-box">
			<p>API token required</p>
			<form onsubmit={(e) => { e.preventDefault(); submitToken(); }}>
				<input
					type="password"
					bind:value={tokenInput}
					placeholder="Paste API token"
					autocomplete="off"
				/>
				<button type="submit">Connect</button>
			</form>
		</div>
	</div>
{/if}

{#if showOperatorRegistration}
	<OperatorRegistration onRegistered={() => { showOperatorRegistration = false; }} />
{/if}

<ProgramHealthBanner
	programSource={store.state.programSource}
	status={store.state.sources[store.state.programSource]?.status ?? 'healthy'}
/>

<ErrorBoundary>
	<ConnectionBanner {connectionState} {syncStatus} />
	<Toast />
	{#if layoutMode === 'simple'}
		<SimpleMode
			state={store.effectiveState}
			onSwitchLayout={switchLayout}
			{onCanvasReady}
			onPreview={(key) => { store.optimisticPreview(key); apiCall(setPreview(key), 'Preview failed'); }}
			onCut={() => {
				if (store.state.previewSource) {
					store.optimisticCut(store.state.previewSource);
					apiCall(cut(store.state.previewSource), 'Cut failed');
				}
			}}
			onDissolve={() => {
				if (store.state.previewSource && !store.state.inTransition && !store.state.ftbActive) {
					apiCall(startTransition(store.state.previewSource, 'mix', 1000), 'Dissolve failed');
				}
			}}
		/>
	{:else}
		<div class="control-room">
			<header class="header">
				<div class="header-row">
					<OutputControls state={store.effectiveState} {connectionState} {switchLayout} />
					<div class="header-right">
						<LockIndicator state={store.effectiveState} subsystem="output" />
						<OperatorBadge state={store.effectiveState} />
						{#if !operatorState.isRegistered() && (store.effectiveState.operators?.length ?? 0) > 0}
							<button class="register-btn" onclick={() => { showOperatorRegistration = true; }}>Register</button>
						{:else if !operatorState.isRegistered()}
							<button class="register-btn" onclick={() => { showOperatorRegistration = true; }}>Register Operator</button>
						{/if}
					</div>
				</div>
			</header>

			<section class="monitors">
				<ProgramPreview state={store.effectiveState} {onCanvasReady} />
			</section>

			<section class="multiview-section">
				<Multiview state={store.effectiveState} onLabelChange={handleLabelChange} />
			</section>

			<section class="control-strip">
				<LockIndicator state={store.effectiveState} subsystem="switching" />
				<PreviewBus state={store.effectiveState} onPreview={(key) => { store.optimisticPreview(key); apiCall(setPreview(key), 'Preview failed'); }} />
				<ProgramBus state={store.effectiveState} onCut={(key) => { store.optimisticCut(key); apiCall(cut(key), 'Cut failed'); }} />
				<TransitionControls state={store.effectiveState} pendingConfirm={keyboard.pendingConfirmAction} />
			</section>

			<section class="bottom-panel">
				<BottomTabs>
					{#snippet children(activeTab)}
						{#if activeTab === 'Audio'}
							<div class="tab-panel audio-tab">
								<div class="panel-header">
									<LockIndicator state={store.effectiveState} subsystem="audio" />
								</div>
								<AudioMixer state={store.effectiveState} {sourceLevels} {programLevels} {pflActiveSource} expandedKeys={eqExpandedKeys} onPFLToggle={handlePFLToggle} onStateUpdate={store.applyUpdate} onExpandToggle={(key) => { eqExpandedKeys = { ...eqExpandedKeys, [key]: !eqExpandedKeys[key] }; }} />
							</div>
						{:else if activeTab === 'Graphics'}
							<div class="tab-panel">
								<div class="panel-header">
									<LockIndicator state={store.effectiveState} subsystem="graphics" />
								</div>
								<GraphicsPanel state={store.effectiveState} />
							</div>
						{:else if activeTab === 'Macros'}
							<div class="tab-panel">
								<MacroPanel />
							</div>
						{:else if activeTab === 'Keys'}
							<div class="tab-panel">
								<KeyPanel state={store.effectiveState} />
							</div>
						{:else if activeTab === 'Replay'}
							<div class="tab-panel">
								<div class="panel-header">
									<LockIndicator state={store.effectiveState} subsystem="replay" />
								</div>
								<ReplayPanel state={store.effectiveState} {pipeline} />
							</div>
						{:else if activeTab === 'Presets'}
							<div class="tab-panel">
								<PresetPanel />
							</div>
						{:else if activeTab === 'SCTE-35'}
							<div class="tab-panel">
								{#if store.effectiveState.scte35?.enabled}
									<SCTE35Panel state={store.effectiveState} onStateUpdate={store.applyUpdate} />
								{:else}
									<div class="panel-disabled">SCTE-35 not enabled on server</div>
								{/if}
							</div>
						{/if}
					{/snippet}
				</BottomTabs>
			</section>
		</div>

		{#if showOverlay}
			<KeyboardOverlay onclose={() => showOverlay = false} />
		{/if}
	{/if}
</ErrorBoundary>

<ServerPipelineOverlay />
<div class="sr-only" aria-live="polite" role="status">{announcement}</div>

<style>
	@import '$lib/layout/responsive.css';

	.control-room {
		display: grid;
		grid-template-rows: auto 1fr auto auto auto;
		height: 100vh;
		background: var(--bg-base);
	}

	.header {
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-subtle);
	}

	.monitors {
		background: var(--bg-base);
		min-height: 0;
		overflow: hidden;
	}

	.multiview-section {
		overflow: hidden;
		background: var(--bg-base);
		min-height: 0;
		max-height: clamp(100px, 15vh, 200px);
	}

	.control-strip {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 10px;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-surface);
	}

	.bottom-panel {
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		height: clamp(200px, 30vh, 320px);
	}

	.tab-panel {
		height: 100%;
		overflow-y: auto;
	}

	.tab-panel.audio-tab {
		overflow-x: auto;
		overflow-y: hidden;
	}

	.header-row {
		display: flex;
		align-items: center;
	}

	.header-right {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-left: auto;
		padding: 0 12px;
	}

	.register-btn {
		padding: 4px 10px;
		border: 1px solid var(--border-subtle, #444);
		border-radius: 4px;
		background: transparent;
		color: var(--text-secondary, #aaa);
		font-size: 11px;
		cursor: pointer;
	}

	.register-btn:hover {
		border-color: var(--text-secondary, #888);
		color: var(--text-primary, #eee);
	}

	.panel-header {
		display: flex;
		justify-content: flex-end;
		padding: 2px 4px 0;
		min-height: 0;
	}

	.token-prompt {
		position: fixed;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		background: rgba(0, 0, 0, 0.85);
		z-index: 10000;
	}

	.token-box {
		background: var(--bg-surface, #1e1e1e);
		border: 1px solid var(--border-subtle, #444);
		border-radius: 8px;
		padding: 24px;
		text-align: center;
	}

	.token-box p {
		margin: 0 0 12px;
		color: var(--text-primary, #eee);
		font-size: 14px;
	}

	.token-box form {
		display: flex;
		gap: 8px;
	}

	.token-box input {
		padding: 6px 10px;
		border: 1px solid var(--border-subtle, #555);
		border-radius: 4px;
		background: var(--bg-base, #111);
		color: var(--text-primary, #eee);
		font-family: monospace;
		font-size: 13px;
		width: 320px;
	}

	.token-box button {
		padding: 6px 16px;
		border: none;
		border-radius: 4px;
		background: #2563eb;
		color: #fff;
		font-size: 13px;
		cursor: pointer;
	}

	.token-box button:hover {
		background: #1d4ed8;
	}

	.panel-disabled {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 100%;
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 0.8rem;
	}
</style>
