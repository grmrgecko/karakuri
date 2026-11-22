<script lang="ts">
  import { onMount } from 'svelte';
  import { resolvePromptAssets, type PromptAssetURL } from '../lib/utils/promptAttachments';

  interface Prompt {
    title: string; html: string; buttons: { id: string; label: string }[];
    feedback: boolean; require_feedback: boolean; expires_at: string; server_time: string;
    assets: { name: string; filename: string; type: string }[];
  }

  const token = window.location.hash.slice(1);
  let prompt = $state<Prompt | null>(null);
  let assets = $state<Record<string, PromptAssetURL>>({});
  let text = $state('');
  let error = $state('');
  let busy = $state(false);
  let loading = $state(true);
  let answered = $state(false);
  let expired = $state(false);
  let remaining = $state(0);
  let body = $derived(resolvePromptAssets(prompt?.html || '', assets));

  async function request(path: string, init: RequestInit = {}) {
    const response = await fetch(`/api/browser_prompts/${path}`, {
      ...init, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      credentials: 'omit', cache: 'no-store', referrerPolicy: 'no-referrer',
    });
    if (!response.ok) {
      if (response.status === 410) expired = true;
      const detail = await response.json().catch(() => ({}));
      throw new Error(detail.message || 'The server could not process this question.');
    }
    return response;
  }

  onMount(() => {
    let disposed = false;
    const urls: string[] = [];
    let timer: ReturnType<typeof setInterval> | undefined;
    void (async () => {
      try {
        if (!token) throw new Error('This link has no question token.');
        const data: Prompt = await (await request('current')).json();
        if (disposed) return;
        prompt = data;
        document.title = data.title;
        const deadline = performance.now() + new Date(data.expires_at).getTime() - new Date(data.server_time).getTime();
        // The deadline runs while attachments download.
        const update = () => {
          remaining = Math.max(0, Math.ceil((deadline - performance.now()) / 1000));
          if (remaining === 0 && !answered) expired = true;
        };
        update(); timer = setInterval(update, 1000);
        for (const asset of data.assets) {
          const response = await request(`assets/${encodeURIComponent(asset.name)}`);
          const blob = await response.blob();
          if (disposed) return;
          const url = URL.createObjectURL(blob); urls.push(url);
          assets = { ...assets, [asset.name]: { url, filename: asset.filename, type: asset.type } };
        }
      } catch (e: any) { if (!disposed) error = e.message || 'Could not load the question.'; }
      finally { if (!disposed) loading = false; }
    })();
    return () => { disposed = true; if (timer) clearInterval(timer); for (const url of urls) URL.revokeObjectURL(url); };
  });

  async function answer(button: string) {
    if (busy || answered || expired) return;
    busy = true; error = '';
    try { await request('response', { method: 'POST', body: JSON.stringify({ button, text }) }); answered = true; }
    catch (e: any) { error = e.message || 'Could not send the response. Try again.'; }
    finally { busy = false; }
  }
</script>

<svelte:head><meta name="referrer" content="no-referrer" /></svelte:head>

<main class="h-screen shrink-0 overflow-y-auto p-6 flex justify-center items-start bg-base">
  <section class="w-full max-w-3xl shrink-0 bg-surface0 rounded-xl p-6 border border-surface1 my-8" aria-live="polite">
    {#if loading}<p>Loading question...</p>
    {:else if answered}<h1 class="text-2xl font-semibold mb-4">Response sent</h1><p>You can close this tab.</p>
    {:else if expired}<h1 class="text-2xl font-semibold mb-4">Question unavailable</h1><p>This question was answered, canceled, or timed out. You can close this tab.</p>
    {:else if prompt}
      <h1 class="text-2xl font-semibold mb-6">{prompt.title}</h1>
      <div class="prompt-markdown mb-6">{@html body}</div>
      {#if prompt.feedback}
        <label for="prompt-feedback" class="block font-medium mb-2">Feedback{prompt.require_feedback ? ' (required)' : ''}</label>
        <textarea id="prompt-feedback" bind:value={text} class="input w-full min-h-28 mb-4" maxlength="4096" placeholder="Describe what you found or changed."></textarea>
      {/if}
      <div class="flex flex-wrap gap-3">
        {#each prompt.buttons as button (button.id)}
          <button type="button" class="btn btn-primary" disabled={busy || (prompt.require_feedback && !text.trim())} onclick={() => answer(button.id)}>{button.label}</button>
        {/each}
      </div>
      <p class="text-xs text-text-muted mt-4">Answer within {Math.floor(remaining / 60)}m {remaining % 60}s. Closing this tab leaves the question unanswered.</p>
    {/if}
    {#if error}<p role="alert" class="text-error mt-4">{error}</p>{/if}
  </section>
</main>
