<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import FloatingAIWindow from './FloatingAIWindow.svelte';
  import AssistantMarkdown from './AssistantMarkdown.svelte';
  import AIReasoning from './AIReasoning.svelte';
  import { api } from '../api/client';
  import { auth, hasPermission, preferences } from '../stores/auth';

  let { kind, id, name, disabled = false, prepare, onchange } : {
    kind: 'flow' | 'dashboard'; id: string; name: string; disabled?: boolean;
    prepare?: () => Promise<boolean>;
    onchange: () => Promise<void>;
  } = $props();

  type Provider = { connection_id: string; name: string };
  type Entry = { role: 'user' | 'assistant' | 'tool' | 'notice'; text: string; reasoning?: string; id?: string; args?: unknown; result?: unknown; failed?: boolean; images?: { MIME: string; Data: string }[] };
  let providers = $state<Provider[]>([]);
  let connection = $state('');
  let models = $state<string[]>([]);
  let model = $state('');
  let modelLoading = $state(false);
  let open = $state(false);
  let busy = $state(false);
  let allowExecution = $state(false);
  let prompt = $state('');
  let entries = $state<Entry[]>([]);
  let messages: unknown[] = [];
  let error = $state('');
  let controller: AbortController | undefined;
  let transcript = $state<HTMLDivElement>();
  let input = $state<HTMLTextAreaElement>();
  let dialog = $state<HTMLDialogElement>();
  let transcriptScroll = 0;

  async function loadProviders() {
    if (!hasPermission('api:connections:read') || (kind === 'flow' && !hasPermission('api:flows:write'))) return;
    try {
      providers = await api.get<Provider[]>('/assistant/connections');
      if (!providers.some(p => p.connection_id === connection)) {
        const saved = $preferences.assistant_connection;
        connection = providers.find(p => p.connection_id === saved)?.connection_id ?? providers[0]?.connection_id ?? '';
        model = connection === saved && typeof $preferences.assistant_model === 'string' ? $preferences.assistant_model : '';
      }
    } catch {
      providers = [];
    }
  }
  onMount(() => {
    void loadProviders();
    const timer = setInterval(() => { if (!open) void loadProviders(); }, 30000);
    return () => clearInterval(timer);
  });
  onDestroy(() => controller?.abort());

  function rememberSelection() {
    if (!connection) return;
    void auth.setPreferences({ assistant_connection: connection, assistant_model: model });
  }

  async function loadModels() {
    const selected = connection;
    modelLoading = true;
    error = '';
    try {
      const result = await api.get<string[]>(`/connections/${selected}/ai/models`);
      if (connection !== selected) return;
      models = result;
      if (!model) model = models[0] ?? '';
      rememberSelection();
    } catch (e: any) { error = e.message || 'could not load models'; }
    finally { modelLoading = false; }
  }

  async function show() {
    if (open) { input?.focus({ preventScroll: true }); return; }
    error = '';
    if (prepare && !await prepare()) return;
    open = true;
    await tick();
    input?.focus({ preventScroll: true });
    if (transcript) transcript.scrollTop = transcriptScroll;
    await loadModels();
  }
  function close() {
    if (busy) return;
    transcriptScroll = transcript?.scrollTop ?? 0;
    open = false;
  }
  function resetConversation() {
    allowExecution = false;
    messages = [];
    entries = [];
    transcriptScroll = 0;
    if (transcript) transcript.scrollTop = 0;
    error = '';
  }
  async function follow() {
    await tick();
    transcript?.scrollTo({ top: transcript.scrollHeight });
  }
  async function send() {
    const text = prompt.trim();
    if (!text || !model || busy) return;
    if (prepare && !await prepare()) return;
    busy = true;
    error = '';
    prompt = '';
    entries.push({ role: 'user', text });
    controller = new AbortController();
    let completed = false;
    let reply = -1;
    try {
      await api.streamJSON('/assistant/chat', {
        kind, id, connection_id: connection, model, prompt: text, messages, allow_execution: allowExecution,
      }, controller.signal, async (event) => {
        if (event.type === 'reply.start') {
          reply = entries.length;
          entries.push({ role: 'assistant', text: '' });
        } else if (event.type === 'reply.delta' && reply >= 0) {
          entries[reply].text += event.delta;
        } else if (event.type === 'reply.reasoning' && reply >= 0) {
          entries[reply].reasoning = event.text;
        } else if (event.type === 'context.compacted') {
          entries.push({ role: 'notice', text: 'Context compacted' });
        } else if (event.type === 'tool.start') {
          entries.push({ role: 'tool', id: event.id, text: event.name, args: event.arguments });
        } else if (event.type === 'tool.result') {
          const entry = [...entries].reverse().find(e => e.role === 'tool' && e.id === event.id);
          if (entry) { entry.result = event.result; entry.failed = event.failed; entry.images = event.images; }
          if (event.changed) await onchange();
        } else if (event.type === 'done') {
          messages = event.messages;
          if (event.notice) entries.push({ role: 'assistant', text: event.notice });
          completed = true;
        } else if (event.type === 'error') {
          throw new Error(event.message);
        }
        await follow();
      });
      if (!completed) throw new Error('the response ended before the agent finished; send another message to retry');
    } catch (e: any) {
      error = e.name === 'AbortError' ? 'Stopped. Completed draft changes are kept.' : e.message || 'the AI request failed';
      // An interrupted tool round cannot be replayed as a complete conversation.
      messages = [];
    } finally {
      try { await onchange(); } catch (e: any) { error = e.message || 'could not refresh the draft; reload the editor'; }
      busy = false;
      controller = undefined;
      await tick();
      if (open && dialog?.contains(document.activeElement)) input?.focus({ preventScroll: true });
    }
  }
</script>

