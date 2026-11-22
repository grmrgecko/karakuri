<script lang="ts" module>
  export interface ConnectionEntry {
    id: string;
    name: string;
    kind: string;
  }
</script>

<script lang="ts">
  import SourceSelect from "../../../shared/SourceSelect.svelte";
  import { hasReference, resolve, type BridgeVariables } from "../../../shared/variables";
  import { LANGUAGES, type ChartQuery, type EnergyQuery, type HAQuery } from "../config";

  // QueryEditor edits one query: its connection, language, text with name
  // suggestions from the connection's metadata, and its options.
  let {
    query,
    connections,
    variables,
    canRemove,
    canDuplicate,
    previewing = false,
    onchange,
    onremove,
    onduplicate,
    onpreview,
  }: {
    query: ChartQuery;
    connections: ConnectionEntry[];
    variables: BridgeVariables;
    canRemove: boolean;
    canDuplicate: boolean;
    previewing?: boolean;
    onchange: (q: ChartQuery) => void;
    onremove: () => void;
    onduplicate: () => void;
    onpreview: () => void;
  } = $props();

  const uid = Math.random().toString(36).slice(2, 8);
  const bridge = () => (window as any).__karakuri;

  // The connection a ${name} reference names now decides the kind, so the
  // languages offered match what the query will run against.
  let connectionId = $derived(hasReference(query.connectionId) ? resolve(query.connectionId, variables) : query.connectionId);
  let kind = $derived(connections.find((c) => c.id === connectionId)?.kind ?? "");
  let languages = $derived(kind ? (LANGUAGES[kind] ?? []) : Object.values(LANGUAGES).flat().filter((l, i, a) => a.findIndex((x) => x.id === l.id) === i));
  let language = $derived(query.language || languages[0]?.id || "");
  let isHA = $derived(language === "ha_history" || language === "ha_statistics");
  let isEnergy = $derived(language === "energy");

  // Energy targets with their names, read from the energy settings.
  let energyTargets = $state<{ target: string; name: string }[]>([]);
  let energyLoaded = false;
  $effect(() => {
    if (!isEnergy || energyLoaded) return;
    energyLoaded = true;
    void loadEnergyTargets();
  });

  async function loadEnergyTargets(): Promise<void> {
    const list = (res: any) => (Array.isArray(res) ? res : (res?.data ?? []));
    const out = [
      { target: "house", name: "House" },
      { target: "untracked", name: "Untracked" },
      { target: "solar", name: "Solar" },
    ];
    try {
      const [groups, sources] = await Promise.all([bridge().api.get("/energy/groups"), bridge().api.get("/energy/sources")]);
      for (const g of list(groups)) out.push({ target: `group:${g.id}`, name: `Group: ${g.name}` });
      for (const s of list(sources)) out.push({ target: `source:${s.id}`, name: s.name });
    } catch {
      // The fixed targets remain; a source can still be typed.
    }
    energyTargets = out;
  }

  function setEnergy(patch: Partial<EnergyQuery>): void {
    set({ energy: { ...(query.energy ?? {}), ...patch } });
  }

  const MEASURES = [
    ["usage", "Usage (kWh)"], ["cost", "Cost"], ["total_cost", "Total cost with charges and credit"],
    ["export_credit", "Export credit"], ["daily_charge", "Daily charge"], ["import", "Grid import"],
    ["export", "Grid export"], ["production", "Solar production"], ["self_consumption", "Solar used at home"],
    ["solar_share", "Solar share of use (%)"],
  ];

  let open = $state(true);
  let suggestions = $state<string[]>([]);
  let metaFor = "";
  let matches = $state<string[]>([]);
  let textarea = $state<HTMLTextAreaElement | null>(null);

  function set(patch: Partial<ChartQuery>): void {
    onchange({ ...query, ...patch });
  }

  // changeConnection keeps the language when the new connection accepts it
  // and otherwise picks the connection's first, so a saved query always
  // names one.
  function changeConnection(id: string): void {
    const target = hasReference(id) ? resolve(id, variables) : id;
    const k = connections.find((c) => c.id === target)?.kind;
    const langs = k ? (LANGUAGES[k] ?? []) : [];
    const keep = langs.some((l) => l.id === query.language);
    set({ connectionId: id, language: keep || !langs.length ? query.language : langs[0].id });
  }

  function setHA(patch: Partial<HAQuery>): void {
    set({ ha: { ...(query.ha ?? {}), ...patch } });
  }

  function metaKind(lang: string): string {
    switch (lang) {
      case "promql":
        return "metrics";
      case "influxql":
      case "flux":
        return "measurements";
      case "sql":
        return "tables";
      case "ha_history":
        return "entities";
      case "ha_statistics":
        return "statistic_ids";
    }
    return "";
  }

  // loadMeta reads the names the connection offers for this language once
  // per connection and language.
  async function loadMeta(): Promise<void> {
    const mk = metaKind(language);
    const key = `${connectionId}\u0000${mk}`;
    if (!connectionId || !mk || key === metaFor || hasReference(connectionId)) return;
    metaFor = key;
    try {
      const res: any = await bridge().api.get(`/connections/${encodeURIComponent(connectionId)}/timeseries/metadata?kind=${mk}`);
      const values = Array.isArray(res?.values) ? res.values : Array.isArray(res?.data?.values) ? res.data.values : [];
      suggestions = values.filter((v: unknown) => typeof v === "string");
    } catch {
      suggestions = [];
    }
  }

  $effect(() => {
    void connectionId;
    void language;
    void loadMeta();
  });

  function wordAt(text: string, at: number): { start: number; word: string } {
    const before = text.slice(0, at);
    const m = /[A-Za-z0-9_:.]+$/.exec(before);
    return m ? { start: at - m[0].length, word: m[0] } : { start: at, word: "" };
  }

  function suggest(): void {
    if (!textarea || !suggestions.length) {
      matches = [];
      return;
    }
    const { word } = wordAt(textarea.value, textarea.selectionStart);
    if (word.length < 1) {
      matches = [];
      return;
    }
    const lower = word.toLowerCase();
    matches = suggestions.filter((s) => s.toLowerCase().includes(lower) && s !== word).slice(0, 8);
  }

  function insert(name: string): void {
    if (!textarea) return;
    const at = textarea.selectionStart;
    const { start } = wordAt(textarea.value, at);
    const text = textarea.value.slice(0, start) + name + textarea.value.slice(at);
    set({ text });
    matches = [];
    const caret = start + name.length;
    requestAnimationFrame(() => {
      textarea?.focus();
      textarea?.setSelectionRange(caret, caret);
    });
  }

  function addListItem(key: "entities" | "statistic_ids", value: string): void {
    const v = value.trim();
    if (!v) return;
    const list = query.ha?.[key] ?? [];
    if (!list.includes(v)) setHA({ [key]: [...list, v] });
  }

  function removeListItem(key: "entities" | "statistic_ids", value: string): void {
    setHA({ [key]: (query.ha?.[key] ?? []).filter((x) => x !== value) });
  }

  const TYPES = ["mean", "min", "max", "sum", "state", "change"];
