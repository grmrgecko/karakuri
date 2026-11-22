<script lang="ts">
  import { onMount } from 'svelte';
  import { connectionsStore } from '../stores/connections';
  import {
    isWritable,
    loadOwnedForward,
    saveOwnedForward,
    type HistoryForward,
  } from '../stores/history';
  import HistoryDestinations from './HistoryDestinations.svelte';

  // HistoryRecordField is the "Record history" section of the control and
  // MQTT device editors. It edits the one forward the editor owns; the parent
  // calls commit once the control or device itself is saved, because a new
  // one has no ID before that.
  let {
    sourceType,
    sourceId,
  }: {
    sourceType: 'control' | 'mqtt_device';
    sourceId: string | null;
  } = $props();

  let existing: HistoryForward | null = $state(null);
  let record = $state(false);
  let destinations = $state<string[]>([]);
  let loading = $state(true);
  let loadError = $state('');

  let writable = $derived(($connectionsStore.connections || []).filter(isWritable));

  onMount(async () => {
    try {
      await connectionsStore.load();
      if (sourceId) {
        existing = await loadOwnedForward(sourceType, sourceId);
        if (existing) {
          record = true;
          destinations = [...(existing.destinations ?? [])];
        }
      }
    } catch (e: any) {
      loadError = e.message || 'Failed to load the history settings';
    } finally {
      loading = false;
    }
  });

  // validate returns the message that should stop the save, or ''.
  export function validate(): string {
    if (record && destinations.length === 0) return 'Pick at least one history destination';
    return '';
  }

  // commit stores or removes the owned forward for the saved control or
  // device. Nothing changes while the section failed to load, so a stale
  // form never deletes a forward it could not see.
  export async function commit(id: string, name: string): Promise<void> {
    if (loading || loadError) return;
    await saveOwnedForward({ type: sourceType, id, name, record, destinations, existing });
  }
</script>

<div class="flex flex-col gap-2">
  <label class="flex items-center gap-2 text-sm font-medium">
    <input type="checkbox" bind:checked={record} disabled={loading || !!loadError} />
    <span>Record history</span>
  </label>
  {#if loadError}
    <p class="text-xs" style="color: var(--theme-error, #f38ba8);">{loadError}</p>
  {:else if record}
    <HistoryDestinations
      connections={writable}
      bind:selected={destinations}
      idPrefix="{sourceType}-history-dest"
    />
  {/if}
  <p class="text-xs opacity-60">
    Every attribute is recorded each time it changes. Settings → History forwarding has
    the metric name, labels, and minimum interval.
    {#if existing && !existing.enabled}
      This forward is switched off there; saving with this checked switches it back on.
    {/if}
  </p>
</div>
