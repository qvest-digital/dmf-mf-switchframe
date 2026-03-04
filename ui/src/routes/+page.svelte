<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import ProgramPreview from '../components/ProgramPreview.svelte';
	import Multiview from '../components/Multiview.svelte';
	import PreviewBus from '../components/PreviewBus.svelte';
	import ProgramBus from '../components/ProgramBus.svelte';
	import TransitionControls from '../components/TransitionControls.svelte';
	import AudioMixer from '../components/AudioMixer.svelte';
	import OutputControls from '../components/OutputControls.svelte';
	import KeyboardOverlay from '../components/KeyboardOverlay.svelte';
	import { createControlRoomStore } from '$lib/state/control-room.svelte';
	import { cut, setPreview, getState, startTransition, fadeToBlack, fireAndForget } from '$lib/api/switch-api';
	import { KeyboardHandler } from '$lib/keyboard/handler';
	import { createPrismConnection } from '$lib/transport/connection';

	const store = createControlRoomStore();
	let showOverlay = $state(false);
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
		stopPolling();
		connection.disconnect();
	});
</script>

<div class="control-room">
	<header class="header">
		<OutputControls state={store.state} />
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
