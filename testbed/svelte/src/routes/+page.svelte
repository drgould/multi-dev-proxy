<script lang="ts">
	import { onMount } from 'svelte';

	let port = $state('—');
	let clock = $state(new Date().toLocaleTimeString());
	let counter = $state(0);
	let search = $state('');

	const items = ['Apple', 'Banana', 'Cherry', 'Date', 'Elderberry', 'Fig', 'Grape'];
	let filtered = $derived(
		search.trim() === ''
			? items
			: items.filter((item) => item.toLowerCase().includes(search.toLowerCase()))
	);

	onMount(() => {
		port = window.location.port || '—';
		const timer = setInterval(() => {
			clock = new Date().toLocaleTimeString();
		}, 1000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>SvelteKit — mdp testbed</title>
</svelte:head>

<div class="page">
	<h1>SvelteKit + TypeScript</h1>

	<p class="meta">
		Port <strong class="port">{port}</strong>
		&middot;
		{clock}
	</p>

	<!-- Counter -->
	<div class="card">
		<h2>Counter</h2>
		<div class="counter-row">
			<button class="counter-btn" onclick={() => counter--}>&minus;</button>
			<span class="counter-val">{counter}</span>
			<button class="counter-btn" onclick={() => counter++}>+</button>
		</div>
	</div>

	<!-- Filterable List -->
	<div class="card list-card">
		<h2>Fruit Filter</h2>
		<p class="hint">Type to filter the list below</p>
		<input
			type="text"
			class="search-input"
			placeholder="Search fruits..."
			bind:value={search}
		/>
		<ul class="fruit-list">
			{#each filtered as item}
				<li class="fruit-item">{item}</li>
			{:else}
				<li class="fruit-empty">No matches</li>
			{/each}
		</ul>
	</div>

	<p class="footer">
		If mdp is working, you should see a floating switcher widget at the top of this page.
	</p>
</div>

<style>
	:global(body) {
		margin: 0;
		padding: 0;
	}

	.page {
		min-height: 100vh;
		background: linear-gradient(135deg, #3d0a0a 0%, #b91c1c 100%);
		color: #fecaca;
		font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
		display: flex;
		flex-direction: column;
		align-items: center;
		padding: 60px 20px 40px;
	}

	h1 {
		font-size: 2.4rem;
		font-weight: 700;
		margin: 0 0 8px;
		color: #fca5a5;
		letter-spacing: -0.5px;
	}

	.meta {
		margin: 0 0 32px;
		opacity: 0.6;
		font-size: 0.9rem;
	}

	.port {
		color: #f87171;
	}

	.card {
		background: rgba(0, 0, 0, 0.25);
		border: 1px solid rgba(255, 255, 255, 0.08);
		border-radius: 12px;
		padding: 24px 32px;
		margin-bottom: 24px;
		text-align: center;
		min-width: 300px;
	}

	.card h2 {
		margin: 0 0 16px;
		font-size: 1.1rem;
		color: #fb923c;
	}

	.counter-row {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 16px;
	}

	.counter-btn {
		width: 40px;
		height: 40px;
		border-radius: 8px;
		border: 1px solid rgba(255, 255, 255, 0.15);
		background: rgba(255, 255, 255, 0.06);
		color: #fecaca;
		font-size: 1.4rem;
		cursor: pointer;
		transition: background 0.15s;
	}

	.counter-btn:hover {
		background: rgba(255, 255, 255, 0.12);
	}

	.counter-val {
		font-size: 2rem;
		font-weight: 700;
		min-width: 60px;
		color: #f87171;
	}

	.hint {
		font-size: 0.8rem;
		opacity: 0.4;
		margin: 0 0 12px;
	}

	.list-card {
		text-align: left;
		max-width: 380px;
		width: 100%;
	}

	.list-card h2,
	.list-card .hint {
		text-align: center;
	}

	.search-input {
		width: 100%;
		padding: 10px 12px;
		border-radius: 8px;
		border: 1px solid rgba(255, 255, 255, 0.15);
		background: rgba(255, 255, 255, 0.06);
		color: #fecaca;
		font-family: inherit;
		font-size: 0.9rem;
		outline: none;
		margin-bottom: 12px;
		box-sizing: border-box;
	}

	.search-input::placeholder {
		color: rgba(254, 202, 202, 0.3);
	}

	.search-input:focus {
		border-color: rgba(248, 113, 113, 0.4);
	}

	.fruit-list {
		list-style: none;
		padding: 0;
		margin: 0;
	}

	.fruit-item {
		padding: 8px 12px;
		background: rgba(255, 255, 255, 0.05);
		border-radius: 6px;
		margin-bottom: 4px;
		transition: background 0.15s;
	}

	.fruit-item:hover {
		background: rgba(255, 255, 255, 0.1);
	}

	.fruit-empty {
		padding: 8px 12px;
		opacity: 0.4;
		font-style: italic;
	}

	.footer {
		margin-top: auto;
		padding-top: 40px;
		font-size: 0.8rem;
		opacity: 0.4;
		text-align: center;
		max-width: 400px;
		line-height: 1.5;
	}
</style>
