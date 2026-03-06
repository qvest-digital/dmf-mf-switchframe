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
	import GraphicsPanel from '../components/GraphicsPanel.svelte';
	import MacroPanel from '../components/MacroPanel.svelte';
	import KeyPanel from '../components/KeyPanel.svelte';
	import ReplayPanel from '../components/ReplayPanel.svelte';
	import OperatorRegistration from '../components/OperatorRegistration.svelte';
	import OperatorBadge from '../components/OperatorBadge.svelte';
	import LockIndicator from '../components/LockIndicator.svelte';
	import { createControlRoomStore } from '$lib/state/control-room.svelte';
	import { cut, setPreview, setLabel, startTransition, fadeToBlack, graphicsOn, graphicsOff, apiCall, setAuthToken, SwitchApiError, listMacros, runMacro } from '$lib/api/switch-api';
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
	import type { GraphicsTemplate } from '$lib/graphics/templates';

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

	// Media pipeline for MoQ video/audio decode
	const pipeline = createMediaPipeline();
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

	// Graphics overlay template/values for rendering on program monitor
	let gfxTemplate = $state<GraphicsTemplate | null>(null);
	let gfxValues = $state<Record<string, string>>({});

	function handleGraphicsTemplateChange(template: GraphicsTemplate | null, values: Record<string, string>) {
		gfxTemplate = template;
		gfxValues = values;
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
			const resp = await fetch('/api/debug/snapshot');
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
	$effect(() => {
		const _program = store.state.programSource;
		const _preview = store.state.previewSource;
		const _pgmCanvas = programCanvas;
		const _pvwCanvas = previewCanvas;
		if (!mounted) return;
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

	onMount(async () => {
		keyboard.attach();
		document.addEventListener('keydown', handleDebugDump);
		mounted = true;
		syncInterval = setInterval(() => { now = Date.now(); }, 1000);

		// Subscribe to "program" MoQ stream so the program canvas shows
		// the actual server output (including transition blends).
		pipeline.setSourceMuted('program', false);
		pipeline.addSource('program');
		pipeline.connectSource('program');

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
				<ProgramPreview state={store.effectiveState} {onCanvasReady} graphicsTemplate={gfxTemplate} graphicsValues={gfxValues} />
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
				<div class="audio-section">
					<div class="panel-header">
						<LockIndicator state={store.effectiveState} subsystem="audio" />
					</div>
					<AudioMixer state={store.effectiveState} {sourceLevels} {programLevels} {pflActiveSource} onPFLToggle={handlePFLToggle} onStateUpdate={store.applyUpdate} />
				</div>
				<div class="graphics-section">
					<div class="panel-header">
						<LockIndicator state={store.effectiveState} subsystem="graphics" />
					</div>
					<GraphicsPanel state={store.effectiveState} onTemplateChange={handleGraphicsTemplateChange} />
				</div>
				<div class="macro-section">
					<MacroPanel />
				</div>
				<div class="key-section">
					<KeyPanel state={store.effectiveState} />
				</div>
				<div class="replay-section">
					<div class="panel-header">
						<LockIndicator state={store.effectiveState} subsystem="replay" />
					</div>
					<ReplayPanel state={store.effectiveState} {pipeline} />
				</div>
			</section>
		</div>

		{#if showOverlay}
			<KeyboardOverlay onclose={() => showOverlay = false} />
		{/if}
	{/if}
</ErrorBoundary>

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
		max-height: 100px;
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
		display: flex;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		height: clamp(160px, 25vh, 220px);
	}

	.audio-section {
		overflow-x: auto;
		overflow-y: hidden;
		border-right: 1px solid var(--border-subtle);
		flex: 2;
		min-width: 280px;
	}

	.graphics-section {
		flex: 1.5;
		min-width: 0;
		overflow-y: auto;
		border-left: 1px solid var(--border-subtle);
		padding: 4px;
	}

	.macro-section {
		flex: 1;
		min-width: 0;
		overflow-y: auto;
		border-left: 1px solid var(--border-subtle);
	}

	.key-section {
		flex: 1.5;
		min-width: 0;
		overflow-y: auto;
		border-left: 1px solid var(--border-subtle);
	}

	.replay-section {
		flex: 1.5;
		min-width: 0;
		overflow-y: auto;
		border-left: 1px solid var(--border-subtle);
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
</style>
