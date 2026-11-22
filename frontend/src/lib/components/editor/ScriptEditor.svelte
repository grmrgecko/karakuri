<script lang="ts">
  import { onMount, type Snippet } from 'svelte';
  import { EditorView, keymap } from '@codemirror/view';
  import { EditorState, Compartment } from '@codemirror/state';
  import { basicSetup } from 'codemirror';
  import { indentWithTab } from '@codemirror/commands';
  import { openSearchPanel } from '@codemirror/search';
  import { setDiagnostics } from '@codemirror/lint';
  import Modal from '../Modal.svelte';
  import GuideDocs from '../GuideDocs.svelte';
  import ScriptTest from './ScriptTest.svelte';
  import type { ScriptTest as ScriptTestSpec } from './scriptTest';
  import type { GuideRef } from '../../stores/guides';
  import { scriptSdkCatalog } from '../../stores/scriptSdkCatalog';
  import { generateScriptSdkDeclarations } from './scriptSdk';
  import type { ScriptAnalysisClient } from './scriptAnalysisClient';
  import type { ScriptVerificationClient } from './scriptVerificationClient';
  import { languageLabel } from '../../utils/highlight';
  import { languageExtensions, scriptTheme, scriptSyntaxHighlighting } from '../../utils/scriptLang';

  let {
    title,
    language,
    value,
    onsave,
    onclose,
    saveLabel = 'Save script',
    hint = 'Ctrl+Space completes · Ctrl+Enter saves',
    docs = null,
    test = null,
    scriptAnalysis = false,
    validate,
    toolbar,
    preview,
    previewing = false,
    children,
    onchange,
    onfiles,
    saveDisabled = false,
  }: {
    title: string;
    language: string;
    value: string;
    onsave: (next: string) => void;
    onclose: () => void;
    // saveLabel and hint name what is being edited: the same editor holds a
    // script and a test payload, and the payload has no completions to offer.
    saveLabel?: string;
    hint?: string;
    // docs names the reference page to offer alongside the editor, or null for
    // a field with nothing to explain. It is the caller's choice rather than
    // the editor's: the same editor holds a sandbox script and a device's
    // command payload, and each has its own page.
    docs?: GuideRef | null;
    // test offers a Test button beside Cancel. The field supplies what a sample
    // looks like and how to run it, because the editor holds text and knows
    // nothing about what the text is for.
    test?: ScriptTestSpec | null;
    // scriptAnalysis is true only for a supported language in the server flow script node.
    scriptAnalysis?: boolean;
    // validate rejects a document before it is saved, returning the reason.
    // The editor stays open with the message shown, so text that does not
    // parse is never silently dropped on close.
    validate?: (next: string) => string | null;
    toolbar?: Snippet;
    preview?: Snippet;
    previewing?: boolean;
    children?: Snippet;
    onchange?: (next: string) => void;
    onfiles?: (files: File[]) => void;
    saveDisabled?: boolean;
  } = $props();

  let host = $state<HTMLDivElement | null>(null);
  let view: EditorView | null = null;
  let lineCount = $state(1);
  let saveError = $state('');
  let docsOpen = $state(false);
  let testOpen = $state(false);
  let editorReady = $state(false);
  let analysisCatalogKey = $derived($scriptSdkCatalog.loaded ? $scriptSdkCatalog.revision : '');
  let analysisClient: ScriptAnalysisClient | null = null;
  let analysisVerification: ScriptVerificationClient | null = null;
  let analysisGeneration = 0;

  // The language mode lives in a compartment so it can be swapped without
  // rebuilding the editor and losing undo history or cursor position.
  const languageConf = new Compartment();
  const analysisConf = new Compartment();

  // Ctrl/Cmd+Enter saves from anywhere in the document. It is bound ahead of
  // the default keymap so the editor never inserts a newline instead.
  const saveKeymap = keymap.of([
    {
      key: 'Mod-Enter',
      preventDefault: true,
      run: () => {
        save();
        return true;
      },
    },
  ]);

  // Escape closes the editor, but only once CodeMirror has had its turn: the
  // completion popup and the search panel both bind Escape and report it
  // handled. Registering below basicSetup means those run first and this only
  // fires when neither is open. Closing is deferred so the view is not
  // destroyed from inside its own key handling.
  const escapeKeymap = keymap.of([
    {
      key: 'Escape',
      run: () => {
        setTimeout(onclose, 0);
        return true;
      },
    },
  ]);

  function clearAnalysis(preserveVerification = false) {
    analysisGeneration++;
    const client = analysisClient;
    analysisClient = null;
    client?.dispose();
    if (!preserveVerification) {
      const verification = analysisVerification;
      analysisVerification = null;
      verification?.dispose();
    }
    view?.dispatch({ effects: analysisConf.reconfigure([]) });
    if (!preserveVerification && view) view.dispatch(setDiagnostics(view.state, []));
  }

  async function configureAnalysis() {
    // Keep one queue while Python/Bash reconfigure so an old request finishes before the newest starts.
    const preserveVerification = !!analysisVerification && scriptAnalysis && ['python', 'bash'].includes(language);
    clearAnalysis(preserveVerification);
    const generation = analysisGeneration;
    if (!view || !editorReady || !scriptAnalysis || !['javascript', 'python', 'bash'].includes(language)) return;

    try {
      await scriptSdkCatalog.load();
      if (generation !== analysisGeneration || !view) return;
      const catalog = scriptSdkCatalog.snapshot();
      if (!catalog.loaded) return;

      if (language === 'javascript') {
        const declarations = generateScriptSdkDeclarations(catalog.functions);
        const [clientModule, codeMirrorModule] = await Promise.all([
          import('./scriptAnalysisClient'),
          import('./scriptAnalysisCodeMirror'),
        ]);
        if (generation !== analysisGeneration || !view) return;

        const client = new clientModule.ScriptAnalysisClient({
          onFailure: () => {
            if (analysisClient !== client) return;
            analysisClient = null;
            client.dispose();
            view?.dispatch({ effects: analysisConf.reconfigure([]) });
          },
        });
        client.setCatalog(catalog.revision, declarations);
        analysisClient = client;
        view.dispatch({
          effects: analysisConf.reconfigure(codeMirrorModule.scriptAnalysisExtensions(client)),
        });
        return;
      }

      const catalogLanguage = language as 'python' | 'bash';
      const [analysisModule, codeMirrorModule, verificationModule] = await Promise.all([
        import('./scriptCatalogAnalysis'),
        import('./scriptCatalogCodeMirror'),
        import('./scriptVerificationClient'),
      ]);
      if (generation !== analysisGeneration || !view) return;
      const analysis = new analysisModule.ScriptCatalogAnalysis(catalogLanguage, catalog.functions);
      const verification = analysisVerification ?? new verificationModule.ScriptVerificationClient();
      analysisVerification = verification;
      view.dispatch({
        effects: analysisConf.reconfigure(
          codeMirrorModule.scriptCatalogAnalysisExtensions(analysis, verification, catalog.revision),
        ),
      });
    } catch {
      // The existing syntax and sandbox completions remain available without
      // catalog analysis or a current function catalog.
      if (generation === analysisGeneration) {
        const verification = analysisVerification;
        analysisVerification = null;
        verification?.dispose();
        view?.dispatch({ effects: analysisConf.reconfigure([]) });
      }
    }
  }

  onMount(() => {
    if (!host) return;

    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          saveKeymap,
          EditorView.domEventHandlers({
            paste: (event) => {
              if (!onfiles || !event.clipboardData?.files.length) return false;
              event.preventDefault();
              onfiles(Array.from(event.clipboardData.files));
              return true;
            },
            dragover: (event) => {
              if (!onfiles || !event.dataTransfer?.types.includes('Files')) return false;
              event.preventDefault();
              return true;
            },
            drop: (event) => {
              if (!onfiles || !event.dataTransfer?.files.length) return false;
              event.preventDefault();
              onfiles(Array.from(event.dataTransfer.files));
              return true;
            },
          }),
          basicSetup,
          analysisConf.of([]),
          escapeKeymap,
          keymap.of([indentWithTab]),
          languageConf.of(languageExtensions(language)),
          scriptTheme,
          scriptSyntaxHighlighting,
          EditorView.lineWrapping,
          EditorView.updateListener.of((update) => {
            if (!update.docChanged) return;
            lineCount = update.state.doc.lines;
            // The rejected document is being edited, so the complaint about it
            // no longer describes what is on screen.
            saveError = '';
            onchange?.(update.state.doc.toString());
          }),
        ],
      }),
    });
    lineCount = view.state.doc.lines;
    editorReady = true;
    view.focus();

    return () => {
      editorReady = false;
      clearAnalysis();
      view?.destroy();
      view = null;
    };
  });

  // Swap the mode when the node's language field changes while the editor is
  // open, keeping the document and history intact.
  $effect(() => {
    const next = language;
    view?.dispatch({ effects: languageConf.reconfigure(languageExtensions(next)) });
  });

  $effect(() => {
    // The primitive key avoids restarting a worker for loading-state updates.
    analysisCatalogKey;
    language;
    scriptAnalysis;
    editorReady;
    void configureAnalysis();
  });

  function save() {
    if (saveDisabled) return;
    if (!view) {
      onclose();
      return;
    }
    const next = view.state.doc.toString();
    const problem = validate?.(next) ?? null;
    if (problem) {
      saveError = problem;
      return;
    }
    onsave(next);
    onclose();
  }

  export function getValue(): string {
    return view?.state.doc.toString() ?? value;
  }

  export function getSelection(): { from: number; to: number } {
    return view?.state.selection.main ?? { from: value.length, to: value.length };
  }

  export function replaceSelection(text: string) {
    if (!view) return;
    const { from, to } = view.state.selection.main;
    view.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length }, userEvent: 'input', scrollIntoView: true });
    view.focus();
  }

  export function replaceDocument(text: string) {
    if (!view) return;
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text }, userEvent: 'input' });
  }

  // Escape is resolved inside CodeMirror by escapeKeymap, so it is swallowed
  // here unconditionally. Letting it reach the modal backdrop would close the
  // editor while the user was only dismissing a completion popup, dropping
  // focus onto the canvas where the next keystroke edits the graph.
  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') event.stopPropagation();
  }
