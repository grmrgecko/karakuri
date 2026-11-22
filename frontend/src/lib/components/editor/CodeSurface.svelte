<script lang="ts">
  import { onMount } from 'svelte';
  import { EditorView, keymap } from '@codemirror/view';
  import { EditorState, Compartment } from '@codemirror/state';
  import { basicSetup } from 'codemirror';
  import { indentWithTab } from '@codemirror/commands';
  import { languageExtensions, scriptTheme, scriptSyntaxHighlighting } from '../../utils/scriptLang';

  // CodeSurface is one CodeMirror instance and nothing else — no modal, no
  // toolbar, no save. It exists so the script editor and anything else that
  // needs code input share one setup: the same theme, the same highlighting,
  // and the same behaviour under Tab.
  let {
    value = '',
    language = 'plain',
    // extraExtensions is for the keymaps a host owns — saving, closing — which
    // belong to whatever wraps this rather than to the surface.
    extraExtensions = [] as any[],
    autofocus = false,
    onchange,
    onready,
  }: {
    value?: string;
    language?: string;
    extraExtensions?: any[];
    autofocus?: boolean;
    onchange?: (next: string) => void;
    // onready hands back the view, for a host that drives it (find & replace,
    // reading the document at save time).
    onready?: (view: EditorView) => void;
  } = $props();

  let host = $state<HTMLDivElement | null>(null);
  let view: EditorView | null = null;

  // The language lives in a compartment so it can be swapped without rebuilding
  // the editor and losing undo history or cursor position.
  const languageConf = new Compartment();

  onMount(() => {
    if (!host) return;
    view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          ...extraExtensions,
          basicSetup,
          keymap.of([indentWithTab]),
          languageConf.of(languageExtensions(language)),
          scriptTheme,
          scriptSyntaxHighlighting,
          EditorView.lineWrapping,
          EditorView.updateListener.of((update) => {
            if (update.docChanged) onchange?.(update.state.doc.toString());
          }),
        ],
      }),
    });
    onready?.(view);
    if (autofocus) view.focus();

    return () => {
      view?.destroy();
      view = null;
    };
  });

  $effect(() => {
    const next = language;
    view?.dispatch({ effects: languageConf.reconfigure(languageExtensions(next)) });
  });
</script>

<div class="code-surface" bind:this={host}></div>

<style>
  .code-surface {
    flex: 1;
    min-height: 0;
    overflow: hidden;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-crust, #11111b);
  }
</style>
