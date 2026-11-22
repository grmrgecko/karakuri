<svelte:options customElement="video-config" />

<script lang="ts">
  import { detectEmbed } from "./embed.ts";
  import type { EmbedResult } from "./embed.ts";

  let { config: configAttr = "{}" }: { config: string | Record<string, any> } = $props();

  const DEFAULTS: Record<string, any> = {
    layout: "player",
    source: "",
    dashboardWidgetId: "",
    dashboardId: "",
    sizing: "contain",
    autoplay: false,
    loop: false,
    mute: false,
  };

  let cfg = $state<Record<string, any>>({ ...DEFAULTS });
  let host = $state<HTMLElement | null>(null);
  let uploads = $state<Array<{id: string; file_name: string}>>([]);
  let uploading = $state<boolean>(false);

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      const merged = { ...DEFAULTS, ...parsed };
      // Migrate old aspectRatio/fit fields into unified sizing.
      if (!parsed.sizing && (parsed.aspectRatio || parsed.fit)) {
        merged.sizing = parsed.aspectRatio || parsed.fit || "contain";
        delete merged.aspectRatio;
        delete merged.fit;
      }
      cfg = merged;
    } catch {
      cfg = { ...DEFAULTS };
    }
  });

  $effect(() => {
    if (cfg.dashboardWidgetId) {
      loadUploads();
    }
  });

  let embedInfo: EmbedResult | null = $derived(detectEmbed(cfg.source));

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

  // Tracks source value when the text input gains focus for blur-based cleanup.
  let sourceOnFocus = "";

  // Extract upload ID from an internal widget path like /widget/{widgetId}/{uploadId}/{filename}.
  function extractUploadId(path: string): string | null {
    if (!path?.startsWith("/widget/")) return null;
    const parts = path.split("/");
    return parts[3] || null;
  }

  // Delete the server-side file for an internal widget path.
  async function cleanupInternalSource(path: string): Promise<void> {
    const uploadId = extractUploadId(path);
    if (!uploadId || !cfg.dashboardWidgetId || !(window as any).__karakuri?.api) return;
    try {
      await (window as any).__karakuri.api.delete(`/dashboards/${cfg.dashboardId}/widgets/${cfg.dashboardWidgetId}/uploads/${uploadId}`);
      await loadUploads();
    } catch {
      // Ignore deletion errors.
    }
  }

  async function loadUploads(): Promise<void> {
    if (!cfg.dashboardWidgetId || !(window as any).__karakuri?.api) return;
    try {
      uploads = await (window as any).__karakuri.api.get(`/dashboards/${cfg.dashboardId}/widgets/${cfg.dashboardWidgetId}/uploads`) || [];
    } catch {
      // Uploads may not be available yet.
    }
  }

  async function handleFileUpload(e: Event): Promise<void> {
    const input = e.target as HTMLInputElement | null;
    const file = input?.files?.[0];
    if (!file || !cfg.dashboardWidgetId || !(window as any).__karakuri?.api) return;

    uploading = true;
    const formData = new FormData();
    formData.append("file", file);
    try {
      const data = await (window as any).__karakuri.api.upload(`/dashboards/${cfg.dashboardId}/widgets/${cfg.dashboardWidgetId}/uploads`, formData);
      cfg.source = `/widget/${cfg.dashboardWidgetId}/${data.id}/${data.file_name}`;
      sourceOnFocus = cfg.source;
      dispatch();
      await loadUploads();
    } catch {
      // Ignore upload errors.
    } finally {
      uploading = false;
      if (input) input.value = "";
    }
  }

  async function deleteUpload(uploadId: string): Promise<void> {
    if (!cfg.dashboardWidgetId || !(window as any).__karakuri?.api) return;
    try {
      await (window as any).__karakuri.api.delete(`/dashboards/${cfg.dashboardId}/widgets/${cfg.dashboardWidgetId}/uploads/${uploadId}`);
      // Clear source if this was the selected video.
      if (cfg.source?.includes(uploadId)) {
        cfg.source = "";
        sourceOnFocus = "";
        dispatch();
      }
      await loadUploads();
    } catch {
      // Ignore deletion errors.
    }
  }

  function selectUpload(upload: {id: string; file_name: string}): void {
    cfg.source = `/widget/${cfg.dashboardWidgetId}/${upload.id}/${upload.file_name}`;
    sourceOnFocus = cfg.source;
    dispatch();
  }
</script>

