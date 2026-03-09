<script lang="ts">
	import type { Macro, MacroStep, MacroAction, ControlRoomState } from '$lib/api/types';
	import { listMacros, saveMacro, deleteMacro, runMacro, listStingers, listPresets } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import MacroStepEditor from './MacroStepEditor.svelte';
	import { ACTION_META, CATEGORIES } from './macro-actions';

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
	let editorError = $state('');
	let expandedStep = $state<number>(0);
	let showPicker = $state(false);
	let showGuide = $state(false);
	let guideDismissed = $state(false);
	let stingerNames = $state<string[]>([]);
	let presetList = $state<{ id: string; name: string }[]>([]);

	let sourceKeys = $derived(Object.keys(crState.sources).sort());

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

	// --- Guide ---
	function initGuide() {
		guideDismissed = localStorage.getItem('switchframe-macro-guide-dismissed') === 'true';
		showGuide = !guideDismissed;
	}

	function dismissGuide() {
		showGuide = false;
		guideDismissed = true;
		localStorage.setItem('switchframe-macro-guide-dismissed', 'true');
	}

	function toggleGuide() {
		showGuide = !showGuide;
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

	async function handleDelete(name: string) {
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
	}

	// --- Step manipulation ---
	function addStep(action: MacroAction) {
		const params: Record<string, unknown> = {};
		// Smart defaults: source for source-dependent actions
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

	function moveStep(index: number, direction: -1 | 1) {
		const target = index + direction;
		if (target < 0 || target >= editingSteps.length) return;
		const copy = [...editingSteps];
		[copy[index], copy[target]] = [copy[target], copy[index]];
		editingSteps = copy;
		expandedStep = target;
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

	// --- Init ---
	$effect(() => {
		loadMacros();
		initGuide();
	});
</script>

<div class="macro-panel">
	<div class="macro-header">
		<span class="macro-title">MACROS</span>
		<div class="header-actions">
			<button class="help-btn" onclick={toggleGuide} title="Help">?</button>
			{#if !editMode}
				<button class="add-btn" onclick={() => startEdit()} title="New macro">+</button>
			{/if}
		</div>
	</div>

	<!-- Getting Started Guide -->
	{#if showGuide && !editMode}
		<div class="guide">
			<div class="guide-title">Getting Started</div>
			<p class="guide-text">
				Macros automate sequences of switcher operations — cuts, transitions, audio changes, graphics, replay, and ad breaks.
			</p>
			<div class="guide-example">
				<div class="guide-example-title">Example:</div>
				<ol class="guide-steps">
					<li>Cut to Camera 1</li>
					<li>Wait 500ms</li>
					<li>Wipe to Camera 2 (left)</li>
					<li>Fade to Black</li>
				</ol>
			</div>
			<p class="guide-text">
				Click <strong>+</strong> to create your first macro. Press <strong>Ctrl+1–9</strong> to run macros by number.
			</p>
			<button class="guide-dismiss" onclick={dismissGuide}>Got it</button>
		</div>
	{/if}

	<!-- Edit Mode -->
	{#if editMode}
		<div class="macro-editor">
			<input
				class="macro-name-input"
				type="text"
				placeholder="Macro name"
				bind:value={editingName}
			/>

			{#each editingSteps as step, i (i)}
				<div class="step-card" class:expanded={expandedStep === i}>
					<button
						class="step-header"
						onclick={() => { expandedStep = expandedStep === i ? -1 : i; }}
					>
						<span class="step-number">{i + 1}.</span>
						<span class="step-summary">{stepSummary(step)}</span>
						<span class="step-chevron">{expandedStep === i ? '\u25BC' : '\u25B6'}</span>
					</button>
					<div class="step-actions">
						<button
							class="step-move"
							disabled={i === 0}
							onclick={() => moveStep(i, -1)}
							title="Move up"
						>\u25B2</button>
						<button
							class="step-move"
							disabled={i === editingSteps.length - 1}
							onclick={() => moveStep(i, 1)}
							title="Move down"
						>\u25BC</button>
						<button
							class="step-delete"
							onclick={() => removeStep(i)}
							title="Remove step"
						>\u00D7</button>
					</div>

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

			<!-- Add Step -->
			<div class="add-step-area">
				{#if showPicker}
					<div class="step-picker">
						{#each CATEGORIES as category}
							<div class="picker-category">{category}</div>
							{#each Object.entries(ACTION_META).filter(([, m]) => m.category === category) as [action, meta]}
								<button
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
				{:else}
					<button class="add-step-btn" onclick={() => { showPicker = true; }}>+ Add Step</button>
				{/if}
			</div>

			<!-- Validation warnings -->
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
				<button class="editor-btn save-btn" onclick={handleSave}>Save</button>
				<button class="editor-btn cancel-btn" onclick={cancelEdit}>Cancel</button>
			</div>
		</div>

	{:else}
		<!-- List Mode -->
		<div class="macro-grid">
			{#each macros as m, i (m.name)}
				<div class="macro-item">
					<button
						class="macro-btn"
						class:running={runningMacro === m.name}
						disabled={runningMacro !== null}
						onclick={() => handleRun(m.name)}
						title="Run macro: {m.name} (Ctrl+{i + 1})"
					>
						{#if runningMacro === m.name}
							<span class="spinner"></span>
						{/if}
						<span class="macro-name">{m.name}</span>
						<span class="macro-step-count">{m.steps.length} step{m.steps.length !== 1 ? 's' : ''}</span>
					</button>
					<div class="macro-actions">
						<button class="action-btn edit-btn" onclick={() => startEdit(m)} title="Edit">E</button>
						<button class="action-btn del-btn" onclick={() => handleDelete(m.name)} title="Delete">X</button>
					</div>
				</div>
			{/each}
		</div>

		{#if macros.length > 0}
			<div class="shortcut-tip">Ctrl+1–{Math.min(macros.length, 9)} to run</div>
		{/if}

		{#if macros.length === 0 && !showGuide}
			<div class="empty-state">No macros. Click + to create one.</div>
		{/if}
	{/if}
</div>

<style>
	.macro-panel {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 4px;
		height: 100%;
		overflow-y: auto;
	}

	.macro-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 2px 4px;
	}

	.macro-title {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 600;
		letter-spacing: 0.06em;
		color: var(--text-secondary);
	}

	.header-actions {
		display: flex;
		gap: 4px;
	}

	.help-btn, .add-btn {
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-size: 0.75rem;
		cursor: pointer;
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		line-height: 1;
		font-family: var(--font-ui);
		font-weight: 600;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.help-btn:hover, .add-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	/* --- Guide --- */
	.guide {
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 8px;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.guide-title {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 600;
		color: var(--text-primary);
	}

	.guide-text {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		color: var(--text-secondary);
		margin: 0;
		line-height: 1.4;
	}

	.guide-text strong {
		color: var(--text-primary);
	}

	.guide-example {
		background: var(--bg-base);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		padding: 6px 8px;
	}

	.guide-example-title {
		font-family: var(--font-ui);
		font-size: 0.6rem;
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		margin-bottom: 2px;
	}

	.guide-steps {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		color: var(--text-primary);
		margin: 0;
		padding-left: 16px;
		line-height: 1.6;
	}

	.guide-dismiss {
		align-self: flex-end;
		background: rgba(59, 130, 246, 0.15);
		border: 1px solid rgba(59, 130, 246, 0.3);
		color: var(--accent-blue);
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 600;
		padding: 3px 12px;
		border-radius: var(--radius-sm);
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.guide-dismiss:hover {
		background: rgba(59, 130, 246, 0.25);
	}

	/* --- List Mode --- */
	.macro-grid {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.macro-item {
		display: flex;
		align-items: stretch;
		gap: 2px;
	}

	.macro-btn {
		flex: 1;
		padding: 6px 8px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: 0.75rem;
		font-weight: 500;
		cursor: pointer;
		text-align: left;
		transition: background var(--transition-fast), border-color var(--transition-fast);
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.macro-btn:hover:not(:disabled) {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	.macro-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.macro-btn.running {
		border-color: var(--accent-blue);
		background: rgba(59, 130, 246, 0.15);
	}

	.macro-name {
		flex: 1;
	}

	.macro-step-count {
		font-size: 0.6rem;
		color: var(--text-tertiary);
		font-family: var(--font-mono);
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

	.macro-actions {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.action-btn {
		padding: 2px 4px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-size: 0.6rem;
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.action-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.del-btn:hover {
		color: #ef4444;
	}

	.shortcut-tip {
		text-align: center;
		font-family: var(--font-ui);
		font-size: 0.6rem;
		color: var(--text-tertiary);
		padding: 4px;
	}

	.empty-state {
		text-align: center;
		color: var(--text-tertiary);
		font-size: 0.7rem;
		padding: 12px 4px;
	}

	/* --- Editor --- */
	.macro-editor {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.macro-name-input {
		width: 100%;
		padding: 4px 6px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: 0.75rem;
		font-weight: 500;
		box-sizing: border-box;
	}

	.macro-name-input:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.macro-name-input::placeholder {
		color: var(--text-tertiary);
	}

	/* --- Step Card --- */
	.step-card {
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		overflow: hidden;
		display: flex;
		flex-wrap: wrap;
	}

	.step-card.expanded {
		border-color: var(--border-strong);
	}

	.step-header {
		flex: 1;
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 4px 6px;
		background: transparent;
		border: none;
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: 0.7rem;
		cursor: pointer;
		text-align: left;
		min-width: 0;
	}

	.step-number {
		color: var(--text-tertiary);
		font-family: var(--font-mono);
		font-size: 0.6rem;
		flex-shrink: 0;
	}

	.step-summary {
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.step-chevron {
		font-size: 0.55rem;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.step-actions {
		display: flex;
		align-items: center;
		gap: 1px;
		padding-right: 2px;
	}

	.step-move, .step-delete {
		padding: 2px 4px;
		background: transparent;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-size: 0.55rem;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.step-move:hover:not(:disabled) {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.step-move:disabled {
		opacity: 0.3;
		cursor: default;
	}

	.step-delete:hover {
		background: rgba(239, 68, 68, 0.15);
		color: #ef4444;
	}

	.step-body {
		width: 100%;
		padding: 4px 6px 6px;
		border-top: 1px solid var(--border-subtle);
	}

	/* --- Add Step Picker --- */
	.add-step-area {
		margin-top: 2px;
	}

	.add-step-btn {
		width: 100%;
		padding: 4px;
		background: var(--bg-panel);
		border: 1px dashed var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 500;
		cursor: pointer;
		transition: background var(--transition-fast), color var(--transition-fast);
	}

	.add-step-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.step-picker {
		background: var(--bg-elevated);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		overflow: hidden;
		max-height: 300px;
		overflow-y: auto;
	}

	.picker-category {
		font-family: var(--font-ui);
		font-size: 0.55rem;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		padding: 4px 6px 2px;
		background: var(--bg-panel);
	}

	.picker-item {
		display: flex;
		align-items: center;
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
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 500;
		color: var(--text-primary);
		min-width: 80px;
	}

	.picker-desc {
		font-family: var(--font-ui);
		font-size: 0.6rem;
		color: var(--text-tertiary);
	}

	/* --- Warnings & Errors --- */
	.editor-warnings {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.editor-warning {
		font-family: var(--font-ui);
		font-size: 0.6rem;
		color: #f59e0b;
		padding: 2px 4px;
		background: rgba(245, 158, 11, 0.1);
		border-radius: var(--radius-sm);
	}

	.editor-error {
		color: #ef4444;
		font-size: 0.65rem;
		font-family: var(--font-ui);
		padding: 0 2px;
	}

	.editor-buttons {
		display: flex;
		gap: 4px;
	}

	.editor-btn {
		flex: 1;
		padding: 4px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: 0.7rem;
		font-weight: 600;
		cursor: pointer;
		transition: background var(--transition-fast);
	}

	.save-btn {
		background: rgba(34, 197, 94, 0.2);
		color: #22c55e;
		border-color: rgba(34, 197, 94, 0.4);
	}

	.save-btn:hover {
		background: rgba(34, 197, 94, 0.3);
	}

	.cancel-btn {
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}

	.cancel-btn:hover {
		background: var(--bg-hover);
	}
</style>
