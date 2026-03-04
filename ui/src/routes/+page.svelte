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
	 * When the program/preview source changes, we detach the old renderer
	 * and attach the new source to the program/preview canvas. Each source
	 * can have multiple renderers (tile + program/preview) simultaneously.
	 */
	function syncProgramPreviewCanvases() {
		if (!mounted) return;

		const programSource = store.state.programSource;
		const previewSource = store.state.previewSource;

		// Program canvas: render the program source's video
		if (programSource !== currentProgramCanvas) {
			// Detach old program renderer from previous source
			if (currentProgramCanvas) {
				pipeline.detachCanvas(currentProgramCanvas, 'program');
			}
			const programCanvas = document.getElementById('program-video') as HTMLCanvasElement | null;
			if (programCanvas && programSource) {
				pipeline.attachCanvas(programSource, 'program', programCanvas);
			}
			currentProgramCanvas = programSource;
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

	let pollInterval: ReturnType<typeof setInterval> | undefined;

	function startPolling() {
		if (pollInterval) return;
		pollInterval = setInterval(async () => {
			try {
				const state = await getState();
				store.applyUpdate(state);
			} catch { /* ignore */ }
		}, 500);
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
		},
		onConnectionChange: (connectionState) => {
			if (connectionState === 'disconnected' || connectionState === 'error') {
				// WebTransport lost; fall back to REST polling
				startPolling();
			}
		},
	});

	onMount(async () => {
		keyboard.attach();
		document.addEventListener('keydown', handleDebugDump);
		mounted = true;

		// Initial state fetch via REST
		try {
			const state = await getState();
			store.applyUpdate(state);
		} catch (e) {
			console.warn('Failed to fetch initial state:', e);
		}

		// Start REST polling as immediate fallback
		startPolling();

		// Attempt WebTransport connection (will replace polling on success)
		connection.connect();
	});

	onDestroy(() => {
		keyboard.detach();
		document.removeEventListener('keydown', handleDebugDump);
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
			<OutputControls state={store.state} {switchLayout} />
		</header>

		<section class="top">
			<ProgramPreview state={store.state} />
		</section>

		<section class="multiview-section">
			<Multiview state={store.state} />
		</section>

		<section class="audio-section">
			<AudioMixer state={store.state} />
		</section>

		<section class="controls">
			<PreviewBus state={store.state} />
			<ProgramBus state={store.state} />
			<TransitionControls state={store.state} />
		</section>
	</div>

	{#if showOverlay}
		<KeyboardOverlay onclose={() => showOverlay = false} />
	{/if}
{/if}

<style>
	.control-room {
		display: grid;
		grid-template-rows: auto auto 1fr auto auto;
		height: 100vh;
		background: var(--bg-primary);
	}
	.header { border-bottom: 1px solid #333; background: var(--bg-secondary); }
	.top { border-bottom: 1px solid #333; }
	.multiview-section { overflow: hidden; }
	.audio-section { border-top: 1px solid #333; max-height: 200px; overflow-y: auto; }
	.controls { border-top: 1px solid #333; background: var(--bg-secondary); }
</style>
