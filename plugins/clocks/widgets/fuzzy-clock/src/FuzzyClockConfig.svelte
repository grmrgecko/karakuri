<svelte:options customElement="fuzzy-clock-config" />

<script lang="ts">
  interface ConfigData {
    timezone: string;
    fuzziness: number;
    language: string;
    typography: string;
    layout: string;
  }

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  const DEFAULTS: ConfigData = {
    timezone: "",
    fuzziness: 3,
    language: "en",
    typography: "serif",
    layout: "single-sentence",
  };

  let cfg = $state<ConfigData>({ ...DEFAULTS });
  let host = $state<HTMLElement | null>(null);

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      cfg = { ...DEFAULTS, ...parsed } as ConfigData;
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
      onchange={(e: Event) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="single-sentence">Single Sentence</option>
      <option value="word-cloud">Word Cloud (Grid)</option>
      <option value="typographic-stack">Typographic Stack</option>
      <option value="bold-hour">Bold Hour</option>
      <option value="approximate">Approximate</option>
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

  <!-- Fuzziness -->
  <div class="field">
    <label class="label" for="fuzziness-select">Fuzziness</label>
    <select
      id="fuzziness-select"
      class="input"
      value={cfg.fuzziness}
      onchange={(e: Event) => { cfg.fuzziness = parseInt((e.target as HTMLSelectElement).value, 10); dispatch(); }}
    >
      <option value="1">1 - Exact</option>
      <option value="2">2 - Five Minutes</option>
      <option value="3">3 - Quarter Hour</option>
      <option value="4">4 - Half Hour</option>
      <option value="5">5 - Hour Only</option>
    </select>
    <span class="hint">How precisely to express the time in words</span>
  </div>

  <!-- Language -->
  <div class="field">
    <label class="label" for="language-input">Language</label>
    <input
      id="language-input"
      type="text"
      class="input"
      value={cfg.language}
      placeholder="en"
      oninput={(e: Event) => { cfg.language = (e.target as HTMLInputElement).value; dispatch(); }}
    />
    <span class="hint">Language code (currently only "en" is supported)</span>
  </div>

  <!-- Typography -->
  <div class="field">
    <label class="label" for="typography-select">Typography</label>
    <select
      id="typography-select"
      class="input"
      value={cfg.typography}
      onchange={(e: Event) => { cfg.typography = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="serif">Serif</option>
      <option value="sans">Sans-Serif</option>
      <option value="mono">Monospace</option>
      <option value="handwritten">Handwritten</option>
    </select>
  </div>
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
</style>