{#if providers.length > 0}
  <button class="btn btn-secondary btn-sm" {disabled} onclick={show} title="Ask Kuri to edit this {kind}">
    <svg class="inline-block align-middle w-4 h-4 mr-1" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5Z" />
      <path d="m20 2 .6 1.4L22 4l-1.4.6L20 6l-.6-1.4L18 4l1.4-.6Z" />
      <path d="m4 18 .6 1.4L6 20l-1.4.6L4 22l-.6-1.4L2 20l1.4-.6Z" />
    </svg>Ask Kuri
  </button>
{/if}

  <FloatingAIWindow {open} title="Kuri" {name} {busy} onclose={close} bind:dialog>
    <div class="flex gap-2">
      <select class="input min-w-0 flex-1" aria-label="AI connection" bind:value={connection} disabled={busy || modelLoading}
        onchange={(event) => { connection = event.currentTarget.value; model = ''; models = []; rememberSelection(); resetConversation(); void loadModels(); }}>
        {#each providers as provider}<option value={provider.connection_id}>{provider.name}</option>{/each}
      </select>
      <input class="input min-w-0 flex-1" aria-label="AI model" list="assistant-models" bind:value={model}
        placeholder={modelLoading ? 'Loading models...' : 'Model'} disabled={busy || modelLoading}
        onchange={(event) => { model = event.currentTarget.value; rememberSelection(); resetConversation(); }} />
      <datalist id="assistant-models">{#each models as item}<option value={item}></option>{/each}</datalist>
    </div>
    <label class="flex items-center gap-2 text-sm" title="Allow deployment, enable/disable, and test runs. Tests can operate devices and hosts.">
      <input type="checkbox" bind:checked={allowExecution} disabled={busy} />
      Allow deployment and execution
    </label>
    <div class="transcript" bind:this={transcript} role="log" aria-label="Kuri conversation" aria-live="polite">
      {#if entries.length === 0}
        <p class="empty">Describe what to change or ask for improvements. Draft changes appear live behind this dialog.</p>
      {/if}
      {#each entries as entry}
        {#if entry.role === 'notice'}
          <p class="notice" role="note">{entry.text}</p>
        {:else if entry.role === 'tool'}
          <details class="tool" class:failed={entry.failed}>
            <summary><i class="las la-wrench mr-1"></i>{entry.text}<span class="opacity-60 ml-2">{entry.result === undefined ? 'Running...' : entry.failed ? 'Failed' : 'Done'}</span></summary>
            <pre>{JSON.stringify(entry.args, null, 2)}</pre>
            {#if entry.result !== undefined}<pre>{JSON.stringify(entry.result, null, 2)}</pre>{/if}
            {#each entry.images ?? [] as image}
              <img class="w-full rounded mt-2" src={`data:${image.MIME};base64,${image.Data}`} alt="Dashboard preview" />
            {/each}
          </details>
        {:else if entry.text || entry.reasoning}
          <div class="message" class:user={entry.role === 'user'}>
            <div class="text-xs opacity-60 mb-1">{entry.role === 'user' ? 'You' : 'Kuri'}</div>
            {#if entry.reasoning}<AIReasoning text={entry.reasoning} />{/if}
            {#if entry.role === 'assistant' && entry.text}
              <AssistantMarkdown source={entry.text} onrender={() => void follow()} />
            {:else if entry.text}
              <div class="whitespace-pre-wrap break-words">{entry.text}</div>
            {/if}
          </div>
        {/if}
      {/each}
      {#if busy}<p class="text-xs opacity-60" role="status">Working...</p>{/if}
    </div>
    {#if error}<p class="text-sm" role="alert" style="color: var(--theme-error)">{error}</p>{/if}
    <form class="flex flex-col gap-2" onsubmit={(e) => { e.preventDefault(); void send(); }}>
      <textarea bind:this={input} class="input w-full" rows="3" aria-label="Message to Kuri" bind:value={prompt}
        placeholder="What would you like to change?" maxlength="32000"
        onkeydown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); void send(); } }}></textarea>
      <div class="flex items-center gap-2">
        <span class="text-xs opacity-60 flex-1">Drafts autosave. Deploy when ready.</span>
        <button type="button" class="btn btn-secondary btn-sm" disabled={busy || !entries.length} onclick={resetConversation}>New chat</button>
        {#if busy}
          <button type="button" class="btn btn-secondary btn-sm" onclick={() => controller?.abort()}>Stop</button>
        {:else}
          <button type="submit" class="btn btn-primary btn-sm" disabled={!prompt.trim() || !model.trim() || modelLoading}>Send</button>
        {/if}
      </div>
    </form>
  </FloatingAIWindow>

<style>
  .transcript { flex: 1; min-height: 60px; overflow-y: auto; display: flex; flex-direction: column; gap: .65rem; }
  .empty { margin: auto; text-align: center; font-size: .9rem; opacity: .65; max-width: 26rem; }
  .message { min-width: 0; padding: .65rem .8rem; border-radius: 8px; font-size: .9rem; background: color-mix(in srgb, var(--theme-base) 65%, transparent); }
  .user { margin-left: 2rem; background: color-mix(in srgb, var(--theme-accent) 13%, transparent); }
  .tool { font-size: .75rem; padding: .5rem; border: 1px solid var(--theme-surface1); border-radius: 6px; }
  .tool summary { cursor: pointer; overflow-wrap: anywhere; }
  .tool pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 180px; overflow-y: auto; padding-top: .5rem; }
  .failed { border-color: var(--theme-error); }
  .notice { display: flex; align-items: center; gap: .5rem; font-size: .75rem; opacity: .65; }
  .notice::before, .notice::after { content: ''; flex: 1; border-top: 1px solid var(--theme-surface1); }
</style>
