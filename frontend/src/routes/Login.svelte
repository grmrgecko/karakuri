<script lang="ts">
  import { navigate } from 'svelte-routing';
  import { auth, isAuthenticated } from '../lib/stores/auth';

  let username = $state('');
  let password = $state('');
  let error = $state('');
  let loading = $state(false);

  $effect(() => {
    if ($isAuthenticated) {
      navigate('/', { replace: true });
    }
  });

  async function handleSubmit() {
    error = '';
    loading = true;
    try {
      await auth.login(username, password);
      navigate('/');
    } catch (e: any) {
      error = e.message || 'Login failed';
    } finally {
      loading = false;
    }
  }
</script>

<div class="flex items-center justify-center min-h-full flex-1">
  <div class="card p-8 w-full max-w-md">
    <h1 class="text-2xl font-bold mb-6 text-center" style="color: var(--theme-accent);">
      Sign in to Karakuri
    </h1>

    {#if error}
      <div class="mb-4 p-3 rounded text-sm"
           style="background-color: var(--theme-error); color: var(--theme-base); opacity: 0.9;">
        {error}
      </div>
    {/if}

    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); handleSubmit(); }} class="flex flex-col gap-4">
      <div>
        <label for="username" class="block text-sm font-medium mb-1">Username</label>
        <input
          id="username"
          type="text"
          class="input w-full"
          bind:value={username}
          required
          autocomplete="username"
        />
      </div>

      <div>
        <label for="password" class="block text-sm font-medium mb-1">Password</label>
        <input
          id="password"
          type="password"
          class="input w-full"
          bind:value={password}
          required
          autocomplete="current-password"
        />
      </div>

      <button
        type="submit"
        class="btn btn-primary w-full mt-2"
        disabled={loading}
      >
        <i class="las la-sign-in-alt mr-1"></i>{loading ? 'Signing in...' : 'Sign in'}
      </button>
    </form>
  </div>
</div>
