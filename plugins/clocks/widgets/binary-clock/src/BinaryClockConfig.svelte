<svelte:options customElement="binary-clock-config" />

<script lang="ts">
  let { config: configAttr = "{}" }: { config: string | Record<string, any> } = $props();

  const DEFAULTS = {
    timezone: "",
    format: "24h",
    ledShape: "circle",
    activeColor: "",
    inactiveColor: "",
    layout: "bcd-grid",
  };

  let cfg = $state<Record<string, any>>({ ...DEFAULTS });
  let host = $state<HTMLElement | null>(null);

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      cfg = { ...DEFAULTS, ...parsed };
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
    host = node.getRootNode()?.host || node;
  }

  const layoutOptions = [
    { value: "bcd-grid", label: "BCD Grid" },
    { value: "true-binary-rows", label: "True Binary (Rows)" },
    { value: "true-binary-cols", label: "True Binary (Cols)" },
    { value: "dot-matrix", label: "Dot Matrix" },
    { value: "bar-graph", label: "Bar Graph" },
  ];

  const formatOptions = [
    { value: "24h", label: "24-hour" },
    { value: "12h", label: "12-hour" },
  ];

  const shapeOptions = [
    { value: "circle", label: "Circle" },
    { value: "square", label: "Square" },
  ];
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <label for="layout">Layout</label>
    <select id="layout" bind:value={cfg.layout} onchange={dispatch}>
      {#each layoutOptions as opt}
        <option value={opt.value}>{opt.label}</option>
      {/each}
    </select>
  </div>

  <div class="field">
    <label for="timezone">Timezone</label>
    <input
      id="timezone"
      type="text"
      bind:value={cfg.timezone}
      oninput={dispatch}
      placeholder="System (leave empty)"
    />
  </div>

  <div class="field">
    <label for="format">Format</label>
    <select id="format" bind:value={cfg.format} onchange={dispatch}>
      {#each formatOptions as opt}
        <option value={opt.value}>{opt.label}</option>
      {/each}
    </select>
  </div>

  <div class="field">
    <label for="ledShape">LED Shape</label>
    <select id="ledShape" bind:value={cfg.ledShape} onchange={dispatch}>
      {#each shapeOptions as opt}
        <option value={opt.value}>{opt.label}</option>
      {/each}
    </select>
  </div>

  <div class="field">
    <label for="activeColor">Active Color</label>
    <div class="color-row">
      <input
        type="color"
        class="color-picker"
        value={cfg.activeColor || "#89b4fa"}
        oninput={(e: Event) => { cfg.activeColor = (e.target as HTMLInputElement).value; dispatch(); }}
      />
      <button
        class="auto-btn"
        class:active={!cfg.activeColor}
        onclick={() => { cfg.activeColor = ""; dispatch(); }}
      >Auto</button>
    </div>
  </div>

  <div class="field">
    <label for="inactiveColor">Inactive Color</label>
    <div class="color-row">
      <input
        type="color"
        class="color-picker"
        value={cfg.inactiveColor || "#313244"}
        oninput={(e: Event) => { cfg.inactiveColor = (e.target as HTMLInputElement).value; dispatch(); }}
      />
      <button
        class="auto-btn"
        class:active={!cfg.inactiveColor}
        onclick={() => { cfg.inactiveColor = ""; dispatch(); }}
      >Auto</button>
    </div>
  </div>
</div>

<style>
  :host {
    display: block;
    width: 100%;
    box-sizing: border-box;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  .config-form {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 8px 0;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  label {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  input,
  select {
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-border, #45475a);
    border-radius: var(--theme-radius-md, 8px);
    padding: 8px 10px;
    color: var(--theme-text, #cdd6f4);
    font-family: var(--theme-font-family, "Inter", sans-serif);
    font-size: 0.9rem;
    outline: none;
    transition: border-color 0.15s ease;
  }

  input:focus,
  select:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  input::placeholder {
    color: var(--theme-surface1, #45475a);
    opacity: 0.8;
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
    font-family: var(--theme-font-family, "Inter", sans-serif);
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

  select {
    cursor: pointer;
    appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%23a6adc8' d='M2 4l4 4 4-4'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 10px center;
    padding-right: 28px;
  }
</style>
