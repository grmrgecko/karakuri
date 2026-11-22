<script lang="ts">
  import {
    imageBytes,
    imageName,
    imageSet,
    imageTypes,
    maxImages,
    readImage,
  } from '../../utils/noteImages';

  let {
    id,
    value,
    onChange,
    onAttach,
    onDetach,
  }: {
    id?: string;
    value: unknown;
    onChange: (images: Record<string, string>) => void;
    // onAttach and onDetach let the field that displays the images follow
    // along: attaching one writes a reference into the markdown so the picture
    // appears without a second step, and detaching takes the reference back
    // out rather than leaving a broken image behind.
    onAttach?: (name: string) => void;
    onDetach?: (name: string) => void;
  } = $props();

  let images = $derived(imageSet(value));
  let names = $derived(Object.keys(images).sort());
  let error = $state('');
  let dragging = $state(false);
  let picker = $state<HTMLInputElement | null>(null);

  // add stores the files an author dropped, pasted, or picked. Failures are
  // reported one at a time: a drop of five files where one is a PDF still
  // attaches the other four.
  async function add(files: FileList | File[] | null | undefined) {
    if (!files?.length) return;
    error = '';
    const next = { ...images };
    const attached: string[] = [];

    for (const file of Array.from(files)) {
      if (Object.keys(next).length >= maxImages) {
        error = `A note holds at most ${maxImages} images.`;
        break;
      }
      try {
        const data = await readImage(file);
        const name = imageName(file.name || 'pasted', file.type, Object.keys(next));
        next[name] = data;
        attached.push(name);
      } catch (e: any) {
        error = e?.message || 'The image could not be attached.';
      }
    }

    if (!attached.length) return;
    onChange(next);
    for (const name of attached) onAttach?.(name);
  }

  function remove(name: string) {
    const next = { ...images };
    delete next[name];
    error = '';
    onChange(next);
    onDetach?.(name);
  }

  function onDrop(event: DragEvent) {
    event.preventDefault();
    dragging = false;
    void add(event.dataTransfer?.files);
  }

  // Pasting is how a screenshot usually arrives, so the drop zone takes a
  // paste while it has focus.
  function onPaste(event: ClipboardEvent) {
    const files = Array.from(event.clipboardData?.items ?? [])
      .filter((item) => item.kind === 'file' && imageTypes.includes(item.type))
      .map((item) => item.getAsFile())
      .filter((file): file is File => !!file);
    if (!files.length) return;
    event.preventDefault();
    void add(files);
  }

  function sizeLabel(data: string): string {
    const bytes = imageBytes(data);
    return bytes >= 1024 * 1024
      ? `${(bytes / (1024 * 1024)).toFixed(1)} MB`
      : `${Math.max(1, Math.round(bytes / 1024))} KB`;
  }
</script>

<div class="image-field">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    {id}
    class="image-drop"
    class:dragging
    role="button"
    tabindex="0"
    onclick={() => picker?.click()}
    onkeydown={(e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        picker?.click();
      }
    }}
    ondragover={(e) => {
      e.preventDefault();
      dragging = true;
    }}
    ondragleave={() => (dragging = false)}
    ondrop={onDrop}
    onpaste={onPaste}
  >
    <i class="las la-image"></i>
    <span>Drop, paste, or click to attach an image</span>
    <span class="image-limit">PNG, JPEG, GIF, or WebP · up to 2 MB each</span>
  </div>

  <input
    bind:this={picker}
    class="hidden"
    type="file"
    accept={imageTypes.join(',')}
    multiple
    onchange={(e) => {
      void add(e.currentTarget.files);
      e.currentTarget.value = '';
    }}
  />

  {#if error}
    <p class="image-error">{error}</p>
  {/if}

  {#each names as name (name)}
    <div class="image-row">
      <img class="image-thumb" src={images[name]} alt={name} />
      <div class="image-meta">
        <span class="image-name" title={name}>{name}</span>
        <span class="image-size">{sizeLabel(images[name])}</span>
      </div>
      <button
        type="button"
        class="image-tool"
        title="Insert a reference to this image in the markdown"
        onclick={() => onAttach?.(name)}
      >
        <i class="las la-plus"></i>
      </button>
      <button
        type="button"
        class="image-tool danger"
        title="Remove this image from the note"
        onclick={() => remove(name)}
      >
        <i class="las la-trash"></i>
      </button>
    </div>
  {/each}
</div>

<style>
  .image-field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .image-drop {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    padding: 10px 8px;
    border: 1px dashed var(--theme-surface1, #45475a);
    border-radius: 6px;
    background-color: var(--theme-crust, #11111b);
    font-size: 11px;
    text-align: center;
    opacity: 0.75;
    cursor: pointer;
  }

  .image-drop:hover,
  .image-drop:focus-visible,
  .image-drop.dragging {
    opacity: 1;
    border-color: var(--theme-accent, #89b4fa);
    outline: none;
  }

  .image-limit {
    font-size: 10px;
    opacity: 0.6;
  }

  .image-error {
    font-size: 11px;
    color: var(--theme-error, #f38ba8);
  }

  .image-row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px;
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: 6px;
  }

  .image-thumb {
    width: 34px;
    height: 34px;
    object-fit: cover;
    border-radius: 4px;
    background-color: var(--theme-mantle, #181825);
    flex-shrink: 0;
  }

  .image-meta {
    display: flex;
    flex-direction: column;
    min-width: 0;
    flex: 1;
  }

  .image-name {
    font-size: 11px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .image-size {
    font-size: 10px;
    opacity: 0.5;
  }

  .image-tool {
    padding: 2px 6px;
    border-radius: 4px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: transparent;
    color: inherit;
    font-size: 11px;
    cursor: pointer;
    opacity: 0.7;
  }

  .image-tool:hover {
    opacity: 1;
    border-color: var(--theme-accent, #89b4fa);
  }

  .image-tool.danger:hover {
    border-color: var(--theme-error, #f38ba8);
    color: var(--theme-error, #f38ba8);
  }
</style>
