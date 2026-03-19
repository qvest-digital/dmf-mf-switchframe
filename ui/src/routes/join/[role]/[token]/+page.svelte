<script lang="ts">
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';

	const roleNames: Record<string, string> = {
		d: 'Director',
		a: 'Audio',
		g: 'Graphics',
		c: 'Captioner',
		v: 'Viewer',
	};

	const role = $derived($page.params.role);
	const token = $derived($page.params.token);
	const roleName = $derived(role ? roleNames[role] || 'Viewer' : 'Viewer');

	let name = $state('');
	let error = $state('');
	let joining = $state(false);

	function getCookie(cookieName: string): string | null {
		if (typeof document === 'undefined') return null;
		const match = document.cookie.match(new RegExp('(^| )' + cookieName + '=([^;]+)'));
		return match ? decodeURIComponent(match[2]) : null;
	}

	function setCookie(cookieName: string, value: string): void {
		document.cookie = `${cookieName}=${encodeURIComponent(value)};path=/;max-age=31536000;SameSite=Strict`;
	}

	async function join(operatorName: string): Promise<void> {
		joining = true;
		error = '';

		// Resume an AudioContext on this user gesture so autoplay is
		// pre-unlocked by the time the main session loads (client-side nav
		// preserves the gesture context). Store on window so the main page
		// can detect that audio is already unlocked.
		try {
			const ctx = new AudioContext();
			await ctx.resume();
			(window as any).__switchframe_audio_unlocked = true;
		} catch {
			// Non-fatal — audio will prompt later if needed.
		}

		try {
			const resp = await fetch('/api/operator/register', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name: operatorName, inviteToken: token }),
			});
			if (!resp.ok) {
				const data = await resp.json().catch(() => ({}));
				throw new Error(data.error || 'Failed to join session');
			}
			const data = await resp.json();
			localStorage.setItem('switchframe_operator_token', data.token);
			setCookie('switchframe_name', operatorName);
			goto('/');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to join session';
			joining = false;
		}
	}

	onMount(() => {
		const savedName = getCookie('switchframe_name');
		if (savedName) {
			join(savedName);
		}
	});

	function handleSubmit(): void {
		if (name.trim()) {
			join(name.trim());
		}
	}
</script>

<div class="join-page">
	{#if joining}
		<div class="joining">
			<h1>switchframe</h1>
			<p>Joining as {roleName}...</p>
		</div>
	{:else}
		<div class="join-form">
			<h1>switchframe</h1>
			<p class="role-label">You're joining as <strong>{roleName}</strong></p>

			{#if error}
				<p class="error">{error}</p>
			{/if}

			<form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }}>
				<!-- svelte-ignore a11y_autofocus -->
				<input
					type="text"
					bind:value={name}
					placeholder="Your name"
					maxlength={50}
					autofocus
				/>
				<button type="submit" disabled={!name.trim()}>Join Session</button>
			</form>
		</div>
	{/if}
</div>

<style>
	.join-page {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 100vh;
		background: var(--bg-base, #09090b);
		color: var(--text-primary, #e4e4e8);
		font-family: var(--font-ui, 'Sora', system-ui, sans-serif);
	}

	.joining {
		text-align: center;
	}

	.joining h1 {
		font-family: var(--font-mono, 'JetBrains Mono', monospace);
		font-size: 1.2rem;
		font-weight: 500;
		color: var(--text-secondary, #85858f);
		margin-bottom: 2rem;
	}

	.joining p {
		color: var(--text-secondary, #85858f);
		font-size: 0.95rem;
	}

	.join-form {
		text-align: center;
		max-width: 360px;
		padding: 2rem;
	}

	.join-form h1 {
		font-family: var(--font-mono, 'JetBrains Mono', monospace);
		font-size: 1.2rem;
		font-weight: 500;
		color: var(--text-secondary, #85858f);
		margin-bottom: 2rem;
	}

	.role-label {
		color: var(--text-secondary, #85858f);
		margin-bottom: 1.5rem;
		font-size: 0.95rem;
	}

	.role-label strong {
		color: var(--text-primary, #e4e4e8);
	}

	input {
		width: 100%;
		padding: 0.75rem 1rem;
		background: var(--bg-panel, #16161a);
		border: 1px solid var(--border-default, rgba(255, 255, 255, 0.09));
		border-radius: 6px;
		color: var(--text-primary, #e4e4e8);
		font-size: 0.95rem;
		font-family: inherit;
		outline: none;
		margin-bottom: 1rem;
	}

	input:focus {
		border-color: var(--border-focus, rgba(255, 255, 255, 0.22));
	}

	input::placeholder {
		color: var(--text-tertiary, #8b8b93);
	}

	button {
		width: 100%;
		padding: 0.75rem;
		background: var(--text-primary, #e4e4e8);
		color: var(--bg-base, #09090b);
		border: none;
		border-radius: 6px;
		font-size: 0.875rem;
		font-weight: 600;
		cursor: pointer;
		font-family: inherit;
	}

	button:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}

	button:hover:not(:disabled) {
		opacity: 0.9;
	}

	.error {
		color: var(--color-error, #ef4444);
		font-size: 0.85rem;
		margin-bottom: 1rem;
	}
</style>
