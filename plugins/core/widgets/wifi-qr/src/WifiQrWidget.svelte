<svelte:options customElement="wifi-qr-widget" />

<script module lang="ts">
  import qrcode from "qrcode-generator";

  // The library's byte mode keeps only the low byte of each UTF-16 unit, which
  // mangles any SSID outside Latin-1. Phones read Wi-Fi codes as UTF-8. This
  // bundle holds its own copy of the library, so the override stays here.
  qrcode.stringToBytes = (s: string) => Array.from(new TextEncoder().encode(s));
</script>

<script lang="ts">
  import { parseConfig, wifiPayload } from "./config";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  // QUIET is the margin in modules. The QR specification asks for four, and
  // phone cameras find the code fastest with all of it.
  const QUIET = 4;

  // code is the symbol as one SVG path, a horizontal run of dark modules per
  // subpath, so a large code is a few hundred path commands rather than
  // thousands of rects.
  let code = $derived.by(() => {
    if (!cfg.ssid) return null;
    const qr = qrcode(0, "M");
    qr.addData(wifiPayload(cfg), "Byte");
    qr.make();
    const n = qr.getModuleCount();
    let d = "";
    for (let row = 0; row < n; row++) {
      let col = 0;
      while (col < n) {
        if (!qr.isDark(row, col)) {
          col++;
          continue;
        }
        const start = col;
        while (col < n && qr.isDark(row, col)) col++;
        d += `M${start + QUIET} ${row + QUIET}h${col - start}v1h${start - col}z`;
      }
    }
    return { size: n + QUIET * 2, path: d };
  });

  let label = $derived(cfg.label || (cfg.ssid ? `Join ${cfg.ssid}` : ""));
</script>

<div class="root colors-{cfg.colors}">
  {#if label}
    <div class="label">{label}</div>
  {/if}
  {#if code}
    <div class="code">
      <svg viewBox="0 0 {code.size} {code.size}" shape-rendering="crispEdges" role="img"
           aria-label="QR code to join the {cfg.ssid} network">
        <rect class="light" width={code.size} height={code.size} />
        <path class="dark" d={code.path} />
      </svg>
    </div>
    {#if cfg.showPassword && cfg.security !== "nopass" && cfg.password}
      <div class="password">{cfg.password}</div>
    {/if}
  {:else}
    <div class="empty">Set a network name</div>
  {/if}
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    background: var(--theme-widget-bg, transparent);
    border-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
    box-sizing: border-box;
  }

  .root {
    display: flex;
    flex-direction: column;
    align-items: center;
    width: 100%;
    height: 100%;
    padding: 0.5rem;
    gap: 0.4rem;
    box-sizing: border-box;
    text-align: center;
  }

  .label {
    flex: none;
    font-size: 1.1rem;
    font-weight: 600;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .code {
    flex: 1;
    min-height: 0;
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .code svg {
    width: 100%;
    height: 100%;
    max-width: 100%;
    max-height: 100%;
  }

  .colors-plain .light { fill: #ffffff; }
  .colors-plain .dark { fill: #000000; }
  .colors-theme .light { fill: var(--theme-base, #1e1e2e); }
  .colors-theme .dark { fill: var(--theme-text, #cdd6f4); }

  .password {
    flex: none;
    font-family: var(--theme-font-mono, monospace);
    font-size: 1.1rem;
    max-width: 100%;
    overflow-wrap: anywhere;
  }

  .empty {
    margin: auto;
    color: var(--theme-text-muted, #a6adc8);
  }
</style>
