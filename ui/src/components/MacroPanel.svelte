<script lang="ts">
	import { holdReload } from '$lib/util/activity';
	import type { Macro, MacroStep, MacroAction, MacroStepStatus, MacroStepState, ControlRoomState } from '$lib/api/types';
	import { listMacros, saveMacro, deleteMacro, runMacro, cancelMacro, dismissMacro, listStingers, listPresets } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import MacroStepEditor from './MacroStepEditor.svelte';
	import { ACTION_META, CATEGORIES } from './macro-actions';
	import {
		ICON_PLAY,
		ICON_PENCIL,
		ICON_TRASH,
		ICON_GRIP,
		ICON_PLUS,
		ICON_CHECK,
		ICON_CLOSE,
		ICON_CHEVRON_DOWN,
		ICON_CHEVRON_RIGHT,
		ICON_HELP,
		ICON_BOLT,
		renderIcon,
	} from '$lib/icons';

	interface Props {
		state: ControlRoomState;
	}
	let { state: crState }: Props = $props();

	// --- State ---
	let macros = $state<Macro[]>([]);
	let runningMacro = $state<string | null>(null);
	let editingSteps = $state<MacroStep[]>([]);
	let editingName = $state('');
	let editMode = $state(false);
	// An open editor holds a macro not saved yet.
	$effect(() => { if (editMode) return holdReload('unsaved macro'); });
	let editorError = $state('');
	let expandedStep = $state<number>(0);
	let showPicker = $state(false);
	let showHelpPopover = $state(false);
	let stingerNames = $state<string[]>([]);
	let presetList = $state<{ id: string; name: string }[]>([]);
	let deleteConfirmName = $state<string | null>(null);
	let dragStepIndex = $state<number | null>(null);

	let sourceKeys = $derived(Object.keys(crState.sources).sort());

	let macroExecution = $derived(crState.macro);
	let isExecuting = $derived(macroExecution != null);
	let isRunning = $derived(macroExecution?.running ?? false);

	// Client-side timer for wait step progress bars
	let now = $state(Date.now());

	$effect(() => {
		if (isRunning) {
			const interval = setInterval(() => { now = Date.now(); }, 50);
			return () => clearInterval(interval);
		}
	});

	function waitProgress(step: MacroStepState): number {
		if (!step.waitStartMs || !step.waitMs || step.waitMs <= 0) return 0;
		const elapsed = now - step.waitStartMs;
		return Math.min(1, Math.max(0, elapsed / step.waitMs));
	}

	function waitElapsed(step: MacroStepState): string {
		if (!step.waitStartMs || !step.waitMs) return '';
		const elapsed = Math.min(now - step.waitStartMs, step.waitMs);
		return `${(elapsed / 1000).toFixed(1)}s / ${(step.waitMs / 1000).toFixed(1)}s`;
	}

	function statusClass(status: MacroStepStatus): string {
		switch (status) {
			case 'done': return 'step-done';
			case 'running': return 'step-running';
			case 'failed': return 'step-failed';
			case 'skipped': return 'step-skipped';
			default: return 'step-pending';
		}
	}

	function sourceLabel(key: string): string {
		return crState.sources[key]?.label || key;
	}

	// --- Validation warnings ---
	let editorWarnings = $derived.by(() => {
		const warnings: string[] = [];
		for (let i = 1; i < editingSteps.length; i++) {
			if (editingSteps[i].action === 'transition' && editingSteps[i - 1].action === 'transition') {
				warnings.push(`Steps ${i} and ${i + 1}: consecutive transitions without a wait`);
			}
		}
		return warnings;
	});

	function toggleHelpPopover() {
		showHelpPopover = !showHelpPopover;
	}

	function closeHelpPopover() {
		showHelpPopover = false;
	}

	// --- CRUD ---
	async function loadMacros() {
		try {
			macros = await listMacros();
		} catch {
			// ignore
		}
	}

	async function handleRun(name: string) {
		runningMacro = name;
		try {
			await runMacro(name);
		} catch (err) {
			notify('error', `Macro "${name}" failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		} finally {
			runningMacro = null;
		}
	}

	async function loadEditorData() {
		try { stingerNames = await listStingers(); } catch { stingerNames = []; }
		try {
			const presets = await listPresets();
			presetList = presets.map(p => ({ id: p.id, name: p.name }));
		} catch { presetList = []; }
	}

	function startEdit(m?: Macro) {
		if (m) {
			editingName = m.name;
			editingSteps = m.steps.map(s => ({ action: s.action, params: { ...s.params } }));
		} else {
			editingName = '';
			editingSteps = [{ action: 'cut', params: { source: sourceKeys[0] ?? '' } }];
		}
		editMode = true;
		editorError = '';
		expandedStep = 0;
		showPicker = false;
		deleteConfirmName = null;
		loadEditorData();
	}

	async function handleSave() {
		editorError = '';
		if (!editingName.trim()) {
			editorError = 'Name is required';
			return;
		}
		if (editingSteps.length === 0) {
			editorError = 'At least one step is required';
			return;
		}
		try {
			await saveMacro({ name: editingName.trim(), steps: editingSteps });
			editMode = false;
			await loadMacros();
			notify('info', `Macro "${editingName}" saved`);
		} catch (err) {
			editorError = err instanceof Error ? err.message : 'Save failed';
		}
	}

	function requestDelete(name: string) {
		deleteConfirmName = deleteConfirmName === name ? null : name;
	}

	function cancelDeleteConfirm() {
		deleteConfirmName = null;
	}

	async function confirmDelete(name: string) {
		deleteConfirmName = null;
		try {
			await deleteMacro(name);
			await loadMacros();
			notify('info', `Macro "${name}" deleted`);
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown error'}`);
		}
	}

	function cancelEdit() {
		editMode = false;
		editorError = '';
		showPicker = false;
	}

	// --- Step manipulation ---
	function addStep(action: MacroAction) {
		const params: Record<string, unknown> = {};
		const sourceActions: MacroAction[] = [
			'cut', 'preview', 'transition', 'set_audio',
			'audio_mute', 'audio_afv', 'audio_trim',
			'audio_eq', 'audio_compressor', 'audio_delay',
			'key_set', 'key_delete', 'source_label',
			'source_delay', 'source_position',
			'replay_mark_in', 'replay_mark_out', 'replay_play',
			'replay_quick_clip', 'replay_play_clip',
		];
		if (sourceActions.includes(action)) {
			params.source = sourceKeys[0] ?? '';
		}
		if (action === 'transition') {
			params.type = 'mix';
			params.durationMs = 1000;
		}
		if (action === 'wait') params.ms = 500;
		if (action === 'set_audio') params.level = 0;
		if (action === 'audio_mute') params.muted = true;
		if (action === 'audio_afv') params.afv = true;
		if (action === 'audio_trim') params.trim = 0;
		if (action === 'audio_master') params.level = 0;
		if (action === 'audio_delay') params.delayMs = 0;
		if (action === 'source_delay') params.delayMs = 0;
		if (action === 'source_position') params.position = 1;
		if (action === 'replay_play') { params.speed = 0.5; params.loop = false; }
		if (action === 'replay_quick_clip') { params.durationSecs = 10; params.speed = 0.5; }
		if (action === 'scte35_cue') { params.durationMs = 30000; params.autoReturn = true; }
		if (['scte35_return', 'scte35_cancel', 'scte35_hold'].includes(action)) params.eventId = 0;
		if (action === 'scte35_extend') { params.eventId = 0; params.durationMs = 30000; }
		editingSteps = [...editingSteps, { action, params }];
		expandedStep = editingSteps.length - 1;
		showPicker = false;
	}

	function removeStep(index: number) {
		editingSteps = editingSteps.filter((_, i) => i !== index);
		if (expandedStep >= editingSteps.length) {
			expandedStep = Math.max(0, editingSteps.length - 1);
		}
	}

	function reorderSteps(fromIndex: number, toIndex: number) {
		if (fromIndex === toIndex || fromIndex < 0 || toIndex < 0) return;
		if (fromIndex >= editingSteps.length || toIndex >= editingSteps.length) return;
		const expandedObj =
			expandedStep >= 0 && expandedStep < editingSteps.length ? editingSteps[expandedStep] : null;
		const copy = [...editingSteps];
		const [removed] = copy.splice(fromIndex, 1);
		copy.splice(toIndex, 0, removed);
		editingSteps = copy;
		if (expandedObj != null) {
			const ni = editingSteps.indexOf(expandedObj);
			expandedStep = ni >= 0 ? ni : 0;
		} else {
			expandedStep = -1;
		}
	}

	function onStepDragStart(index: number, e: DragEvent) {
		dragStepIndex = index;
		e.dataTransfer?.setData('text/plain', String(index));
		e.dataTransfer!.effectAllowed = 'move';
	}

	function onStepDragOver(index: number, e: DragEvent) {
		e.preventDefault();
		e.dataTransfer!.dropEffect = 'move';
	}

	function onStepDrop(index: number, e: DragEvent) {
		e.preventDefault();
		const from = dragStepIndex ?? parseInt(e.dataTransfer?.getData('text/plain') || '-1', 10);
		dragStepIndex = null;
		if (Number.isNaN(from) || from < 0) return;
		reorderSteps(from, index);
	}

	function onStepDragEnd() {
		dragStepIndex = null;
	}

	function updateStepAction(index: number, action: MacroAction) {
		const step = editingSteps[index];
		const params: Record<string, unknown> = {};
		const sourceActions: MacroAction[] = [
			'cut', 'preview', 'transition', 'set_audio',
			'audio_mute', 'audio_afv', 'audio_trim',
			'audio_eq', 'audio_compressor', 'audio_delay',
			'key_set', 'key_delete', 'source_label',
			'source_delay', 'source_position',
			'replay_mark_in', 'replay_mark_out', 'replay_play',
			'replay_quick_clip', 'replay_play_clip',
		];
		if (sourceActions.includes(action)) {
			params.source = step.params.source || sourceKeys[0] || '';
		}
		if (action === 'transition') { params.type = 'mix'; params.durationMs = 1000; }
		if (action === 'wait') params.ms = 500;
		if (action === 'set_audio') params.level = 0;
		if (action === 'audio_mute') params.muted = true;
		if (action === 'audio_afv') params.afv = true;
		if (action === 'audio_trim') params.trim = 0;
		if (action === 'audio_master') params.level = 0;
		if (action === 'audio_delay') params.delayMs = 0;
		if (action === 'source_delay') params.delayMs = 0;
		if (action === 'source_position') params.position = 1;
		if (action === 'replay_play') { params.speed = 0.5; params.loop = false; }
		if (action === 'replay_quick_clip') { params.durationSecs = 10; params.speed = 0.5; }
		if (action === 'scte35_cue') { params.durationMs = 30000; params.autoReturn = true; }
		if (['scte35_return', 'scte35_cancel', 'scte35_hold'].includes(action)) params.eventId = 0;
		if (action === 'scte35_extend') { params.eventId = 0; params.durationMs = 30000; }
		editingSteps[index] = { action, params };
		editingSteps = [...editingSteps];
	}

	function updateStepParam(index: number, key: string, value: unknown) {
		editingSteps[index].params[key] = value;
		editingSteps = [...editingSteps];
	}

	function stepSummary(step: MacroStep): string {
		const meta = ACTION_META[step.action];
		if (!meta) return step.action;
		switch (step.action) {
			case 'cut':
			case 'preview':
				return `${meta.label} \u2192 ${sourceLabel(step.params.source as string || '?')}`;
			case 'transition': {
				const type = (step.params.type as string) || 'mix';
				const dur = step.params.durationMs as number || 1000;
				let suffix = '';
				if (type === 'wipe' && step.params.wipeDirection) suffix = ` [${step.params.wipeDirection}]`;
				if (type === 'stinger' && step.params.stingerName) suffix = ` [${step.params.stingerName}]`;
				return `${type.charAt(0).toUpperCase() + type.slice(1)} \u2192 ${sourceLabel(step.params.source as string || '?')} (${dur}ms)${suffix}`;
			}
			case 'ftb':
				return 'Fade to Black';
			case 'wait':
				return `Wait ${step.params.ms || 0}ms`;
			case 'set_audio': {
				const lvl = step.params.level as number ?? 0;
				return `Audio: ${sourceLabel(step.params.source as string || '?')} \u2192 ${lvl > 0 ? '+' : ''}${lvl} dB`;
			}
			case 'audio_mute':
				return `${step.params.muted !== false ? 'Mute' : 'Unmute'} ${sourceLabel(step.params.source as string || '?')}`;
			case 'audio_afv':
				return `AFV ${step.params.afv !== false ? 'On' : 'Off'}: ${sourceLabel(step.params.source as string || '?')}`;
			case 'audio_trim': {
				const trim = step.params.trim as number ?? 0;
				return `Trim: ${sourceLabel(step.params.source as string || '?')} \u2192 ${trim > 0 ? '+' : ''}${trim} dB`;
			}
			case 'audio_master': {
				const lvl = step.params.level as number ?? 0;
				return `Master \u2192 ${lvl > 0 ? '+' : ''}${lvl} dB`;
			}
			case 'audio_eq':
				return `EQ: ${sourceLabel(step.params.source as string || '?')}`;
			case 'audio_compressor':
				return `Compressor: ${sourceLabel(step.params.source as string || '?')}`;
			case 'audio_delay':
				return `Audio Delay: ${sourceLabel(step.params.source as string || '?')} \u2192 ${step.params.delayMs || 0}ms`;
			case 'graphics_on':
				return 'Graphics On';
			case 'graphics_off':
				return 'Graphics Off';
			case 'graphics_auto_on':
				return 'Auto Graphics On';
			case 'graphics_auto_off':
				return 'Auto Graphics Off';
			case 'recording_start':
				return 'Start Recording';
			case 'recording_stop':
				return 'Stop Recording';
			case 'preset_recall':
				return `Recall: ${step.params.name || step.params.id || '?'}`;
			case 'key_set':
				return `Key: ${sourceLabel(step.params.source as string || '?')}`;
			case 'key_delete':
				return `Remove Key: ${sourceLabel(step.params.source as string || '?')}`;
			case 'source_label':
				return `Label: ${sourceLabel(step.params.source as string || '?')} \u2192 "${step.params.label || ''}"`;
			case 'source_delay':
				return `Delay: ${sourceLabel(step.params.source as string || '?')} \u2192 ${step.params.delayMs || 0}ms`;
			case 'source_position':
				return `Position: ${sourceLabel(step.params.source as string || '?')} \u2192 #${step.params.position || 1}`;
			case 'replay_mark_in':
				return `Mark In: ${sourceLabel(step.params.source as string || '?')}`;
			case 'replay_mark_out':
				return `Mark Out: ${sourceLabel(step.params.source as string || '?')}`;
			case 'replay_play':
				return `Replay: ${sourceLabel(step.params.source as string || '?')} @ ${step.params.speed || 0.5}x${step.params.loop ? ' (loop)' : ''}`;
			case 'replay_stop':
				return 'Stop Replay';
			case 'replay_quick_clip':
				return `Quick Clip: ${sourceLabel(step.params.source as string || '?')} ${step.params.durationSecs || 10}s @ ${step.params.speed || 0.5}x`;
			case 'replay_play_last':
				return 'Play Last Clip';
			case 'replay_play_clip':
				return `Play Clip: ${step.params.clipId || '?'}`;
			case 'scte35_cue':
				return `Ad Break (${((step.params.durationMs as number) || 30000) / 1000}s)`;
			case 'scte35_return':
				return `Return${(step.params.eventId as number) ? ` #${step.params.eventId}` : ''}`;
			case 'scte35_cancel':
				return `Cancel #${step.params.eventId || 0}`;
			case 'scte35_hold':
				return `Hold #${step.params.eventId || 0}`;
			case 'scte35_extend':
				return `Extend #${step.params.eventId || 0} (+${step.params.durationMs || 0}ms)`;
			default:
				return meta.label;
		}
	}

	$effect(() => {
		loadMacros();
	});
