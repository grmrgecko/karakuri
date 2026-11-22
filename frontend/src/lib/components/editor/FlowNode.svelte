<script lang="ts">
  import { Handle, useUpdateNodeInternals, type NodeProps } from '@xyflow/svelte';
  import { nodeCatalog } from '../../stores/nodeCatalog';
  import { hasPermission } from '../../stores/auth';
  import { editor } from '../../stores/editor';
  import { runState } from '../../stores/runState';
  import { subflowLibrary } from '../../stores/subflows';
  import { nodePorts } from '../../utils/nodePorts';
  import { portTypeLabel, PortAny } from '../../utils/payloadPorts';
  import { portTypeColor } from '../../utils/typeColors';
  import { carriedTypes } from '../../stores/carriedTypes';
  import { testTargets } from '../../stores/testTargets';
  import RotateGrips from './RotateGrips.svelte';
  import { handleSides, nodeRotation } from './rotation';
  import type { EditorNode } from './types';

  let { id, data, selected }: NodeProps<EditorNode> = $props();

  let meta = $derived($nodeCatalog.byType[data.nodeType]);
  // A type absent from a loaded catalog is a node whose plugin is disabled or
  // uninstalled. The node keeps its config and wiring; the card marks itself
  // unavailable and says why. Nothing is missing before the catalog loads, or
  // every node would flash red while the editor opens.
  let missing = $derived($nodeCatalog.loaded && !meta);
  let missingNote = $derived(
    missing
      ? `Node type "${data.nodeType}" is not available. The plugin that provides it is disabled or not installed; the node keeps its settings and wiring but cannot run until the plugin loads.`
      : ''
  );
  let isSubflow = $derived(data.nodeType === 'subflow');

  // Shared subflow references fetch the referenced graph for port derivation.
  $effect(() => {
    if (isSubflow && data.subflowId) subflowLibrary.ensureGraph(data.subflowId);
  });

  // Ports come from the shared resolver, which mirrors flow.NodePorts. Only
  // the subflow graph has to be supplied here, since the editor is where the
  // referenced graphs live.
  let subGraph = $derived(
    data.subgraphId
      ? $editor.docSubgraphs[data.subgraphId]
      : data.subflowId
        ? $subflowLibrary.graphs[data.subflowId]?.graph
        : null
  );

  let ports = $derived(nodePorts(meta, data, subGraph));

  // An output that only carries its input onward is answered from the graph,
  // which this component cannot see: EditorShell resolves it for every node at
  // once and leaves the answer here.
  let carried = $derived($carriedTypes[id] ?? {});

  let subflowName = $derived.by(() => {
    if (!isSubflow) return '';
    if (data.subflowId) return $subflowLibrary.graphs[data.subflowId]?.name
      || $subflowLibrary.records.find((r) => r.id === data.subflowId)?.name
      || 'Subflow';
    return 'Subflow';
  });

  // A Set/Get pair is identified by its published name, so the card leads
  // with it.
  let linkName = $derived(
    data.nodeType === 'link-set' || data.nodeType === 'link-get'
      ? String(data.config.name ?? '')
      : ''
  );
  let label = $derived(
    data.label ||
      (isSubflow
        ? subflowName
        : linkName
          ? `${meta?.label || data.nodeType}: ${linkName}`
          : meta?.label || data.nodeType)
  );
  let icon = $derived(missing ? 'las la-exclamation-triangle' : meta?.icon || 'las la-cube');
  // tint is the author's chosen card color, stored on the node like _label.
  // Unknown names simply match no tint class, so a hand-edited value cannot
  // inject styling.
  let tint = $derived(typeof data.config._color === 'string' ? data.config._color : '');
  // rotation is the angle the author turned the card to, stored beside the
  // color, and sides is which way each of its edges now faces.
  let rotation = $derived(nodeRotation(data.config));
  let sides = $derived(handleSides(rotation));

  // Turning the card moves its sockets without changing the size Svelte Flow
  // re-measures on its own, so the edges only follow once the node internals
  // are refreshed; a changed port list moves them the same way when the card's
  // measured box happens to stay the same size. The first pass is skipped: an
  // unturned node measures the same as it always did.
  const updateInternals = useUpdateNodeInternals();
  let measuredRotation = 0;
  let measuredPorts = '';
  $effect(() => {
    const portKey = [...ports.inputs, ...ports.outputs].map((p) => p.id).join('|');
    if (rotation === measuredRotation && (measuredPorts === '' || portKey === measuredPorts)) {
      measuredPorts = portKey;
      return;
    }
    measuredRotation = rotation;
    measuredPorts = portKey;
    updateInternals(id);
  });

  // Return sockets live on the bottom edge. A wire into one completes the
  // request that started the branch, rather than starting the node again.
  let bottomInputs = $derived(ports.inputs.filter((p) => p.kind === 'return'));
  let inputs = $derived(ports.inputs.filter((p) => p.kind !== 'return'));
  let outputs = $derived(
    ports.outputs.map((p) => (carried[p.id] ? { ...p, type: carried[p.id] } : p))
  );
  let problem = $derived($editor.problems?.[`node:${id}`]);

  // A run starts at a trigger, so the trigger node carries the button that
  // starts a test one. EditorShell decides which nodes qualify; an entry here
  // means this node does, and its value is the reason it cannot run right now.
  let testBlocked = $derived($testTargets[id]);
  let canTestRun = $derived(testBlocked !== undefined);

  // Run overlay: the latest run state for this node drives a status ring.
  // While drilled into a subflow, event node IDs carry the drill prefix.
  let ns = $derived($runState.nodeStates[$editor.runPrefix + id]);

  // Subflow nodes show an aggregate of their namespaced child states:
  // running while any child is active, failed on any unhandled failure,
  // succeeded once every observed child has settled.
  let aggregate = $derived.by(() => {
    if (!isSubflow) return undefined;
    const prefix = $editor.runPrefix + id + '/';
    const states = Object.entries($runState.nodeStates)
      .filter(([key]) => key.startsWith(prefix))
      .map(([, v]) => v);
    if (states.length === 0) return undefined;
    if (states.some((s) => s.state === 'running' || s.state === 'retrying')) return 'running';
    if (states.some((s) => s.state === 'failed' && !s.handled)) return 'failed';
    return 'succeeded';
  });

  let runClass = $derived.by(() => {
    if (isSubflow) return aggregate ? `run-${aggregate}` : '';
    return ns ? `run-${ns.state}` + (ns.state === 'failed' && ns.handled ? ' run-handled' : '') : '';
  });
  // maxAttempts reflects the effective retry policy: node override, else the
  // type default. Unknown policies show a bare attempt count.
  let maxAttempts = $derived(data.retry?.max_attempts ?? meta?.default_retry?.max_attempts);
  // During "retrying" the shown try is the upcoming attempt.
  let tryNumber = $derived(ns ? (ns.state === 'retrying' ? ns.attempt + 1 : ns.attempt) : 0);
  let showAttempts = $derived(!!ns && (ns.state === 'retrying' || tryNumber > 1));

  // portStyle carries a port's type color to its badge and socket dot as a CSS
  // variable, so the styles below can defer to their neutral defaults when the
  // port is untyped.
  function portStyle(port: { type?: string }): string | undefined {
    const c = portTypeColor(port.type);
    return c ? `--port-type-color: ${c}` : undefined;
  }
