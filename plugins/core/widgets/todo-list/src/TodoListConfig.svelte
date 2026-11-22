<svelte:options customElement="todo-list-config" />

<script lang="ts">
  import EntityPicker from "../../shared/EntityPicker.svelte";
  import { parseConfig, type TodoConfig } from "./config";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $state<TodoConfig>(parseConfig("{}"));
  let host = $state<HTMLElement | null>(null);

  // Every edit round trips through a save and comes back as a new attribute a
  // keystroke behind, so the attribute seeds the panel once per placement and
  // the panel owns its state from then on.
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

  function setHost(node: HTMLElement): void {
    host = ((node.getRootNode() as ShadowRoot)?.host as HTMLElement) || node;
  }

  function update(patch: Partial<TodoConfig>): void {
    cfg = { ...cfg, ...patch, source: "ha" };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: { ...cfg }, bubbles: true, composed: true }),
    );
  }
</script>

<div class="config-form" use:setHost>
  <EntityPicker idPrefix="todo" domain="todo" connectionId={cfg.connectionId}
                entityId={cfg.entityId} onchange={(patch) => update(patch)} />

  <div class="field">
    <label class="label" for="todo-title">Title</label>
    <input id="todo-title" class="input" value={cfg.title} placeholder="the list's name"
           oninput={(e: Event) => update({ title: (e.target as HTMLInputElement).value })} />
  </div>

  <div class="field">
    <label class="label" for="todo-max">Most items shown</label>
    <input id="todo-max" class="input" type="number" min="0" step="1" value={cfg.maxItems}
           onchange={(e: Event) => {
             const n = Math.floor(Number((e.target as HTMLInputElement).value));
             update({ maxItems: Number.isFinite(n) && n > 0 ? n : 0 });
           }} />
    <span class="hint">0 shows every item.</span>
  </div>

  <label class="check">
    <input type="checkbox" checked={cfg.showCompleted}
           onchange={(e: Event) => update({ showCompleted: (e.target as HTMLInputElement).checked })} />
    <span>Show completed items</span>
  </label>
  <label class="check">
    <input type="checkbox" checked={cfg.allowAdd}
           onchange={(e: Event) => update({ allowAdd: (e.target as HTMLInputElement).checked })} />
    <span>Allow adding items</span>
  </label>
  <label class="check">
    <input type="checkbox" checked={cfg.displayOnly}
           onchange={(e: Event) => update({ displayOnly: (e.target as HTMLInputElement).checked })} />
    <span>Display only</span>
  </label>
  <span class="hint">
    Display only shows the list without checkboxes or the add field, and the server refuses changes.
    Changes also need a dashboard that allows control.
  </span>
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

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }
</style>
