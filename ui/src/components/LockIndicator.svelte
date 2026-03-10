<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { getSession } from '$lib/state/operator.svelte';
	import { operatorLock, operatorUnlock, operatorForceUnlock, apiCall } from '$lib/api/switch-api';

	let { state: crState, subsystem }: { state: ControlRoomState; subsystem: string } = $props();

	const session = $derived(getSession());
	const lockInfo = $derived(crState.locks?.[subsystem]);
	const isLocked = $derived(lockInfo !== undefined);
	const isOwnLock = $derived(isLocked && session !== null && lockInfo?.holderId === session.id);
	const isOtherLock = $derived(isLocked && !isOwnLock);
	const isDirector = $derived(session?.role === 'director');

	function toggleLock() {
		if (!session) return;
		if (isOwnLock) {
			apiCall(operatorUnlock(subsystem), 'Unlock failed');
		} else if (!isLocked) {
			apiCall(operatorLock(subsystem), 'Lock failed');
		} else if (isDirector) {
			apiCall(operatorForceUnlock(subsystem), 'Force unlock failed');
		}
	}
</script>

{#if session && (crState.operators?.length ?? 0) > 0}
	<button
		class="lock-btn"
		class:locked-self={isOwnLock}
		class:locked-other={isOtherLock}
		class:unlocked={!isLocked}
		onclick={toggleLock}
		disabled={isOtherLock && !isDirector}
		title={isOwnLock
			? `Locked by you - click to unlock`
			: isOtherLock
				? `Locked by ${lockInfo?.holderName}${isDirector ? ' - click to force unlock' : ''}`
				: `Click to lock ${subsystem}`}
		aria-label="{subsystem} lock"
	>
		{#if isOwnLock}
			<svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
				<path d="M4 7V5a4 4 0 1 1 8 0v2h1a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1h1zm2 0h4V5a2 2 0 1 0-4 0v2z"/>
			</svg>
		{:else if isOtherLock}
			<svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
				<path d="M4 7V5a4 4 0 1 1 8 0v2h1a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1h1zm2 0h4V5a2 2 0 1 0-4 0v2z"/>
			</svg>
		{:else}
			<svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
				<path d="M10 7V5a2 2 0 1 0-4 0v2H4V5a4 4 0 1 1 8 0v2h1a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1h7z"/>
			</svg>
		{/if}
	</button>
{/if}

<style>
	.lock-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 20px;
		height: 20px;
		border: none;
		border-radius: 3px;
		cursor: pointer;
		padding: 0;
		background: transparent;
	}

	.lock-btn.unlocked {
		color: var(--text-secondary, #666);
	}

	.lock-btn.unlocked:hover {
		color: var(--text-primary, #ccc);
		background: rgba(255, 255, 255, 0.05);
	}

	.lock-btn.locked-self {
		color: var(--color-success);
	}

	.lock-btn.locked-self:hover {
		background: rgba(34, 197, 94, 0.1);
	}

	.lock-btn.locked-other {
		color: var(--color-error);
	}

	.lock-btn.locked-other:not(:disabled):hover {
		background: rgba(239, 68, 68, 0.1);
	}

	.lock-btn:disabled {
		cursor: not-allowed;
		opacity: 0.5;
	}
</style>
