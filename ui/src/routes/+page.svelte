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
	import SimpleMode from '../components/SimpleMode.svelte';
	import { createControlRoomStore } from '$lib/state/control-room.svelte';
	import { cut, setPreview, getState, startTransition, fadeToBlack, fireAndForget } from '$lib/api/switch-api';
	import { KeyboardHandler } from '$lib/keyboard/handler';
	import { createPrismConnection } from '$lib/transport/connection';
	import { createMediaPipeline } from '$lib/transport/media-pipeline';
	import { getLayoutMode, setLayoutMode, type LayoutMode } from '$lib/layout/preferences';

	const store = createControlRoomStore();
	let showOverlay = $state(false);
	let layoutMode = $state<LayoutMode>(getLayoutMode());
	let mounted = $state(false);
	let connectionState = $state<'webtransport' | 'polling' | 'disconnected'>('disconnected');

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

	// Per-source audio levels sampled from media pipeline decoders (linear 0..1)
	let sourceLevels = $state<Record<string, { peakL: number; peakR: number }>>({});
	// Program output peak levels sampled from program audio decoder (linear 0..1)
	let programLevels = $state<{ peakL: number; peakR: number }>({ peakL: 0, peakR: 0 });
	let meterRafId: number | undefined;

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

	/** Poll audio decoders for per-source peak levels at display refresh rate. */
	function meterLoop() {
		const levels: Record<string, { peakL: number; peakR: number }> = {};
		for (const key of store.sourceKeys) {
			const decoder = pipeline.getAudioDecoder(key);
			if (decoder) {
				const l = decoder.getLevels();
				levels[key] = { peakL: l.peak[0] ?? 0, peakR: l.peak[1] ?? 0 };
			}
		}
		sourceLevels = levels;

		// Sample program output peak from program audio decoder
		const programDecoder = pipeline.getAudioDecoder('program');
		if (programDecoder) {
			const pl = programDecoder.getLevels();
			programLevels = { peakL: pl.peak[0] ?? 0, peakR: pl.peak[1] ?? 0 };
		}

		meterRafId = requestAnimationFrame(meterLoop);
	}

	// Track which sources are connected to avoid duplicate work
	let connectedSources = new Set<string>();
	// Track which canvases are attached
	let attachedCanvases = new Set<string>();
	// Track program/preview canvas bindings
	let currentProgramCanvas: string | null = null;
	let currentPreviewCanvas: string | null = null;

	/**
	 * Sync media pipeline sources with control room state.
	 * Called when the source list changes: adds new sources, removes stale ones,
	 * and attaches canvases once DOM elements are available.
	 */
	async function syncSources() {
		if (!mounted) return;

		const stateSourceKeys = Object.keys(store.state.sources).sort();
		const pipelineSources = connectedSources;

		// Add new sources
		for (const key of stateSourceKeys) {
			if (!pipelineSources.has(key)) {
				pipeline.addSource(key);
				pipeline.connectSource(key);
				connectedSources.add(key);
			}
		}

		// Remove stale sources
		for (const key of pipelineSources) {
			if (!store.state.sources[key]) {
				pipeline.removeSource(key);
				connectedSources.delete(key);
				attachedCanvases.delete(key);
			}
		}

		// Wait for DOM to update after source list change
		await tick();

		// Attach multiview tile canvases
		for (const key of stateSourceKeys) {
			if (!attachedCanvases.has(key)) {
				const canvas = document.getElementById(`tile-${key}`) as HTMLCanvasElement | null;
				if (canvas) {
					pipeline.attachCanvas(key, `tile-${key}`, canvas);
					attachedCanvases.add(key);
				}
			}
		}

		// Attach program/preview canvases based on current assignments
		syncProgramPreviewCanvases();
	}

	/**
	 * Update which source is rendered on the program and preview canvases.
	 *
	 * Program canvas: always renders the "program" MoQ stream, which is the
	 * authoritative server output. During transitions, this shows the blended
	 * dissolve/dip/FTB frames. During normal operation, it shows the program
	 * source's passthrough video. Attached once and stays until layout change.
	 *
	 * Preview canvas: renders the preview source's individual stream so you
	 * see the raw video of whatever source you're about to cut/transition to.
	 */
	function syncProgramPreviewCanvases() {
		if (!mounted) return;

		const previewSource = store.state.previewSource;

		// Program canvas: render the "program" MoQ stream (shows transitions)
		if (currentProgramCanvas !== 'program') {
			if (currentProgramCanvas) {
				pipeline.detachCanvas(currentProgramCanvas, 'program');
			}
			const programCanvas = document.getElementById('program-video') as HTMLCanvasElement | null;
			if (programCanvas) {
				pipeline.attachCanvas('program', 'program', programCanvas);
				currentProgramCanvas = 'program';
			}
		}

		// Preview canvas: render the preview source's video
		if (previewSource !== currentPreviewCanvas) {
			// Detach old preview renderer from previous source
			if (currentPreviewCanvas) {
				pipeline.detachCanvas(currentPreviewCanvas, 'preview');
			}
			const previewCanvas = document.getElementById('preview-video') as HTMLCanvasElement | null;
			if (previewCanvas && previewSource) {
				pipeline.attachCanvas(previewSource, 'preview', previewCanvas);
			}
			currentPreviewCanvas = previewSource;
		}
	}

	// React to source list changes
	$effect(() => {
		// Access sourceKeys to create the reactive dependency
		const _keys = store.sourceKeys;
		syncSources();
	});

	// React to program/preview changes
	$effect(() => {
		const _program = store.state.programSource;
		const _preview = store.state.previewSource;
		syncProgramPreviewCanvases();
	});

	// Re-attach canvases when layout mode changes (DOM is replaced)
	$effect(() => {
		const _mode = layoutMode;
		// Detach all current renderers — their canvases are about to be destroyed
		for (const key of attachedCanvases) {
			pipeline.detachCanvas(key, `tile-${key}`);
		}
		if (currentProgramCanvas) {
			pipeline.detachCanvas(currentProgramCanvas, 'program');
		}
		if (currentPreviewCanvas) {
			pipeline.detachCanvas(currentPreviewCanvas, 'preview');
		}
		attachedCanvases = new Set<string>();
		currentProgramCanvas = null;
		currentPreviewCanvas = null;
		// Re-sync after DOM updates
		tick().then(() => syncSources());
	});

	let pollInterval: ReturnType<typeof setInterval> | undefined;

	function startPolling() {
		if (pollInterval) return;
		pollInterval = setInterval(async () => {
			try {
				const state = await getState();
				store.applyUpdate(state);
			} catch { /* ignore */ }
		}, 500);
		// Reflect polling state (WebTransport callbacks will override to 'webtransport' if it connects)
		if (connectionState !== 'webtransport') {
			connectionState = 'polling';
		}
	}

	function stopPolling() {
		if (pollInterval) {
			clearInterval(pollInterval);
			pollInterval = undefined;
		}
	}

	const connection = createPrismConnection({
		url: window.location.origin,
		onControlState: (data) => {
			store.applyFromMoQ(data);
			// MoQ is delivering state; stop polling
			stopPolling();
			connectionState = 'webtransport';
		},
		onConnectionChange: (connState) => {
			if (connState === 'connected') {
				connectionState = 'webtransport';
			} else if (connState === 'disconnected' || connState === 'error') {
				// WebTransport lost; fall back to REST polling
				connectionState = pollInterval ? 'polling' : 'disconnected';
				startPolling();
			}
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

		// Initial state fetch via REST
		try {
			const state = await getState();
			store.applyUpdate(state);
		} catch (e) {
			console.warn('Failed to fetch initial state:', e);
		}

		// Start audio metering rAF loop
		meterRafId = requestAnimationFrame(meterLoop);

		// Start REST polling as immediate fallback
		startPolling();

		// Attempt WebTransport connection (will replace polling on success)
		connection.connect();
	});

	onDestroy(() => {
		keyboard.detach();
		document.removeEventListener('keydown', handleDebugDump);
		if (meterRafId !== undefined) cancelAnimationFrame(meterRafId);
		stopPolling();
		connection.disconnect();
		pipeline.destroy();
		mounted = false;
	});
</script>

{#if layoutMode === 'simple'}
	<SimpleMode state={store.state} onSwitchLayout={switchLayout} />
{:else}
	<div class="control-room">
		<header class="header">
			<OutputControls state={store.state} {connectionState} {switchLayout} />
		</header>

		<section class="monitors">
			<ProgramPreview state={store.state} />
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
