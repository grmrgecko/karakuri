<script module lang="ts">
  import type { DashboardVariable, VariableQuery } from '../stores/dashboards';

  /** VariableDraft is a variable as the editor holds it: every field a string so inputs bind directly. */
  export interface VariableDraft {
    key: number;
    name: string;
    label: string;
    description: string;
    type: 'custom' | 'constant' | 'query' | 'time_range' | 'interval';
    display: 'above' | 'label_hidden' | 'menu' | 'hidden';
    /** Custom options, time range presets, or interval durations, one per line as value | label. */
    options: string;
    default: string;
    value: string;
    query: Required<VariableQuery>;
    /** Interval auto sizing. */
    auto: boolean;
    steps: string;
    min_interval: string;
    range_variable: string;
  }

  /** TIME_SERIES_SOURCES are the query sources that read a time-series connection. */
  export const TIME_SERIES_SOURCES = ['prometheus_label_values', 'prometheus_metrics', 'prometheus_query', 'influx_query', 'sql_query'];

  let nextKey = 1;

  function blankQuery(): Required<VariableQuery> {
    return {
      source: 'ha_entities', connection_id: '', pattern: '', entity_id: '', regex: '', sort: '', follow: false,
      label: '', match: '', text: '', language: '', range_variable: '',
    };
  }

  /** draftOf turns a stored variable into an editable draft. */
  export function draftOf(v: DashboardVariable): VariableDraft {
    return {
      key: nextKey++,
      name: v.name,
      label: v.label ?? '',
      description: v.description ?? '',
      type: v.type ?? 'custom',
      display: v.display || 'above',
      options: ['custom', 'time_range', 'interval'].includes(v.type ?? 'custom')
        ? (v.options ?? []).map(optionLine).join('\n')
        : '',
      default: v.default ?? '',
      value: v.value ?? '',
      query: { ...blankQuery(), ...(v.query ?? {}) } as Required<VariableQuery>,
      auto: !!v.auto,
      steps: v.steps ? String(v.steps) : '',
      min_interval: v.min_interval ?? '',
      range_variable: v.range_variable ?? '',
    };
  }

  /** blankDraft returns a new custom variable with an unused name. */
  export function blankDraft(existing: VariableDraft[]): VariableDraft {
    let n = existing.length + 1;
    while (existing.some((d) => d.name === `var${n}`)) n++;
    return draftOf({ name: `var${n}`, options: [] });
  }

  /**
   * variableOf turns a draft into what the server stores, keeping only the
   * fields its type uses. The server checks names, sizes, and the default.
   */
  export function variableOf(d: VariableDraft): DashboardVariable {
    const v: DashboardVariable = { name: d.name.trim() };
    if (d.label.trim()) v.label = d.label.trim();
    if (d.description.trim()) v.description = d.description.trim();
    if (d.type !== 'custom') v.type = d.type;
    if (d.type !== 'constant' && d.display !== 'above') v.display = d.display;
    if (d.type === 'custom' || d.type === 'time_range') {
      const options = parseOptions(d.options);
      if (d.type === 'custom' || options.length) v.options = options;
    } else if (d.type === 'interval') {
      v.options = parseOptions(d.options);
      if (d.auto) {
        v.auto = true;
        if (Number(d.steps) > 0) v.steps = Number(d.steps);
        if (d.min_interval.trim()) v.min_interval = d.min_interval.trim();
        if (d.range_variable) v.range_variable = d.range_variable;
      }
    } else if (d.type === 'constant') {
      v.value = d.value.trim();
    } else {
      const q = d.query;
      const query: VariableQuery = { source: q.source, connection_id: q.connection_id };
      if (TIME_SERIES_SOURCES.includes(q.source)) {
        if (q.label.trim()) query.label = q.label.trim();
        if (q.match.trim()) query.match = q.match.trim();
        if (q.text.trim()) query.text = q.text.trim();
        if (q.source === 'influx_query' && q.language) query.language = q.language;
        if (q.range_variable) query.range_variable = q.range_variable;
      } else if (q.source === 'ha_entities') query.pattern = q.pattern.trim();
      else query.entity_id = q.entity_id.trim();
      if (q.regex.trim()) query.regex = q.regex.trim();
      if (q.sort) query.sort = q.sort;
      if (q.source === 'ha_select' && q.follow) query.follow = true;
      v.query = query;
    }
    if (d.type !== 'constant' && d.default.trim()) v.default = d.default.trim();
    return v;
  }

  /**
   * optionLine writes an option as parseOptions reads it back. A value that
   * holds a bar is always written with the spaced separator, repeating the
   * value as its label when it has none, so the bar stays in the value.
   */
  function optionLine(o: { value: string; label?: string }): string {
    if (o.label || o.value.includes('|')) return `${o.value} | ${o.label || o.value}`;
    return o.value;
  }

  /**
   * parseOptions reads one option per line, as value or value | label. The
   * line is cut at the first bar with a space on each side, or at the first
   * bar when it has none, so a value such as web|api can hold a bar.
   */
  export function parseOptions(text: string) {
    return text
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => {
        const spaced = line.indexOf(' | ');
        const bar = spaced >= 0 ? spaced + 1 : line.indexOf('|');
        if (bar < 0) return { value: line };
        const value = line.slice(0, bar).trim();
        const label = line.slice(bar + 1).trim();
        return label && label !== value ? { value, label } : { value };
      });
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api/client';
  import { connectionsStore } from '../stores/connections';
  import FormField from './FormField.svelte';
  import { optionText, referencesOf } from '../variables';
  import type { DashboardWidget, VariableOption } from '../stores/dashboards';

  // VariablesEditor edits a dashboard's variables as a list
  // grouped by where their picker is drawn, one selected at a time.
  let {
    drafts = $bindable(),
    widgets,
    dashboardId,
  }: {
    drafts: VariableDraft[];
    widgets: DashboardWidget[];
    dashboardId: string;
  } = $props();

  const DISPLAYS = [
    { value: 'above', label: 'Above dashboard' },
    { value: 'label_hidden', label: 'Above dashboard, label hidden' },
    { value: 'menu', label: 'Controls menu' },
    { value: 'hidden', label: 'Hidden' },
  ] as const;

  type Group = { id: string; title: string; hint: string; match: (d: VariableDraft) => boolean };
  const GROUPS: Group[] = [
    { id: 'above', title: 'Above dashboard', hint: 'A picker bar over the dashboard', match: (d) => d.type !== 'constant' && (d.display === 'above' || d.display === 'label_hidden') },
    { id: 'menu', title: 'Controls menu', hint: 'Behind a button in the corner', match: (d) => d.type !== 'constant' && d.display === 'menu' },
    { id: 'hidden', title: 'Hidden', hint: 'No picker; flows, links, and constants set them', match: (d) => d.type === 'constant' || d.display === 'hidden' },
  ];

  let selectedKey = $state<number | null>(drafts[0]?.key ?? null);
  let selected = $derived(drafts.find((d) => d.key === selectedKey) ?? null);
  let haConnections = $derived($connectionsStore.connections.filter((c) => c.kind === 'homeassistant'));
  let tsConnections = $derived(
    $connectionsStore.connections.filter((c) =>
      selected?.query.source.startsWith('prometheus_') ? c.kind === 'prometheus'
      : selected?.query.source === 'influx_query' ? c.kind === 'influxdb'
      : c.kind === 'postgres' || c.kind === 'influxdb'
    )
  );
  let rangeVariables = $derived(drafts.filter((x) => x.type === 'time_range' && x.key !== selected?.key));

  onMount(() => {
    connectionsStore.load().catch(() => {});
  });

  function add() {
    const d = blankDraft(drafts);
    drafts = [...drafts, d];
    selectedKey = d.key;
  }

  function duplicate(d: VariableDraft) {
    let n = 2;
    while (drafts.some((x) => x.name === `${d.name}_${n}`)) n++;
    const copy: VariableDraft = { ...d, query: { ...d.query }, key: nextKey++, name: `${d.name}_${n}` };
    const at = drafts.findIndex((x) => x.key === d.key);
    drafts = [...drafts.slice(0, at + 1), copy, ...drafts.slice(at + 1)];
    selectedKey = copy.key;
  }

  function remove(d: VariableDraft) {
    const at = drafts.findIndex((x) => x.key === d.key);
    drafts = drafts.filter((x) => x.key !== d.key);
    selectedKey = drafts[Math.min(at, drafts.length - 1)]?.key ?? null;
  }

  // move swaps a variable with its neighbor in the same group, which is the
  // order its picker is drawn in.
  function move(d: VariableDraft, delta: number) {
    const group = GROUPS.find((g) => g.match(d))!;
    const members = drafts.filter(group.match);
    const target = members[members.indexOf(d) + delta];
    if (!target) return;
    const list = [...drafts];
    const a = list.indexOf(d);
    const b = list.indexOf(target);
    [list[a], list[b]] = [list[b], list[a]];
    drafts = list;
  }

  // usedBy names the placements whose config or conditions refer to a name.
  function usedBy(name: string): string[] {
    return widgets
      .filter((w) => referencesOf(w).has(name))
      .map((w) => w.display?.name || w.widget?.name || 'Widget');
  }

  // ---- Query preview ----

  let previewing = $state(false);
  let preview = $state<{ key: number; options: VariableOption[]; truncated: boolean; error: string } | null>(null);

  async function runPreview(d: VariableDraft) {
    previewing = true;
    try {
      const res = await api.post<{ options: VariableOption[]; truncated: boolean; error?: string }>(
        `/dashboards/${dashboardId}/variables/preview`,
        { variable: variableOf(d) }
      );
      preview = { key: d.key, options: res.options ?? [], truncated: !!res.truncated, error: res.error ?? '' };
    } catch (e: any) {
      preview = { key: d.key, options: [], truncated: false, error: e?.message || 'The query could not be previewed.' };
    } finally {
      previewing = false;
    }
  }

  // Entity suggestions for the pattern and select fields, per connection.
  let entityIds = $state<Record<string, string[]>>({});
  async function loadEntities(connectionId: string) {
    if (!connectionId || entityIds[connectionId]) return;
    try {
      const res: any = await api.get(`/connections/${connectionId}/ha/states`);
      const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
      entityIds = { ...entityIds, [connectionId]: list.map((e) => String(e.entity_id)).sort() };
    } catch {
      entityIds = { ...entityIds, [connectionId]: [] };
    }
  }
  $effect(() => {
    if (selected?.type === 'query' && selected.query.connection_id && !TIME_SERIES_SOURCES.includes(selected.query.source)) {
      void loadEntities(selected.query.connection_id);
    }
  });

  let selectEntities = $derived(
    (entityIds[selected?.query.connection_id ?? ''] ?? []).filter((id) => id.startsWith('input_select.') || id.startsWith('select.'))
  );
  let entityPrefixes = $derived(
    [...new Set((entityIds[selected?.query.connection_id ?? ''] ?? []).map((id) => id.split('.')[0] + '.*'))]
  );

  const ref = (name: string, suffix = '') => '$' + '{' + name + suffix + '}';
