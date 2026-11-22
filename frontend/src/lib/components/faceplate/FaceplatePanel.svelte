<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../../api/client';
  import { ScriptRunner, maxScriptRounds, themeVars, type ChangeEvent } from '../../faceplateSandbox';
  import TemplateFrame from './TemplateFrame.svelte';

  // FaceplatePanel draws the flow's faceplate tabs. It holds the values being
  // edited across every tab, so a change script can move a control on another
  // tab and Apply sends everything at once.
  export interface Control {
    id: string;
    label: string;
    type: string;
    description?: string;
    default?: unknown;
    min?: number;
    max?: number;
    step?: number;
    options?: { value: unknown; label?: string }[];
    template?: string;
  }
  export interface Tab {
    key: string;
    name: string;
    order: number;
    controls: Control[];
    values: Record<string, unknown>;
    script?: string;
    subflow?: string;
  }
  interface Reading {
    state: string;
    unit?: string;
    name?: string;
    available: boolean;
  }

  let {
    flowId,
    tabs,
    activeKey,
    canApply,
    onApplied,
  }: {
    flowId: string;
    tabs: Tab[];
    activeKey: string;
    canApply: boolean;
    onApplied: () => void;
  } = $props();

  // edits holds values changed since the tabs loaded, by tab key and control.
  let edits = $state<Record<string, Record<string, unknown>>>({});
  let readings = $state<Record<string, Record<string, Reading>>>({});
  let applying = $state(false);
  let error = $state('');
  let notice = $state('');
  let scriptErrors = $state<Record<string, string>>({});
  // brokenTemplates are controls whose template failed; they fall back.
  let brokenTemplates = $state<Record<string, string>>({});

  let active = $derived(tabs.find((t) => t.key === activeKey));
  let dirty = $derived(Object.values(edits).some((m) => Object.keys(m).length > 0));

  function stored(tab: Tab, c: Control): unknown {
    // A stored null is no value, as the server reads it.
    return tab.values[c.id] ?? c.default;
  }
  function current(tab: Tab, c: Control): unknown {
    const e = edits[tab.key];
    return e && c.id in e ? e[c.id] : stored(tab, c);
  }
  function allValues(): Record<string, Record<string, unknown>> {
    const out: Record<string, Record<string, unknown>> = {};
    for (const tab of tabs) {
      out[tab.key] = {};
      for (const c of tab.controls) out[tab.key][c.id] = current(tab, c);
    }
    return out;
  }

  // ---- Change scripts ----

  let runner: ScriptRunner | null = null;
  $effect(() => {
    const scripts = tabs.filter((t) => t.script?.trim()).map((t) => ({ key: t.key, name: t.name, source: t.script! }));
    runner?.destroy();
    scriptErrors = {};
    runner = new ScriptRunner(scripts, (key, message) => (scriptErrors = { ...scriptErrors, [key]: message }));
    return () => runner?.destroy();
  });

  function put(key: string, control: string, value: unknown) {
    edits = { ...edits, [key]: { ...(edits[key] ?? {}), [control]: value } };
  }

  // setValue records a user's change, then lets the change scripts answer it.
  // Scripts may answer each other's sets for a bounded number of rounds.
  async function setValue(tab: Tab, c: Control, value: unknown) {
    const old = current(tab, c);
    put(tab.key, c.id, value);
    notice = '';
    if (!runner) return;
    const names: Record<string, string> = {};
    for (const t of tabs) names[t.name] = t.key;
    let queue: ChangeEvent[] = [{ tab: tab.key, control: c.id, value, old }];
    for (let round = 0; queue.length > 0; round++) {
      if (round >= maxScriptRounds) {
        scriptErrors = { ...scriptErrors, [tab.key]: `the change scripts kept changing each other's values, so they were stopped after ${maxScriptRounds} rounds` };
        return;
      }
      const next: ChangeEvent[] = [];
      for (const change of queue) {
        const result = await runner.change(change, allValues(), names);
        for (const e of result.errors) scriptErrors = { ...scriptErrors, [e.key]: e.message };
        for (const s of result.sets) {
          const target = tabs.find((t) => t.key === s.tab);
          const control = target?.controls.find((x) => x.id === s.control);
          if (!target || !control) continue;
          const before = current(target, control);
          if (JSON.stringify(before) === JSON.stringify(s.value)) continue;
          put(target.key, control.id, s.value);
          next.push({ tab: target.key, control: control.id, value: s.value, old: before });
        }
      }
      queue = next;
    }
  }

  // ---- Readings ----

  async function loadReadings() {
    try {
      readings = await api.get<Record<string, Record<string, Reading>>>(`/flows/${flowId}/faceplates/readings`);
    } catch {
      // Readings are a convenience; the controls work without them.
    }
  }
  loadReadings();
  const readingTimer = setInterval(loadReadings, 5000);
  onDestroy(() => clearInterval(readingTimer));

  // ---- Apply ----

  async function apply() {
    error = '';
    notice = '';
    applying = true;
    try {
      const result = await api.post<{ version: number }>(`/flows/${flowId}/faceplates/apply`, { values: edits });
      notice = `Applied as version ${result.version}`;
      edits = {};
      onApplied();
    } catch (e: any) {
      error = e?.message || 'The values could not be applied';
    } finally {
      applying = false;
    }
  }

  function discard() {
    edits = {};
    notice = '';
    error = '';
  }

  // ---- Editors ----

  function entityValue(v: unknown): { connection_id: string; entity_id: string } {
    const o = (v && typeof v === 'object' ? v : {}) as Record<string, unknown>;
    return { connection_id: String(o.connection_id ?? ''), entity_id: String(o.entity_id ?? '') };
  }
  function jsonText(v: unknown): string {
    return v === undefined || v === null ? '' : JSON.stringify(v, null, 2);
  }
  let jsonErrors = $state<Record<string, string>>({});
  function setJSON(tab: Tab, c: Control, raw: string) {
    const id = `${tab.key}|${c.id}`;
    try {
      const v = raw.trim() === '' ? null : JSON.parse(raw);
      jsonErrors = { ...jsonErrors, [id]: '' };
      setValue(tab, c, v);
    } catch {
      jsonErrors = { ...jsonErrors, [id]: 'Not valid JSON yet' };
    }
  }
  function optionValue(c: Control, raw: string): unknown {
    const o = c.options?.find((x) => String(x.value) === raw);
    return o ? o.value : raw;
  }
  function edited(tab: Tab, c: Control): boolean {
    return !!edits[tab.key] && c.id in edits[tab.key];
  }

  let theme = themeVars();
