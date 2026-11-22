<script lang="ts">
  import { nodeCatalog, type RetryPolicy } from '../../stores/nodeCatalog';
  import { nodePorts } from '../../utils/nodePorts';
  import { PortAny } from '../../utils/payloadPorts';
  import ConfigForm from './ConfigForm.svelte';
  import type { EditorNode, FlowNodeData } from './types';

  let {
    node,
    onUpdateData,
    onConfigChange,
    onToggleParam,
    onDelete,
    onOpenSubflow,
    onPromote,
    onImportLocal,
  }: {
    node: EditorNode;
    onUpdateData: (patch: Partial<FlowNodeData>) => void;
    onConfigChange: (fieldId: string, value: unknown) => void;
    // onToggleParam exposes or hides a config field as a value input socket.
    onToggleParam: (fieldId: string, exposed: boolean) => void;
    onDelete: () => void;
    onOpenSubflow?: () => void;
    onPromote?: () => void;
    onImportLocal?: () => void;
  } = $props();

  let meta = $derived($nodeCatalog.byType[node.data.nodeType]);
  // A type absent from a loaded catalog means the plugin that provides it is
  // disabled or uninstalled. There is no config spec to render, so the panel
  // explains the state instead; the stored config survives untouched.
  let missing = $derived($nodeCatalog.loaded && !meta);
  let isSubflow = $derived(node.data.nodeType === 'subflow');
  // An annotation draws its own contents and never shows a node header, so a
  // label field on it would edit something the canvas has nowhere to put.
  let isAnnotation = $derived(meta?.category === 'annotation');
  let isTrigger = $derived(meta?.category === 'trigger');

  // test_params moves out of the main config list into a dedicated Testing
  // section so trigger test data is clearly separated from real config.
  let mainSpec = $derived((meta?.config_spec ?? []).filter((f) => f.id !== 'test_params'));
  let testSpec = $derived((meta?.config_spec ?? []).filter((f) => f.id === 'test_params'));
  let testingOpen = $state(false);

  // The test payload is edited as the type the trigger's own output port
  // declares, so a text trigger is tested with text and a JSON one with a
  // document. The port is the first that is not the failure branch, which is
  // the one a run starts on.
  let testPayloadType = $derived(
    nodePorts(meta, node.data).outputs.find((p) => p.kind !== 'error')?.type ?? PortAny,
  );

  const defaultRetry: RetryPolicy = { max_attempts: 3, delay_ms: 1000, backoff: 2, max_delay_ms: 30000 };

  function toggleRetry(enabled: boolean) {
    onUpdateData({ retry: enabled ? { ...(meta?.default_retry ?? defaultRetry) } : undefined });
  }

  function setRetryField(field: keyof RetryPolicy, value: number) {
    if (!node.data.retry || !Number.isFinite(value)) return;
    onUpdateData({ retry: { ...node.data.retry, [field]: value } });
  }
</script>

