<script lang="ts">
  import { onMount } from 'svelte';
  import { auth, currentUser } from '../../lib/stores/auth';
  import { themes as themeStore } from '../../lib/stores/themes';
  import { api } from '../../lib/api/client';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  let themes: any[] = $state([]);
  let defaultThemePkg = $state('');
  let themesLoading = $state(true);

  let user = $derived($currentUser);
  let activeTheme = $derived($themeStore.active);

  onMount(() => {
    loadThemes();
  });

  async function loadThemes() {
    themesLoading = true;
    try {
      const res = await api.get<{ themes: any[]; default_theme: string }>('/themes');
      themes = res.themes || [];
      defaultThemePkg = res.default_theme || '';
    } catch { /* handled by empty state */ }
    themesLoading = false;
  }

  async function applyTheme(theme: any) {
    themeStore.apply(theme);
    themeStore.applyFromStylesheet(theme.package_name);

    if (user) {
      try {
        // Theme choice rides the user's preferences blob, so it follows the
        // account rather than the browser.
        await auth.setPreferences({ theme_id: theme.id });
      } catch {
        // Theme applied locally even if save fails.
      }
    }
  }

  function themeCardStyle(theme: any): string {
    try {
      const vars: Record<string, string> = JSON.parse(theme.variables);
      const base = vars.base || '';
      const text = vars.text || '';
      const surface0 = vars.surface0 || '';
      const surface1 = vars.surface1 || '';
      const subtext0 = vars.subtext0 || '';
      const mauve = vars.mauve || vars.blue || '';
      const green = vars.green || vars.teal || '';
      return [
        base && `--card-base: ${base}`,
        text && `--card-text: ${text}`,
        surface0 && `--card-surface0: ${surface0}`,
        surface1 && `--card-surface1: ${surface1}`,
        subtext0 && `--card-muted: ${subtext0}`,
        mauve && `--card-accent: ${mauve}`,
        green && `--card-success: ${green}`,
        base && `background-color: ${base}`,
        text && `color: ${text}`,
        surface1 && `border-color: ${surface1}`,
      ].filter(Boolean).join('; ');
    } catch {
      return '';
    }
  }

  function swatchColors(theme: any): string[] {
    try {
      const vars: Record<string, string> = JSON.parse(theme.variables);
      const keys = ['red', 'peach', 'yellow', 'green', 'teal', 'blue', 'mauve', 'pink'];
      return keys.map((k) => vars[k]).filter(Boolean);
    } catch {
      return [];
    }
  }
</script>

<PageHeader title="Themes" />

<p class="text-sm mb-4" style="color: var(--theme-text-muted);">
  Click a theme to make it yours. The site-wide default is <code>theme.default</code> in the server configuration.
</p>

{#if themesLoading}
  <p class="opacity-60">Loading themes...</p>
{:else}
  <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
    {#each themes as theme (theme.id)}
      <div
        class="theme-card"
        class:theme-active={activeTheme?.id === theme.id}
        style={themeCardStyle(theme)}
        onclick={() => applyTheme(theme)}
        onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); applyTheme(theme); } }}
        role="button"
        tabindex="0"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="flex-1 min-w-0">
            <div class="font-semibold flex items-center gap-2 flex-wrap">
              {theme.name}
              {#if theme.package_name === defaultThemePkg}
                <span class="badge-default" style:background-color="var(--card-accent)">Default</span>
              {/if}
              {#if activeTheme?.id === theme.id}
                <span class="badge-active" style:background-color="var(--card-success)">Active</span>
              {/if}
            </div>
            <div class="text-sm mt-1" style="color: var(--card-muted);">{theme.description}</div>
            <div class="swatch-row">
              {#each swatchColors(theme) as color}
                <span class="swatch" style:background-color={color}></span>
              {/each}
            </div>
          </div>
          <div class="flex-shrink-0 flex flex-col items-end gap-1">
            {#if theme.author}
              <div class="text-xs" style="color: var(--card-muted);">{theme.author}</div>
            {/if}
            <div class="text-xs" style="color: var(--card-muted); opacity: 0.6;">v{theme.version}</div>
          </div>
        </div>
      </div>
    {/each}
  </div>
{/if}

<style>
  .theme-card {
    cursor: pointer;
    padding: 1rem;
    border-radius: 8px;
    border: 2px solid var(--card-surface1, var(--theme-surface1, #45475a));
    transition: border-color 0.15s, box-shadow 0.15s, transform 0.15s;
  }

  .theme-card:hover {
    transform: translateY(-1px);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  }

  .theme-active {
    border-color: var(--card-accent, var(--theme-accent, #89b4fa));
    box-shadow: 0 0 0 1px var(--card-accent, var(--theme-accent, #89b4fa));
  }

  .theme-active:hover {
    border-color: var(--card-accent, var(--theme-accent, #89b4fa));
  }

  .badge-default, .badge-active {
    display: inline-block;
    font-size: 0.7rem;
    font-weight: 600;
    padding: 0.1rem 0.45rem;
    border-radius: 4px;
    color: var(--card-base, var(--theme-base, #1e1e2e));
  }

  .swatch-row {
    display: flex;
    gap: 4px;
    margin-top: 0.5rem;
  }

  .swatch {
    width: 16px;
    height: 16px;
    border-radius: 50%;
    flex-shrink: 0;
  }

</style>