<div class="config-form" use:setHost>
  <!-- Layout -->
  <div class="field">
    <label class="label" for="vid-layout">Layout</label>
    <select
      id="vid-layout"
      class="input"
      value={cfg.layout}
      onchange={(e: Event) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="player">Player</option>
      <option value="ambient">Ambient</option>
    </select>
    <span class="hint">
      {cfg.layout === "ambient"
        ? "No controls, loops silently. Ideal for dynamic wallpapers."
        : "Standard player with controls and progress bar."}
    </span>
  </div>

  <!-- Video URL -->
  <div class="field">
    <label class="label" for="vid-source">Video URL</label>
    <input
      id="vid-source"
      type="text"
      class="input"
      placeholder="https://youtube.com/watch?v=... or direct video URL"
      value={cfg.source}
      onfocus={() => { sourceOnFocus = cfg.source || ""; }}
      oninput={(e: Event) => { cfg.source = (e.target as HTMLInputElement).value; dispatch(); }}
      onchange={() => {
        const oldSrc = sourceOnFocus;
        sourceOnFocus = cfg.source || "";
        if (oldSrc !== cfg.source && extractUploadId(oldSrc)) {
          cleanupInternalSource(oldSrc);
        }
      }}
    />
    {#if embedInfo}
      <span class="hint embed-hint">Detected: {embedInfo.platform} embed</span>
    {:else if cfg.source}
      <span class="hint">Direct video file. Supports MP4, WebM, OGG.</span>
    {/if}
  </div>

  <!-- File Upload -->
  {#if cfg.dashboardWidgetId}
    <div class="field">
      <span class="label">Upload Video</span>
      <label class="upload-btn">
        {uploading ? "Uploading..." : "Choose File"}
        <input
          type="file"
          accept="video/*"
          onchange={handleFileUpload}
          disabled={uploading}
          hidden
        />
      </label>
      {#if uploads.length > 0}
        <div class="upload-list">
          {#each uploads as upload}
            <div class="upload-item" class:selected={cfg.source?.includes(upload.id)}>
              <button class="upload-select" onclick={() => selectUpload(upload)}>
                <span class="upload-name" title={upload.file_name}>{upload.file_name}</span>
              </button>
              <button class="upload-delete" onclick={() => deleteUpload(upload.id)} aria-label="Delete upload">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Sizing -->
  <div class="field">
    <label class="label" for="vid-sizing">Sizing</label>
    <select
      id="vid-sizing"
      class="input"
      value={cfg.sizing}
      onchange={(e: Event) => { cfg.sizing = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="contain">Contain (letterbox)</option>
      <option value="cover">Cover (crop to fill)</option>
      <option value="fill">Fill (stretch)</option>
      <option value="16/9">16:9</option>
      <option value="4/3">4:3</option>
      <option value="1/1">1:1</option>
      <option value="9/16">9:16 (vertical)</option>
    </select>
  </div>

  <!-- Playback options -->
  <div class="field">
    <span class="label">Playback</span>
    <div class="checkbox-group">
      <label class="checkbox-label">
        <input
          type="checkbox"
          checked={cfg.autoplay}
          onchange={(e: Event) => { cfg.autoplay = (e.target as HTMLInputElement).checked; dispatch(); }}
        />
        Autoplay
      </label>
      <label class="checkbox-label">
        <input
          type="checkbox"
          checked={cfg.loop}
          onchange={(e: Event) => { cfg.loop = (e.target as HTMLInputElement).checked; dispatch(); }}
        />
        Loop
      </label>
      <label class="checkbox-label">
        <input
          type="checkbox"
          checked={cfg.mute}
          onchange={(e: Event) => { cfg.mute = (e.target as HTMLInputElement).checked; dispatch(); }}
        />
        Mute
      </label>
    </div>
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

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .embed-hint {
    color: var(--theme-accent, #89b4fa);
    opacity: 1;
  }

  .checkbox-group {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.8rem;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
  }

  .checkbox-label input[type="checkbox"] {
    accent-color: var(--theme-accent, #89b4fa);
    width: 14px;
    height: 14px;
    cursor: pointer;
  }

  .upload-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.4rem 1rem;
    border: 1px dashed var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    cursor: pointer;
    transition: border-color 0.2s, background 0.2s;
    font-family: inherit;
  }

  .upload-btn:hover {
    border-color: var(--theme-accent, #89b4fa);
    background: var(--theme-surface-1, #45475a);
  }

  .upload-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    margin-top: 0.25rem;
  }

  .upload-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.3rem 0.5rem;
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    font-size: 0.75rem;
  }

  .upload-item.selected {
    border-color: var(--theme-accent, #89b4fa);
  }

  .upload-select {
    flex: 1;
    min-width: 0;
    background: none;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    padding: 0;
    text-align: left;
    font-family: inherit;
    font-size: inherit;
  }

  .upload-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
  }

  .upload-delete {
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    background: none;
    color: var(--theme-error, #f38ba8);
    cursor: pointer;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .upload-delete svg {
    width: 14px;
    height: 14px;
  }

  .upload-delete:hover {
    opacity: 0.7;
  }


</style>
