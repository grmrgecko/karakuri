<script lang="ts">
  import { runState } from '../../stores/runState';
  import { editor } from '../../stores/editor';
  import { nodeCatalog } from '../../stores/nodeCatalog';
  import { relTime, duration, clockTime, untilTime } from '../../utils/time';
  import type { PendingWait } from '../../stores/runState';
  import type { EditorNode } from './types';

  let { selectedNode, nodes }: { selectedNode?: EditorNode; nodes: EditorNode[] } = $props();

  // Ticking clock so relative times stay fresh.
  let now = $state(Date.now());
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 10000);
    return () => clearInterval(t);
  });

  let run = $derived($runState.run);
  // Run event node IDs carry the drill prefix while inside a subflow
  // ("<subflowNodeID>/<childID>"): canvas node IDs map through it.
  let prefix = $derived($editor.runPrefix);
  let nodeState = $derived(selectedNode ? $runState.nodeStates[prefix + selectedNode.id] : undefined);
  let nodeDebugs = $derived(selectedNode ? $runState.debugs.filter((d) => d.node_id === prefix + selectedNode.id) : []);
  let nodeEvents = $derived(selectedNode ? $runState.events.filter((e) => e.node_id === prefix + selectedNode.id) : []);

  // nodeLabel resolves a display name for a (possibly namespaced) node id,
  // tolerating deleted nodes and subflow children not on this canvas.
  function nodeLabel(id: string): string {
    const local = id.startsWith(prefix) ? id.slice(prefix.length) : id;
    const n = nodes.find((x) => x.id === local);
    if (!n) return id;
    return n.data.label || $nodeCatalog.byType[n.data.nodeType]?.label || n.data.nodeType;
  }

  function pretty(payload: unknown): string {
    try {
      return JSON.stringify(payload, null, 2);
    } catch {
      return String(payload);
    }
  }

  let waits = $derived($runState.waits);
  let nodeWaits = $derived(selectedNode ? waits.filter((w) => w.node_id === prefix + selectedNode.id) : []);
  let cancelError = $state('');

  // waitKeyLabel shows the part of a keyed wait's key an author chose, such
  // as the topic, and nothing for the node's single unkeyed wait.
  function waitKeyLabel(key: string): string {
    const i = key.indexOf(':key:');
    return i >= 0 ? key.slice(i + 5) : '';
  }

  async function cancelWait(w: PendingWait) {
    cancelError = '';
    try {
      await runState.cancelWait(w.node_id, w.key);
    } catch (err) {
      cancelError = err instanceof Error ? err.message : String(err);
    }
  }

  function selectNode(id: string) {
    const local = id.startsWith(prefix) ? id.slice(prefix.length) : id;
    if (nodes.some((n) => n.id === local)) editor.select(local);
  }
</script>

