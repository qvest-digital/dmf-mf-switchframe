<script lang="ts">
	import type { OperatorRole } from '$lib/api/types';
	import { register } from '$lib/state/operator.svelte';
	import { notify } from '$lib/state/notifications.svelte';

	let { onRegistered }: { onRegistered: () => void } = $props();

	let name = $state('');
	let role = $state<OperatorRole>('director');
	let submitting = $state(false);
	let error = $state('');

	async function handleSubmit() {
		if (!name.trim()) {
			error = 'Name is required';
			return;
		}
		submitting = true;
		error = '';
		try {
			await register(name.trim(), role);
			notify('info', `Registered as ${name.trim()} (${role})`);
			onRegistered();
		} catch (err) {
			error = err instanceof Error ? err.message : 'Registration failed';
		} finally {
			submitting = false;
		}
	}
</script>

<div class="overlay">
	<div class="dialog" role="dialog" aria-label="Operator Registration">
		<h2>Operator Registration</h2>
		<p class="subtitle">Register to control the switcher</p>

		<form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }}>
			<label class="field">
				<span class="field-label">Name</span>
				<input
					type="text"
					bind:value={name}
					placeholder="Your name"
					autocomplete="off"
					disabled={submitting}
				/>
			</label>

			<label class="field">
				<span class="field-label">Role</span>
				<select bind:value={role} disabled={submitting}>
					<option value="director">Director</option>
					<option value="audio">Audio</option>
					<option value="graphics">Graphics</option>
					<option value="viewer">Viewer</option>
				</select>
			</label>

			{#if error}
				<p class="error">{error}</p>
			{/if}

			<button type="submit" disabled={submitting}>
				{submitting ? 'Registering...' : 'Register'}
			</button>
		</form>

		<p class="hint">Viewers can observe but not control. Other roles can command their subsystem.</p>
	</div>
</div>

<style>
	.overlay {
		position: fixed;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		background: rgba(0, 0, 0, 0.85);
		z-index: 10000;
	}

	.dialog {
		background: var(--bg-surface, #1e1e1e);
		border: 1px solid var(--border-subtle, #444);
		border-radius: 8px;
		padding: 24px 32px;
		min-width: 320px;
		max-width: 400px;
	}

	h2 {
		margin: 0 0 4px;
		color: var(--text-primary, #eee);
		font-size: 18px;
	}

	.subtitle {
		margin: 0 0 16px;
		color: var(--text-secondary, #999);
		font-size: 13px;
	}

	form {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.field {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.field-label {
		color: var(--text-secondary, #aaa);
		font-size: 12px;
		text-transform: uppercase;
		letter-spacing: 0.5px;
	}

	input, select {
		padding: 8px 10px;
		border: 1px solid var(--border-subtle, #555);
		border-radius: 4px;
		background: var(--bg-base, #111);
		color: var(--text-primary, #eee);
		font-size: 14px;
	}

	select {
		cursor: pointer;
	}

	button {
		margin-top: 4px;
		padding: 10px 16px;
		border: none;
		border-radius: 4px;
		background: #2563eb;
		color: #fff;
		font-size: 14px;
		cursor: pointer;
	}

	button:hover:not(:disabled) {
		background: #1d4ed8;
	}

	button:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.error {
		margin: 0;
		color: #ef4444;
		font-size: 13px;
	}

	.hint {
		margin: 16px 0 0;
		color: var(--text-secondary, #777);
		font-size: 11px;
		text-align: center;
	}
</style>