</script>

<div class="card query" class:hidden-query={query.hide}>
  <div class="row head">
    <button type="button" class="toggle" aria-expanded={open} onclick={() => (open = !open)}>{open ? "▾" : "▸"}</button>
    <input class="input ref" value={query.refId} aria-label="Reference ID" maxlength="8"
           onchange={(e) => set({ refId: (e.target as HTMLInputElement).value.trim() || query.refId })} />
    <span class="summary">{connections.find((c) => c.id === connectionId)?.name ?? (query.connectionId || "No connection")}</span>
    <button type="button" class="icon" title={query.hide ? "Show" : "Hide"} aria-label={query.hide ? "Show query" : "Hide query"}
            onclick={() => set({ hide: !query.hide })}>{query.hide ? "◌" : "◉"}</button>
    <button type="button" class="icon" title="Duplicate" aria-label="Duplicate query" disabled={!canDuplicate} onclick={onduplicate}>⧉</button>
    <button type="button" class="remove" aria-label="Remove query" disabled={!canRemove} onclick={onremove}>×</button>
  </div>
  {#if open}
    <div class="field">
      <label class="label" for="conn-{uid}">Connection</label>
      <SourceSelect id="conn-{uid}" value={query.connectionId} placeholder="Choose a connection"
                    options={connections.map((c) => ({ value: c.id, label: `${c.name} (${c.kind})` }))}
                    onchange={changeConnection} />
    </div>
    <div class="field">
      <label class="label" for="lang-{uid}">Language</label>
      <select id="lang-{uid}" class="input" value={language} onchange={(e) => set({ language: (e.target as HTMLSelectElement).value })}>
        {#each languages as l}<option value={l.id}>{l.name}</option>{/each}
      </select>
    </div>

    {#if isEnergy}
      <div class="field">
        <span class="label">Targets</span>
        <div class="chips">
          {#each energyTargets as t}
            {@const on = (query.energy?.targets ?? ["house"]).includes(t.target)}
            <button type="button" class="chip" class:on
                    onclick={() => {
                      const list = query.energy?.targets ?? ["house"];
                      const next = on ? list.filter((x) => x !== t.target) : [...list, t.target];
                      setEnergy({ targets: next.length ? next : undefined });
                    }}>{t.name}</button>
          {/each}
        </div>
        <span class="hint">Each target is one series, or one row in table format.</span>
      </div>
      <div class="field">
        <label class="label" for="measure-{uid}">Measure</label>
        <select id="measure-{uid}" class="input" value={query.energy?.measure ?? "usage"}
                onchange={(e) => setEnergy({ measure: (e.target as HTMLSelectElement).value })}>
          {#each MEASURES as [id, name]}<option value={id}>{name}</option>{/each}
        </select>
      </div>
      <div class="field">
        <label class="label" for="eperiod-{uid}">Period</label>
        <select id="eperiod-{uid}" class="input" value={query.energy?.period ?? "auto"}
                onchange={(e) => setEnergy({ period: (e.target as HTMLSelectElement).value })}>
          <option value="auto">From the chart's range</option>
          <option value="hour">Hour</option>
          <option value="day">Day</option>
          <option value="week">Week</option>
          <option value="month">Month</option>
        </select>
      </div>
      <div class="field">
        <label class="label" for="eformat-{uid}">Format</label>
        <select id="eformat-{uid}" class="input" value={query.format}
                onchange={(e) => set({ format: (e.target as HTMLSelectElement).value as ChartQuery["format"] })}>
          <option value="time_series">A series per target</option>
          <option value="table">One total per target, for pie and bar charts</option>
        </select>
      </div>
    {:else if isHA}
      <div class="field">
        <span class="label">{language === "ha_history" ? "Entities" : "Statistic IDs"}</span>
        {#each (language === "ha_history" ? query.ha?.entities : query.ha?.statistic_ids) ?? [] as item}
          <div class="row">
            <code class="item">{item}</code>
            <button type="button" class="remove" aria-label="Remove {item}"
                    onclick={() => removeListItem(language === "ha_history" ? "entities" : "statistic_ids", item)}>×</button>
          </div>
        {/each}
        <input class="input mono" list="meta-{uid}" placeholder={language === "ha_history" ? "sensor.living_room_temperature" : "sensor.energy"}
               aria-label="Add"
               onchange={(e) => {
                 const input = e.target as HTMLInputElement;
                 addListItem(language === "ha_history" ? "entities" : "statistic_ids", input.value);
                 input.value = "";
               }} />
        <span class="hint">Pick or type an ID and press Enter. A ${"{name}"} reference is allowed.</span>
      </div>
      {#if language === "ha_history"}
        <div class="field">
          <label class="label" for="attr-{uid}">Attribute</label>
          <input id="attr-{uid}" class="input" placeholder="The state" value={query.ha?.attribute ?? ""}
                 onchange={(e) => setHA({ attribute: (e.target as HTMLInputElement).value.trim() || undefined })} />
        </div>
      {:else}
        <div class="field">
          <label class="label" for="period-{uid}">Period</label>
          <select id="period-{uid}" class="input" value={query.ha?.period ?? "hour"}
                  onchange={(e) => setHA({ period: (e.target as HTMLSelectElement).value })}>
            <option value="5minute">5 minutes</option>
            <option value="hour">Hour</option>
            <option value="day">Day</option>
            <option value="week">Week</option>
            <option value="month">Month</option>
          </select>
        </div>
        <div class="field">
          <span class="label">Types</span>
          <div class="chips">
            {#each TYPES as type}
              {@const on = (query.ha?.types ?? ["mean"]).includes(type)}
              <button type="button" class="chip" class:on
                      onclick={() => {
                        const list = query.ha?.types ?? ["mean"];
                        const next = on ? list.filter((x) => x !== type) : [...list, type];
                        setHA({ types: next.length ? next : undefined });
                      }}>{type}</button>
            {/each}
          </div>
        </div>
      {/if}
      <datalist id="meta-{uid}">
        {#each suggestions.slice(0, 500) as s}<option value={s}></option>{/each}
      </datalist>
    {:else}
      <div class="field text-field">
        <label class="label" for="text-{uid}">Query</label>
        <textarea id="text-{uid}" class="input mono" rows="4" spellcheck="false" bind:this={textarea} value={query.text}
                  placeholder={language === "promql" ? "rate(node_cpu_seconds_total[$__rate_interval])" : language === "flux" ? 'from(bucket: "home") |> range(start: v.timeRangeStart)' : "SELECT mean(value) FROM … WHERE $timeFilter GROUP BY time($__interval)"}
                  oninput={(e) => {
                    set({ text: (e.target as HTMLTextAreaElement).value });
                    suggest();
                  }}
                  onkeydown={(e) => {
                    if (e.key === "Escape") matches = [];
                    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                      e.preventDefault();
                      onpreview();
                    }
                  }}
                  onblur={() => setTimeout(() => (matches = []), 150)}></textarea>
        {#if matches.length}
          <div class="suggestions" role="listbox">
            {#each matches as m}
              <button type="button" role="option" aria-selected="false" class="suggestion" onmousedown={(e) => { e.preventDefault(); insert(m); }}>{m}</button>
            {/each}
          </div>
        {/if}
        <span class="hint">Macros: $__interval, $__rate_interval, $timeFilter, $__timeFilter(column). Ctrl+Enter previews.</span>
      </div>
      <div class="row">
        <div class="field">
          <label class="label" for="legend-{uid}">Legend</label>
          <input id="legend-{uid}" class="input mono" placeholder={"{{instance}}"} value={query.legendFormat}
                 onchange={(e) => set({ legendFormat: (e.target as HTMLInputElement).value })} />
        </div>
        <div class="field">
          <label class="label" for="format-{uid}">Format</label>
          <select id="format-{uid}" class="input" value={query.format}
                  onchange={(e) => set({ format: (e.target as HTMLSelectElement).value as ChartQuery["format"] })}>
            <option value="time_series">Time series</option>
            <option value="table">Table</option>
          </select>
        </div>
      </div>
    {/if}

    <div class="row">
      <div class="field">
        <label class="label" for="mdp-{uid}">Max data points</label>
        <input id="mdp-{uid}" class="input" type="number" min="0" placeholder="Width" value={query.maxDataPoints || ""}
               onchange={(e) => {
                 const n = Math.max(0, Math.floor(Number((e.target as HTMLInputElement).value) || 0));
                 set({ maxDataPoints: n });
               }} />
      </div>
      <div class="field">
        <label class="label" for="mi-{uid}">Min interval</label>
        <input id="mi-{uid}" class="input" placeholder="15s" value={query.minInterval}
               onchange={(e) => set({ minInterval: (e.target as HTMLInputElement).value.trim() })} />
      </div>
    </div>
    {#if language === "promql"}
      <label class="checkbox-label">
        <input type="checkbox" checked={query.instant} onchange={(e) => set({ instant: (e.target as HTMLInputElement).checked })} />
        <span>Instant (one value at the range end)</span>
      </label>
    {/if}
    <div class="row">
      <button type="button" class="add" disabled={previewing || !query.connectionId} onclick={onpreview}>
        {previewing ? "Running…" : "Preview"}
      </button>
    </div>
  {/if}
</div>

<style>
  .head {
    align-items: center;
  }

  /* Outranks the panel's shared full-width input rule. */
  .head .ref {
    width: 3rem;
    flex: none;
    text-align: center;
    font-weight: 600;
  }

  .summary {
    flex: 1;
    min-width: 0;
    font-size: 0.8rem;
    color: var(--theme-text-muted, #a6adc8);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .toggle,
  .icon {
    background: none;
    border: none;
    color: var(--theme-text-muted, #a6adc8);
    cursor: pointer;
    font: inherit;
    padding: 0 0.2rem;
  }

  .hidden-query {
    opacity: 0.7;
  }

  .text-field {
    position: relative;
  }

  textarea {
    resize: vertical;
    min-height: 4.5rem;
  }

  .suggestions {
    display: flex;
    flex-direction: column;
    max-height: 12rem;
    overflow: auto;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-dropdown-bg, var(--theme-surface0, #313244));
  }

  .suggestion {
    text-align: left;
    padding: 0.25rem 0.5rem;
    background: none;
    border: none;
    color: var(--theme-text, #cdd6f4);
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.75rem;
    cursor: pointer;
  }

  .suggestion:hover {
    background: var(--theme-hover-bg, var(--theme-surface1, #45475a));
  }

  .item {
    flex: 1;
    font-size: 0.75rem;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