{#snippet waitList(list: PendingWait[])}
  <div>
    <h4 class="section-title">Pending waits</h4>
    <div class="flex flex-col gap-1">
      {#each list as w (w.node_id + '|' + w.key)}
        <div class="wait-row" title="Armed by run {w.run_id}. If the server is down at the deadline: {w.missed_deadline === 'drop' ? 'dropped' : 'sent late'}.">
          <button class="wait-node truncate" onclick={() => selectNode(w.node_id)}>{nodeLabel(w.node_id)}</button>
          {#if waitKeyLabel(w.key)}<span class="truncate opacity-60">{waitKeyLabel(w.key)}</span>{/if}
          <span class="wait-when" title={new Date(w.deadline).toLocaleString()}>{untilTime(w.deadline, now)}</span>
          {#if !w.durable}
            <span class="wait-volatile" title="This wait could not be stored and will not survive a restart.">
              <i class="las la-exclamation-triangle"></i>
            </span>
          {/if}
          <button class="wait-cancel" title="Cancel this wait without sending it" aria-label="Cancel wait" onclick={() => cancelWait(w)}>
            <i class="las la-times"></i>
          </button>
        </div>
      {/each}
    </div>
    {#if cancelError}<p class="text-xs mt-1" style="color: var(--theme-error, #f38ba8);">{cancelError}</p>{/if}
  </div>
{/snippet}

<div class="flex flex-col gap-4 p-3 text-sm">
  {#if !run}
    <p class="text-xs opacity-60">
      No runs yet. Use the <i class="las la-play"></i> button on a trigger node to run the draft graph.
    </p>
    {#if waits.length > 0}{@render waitList(waits)}{/if}
  {:else if selectedNode}
    <!-- Per-node run view -->
    <div class="flex items-center gap-2 min-w-0">
      <div class="min-w-0">
        <h3 class="text-sm font-semibold truncate">{nodeLabel(selectedNode.id)}</h3>
        <p class="text-xs opacity-50 truncate">{selectedNode.id}</p>
      </div>
    </div>

    {#if nodeState}
      <div class="flex items-center gap-2 flex-wrap">
        <span class="state-badge state-{nodeState.state}" class:handled={nodeState.handled}>
          {nodeState.state}{nodeState.state === 'failed' && nodeState.handled ? ' (handled)' : ''}
        </span>
        {#if nodeState.attempt > 1 || nodeState.state === 'retrying'}
          <span class="text-xs opacity-70">attempt {nodeState.attempt}</span>
        {/if}
      </div>
      {#if nodeState.error}
        <div class="error-box">
          <div class="text-xs font-semibold mb-1">Error</div>
          <pre>{nodeState.error}</pre>
        </div>
      {/if}
    {:else}
      <p class="text-xs opacity-60">This node did not participate in the run.</p>
    {/if}

    {#if nodeWaits.length > 0}{@render waitList(nodeWaits)}{/if}

    {#if nodeDebugs.length > 0}
      <div>
        <h4 class="section-title">Debug output</h4>
        <div class="flex flex-col gap-2">
          {#each nodeDebugs as d, i (i)}
            <div class="debug-entry">
              <div class="flex items-center gap-2 text-xs opacity-60 mb-1">
                <span>{clockTime(d.at)}</span>
                {#if d.note}<span class="truncate">{d.note}</span>{/if}
              </div>
              <pre class="payload">{pretty(d.payload)}</pre>
            </div>
          {/each}
        </div>
      </div>
    {/if}

    {#if nodeEvents.length > 0}
      <div>
        <h4 class="section-title">Timeline</h4>
        <ul class="timeline">
          {#each nodeEvents as ev, i (i)}
            <li>
              <span class="tl-time">{clockTime(ev.at)}</span>
              {#if ev.event === 'debug'}
                <span class="tl-state" style="color: var(--theme-accent);">debug</span>
              {:else}
                <span class="tl-state state-text-{ev.state}">{ev.state}</span>
                {#if ev.attempt > 1 || ev.state === 'retrying'}
                  <span class="opacity-60">attempt {ev.attempt}</span>
                {/if}
              {/if}
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  {:else}
    <!-- Run summary view -->
    <div class="flex items-center gap-2 flex-wrap">
      <span class="state-badge state-{run.status}">{run.status}</span>
      {#if run.had_failures}
        <span class="text-xs" style="color: var(--theme-warning);" title="Some nodes failed and were handled by an error branch">
          <i class="las la-exclamation-triangle"></i> had failures
        </span>
      {/if}
    </div>

    <dl class="run-meta">
      <dt>Started</dt>
      <dd>{clockTime(run.started_at)} <span class="opacity-50">({relTime(run.started_at, now)})</span></dd>
      {#if run.ended_at}
        <dt>Ended</dt>
        <dd>{clockTime(run.ended_at)}</dd>
        <dt>Duration</dt>
        <dd>{duration(run.started_at, run.ended_at)}</dd>
      {/if}
      {#if run.resumed_from_run_id}
        <dt>Resumed</dt>
        <dd title="This run finished a wait armed by run {run.resumed_from_run_id}, carried over a restart or a redeploy.">
          from run {run.resumed_from_run_id.slice(0, 8)}
        </dd>
      {/if}
    </dl>

    {#if waits.length > 0}{@render waitList(waits)}{/if}

    {#if run.error}
      <div class="error-box">
        <div class="text-xs font-semibold mb-1">Error</div>
        <pre>{run.error}</pre>
      </div>
    {/if}

    <div>
      <h4 class="section-title">Debug entries</h4>
      {#if $runState.debugs.length === 0}
        <p class="text-xs opacity-50">No debug output in this run.</p>
      {:else}
        <div class="flex flex-col gap-1">
          {#each $runState.debugs as d, i (i)}
            <button class="debug-row" onclick={() => selectNode(d.node_id)} title="Show this node's run details">
              <span class="tl-time">{clockTime(d.at)}</span>
              <span class="truncate font-medium">{nodeLabel(d.node_id)}</span>
              {#if d.note}<span class="truncate opacity-60">{d.note}</span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <p class="text-xs opacity-40">Select a node on the canvas for its states, attempts, and payloads.</p>
  {/if}
</div>

<style>
  .section-title {
    font-size: 0.7rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    opacity: 0.6;
    margin-bottom: 0.4rem;
  }

  .state-badge {
    display: inline-block;
    padding: 0.1rem 0.55rem;
    border-radius: 999px;
    border: 1px solid;
    font-size: 0.7rem;
    font-weight: 600;
    text-transform: capitalize;
  }

  .state-badge.state-running,
  .state-badge.state-retrying {
    color: var(--theme-warning, #f9e2af);
    border-color: var(--theme-warning, #f9e2af);
  }

  .state-badge.state-succeeded {
    color: var(--theme-success, #a6e3a1);
    border-color: var(--theme-success, #a6e3a1);
  }

  .state-badge.state-failed,
  .state-badge.state-canceled {
    color: var(--theme-error, #f38ba8);
    border-color: var(--theme-error, #f38ba8);
  }

  .state-badge.state-failed.handled {
    color: var(--theme-warning, #fab387);
    border-color: var(--theme-warning, #fab387);
  }

  .state-badge.state-skipped {
    color: var(--theme-overlay0, #6c7086);
    border-color: var(--theme-overlay0, #6c7086);
  }

  .state-text-running, .state-text-retrying { color: var(--theme-warning, #f9e2af); }
  .state-text-succeeded { color: var(--theme-success, #a6e3a1); }
  .state-text-failed { color: var(--theme-error, #f38ba8); }
  .state-text-skipped { color: var(--theme-overlay0, #6c7086); }

  .error-box {
    border: 1px solid var(--theme-error, #f38ba8);
    border-radius: var(--theme-radius-md, 8px);
    padding: 0.5rem;
    color: var(--theme-error, #f38ba8);
  }

  .error-box pre {
    font-size: 0.7rem;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 8rem;
    overflow-y: auto;
  }

  .debug-entry {
    background-color: var(--theme-surface0, #313244);
    border-radius: var(--theme-radius-md, 8px);
    padding: 0.5rem;
  }

  .payload {
    font-size: 0.7rem;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 14rem;
    overflow-y: auto;
    font-family: var(--theme-font-mono, monospace);
  }

  .timeline {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    font-size: 0.72rem;
  }

  .timeline li {
    display: flex;
    gap: 0.5rem;
    align-items: baseline;
  }

  .tl-time {
    font-family: var(--theme-font-mono, monospace);
    opacity: 0.5;
    font-size: 0.68rem;
    white-space: nowrap;
  }

  .tl-state {
    font-weight: 600;
    text-transform: capitalize;
  }

  .debug-row {
    display: flex;
    gap: 0.5rem;
    align-items: baseline;
    width: 100%;
    text-align: left;
    padding: 0.25rem 0.4rem;
    border-radius: var(--theme-radius-sm, 4px);
    font-size: 0.72rem;
    background: none;
    border: none;
    color: inherit;
    cursor: pointer;
  }

  .debug-row:hover {
    background-color: var(--theme-surface0, #313244);
  }

  .wait-row {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    padding: 0.2rem 0.4rem;
    border-radius: var(--theme-radius-sm, 4px);
    font-size: 0.72rem;
  }

  .wait-row:hover {
    background-color: var(--theme-surface0, #313244);
  }

  .wait-node {
    font-weight: 500;
    background: none;
    border: none;
    color: inherit;
    padding: 0;
    cursor: pointer;
    text-align: left;
  }

  .wait-when {
    margin-left: auto;
    font-family: var(--theme-font-mono, monospace);
    opacity: 0.7;
    white-space: nowrap;
  }

  .wait-volatile {
    color: var(--theme-warning, #f9e2af);
  }

  .wait-cancel {
    background: none;
    border: none;
    color: inherit;
    opacity: 0.5;
    cursor: pointer;
    padding: 0 0.15rem;
  }

  .wait-cancel:hover {
    opacity: 1;
    color: var(--theme-error, #f38ba8);
  }
</style>
