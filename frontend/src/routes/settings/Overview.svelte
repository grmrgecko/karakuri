<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api/client';
  import { hasPermission } from '../../lib/stores/auth';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  // Each stat names the endpoint that counts it and the permission that gates
  // the read, so a user without the permission sees no card instead of a zero.
  interface Stat {
    label: string;
    icon: string;
    accent: string;
    permission?: string;
    count: number;
    load: () => Promise<number>;
  }

  // Paginated list responses carry the count in the pagination meta; the client
  // flattens that onto the response, so a limit of 1 is enough to read a total.
  async function countPaged(path: string): Promise<number> {
    const res = await api.get<{ total?: number }>(path);
    return res?.total ?? 0;
  }

  async function countList(path: string): Promise<number> {
    const res = await api.get<unknown[]>(path);
    return Array.isArray(res) ? res.length : 0;
  }

  // Themes answer with {themes, default_theme} rather than a bare list.
  async function countThemes(): Promise<number> {
    const res = await api.get<{ themes?: unknown[] }>('/themes');
    return res?.themes?.length ?? 0;
  }

  let stats = $state<Stat[]>([
    {
      label: 'Flows',
      icon: 'la-project-diagram',
      accent: 'var(--theme-blue, #89b4fa)',
      permission: 'api:flows:read',
      count: 0,
      load: () => countPaged('/flows?limit=1&is_subflow=false'),
    },
    {
      label: 'Dashboards',
      icon: 'la-columns',
      accent: 'var(--theme-green, #a6e3a1)',
      permission: 'api:dashboards:read',
      count: 0,
      load: () => countPaged('/dashboards?limit=1'),
    },
    {
      label: 'Connections',
      icon: 'la-plug',
      accent: 'var(--theme-teal, #94e2d5)',
      permission: 'api:connections:read',
      count: 0,
      load: () => countList('/connections'),
    },
    {
      label: 'Users',
      icon: 'la-users',
      accent: 'var(--theme-mauve, #cba6f7)',
      permission: 'api:users:read',
      count: 0,
      load: () => countPaged('/users?limit=1'),
    },
    {
      label: 'Roles',
      icon: 'la-shield-alt',
      accent: 'var(--theme-pink, #f5c2e7)',
      permission: 'api:roles:read',
      count: 0,
      load: () => countList('/roles'),
    },
    {
      label: 'Plugins',
      icon: 'la-puzzle-piece',
      accent: 'var(--theme-peach, #fab387)',
      permission: 'api:plugins:read',
      count: 0,
      load: () => countList('/plugins'),
    },
    {
      // Widgets are contributed by plugins, so this counts what a dashboard
      // can place right now rather than every package ever installed.
      label: 'Widgets',
      icon: 'la-th-large',
      accent: 'var(--theme-sky, #89dceb)',
      count: 0,
      load: () => countPaged('/widgets?limit=1'),
    },
    {
      label: 'Themes',
      icon: 'la-palette',
      accent: 'var(--theme-yellow, #f9e2af)',
      count: 0,
      load: countThemes,
    },
  ]);

  let loading = $state(true);

  let visible = $derived(stats.filter((s) => !s.permission || hasPermission(s.permission)));

  onMount(async () => {
    // Counts are independent; one failing endpoint leaves that card at zero.
    await Promise.all(
      visible.map(async (stat) => {
        try {
          stat.count = await stat.load();
        } catch {
          stat.count = 0;
        }
      }),
    );
    loading = false;
  });
</script>

<PageHeader title="Overview" />

{#if loading}
  <p class="opacity-60">Loading stats...</p>
{:else}
  <div class="kpi-grid">
    {#each visible as stat (stat.label)}
      <div class="kpi-card" style="--kpi-accent: {stat.accent}">
        <div class="kpi-header">
          <i class="las {stat.icon} kpi-icon"></i>
        </div>
        <span class="kpi-label">{stat.label}</span>
        <span class="kpi-value">{stat.count}</span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .kpi-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 0.75rem;
  }

  .kpi-card {
    position: relative;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    padding: 1.25rem 1rem 1rem;
    background-color: var(--theme-card-bg, var(--theme-surface0, #313244));
    border: 1px solid var(--theme-card-border, var(--theme-surface1, #45475a));
    border-radius: var(--theme-radius-md, 8px);
    box-shadow: var(--theme-shadow-sm, 0 1px 2px rgba(0, 0, 0, 0.05));
  }

  /* Accent stripe rides the card so its color stays with the entity even when
     a permission drops a card out of the grid. */
  .kpi-card::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 3px;
    background-color: var(--kpi-accent);
  }

  .kpi-header {
    margin-bottom: 0.75rem;
  }

  .kpi-icon {
    font-size: 1.5rem;
    color: var(--theme-subtext0, #a6adc8);
  }

  .kpi-label {
    text-transform: uppercase;
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.06em;
    color: var(--theme-subtext0, #a6adc8);
    margin-bottom: 0.25rem;
  }

  .kpi-value {
    font-size: 1.5rem;
    font-weight: 800;
    letter-spacing: -0.02em;
  }

  @media (max-width: 768px) {
    .kpi-grid {
      grid-template-columns: repeat(2, 1fr);
    }
  }
</style>
