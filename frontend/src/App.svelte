<script lang="ts">
  import { Router, Route } from 'svelte-routing';
  import { onMount } from 'svelte';
  import { api } from './lib/api/client';
  import { auth } from './lib/stores/auth';
  import { websocket } from './lib/stores/websocket';
  import { themes } from './lib/stores/themes';
  import { initWidgetBridge } from './lib/widgetBridge';
  import Nav from './lib/components/Nav.svelte';
  import ConfirmDialog from './lib/components/ConfirmDialog.svelte';
  import Login from './routes/Login.svelte';
  import FlowList from './routes/FlowList.svelte';
  import Home from './routes/Home.svelte';
  import FlowEditor from './routes/FlowEditor.svelte';
  import DashboardList from './routes/DashboardList.svelte';
  import DashboardView from './routes/DashboardView.svelte';
  import DashboardEdit from './routes/DashboardEdit.svelte';
  import DashboardShare from './routes/DashboardShare.svelte';
  import FlowShare from './routes/FlowShare.svelte';
  import FolderShare from './routes/FolderShare.svelte';
  import ResolveRef from './lib/components/ResolveRef.svelte';
  import DashboardRender from './routes/DashboardRender.svelte';
  import DashboardGroupShare from './routes/DashboardGroupShare.svelte';
  import SettingsLayout from './routes/settings/Layout.svelte';
  import Activity from './routes/Activity.svelte';
  import Help from './routes/Help.svelte';
  import BrowserPrompt from './routes/BrowserPrompt.svelte';

  const isBrowserPrompt = window.location.pathname === '/prompt';

  let initialized = $state(false);

  onMount(async () => {
    if (isBrowserPrompt) return;
    // A render is not a session: the headless browser carries a token that
    // grants a view of one dashboard and nothing else, so asking who is signed
    // in would only produce a 401 to render around.
    if (!api.isRenderMode()) {
      await auth.init();
    }

    // Connect whether or not anyone is signed in: an anonymous viewer of a
    // public dashboard still needs live values, and the server gates the
    // socket per topic rather than at the door. Signing in or out reopens it,
    // since the token rides the URL.
    websocket.connect();
    auth.subscribe(() => websocket.reconnect());

    // Load themes and apply the user's preference (falling back to the site
    // default), then keep the injected stylesheet in step with the active
    // theme. Anonymous visitors get the site default, so public dashboards
    // are themed too.
    let userThemeId: number | null = null;
    const unsubAuth = auth.subscribe((s) => {
      userThemeId = (s.user?.preferences?.theme_id as number | undefined) ?? null;
    });
    unsubAuth();
    await themes.list(userThemeId);
    themes.subscribe((s) => {
      if (s.active) themes.applyFromStylesheet(s.active.package_name);
    });

    // Expose the API client and topic subscriptions to dashboard widgets.
    initWidgetBridge();

    initialized = true;
  });
</script>

{#if isBrowserPrompt}
  <BrowserPrompt />
{:else if !initialized}
  <div class="flex items-center justify-center min-h-screen">
    <div class="animate-pulse text-text-muted text-lg">Loading...</div>
  </div>
{:else}
  <ConfirmDialog />
  <Router>
    <Nav />
    <main class="flex-1 flex flex-col min-h-0 overflow-y-auto">
      <Route path="/login"><Login /></Route>
      <Route path="/settings/*"><SettingsLayout /></Route>
      <Route path="/activity"><Activity /></Route>
      <Route path="/help"><Help /></Route>
      <Route path="/help/:id">
        {#snippet children({ params }: { params: Record<string, string> })}
          <Help id={params.id} />
        {/snippet}
      </Route>
      <Route path="/folders/:id/share">
        {#snippet children({ params }: { params: Record<string, string> })}
          <FolderShare id={params.id} />
        {/snippet}
      </Route>
      <Route path="/flows/:id/share">
        {#snippet children({ params }: { params: Record<string, string> })}
          <ResolveRef kind="flow" ref={params.id} suffix="/share">
            {#snippet children(id: string)}
              <FlowShare {id} />
            {/snippet}
          </ResolveRef>
        {/snippet}
      </Route>
      <Route path="/flows/:id">
        {#snippet children({ params }: { params: Record<string, string> })}
          <ResolveRef kind="flow" ref={params.id}>
            {#snippet children(id: string)}
              <FlowEditor {id} />
            {/snippet}
          </ResolveRef>
        {/snippet}
      </Route>
      <Route path="/dashboards"><DashboardList /></Route>
      <Route path="/dashboard/:id/share">
        {#snippet children({ params }: { params: Record<string, string> })}
          <ResolveRef kind="dashboard" ref={params.id} suffix="/share">
            {#snippet children(id: string)}
              <DashboardShare {id} />
            {/snippet}
          </ResolveRef>
        {/snippet}
      </Route>
      <Route path="/dashboard/:id/render">
        {#snippet children({ params }: { params: Record<string, string> })}
          <ResolveRef kind="dashboard" ref={params.id} suffix="/render">
            {#snippet children(id: string)}
              <DashboardRender {id} />
            {/snippet}
          </ResolveRef>
        {/snippet}
      </Route>
      <Route path="/dashboard/:id/edit">
        {#snippet children({ params }: { params: Record<string, string> })}
          <ResolveRef kind="dashboard" ref={params.id} suffix="/edit">
            {#snippet children(id: string)}
              <DashboardEdit {id} />
            {/snippet}
          </ResolveRef>
        {/snippet}
      </Route>
      <Route path="/dashboard/:id">
        {#snippet children({ params }: { params: Record<string, string> })}
          <ResolveRef kind="dashboard" ref={params.id}>
            {#snippet children(id: string)}
              <DashboardView {id} />
            {/snippet}
          </ResolveRef>
        {/snippet}
      </Route>
      <Route path="/dashboard-group/:id/share">
        {#snippet children({ params }: { params: Record<string, string> })}
          <DashboardGroupShare id={params.id} />
        {/snippet}
      </Route>
      <Route path="/flows"><FlowList /></Route>
      <Route path="/"><Home /></Route>
    </main>
  </Router>
{/if}
