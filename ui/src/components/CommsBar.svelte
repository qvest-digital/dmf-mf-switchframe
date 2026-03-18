<script lang="ts">
	import type { CommsState } from '$lib/api/types';
	import { commsJoin, commsLeave, commsMute, apiCall } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';

	interface Props {
		commsState?: CommsState;
		operatorId: string;
		operatorName: string;
		visible: boolean;
		onToggle: () => void;
	}

	let { commsState, operatorId, operatorName, visible, onToggle }: Props = $props();

	const isJoined = $derived(
		(commsState?.participants ?? []).some((p) => p.operatorId === operatorId)
	);
	const selfParticipant = $derived(
		(commsState?.participants ?? []).find((p) => p.operatorId === operatorId)
	);
	const isMuted = $derived(selfParticipant?.muted ?? true);

	function handleMuteToggle() {
		apiCall(commsMute(operatorId, !isMuted), 'Comms mute');
	}

	let joining = $state(false);

	async function handleJoin() {
		if (!operatorId || !operatorName) {
			notify('Register as an operator first to use comms', 'error');
			return;
		}
		joining = true;
		try {
			await commsJoin(operatorId, operatorName);
		} catch (e) {
			notify(`Failed to join comms: ${e}`, 'error');
		} finally {
			joining = false;
		}
	}

	function handleLeave() {
		apiCall(commsLeave(operatorId), 'Leave comms');
	}
</script>

{#if visible && !isJoined}
	<div class="comms-bar comms-join-bar">
		<span class="comms-label">COMMS</span>
		<button
			class="comms-btn join-btn"
			onclick={handleJoin}
			disabled={joining || !operatorId}
			title={!operatorId ? 'Register as an operator first' : 'Join voice comms'}
		>
			{joining ? 'JOINING...' : 'JOIN'}
		</button>
		<span class="join-hint">
			{!operatorId ? 'Register as an operator to use comms' : 'Click to join operator voice channel'}
		</span>
	</div>
{:else if visible && isJoined}
	<div class="comms-bar">
		<span class="comms-label">COMMS</span>

		<button
			class="comms-btn mute-btn"
			class:muted={isMuted}
			onclick={handleMuteToggle}
			title={isMuted ? 'Unmute microphone' : 'Mute microphone'}
		>
			{isMuted ? 'UNMUTE' : 'MUTE'}
		</button>

		<div class="participants">
			{#each commsState?.participants ?? [] as participant}
				<span
					class="participant"
					class:speaking={participant.speaking}
					class:muted={participant.muted}
					title="{participant.name}{participant.operatorId === operatorId ? ' (you)' : ''} - {participant.muted ? 'muted' : 'unmuted'}"
				>
					<span class="participant-dot"></span>
					<span class="participant-name">
						{participant.name}{#if participant.operatorId === operatorId} <span class="you-suffix">(you)</span>{/if}
					</span>
				</span>
			{/each}
		</div>

		<button
			class="comms-btn leave-btn"
			onclick={handleLeave}
			title="Leave comms"
		>
			LEAVE
		</button>
	</div>
{/if}

<style>
	.comms-bar {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 10px;
		background: var(--bg-elevated);
		border-bottom: 1px solid var(--border-default);
		font-family: var(--font-ui);
		font-size: 11px;
	}

	.comms-label {
		font-weight: 700;
		font-size: 10px;
		letter-spacing: 0.06em;
		color: var(--text-muted);
		user-select: none;
	}

	.comms-btn {
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.04em;
		cursor: pointer;
		transition:
			background 0.15s,
			border-color 0.15s,
			color 0.15s;
	}

	.comms-btn:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
	}

	.mute-btn.muted {
		background: var(--color-red, #f87171);
		border-color: var(--color-red, #f87171);
		color: #fff;
	}

	.mute-btn.muted:hover {
		background: #ef4444;
		border-color: #ef4444;
	}

	.leave-btn {
		margin-left: auto;
	}

	.leave-btn:hover {
		border-color: var(--color-red, #f87171);
		color: var(--color-red, #f87171);
	}

	.participants {
		display: flex;
		align-items: center;
		gap: 8px;
		flex: 1;
		min-width: 0;
		overflow-x: auto;
	}

	.participant {
		display: flex;
		align-items: center;
		gap: 4px;
		white-space: nowrap;
	}

	.participant-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--color-green, #4ade80);
		flex-shrink: 0;
	}

	.participant.muted .participant-dot {
		background: transparent;
		border: 1px solid var(--text-muted);
	}

	.participant.speaking .participant-dot {
		background: var(--color-green, #4ade80);
		box-shadow: 0 0 4px var(--color-green, #4ade80);
	}

	.participant-name {
		color: var(--text-secondary);
		font-size: 11px;
	}

	.you-suffix {
		color: var(--text-muted);
		font-size: 10px;
	}

	.join-btn {
		background: var(--color-green, #4ade80);
		border-color: var(--color-green, #4ade80);
		color: #fff;
	}

	.join-btn:hover:not(:disabled) {
		background: #22c55e;
		border-color: #22c55e;
	}

	.join-btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.join-hint {
		color: var(--text-muted);
		font-size: 10px;
	}
</style>
