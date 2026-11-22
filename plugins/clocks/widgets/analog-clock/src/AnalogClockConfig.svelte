<svelte:options customElement="analog-clock-config" />

<script lang="ts">
  let { config: configAttr = "{}" } = $props();

  const DEFAULTS = {
    timezone: "",
    secondHand: "tick",
    faceStyle: "dark",
    accentColor: "",
    layout: "classic",
    secondTimezone: "Europe/London",
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
    host = (node.getRootNode() as ShadowRoot)?.host as HTMLElement || node;
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
      onchange={(e) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="classic">Classic Dial</option>
      <option value="minimalist">Minimalist</option>
      <option value="station">Station (Swiss)</option>
      <option value="bauhaus">Bauhaus</option>
      <option value="dual-time">Dual Time</option>
      <option value="skeleton">Skeleton</option>
      <option value="square-face">Square Face</option>
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
      oninput={(e) => { cfg.timezone = (e.target as HTMLInputElement).value; dispatch(); }}
      placeholder="System (leave empty)"
    />
  </div>

  <!-- Second Hand -->
  <div class="field">
    <label class="label" for="second-hand-select">Second Hand</label>
    <select
      id="second-hand-select"
      class="input"
      value={cfg.secondHand}
      onchange={(e) => { cfg.secondHand = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="tick">Tick</option>
      <option value="sweep">Sweep</option>
    </select>
  </div>

  <!-- Face Style -->
  <div class="field">
    <label class="label" for="face-style-select">Face Style</label>
    <select
      id="face-style-select"
      class="input"
      value={cfg.faceStyle}
      onchange={(e) => { cfg.faceStyle = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="light">Light</option>
      <option value="dark">Dark</option>
      <option value="transparent">Transparent</option>
    </select>
  </div>

  <!-- Accent Color -->
  <div class="field">
    <label class="label" for="accent-color-picker">Accent Color</label>
    <div class="color-row">
      <input
        id="accent-color-picker"
        type="color"
        class="color-picker"
        value={cfg.accentColor || "#89b4fa"}
        oninput={(e) => { cfg.accentColor = (e.target as HTMLInputElement).value; dispatch(); }}
      />
      <button
        class="auto-btn"
        class:active={!cfg.accentColor}
        onclick={() => { cfg.accentColor = ""; dispatch(); }}
      >Auto</button>
    </div>
  </div>

  <!-- Second Timezone (only for dual-time) -->
  {#if cfg.layout === "dual-time"}
    <div class="field">
      <label class="label" for="second-timezone-input">Second Timezone</label>
      <input
        id="second-timezone-input"
        type="text"
        class="input"
        value={cfg.secondTimezone}
        oninput={(e) => { cfg.secondTimezone = (e.target as HTMLInputElement).value; dispatch(); }}
        placeholder="e.g. Europe/London"
      />
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
</style>
