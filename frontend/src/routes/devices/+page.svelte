<script lang="ts">
	import { onMount } from 'svelte';

	type Device = {
		id: string; hostname: string; os: string; mesh_ip: string;
		wg_public_key: string; last_endpoint: string; last_seen: string | null;
		agent_version: string; online: boolean; created_at: string;
	};

	let devices = $state<Device[]>([]);
	let loading = $state(true);
	let deleting = $state<string | null>(null);

	async function load() {
		const d = await fetch('/api/v1/devices').then(r => r.json());
		devices = d;
		loading = false;
	}

	onMount(() => {
		load();
		const t = setInterval(load, 15_000);
		return () => clearInterval(t);
	});

	async function deleteDevice(dev: Device) {
		if (!confirm(`Remove ${dev.hostname} from the mesh?\nThis cannot be undone.`)) return;
		deleting = dev.id;
		await fetch(`/api/v1/devices/${dev.id}`, { method: 'DELETE' });
		devices = devices.filter(d => d.id !== dev.id);
		deleting = null;
	}

	function timeAgo(dateStr: string | null): string {
		if (!dateStr) return 'never';
		const d = new Date(dateStr);
		const diff = Math.floor((Date.now() - d.getTime()) / 1000);
		if (diff < 60) return 'just now';
		if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
		if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
		return `${Math.floor(diff / 86400)}d ago`;
	}

	function truncate(s: string, n = 20): string {
		return s.length > n ? s.slice(0, n) + '…' : s;
	}

	const onlineCount = $derived(devices.filter(d => d.online).length);
</script>

<svelte:head><title>Devices · Zeta</title></svelte:head>

<div class="top-bar">
	<div>
		<div class="page-title">Devices</div>
		<div class="page-sub">{devices.length} total · {onlineCount} online</div>
	</div>
</div>

{#if loading}
	<div class="loading">Loading…</div>
{:else if devices.length === 0}
	<div class="table-wrap">
		<div class="empty">
			<div class="empty-icon">📡</div>
			<p>No devices enrolled yet.</p>
			<p style="margin-top:0.5rem">Create a preauth key and enroll your first device.</p>
		</div>
	</div>
{:else}
	<div class="table-wrap">
		<table>
			<thead>
				<tr>
					<th>Status</th>
					<th>Hostname</th>
					<th>Mesh IP</th>
					<th>Endpoint</th>
					<th>OS</th>
					<th>Agent</th>
					<th>Last Seen</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each devices as dev}
					<tr>
						<td>
							<span class="badge {dev.online ? 'badge-online' : 'badge-offline'}">
								<span class="badge-dot"></span>
								{dev.online ? 'online' : 'offline'}
							</span>
						</td>
						<td style="font-weight:600;color:var(--accent)">{dev.hostname}</td>
						<td class="mono">{dev.mesh_ip}</td>
						<td class="mono">{dev.last_endpoint || '—'}</td>
						<td class="sub">{dev.os}</td>
						<td class="mono">{dev.agent_version || '—'}</td>
						<td class="sub">{timeAgo(dev.last_seen)}</td>
						<td>
							<button
								class="btn btn-danger btn-sm"
								onclick={() => deleteDevice(dev)}
								disabled={deleting === dev.id}
							>
								{deleting === dev.id ? '…' : 'Remove'}
							</button>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	<div style="margin-top:1rem;padding:0 0.25rem">
		<details style="font-size:0.78rem;color:var(--text)">
			<summary style="cursor:pointer;font-weight:600;color:var(--accent)">WireGuard Public Keys</summary>
			<div style="margin-top:0.75rem;display:flex;flex-direction:column;gap:0.5rem">
				{#each devices as dev}
					<div style="display:flex;align-items:center;gap:0.75rem">
						<span style="min-width:140px;font-weight:500;color:var(--accent)">{dev.hostname}</span>
						<span class="mono-key">{dev.wg_public_key}</span>
					</div>
				{/each}
			</div>
		</details>
	</div>
{/if}
