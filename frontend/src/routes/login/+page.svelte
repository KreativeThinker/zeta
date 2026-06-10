<script lang="ts">
	import { goto } from '$app/navigation';

	let password = $state('');
	let error = $state('');
	let loading = $state(false);

	async function submit(e: Event) {
		e.preventDefault();
		if (!password || loading) return;
		loading = true;
		error = '';
		try {
			const res = await fetch('/api/v1/auth/login', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ password }),
			});
			if (res.ok) {
				await goto('/');
			} else {
				error = 'Invalid password';
			}
		} catch {
			error = 'Could not reach server';
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head><title>Login · Zeta</title></svelte:head>

<div class="login-wrap">
	<div class="login-brand">Zeta</div>

	<div class="login-card">
		<form onsubmit={submit}>
			<input
				type="password"
				placeholder="Admin password"
				bind:value={password}
				autofocus
				autocomplete="current-password"
				disabled={loading}
			/>
			<button type="submit" class="btn btn-primary" style="width:100%;margin-top:0.75rem" disabled={loading || !password}>
				{loading ? 'Logging in…' : 'Login'}
			</button>
			{#if error}
				<p class="login-error">{error}</p>
			{/if}
		</form>
	</div>
</div>

<style>
	.login-wrap {
		min-height: 100vh;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: 1.5rem;
		background: var(--bg);
	}
	.login-brand {
		font-size: 1.75rem;
		font-weight: 700;
		color: var(--accent);
		letter-spacing: -0.03em;
		margin-bottom: 1.5rem;
	}
	.login-card {
		width: 100%;
		max-width: 360px;
		background: var(--white);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 1.75rem;
		box-shadow: 0 1px 4px rgba(0,0,0,0.06);
	}
	.login-card input {
		width: 100%;
		padding: 0.65rem 0.875rem;
		border: 1px solid var(--border-mid);
		border-radius: var(--radius-sm);
		background: var(--bg);
		color: var(--accent);
		font-family: var(--font-sans);
		font-size: 0.9375rem;
		outline: none;
		transition: border-color 0.15s;
	}
	.login-card input:focus { border-color: var(--accent); }
	.login-error {
		color: var(--danger);
		font-size: 0.8125rem;
		text-align: center;
		margin-top: 0.75rem;
	}
</style>
