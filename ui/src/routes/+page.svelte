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
	import SimpleMode from '../components/SimpleMode.svelte';
	import ErrorBoundary from '../components/ErrorBoundary.svelte';
	import { createControlRoomStore } from '$lib/state/control-room.svelte';
	import { cut, setPreview, startTransition, fadeToBlack, fireAndForget } from '$lib/api/switch-api';
	import { KeyboardHandler } from '$lib/keyboard/handler';
	import { ConnectionManager } from '$lib/transport/connection-manager';
	import { createMediaPipeline } from '$lib/transport/media-pipeline';
	import { PipelineManager } from '$lib/pipeline/manager';
	import { getLayoutMode, setLayoutMode, type LayoutMode } from '$lib/layout/preferences';
	import type { ControlRoomState } from '$lib/api/types';

	const store = createControlRoomStore();
	let showOverlay = $state(false);
	let layoutMode = $state<LayoutMode>(getLayoutMode());
	let mounted = $state(false);
	let connectionState = $state<'webtransport' | 'polling' | 'disconnected'>('disconnected');
	let initialLoading = $state(true);
	let connectionError: string | null = $state(null);

	// ARIA live region for screen reader announcements
	let announcement = $state('');
	let announcementTimer: ReturnType<typeof setTimeout> | undefined;

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
			if (store.state.previewSource) fireAndForget(cut(store.state.previewSource));
		},
		onSetPreview: (key) => fireAndForget(setPreview(key)),
		onHotPunch: (key) => fireAndForget(cut(key)),
		onAutoTransition: () => {
			if (store.state.previewSource && !store.state.inTransition && !store.state.ftbActive) {
				fireAndForget(startTransition(store.state.previewSource, transitionType, transitionDuration));
			}
		},
		onFadeToBlack: () => {
			if (!store.state.inTransition || store.state.ftbActive) {
				fireAndForget(fadeToBlack());
			}
		},
		onToggleFullscreen: () => {
			document.fullscreenElement
				? document.exitFullscreen()
				: document.documentElement.requestFullscreen();
		},
		onToggleOverlay: () => { showOverlay = !showOverlay; },
		onSetTransitionType: (type) => {
			if (type === 'mix' || type === 'dip') {
				transitionType = type;
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
			flashMessage('Debug snapshot copied to clipboard');
		} catch {
			console.log('=== SWITCHFRAME DEBUG SNAPSHOT ===');
			console.log(json);
			flashMessage('Debug snapshot logged to console');
		}
	}

	function flashMessage(msg: string) {
		const badge = document.createElement('div');
		Object.assign(badge.style, {
			position: 'fixed', bottom: '20px', left: '50%',
			transform: 'translateX(-50%)', background: 'rgba(0,200,100,0.9)',
			color: '#fff', padding: '8px 20px', borderRadius: '6px',
			fontFamily: "'SF Mono', monospace", fontSize: '13px',
			zIndex: '99999', transition: 'opacity 0.5s',
		});
		badge.textContent = msg;
		document.body.appendChild(badge);
		setTimeout(() => { badge.style.opacity = '0'; }, 1500);
		setTimeout(() => badge.remove(), 2000);
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

	// Re-attach canvases when layout mode changes (DOM is replaced)
	$effect(() => {
		const _mode = layoutMode;
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
		onInitialLoadError: (error) => {
			console.warn('Failed to fetch initial state:', error);
			connectionError = error;
		},
	});

	onMount(async () => {
		keyboard.attach();
		document.addEventListener('keydown', handleDebugDump);
		mounted = true;

		// Subscribe to "program" MoQ stream so the program canvas shows
		// the actual server output (including transition blends).
		pipeline.setSourceMuted('program', false);
		pipeline.addSource('program');
		pipeline.connectSource('program');

		// Resume AudioContexts on first user gesture (browser autoplay policy).
		const resumeAudio = () => {
			pipeline.resumeAllAudio();
			document.removeEventListener('click', resumeAudio);
			document.removeEventListener('keydown', resumeAudio);
		};
		document.addEventListener('click', resumeAudio, { once: true });
		document.addEventListener('keydown', resumeAudio, { once: true });

		// Start audio metering rAF loop
		pipelineManager.startMetering();

		// Fetch initial state, start polling, and attempt WebTransport connection
		await connectionManager.start();
	});

	onDestroy(() => {
		keyboard.detach();
		document.removeEventListener('keydown', handleDebugDump);
		pipelineManager.destroy();
		connectionManager.stop();
		pipeline.destroy();
		mounted = false;
	});
</script>

<LoadingOverlay loading={initialLoading} error={connectionError} />

<ErrorBoundary>
	{#if layoutMode === 'simple'}
		<SimpleMode state={store.state} onSwitchLayout={switchLayout} {onCanvasReady} />
	{:else}
		<div class="control-room">
			<header class="header">
				<OutputControls state={store.state} {connectionState} {switchLayout} />
			</header>

			<section class="monitors">
				<ProgramPreview state={store.state} {onCanvasReady} />
			</section>

			<section class="multiview-section">
				<Multiview state={store.state} />
			</section>

			<section class="bottom-panel">
				<div class="audio-section">
					<AudioMixer state={store.state} {sourceLevels} {programLevels} onStateUpdate={store.applyUpdate} />
				</div>
				<div class="control-section">
					<div class="buses">
						<PreviewBus state={store.state} />
						<ProgramBus state={store.state} />
					</div>
					<TransitionControls state={store.state} />
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
	.control-room {
		display: grid;
		grid-template-rows: auto auto 1fr auto;
		height: 100vh;
		background: var(--bg-base);
	}

	.header {
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-subtle);
	}

	.monitors {
		background: var(--bg-base);
	}

	.multiview-section {
		overflow: hidden;
		background: var(--bg-base);
		min-height: 0;
	}

	.bottom-panel {
		display: flex;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		max-height: 240px;
	}

	.audio-section {
		overflow-x: auto;
		overflow-y: hidden;
		border-right: 1px solid var(--border-subtle);
		flex-shrink: 0;
	}

	.control-section {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
	}

	.buses {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}
</style>
