<svelte:options customElement="digital-clock-config" />

<script lang="ts">
  interface TimezoneEntry {
    timezone: string;
    label: string;
  }

  interface ClockConfig {
    timezone: string;
    format: string;
    showSeconds: boolean;
    fontStyle: string;
    color: string;
    layout: string;
    worldTimezones: TimezoneEntry[];
  }

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  const DEFAULTS: ClockConfig = {
    timezone: "",
    format: "24h",
    showSeconds: true,
    fontStyle: "monospace",
    color: "",
    layout: "standard-inline",
    worldTimezones: [
      { timezone: "America/New_York", label: "New York" },
      { timezone: "Europe/London", label: "London" },
      { timezone: "Asia/Tokyo", label: "Tokyo" },
    ],
  };

  let cfg = $state<ClockConfig>({ ...DEFAULTS });
  let host = $state<Element | null>(null);

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      cfg = { ...DEFAULTS, ...parsed } as ClockConfig;
      // Ensure worldTimezones is always an array.
      if (!Array.isArray(cfg.worldTimezones)) {
        cfg.worldTimezones = [...DEFAULTS.worldTimezones];
      }
    } catch {
      cfg = { ...DEFAULTS };
    }
  });

  function dispatch(): void {
    const event = new CustomEvent("configchange", {
      detail: { ...cfg },
      bubbles: true,
      composed: true,
    });
    host?.dispatchEvent(event);
  }

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  function addTimezone(): void {
    cfg.worldTimezones = [
      ...cfg.worldTimezones,
      { timezone: "UTC", label: "UTC" },
    ];
    dispatch();
  }

  function removeTimezone(index: number): void {
    cfg.worldTimezones = cfg.worldTimezones.filter((_: TimezoneEntry, i: number) => i !== index);
    dispatch();
  }

  function updateTimezoneField(index: number, field: string, value: string): void {
    cfg.worldTimezones = cfg.worldTimezones.map((entry: TimezoneEntry, i: number) =>
      i === index ? { ...entry, [field]: value } : entry
    );
    dispatch();
  }
</script>

<div class="config-form" use:setHost>
  <!-- Layout -->
  <div class="field">
    <label class="label" for="layout-select">Layout</label>
    <select
      id="layout-select"
      class="input"
      value={cfg.layout}
      onchange={(e: Event) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="standard-inline">Standard Inline</option>
      <option value="stacked-vertical">Stacked Vertical</option>
      <option value="big-date">Big Date</option>
      <option value="world-list">World List</option>
      <option value="retro-lcd">Retro LCD</option>
      <option value="time-progress">Time Progress</option>
      <option value="sidebar-date">Sidebar Date</option>
    </select>
  </div>

  <!-- Timezone -->
  <div class="field">
    <label class="label" for="timezone-input">Timezone</label>
    <input
      id="timezone-input"
      type="text"
      class="input"
      value={cfg.timezone}
      placeholder="System (leave empty)"
      oninput={(e: Event) => { cfg.timezone = (e.target as HTMLInputElement).value; dispatch(); }}
    />
    <span class="hint">IANA timezone, empty for system default</span>
  </div>

  <!-- Format -->
  <div class="field">
    <label class="label" for="format-select">Format</label>
    <select
      id="format-select"
      class="input"
      value={cfg.format}
      onchange={(e: Event) => { cfg.format = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="24h">24-hour</option>
      <option value="12h">12-hour</option>
    </select>
  </div>

  <!-- Show Seconds -->
  <div class="field checkbox-field">
    <label class="checkbox-label">
      <input
        type="checkbox"
        checked={cfg.showSeconds}
        onchange={(e: Event) => { cfg.showSeconds = (e.target as HTMLInputElement).checked; dispatch(); }}
      />
      <span>Show Seconds</span>
    </label>
  </div>

  <!-- Font Style -->
  <div class="field">
    <label class="label" for="font-style-select">Font Style</label>
    <select
      id="font-style-select"
      class="input"
      value={cfg.fontStyle}
      onchange={(e: Event) => { cfg.fontStyle = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="monospace">Monospace</option>
      <option value="sans">Sans-serif</option>
      <option value="segmented">Segmented</option>
    </select>
  </div>

  <!-- Color -->
  <div class="field">
    <label class="label" for="color-picker">Color</label>
    <div class="color-row">
      <input
        id="color-picker"
        type="color"
        class="color-picker"
        value={cfg.color || "#cdd6f4"}
        oninput={(e: Event) => { cfg.color = (e.target as HTMLInputElement).value; dispatch(); }}
      />
      <button
        class="auto-btn"
        class:active={!cfg.color}
        onclick={() => { cfg.color = ""; dispatch(); }}
      >Auto</button>
    </div>
  </div>

  <!-- World Timezones (only for world-list layout) -->
  {#if cfg.layout === "world-list"}
    <div class="field">
      <span class="label">World Timezones</span>
      <div class="tz-list">
        {#each cfg.worldTimezones as entry, index}
          <div class="tz-row">
            <input
              type="text"
              class="input tz-input"
              value={entry.label}
              placeholder="Label"
              oninput={(e: Event) => updateTimezoneField(index, "label", (e.target as HTMLInputElement).value)}
            />
            <input
              type="text"
              class="input tz-input"
              value={entry.timezone}
              placeholder="IANA Timezone"
              oninput={(e: Event) => updateTimezoneField(index, "timezone", (e.target as HTMLInputElement).value)}
            />
            <button
              class="remove-btn"
              onclick={() => removeTimezone(index)}
              title="Remove"
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
        {/each}
      </div>
      <button class="add-btn" onclick={addTimezone}>+ Add Timezone</button>
    </div>
  {/if}
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

  .checkbox-field {
    flex-direction: row;
    align-items: center;
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    cursor: pointer;
    color: var(--theme-text, #cdd6f4);
  }

  .checkbox-label input[type="checkbox"] {
    width: 16px;
    height: 16px;
    accent-color: var(--theme-accent, #89b4fa);
    cursor: pointer;
  }

  .color-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .color-picker {
    width: 40px;
    height: 32px;
    padding: 2px;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    cursor: pointer;
  }

  .auto-btn {
    padding: 0.3rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    font-family: inherit;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
  }

  .auto-btn:hover {
    background: var(--theme-surface1, #45475a);
  }

  .auto-btn.active {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }

  .tz-list {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .tz-row {
    display: flex;
    gap: 0.4rem;
    align-items: center;
  }

  .tz-input {
    flex: 1;
    min-width: 0;
  }

  .remove-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-error, #f38ba8);
    cursor: pointer;
    flex-shrink: 0;
    transition: background 0.15s, border-color 0.15s;
  }

  .remove-btn:hover {
    background: var(--theme-surface1, #45475a);
    border-color: var(--theme-error, #f38ba8);
  }

  .remove-btn svg {
    width: 14px;
    height: 14px;
  }

  .add-btn {
    padding: 0.35rem 0.75rem;
    border: 1px dashed var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    font-size: 0.8rem;
    font-family: inherit;
    cursor: pointer;
    transition: background 0.15s, border-color 0.15s;
    margin-top: 0.25rem;
  }

  .add-btn:hover {
    background: var(--theme-surface0, #313244);
    border-color: var(--theme-accent, #89b4fa);
  }
</style>
