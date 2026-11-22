<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api/client';
  import { connectionsStore } from '../stores/connections';
  import { controlsStore } from '../stores/controls';
  import { mqttDevicesStore } from '../stores/mqttDevices';
  import type { Condition, DashboardVariable } from '../stores/dashboards';
  import { optionsOf, optionText } from '../variables';

  // ConditionListEditor edits one list of display conditions: a placement's
  // visibility conditions or one status cue's. Both take the same forms and
  // grant their sources the same reads, so they share one editor.
  let {
    conditions,
    variables = [],
    idPrefix,
    onchange,
  }: {
    conditions: Condition[];
    variables?: DashboardVariable[];
    /** idPrefix keeps datalist IDs apart when several lists are on one panel. */
    idPrefix: string;
    onchange: (next: Condition[]) => void;
  } = $props();

  const operators = [
    { value: 'equals', label: 'equals' },
    { value: 'not_equals', label: 'does not equal' },
    { value: 'above', label: 'is above' },
    { value: 'below', label: 'is below' },
    { value: 'contains', label: 'contains' },
  ];
  // A variable's value is a word from its list, so only these comparisons apply.
  const variableOperators = operators.filter((op) => op.value === 'equals' || op.value === 'not_equals' || op.value === 'contains');
  const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

  let haConnections = $derived($connectionsStore.connections.filter((c) => c.kind === 'homeassistant'));
  let entityOptions = $state<Record<string, string[]>>({});

  onMount(() => {
    connectionsStore.load().catch(() => {});
    controlsStore.load().catch(() => {});
    mqttDevicesStore.load().catch(() => {});
  });

  function patchCondition(index: number, next: Partial<Condition>) {
    onchange(conditions.map((c, i) => (i === index ? { ...c, ...next } : c)));
  }

  // conditionOf returns a fresh condition of a kind with sensible defaults.
  function conditionOf(kind: Condition['kind']): Condition {
    if (kind === 'time') return { kind, from: '22:00', to: '06:00' };
    if (kind === 'variable') {
      const first = variables[0];
      return { kind, variable: first?.name ?? '', operator: 'equals', value: first ? (optionsOf(first)[0]?.value ?? '') : '' };
    }
    return { kind, source: 'ha', operator: 'equals', value: 'on' };
  }

  function addCondition(kind: Condition['kind']) {
    onchange([...conditions, conditionOf(kind)]);
  }

  function removeCondition(index: number) {
    onchange(conditions.filter((_, i) => i !== index));
  }

  function toggleDay(index: number, day: number) {
    const current = conditions[index].days ?? [];
    const next = current.includes(day) ? current.filter((d) => d !== day) : [...current, day].sort();
    patchCondition(index, { days: next });
  }

  // loadEntities fetches a Home Assistant connection's entity IDs for the
  // condition's suggestions, once per connection.
  async function loadEntities(connectionId: string) {
    if (!connectionId || entityOptions[connectionId]) return;
    try {
      const res: any = await api.get(`/connections/${connectionId}/ha/states`);
      const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
      entityOptions = { ...entityOptions, [connectionId]: list.map((e) => String(e.entity_id)).sort() };
    } catch {
      entityOptions = { ...entityOptions, [connectionId]: [] };
    }
  }

  $effect(() => {
    for (const c of conditions) {
      if (c.kind === 'state' && c.source === 'ha' && c.connectionId) void loadEntities(c.connectionId);
    }
  });

  const value = (e: Event) => (e.currentTarget as HTMLInputElement | HTMLSelectElement).value;
