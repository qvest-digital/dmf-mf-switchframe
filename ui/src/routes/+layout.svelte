<script lang="ts">
	import '../app.css';
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { version } from '$app/environment';
	import { tabStorage, watchStaleBundle } from '$lib/util/stale-bundle';

	interface Props {
		children: Snippet;
	}

	let { children }: Props = $props();

	// Stays until the page reloads: a tab on an old build keeps that build's
	// bugs, so the operator is told for as long as it lasts.
	let staleBuild = $state(false);

	onMount(() =>
		watchStaleBundle({
			current: version,
			fetchVersion: async () => {
				const res = await fetch('/_app/version.json', { cache: 'no-store' });
				if (!res.ok) throw new Error(`HTTP ${res.status}`);
				return (await res.json()).version;
			},
			reload: () => location.reload(),
			prompt: () => { staleBuild = true; },
			storage: tabStorage(),
		}),
	);
</script>

{#if staleBuild}
	<div class="stale-build" role="alert" data-testid="stale-build-banner">
		<span>A newer version of this page is available.</span>
		<button type="button" onclick={() => location.reload()}>Reload</button>
	</div>
{/if}
{@render children()}

<style>
	.stale-build {
		position: fixed;
		top: 0;
		left: 50%;
		transform: translateX(-50%);
		z-index: 10000;
		display: flex;
		gap: 12px;
		align-items: center;
		padding: 6px 12px;
		border-radius: 0 0 6px 6px;
		background: #8a5a00;
		color: #fff;
		font-size: 13px;
	}
	.stale-build button {
		padding: 2px 10px;
		border: 1px solid #fff;
		border-radius: 4px;
		background: transparent;
		color: inherit;
		cursor: pointer;
	}
</style>
