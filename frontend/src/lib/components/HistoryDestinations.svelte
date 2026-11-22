<script lang="ts">
  import type { Connection } from '../stores/connections';
  import { writeHint } from '../stores/history';

  // HistoryDestinations picks the writable time-series connections a forward
  // writes to. Destinations that will not record anything yet say why.
  let {
    connections,
    selected = $bindable([]),
    idPrefix = 'history-dest',
  }: {
    connections: Connection[];
    selected: string[];
    idPrefix?: string;
  } = $props();

  function toggle(id: string, on: boolean) {
    selected = on ? [...selected.filter((d) => d !== id), id] : selected.filter((d) => d !== id);
  }
</script>

{#if connections.length === 0}
  <p class="text-xs opacity-60">
    No writable time-series connections. Add a Prometheus, InfluxDB, or PostgreSQL
    connection in Connections first.
  </p>
{:else}
  <div class="flex flex-col gap-1">
    {#each connections as conn (conn.id)}
      <label class="flex items-center gap-2 text-sm" for="{idPrefix}-{conn.id}">
        <input
          id="{idPrefix}-{conn.id}"
          type="checkbox"
          checked={selected.includes(conn.id)}
          onchange={(e) => toggle(conn.id, e.currentTarget.checked)}
        />
        <span>{conn.name}</span>
        <span class="text-xs opacity-60">{conn.kind}</span>
        {#if writeHint(conn)}
          <span class="text-xs" style="color: var(--theme-warning, #f9e2af)">{writeHint(conn)}</span>
        {/if}
      </label>
    {/each}
  </div>
{/if}
