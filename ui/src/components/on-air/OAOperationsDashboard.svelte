<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';

	interface Props {
		crState: ControlRoomState;
		onCut?: (source: string) => void;
		onBugToggle?: () => void;
	}

	let { crState, onCut, onBugToggle }: Props = $props();
</script>

<div class="operations-dashboard">
	<!-- SLATES section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">SLATES</span>
		</header>
		<div class="section-content">
			<p class="section-placeholder">No slates configured</p>
		</div>
	</section>

	<!-- BUG section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">BUG</span>
		</header>
		<div class="section-content">
			<p class="section-placeholder">No bug loaded</p>
		</div>
	</section>

	<!-- CLIPS section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">CLIPS</span>
		</header>
		<div class="section-content">
			<p class="section-placeholder">No clips uploaded</p>
		</div>
	</section>

	<!-- PLAYOUT section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">PLAYOUT</span>
		</header>
		<div class="section-content">
			<p class="section-placeholder">Playout not enabled</p>
		</div>
	</section>
</div>

<style>
	.operations-dashboard {
		flex: 1;
		overflow-y: auto;
		padding: 0;
	}

	.dashboard-section {
		border-bottom: 1px solid var(--border-default);
	}

	.section-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 12px;
		background: var(--bg-surface);
	}

	.section-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
		text-transform: uppercase;
	}

	.section-content {
		padding: 8px 12px;
	}

	.section-placeholder {
		font-size: var(--text-sm);
		color: var(--text-secondary);
		margin: 0;
	}
</style>
