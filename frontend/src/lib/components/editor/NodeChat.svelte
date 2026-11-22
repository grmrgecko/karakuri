<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { api } from '../../api/client';
  import FloatingAIWindow from '../FloatingAIWindow.svelte';
  import AIReasoning from '../AIReasoning.svelte';

  let { flowId, nodeId, subgraphId, name, open, onclose, prepare }: {
    flowId: string; nodeId: string; subgraphId?: string; name: string;
    open: boolean; onclose: () => void; prepare: () => Promise<boolean>;
  } = $props();

  type Entry = { role: 'user' | 'assistant' | 'tool' | 'notice'; text: string; reasoning?: string; args?: unknown; result?: unknown; failed?: boolean };
  let entries = $state<Entry[]>([]);
  let prompt = $state('');
  let busy = $state(false);
  let error = $state('');
  let response: Record<string, unknown> | undefined;
  let controller: AbortController | undefined;
  let transcript = $state<HTMLDivElement>();
  let input = $state<HTMLTextAreaElement>();
  let dialog = $state<HTMLDialogElement>();
  let scroll = 0;
  let destroyed = false;

  onDestroy(() => { destroyed = true; controller?.abort(); });
  $effect(() => {
    if (open) void tick().then(() => {
      input?.focus({ preventScroll: true });
      if (transcript) transcript.scrollTop = scroll;
    });
  });

  function close() {
    if (busy) return;
    scroll = transcript?.scrollTop ?? 0;
    onclose();
  }
  function reset() {
    entries = [];
    response = undefined;
    error = '';
    scroll = 0;
    input?.focus({ preventScroll: true });
  }
  function outputText(value: unknown): string {
    return typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? '';
  }
  // turnReasoning joins the reasoning of every assistant round after the
  // envelope's last user message, which is the turn that just finished.
  function turnReasoning(envelope: Record<string, unknown> | undefined): string {
    const rows = Array.isArray(envelope?.transcript) ? envelope.transcript as { role?: string; reasoning_summary?: string }[] : [];
    let start = 0;
    rows.forEach((row, i) => { if (row.role === 'user') start = i + 1; });
    return rows.slice(start).map(row => row.role === 'assistant' ? row.reasoning_summary?.trim() ?? '' : '').filter(Boolean).join('\n\n');
  }
  async function follow() {
    await tick();
    transcript?.scrollTo({ top: transcript.scrollHeight });
  }
  async function send() {
    const text = prompt.trim();
    if (!text || busy) return;
    busy = true;
    error = '';
    const abort = new AbortController();
    controller = abort;
    let completed = false;
    let nextResponse: Record<string, unknown> | undefined;
    let reply = -1;
    try {
      if (!await prepare()) throw new Error('could not save the draft; resolve the save error before chatting');
      if (abort.signal.aborted) throw new DOMException('Stopped', 'AbortError');
      prompt = '';
      entries.push({ role: 'user', text });
      reply = entries.length;
      entries.push({ role: 'assistant', text: '' });
      await api.streamJSON(`/flows/${flowId}/nodes/${encodeURIComponent(nodeId)}/chat`, {
        prompt: text, subgraph_id: subgraphId, response,
      }, abort.signal, async (event) => {
        if (event.type === 'content.delta') {
          entries[reply].text += event.delta;
        } else if (event.type === 'context.compacted') {
          entries.push({ role: 'notice', text: 'Context compacted' });
        } else if (event.type === 'tool.progress') {
          const label = event.label || event.tool_name || 'Tool';
          entries.push({ role: 'tool', text: `${label}: ${event.status || 'Running'}` });
        } else if (event.type === 'result') {
          entries[reply].text = outputText(event.output);
          for (const tool of event.meta?.tool_calls ?? []) {
            entries.push({ role: 'tool', text: tool.tool, args: tool.args, result: tool.result, failed: tool.error });
          }
        } else if (event.type === 'response.completed') {
          nextResponse = event.response;
          const reasoning = turnReasoning(nextResponse);
          if (reasoning) entries[reply].reasoning = reasoning;
        } else if (event.type === 'done') {
          completed = true;
        } else if (event.type === 'error') {
          throw new Error(event.message);
        }
        await follow();
      });
      if (!completed || !nextResponse) throw new Error('the response ended before the node finished; send another message to retry');
      response = nextResponse;
    } catch (e: any) {
      const details = Object.values(e.details ?? {}).join('; ');
      error = e.name === 'AbortError' ? 'Stopped. Start a new turn to continue.' : details || e.message || 'the node chat request failed';
      response = undefined;
    } finally {
      busy = false;
      controller = undefined;
      if (!destroyed) {
        await tick();
        if (open && dialog?.contains(document.activeElement)) input?.focus({ preventScroll: true });
      }
    }
  }
