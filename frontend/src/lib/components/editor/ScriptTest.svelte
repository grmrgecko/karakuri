<script lang="ts">
  import Modal from '../Modal.svelte';
  import CodeSurface from './CodeSurface.svelte';
  import type { ScriptTest, ScriptTestResult } from './scriptTest';

  // ScriptTest runs a script against a sample input and shows what it printed
  // beside what it returned. The sample is one JSON document rather than a set
  // of fields: what a script reads is a message, and a message is a document —
  // typing it as such is quicker and closer to what will actually arrive.
  let {
    test,
    source,
    onclose,
  }: {
    test: ScriptTest;
    // source is read at run time rather than at open, so editing the script and
    // running again does not mean closing and reopening.
    source: () => string;
    onclose: () => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  let sample = $state(test.sample ?? '{}');
  let running = $state(false);
  let result = $state<ScriptTestResult | null>(null);
  let failure = $state('');

  async function run() {
    let parsed: unknown;
    try {
      parsed = sample.trim() === '' ? {} : JSON.parse(sample);
    } catch (e: any) {
      // A malformed sample is the author's typing, not the script's fault, so
      // it is reported here rather than sent to be run.
      failure = `The sample is not valid JSON: ${e?.message || e}`;
      result = null;
      return;
    }

    running = true;
    failure = '';
    result = null;
    try {
      result = await test.run(source(), parsed);
    } catch (e: any) {
      failure = e?.message || 'The test could not run';
    } finally {
      running = false;
    }
  }

  function pretty(value: unknown): string {
    if (value === undefined) return 'nothing';
    return JSON.stringify(value, null, 2);
  }
</script>

<Modal title={test.title ?? 'Test the script'} {onclose} maxWidth="2xl" maxHeight="86vh">
  <div class="test-body">
    <p class="test-hint">
      {test.hint ?? 'The sample below is what the script receives. Nothing is saved or sent.'}
    </p>

    <div class="test-sample">
      <CodeSurface value={sample} language="json" autofocus onchange={(next) => (sample = next)} />
    </div>

    {#if failure}
      <p class="test-failure">{failure}</p>
    {/if}

    {#if result}
      {#if result.error}
        <p class="test-section">Failed</p>
        <pre class="test-out test-failed">{result.error}</pre>
      {/if}
      {#if result.output?.length}
        <p class="test-section">Output</p>
        <pre class="test-out">{result.output.join('\n')}</pre>
      {/if}
      {#if !result.error}
        <p class="test-section">Returned</p>
        <pre class="test-out">{pretty(result.value)}</pre>
      {/if}
    {/if}

    <div class="test-actions">
      <button type="button" class="btn" onclick={onclose}>Close</button>
      <button type="button" class="btn btn-primary" onclick={run} disabled={running}>
        {running ? 'Running...' : 'Run'}
      </button>
    </div>
  </div>
</Modal>

<style>
  .test-body {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-height: 0;
  }

  .test-hint {
    font-size: 12px;
    opacity: 0.7;
  }

  /* The sample gets a fixed slice of the modal so a long result does not
     squeeze the thing being edited down to a line. */
  .test-sample {
    display: flex;
    height: 30vh;
    min-height: 120px;
  }

  .test-section {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    opacity: 0.6;
  }

  .test-out {
    margin: 0;
    padding: 8px 10px;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-crust, #11111b);
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
    line-height: 1.5;
    max-height: 24vh;
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .test-failed {
    color: var(--theme-error, #f38ba8);
  }

  .test-failure {
    font-size: 12px;
    color: var(--theme-error, #f38ba8);
  }

  .test-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 4px;
  }
</style>
