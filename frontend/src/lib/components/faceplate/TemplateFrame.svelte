<script lang="ts">
  import { onDestroy } from 'svelte';
  import { sandboxAttrs, templateDocument, type TemplateState } from '../../faceplateSandbox';

  // TemplateFrame draws one control with its author's template, isolated in a
  // sandboxed frame. It reports set values and failures; on failure the panel
  // falls back to the built-in editor.
  let {
    template,
    templateState,
    onSet,
    onError,
  }: {
    template: string;
    templateState: TemplateState;
    onSet: (value: unknown) => void;
    onError: (message: string) => void;
  } = $props();

  let frame: HTMLIFrameElement | undefined = $state();
  let height = $state(120);
  let ready = $state(false);
  let failed = false;
  let srcdoc = $derived(templateDocument(template));

  function post() {
    if (!ready || !frame?.contentWindow) return;
    frame.contentWindow.postMessage({ karakuri: 'state', state: JSON.parse(JSON.stringify(templateState)) }, '*');
  }

  function listen(e: MessageEvent) {
    if (!frame || e.source !== frame.contentWindow) return;
    const m = e.data;
    if (m?.karakuri === 'ready') {
      ready = true;
      post();
    } else if (m?.karakuri === 'set') {
      onSet(m.value);
    } else if (m?.karakuri === 'height' && typeof m.height === 'number') {
      height = Math.min(Math.max(m.height, 40), 800);
    } else if (m?.karakuri === 'error' && !failed) {
      failed = true;
      onError(String(m.message ?? 'the template failed'));
    }
  }

  window.addEventListener('message', listen);
  onDestroy(() => window.removeEventListener('message', listen));

  // A template that never says it is ready within five seconds has failed.
  const readyTimer = setTimeout(() => {
    if (!ready && !failed) {
      failed = true;
      onError('the template did not start');
    }
  }, 5000);
  onDestroy(() => clearTimeout(readyTimer));

  $effect(() => {
    // Re-post whenever the state changes.
    void JSON.stringify(templateState);
    post();
  });
</script>

<iframe
  bind:this={frame}
  title="Custom control"
  sandbox={sandboxAttrs}
  {srcdoc}
  style="width: 100%; height: {height}px; border: 0; background: transparent;"
></iframe>