</script>

<FloatingAIWindow {open} title="Test node in chat" {name} {busy} onclose={close} bind:dialog>
  <div class="transcript" bind:this={transcript} role="log" aria-label="Node chat conversation" aria-live="polite">
    {#if entries.length === 0}
      <p class="empty">Send a message to test this node's configured model and request. Messages supply In for prompt expressions. Wired tools run as configured.</p>
    {/if}
    {#each entries as entry}
      {#if entry.role === 'notice'}
        <p class="notice" role="note">{entry.text}</p>
      {:else if entry.role === 'tool'}
        <details class="tool" class:failed={entry.failed}>
          <summary><i class="las la-wrench mr-1"></i>{entry.text}{#if entry.failed}<span class="ml-2">Failed</span>{/if}</summary>
          {#if entry.args !== undefined}<pre>{outputText(entry.args)}</pre>{/if}
          {#if entry.result !== undefined}<pre>{outputText(entry.result)}</pre>{/if}
        </details>
      {:else if entry.text || entry.reasoning}
        <div class="message" class:user={entry.role === 'user'}>
          <div class="text-xs opacity-60 mb-1">{entry.role === 'user' ? 'You' : 'AI'}</div>
          {#if entry.reasoning}<AIReasoning text={entry.reasoning} />{/if}
          <div class="whitespace-pre-wrap break-words">{entry.text}</div>
        </div>
      {/if}
    {/each}
    {#if busy}<p class="text-xs opacity-60" role="status">Working...</p>{/if}
  </div>
  {#if error}<p class="text-sm" role="alert" style="color: var(--theme-error)">{error}</p>{/if}
  <form class="flex flex-col gap-2" onsubmit={(e) => { e.preventDefault(); void send(); }}>
    <textarea bind:this={input} class="input w-full" rows="3" aria-label="Message to node" bind:value={prompt}
      placeholder="Message to test this node" maxlength="32000"
      onkeydown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); void send(); } }}></textarea>
    <div class="flex items-center gap-2">
      <span class="text-xs opacity-60 flex-1">Uses the node's draft settings and wiring.</span>
      <button type="button" class="btn btn-secondary btn-sm" disabled={busy || !entries.length} onclick={reset}>New chat</button>
      {#if busy}
        <button type="button" class="btn btn-secondary btn-sm" onclick={() => controller?.abort()}>Stop</button>
      {:else}
        <button type="submit" class="btn btn-primary btn-sm" disabled={!prompt.trim()}>Send</button>
      {/if}
    </div>
  </form>
</FloatingAIWindow>

<style>
  .transcript { flex: 1; min-height: 60px; overflow-y: auto; display: flex; flex-direction: column; gap: .65rem; }
  .empty { margin: auto; text-align: center; font-size: .9rem; opacity: .65; max-width: 26rem; }
  .message { padding: .65rem .8rem; border-radius: 8px; font-size: .9rem; background: color-mix(in srgb, var(--theme-base) 65%, transparent); }
  .user { margin-left: 2rem; background: color-mix(in srgb, var(--theme-accent) 13%, transparent); }
  .tool { font-size: .75rem; padding: .5rem; border: 1px solid var(--theme-surface1); border-radius: 6px; }
  .tool summary { cursor: pointer; overflow-wrap: anywhere; }
  .tool pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 180px; overflow-y: auto; padding-top: .5rem; }
  .failed { border-color: var(--theme-error); }
  .notice { display: flex; align-items: center; gap: .5rem; font-size: .75rem; opacity: .65; }
  .notice::before, .notice::after { content: ''; flex: 1; border-top: 1px solid var(--theme-surface1); }
</style>