</script>

<div class="macro-panel">
	<!-- Left rail: macro list -->
	<aside class="macro-rail">
		<div class="macro-rail-header">
			<span class="macro-title" aria-hidden="true">{@html renderIcon(ICON_BOLT, 12, 'fill')}</span>
			<span class="macro-title-text">MACROS</span>
			<div class="rail-header-actions">
				<div class="help-wrap">
					<button
						type="button"
						class="icon-btn help-btn"
						onclick={toggleHelpPopover}
						title="Macro help"
						aria-expanded={showHelpPopover}
						aria-haspopup="true"
					>
						{@html renderIcon(ICON_HELP, 14, 'stroke')}
					</button>
					{#if showHelpPopover}
						<div class="macro-help-popover" role="tooltip">
							<button type="button" class="help-popover-close" onclick={closeHelpPopover} aria-label="Close help">
								{@html renderIcon(ICON_CLOSE, 12, 'stroke')}
							</button>
							<div class="help-popover-title">Macros</div>
							<p class="help-popover-text">
								Automate switcher sequences — cuts, transitions, audio, graphics, replay, and ad breaks.
								Use <strong>Ctrl+1–9</strong> to run by slot. Click <strong>+</strong> to create a macro; use Run (play) from the list.
							</p>
						</div>
					{/if}
				</div>
				{#if !editMode}
					<button type="button" class="icon-btn add-btn" onclick={() => startEdit()} title="New macro">
						{@html renderIcon(ICON_PLUS, 14, 'stroke')}
					</button>
				{/if}
			</div>
		</div>

		<div class="macro-list">
			{#each macros as m, i (m.name)}
				<div class="macro-row">
					<button
						type="button"
						class="macro-btn macro-run-btn"
						class:running={runningMacro === m.name}
						disabled={runningMacro !== null || isRunning}
						onclick={() => handleRun(m.name)}
						title="Run: {m.name} (Ctrl+{i + 1})"
					>
						{#if runningMacro === m.name}
							<span class="spinner" aria-hidden="true"></span>
						{:else}
							<span class="macro-play-icon" aria-hidden="true">{@html renderIcon(ICON_PLAY, 12, 'fill')}</span>
						{/if}
					</button>
					<span class="macro-row-name" title={m.name}>{m.name}</span>
					<span class="step-badge" title="Steps">{m.steps.length}</span>
					<button type="button" class="icon-btn edit-btn" onclick={() => startEdit(m)} title="Edit">
						{@html renderIcon(ICON_PENCIL, 13, 'stroke')}
					</button>
					<div class="delete-wrap">
						<button
							type="button"
							class="icon-btn del-btn"
							onclick={() => requestDelete(m.name)}
							title="Delete"
						>
							{@html renderIcon(ICON_TRASH, 13, 'stroke')}
						</button>
						{#if deleteConfirmName === m.name}
							<div class="delete-popover">
								<span class="delete-popover-text">Delete &ldquo;{m.name}&rdquo;?</span>
								<div class="delete-popover-actions">
									<button type="button" class="del-confirm-btn" onclick={() => confirmDelete(m.name)}>Delete</button>
									<button type="button" class="del-cancel-btn" onclick={cancelDeleteConfirm}>Cancel</button>
								</div>
							</div>
						{/if}
					</div>
				</div>
			{/each}
			{#if macros.length === 0}
				<div class="rail-empty">No macros yet</div>
			{/if}
		</div>

		{#if macros.length > 0}
			<div class="shortcut-tip">Ctrl+1–{Math.min(macros.length, 9)} run</div>
		{/if}
	</aside>

	<!-- Right: execution, editor, or placeholder -->
	<main class="macro-main">
		{#if crState.macro && !editMode}
			{@const exec = crState.macro}
			<div class="macro-main-scroll execution-view">
				<div class="execution-header">
					<div class="execution-title-row">
						{#if exec.running}
							<span class="exec-status-icon exec-running-icon" aria-hidden="true">{@html renderIcon(ICON_PLAY, 14, 'fill')}</span>
							<span class="execution-title">Running: {exec.macroName}</span>
						{:else if exec.error}
							<span class="execution-title exec-failed">{exec.macroName}</span>
						{:else}
							<span class="exec-status-icon exec-success-icon" aria-hidden="true">{@html renderIcon(ICON_CHECK, 14, 'stroke')}</span>
							<span class="execution-title exec-success">{exec.macroName}</span>
						{/if}
					</div>
					<span class="exec-step-counter">{exec.currentStep + 1} / {exec.steps.length}</span>
				</div>

				<div class="exec-progress-track" aria-hidden="true">
					<div
						class="exec-progress-fill"
						style="width: {exec.steps.length ? ((exec.currentStep + (exec.running ? 0.5 : 1)) / exec.steps.length) * 100 : 0}%"
					></div>
				</div>

				<div class="exec-step-list">
					{#each exec.steps as step, i}
						<div class="exec-step-row {statusClass(step.status)}">
							<span class="exec-step-icon" aria-label={step.status}>
								{#if step.status === 'done'}
									{@html renderIcon(ICON_CHECK, 12, 'stroke')}
								{:else if step.status === 'running'}
									<span class="exec-dot"></span>
								{:else if step.status === 'failed'}
									{@html renderIcon(ICON_CLOSE, 12, 'stroke')}
								{:else if step.status === 'skipped'}
									<span class="exec-dash">&ndash;</span>
								{:else}
									<span class="exec-ring"></span>
								{/if}
							</span>
							<span class="exec-step-summary">{step.summary}</span>
							{#if step.status === 'failed' && step.error}
								<div class="exec-step-error">{step.error}</div>
							{/if}
							{#if step.status === 'running' && step.action === 'wait' && step.waitMs}
								<div class="exec-progress-container">
									<div class="exec-progress-bar" style="width: {waitProgress(step) * 100}%"></div>
								</div>
								<div class="exec-progress-label">{waitElapsed(step)}</div>
							{/if}
							{#if step.status === 'running' && step.action === 'transition' && crState.inTransition}
								<div class="exec-progress-container">
									<div class="exec-progress-bar" style="width: {crState.transitionPosition * 100}%"></div>
								</div>
								<div class="exec-progress-label">{Math.round(crState.transitionPosition * 100)}%</div>
							{/if}
						</div>
					{/each}
				</div>

				<div class="execution-footer">
					{#if exec.running}
						<button type="button" class="exec-btn exec-btn-cancel" onclick={() => cancelMacro()}>Cancel</button>
					{:else if exec.error}
						<span class="exec-result">Failed at step {exec.currentStep + 1}</span>
						<button type="button" class="exec-btn exec-btn-dismiss" onclick={() => dismissMacro()}>Dismiss</button>
					{:else}
						<span class="exec-result exec-success">Complete!</span>
						<button type="button" class="exec-btn exec-btn-dismiss" onclick={() => dismissMacro()}>Dismiss</button>
					{/if}
				</div>
			</div>
		{:else if editMode}
			<div class="macro-main-scroll macro-editor">
				<input
					class="macro-name-input"
					type="text"
					placeholder="Macro name"
					bind:value={editingName}
				/>

				<div class="step-list" role="list">
					{#each editingSteps as step, i (i)}
						<div
							class="step-card"
							class:expanded={expandedStep === i}
							class:dragging={dragStepIndex === i}
							role="listitem"
							ondragover={(e) => onStepDragOver(i, e)}
							ondrop={(e) => onStepDrop(i, e)}
						>
							<div
								class="step-grip"
								draggable="true"
								role="button"
								tabindex="0"
								title="Drag to reorder"
								aria-label="Drag to reorder step {i + 1}"
								ondragstart={(e) => onStepDragStart(i, e)}
								ondragend={onStepDragEnd}
							>
								{@html renderIcon(ICON_GRIP, 14, 'stroke')}
							</div>
							<button
								type="button"
								class="step-header"
								onclick={() => { expandedStep = expandedStep === i ? -1 : i; }}
							>
								<span class="step-number">{i + 1}</span>
								<span class="action-badge">{ACTION_META[step.action]?.label ?? step.action}</span>
								<span class="step-summary-text">{stepSummary(step)}</span>
								<span class="step-chevron" aria-hidden="true">
									{#if expandedStep === i}
										{@html renderIcon(ICON_CHEVRON_DOWN, 12, 'stroke')}
									{:else}
										{@html renderIcon(ICON_CHEVRON_RIGHT, 12, 'stroke')}
									{/if}
								</span>
							</button>
							<button type="button" class="icon-btn step-delete" onclick={() => removeStep(i)} title="Remove step">
								{@html renderIcon(ICON_TRASH, 12, 'stroke')}
							</button>

							{#if expandedStep === i}
								<div class="step-body">
									<MacroStepEditor
										{step}
										index={i}
										{sourceKeys}
										{sourceLabel}
										{stingerNames}
										presetNames={presetList}
										onupdate={(key, value) => updateStepParam(i, key, value)}
										onchangeaction={(action) => updateStepAction(i, action)}
									/>
								</div>
							{/if}
						</div>
					{/each}
				</div>

				<div class="add-step-area">
					<div class="add-step-flyout-wrap">
						<button
							type="button"
							class="add-step-btn"
							onclick={() => { showPicker = !showPicker; }}
						>
							<span class="add-step-icon" aria-hidden="true">{@html renderIcon(ICON_PLUS, 12, 'stroke')}</span>
							Add step
							<span class="add-step-chev" aria-hidden="true">{@html renderIcon(ICON_CHEVRON_DOWN, 10, 'stroke')}</span>
						</button>
						{#if showPicker}
							<div class="step-picker">
								{#each CATEGORIES as category}
									<div class="picker-category">{category}</div>
									{#each Object.entries(ACTION_META).filter(([, m]) => m.category === category) as [action, meta]}
										<button
											type="button"
											class="picker-item"
											onclick={() => addStep(action as MacroAction)}
											title={meta.description}
										>
											<span class="picker-label">{meta.label}</span>
											<span class="picker-desc">{meta.description}</span>
										</button>
									{/each}
								{/each}
							</div>
						{/if}
					</div>
				</div>

				{#if editorWarnings.length > 0}
					<div class="editor-warnings">
						{#each editorWarnings as w}
							<div class="editor-warning">{w}</div>
						{/each}
					</div>
				{/if}

				{#if editorError}
					<div class="editor-error">{editorError}</div>
				{/if}

				<div class="editor-buttons">
					<button type="button" class="editor-btn save-btn" onclick={handleSave}>
						<span aria-hidden="true">{@html renderIcon(ICON_CHECK, 14, 'stroke')}</span>
						Save
					</button>
					<button type="button" class="editor-btn cancel-btn" onclick={cancelEdit}>
						<span aria-hidden="true">{@html renderIcon(ICON_CLOSE, 14, 'stroke')}</span>
						Cancel
					</button>
				</div>
			</div>
		{:else}
			<div class="macro-main-placeholder">
				<p class="placeholder-line">Run a macro from the list, or create one with <strong>+</strong>.</p>
				{#if isExecuting}
					<p class="placeholder-sub">Execution status appears here while a macro runs.</p>
				{/if}
			</div>
		{/if}
	</main>
</div>

<style>
	.macro-panel {
		display: flex;
		flex-direction: row;
		align-items: stretch;
		height: 100%;
		min-height: 0;
		overflow: hidden;
		font-family: var(--font-ui);
		background: var(--bg-base);
	}

	/* --- Left rail --- */
	.macro-rail {
		display: flex;
		flex-direction: column;
		width: 200px;
		min-width: 200px;
		max-width: 200px;
		min-height: 0;
		border-right: 1px solid var(--border-subtle);
		background: var(--bg-surface);
	}

	.macro-rail-header {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 6px 6px 6px 8px;
		flex-shrink: 0;
		border-bottom: 1px solid var(--border-subtle);
	}

	.macro-title {
		display: flex;
		color: var(--accent-yellow);
		flex-shrink: 0;
	}

	.macro-title-text {
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
		flex: 1;
		min-width: 0;
	}

	.rail-header-actions {
		display: flex;
		align-items: center;
		gap: 2px;
		flex-shrink: 0;
	}

	.help-wrap {
		position: relative;
	}

	.icon-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 26px;
		height: 24px;
		padding: 0;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--text-secondary);
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
	}

	.icon-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.icon-btn :global(svg) {
		display: block;
	}

	.macro-help-popover {
		position: absolute;
		top: 100%;
		left: 0;
		z-index: 50;
		margin-top: 4px;
		width: 220px;
		padding: 8px 28px 8px 8px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		box-shadow: 0 4px 12px rgba(0, 0, 0, 0.35);
	}

	.help-popover-close {
		position: absolute;
		top: 4px;
		right: 4px;
		width: 22px;
		height: 22px;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		background: transparent;
		border: none;
		border-radius: var(--radius-xs);
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.help-popover-close:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.help-popover-title {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-primary);
		margin-bottom: 4px;
	}

	.help-popover-text {
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		margin: 0;
		line-height: 1.45;
	}

	.help-popover-text strong {
		color: var(--text-primary);
		font-weight: 600;
	}

	.macro-list {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 4px 4px 6px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.macro-row {
		display: flex;
		align-items: center;
		gap: 4px;
		min-width: 0;
		padding: 2px 2px 2px 4px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		position: relative;
	}

	.macro-run-btn {
		flex-shrink: 0;
		width: 26px;
		height: 26px;
		padding: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-xs);
		color: var(--accent-blue);
		cursor: pointer;
		transition: background var(--transition-fast), border-color var(--transition-normal);
	}

	.macro-run-btn:hover:not(:disabled) {
		background: var(--bg-hover);
		border-color: var(--accent-blue);
	}

	.macro-run-btn:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.macro-run-btn.running {
		border-color: var(--accent-blue);
		background: color-mix(in srgb, var(--accent-blue) 12%, var(--bg-control));
	}

	.macro-play-icon :global(svg) {
		display: block;
	}

	.macro-row-name {
		flex: 1;
		min-width: 0;
		font-size: var(--text-2xs);
		font-weight: 500;
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.step-badge {
		flex-shrink: 0;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		padding: 0 4px;
		line-height: 1.4;
	}

	.spinner {
		display: inline-block;
		width: 10px;
		height: 10px;
		border: 2px solid var(--accent-blue);
		border-top-color: transparent;
		border-radius: 50%;
		animation: spin 0.6s linear infinite;
	}

	@keyframes spin {
		to { transform: rotate(360deg); }
	}

	.delete-wrap {
		position: relative;
		flex-shrink: 0;
	}

	.delete-popover {
		position: absolute;
		top: 100%;
		right: 0;
		z-index: 40;
		margin-top: 4px;
		width: 160px;
		padding: 6px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		box-shadow: 0 4px 12px rgba(0, 0, 0, 0.35);
	}

	.delete-popover-text {
		display: block;
		font-size: var(--text-2xs);
		color: var(--text-primary);
		margin-bottom: 6px;
		line-height: 1.3;
	}

	.delete-popover-actions {
		display: flex;
		gap: 4px;
		justify-content: flex-end;
	}

	.del-confirm-btn {
		padding: 2px 8px;
		font-size: var(--text-2xs);
		font-weight: 600;
		font-family: var(--font-ui);
		background: color-mix(in srgb, var(--color-error) 18%, transparent);
		border: 1px solid color-mix(in srgb, var(--color-error) 40%, transparent);
		color: var(--color-error);
		border-radius: var(--radius-xs);
		cursor: pointer;
	}

	.del-confirm-btn:hover {
		background: color-mix(in srgb, var(--color-error) 28%, transparent);
	}

	.del-cancel-btn {
		padding: 2px 8px;
		font-size: var(--text-2xs);
		font-family: var(--font-ui);
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		border-radius: var(--radius-xs);
		cursor: pointer;
	}

	.del-cancel-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.rail-empty {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		padding: 8px 6px;
		text-align: center;
	}

	.shortcut-tip {
		flex-shrink: 0;
		text-align: center;
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		padding: 4px 6px 6px;
		border-top: 1px solid var(--border-subtle);
	}

	/* --- Main panel --- */
	.macro-main {
		flex: 1;
		min-width: 0;
		min-height: 0;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		background: var(--bg-base);
	}

	.macro-main-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 8px 10px 10px;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.macro-main-placeholder {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: 16px;
		text-align: center;
	}

	.placeholder-line {
		font-size: var(--text-xs);
		color: var(--text-secondary);
		margin: 0 0 6px;
		max-width: 280px;
		line-height: 1.4;
	}

	.placeholder-sub {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		margin: 0;
	}

	/* --- Editor --- */
	.macro-editor {
		gap: 8px;
	}

	.macro-name-input {
		width: 100%;
		padding: 5px 8px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 500;
		box-sizing: border-box;
		flex-shrink: 0;
	}

	.macro-name-input:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.macro-name-input::placeholder {
		color: var(--text-tertiary);
	}

	.step-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.step-card {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		overflow: hidden;
		transition: border-color var(--transition-fast), opacity var(--transition-fast);
	}

	.step-card.expanded {
		border-color: var(--border-strong);
	}

	.step-card.dragging {
		opacity: 0.55;
	}

	.step-grip {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		width: 22px;
		min-height: 32px;
		padding: 0 2px;
		cursor: grab;
		color: var(--text-tertiary);
		background: var(--bg-control);
		border-right: 1px solid var(--border-subtle);
		user-select: none;
	}

	.step-grip:active {
		cursor: grabbing;
	}

	.step-header {
		flex: 1;
		min-width: 0;
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 5px 6px;
		background: transparent;
		border: none;
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		cursor: pointer;
		text-align: left;
	}

	.step-number {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
		min-width: 1rem;
	}

	.action-badge {
		flex-shrink: 0;
		font-size: var(--text-2xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.03em;
		color: var(--accent-orange);
		background: color-mix(in srgb, var(--accent-orange) 12%, transparent);
		border: 1px solid color-mix(in srgb, var(--accent-orange) 35%, transparent);
		padding: 1px 5px;
		border-radius: var(--radius-xs);
		max-width: 88px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.step-summary-text {
		flex: 1;
		min-width: 0;
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.step-chevron {
		flex-shrink: 0;
		display: flex;
		color: var(--text-tertiary);
	}

	.step-delete {
		flex-shrink: 0;
		margin: 3px 4px 3px 0;
		align-self: flex-start;
	}

	.step-delete:hover {
		color: var(--color-error);
		border-color: color-mix(in srgb, var(--color-error) 35%, transparent);
	}

	.step-body {
		width: 100%;
		padding: 6px 8px 8px 10px;
		border-top: 1px solid var(--border-subtle);
		background: var(--bg-elevated);
		box-sizing: border-box;
	}

	.add-step-area {
		position: relative;
		flex-shrink: 0;
	}

	.add-step-flyout-wrap {
		position: relative;
		display: inline-block;
	}

	.add-step-btn {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px;
		background: var(--bg-control);
		border: 1px dashed var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
	}

	.add-step-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
		border-color: var(--border-strong);
		border-style: solid;
	}

	.add-step-icon,
	.add-step-chev {
		display: flex;
		color: var(--text-tertiary);
	}

	.step-picker {
		position: absolute;
		left: 0;
		bottom: 100%;
		margin-bottom: 4px;
		min-width: 260px;
		max-width: min(340px, 90vw);
		max-height: min(320px, 45vh);
		overflow-y: auto;
		background: var(--bg-elevated);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		z-index: 30;
		box-shadow: 0 -4px 16px rgba(0, 0, 0, 0.35);
	}

	.picker-category {
		font-size: var(--text-2xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-tertiary);
		padding: 5px 8px 2px;
		background: var(--bg-surface);
		position: sticky;
		top: 0;
		border-bottom: 1px solid var(--border-subtle);
	}

	.picker-item {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		width: 100%;
		padding: 4px 8px;
		background: transparent;
		border: none;
		border-bottom: 1px solid var(--border-subtle);
		cursor: pointer;
		text-align: left;
		transition: background var(--transition-fast);
	}

	.picker-item:hover {
		background: var(--bg-hover);
	}

	.picker-item:last-child {
		border-bottom: none;
	}

	.picker-label {
		font-size: var(--text-2xs);
		font-weight: 500;
		color: var(--text-primary);
		min-width: 72px;
		flex-shrink: 0;
	}

	.picker-desc {
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		line-height: 1.3;
	}

	.editor-warnings {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.editor-warning {
		font-size: var(--text-2xs);
		color: var(--accent-yellow);
		padding: 3px 6px;
		background: color-mix(in srgb, var(--accent-yellow) 10%, transparent);
		border-radius: var(--radius-xs);
		border: 1px solid color-mix(in srgb, var(--accent-yellow) 25%, transparent);
	}

	.editor-error {
		color: var(--color-error);
		font-size: var(--text-2xs);
		padding: 0 2px;
	}

	.editor-buttons {
		display: flex;
		gap: 6px;
		flex-shrink: 0;
		padding-top: 2px;
	}

	.editor-btn {
		flex: 1;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 6px;
		padding: 5px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.editor-btn :global(svg) {
		display: block;
	}

	.save-btn {
		background: color-mix(in srgb, var(--color-success) 16%, transparent);
		color: var(--color-success);
		border-color: color-mix(in srgb, var(--color-success) 40%, transparent);
	}

	.save-btn:hover {
		background: color-mix(in srgb, var(--color-success) 26%, transparent);
	}

	.cancel-btn {
		background: var(--bg-control);
		color: var(--text-secondary);
	}

	.cancel-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	/* --- Execution --- */
	.execution-view {
		gap: 8px;
	}

	.execution-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 6px 8px;
		background: var(--bg-surface);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		flex-shrink: 0;
	}

	.execution-title-row {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}

	.exec-status-icon {
		flex-shrink: 0;
		display: flex;
	}

	.exec-running-icon {
		color: var(--accent-blue);
		animation: pulse-op 1.2s ease-in-out infinite;
	}

	.exec-success-icon {
		color: var(--color-success);
	}

	@keyframes pulse-op {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.45; }
	}

	.execution-title {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.execution-title.exec-failed {
		color: var(--color-error);
	}

	.execution-title.exec-success {
		color: var(--color-success);
	}

	.exec-step-counter {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.exec-progress-track {
		height: 3px;
		background: var(--bg-control);
		border-radius: var(--radius-xs);
		overflow: hidden;
		flex-shrink: 0;
	}

	.exec-progress-fill {
		height: 100%;
		background: var(--accent-blue);
		border-radius: var(--radius-xs);
		transition: width var(--transition-normal);
	}

	.exec-step-list {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.exec-step-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
		padding: 4px 8px;
		background: var(--bg-surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs);
		transition: border-color var(--transition-fast), background var(--transition-fast);
	}

	.exec-step-row.step-done {
		border-color: color-mix(in srgb, var(--color-success) 35%, transparent);
	}

	.exec-step-row.step-running {
		border-color: var(--accent-blue);
		background: color-mix(in srgb, var(--accent-blue) 6%, var(--bg-surface));
	}

	.exec-step-row.step-failed {
		border-color: color-mix(in srgb, var(--color-error) 40%, transparent);
		background: color-mix(in srgb, var(--color-error) 6%, var(--bg-surface));
	}

	.exec-step-row.step-skipped {
		opacity: 0.45;
	}

	.exec-step-icon {
		flex-shrink: 0;
		width: 16px;
		height: 16px;
		display: flex;
		align-items: center;
		justify-content: center;
		color: var(--text-tertiary);
	}

	.exec-step-icon :global(svg) {
		display: block;
	}

	.step-done .exec-step-icon {
		color: var(--color-success);
	}

	.step-running .exec-step-icon {
		color: var(--accent-blue);
	}

	.step-failed .exec-step-icon {
		color: var(--color-error);
	}

	.exec-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: currentColor;
		animation: pulse-op 1s ease-in-out infinite;
	}

	.exec-dash {
		font-size: var(--text-sm);
		line-height: 1;
		color: var(--text-tertiary);
	}

	.exec-ring {
		width: 8px;
		height: 8px;
		border: 1.5px solid currentColor;
		border-radius: 50%;
		opacity: 0.7;
	}

	.exec-step-summary {
		flex: 1;
		min-width: 0;
		font-size: var(--text-2xs);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.exec-step-error {
		width: 100%;
		font-size: var(--text-2xs);
		color: var(--color-error);
		padding: 2px 0 0 22px;
		line-height: 1.3;
	}

	.exec-progress-container {
		width: 100%;
		height: 3px;
		background: var(--bg-control);
		border-radius: var(--radius-xs);
		margin: 2px 0 0 22px;
		overflow: hidden;
	}

	.exec-progress-bar {
		height: 100%;
		background: var(--accent-blue);
		border-radius: var(--radius-xs);
		transition: width 0.05s linear;
	}

	.exec-progress-label {
		width: 100%;
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		padding-left: 22px;
	}

	.execution-footer {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 8px;
		padding-top: 4px;
		flex-shrink: 0;
		flex-wrap: wrap;
	}

	.exec-result {
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		margin-right: auto;
	}

	.exec-result.exec-success {
		color: var(--color-success);
		font-weight: 600;
	}

	.exec-btn {
		padding: 4px 12px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.exec-btn-cancel {
		background: color-mix(in srgb, var(--color-error) 14%, transparent);
		color: var(--color-error);
		border-color: color-mix(in srgb, var(--color-error) 35%, transparent);
	}

	.exec-btn-cancel:hover {
		background: color-mix(in srgb, var(--color-error) 24%, transparent);
	}

	.exec-btn-dismiss {
		background: var(--bg-control);
		color: var(--text-secondary);
	}

	.exec-btn-dismiss:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}
</style>
