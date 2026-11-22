<svelte:options customElement="wifi-qr-config" />

<script lang="ts">
  import { parseConfig, type WifiQrConfig } from "./config";
  import { placementIds } from "../../shared/dashboardLive";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $state<WifiQrConfig>(parseConfig("{}"));
  let host = $state<Element | null>(null);

  // Every edit comes back as a new attribute one keystroke behind the input,
  // so the attribute seeds the panel once per placement and the panel owns
  // its state after that.
  let seededFor: string | null = null;
  $effect(() => {
    const { widgetId } = placementIds(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
  });

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  function update(patch: Partial<WifiQrConfig>): void {
    cfg = { ...cfg, ...patch };
    host?.dispatchEvent(new CustomEvent("configchange", { detail: { ...cfg }, bubbles: true, composed: true }));
  }
</script>

<div class="config-form" use:setHost>
  <div class="warning" role="note">
    Anyone who can view this dashboard can read the password from this widget's settings, just as
    they can by scanning the code. Use a guest network.
  </div>

  <div class="field">
    <label class="label" for="wq-ssid">Network name</label>
    <input id="wq-ssid" class="input" value={cfg.ssid} autocomplete="off"
           oninput={(e: Event) => update({ ssid: (e.target as HTMLInputElement).value })} />
  </div>

  <div class="field">
    <label class="label" for="wq-security">Security</label>
    <select id="wq-security" class="input" value={cfg.security}
            onchange={(e: Event) => update({ security: (e.target as HTMLSelectElement).value as WifiQrConfig["security"] })}>
      <option value="WPA">WPA, WPA2, or WPA3</option>
      <option value="WEP">WEP</option>
      <option value="nopass">None</option>
    </select>
  </div>

  {#if cfg.security !== "nopass"}
    <div class="field">
      <label class="label" for="wq-password">Password</label>
      <input id="wq-password" class="input" value={cfg.password} autocomplete="off" spellcheck="false"
             oninput={(e: Event) => update({ password: (e.target as HTMLInputElement).value })} />
    </div>
  {/if}

  <div class="field">
    <label class="label" for="wq-label">Label</label>
    <input id="wq-label" class="input" value={cfg.label} placeholder={cfg.ssid ? `Join ${cfg.ssid}` : "Join network"}
           oninput={(e: Event) => update({ label: (e.target as HTMLInputElement).value })} />
  </div>

  <div class="field">
    <label class="label" for="wq-colors">Colors</label>
    <select id="wq-colors" class="input" value={cfg.colors}
            onchange={(e: Event) => update({ colors: (e.target as HTMLSelectElement).value as WifiQrConfig["colors"] })}>
      <option value="plain">Black on white</option>
      <option value="theme">Theme</option>
    </select>
    <span class="hint">Black on white scans most reliably. Some phones cannot read a light code on a dark theme.</span>
  </div>

  <div class="field">
    <label class="checkbox-label">
      <input type="checkbox" checked={cfg.hidden}
             onchange={(e: Event) => update({ hidden: (e.target as HTMLInputElement).checked })} />
      <span>Hidden network</span>
    </label>
    {#if cfg.security !== "nopass"}
      <label class="checkbox-label">
        <input type="checkbox" checked={cfg.showPassword}
               onchange={(e: Event) => update({ showPassword: (e.target as HTMLInputElement).checked })} />
        <span>Print the password under the code</span>
      </label>
    {/if}
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
    color: var(--theme-text-muted, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-text-muted, #a6adc8);
    opacity: 0.8;
  }

  .warning {
    font-size: 0.8rem;
    line-height: 1.4;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--theme-warning, #f9e2af);
    border-radius: var(--theme-radius-sm, 4px);
    color: var(--theme-text, #cdd6f4);
    background: color-mix(in srgb, var(--theme-warning, #f9e2af) 12%, transparent);
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-input-border, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-input-bg, var(--theme-surface0, #313244));
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

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .checkbox-label input[type="checkbox"] {
    width: 16px;
    height: 16px;
    accent-color: var(--theme-accent, #89b4fa);
  }
</style>
