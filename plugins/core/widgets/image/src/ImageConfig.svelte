<svelte:options customElement="image-config" />

<script lang="ts">
  let { config: configAttr = "{}" } = $props();

  const DEFAULTS: Record<string, any> = {
    layout: "static",
    images: [],
    dashboardWidgetId: "",
    dashboardId: "",
    fit: "cover",
    refreshInterval: 0,
    slideInterval: 5,
    transition: "fade",
  };

  let cfg = $state<Record<string, any>>({ ...DEFAULTS });
  let host = $state<HTMLElement | null>(null);
  let uploading = $state<boolean>(false);
  let urlInput = $state<string>("");

  $effect(() => {
    try {
      const parsed = typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr;
      const merged = { ...DEFAULTS, ...parsed };
      // Ensure images is always an array.
      if (!Array.isArray(merged.images)) merged.images = [];
      cfg = merged;
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

  // Add a URL image to the list.
  function addUrlImage(): void {
    const url = urlInput.trim();
    if (!url) return;
    cfg.images = [...cfg.images, { type: "url", url, label: url }];
    urlInput = "";
    dispatch();
  }

  function addUrlOnEnter(e: KeyboardEvent): void {
    if (e.key === "Enter") {
      e.preventDefault();
      addUrlImage();
    }
  }

  // Upload image files and add them to the list.
  async function handleFileUpload(e: Event): Promise<void> {
    const input = e.target as HTMLInputElement | null;
    const files = input?.files;
    if (!files?.length || !cfg.dashboardWidgetId || !(window as any).__karakuri?.api) return;

    uploading = true;
    for (const file of files) {
      const formData = new FormData();
      formData.append("file", file);
      try {
        const data = await (window as any).__karakuri.api.upload(`/dashboards/${cfg.dashboardId}/widgets/${cfg.dashboardWidgetId}/uploads`, formData);
        const uploadUrl = `/widget/${cfg.dashboardWidgetId}/${data.id}/${data.file_name}`;
        cfg.images = [...cfg.images, {
          type: "upload",
          url: uploadUrl,
          label: data.file_name || file.name,
          uploadId: data.id,
        }];
      } catch {
        // Skip failed uploads.
      }
    }
    uploading = false;
    dispatch();
    if (input) input.value = "";
  }

  // Remove an image from the list and delete the upload if applicable.
  async function removeImage(index: number): Promise<void> {
    const img = cfg.images[index];
    if (img?.type === "upload" && img.uploadId && cfg.dashboardWidgetId && (window as any).__karakuri?.api) {
      try {
        await (window as any).__karakuri.api.delete(`/dashboards/${cfg.dashboardId}/widgets/${cfg.dashboardWidgetId}/uploads/${img.uploadId}`);
      } catch {
        // Ignore deletion errors.
      }
    }
    cfg.images = cfg.images.filter((_: any, i: number) => i !== index);
    dispatch();
  }

  // Move an image up or down in the list.
  function moveImage(index: number, direction: number): void {
    const target = index + direction;
    if (target < 0 || target >= cfg.images.length) return;
    const list = [...cfg.images];
    [list[index], list[target]] = [list[target], list[index]];
    cfg.images = list;
    dispatch();
  }

  // In static mode only the first image is used, in slideshow all are used.
  let isSlideshow = $derived(cfg.layout === "slideshow");
</script>

<div class="config-form" use:setHost>
  <!-- Layout -->
  <div class="field">
    <label class="label" for="img-layout">Layout</label>
    <select
      id="img-layout"
      class="input"
      value={cfg.layout}
      onchange={(e: Event) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="static">Static</option>
      <option value="slideshow">Slideshow</option>
    </select>
  </div>

  <!-- Image List -->
  <div class="field">
    <span class="label">{isSlideshow ? "Images" : "Image"}</span>
    {#if !isSlideshow && cfg.images.length > 0}
      <span class="hint">Only the first image is shown in static mode.</span>
    {/if}

    <!-- Existing images -->
    {#if cfg.images.length > 0}
      <div class="image-list">
        {#each cfg.images as img, i}
          <div class="image-item">
            <span class="image-label" title={img.label || img.url}>
              {#if img.type === "upload"}
                <svg class="item-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <polyline points="17 8 12 3 7 8" />
                  <line x1="12" y1="3" x2="12" y2="15" />
                </svg>
              {:else}
                <svg class="item-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
                  <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
                </svg>
              {/if}
              {img.label || img.url}
            </span>
            <div class="image-actions">
              {#if isSlideshow && cfg.images.length > 1}
                <button class="action-btn" onclick={() => moveImage(i, -1)} aria-label="Move up" disabled={i === 0}>
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="18 15 12 9 6 15" />
                  </svg>
                </button>
                <button class="action-btn" onclick={() => moveImage(i, 1)} aria-label="Move down" disabled={i === cfg.images.length - 1}>
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="6 9 12 15 18 9" />
                  </svg>
                </button>
              {/if}
              <button class="action-btn delete-btn" onclick={() => removeImage(i)} aria-label="Remove image">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            </div>
          </div>
        {/each}
      </div>
    {/if}

    <!-- Add by URL -->
    <div class="add-row">
      <input
        type="text"
        class="input add-input"
        placeholder="Paste image URL..."
        bind:value={urlInput}
        onkeydown={addUrlOnEnter}
      />
      <button class="add-btn" onclick={addUrlImage} disabled={!urlInput.trim()}>Add URL</button>
    </div>

    <!-- Add by upload -->
    {#if cfg.dashboardWidgetId}
      <label class="upload-btn">
        {uploading ? "Uploading..." : "Upload Image"}
        <input
          type="file"
          accept="image/*"
          multiple={isSlideshow}
          onchange={handleFileUpload}
          disabled={uploading}
          hidden
        />
      </label>
    {/if}
  </div>

  <!-- Fit Mode -->
  <div class="field">
    <label class="label" for="img-fit">Fit Mode</label>
    <select
      id="img-fit"
      class="input"
      value={cfg.fit}
      onchange={(e: Event) => { cfg.fit = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="cover">Cover</option>
      <option value="contain">Contain</option>
      <option value="fill">Fill</option>
    </select>
  </div>

  <!-- Refresh Interval -->
  <div class="field">
    <label class="label" for="img-refresh">Refresh Interval (seconds)</label>
    <input
      id="img-refresh"
      type="number"
      class="input"
      min="0"
      step="1"
      placeholder="0 = no refresh"
      value={cfg.refreshInterval}
      oninput={(e: Event) => { cfg.refreshInterval = parseInt((e.target as HTMLInputElement).value) || 0; dispatch(); }}
    />
    <span class="hint">Periodically reload URL images (useful for webcams). 0 to disable.</span>
  </div>

  <!-- Slideshow options -->
  {#if isSlideshow}
    <div class="field">
      <label class="label" for="img-slide-interval">Slide Duration (seconds)</label>
      <input
        id="img-slide-interval"
        type="number"
        class="input"
        min="1"
        step="1"
        placeholder="5"
        value={cfg.slideInterval || 5}
        oninput={(e: Event) => { cfg.slideInterval = parseInt((e.target as HTMLInputElement).value) || 5; dispatch(); }}
      />
    </div>

    <div class="field">
      <label class="label" for="img-transition">Transition Effect</label>
      <select
        id="img-transition"
        class="input"
        value={cfg.transition}
        onchange={(e: Event) => { cfg.transition = (e.target as HTMLSelectElement).value; dispatch(); }}
      >
        <option value="fade">Fade</option>
        <option value="slide">Slide</option>
        <option value="slide-up">Slide Up</option>
        <option value="zoom">Zoom</option>
        <option value="blur">Blur</option>
        <option value="flip">Flip</option>
        <option value="crossfade">Crossfade</option>
        <option value="kenburns">Ken Burns</option>
        <option value="none">None (instant)</option>
      </select>
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

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .image-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    margin-top: 0.25rem;
  }

  .image-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.3rem 0.5rem;
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    font-size: 0.75rem;
    gap: 0.25rem;
  }

  .image-label {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
    min-width: 0;
    color: var(--theme-text, #cdd6f4);
  }

  .item-icon {
    width: 12px;
    height: 12px;
    flex-shrink: 0;
    color: var(--theme-subtext, #a6adc8);
  }

  .image-actions {
    display: flex;
    align-items: center;
    gap: 0.15rem;
    flex-shrink: 0;
  }

  .action-btn {
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    background: none;
    color: var(--theme-subtext, #a6adc8);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .action-btn svg {
    width: 14px;
    height: 14px;
  }

  .action-btn:hover:not(:disabled) {
    color: var(--theme-text, #cdd6f4);
  }

  .action-btn:disabled {
    opacity: 0.3;
    cursor: default;
  }

  .delete-btn {
    color: var(--theme-error, #f38ba8);
  }

  .delete-btn:hover {
    opacity: 0.7;
  }

  .add-row {
    display: flex;
    gap: 0.35rem;
    margin-top: 0.25rem;
  }

  .add-input {
    flex: 1;
    min-width: 0;
  }

  .add-btn {
    padding: 0.4rem 0.75rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    cursor: pointer;
    font-family: inherit;
    white-space: nowrap;
    flex-shrink: 0;
  }

  .add-btn:hover:not(:disabled) {
    border-color: var(--theme-accent, #89b4fa);
  }

  .add-btn:disabled {
    opacity: 0.4;
    cursor: default;
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
</style>
