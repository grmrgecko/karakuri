<svelte:options customElement="activity-config" />

<script lang="ts">
  import { onMount } from "svelte";
  import SourceSelect from "../../shared/SourceSelect.svelte";
  import { parseConfig, type ActivityConfig, type Row, type RowSource } from "./config";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $state<ActivityConfig>(parseConfig("{}"));
  let host = $state<HTMLElement | null>(null);
  let controls = $state<{ id: string; name: string }[]>([]);
  let devices = $state<{ id: string; name: string }[]>([]);
  let connections = $state<{ id: string; name: string }[]>([]);

  const bridge = () => (window as any).__karakuri;

  // The attribute seeds the panel once per placement; the panel owns its
  // state from then on, since every edit comes back a keystroke behind.
  let seededFor: string | null = null;
  $effect(() => {
    const widgetId = placementId(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
  });

  function placementId(attr: string | Record<string, unknown>): string {
    if (typeof attr !== "string") return String((attr as any)?.dashboardWidgetId ?? "");
    try {
      return String(JSON.parse(attr)?.dashboardWidgetId ?? "");
    } catch {
      return "";
    }
  }

  function items(res: any): any[] {
    const body = Array.isArray(res) ? res : (res?.data ?? res?.items ?? []);
    return Array.isArray(body) ? body : [];
  }

  onMount(async () => {
    const api = bridge()?.api;
    if (!api) return;
    const [c, d, k] = await Promise.allSettled([api.get("/controls"), api.get("/mqtt_devices"), api.get("/connections")]);
    if (c.status === "fulfilled") controls = items(c.value).map((x: any) => ({ id: x.id, name: x.name }));
    if (d.status === "fulfilled") devices = items(d.value).map((x: any) => ({ id: x.id, name: x.name }));
    if (k.status === "fulfilled") {
      connections = items(k.value).filter((x: any) => x.kind === "homeassistant").map((x: any) => ({ id: x.id, name: x.name }));
    }
  });

  function setHost(node: HTMLElement): void {
    host = ((node.getRootNode() as ShadowRoot)?.host as HTMLElement) || node;
  }

  function emit(): void {
    host?.dispatchEvent(new CustomEvent("configchange", { detail: { ...cfg, rows: cfg.rows.map((r) => ({ ...r })) }, bubbles: true, composed: true }));
  }

  function update(patch: Partial<ActivityConfig>): void {
    cfg = { ...cfg, ...patch };
    emit();
  }

  function addRow(): void {
    const row: Row = { id: Math.random().toString(36).slice(2), source: "control", displayOnly: true };
    update({ rows: [...cfg.rows, row] });
  }

  function updateRow(id: string, patch: Partial<Row>): void {
    update({ rows: cfg.rows.map((r) => (r.id === id ? { ...r, ...patch, displayOnly: true } : r)) });
  }

  function removeRow(id: string): void {
    update({ rows: cfg.rows.filter((r) => r.id !== id) });
  }

  const SOURCES: { value: RowSource; label: string }[] = [
    { value: "control", label: "Control" },
    { value: "mqtt", label: "Device" },
    { value: "ha", label: "Home Assistant entity" },
  ];
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <span class="label">Show activity of</span>
    {#each cfg.rows as row (row.id)}
      <div class="row">
        <select class="input" value={row.source} aria-label="Kind"
                onchange={(e: Event) => updateRow(row.id, { source: (e.target as HTMLSelectElement).value as RowSource, controlId: undefined, deviceId: undefined, connectionId: undefined, entityId: undefined })}>
          {#each SOURCES as s (s.value)}<option value={s.value}>{s.label}</option>{/each}
        </select>
        {#if row.source === "control"}
          <SourceSelect value={row.controlId || ""} placeholder="Select a control..." ariaLabel="Control"
                        options={controls.map((c) => ({ value: c.id, label: c.name }))}
                        onchange={(v) => updateRow(row.id, { controlId: v })} />
        {:else if row.source === "mqtt"}
          <SourceSelect value={row.deviceId || ""} placeholder="Select a device..." ariaLabel="Device"
                        options={devices.map((d) => ({ value: d.id, label: d.name }))}
                        onchange={(v) => updateRow(row.id, { deviceId: v })} />
        {:else}
          <SourceSelect value={row.connectionId || ""} placeholder="Select a connection..." ariaLabel="Connection"
                        options={connections.map((c) => ({ value: c.id, label: c.name }))}
                        onchange={(v) => updateRow(row.id, { connectionId: v })} />
          <input class="input" placeholder="binary_sensor.front_door" value={row.entityId ?? ""} aria-label="Entity"
                 onchange={(e: Event) => updateRow(row.id, { entityId: (e.target as HTMLInputElement).value.trim() })} />
        {/if}
        <button class="remove" onclick={() => removeRow(row.id)} aria-label="Remove">×</button>
      </div>
    {/each}
    <button class="add" onclick={addRow}>Add a control, device, or entity</button>
    <span class="hint">Entities are listed only if the event log is set to log them, under Activity, Settings.</span>
  </div>

  <div class="field">
    <label class="label" for="act-categories">Categories</label>
    <input id="act-categories" class="input" placeholder="device, connection" value={cfg.categories}
           onchange={(e: Event) => update({ categories: (e.target as HTMLInputElement).value })} />
    <span class="hint">Comma separated. Empty shows every category.</span>
  </div>

  <div class="field">
    <label class="label" for="act-levels">Levels</label>
    <input id="act-levels" class="input" placeholder="warning, error" value={cfg.levels}
           onchange={(e: Event) => update({ levels: (e.target as HTMLInputElement).value })} />
  </div>

  <div class="field">
    <label class="label" for="act-limit">Entries shown</label>
    <input id="act-limit" class="input" type="number" min="1" max="50" step="1" value={cfg.limit}
           onchange={(e: Event) => {
             const n = Number((e.target as HTMLInputElement).value);
             update({ limit: Number.isFinite(n) && n >= 1 ? Math.min(Math.round(n), 50) : 10 });
           }} />
  </div>

  <label class="check">
    <input type="checkbox" checked={cfg.showActor}
           onchange={(e: Event) => update({ showActor: (e.target as HTMLInputElement).checked })} />
    <span>Show who did it</span>
  </label>
</div>

<style>
  :host {
    display: block;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  .config-form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .row {
    display: flex;
    gap: 0.3rem;
    align-items: center;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }

  .add,
  .remove {
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    font-size: 0.8rem;
    padding: 0.3rem 0.6rem;
    cursor: pointer;
  }

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }
</style>