</script>

{#if active}
  <div class="faceplate">
    {#if active.subflow}
      <p class="text-xs opacity-60 mb-3">From the subflow "{active.subflow}". These values apply to this flow only.</p>
    {/if}
    {#if scriptErrors[active.key]}
      <div class="msg err">Change script: {scriptErrors[active.key]}</div>
    {/if}
    {#if active.controls.length === 0}
      <p class="opacity-60 text-sm">This faceplate has no controls yet. Add them in the Faceplate node's settings on the Flow editor tab.</p>
    {/if}

    <div class="controls">
      {#each active.controls as c (c.id)}
        {@const value = current(active, c)}
        {@const reading = readings[active.key]?.[c.id]}
        {@const fid = `${active.key}|${c.id}`}
        <div class="control" class:changed={edited(active, c)}>
          <div class="head">
            <label class="label" for={fid}>{c.label || c.id}</label>
            {#if reading}
              <span class="reading" class:off={!reading.available} title={reading.name ?? ''}>
                {reading.available ? `${reading.state}${reading.unit ? ' ' + reading.unit : ''}` : 'unavailable'}
              </span>
            {/if}
          </div>
          {#if c.template && !brokenTemplates[fid]}
            <TemplateFrame
              template={c.template}
              templateState={{ value, control: c, values: allValues(), reading: reading ?? null, readings, theme }}
              onSet={(v) => canApply && setValue(active, c, v)}
              onError={(m) => (brokenTemplates = { ...brokenTemplates, [fid]: m })}
            />
          {:else}
            {#if brokenTemplates[fid]}
              <p class="text-xs mb-1" style="color: var(--theme-error);">The custom editor failed ({brokenTemplates[fid]}), so the plain one is shown.</p>
            {/if}
            {#if c.type === 'number' && c.options?.length}
              <select id={fid} class="input" disabled={!canApply} value={String(value ?? '')} onchange={(e) => setValue(active, c, Number(e.currentTarget.value))}>
                {#each c.options as o (String(o.value))}<option value={String(o.value)}>{o.label ?? o.value}</option>{/each}
              </select>
            {:else if c.type === 'number'}
              <div class="row">
                {#if c.min !== undefined && c.max !== undefined}
                  <input type="range" class="grow" disabled={!canApply} min={c.min} max={c.max} step={c.step ?? 'any'} value={Number(value ?? c.min)}
                    oninput={(e) => setValue(active, c, Number(e.currentTarget.value))} aria-label={c.label} />
                {/if}
                <input id={fid} type="number" class="input num" disabled={!canApply} min={c.min} max={c.max} step={c.step ?? 'any'} value={value ?? ''}
                  onchange={(e) => e.currentTarget.value !== '' && setValue(active, c, Number(e.currentTarget.value))} />
              </div>
            {:else if c.type === 'boolean'}
              <label class="row toggle">
                <input id={fid} type="checkbox" disabled={!canApply} checked={value === true} onchange={(e) => setValue(active, c, e.currentTarget.checked)} />
                <span>{value === true ? 'On' : 'Off'}</span>
              </label>
            {:else if c.type === 'string' && c.options?.length}
              <select id={fid} class="input" disabled={!canApply} value={String(value ?? '')} onchange={(e) => setValue(active, c, optionValue(c, e.currentTarget.value))}>
                {#if value === undefined || value === null || value === ''}<option value="">Choose...</option>{/if}
                {#each c.options as o (String(o.value))}<option value={String(o.value)}>{o.label ?? o.value}</option>{/each}
              </select>
            {:else if c.type === 'string'}
              <input id={fid} class="input" disabled={!canApply} value={value ?? ''} onchange={(e) => setValue(active, c, e.currentTarget.value)} />
            {:else if c.type === 'entity'}
              {@const ev = entityValue(value)}
              <div class="row">
                <input id={fid} class="input grow" disabled={!canApply} placeholder="sensor.living_room_lux" value={ev.entity_id}
                  onchange={(e) => setValue(active, c, { ...ev, entity_id: e.currentTarget.value.trim() })} />
                <input class="input conn" disabled={!canApply} placeholder="Connection ID" value={ev.connection_id} aria-label="Home Assistant connection"
                  onchange={(e) => setValue(active, c, { ...ev, connection_id: e.currentTarget.value.trim() })} />
              </div>
            {:else}
              <textarea id={fid} class="input mono" rows="4" disabled={!canApply} value={jsonText(value)} onchange={(e) => setJSON(active, c, e.currentTarget.value)}></textarea>
              {#if jsonErrors[fid]}<p class="text-xs" style="color: var(--theme-error);">{jsonErrors[fid]}</p>{/if}
            {/if}
          {/if}
          {#if c.description}<p class="desc">{c.description}</p>{/if}
        </div>
      {/each}
    </div>

    {#if error}<div class="msg err">{error}</div>{/if}
    {#if notice}<div class="msg">{notice}</div>{/if}
    {#if canApply}
      <div class="actions">
        <button class="btn btn-primary text-sm" disabled={!dirty || applying} onclick={apply}>
          <i class="las la-check mr-1"></i>{applying ? 'Applying...' : 'Apply'}
        </button>
        {#if dirty}<button class="btn text-sm" onclick={discard}>Discard changes</button>{/if}
        <span class="text-xs opacity-60">Apply saves and deploys the flow.</span>
      </div>
    {:else}
      <p class="text-xs opacity-60 mt-4">You can view these settings but not change them.</p>
    {/if}
  </div>
{/if}

<style>
  .faceplate {
    max-width: 44rem;
    margin: 0 auto;
    padding: 1.5rem;
  }
  .controls {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .control {
    padding: 0.75rem 1rem;
    border-radius: 0.5rem;
    background-color: var(--theme-surface0);
    border-left: 3px solid transparent;
  }
  .control.changed {
    border-left-color: var(--theme-accent);
  }
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 0.4rem;
  }
  .label {
    font-weight: 600;
    font-size: 0.9rem;
  }
  .reading {
    font-size: 0.8rem;
    font-variant-numeric: tabular-nums;
    padding: 0.05rem 0.5rem;
    border-radius: 999px;
    background-color: var(--theme-surface1);
  }
  .reading.off {
    opacity: 0.6;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
  }
  .grow {
    flex: 1;
    min-width: 0;
  }
  .num {
    width: 7rem;
  }
  .conn {
    width: 12rem;
  }
  .input {
    width: 100%;
  }
  .row .input.num,
  .row .input.conn {
    width: auto;
  }
  .mono {
    font-family: monospace;
    font-size: 0.8rem;
  }
  .toggle {
    cursor: pointer;
  }
  .desc {
    font-size: 0.75rem;
    opacity: 0.65;
    margin-top: 0.35rem;
  }
  .msg {
    margin-top: 1rem;
    padding: 0.6rem 0.8rem;
    border-radius: 0.4rem;
    font-size: 0.85rem;
    background-color: var(--theme-surface0);
  }
  .msg.err {
    background-color: var(--theme-error);
    color: var(--theme-base);
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-top: 1.25rem;
  }
</style>