</script>

<Modal {title} {onclose} maxWidth="wide" maxHeight="88vh">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="script-editor" {onkeydown}>
    {#if toolbar}{@render toolbar()}{/if}
    <div class="script-toolbar" class:hidden={previewing}>
      <span class="script-language">{languageLabel(language)}</span>
      <span class="script-lines">{lineCount} {lineCount === 1 ? 'line' : 'lines'}</span>
      <button
        type="button"
        class="script-tool"
        onclick={() => view && openSearchPanel(view)}
        title="Find and replace"
      >
        <i class="las la-search"></i> Find &amp; replace
      </button>
      {#if docs}
        <button
          type="button"
          class="script-tool"
          onclick={() => (docsOpen = true)}
          title="Open the reference for this field"
        >
          <i class="las la-book"></i> View docs
        </button>
      {/if}
      <span class="script-hint">{hint}</span>
    </div>

    <div class="script-surface" class:hidden={previewing} bind:this={host}></div>
    {#if previewing && preview}{@render preview()}{/if}
    {#if children}{@render children()}{/if}

    <div class="script-actions">
      {#if saveError}
        <span class="script-error">{saveError}</span>
      {/if}
      {#if test}
        <button type="button" class="btn" onclick={() => (testOpen = true)}>
          <i class="las la-vial"></i> Test
        </button>
      {/if}
      <button type="button" class="btn" onclick={onclose}>Cancel</button>
      <button type="button" class="btn btn-primary" disabled={saveDisabled} onclick={save}>{saveLabel}</button>
    </div>
  </div>
</Modal>

<!--
  The reference sits outside the editor's modal so its own Escape handling is
  not swallowed by the editor's, which stops the key from reaching the backdrop.
-->
{#if testOpen && test}
  <ScriptTest
    {test}
    source={() => view?.state.doc.toString() ?? value}
    onclose={() => (testOpen = false)}
  />
{/if}

{#if docsOpen && docs}
  <GuideDocs
    guideId={docs.guide}
    title={docs.title ?? ''}
    sections={docs.sections ?? []}
    current={docs.current ?? ''}
    onclose={() => (docsOpen = false)}
  />
{/if}

<style>
  .script-editor {
    display: flex;
    flex-direction: column;
    gap: 10px;
    flex: 1;
    min-height: 0;
  }

  .script-toolbar.hidden,
  .script-surface.hidden {
    display: none;
  }

  .script-toolbar {
    display: flex;
    align-items: center;
    gap: 12px;
    font-size: 11px;
  }

  .script-language {
    padding: 2px 8px;
    border-radius: 999px;
    background-color: var(--theme-surface1, #45475a);
    font-weight: 600;
  }

  .script-lines {
    opacity: 0.5;
  }

  .script-tool {
    padding: 2px 8px;
    border-radius: 4px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: transparent;
    color: inherit;
    font-size: 11px;
    cursor: pointer;
    opacity: 0.75;
  }

  .script-tool:hover {
    opacity: 1;
    border-color: var(--theme-accent, #89b4fa);
  }

  .script-hint {
    margin-left: auto;
    opacity: 0.45;
  }

  .script-surface {
    flex: 1;
    min-height: 420px;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-crust, #11111b);
    overflow: hidden;
  }

  /* CodeMirror renders into this container and manages its own scrolling. */
  .script-surface :global(.cm-editor) {
    height: 100%;
  }

  .script-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
  }

  .script-error {
    margin-right: auto;
    font-size: 12px;
    color: var(--theme-error, #f38ba8);
  }
</style>