</script>

<div class="variables-editor">
  <aside class="variable-list" aria-label="Variables">
    {#each GROUPS as group (group.id)}
      {@const members = drafts.filter(group.match)}
      <div class="group">
        <div class="group-title" title={group.hint}>{group.title} <span class="count">{members.length}</span></div>
        {#each members as d (d.key)}
          <button
            type="button"
            class="variable-item"
            class:active={d.key === selectedKey}
            onclick={() => (selectedKey = d.key)}
          >
            <span class="item-name">{d.name || '(unnamed)'}</span>
            {#if d.type !== 'custom'}<span class="type-badge">{d.type}</span>{/if}
          </button>
        {/each}
      </div>
    {/each}
    <button type="button" class="btn btn-secondary btn-sm w-full" onclick={add}>
      <i class="las la-plus mr-1"></i>Variable
    </button>
  </aside>

  <section class="variable-detail">
    {#if !selected}
      <div class="empty">
        <p>Variables template widgets on a value such as a person or a room.</p>
        <p>
          Write <code>{ref('name')}</code> in a widget's settings for the value or <code>{ref('name', ':text')}</code>
          for its label. Viewers pick a value, a <code>var-name</code> link opens on one, and a Dashboard Command or
          a followed Home Assistant helper sets it for everyone.
        </p>
      </div>
    {:else}
      {@const d = selected}
      <div class="detail-actions">
        <select class="input text-sm" aria-label="Variable type" bind:value={d.type}>
          <option value="custom">Custom list</option>
          <option value="query">Query</option>
          <option value="time_range">Time range</option>
          <option value="interval">Interval</option>
          <option value="constant">Constant</option>
        </select>
        <div class="flex-1"></div>
        <button type="button" class="btn btn-secondary btn-sm" title="Move up" aria-label="Move up" onclick={() => move(d, -1)}>
          <i class="las la-arrow-up"></i>
        </button>
        <button type="button" class="btn btn-secondary btn-sm" title="Move down" aria-label="Move down" onclick={() => move(d, 1)}>
          <i class="las la-arrow-down"></i>
        </button>
        <button type="button" class="btn btn-secondary btn-sm" onclick={() => duplicate(d)}>
          <i class="las la-copy mr-1"></i>Duplicate
        </button>
        <button type="button" class="btn btn-secondary btn-sm" onclick={() => remove(d)}>
          <i class="las la-trash mr-1"></i>Delete
        </button>
      </div>

      {#if d.type === 'constant'}
        <FormField label="Name" id="var-name">
          <input id="var-name" class="input w-full" bind:value={d.name} placeholder="ha" />
        </FormField>
      {:else}
        <div class="grid grid-cols-2 gap-3">
          <FormField label="Name" id="var-name">
            <input id="var-name" class="input w-full" bind:value={d.name} placeholder="person" />
          </FormField>
          <FormField label="Label" id="var-label">
            <input id="var-label" class="input w-full" bind:value={d.label} placeholder="Person" />
          </FormField>
        </div>
        <FormField label="Description" id="var-description">
          <input id="var-description" class="input w-full" bind:value={d.description} placeholder="Shown as the picker's tooltip" />
        </FormField>
        <FormField label="Display" id="var-display">
          <select id="var-display" class="input w-full" bind:value={d.display}>
            {#each DISPLAYS as option (option.value)}
              <option value={option.value}>{option.label}</option>
            {/each}
          </select>
        </FormField>
      {/if}

      {#if d.type === 'custom'}
        <div class="grid grid-cols-5 gap-3">
          <div class="col-span-3">
            <FormField label="Options, one per line as value | label" id="var-options">
              <textarea id="var-options" class="input w-full font-mono text-xs" rows="6" bind:value={d.options}
                placeholder={'james | James\nsam | Sam'}></textarea>
            </FormField>
          </div>
          <div class="col-span-2">
            <FormField label="Default" id="var-default">
              <select id="var-default" class="input w-full" bind:value={d.default}>
                <option value="">First option</option>
                <!-- Keyed by position: a draft may list a value twice until the server refuses it. -->
                {#each parseOptions(d.options) as option, i (i)}
                  <option value={option.value}>{optionText(option)}</option>
                {/each}
              </select>
            </FormField>
          </div>
        </div>
      {:else if d.type === 'time_range'}
        <div class="grid grid-cols-5 gap-3">
          <div class="col-span-3">
            <FormField label="Presets, one per line as from,to | label" id="var-presets">
              <textarea id="var-presets" class="input w-full font-mono text-xs" rows="6" bind:value={d.options}
                placeholder={'now-24h,now | Last 24 hours\nnow/d,now/d | Today'}></textarea>
            </FormField>
            <p class="hint">Empty offers the built-in quick ranges. Viewers may also enter any range.</p>
          </div>
          <div class="col-span-2">
            <FormField label="Default" id="var-range-default">
              <input id="var-range-default" class="input w-full font-mono" bind:value={d.default} placeholder="now-6h,now" />
            </FormField>
          </div>
        </div>
        <p class="hint">
          Ends are <code>now</code> with offsets and rounding such as <code>now-7d</code> or <code>now-1d/d</code>, RFC 3339 times,
          or Unix milliseconds. Charts take it as <code>{ref(d.name)}</code>; <code>{ref(d.name, ':from')}</code> and
          <code>{ref(d.name, ':to')}</code> give one end.
        </p>
      {:else if d.type === 'interval'}
        <div class="grid grid-cols-5 gap-3">
          <div class="col-span-3">
            <FormField label="Intervals, one per line" id="var-intervals">
              <textarea id="var-intervals" class="input w-full font-mono text-xs" rows="5" bind:value={d.options}
                placeholder={'1m\n5m\n1h\n1d'}></textarea>
            </FormField>
          </div>
          <div class="col-span-2">
            <FormField label="Default" id="var-interval-default">
              <input id="var-interval-default" class="input w-full font-mono" bind:value={d.default} placeholder="First option" />
            </FormField>
          </div>
        </div>
        <label class="flex items-center gap-2">
          <input type="checkbox" bind:checked={d.auto} />
          <span class="text-sm">Offer auto, sized from a time range</span>
        </label>
        {#if d.auto}
          <div class="grid grid-cols-3 gap-3">
            <FormField label="Time range variable" id="var-range-var">
              <select id="var-range-var" class="input w-full" bind:value={d.range_variable}>
                <option value="">(last 6 hours)</option>
                {#each rangeVariables as r (r.key)}<option value={r.name}>{r.name}</option>{/each}
              </select>
            </FormField>
            <FormField label="Steps" id="var-steps">
              <input id="var-steps" class="input w-full" type="number" min="1" bind:value={d.steps} placeholder="30" />
            </FormField>
            <FormField label="Minimum" id="var-min-interval">
              <input id="var-min-interval" class="input w-full font-mono" bind:value={d.min_interval} placeholder="10s" />
            </FormField>
          </div>
        {/if}
      {:else if d.type === 'constant'}
        <FormField label="Value" id="var-value">
          <input id="var-value" class="input w-full font-mono" bind:value={d.value} placeholder="A connection ID, entity prefix, or any fixed text" />
        </FormField>
        <p class="hint">A constant has no picker and no command changes it. Use it for a value many widgets repeat.</p>
      {:else}
        <div class="grid grid-cols-2 gap-3">
          <FormField label="Source" id="var-source">
            <select id="var-source" class="input w-full" bind:value={d.query.source}>
              <option value="ha_entities">Home Assistant entities matching a pattern</option>
              <option value="ha_select">Home Assistant input_select or select options</option>
              <option value="prometheus_label_values">Prometheus label values</option>
              <option value="prometheus_metrics">Prometheus metric names</option>
              <option value="prometheus_query">Prometheus query result</option>
              <option value="influx_query">InfluxDB query</option>
              <option value="sql_query">SQL query</option>
            </select>
          </FormField>
          <FormField label="Connection" id="var-connection">
            <select id="var-connection" class="input w-full" bind:value={d.query.connection_id}>
              <option value="">(select a connection)</option>
              {#each TIME_SERIES_SOURCES.includes(d.query.source) ? tsConnections : haConnections as conn (conn.id)}
                <option value={conn.id}>{conn.name}</option>
              {/each}
            </select>
          </FormField>
        </div>
        {#if TIME_SERIES_SOURCES.includes(d.query.source)}
          {#if d.query.source === 'prometheus_label_values' || d.query.source === 'prometheus_query'}
            <FormField label={d.query.source === 'prometheus_query' ? 'Label (empty lists series names)' : 'Label'} id="var-prom-label">
              <input id="var-prom-label" class="input w-full font-mono" bind:value={d.query.label} placeholder="instance" />
            </FormField>
          {/if}
          {#if d.query.source === 'prometheus_label_values' || d.query.source === 'prometheus_metrics'}
            <FormField label="Series selector (optional)" id="var-prom-match">
              <input id="var-prom-match" class="input w-full font-mono" bind:value={d.query.match} placeholder={'up{job="node"}'} />
            </FormField>
          {:else}
            {#if d.query.source === 'influx_query'}
              <FormField label="Language" id="var-influx-lang">
                <select id="var-influx-lang" class="input w-full" bind:value={d.query.language}>
                  <option value="">InfluxQL</option>
                  <option value="flux">Flux (InfluxDB 2)</option>
                  <option value="sql">SQL (InfluxDB 3)</option>
                </select>
              </FormField>
            {/if}
            <FormField label="Query" id="var-ts-text">
              <textarea id="var-ts-text" class="input w-full font-mono text-xs" rows="4" bind:value={d.query.text}
                placeholder={d.query.source === 'sql_query' ? 'SELECT DISTINCT room AS __value, name AS __text FROM rooms' : d.query.source === 'influx_query' ? 'SHOW TAG VALUES FROM "temperature" WITH KEY = "room"' : 'up{job="node"}'}></textarea>
            </FormField>
          {/if}
          <FormField label="Time range variable for time macros" id="var-ts-range">
            <select id="var-ts-range" class="input w-full" bind:value={d.query.range_variable}>
              <option value="">(last 6 hours)</option>
              {#each rangeVariables as r (r.key)}<option value={r.name}>{r.name}</option>{/each}
            </select>
          </FormField>
          <p class="hint">
            The query may refer to variables listed before this one, such as <code>{ref('host')}</code>; values are escaped
            for the query's strings, and <code>{ref('host', ':raw')}</code>, <code>:regex</code>, or <code>:sqlstring</code> choose another form.
          </p>
        {:else if d.query.source === 'ha_entities'}
          <FormField label="Entity pattern" id="var-pattern">
            <input id="var-pattern" class="input w-full font-mono" list="var-patterns" bind:value={d.query.pattern} placeholder="person.*" />
            <datalist id="var-patterns">
              {#each entityPrefixes as prefix}<option value={prefix}></option>{/each}
            </datalist>
          </FormField>
          <p class="hint">Each matching entity is an option: its ID the value, its friendly name the label. <code>*</code> matches anything.</p>
        {:else}
          <FormField label="Entity" id="var-entity">
            <input id="var-entity" class="input w-full font-mono" list="var-selects" bind:value={d.query.entity_id} placeholder="input_select.active_person" />
            <datalist id="var-selects">
              {#each selectEntities as id}<option value={id}></option>{/each}
            </datalist>
          </FormField>
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={d.query.follow} />
            <span class="text-sm">Follow the entity's state</span>
          </label>
          <p class="hint" style="margin-top: -0.5rem;">
            Every viewer switches when the helper changes in Home Assistant, so its automations drive the dashboard.
            Viewers may read the helper's state. Dashboard Commands cannot set a followed variable.
          </p>
        {/if}
        <div class="grid grid-cols-5 gap-3">
          <div class="col-span-3">
            <FormField label="Regex (optional)" id="var-regex">
              <input id="var-regex" class="input w-full font-mono" bind:value={d.query.regex} placeholder={'^person\\.(?<value>.+)$'} />
            </FormField>
          </div>
          <div class="col-span-2">
            <FormField label="Sort" id="var-sort">
              <select id="var-sort" class="input w-full" bind:value={d.query.sort}>
                <option value="">As the source lists them</option>
                <option value="asc">By label, A to Z</option>
                <option value="desc">By label, Z to A</option>
              </select>
            </FormField>
          </div>
        </div>
        <p class="hint" style="margin-top: -0.5rem;">
          A <code>value</code> group, or the first group, becomes the value and a <code>text</code> group the label.
          Items the regex does not match are left out.
        </p>
        <FormField label="Default" id="var-query-default">
          <input id="var-query-default" class="input w-full" list="var-query-options" bind:value={d.default} placeholder="First option" />
          <datalist id="var-query-options">
            {#each preview?.key === d.key ? preview.options : [] as option, i (i)}
              <option value={option.value}>{optionText(option)}</option>
            {/each}
          </datalist>
        </FormField>
        <div class="preview">
          <div class="flex items-center justify-between">
            <span class="text-sm font-medium">
              Preview of values{#if preview?.key === d.key} ({preview.options.length}){/if}
            </span>
            <button type="button" class="btn btn-secondary btn-sm" disabled={previewing || !d.query.connection_id} onclick={() => runPreview(d)}>
              <i class="las la-play mr-1"></i>{previewing ? 'Running...' : 'Run query'}
            </button>
          </div>
          {#if preview?.key === d.key}
            {#if preview.error}
              <p class="preview-error">{preview.error}</p>
            {:else if preview.options.length === 0}
              <p class="hint">No values found.</p>
            {:else}
              <div class="chips">
                {#each preview.options as option, i (i)}
                  <span class="chip" title={option.value}>{optionText(option)}</span>
                {/each}
              </div>
              {#if preview.truncated}
                <p class="hint">More matched; the first 50 are kept.</p>
              {/if}
            {/if}
          {/if}
        </div>
        <p class="hint">Viewers may read what every value's widgets bind, so a query that grows grants more. Options refresh when a viewer loads.</p>
      {/if}

      <div class="used-by">
        <span class="text-sm font-medium">Used by</span>
        {#if d.name.trim()}
          {@const users = usedBy(d.name.trim())}
          {#if users.length === 0}
            <p class="hint">No widget uses <code>{ref(d.name.trim())}</code> yet.</p>
          {:else}
            <div class="chips">
              {#each users as name, i (i)}<span class="chip">{name}</span>{/each}
            </div>
          {/if}
          <p class="hint">Write <code>{ref(d.name.trim())}</code> for the value or <code>{ref(d.name.trim(), ':text')}</code> for its label.</p>
        {/if}
      </div>
    {/if}
  </section>
</div>

<style>
  .variables-editor {
    display: grid;
    grid-template-columns: 13rem minmax(0, 1fr);
    gap: 1.25rem;
    min-height: 24rem;
  }

  .variable-list {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding-right: 1rem;
    border-right: 1px solid var(--theme-surface0, #313244);
  }

  .group {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }

  .group-title {
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--theme-text-muted, #a6adc8);
    padding: 0 0.4rem 0.2rem;
  }

  .count {
    margin-left: 0.25rem;
    opacity: 0.7;
  }

  .variable-item {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.35rem 0.5rem;
    border: none;
    border-radius: 6px;
    background: none;
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    text-align: left;
    cursor: pointer;
  }

  .variable-item:hover {
    background: var(--theme-surface0, #313244);
  }

  .variable-item.active {
    background: var(--theme-surface1, #45475a);
  }

  .item-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-mono, monospace);
  }

  .type-badge {
    font-size: 0.65rem;
    padding: 0 0.35rem;
    border-radius: 999px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text-muted, #a6adc8);
  }

  .variable-detail {
    display: flex;
    flex-direction: column;
    gap: 0.85rem;
    min-width: 0;
  }

  .detail-actions {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .empty {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    font-size: 0.85rem;
    color: var(--theme-text-muted, #a6adc8);
  }

  .hint {
    font-size: 0.75rem;
    color: var(--theme-text-muted, #a6adc8);
  }

  .preview,
  .used-by {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.75rem;
    border-radius: 8px;
    background: var(--theme-surface0, #313244);
  }

  .preview-error {
    font-size: 0.8rem;
    color: var(--theme-error, #f38ba8);
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
  }

  .chip {
    padding: 0.1rem 0.5rem;
    font-size: 0.75rem;
    border-radius: 999px;
    background: var(--theme-surface1, #45475a);
  }
</style>
