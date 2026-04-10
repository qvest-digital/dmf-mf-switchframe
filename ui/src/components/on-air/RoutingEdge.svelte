<script lang="ts">
	interface Props {
		x1: number;
		y1: number;
		x2: number;
		y2: number;
		active: boolean;
		isProgram: boolean;
	}

	let { x1, y1, x2, y2, active, isProgram }: Props = $props();

	// Bezier control points: offset 40% of the horizontal distance
	const cx = $derived((x2 - x1) * 0.4);
</script>

<defs>
	<marker
		id="arrowhead-{x1}-{y1}-{x2}-{y2}"
		markerWidth="6"
		markerHeight="4"
		refX="5"
		refY="2"
		orient="auto"
	>
		<polygon
			points="0 0, 6 2, 0 4"
			fill={active ? '#06B6D4' : 'var(--border-subtle, #333)'}
		/>
	</marker>
</defs>

<path
	d="M{x1},{y1} C{x1 + cx},{y1} {x2 - cx},{y2} {x2},{y2}"
	class="routing-edge"
	class:edge-active={active}
	class:edge-inactive={!active}
	stroke-width={isProgram ? 2 : 1}
	marker-end="url(#arrowhead-{x1}-{y1}-{x2}-{y2})"
/>

<style>
	.routing-edge {
		fill: none;
		transition: stroke 150ms;
	}

	.routing-edge.edge-active {
		stroke: #06B6D4;
	}

	.routing-edge.edge-inactive {
		stroke: var(--border-subtle, #333);
		stroke-dasharray: 4 4;
	}
</style>
