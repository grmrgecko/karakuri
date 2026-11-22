<script lang="ts">
  import type { Snippet } from 'svelte';

  let { open, title, name, busy = false, onclose, children, dialog = $bindable() }: {
    open: boolean; title: string; name: string; busy?: boolean;
    onclose: () => void; children: Snippet; dialog?: HTMLDialogElement;
  } = $props();
  const titleId = $props.id();
  let dialogPosition: { x: number; y: number } | undefined;
  let drag: { pointer: number; x: number; y: number; left: number; top: number } | undefined;
  let dragging = $state(false);
  let resizing: { pointer: number; x: number; y: number; width: number; height: number } | undefined;

  function mountDialog(element: HTMLDialogElement) {
    dialog = element;
    $effect(() => {
      if (open) {
        element.show();
        if (!dialogPosition) {
          const bounds = element.getBoundingClientRect();
          positionDialog((window.innerWidth - bounds.width) / 2, (window.innerHeight - bounds.height) / 2);
        }
        constrainDialog();
      } else {
        element.close();
      }
    });
    const resize = new ResizeObserver(constrainDialog);
    resize.observe(element);
    window.addEventListener('resize', constrainDialog);
    return { destroy: () => {
      resize.disconnect();
      window.removeEventListener('resize', constrainDialog);
      element.close();
    } };
  }
  function positionDialog(x: number, y: number) {
    const bounds = dialog!.getBoundingClientRect();
    dialogPosition = {
      x: Math.max(16, Math.min(x, window.innerWidth - bounds.width - 16)),
      y: Math.max(16, Math.min(y, window.innerHeight - bounds.height - 16)),
    };
    dialog!.style.margin = '0';
    dialog!.style.left = `${dialogPosition.x}px`;
    dialog!.style.top = `${dialogPosition.y}px`;
  }
  function constrainDialog() {
    if (dialog!.open && dialogPosition) positionDialog(dialogPosition.x, dialogPosition.y);
  }
  function startDrag(event: PointerEvent) {
    if (event.button !== 0 || !event.isPrimary) return;
    const bounds = dialog!.getBoundingClientRect();
    drag = { pointer: event.pointerId, x: event.clientX, y: event.clientY, left: bounds.left, top: bounds.top };
    (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
    dragging = true;
  }
  function moveDrag(event: PointerEvent) {
    if (!drag || drag.pointer !== event.pointerId) return;
    positionDialog(drag.left + event.clientX - drag.x, drag.top + event.clientY - drag.y);
  }
  function endDrag(event: PointerEvent) {
    if (!drag || drag.pointer !== event.pointerId) return;
    drag = undefined;
    dragging = false;
    const handle = event.currentTarget as HTMLElement;
    if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
  }
  function moveWithKeyboard(event: KeyboardEvent) {
    const step = event.shiftKey ? 50 : 10;
    const offsets: Record<string, [number, number]> = {
      ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step],
    };
    const offset = offsets[event.key];
    if (!offset) return;
    event.preventDefault();
    const bounds = dialog!.getBoundingClientRect();
    positionDialog(bounds.left + offset[0], bounds.top + offset[1]);
  }
  function resizeDialog(width: number, height: number) {
    const bounds = dialog!.getBoundingClientRect();
    dialog!.style.width = `${Math.min(Math.max(360, width), window.innerWidth - bounds.left - 16)}px`;
    dialog!.style.height = `${Math.min(Math.max(420, height), window.innerHeight - bounds.top - 16)}px`;
  }
  function startResize(event: PointerEvent) {
    if (event.button !== 0 || !event.isPrimary) return;
    event.preventDefault();
    (event.currentTarget as HTMLElement).focus({ preventScroll: true });
    const bounds = dialog!.getBoundingClientRect();
    resizing = { pointer: event.pointerId, x: event.clientX, y: event.clientY, width: bounds.width, height: bounds.height };
    (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
  }
  function moveResize(event: PointerEvent) {
    if (!resizing || resizing.pointer !== event.pointerId) return;
    resizeDialog(resizing.width + event.clientX - resizing.x, resizing.height + event.clientY - resizing.y);
  }
  function endResize(event: PointerEvent) {
    if (!resizing || resizing.pointer !== event.pointerId) return;
    resizing = undefined;
    const handle = event.currentTarget as HTMLElement;
    if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
  }
  function resizeWithKeyboard(event: KeyboardEvent) {
    const step = event.shiftKey ? 50 : 10;
    const offsets: Record<string, [number, number]> = {
      ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step],
    };
    const offset = offsets[event.key];
    if (!offset) return;
    event.preventDefault();
    const bounds = dialog!.getBoundingClientRect();
    resizeDialog(bounds.width + offset[0], bounds.height + offset[1]);
  }
</script>

<dialog use:mountDialog class="assistant-dialog" aria-labelledby={titleId}
  onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); if (!busy) onclose(); } }}
  oncancel={(e) => { e.preventDefault(); if (!busy) onclose(); }}>
  <div class="assistant-header">
    <button type="button" class="assistant-drag-handle min-w-0" class:dragging
      aria-label="Move AI dialog" title="Drag to move, or focus and use arrow keys"
      onpointerdown={startDrag} onpointermove={moveDrag} onpointerup={endDrag}
      onpointercancel={endDrag} onlostpointercapture={endDrag} onkeydown={moveWithKeyboard}>
      <span id={titleId} class="block font-semibold text-lg">{title}</span>
      <span class="block text-sm opacity-70 truncate">{name}</span>
    </button>
    <button class="btn btn-secondary btn-sm" onclick={onclose} disabled={busy} aria-label="Close AI chat"><i class="las la-times"></i></button>
  </div>
  {@render children()}
  <button type="button" class="assistant-resize-handle" aria-label="Resize AI dialog"
    title="Drag to resize, or focus and use arrow keys"
    onpointerdown={startResize} onpointermove={moveResize} onpointerup={endResize}
    onpointercancel={endResize} onlostpointercapture={endResize} onkeydown={resizeWithKeyboard}>
    <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-linecap="round" aria-hidden="true">
      <path d="M5 13 13 5M9 13l4-4M13 13h0" />
    </svg>
  </button>
</dialog>

<style>
  .assistant-dialog {
    position: fixed;
    z-index: 40;
    width: min(620px, calc(100vw - 2rem));
    height: min(760px, calc(100dvh - 2rem));
    min-width: min(360px, calc(100vw - 2rem));
    min-height: min(420px, calc(100dvh - 2rem));
    max-width: calc(100vw - 2rem);
    max-height: calc(100dvh - 2rem);
    overflow: auto;
    margin: auto;
    padding: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
    color: var(--theme-text);
    background: color-mix(in srgb, var(--theme-mantle) 84%, transparent);
    border: 1px solid var(--theme-surface2);
    border-radius: var(--theme-radius-lg, 12px);
    box-shadow: 0 20px 70px #0005;
  }
  .assistant-dialog > :global(*) { flex-shrink: 0; }
  .assistant-dialog:not([open]) { display: none; }
  .assistant-drag-handle { flex: 1; text-align: left; cursor: grab; touch-action: none; user-select: none; }
  .assistant-drag-handle.dragging { cursor: grabbing; }
  .assistant-resize-handle { position: absolute; right: 3px; bottom: 3px; width: 20px; height: 20px; cursor: nwse-resize; touch-action: none; opacity: .6; }
  .assistant-resize-handle svg { width: 100%; height: 100%; }
  .assistant-header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
</style>
