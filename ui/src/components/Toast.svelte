<script lang="ts">
	import { getNotifications, dismiss } from '$lib/state/notifications.svelte';

	let items = $derived(getNotifications());
</script>

<div class="toast-container" aria-live="polite">
	{#each items as notification (notification.id)}
		<div class="toast-item {notification.type}" role="alert">
			<span class="toast-message">{notification.message}</span>
			<button
				class="toast-dismiss"
				onclick={() => dismiss(notification.id)}
				aria-label="Dismiss notification"
			>&times;</button>
		</div>
	{/each}
</div>

<style>
	.toast-container {
		position: fixed;
		top: 12px;
		left: 50%;
		transform: translateX(-50%);
		z-index: 1000;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
		pointer-events: none;
	}

	.toast-item {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 8px 14px;
		border-radius: 6px;
		font-family: 'SF Mono', 'Fira Code', monospace;
		font-size: 13px;
		color: #fff;
		pointer-events: auto;
		animation: slideIn 0.3s ease-out;
		max-width: 500px;
	}

	.toast-item.error {
		background: #cc0000;
	}

	.toast-item.warning {
		background: #cc8822;
	}

	.toast-item.info {
		background: #333;
	}

	.toast-message {
		flex: 1;
	}

	.toast-dismiss {
		background: none;
		border: none;
		color: #fff;
		font-size: 18px;
		cursor: pointer;
		padding: 0 2px;
		line-height: 1;
		opacity: 0.8;
	}

	.toast-dismiss:hover {
		opacity: 1;
	}

	@keyframes slideIn {
		from {
			transform: translateY(-20px);
			opacity: 0;
		}
		to {
			transform: translateY(0);
			opacity: 1;
		}
	}
</style>