</script>

    {#each conditions as c, i (i)}
      <div class="condition">
        <div class="flex items-center gap-1">
          <select class="input flex-1 text-xs" value={c.kind} onchange={(e) => {
            onchange(conditions.map((existing, j) => (j === i ? conditionOf(value(e) as Condition['kind']) : existing)));
          }}>
            <option value="state">A value</option>
            <option value="time">A time of day</option>
            {#if variables.length > 0 || c.kind === 'variable'}
              <option value="variable">A variable</option>
            {/if}
          </select>
          <button class="icon-btn" title="Remove condition" aria-label="Remove condition" onclick={() => removeCondition(i)}>
            <i class="las la-trash"></i>
          </button>
        </div>

        {#if c.kind === 'variable'}
          {@const variable = variables.find((f) => f.name === c.variable)}
          <select class="input w-full text-xs mt-1" value={c.variable ?? ''} onchange={(e) => patchCondition(i, { variable: value(e) })}>
            {#if !variable}
              <option value={c.variable ?? ''}>{c.variable || '(select a variable)'}</option>
            {/if}
            {#each variables as f (f.name)}
              <option value={f.name}>{f.label || f.name}</option>
            {/each}
          </select>
          <div class="flex gap-1 mt-1">
            <select class="input text-xs" value={c.operator ?? 'equals'} onchange={(e) => patchCondition(i, { operator: value(e) as Condition['operator'] })}>
              {#each variableOperators as op (op.value)}
                <option value={op.value}>{op.label}</option>
              {/each}
            </select>
            <input class="input flex-1 text-xs" list="variable-values-{idPrefix}-{i}" value={c.value ?? ''}
              onchange={(e) => patchCondition(i, { value: value(e) })} />
            <datalist id="variable-values-{idPrefix}-{i}">
              {#each variable ? optionsOf(variable) : [] as option, j (j)}
                <option value={option.value}>{optionText(option)}</option>
              {/each}
            </datalist>
          </div>
        {:else if c.kind === 'time'}
          <div class="flex items-center gap-1 mt-1">
            <input type="time" class="input text-xs flex-1" value={c.from ?? ''} onchange={(e) => patchCondition(i, { from: value(e) })} />
            <span class="text-xs opacity-60">to</span>
            <input type="time" class="input text-xs flex-1" value={c.to ?? ''} onchange={(e) => patchCondition(i, { to: value(e) })} />
          </div>
          <div class="days">
            {#each days as label, day}
              <button class="day" class:on={(c.days ?? []).includes(day)} onclick={() => toggleDay(i, day)}>{label}</button>
            {/each}
          </div>
          <small>No days selected means every day. A window past midnight belongs to the day it starts.</small>
        {:else}
          <select class="input w-full text-xs mt-1" value={c.source ?? 'ha'} onchange={(e) => patchCondition(i, { source: value(e) as Condition['source'], controlId: undefined, connectionId: undefined, entityId: undefined, deviceId: undefined })}>
            <option value="ha">Home Assistant entity</option>
            <option value="control">Control</option>
            <option value="mqtt">MQTT device</option>
          </select>
          {#if c.source === 'control'}
            <select class="input w-full text-xs mt-1" value={c.controlId ?? ''} onchange={(e) => patchCondition(i, { controlId: value(e) })}>
              <option value="">(select a control)</option>
              {#each $controlsStore.controls as control (control.id)}
                <option value={control.id}>{control.name}</option>
              {/each}
            </select>
          {:else if c.source === 'mqtt'}
            <select class="input w-full text-xs mt-1" value={c.deviceId ?? ''} onchange={(e) => patchCondition(i, { deviceId: value(e) })}>
              <option value="">(select a device)</option>
              {#each $mqttDevicesStore.devices as device (device.id)}
                <option value={device.id}>{device.name}</option>
              {/each}
            </select>
          {:else}
            <select class="input w-full text-xs mt-1" value={c.connectionId ?? ''} onchange={(e) => patchCondition(i, { connectionId: value(e) })}>
              <option value="">(select a connection)</option>
              {#each haConnections as conn (conn.id)}
                <option value={conn.id}>{conn.name}</option>
              {/each}
            </select>
            <input class="input w-full text-xs mt-1" list="entities-{idPrefix}-{i}" placeholder="binary_sensor.garage_door"
              value={c.entityId ?? ''} onchange={(e) => patchCondition(i, { entityId: value(e).trim() })} />
            <datalist id="entities-{idPrefix}-{i}">
              {#each entityOptions[c.connectionId ?? ''] ?? [] as entity}
                <option value={entity}></option>
              {/each}
            </datalist>
          {/if}
          <input class="input w-full text-xs mt-1" placeholder={c.source === 'mqtt' ? 'Field (empty for its value)' : 'Attribute (empty for the state)'}
            value={c.attribute ?? ''} onchange={(e) => patchCondition(i, { attribute: value(e).trim() || undefined })} />
          <div class="flex gap-1 mt-1">
            <select class="input text-xs" value={c.operator ?? 'equals'} onchange={(e) => patchCondition(i, { operator: value(e) as Condition['operator'] })}>
              {#each operators as op (op.value)}
                <option value={op.value}>{op.label}</option>
              {/each}
            </select>
            <input class="input flex-1 text-xs" placeholder="on" value={c.value ?? ''} onchange={(e) => patchCondition(i, { value: value(e) })} />
          </div>
        {/if}
      </div>
    {/each}
    <div class="flex gap-1">
      <button class="btn btn-secondary btn-sm" onclick={() => addCondition('state')}><i class="las la-plus mr-1"></i>Value</button>
      <button class="btn btn-secondary btn-sm" onclick={() => addCondition('time')}><i class="las la-plus mr-1"></i>Time</button>
      {#if variables.length > 0}
        <button class="btn btn-secondary btn-sm" onclick={() => addCondition('variable')}><i class="las la-plus mr-1"></i>Variable</button>
      {/if}
    </div>

<style>
  small {
    font-size: 0.7rem;
    opacity: 0.6;
  }

  .condition {
    padding: 0.45rem;
    border: 1px solid var(--theme-surface0, #313244);
    border-radius: var(--theme-radius-sm, 4px);
  }

  .icon-btn {
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    padding: 0 0.3rem;
  }

  .icon-btn:hover {
    color: var(--theme-error, #f38ba8);
  }

  .days {
    display: flex;
    gap: 0.2rem;
    margin-top: 0.3rem;
  }

  .day {
    flex: 1;
    font-size: 0.65rem;
    padding: 0.15rem 0;
    border-radius: var(--theme-radius-sm, 4px);
    border: 1px solid var(--theme-surface1, #45475a);
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
  }

  .day.on {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
    border-color: var(--theme-accent, #89b4fa);
  }
</style>
