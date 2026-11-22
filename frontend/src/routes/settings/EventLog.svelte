<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api/client';
  import PageHeader from '../../lib/components/PageHeader.svelte';
  import FormField from '../../lib/components/FormField.svelte';

  interface Settings {
    retention_days: Record<string, number>;
    max_rows: number;
    excluded_controls: string[];
    ha_entities: { connection_id: string; entity_id: string }[];
    entries?: number;
  }

  const CATEGORIES = ['device', 'connection', 'flow', 'agent', 'security', 'system', 'custom'];

  let settings: Settings | null = $state(null);
  let excludedText = $state('');
  let entitiesText = $state('');
  let error = $state('');
  let notice = $state('');
  let saving = $state(false);

  function apply(s: Settings) {
    settings = s;
    excludedText = s.excluded_controls.join('\n');
    entitiesText = s.ha_entities.map((e) => `${e.connection_id} ${e.entity_id}`).join('\n');
  }

  onMount(async () => {
    try {
      apply(await api.get<Settings>('/events/settings'));
    } catch (e: any) {
      error = e.message || 'The event log settings could not be read';
    }
  });

  async function save() {
    if (!settings) return;
    error = '';
    notice = '';
    saving = true;
    const ha = entitiesText.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => {
      const [connection_id, entity_id] = l.split(/\s+/);
      return { connection_id, entity_id };
    });
    try {
      apply(await api.put<Settings>('/events/settings', {
        retention_days: settings.retention_days,
        max_rows: Number(settings.max_rows),
        excluded_controls: excludedText.split('\n').map((l) => l.trim()).filter(Boolean),
        ha_entities: ha,
      }));
      notice = 'Settings saved';
    } catch (e: any) {
      error = e.message || 'The event log settings could not be saved';
    } finally {
      saving = false;
    }
  }
</script>

<PageHeader title="Event log" />

{#if error}
  <div class="mb-4 p-3 rounded text-sm" style="background-color: var(--theme-error); color: var(--theme-base);">{error}</div>
{/if}
{#if notice}
  <div class="mb-4 p-3 rounded text-sm" style="background-color: var(--theme-surface0);">{notice}</div>
{/if}

{#if settings}
  <section class="card p-4">
    <h2 class="section-title">Keep entries for</h2>
    <div class="grid">
      {#each CATEGORIES as c (c)}
        <FormField label="{c[0].toUpperCase() + c.slice(1)} (days)" id="ret-{c}">
          <input id="ret-{c}" class="input" type="number" min="1" max="3650" bind:value={settings.retention_days[c]} />
        </FormField>
      {/each}
      <FormField label="Most entries kept" id="ret-max">
        <input id="ret-max" class="input" type="number" min="10000" max="10000000" bind:value={settings.max_rows} />
        <p class="hint">Past this, the oldest entries go first, never security ones. {settings.entries ?? 0} stored now.</p>
      </FormField>
    </div>
    <FormField label="Controls not logged" id="excluded">
      <textarea id="excluded" class="input" rows="3" bind:value={excludedText} placeholder="One control ID per line"></textarea>
      <p class="hint">For controls that mirror a busy sensor.</p>
    </FormField>
    <FormField label="Home Assistant entities logged" id="ha">
      <textarea id="ha" class="input" rows="4" bind:value={entitiesText} placeholder="connection-id entity_id, one per line"></textarea>
      <p class="hint">Each listed entity is logged when its state changes. Others are not, since a house may have thousands.</p>
    </FormField>
    <button class="btn btn-primary text-sm mt-2" onclick={save} disabled={saving}>{saving ? 'Saving...' : 'Save settings'}</button>
  </section>
{:else if !error}
  <p class="opacity-60">Loading...</p>
{/if}

<style>
  .section-title {
    font-weight: 600;
    margin-bottom: 0.5rem;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .input {
    width: 100%;
  }
  .hint {
    font-size: 0.75rem;
    opacity: 0.6;
    margin-top: 0.25rem;
  }
</style>
