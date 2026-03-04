<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import ProgramPreview from '../components/ProgramPreview.svelte';
	import Multiview from '../components/Multiview.svelte';
	import PreviewBus from '../components/PreviewBus.svelte';
	import ProgramBus from '../components/ProgramBus.svelte';
	import TransitionControls from '../components/TransitionControls.svelte';
	import KeyboardOverlay from '../components/KeyboardOverlay.svelte';
	import { createControlRoomStore } from '$lib/state/control-room.svelte';
	import { cut, setPreview, getState } from '$lib/api/switch-api';
	import { KeyboardHandler } from '$lib/keyboard/handler';

	const store = createControlRoomStore();
	let showOverlay = $state(false);

	const keyboard = new KeyboardHandler({
		onCut: () => {
			if (store.state.previewSource) cut(store.state.previewSource);
		},
		onSetPreview: (key) => setPreview(key),
		onHotPunch: (key) => cut(key),
		onAutoTransition: () => {},
		onFadeToBlack: () => {},
		onToggleFullscreen: () => {
			document.fullscreenElement
				? document.exitFullscreen()
				: document.documentElement.requestFullscreen();
		},
		onToggleOverlay: () => { showOverlay = !showOverlay; },
		getSourceKeys: () => store.sourceKeys,
	});

	let pollInterval: ReturnType<typeof setInterval>;

	onMount(async () => {
		keyboard.attach();

		// Initial state fetch
		try {
			const state = await getState();
			store.applyUpdate(state);
		} catch (e) {
			console.warn('Failed to fetch initial state:', e);
		}

		// Poll REST every 500ms (will be replaced by MoQ in Task 18)
		pollInterval = setInterval(async () => {
			try {
				const state = await getState();
				store.applyUpdate(state);
			} catch { /* ignore */ }
		}, 500);
	});

	onDestroy(() => {
		keyboard.detach();
		if (pollInterval) clearInterval(pollInterval);
	});
</script>

<div class="control-room">
	<section class="top">
		<ProgramPreview state={store.state} />
	</section>

	<section class="multiview-section">
		<Multiview state={store.state} />
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
		grid-template-rows: auto 1fr auto;
		height: 100vh;
		background: var(--bg-primary);
	}
	.top { border-bottom: 1px solid #333; }
	.multiview-section { overflow: hidden; }
	.controls { border-top: 1px solid #333; background: var(--bg-secondary); }
</style>
