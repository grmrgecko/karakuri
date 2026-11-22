<script lang="ts">
  import { Link, navigate, useLocation } from 'svelte-routing';
  import { auth, isAuthenticated, currentUser, hasPermission } from '../stores/auth';

  // A dashboard being viewed owns the whole window: it is what a wall panel
  // shows, and the app chrome would eat a row of it. Only the bare view route
  // hides the nav — the editor keeps it, since editing is done from the app.
  const location = useLocation();
  let isDashboardView = $derived(/^\/dashboard\/[^/]+\/?$/.test($location.pathname));

  let showUserMenu = $state(false);

  async function handleLogout() {
    await auth.logout();
    navigate('/login');
  }

  let user = $derived($currentUser);
  // Reading $auth ties this to sign-in, so the link appears once the user's
  // permissions have loaded.
  let canActivity = $derived($auth.user ? hasPermission('api:events:read') : false);
</script>

{#if !isDashboardView}
<nav class="flex items-center justify-between px-6 py-3 border-b"
     style="background-color: var(--theme-nav-bg, var(--theme-mantle)); border-color: var(--theme-border, var(--theme-surface0));">
  <div class="flex items-center gap-6">
    <Link to="/" class="flex items-center gap-2 text-xl font-bold" style="color: var(--theme-accent);">
      <img src="/favicon.svg" alt="" class="h-8 w-8" />
      Karakuri
    </Link>

    {#if $isAuthenticated}
      <div class="flex items-center gap-4 text-sm">
        <Link to="/flows" class="hover:opacity-80"><i class="las la-project-diagram mr-1"></i>Flows</Link>
        <Link to="/dashboards" class="hover:opacity-80"><i class="las la-th-large mr-1"></i>Dashboards</Link>
        {#if canActivity}
          <Link to="/activity" class="hover:opacity-80"><i class="las la-stream mr-1"></i>Activity</Link>
        {/if}
      </div>
    {/if}
  </div>

  <div class="flex items-center gap-3">
    {#if $isAuthenticated}
      <Link to="/help" class="text-sm hover:opacity-80"><i class="las la-question-circle mr-1"></i>Help</Link>
    {/if}
    {#if $isAuthenticated && user}
      <div class="relative">
        <button
          class="btn btn-secondary text-sm px-3 py-1"
          onclick={(e: MouseEvent) => { e.stopPropagation(); showUserMenu = !showUserMenu; }}
        >
          <i class="las la-user-circle mr-1"></i>{user.username}
        </button>
        {#if showUserMenu}
          <div class="absolute right-0 top-full mt-1 card p-2 min-w-36 z-50">
            <Link to="/settings" class="block px-3 py-2 rounded text-sm hover:opacity-80">
              <i class="las la-cog mr-1"></i>Settings
            </Link>
            <button
              class="block w-full text-left px-3 py-2 rounded text-sm hover:opacity-80"
              onclick={handleLogout}
            >
              <i class="las la-sign-out-alt mr-1"></i>Logout
            </button>
          </div>
        {/if}
      </div>
    {:else}
      <Link to="/login" class="btn btn-primary text-sm px-3 py-1"><i class="las la-sign-in-alt mr-1"></i>Login</Link>
    {/if}
  </div>
</nav>
{/if}

<svelte:window onclick={() => { showUserMenu = false; }} />
