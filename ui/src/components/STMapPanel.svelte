<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';

	interface Props {
		state: ControlRoomState;
	}

	let { state: crState }: Props = $props();
</script>

<div class="stmap-panel">
	<p class="placeholder">ST Map panel (loading...)</p>
</div>

<style>
	.stmap-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 6px;
		height: 100%;
		overflow-y: auto;
	}

	.placeholder {
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		text-align: center;
		padding: 16px;
	}
</style>
