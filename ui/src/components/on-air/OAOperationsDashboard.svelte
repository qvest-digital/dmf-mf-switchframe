<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import OASlatesSection from './OASlatesSection.svelte';
	import OABugSection from './OABugSection.svelte';
	import OAClipsSection from './OAClipsSection.svelte';
	import OAPlayoutSection from './OAPlayoutSection.svelte';

	interface SlateInfo {
		clipId: string;
		name: string;
		isOnAir?: boolean;
	}

	interface BugInfo {
		id: string;
		name: string;
	}

	interface ClipItem {
		id: string;
		name: string;
		durationMs?: number;
	}

	interface Props {
		crState: ControlRoomState;
		onCut?: (source: string) => void;
		onBugToggle?: () => void;
	}

	let { crState, onCut, onBugToggle }: Props = $props();

	// Section state (stub data for now, wired to API in Task 6)
	let slates = $state<SlateInfo[]>([]);
	let bugs = $state<BugInfo[]>([]);
	let activeBugId = $state<string | undefined>();
	let clips = $state<ClipItem[]>([]);
	let playoutEnabled = $state(false);
	let uploadProgress = $state<{ filename: string; percent: number } | null>(null);
</script>

<div class="operations-dashboard">
	<!-- SLATES section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">SLATES</span>
		</header>
		<div class="section-content">
			<OASlatesSection
				{slates}
				onFireSlate={(clipId, name) => { /* TODO: wire in Task 6 */ }}
				onAddSlate={() => { /* TODO: wire in Task 6 */ }}
				onRemoveSlate={(clipId) => { /* TODO: wire in Task 6 */ }}
			/>
		</div>
	</section>

	<!-- BUG section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">BUG</span>
		</header>
		<div class="section-content">
			<OABugSection
				{bugs}
				{activeBugId}
				onSelectBug={(id) => { activeBugId = id; }}
				onUploadBug={(files) => { /* TODO: wire in Task 6 */ }}
			/>
		</div>
	</section>

	<!-- CLIPS section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">CLIPS</span>
		</header>
		<div class="section-content">
			<OAClipsSection
				{clips}
				{uploadProgress}
				onUploadClip={(files) => { /* TODO: wire in Task 6 */ }}
				onMakeSlate={(clipId, name) => { /* TODO: wire in Task 6 */ }}
				onDeleteClip={(id) => { /* TODO: wire in Task 6 */ }}
			/>
		</div>
	</section>

	<!-- PLAYOUT section -->
	<section class="dashboard-section">
		<header class="section-header">
			<span class="section-title">PLAYOUT</span>
		</header>
		<div class="section-content">
			<OAPlayoutSection
				enabled={playoutEnabled}
				onToggle={(v) => { playoutEnabled = v; }}
				onNewSchedule={() => { /* TODO: wire in Task 6 */ }}
				onLoadFile={(f) => { /* TODO: wire in Task 6 */ }}
				onLoadSaved={(n) => { /* TODO: wire in Task 6 */ }}
			/>
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
</style>