<div class="flex flex-col gap-4 p-3">
  <div class="flex items-center gap-2">
    <i class={meta?.icon || 'las la-cube'} style="font-size: 1.25rem; color: var(--theme-accent);"></i>
    <div class="min-w-0">
      <h3 class="text-sm font-semibold truncate">{meta?.label || node.data.nodeType}</h3>
      <p class="text-xs opacity-50 truncate">{node.id}</p>
    </div>
  </div>

  {#if missing}
    <div class="missing-note">
      <i class="las la-exclamation-triangle"></i>
      <p>
        The node type <code>{node.data.nodeType}</code> is not available. The plugin that
        provides it is disabled or not installed, so its settings cannot be shown and the
        flow cannot deploy. The node keeps its settings and wiring, and works again once
        the plugin is enabled under Settings, Plugins.
      </p>
    </div>
  {/if}

  {#if meta?.description}
    <p class="text-xs opacity-60">{meta.description}</p>
  {/if}

  {#if isSubflow}
    <div class="flex flex-col gap-2">
      <p class="text-xs opacity-60">
        {node.data.subgraphId
          ? 'Local subflow: exists only inside this flow.'
          : 'Shared subflow from the library. Its deployed version runs inside this flow.'}
      </p>
      <button class="btn btn-secondary btn-sm w-full" onclick={() => onOpenSubflow?.()}>
        <i class="las la-layer-group mr-1"></i>{node.data.subgraphId ? 'Edit subflow' : 'View subflow'}
      </button>
      {#if node.data.subgraphId && onPromote}
        <button class="btn btn-secondary btn-sm w-full" onclick={() => onPromote?.()}>
          <i class="las la-share-square mr-1"></i>Promote to shared library...
        </button>
      {/if}
      {#if node.data.subflowId && onImportLocal}
        <button class="btn btn-secondary btn-sm w-full" onclick={() => onImportLocal?.()}>
          <i class="las la-download mr-1"></i>Import as local copy
        </button>
      {/if}
    </div>
  {/if}

  {#if !isAnnotation}
    <div>
      <label for="node-label" class="block text-xs font-medium mb-1">Node label</label>
      <input
        id="node-label"
        class="input w-full text-sm"
        placeholder={meta?.label || node.data.nodeType}
        value={node.data.label}
        oninput={(e) => onUpdateData({ label: e.currentTarget.value })}
      />
      <p class="text-xs mt-1 opacity-50">Overrides the display name on the canvas.</p>
    </div>
  {/if}

  <!-- With the type missing there is no spec to render fields from, and the
       form's own "no configuration" line would contradict the note above. -->
  {#if !missing}
    {#key node.id}
      <ConfigForm
        spec={mainSpec}
        values={node.data.config}
        idPrefix="cfg-{node.id}"
        onChange={onConfigChange}
        params={node.data.params ?? []}
        onToggleParam={onToggleParam}
        scriptAnalysis={node.data.nodeType === 'script'}
      />

    {#if testSpec.length > 0}
      <div class="border-t pt-3" style="border-color: var(--theme-surface1);">
        <button
          type="button"
          class="testing-toggle"
          aria-expanded={testingOpen}
          onclick={() => (testingOpen = !testingOpen)}
        >
          <i class="las {testingOpen ? 'la-angle-down' : 'la-angle-right'}"></i>
          <span class="font-medium">Testing</span>
          {#if node.data.config.test_params != null}
            <span class="testing-set" title="Test parameters are set"></span>
          {/if}
        </button>
        {#if testingOpen}
          <div class="mt-2">
            <ConfigForm
              spec={testSpec}
              values={node.data.config}
              idPrefix="test-{node.id}"
              onChange={onConfigChange}
              payloadType={testPayloadType}
              helpAbove
            />
          </div>
        {/if}
      </div>
    {/if}
    {/key}
  {/if}

  {#if !isTrigger && !isAnnotation && !missing}
    <div class="border-t pt-3" style="border-color: var(--theme-surface1);">
      <label class="flex items-center gap-2 text-sm cursor-pointer">
        <input
          type="checkbox"
          checked={!!node.data.execute_once}
          onchange={(e) => onUpdateData({ execute_once: e.currentTarget.checked || undefined })}
        />
        <span class="font-medium">Run once per run</span>
      </label>
      <p class="text-xs opacity-60 mt-1">
        Runs for the first message that reaches this node and drops the rest, so work that is
        per-run rather than per-message does not repeat behind a split.
      </p>
    </div>
  {/if}

  {#if meta?.retryable}
    <div class="border-t pt-3" style="border-color: var(--theme-surface1);">
      <label class="flex items-center gap-2 text-sm cursor-pointer">
        <input
          type="checkbox"
          checked={!!node.data.retry}
          onchange={(e) => toggleRetry(e.currentTarget.checked)}
        />
        <span class="font-medium">Override retry policy</span>
      </label>
      {#if node.data.retry}
        <div class="grid grid-cols-2 gap-2 mt-2">
          <div>
            <label for="retry-attempts" class="block text-xs mb-1 opacity-70">Max attempts</label>
            <input
              id="retry-attempts"
              type="number"
              min="1"
              max="1000"
              class="input w-full text-sm"
              value={node.data.retry.max_attempts}
              oninput={(e) => setRetryField('max_attempts', parseInt(e.currentTarget.value, 10))}
            />
          </div>
          <div>
            <label for="retry-delay" class="block text-xs mb-1 opacity-70">Delay (s)</label>
            <input
              id="retry-delay"
              type="number"
              min="0"
              step="0.1"
              class="input w-full text-sm"
              value={node.data.retry.delay_ms / 1000}
              oninput={(e) => setRetryField('delay_ms', Math.round(Number(e.currentTarget.value) * 1000))}
            />
          </div>
          <div>
            <label for="retry-backoff" class="block text-xs mb-1 opacity-70">Backoff</label>
            <input
              id="retry-backoff"
              type="number"
              min="1"
              step="0.1"
              class="input w-full text-sm"
              value={node.data.retry.backoff}
              oninput={(e) => setRetryField('backoff', Number(e.currentTarget.value))}
            />
          </div>
          <div>
            <label for="retry-maxdelay" class="block text-xs mb-1 opacity-70">Max delay (s)</label>
            <input
              id="retry-maxdelay"
              type="number"
              min="0"
              step="0.1"
              class="input w-full text-sm"
              value={node.data.retry.max_delay_ms / 1000}
              oninput={(e) => setRetryField('max_delay_ms', Math.round(Number(e.currentTarget.value) * 1000))}
            />
          </div>
          <div>
            <label for="retry-jitter" class="block text-xs mb-1 opacity-70">Jitter (%)</label>
            <input
              id="retry-jitter"
              type="number"
              min="0"
              max="100"
              step="1"
              class="input w-full text-sm"
              value={(node.data.retry.jitter ?? 0) * 100}
              oninput={(e) => setRetryField('jitter', Number(e.currentTarget.value) / 100)}
            />
          </div>
        </div>
        <p class="text-xs mt-2 opacity-50">Delays use seconds. Jitter varies each delay by this percentage within the maximum delay. Permanent failures do not retry.</p>
      {/if}
    </div>
  {/if}

  <div class="border-t pt-3" style="border-color: var(--theme-surface1);">
    <button class="btn btn-danger btn-sm w-full" onclick={onDelete}>
      <i class="las la-trash mr-1"></i> Delete Node
    </button>
  </div>
</div>

<style>
  .missing-note {
    display: flex;
    gap: 0.5rem;
    padding: 0.5rem 0.6rem;
    border: 1px dashed var(--theme-error, #f38ba8);
    border-radius: 6px;
    font-size: 0.75rem;
    line-height: 1.4;
  }

  .missing-note i {
    color: var(--theme-error, #f38ba8);
    font-size: 1rem;
    margin-top: 0.1rem;
  }

  .missing-note p {
    margin: 0;
  }

  .missing-note code {
    font-size: 0.7rem;
  }

  .testing-toggle {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    width: 100%;
    padding: 0;
    background: none;
    border: none;
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    cursor: pointer;
    text-align: left;
  }

  .testing-set {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background-color: var(--theme-accent, #89b4fa);
    display: inline-block;
  }
</style>