</script>

<div
  class="flow-node {tint ? `tint-${tint}` : ''} {runClass}"
  class:selected
  class:has-problem={!!problem}
  class:missing-type={missing}
  class:subflow-node={isSubflow}
  class:bypassed={!!data.bypass}
  class:collapsed={!!data.collapsed}
  class:rotated={rotation !== 0}
  style={rotation ? `--node-rotation: ${rotation}deg` : undefined}
  title={missingNote ||
    (data.bypass
      ? 'Bypassed: the input passes straight through to the output'
      : problem || (ns?.error ? ns.error : meta?.description || ''))}
>
  <div class="node-header">
    <i class={icon}></i>
    <span class="node-label">{label}</span>
    <!-- Header actions and marks sit together at the trailing edge of the
         title band, so the run button lands at the node's right edge rather
         than crowding the label. -->
    <div class="node-actions">
      {#if (data.nodeType === 'ai-agent' || data.nodeType === 'ai-basic-chain') && !$editor.canvasReadOnly && hasPermission('api:flows:write')}
        <button type="button" class="chat-btn nodrag nopan" title="Test this node in chat"
          aria-label={`Chat with ${label}`} disabled={missing || !!data.bypass}
          onclick={(e) => { e.stopPropagation(); editor.requestNodeChat(id); }}
          ondblclick={(e) => e.stopPropagation()}
          oncontextmenu={(e) => { e.preventDefault(); e.stopPropagation(); }}>
          Chat
        </button>
      {/if}
      {#if canTestRun}
        <button
          class="run-btn"
          disabled={!!testBlocked}
          title={testBlocked || 'Test run from this trigger (alt-click or right-click to choose a payload)'}
          aria-label="Test run from this trigger"
          onclick={(e: MouseEvent) => {
            e.stopPropagation();
            editor.requestTestRun(id, e.altKey);
          }}
          oncontextmenu={(e: MouseEvent) => {
            // The canvas menu would otherwise cover the button; a right-click
            // here means "run, but let me pick the payload".
            e.preventDefault();
            e.stopPropagation();
            if (!testBlocked) editor.requestTestRun(id, true);
          }}
        >
          <i class="las la-play"></i>
        </button>
      {/if}
      {#if data.retry}
        <i class="las la-redo-alt retry-mark" title="Retry override set"></i>
      {/if}
      {#if data.bypass}
        <i class="las la-forward bypass-mark" title="Bypassed: the input passes straight through to the output"></i>
      {/if}
      {#if isSubflow}
        <button
          class="drill-btn"
          title={data.subgraphId ? 'Edit subflow' : 'View subflow'}
          aria-label="Open subflow"
          onclick={(e: MouseEvent) => {
            e.stopPropagation();
            editor.requestDrill(id);
          }}
        >
          <i class="las la-layer-group"></i>
        </button>
      {/if}
    </div>
  </div>
  {#if showAttempts}
    <span class="attempt-badge" class:retrying={ns.state === 'retrying'}>
      try {tryNumber}{maxAttempts ? `/${maxAttempts}` : ''}
    </span>
  {/if}
  <div class="node-body">
    <div class="ports inputs">
      {#each inputs as port (port.id)}
        <div
          class="port"
          class:optional-port={port.optional}
          class:param-port={port.kind === 'param'}
          style={portStyle(port)}
        >
          <Handle
            type="target"
            position={sides.input}
            id={port.id}
            class={port.kind === 'param' ? 'param-handle' : port.optional ? 'optional-handle' : ''}
          />
          <span
            class="port-label"
            title={port.optional
              ? 'Optional input: a message here runs the node on its own, without waiting for the other inputs.'
              : undefined}>{port.label}</span
          >
          {#if port.type && port.type !== PortAny}
            <span class="port-type" title={`Carries a ${portTypeLabel(port.type).toLowerCase()}`}>
              {portTypeLabel(port.type)}
            </span>
          {/if}
        </div>
      {/each}
    </div>
    <div class="ports outputs">
      {#each outputs as port (port.id)}
        <div class="port" class:error-port={port.kind === 'error'} style={portStyle(port)}>
          {#if port.type && port.type !== PortAny}
            <span class="port-type" title={`Carries a ${portTypeLabel(port.type).toLowerCase()}`}>
              {portTypeLabel(port.type)}
            </span>
          {/if}
          <span class="port-label">{port.label}</span>
          <Handle
            type="source"
            position={sides.output}
            id={port.id}
            class={port.kind === 'error' ? 'error-handle' : ''}
          />
        </div>
      {/each}
    </div>
  </div>
  {#if bottomInputs.length}
    <div class="ports bottom">
      {#each bottomInputs as port (port.id)}
        <div class="port" style={portStyle(port)}>
          <span
            class="port-label"
            title="Return input: wire the branch result here to answer the request."
            >{port.label}</span
          >
          <Handle type="target" position={sides.bottom} id={port.id} class="return-handle" />
        </div>
      {/each}
    </div>
  {/if}
  {#if selected && !$editor.canvasReadOnly}
    <RotateGrips nodeId={id} {rotation} />
  {/if}
</div>

<style>
  .flow-node {
    min-width: 150px;
    /* Radius of the card's inner edge: children hugging the border use this. */
    --node-inner-radius: calc(var(--theme-radius-md, 8px) - 1px);
    border-radius: var(--theme-radius-md, 8px);
    /* The tint reads over the whole card. Every node starts neutral; the author picks a
       color from the context menu, and the tint classes below set it. */
    --cat-color: var(--theme-overlay0, #6c7086);
    background-color: color-mix(in srgb, var(--cat-color, var(--theme-accent)) 12%, var(--theme-surface0, #313244));
    border: 1px solid color-mix(in srgb, var(--cat-color, var(--theme-accent)) 35%, var(--theme-surface1, #45475a));
    font-size: 0.75rem;
    color: var(--theme-text, #cdd6f4);
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.3);
  }

  /* Author-picked card colors, chosen from the node context menu. The names
     are the contract with EditorShell's swatch row. Declared before the
     bypassed rule so a bypassed node reads mauve whatever its color. */
  .flow-node.tint-red { --cat-color: var(--theme-error, #f38ba8); }
  .flow-node.tint-peach { --cat-color: var(--theme-peach, #fab387); }
  .flow-node.tint-yellow { --cat-color: var(--theme-warning, #f9e2af); }
  .flow-node.tint-green { --cat-color: var(--theme-success, #a6e3a1); }
  .flow-node.tint-teal { --cat-color: var(--theme-info, #94e2d5); }
  .flow-node.tint-blue { --cat-color: var(--theme-accent, #89b4fa); }
  .flow-node.tint-mauve { --cat-color: var(--theme-mauve, #cba6f7); }
  .flow-node.tint-pink { --cat-color: var(--theme-pink, #f5c2e7); }

  .flow-node.selected {
    border-color: var(--theme-accent, #89b4fa);
    box-shadow: 0 0 0 1px var(--theme-accent, #89b4fa);
  }

  /* Subflow nodes: stacked double-border card look. */
  .flow-node.subflow-node {
    --node-inner-radius: calc(var(--theme-radius-md, 8px) - 4px);
    border-style: double;
    border-width: 4px;
    box-shadow:
      3px 3px 0 0 var(--theme-surface1, #45475a),
      6px 6px 0 0 var(--theme-surface0, #313244),
      0 2px 6px rgba(0, 0, 0, 0.3);
  }

  /* One trailing group keeps a single auto margin in the header: several
     would each take a share of the free space and split the marks apart. */
  .node-actions {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    margin-left: auto;
  }

  .chat-btn { padding: 1px 5px; border: 1px solid var(--theme-surface2); border-radius: 4px; font-size: .65rem; font-weight: 500; cursor: pointer; }
  .chat-btn:hover:not(:disabled) { color: var(--theme-accent); border-color: var(--theme-accent); }
  .chat-btn:disabled { opacity: .5; cursor: not-allowed; }

  .drill-btn {
    padding: 0 2px;
    background: none;
    border: none;
    color: var(--theme-text, #cdd6f4);
    opacity: 0.55;
    cursor: pointer;
    font-size: 0.9rem;
    line-height: 1;
  }

  .drill-btn:hover {
    opacity: 1;
    color: var(--theme-accent, #89b4fa);
  }

  /* The trigger's own run button, at the trailing edge of the title band. */
  .run-btn {
    padding: 0 2px;
    background: none;
    border: none;
    cursor: pointer;
    line-height: 1;
  }

  .run-btn i {
    font-size: 0.85rem !important;
    color: var(--theme-success, #a6e3a1);
    opacity: 0.75;
  }

  .run-btn:hover:not(:disabled) i {
    opacity: 1;
  }

  .run-btn:disabled {
    cursor: not-allowed;
  }

  .run-btn:disabled i {
    color: var(--theme-overlay0, #6c7086);
    opacity: 0.6;
  }

  .flow-node.has-problem {
    border-color: var(--theme-error, #f38ba8);
    box-shadow: 0 0 0 2px var(--theme-error, #f38ba8);
  }

  /* A missing type is drawn as absent rather than misconfigured: the same
     error color as a problem, but a dashed border, since there is nothing on
     the node itself to fix. */
  .flow-node.missing-type {
    border-color: var(--theme-error, #f38ba8);
    border-style: dashed;
    box-shadow: 0 0 0 1px var(--theme-error, #f38ba8);
  }

  /* ---- Run overlay status rings ---- */

  .flow-node {
    position: relative;
    transition: opacity 200ms, box-shadow 200ms;
  }

  .flow-node.run-running {
    border-color: var(--theme-accent, #89b4fa);
    animation: run-pulse-accent 1.1s ease-in-out infinite;
  }

  .flow-node.run-retrying {
    border-color: var(--theme-warning, #f9e2af);
    animation: run-pulse-warn 1.1s ease-in-out infinite;
  }

  .flow-node.run-succeeded {
    border-color: var(--theme-success, #a6e3a1);
    box-shadow: 0 0 0 2px var(--theme-success, #a6e3a1);
  }

  .flow-node.run-failed {
    border-color: var(--theme-error, #f38ba8);
    box-shadow: 0 0 0 2px var(--theme-error, #f38ba8);
  }

  /* A handled failure was routed to an error branch: outline variant. */
  .flow-node.run-failed.run-handled {
    border-color: var(--theme-warning, #fab387);
    box-shadow: 0 0 0 2px var(--theme-warning, #fab387), inset 0 0 0 1px var(--theme-error, #f38ba8);
  }

  /* Skipped draws no overlay: dimming is what a bypassed node looks like, and
     a skipped state lingering from the last run must not read as bypass. */

  /* Selection and problems win over the run ring for clarity. */
  .flow-node.selected {
    border-color: var(--theme-accent, #89b4fa);
    box-shadow: 0 0 0 1px var(--theme-accent, #89b4fa);
  }

  @keyframes run-pulse-accent {
    0%, 100% { box-shadow: 0 0 0 2px var(--theme-accent, #89b4fa); }
    50% { box-shadow: 0 0 0 5px rgba(137, 180, 250, 0.35); }
  }

  @keyframes run-pulse-warn {
    0%, 100% { box-shadow: 0 0 0 2px var(--theme-warning, #f9e2af); }
    50% { box-shadow: 0 0 0 5px rgba(249, 226, 175, 0.35); }
  }

  .attempt-badge {
    position: absolute;
    top: -0.65rem;
    right: -0.5rem;
    padding: 0.05rem 0.4rem;
    border-radius: 999px;
    font-size: 0.62rem;
    font-weight: 700;
    background-color: var(--theme-surface1, #45475a);
    color: var(--theme-text, #cdd6f4);
    border: 1px solid var(--theme-overlay0, #6c7086);
    white-space: nowrap;
  }

  .attempt-badge.retrying {
    background-color: var(--theme-warning, #f9e2af);
    color: var(--theme-crust, #11111b);
    border-color: var(--theme-warning, #f9e2af);
  }

  .node-header {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.35rem 0.6rem;
    font-weight: 600;
    /* The title band carries the tint more strongly than the body. It sits
       inside the card border, so its corners follow the card's inner radius —
       matching it keeps the card border from appearing doubled. */
    background-color: color-mix(in srgb, var(--cat-color, var(--theme-accent)) 22%, var(--theme-surface0, #313244));
    border-bottom: 1px solid color-mix(in srgb, var(--cat-color, var(--theme-accent)) 35%, var(--theme-surface1, #45475a));
    border-radius: var(--node-inner-radius) var(--node-inner-radius) 0 0;
  }

  .node-header i {
    font-size: 1rem;
    color: var(--cat-color, var(--theme-accent));
  }

  .retry-mark {
    opacity: 0.7;
    font-size: 0.8rem !important;
  }

  .bypass-mark {
    color: var(--theme-mauve, #cba6f7);
    font-size: 0.8rem !important;
  }

  /* A bypassed node stays wired but nothing runs in it: dimmed, dashed, and
     recolored mauve so it reads as wiring rather than a step. */
  .flow-node.bypassed {
    --cat-color: var(--theme-mauve, #cba6f7);
    opacity: 0.55;
    border-style: dashed;
  }

  /* A collapsed node keeps only its title bar, sized to the title alone. The
     port rows shrink to zero rather than unmounting, so the handles Svelte
     Flow anchors edges to stay in the DOM: every wire converges on a single
     socket per side. Expanding
     is a context-menu action, so the header carries no extra control. */
  .flow-node.collapsed {
    min-width: 0;
    width: max-content;
  }

  .flow-node.collapsed .node-header {
    border-bottom: none;
    border-radius: var(--node-inner-radius);
  }

  .flow-node.collapsed .node-body {
    padding: 0;
    min-height: 0;
    gap: 0;
  }

  .flow-node.collapsed .port {
    min-height: 0;
    height: 0;
    padding: 0;
  }

  .flow-node.collapsed .port-label,
  .flow-node.collapsed .port-type {
    display: none;
  }

  .node-body {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.3rem 0;
    min-height: 1.4rem;
  }

  .ports {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }

  .port {
    position: relative;
    display: flex;
    align-items: center;
    min-height: 1.25rem;
  }

  .inputs .port {
    padding-left: 0.6rem;
  }

  .outputs .port {
    justify-content: flex-end;
    padding-right: 0.6rem;
  }

  /* A declared payload type, shown beside the port so the graph reads like a
     signature. Untyped message ports render nothing, which keeps the common
     node looking exactly as it did. The pill takes the type's rotating-hue
     color, matching the edges that will carry it. */
  .port-type {
    font-size: 0.6rem;
    font-weight: 600;
    letter-spacing: 0.02em;
    padding: 0 0.25rem;
    border-radius: 3px;
    color: var(--port-type-color, var(--theme-mauve, #cba6f7));
    background-color: color-mix(
      in srgb,
      var(--port-type-color, var(--theme-mauve, #cba6f7)) 16%,
      transparent
    );
    white-space: nowrap;
  }

  .port-label {
    opacity: 0.75;
    font-size: 0.68rem;
  }

  .error-port .port-label {
    color: var(--theme-error, #f38ba8);
    opacity: 1;
  }

  /* Optional inputs do not gate the node, so they read as a lighter commitment
     than a normal input: dimmer label, hollow handle. */
  .optional-port .port-label {
    opacity: 0.55;
    font-style: italic;
  }

  .port :global(.optional-handle) {
    background-color: var(--theme-base, #1e1e2e) !important;
    border-color: var(--theme-overlay0, #6c7086) !important;
  }

  /* The handle is a large transparent grab area with a small dot drawn in the
     middle. A 9px socket is close to impossible to hit, and a miss lands on the
     pane, which pans the canvas instead of starting a connection. Enlarging
     symmetrically keeps the element's centre where it was, so edge endpoints
     still meet the dot. */
  .port :global(.svelte-flow__handle) {
    width: 22px;
    height: 22px;
    background-color: transparent;
    border: none;
    border-radius: 50%;
  }

  .port :global(.svelte-flow__handle)::after {
    content: '';
    position: absolute;
    top: 50%;
    left: 50%;
    width: 9px;
    height: 9px;
    border-radius: 50%;
    transform: translate(-50%, -50%);
    /* The socket dot takes the port's type color, like the edge it anchors. */
    background-color: var(--port-type-color, var(--theme-overlay0, #6c7086));
    border: 1px solid var(--theme-crust, #11111b);
    transition: background-color 120ms, transform 120ms;
  }

  .port :global(.svelte-flow__handle:hover)::after {
    background-color: var(--theme-accent, #89b4fa);
    transform: translate(-50%, -50%) scale(1.3);
  }

  .port :global(.svelte-flow__handle.error-handle)::after {
    background-color: var(--theme-error, #f38ba8);
  }

  /* While a connection is being dragged, Svelte Flow marks the handles that
     would accept it. Growing them makes the valid drop targets obvious. */
  .port :global(.svelte-flow__handle.connectingto)::after,
  .port :global(.svelte-flow__handle.valid)::after {
    background-color: var(--theme-success, #a6e3a1);
    transform: translate(-50%, -50%) scale(1.5);
  }

  /* ---- Rotation ---- */

  /* The card turns about its own centre. Only the drawing turns: the node's
     measured box, and so its position and size on the canvas, are the ones it
     always had. */
  .flow-node.rotated {
    transform: rotate(var(--node-rotation, 0deg));
  }

  /* Sockets stay on the card's own edges however far it is turned, whatever
     Position the handle declares — the declared position aims the wire, it
     does not place the socket. Counter-turning the handle keeps its box square
     to the screen, which is what Svelte Flow measures the edge endpoint from,
     so an endpoint meets its dot at every angle. */
  .inputs .port :global(.svelte-flow__handle) {
    top: 50%;
    bottom: auto;
    left: 0;
    right: auto;
    transform: translate(-50%, -50%) rotate(calc(-1 * var(--node-rotation, 0deg)));
  }

  .outputs .port :global(.svelte-flow__handle) {
    top: 50%;
    bottom: auto;
    left: auto;
    right: 0;
    transform: translate(50%, -50%) rotate(calc(-1 * var(--node-rotation, 0deg)));
  }

  /* A request node's return sockets sit centred on the bottom edge, where
     the wire coming back from the branch visibly closes the loop. The handle
     straddles the card's bottom border the way the side sockets straddle
     theirs, and counter-turns for the same measurement reason. The row, not
     the padded port box, is the handle's positioned ancestor: the row's
     bottom is the card's bottom, so bottom: 0 lands the dot on the border.
     Padding moves only the in-flow label, never the absolute handle. */
  .ports.bottom {
    position: relative;
    flex-direction: row;
    justify-content: center;
    padding-bottom: 0.35rem;
  }

  .ports.bottom .port {
    position: static;
    justify-content: center;
  }

  .ports.bottom .port .port-label {
    opacity: 0.55;
    font-style: italic;
  }

  .ports.bottom .port :global(.svelte-flow__handle) {
    top: auto;
    bottom: 0;
    left: 50%;
    right: auto;
    transform: translate(-50%, 50%) rotate(calc(-1 * var(--node-rotation, 0deg)));
  }
</style>
