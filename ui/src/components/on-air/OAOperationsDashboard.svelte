<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import {
		listClips, deleteClip, uploadClip,
		clipPlayerLoad, clipPlayerPlay,
		cut,
		graphicsImageUpload, graphicsAddLayer,
		playoutSavedList, playoutImportBxf,
	} from '$lib/api/switch-api';
	import { resolveApiUrl } from '$lib/api/base-url';
	import OASlatesSection from './OASlatesSection.svelte';
	import OABugSection from './OABugSection.svelte';
	import OAClipsSection from './OAClipsSection.svelte';
	import OAPlayoutSection from './OAPlayoutSection.svelte';

	interface SlateInfo {
		clipId: string;
		name: string;
		isOnAir?: boolean;
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
		playoutChannel?: unknown;
		playlistItems?: unknown[];
		onShowAsRun?: () => void;
		onPlayoutToggle?: (enabled: boolean) => void;
	}

	let { crState, onCut, onBugToggle, playoutChannel, playlistItems, onShowAsRun, onPlayoutToggle }: Props = $props();

	// ── Slates (localStorage-backed) ──

	const SLATES_KEY = 'switchframe-slates';

	function loadSlates(): SlateInfo[] {
		try {
			return JSON.parse(localStorage.getItem(SLATES_KEY) ?? '[]');
		} catch { return []; }
	}

	function saveSlates(s: SlateInfo[]) {
		localStorage.setItem(SLATES_KEY, JSON.stringify(s));
	}

	let slates = $state<SlateInfo[]>(loadSlates());

	async function fireSlate(clipId: string, name: string) {
		const players = crState.clipPlayers ?? [];
		let playerIdx = players.findIndex(p => !p.clipId || p.state === 'empty' || p.state === 'loaded');
		if (playerIdx < 0) playerIdx = 0; // fallback to player 1
		const playerNum = playerIdx + 1;

		try {
			await clipPlayerLoad(playerNum, clipId);
			await clipPlayerPlay(playerNum, 1, true);
			await cut(`clip:${playerNum}`);
			slates = slates.map(s => ({ ...s, isOnAir: s.clipId === clipId }));
		} catch {
			// silently handle error
		}
	}

	function makeSlate(clipId: string, clipName: string) {
		const name = window.prompt('Slate name:', clipName);
		if (!name) return;
		slates = [...slates, { clipId, name }];
		saveSlates(slates);
	}

	function removeSlate(clipId: string) {
		slates = slates.filter(s => s.clipId !== clipId);
		saveSlates(slates);
	}

	function addSlate() {
		if (clips.length === 0) return;
		const name = window.prompt('Slate name:');
		if (!name) return;
		const available = clips.filter(c => !slates.some(s => s.clipId === c.id));
		if (available.length === 0) return;
		slates = [...slates, { clipId: available[0].id, name }];
		saveSlates(slates);
	}

	// ── Bugs (derived from graphics layers) ──

	const bugs = $derived(
		(crState.graphics?.layers ?? [])
			.filter(l => l.sequenceLoaded || l.imageName)
			.map(l => ({ id: String(l.id), name: l.imageName || `Layer ${l.id}` }))
	);

	let activeBugId = $state<string | undefined>(
		localStorage.getItem('switchframe-active-bug') ?? undefined
	);

	function selectBug(id: string) {
		activeBugId = id;
		localStorage.setItem('switchframe-active-bug', id);
	}

	async function handleUploadBug(files: FileList) {
		if (files.length === 0) return;
		try {
			const { id: layerId } = await graphicsAddLayer();
			await graphicsImageUpload(layerId, files[0]);
		} catch {
			// silently handle error
		}
	}

	// ── Clips (API-backed) ──

	let clips = $state<ClipItem[]>([]);
	let uploadProgress = $state<{ filename: string; percent: number } | null>(null);

	$effect(() => {
		listClips().then(result => {
			clips = result.map(c => ({
				id: c.id,
				name: c.name,
				durationMs: c.durationMs,
			}));
		}).catch(() => {});
	});

	async function handleUploadClip(files: FileList) {
		const file = files[0];
		if (!file) return;
		uploadProgress = { filename: file.name, percent: 0 };

		try {
			await uploadClip(file, (pct) => {
				uploadProgress = { filename: file.name, percent: pct };
			});
			uploadProgress = { filename: file.name, percent: 100 };
			// Refresh clips list
			const result = await listClips();
			clips = result.map(c => ({
				id: c.id,
				name: c.name,
				durationMs: c.durationMs,
			}));
			setTimeout(() => { uploadProgress = null; }, 2000);
		} catch {
			uploadProgress = null;
		}
	}

	async function handleDeleteClip(id: string) {
		try {
			await deleteClip(id);
			clips = clips.filter(c => c.id !== id);
			// Also remove from slates if it was a slate
			slates = slates.filter(s => s.clipId !== id);
			saveSlates(slates);
		} catch {
			// silently handle error
		}
	}

	// ── Playout ──

	let playoutEnabled = $state(false);
	let savedPlaylists = $state<string[]>([]);

	$effect(() => {
		if (!playoutEnabled) return;
		playoutSavedList()
			.then(data => { savedPlaylists = data ?? []; })
			.catch(() => {});
	});

	function handlePlayoutToggle(enabled: boolean) {
		playoutEnabled = enabled;
		onPlayoutToggle?.(enabled);
	}

	async function handleNewSchedule() {
		// Empty playlist - just enable playout mode
		playoutEnabled = true;
		onPlayoutToggle?.(true);
	}

	async function handleLoadFile(file: File) {
		const text = await file.text();
		if (file.name.endsWith('.bxf') || file.name.endsWith('.xml')) {
			try {
				await playoutImportBxf(1, text);
			} catch {
				// silently handle error
			}
		} else {
			// JSON playlist
			try {
				await fetch(resolveApiUrl('/api/playout/channels/1/load'), {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: text,
				});
			} catch {
				// silently handle error
			}
		}
	}

	async function handleLoadSaved(name: string) {
		try {
			const playlist = await fetch(resolveApiUrl(`/api/playout/saved/${encodeURIComponent(name)}`));
			if (playlist.ok) {
				const data = await playlist.json();
				await fetch(resolveApiUrl('/api/playout/channels/1/load'), {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify(data),
				});
			}
		} catch {
			// silently handle error
		}
	}
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
				onFireSlate={fireSlate}
				onAddSlate={addSlate}
				onRemoveSlate={removeSlate}
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
				onSelectBug={selectBug}
				onUploadBug={handleUploadBug}
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
				onUploadClip={handleUploadClip}
				onMakeSlate={makeSlate}
				onDeleteClip={handleDeleteClip}
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
				onToggle={handlePlayoutToggle}
				onNewSchedule={handleNewSchedule}
				onLoadFile={handleLoadFile}
				onLoadSaved={handleLoadSaved}
				{savedPlaylists}
				{onShowAsRun}
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
