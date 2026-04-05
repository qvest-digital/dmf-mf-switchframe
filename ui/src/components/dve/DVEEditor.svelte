<script lang="ts">
	import { onMount } from 'svelte';
	import type { ControlRoomState, DVEPresetInfo } from '$lib/api/types';
	import type { FastControl } from '$lib/transport/fast-control';
	import PresetStrip from './PresetStrip.svelte';
	import SlotList from './SlotList.svelte';
	import SlotInspector from './SlotInspector.svelte';
	import TransitionEditor from './TransitionEditor.svelte';
	import {
		setDVELayout, deleteDVELayout,
		updateDVESlot, dveSlotOn, dveSlotOff,
		listDVEPresets, saveDVEPreset, deleteDVEPreset,
		recallDVEPreset,
		apiCall,
	} from '$lib/api/switch-api';

	interface Props {
		state: ControlRoomState;
		fastControl?: FastControl | null;
	}

	let { state: crState, fastControl = null }: Props = $props();

	// ── Local state ──
	let selectedSlotId = $state<number | null>(null);
	let presets = $state<DVEPresetInfo[]>([]);

	// ── Derived state from ControlRoomState ──
	let layoutState = $derived(crState.layout);
	let slots = $derived(layoutState?.slots ?? []);
	let activePreset = $derived(layoutState?.activePreset ?? '');

	let selectedSlot = $derived(
		selectedSlotId != null ? slots.find((s) => s.id === selectedSlotId) ?? null : null,
	);

	// Auto-select first slot if current selection disappears
	$effect(() => {
		const ids = new Set(slots.map((s) => s.id));
		if (selectedSlotId != null && !ids.has(selectedSlotId)) {
			selectedSlotId = slots.length > 0 ? slots[0].id : null;
		}
	});

	// Auto-select first slot when layout activates
	$effect(() => {
		if (selectedSlotId == null && slots.length > 0) {
			selectedSlotId = slots[0].id;
		}
	});

	// ── Lifecycle ──
	onMount(() => {
		listDVEPresets()
			.then((p) => {
				presets = p;
			})
			.catch(() => {});
	});

	// ── PresetStrip callbacks ──
	function handleRecallPreset(id: string, durationMs: number) {
		if (durationMs <= 0) {
			apiCall(setDVELayout({ preset: id }), 'DVE preset');
		} else {
			apiCall(recallDVEPreset(id, durationMs), 'DVE preset recall');
		}
	}

	async function handleSavePreset(name: string) {
		await saveDVEPreset(name);
		presets = await listDVEPresets();
	}

	async function handleDeletePreset(id: string) {
		await deleteDVEPreset(id);
		presets = await listDVEPresets();
	}

	function handleClear() {
		apiCall(deleteDVELayout(), 'Clear DVE');
	}

	// ── SlotList callbacks ──
	function handleSlotSelect(id: number) {
		selectedSlotId = id;
	}

	function handleSlotToggle(id: number) {
		const slot = slots.find((s) => s.id === id);
		if (!slot) return;
		if (slot.enabled) {
			apiCall(dveSlotOff(id), 'DVE slot off');
		} else {
			apiCall(dveSlotOn(id), 'DVE slot on');
		}
	}

	// ── SlotInspector callbacks ──
	function handleSlotUpdate(slotId: number, update: Record<string, unknown>) {
		apiCall(updateDVESlot(slotId, update), 'DVE update');
	}

	function handleSourceChange(slotId: number, source: string) {
		apiCall(updateDVESlot(slotId, { sourceKey: source }), 'DVE source');
	}

	function handleScaleModeChange(slotId: number, mode: string) {
		apiCall(updateDVESlot(slotId, { scaleMode: mode }), 'DVE scale mode');
	}

	function handleCropAnchorChange(slotId: number, anchor: [number, number]) {
		apiCall(updateDVESlot(slotId, { cropAnchor: anchor }), 'DVE crop anchor');
	}

	// ── TransitionEditor callback ──
	function handleTransitionRecall(presetId: string, durationMs: number, easing: string) {
		apiCall(recallDVEPreset(presetId, durationMs, easing), 'DVE transition');
	}
</script>

<div class="dve-editor">
	<!-- Zone 1: Preset strip -->
	<PresetStrip
		{activePreset}
		userPresets={presets}
		onRecall={handleRecallPreset}
		onSave={handleSavePreset}
		onDelete={handleDeletePreset}
		onClear={handleClear}
	/>

	<!-- Zone 2: Slot list + Inspector -->
	{#if slots.length === 0}
		<div class="empty-state">
			<span>Select a preset to start compositing</span>
		</div>
	{:else}
		<div class="main-area">
			<div class="slot-list-pane">
				<SlotList
					{slots}
					sources={crState.sources}
					{selectedSlotId}
					programSource={crState.programSource}
					previewSource={crState.previewSource}
					onSelect={handleSlotSelect}
					onToggle={handleSlotToggle}
				/>
			</div>

			<div class="inspector-pane">
				{#if selectedSlot}
					<SlotInspector
						slot={selectedSlot}
						sources={crState.sources}
						{fastControl}
						onUpdate={handleSlotUpdate}
						onSourceChange={handleSourceChange}
						onScaleModeChange={handleScaleModeChange}
						onCropAnchorChange={handleCropAnchorChange}
					/>
				{:else}
					<div class="inspector-empty">
						<span>Click a slot to inspect</span>
					</div>
				{/if}
			</div>
		</div>
	{/if}

	<!-- Zone 3: Transition editor (collapsible) -->
	<TransitionEditor
		currentLayout={layoutState ?? null}
		onRecallPreset={handleTransitionRecall}
	/>
</div>

<style>
	.dve-editor {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
	}

	/* Zone 2: empty state */
	.empty-state {
		flex: 1;
		display: flex;
		align-items: center;
		justify-content: center;
		color: var(--text-tertiary);
		font-size: 11px;
		font-family: var(--font-ui);
		min-height: 0;
	}

	/* Zone 2: main area split */
	.main-area {
		flex: 1;
		display: flex;
		min-height: 0;
	}

	.slot-list-pane {
		flex-shrink: 0;
		width: 120px;
		border-right: 1px solid var(--border-subtle);
		overflow-y: auto;
	}

	.inspector-pane {
		flex: 1;
		min-width: 0;
		overflow-y: auto;
	}

	.inspector-empty {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 100%;
		color: var(--text-tertiary);
		font-size: 11px;
		font-family: var(--font-ui);
	}
</style>
