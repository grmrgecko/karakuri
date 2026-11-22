<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from 'svelte-routing';
  import { pluginStore } from '../../lib/stores/plugins';
  import type { PluginState } from '../../lib/stores/plugins';
  import { hasPermission } from '../../lib/stores/auth';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  let plugins: PluginState[] = $state([]);
  let loading = $state(true);
  let error = $state('');
  let busy = $state('');
  let canWrite = hasPermission('api:plugins:write');

  onMount(() => {
    const unsub = pluginStore.subscribe((s) => {
      plugins = s.plugins;
      loading = s.loading;
    });
    pluginStore.list();
    return unsub;
  });

  // Every lifecycle button is the same shape: act, then re-read the list so
  // the card reflects what actually happened rather than what was asked for.
  async function act(name: string, fn: (n: string) => Promise<void>) {
    busy = name;
    error = '';
    try {
      await fn(name);
    } catch (e: any) {
      error = e.message || 'Plugin action failed';
    }
    await pluginStore.list();
    busy = '';
  }

  function statusColor(plugin: PluginState): string {
    if (!plugin.enabled) return 'var(--theme-subtext0, #a6adc8)';
    switch (plugin.status) {
      case 'loaded':
        return 'var(--theme-green, #a6e3a1)';
      case 'error':
        return 'var(--theme-red, #f38ba8)';
      default:
        return 'var(--theme-subtext0, #a6adc8)';
    }
  }

  function statusLabel(plugin: PluginState): string {
    if (plugin.builtin) return 'built in';
    return plugin.enabled ? plugin.status : 'disabled';
  }

  // A plugin's contributions are what makes it worth having, so the card says
  // what each one brings rather than only whether it is running.
  function contributions(plugin: PluginState): string[] {
    const parts: string[] = [];
    if (plugin.nodes?.length) {
      parts.push(`${plugin.nodes.length} node${plugin.nodes.length === 1 ? '' : 's'}`);
    }
    if (plugin.kinds?.length) {
      parts.push(`${plugin.kinds.length} connection kind${plugin.kinds.length === 1 ? '' : 's'}`);
    }
    if (plugin.widgets?.length) {
      parts.push(`${plugin.widgets.length} widget${plugin.widgets.length === 1 ? '' : 's'}`);
    }
    if (plugin.has_process) parts.push('API');
    return parts;
  }
</script>

<PageHeader title="Plugins" />

{#if error}
  <p class="mb-4 text-sm" style="color: var(--theme-red, #f38ba8)">{error}</p>
{/if}

{#if loading && plugins.length === 0}
  <p class="opacity-60">Loading plugins...</p>
{:else if plugins.length === 0}
  <p class="opacity-60">No plugins discovered.</p>
{:else}
  <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
    {#each plugins as plugin (plugin.name)}
      <div class="card p-4">
        <div class="flex items-start justify-between gap-3 mb-2">
          <div class="flex-1 min-w-0">
            <div class="font-semibold flex items-center gap-2 flex-wrap">
              {plugin.name}
              <span
                class="text-xs font-medium px-2 py-0.5 rounded"
                style="color: {statusColor(plugin)}; border: 1px solid {statusColor(plugin)}"
              >
                {statusLabel(plugin)}
              </span>
            </div>
            {#if plugin.description}
              <div class="text-sm opacity-60 mt-1">{plugin.description}</div>
            {/if}
            {#if contributions(plugin).length > 0}
              <div class="text-xs opacity-50 mt-2">
                <i class="las la-puzzle-piece mr-1"></i>{contributions(plugin).join(' · ')}
              </div>
            {/if}
          </div>
          <div class="flex-shrink-0 flex flex-col items-end gap-1">
            {#if plugin.author}
              <div class="text-xs opacity-40">{plugin.author}</div>
            {/if}
            {#if plugin.version}
              <div class="text-xs opacity-30">v{plugin.version}</div>
            {/if}
          </div>
        </div>

        {#if plugin.error_message}
          <div class="text-xs mt-1" style="color: var(--theme-red, #f38ba8)">
            {plugin.error_message}
          </div>
        {/if}

        {#if canWrite && !plugin.builtin}
          <div class="flex gap-2 mt-3 flex-wrap">
            {#if !plugin.enabled}
              <button class="btn btn-primary text-xs px-2 py-1" disabled={busy === plugin.name}
                      onclick={() => act(plugin.name, pluginStore.enable)}>
                Enable
              </button>
            {:else if plugin.status === 'loaded'}
              <button class="btn text-xs px-2 py-1" disabled={busy === plugin.name}
                      onclick={() => act(plugin.name, pluginStore.unload)}>
                Unload
              </button>
              <button class="btn text-xs px-2 py-1" disabled={busy === plugin.name}
                      onclick={() => act(plugin.name, pluginStore.reload)}>
                Reload
              </button>
              {#if plugin.has_settings}
                <button class="btn text-xs px-2 py-1"
                        onclick={() => navigate(`/settings/plugins/${plugin.name}`)}>
                  Settings
                </button>
              {/if}
              <button class="btn text-xs px-2 py-1" disabled={busy === plugin.name}
                      onclick={() => act(plugin.name, pluginStore.disable)}>
                Disable
              </button>
            {:else}
              <button class="btn btn-primary text-xs px-2 py-1" disabled={busy === plugin.name}
                      onclick={() => act(plugin.name, pluginStore.load)}>
                Load
              </button>
              <button class="btn text-xs px-2 py-1" disabled={busy === plugin.name}
                      onclick={() => act(plugin.name, pluginStore.disable)}>
                Disable
              </button>
            {/if}
          </div>
        {/if}
      </div>
    {/each}
  </div>
{/if}
