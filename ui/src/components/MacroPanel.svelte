<script lang="ts">
	import type { Macro, MacroStep } from '$lib/api/types';
	import { listMacros, saveMacro, deleteMacro, runMacro, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	let macros = $state<Macro[]>([]);
	let runningMacro = $state<string | null>(null);
	let editingMacro = $state<Macro | null>(null);
	let editorJson = $state('');
	let editorError = $state('');

	async function loadMacros() {
		try {
			macros = await listMacros();
		} catch {
			// ignore — macros may not be configured
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

	function startEdit(m?: Macro) {
		if (m) {
			editingMacro = m;
			editorJson = JSON.stringify(m, null, 2);
		} else {
			editingMacro = { name: '', steps: [{ action: 'cut', params: { source: '' } }] };
			editorJson = JSON.stringify(editingMacro, null, 2);
		}
		editorError = '';
	}

	async function handleSave() {
		editorError = '';
		try {
			const parsed = JSON.parse(editorJson) as Macro;
			if (!parsed.name?.trim()) {
				editorError = 'Name is required';
				return;
			}
			if (!parsed.steps?.length) {
				editorError = 'At least one step is required';
				return;
			}
			await saveMacro(parsed);
			editingMacro = null;
			await loadMacros();
			notify('info', `Macro "${parsed.name}" saved`);
		} catch (err) {
			editorError = err instanceof SyntaxError ? 'Invalid JSON' : (err instanceof Error ? err.message : 'Save failed');
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
		editingMacro = null;
		editorError = '';
	}

	// Load macros on mount
	$effect(() => {
		loadMacros();
	});
</script>

<div class="macro-panel">
	<div class="macro-header">
		<span class="macro-title">MACROS</span>
		<button class="add-btn" onclick={() => startEdit()} title="New macro">+</button>
	</div>

	{#if editingMacro !== null}
		<div class="macro-editor">
			<textarea
				class="editor-textarea"
				bind:value={editorJson}
				rows="8"
				spellcheck="false"
			></textarea>
			{#if editorError}
				<div class="editor-error">{editorError}</div>
			{/if}
			<div class="editor-buttons">
				<button class="editor-btn save-btn" onclick={handleSave}>Save</button>
				<button class="editor-btn cancel-btn" onclick={cancelEdit}>Cancel</button>
			</div>
		</div>
	{/if}

	<div class="macro-grid">
		{#each macros as m (m.name)}
			<div class="macro-item">
				<button
					class="macro-btn"
					class:running={runningMacro === m.name}
					disabled={runningMacro !== null}
					onclick={() => handleRun(m.name)}
					title="Run macro: {m.name}"
				>
					{#if runningMacro === m.name}
						<span class="spinner"></span>
					{/if}
					{m.name}
				</button>
				<div class="macro-actions">
					<button class="action-btn" onclick={() => startEdit(m)} title="Edit">E</button>
					<button class="action-btn del-btn" onclick={() => handleDelete(m.name)} title="Delete">X</button>
				</div>
			</div>
		{/each}
	</div>

	{#if macros.length === 0 && editingMacro === null}
		<div class="empty-state">No macros. Click + to create one.</div>
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

	.add-btn {
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-size: 0.85rem;
		cursor: pointer;
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		line-height: 1;
	}

	.add-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

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
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast);
		display: flex;
		align-items: center;
		gap: 4px;
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
	}

	.action-btn:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.del-btn:hover {
		color: #ef4444;
	}

	.macro-editor {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 4px;
		background: var(--bg-panel);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.editor-textarea {
		width: 100%;
		font-family: var(--font-mono);
		font-size: 0.65rem;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		padding: 4px;
		resize: vertical;
	}

	.editor-error {
		color: #ef4444;
		font-size: 0.65rem;
		font-family: var(--font-ui);
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

	.empty-state {
		text-align: center;
		color: var(--text-tertiary);
		font-size: 0.7rem;
		padding: 12px 4px;
	}
</style>
