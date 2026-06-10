<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';

	type PreauthKey = {
		key: string; label: string; reusable: boolean;
		expiry: string; used_at: string | null; created_at: string;
	};

	let keys = $state<PreauthKey[]>([]);
	let loading = $state(true);
	let creating = $state(false);
	let showForm = $state(false);
	let newLabel = $state('');
	let newTTL = $state(24);
	let newReusable = $state(false);
	let copiedKey = $state<string | null>(null);

	async function load() {
		const r = await fetch('/api/v1/preauth-keys');
		if (r.status === 401) { goto('/login'); return; }
		keys = await r.json();
		loading = false;
	}

	onMount(load);

	async function createKey() {
		creating = true;
		const res = await fetch('/api/v1/preauth-keys', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ label: newLabel, ttl_hours: newTTL, reusable: newReusable }),
		});
		if (res.ok) {
			newLabel = '';
			newTTL = 24;
			newReusable = false;
			showForm = false;
			await load();
		}
		creating = false;
	}

	async function deleteKey(key: string) {
		if (!confirm('Delete this preauth key?')) return;
		await fetch(`/api/v1/preauth-keys/${encodeURIComponent(key)}`, { method: 'DELETE' });
		keys = keys.filter(k => k.key !== key);
	}

	async function copyKey(key: string) {
		await navigator.clipboard.writeText(key);
		copiedKey = key;
		setTimeout(() => { copiedKey = null; }, 2000);
	}

	function formatExpiry(dateStr: string): string {
		const d = new Date(dateStr);
		const diff = d.getTime() - Date.now();
		if (diff < 0) return 'expired';
		const h = Math.floor(diff / 3_600_000);
		if (h < 24) return `${h}h left`;
		return `${Math.floor(h / 24)}d left`;
	}

	function isExpired(dateStr: string): boolean {
		return new Date(dateStr).getTime() < Date.now();
	}
</script>

<svelte:head><title>Preauth Keys · Zeta</title></svelte:head>

<div class="top-bar">
	<div>
		<div class="page-title">Preauth Keys</div>
		<div class="page-sub">One-time or reusable enrollment tokens</div>
	</div>
	<button class="btn btn-primary" onclick={() => showForm = !showForm}>
		{showForm ? 'Cancel' : '+ New Key'}
	</button>
</div>

{#if showForm}
	<div class="card" style="margin-bottom:1.5rem">
		<div class="section-title" style="margin-bottom:1rem">New Preauth Key</div>
		<div class="form-row">
			<div class="field">
				<label for="label">Label</label>
				<input id="label" type="text" placeholder="e.g. laptop" bind:value={newLabel} style="width:200px" />
			</div>
			<div class="field">
				<label for="ttl">Expires in (hours)</label>
				<input id="ttl" type="number" min="1" max="8760" bind:value={newTTL} style="width:120px" />
			</div>
			<div class="field" style="justify-content:flex-end">
				<label style="display:flex;align-items:center;gap:0.5rem;cursor:pointer">
					<input type="checkbox" bind:checked={newReusable} style="width:auto;padding:0" />
					Reusable
				</label>
			</div>
			<button class="btn btn-primary" onclick={createKey} disabled={creating}>
				{creating ? 'Creating…' : 'Create'}
			</button>
		</div>
	</div>
{/if}

{#if loading}
	<div class="loading">Loading…</div>
{:else if keys.length === 0}
	<div class="table-wrap">
		<div class="empty">
			<div class="empty-icon">🔑</div>
			<p>No preauth keys yet.</p>
			<p style="margin-top:0.5rem">Create one to enroll devices headlessly.</p>
		</div>
	</div>
{:else}
	<div class="table-wrap">
		<table>
			<thead>
				<tr>
					<th>Key</th>
					<th>Label</th>
					<th>Type</th>
					<th>Expires</th>
					<th>Used</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each keys as k}
					<tr style={isExpired(k.expiry) ? 'opacity:0.5' : ''}>
						<td class="mono" style="max-width:200px">
							<div style="display:flex;align-items:center;gap:0.5rem">
								<span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:140px">{k.key}</span>
								<button
									class="btn btn-ghost btn-sm"
									style="padding:0.15rem 0.5rem;font-size:0.7rem;flex-shrink:0"
									onclick={() => copyKey(k.key)}
								>
									{copiedKey === k.key ? '✓' : 'copy'}
								</button>
							</div>
						</td>
						<td style="font-weight:500">{k.label || '—'}</td>
						<td>
							{#if k.reusable}
								<span class="badge badge-reusable">reusable</span>
							{:else}
								<span class="badge badge-offline">one-time</span>
							{/if}
						</td>
						<td class="sub">{formatExpiry(k.expiry)}</td>
						<td class="sub">{k.used_at ? 'yes' : 'no'}</td>
						<td>
							<button class="btn btn-danger btn-sm" onclick={() => deleteKey(k.key)}>Delete</button>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
