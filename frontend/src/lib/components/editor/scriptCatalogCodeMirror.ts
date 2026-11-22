import { pickedCompletion, type Completion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete';
import { setDiagnostics } from '@codemirror/lint';
import { EditorState, StateEffect, StateField, type Extension } from '@codemirror/state';
import {
  EditorView,
  hoverTooltip,
  keymap,
  showTooltip,
  ViewPlugin,
  type Tooltip,
  type ViewUpdate,
} from '@codemirror/view';
import type { ScriptCatalogAnalysis } from './scriptCatalogAnalysis';
import type {
  CatalogDocumentation,
  CatalogSignatureHelp,
} from './scriptCatalogProjection';
import type { ScriptVerificationClient } from './scriptVerificationClient';
import { withCompletionSource } from '../../utils/scriptLang';

function documentationNode(documentation: CatalogDocumentation): HTMLElement {
  const dom = document.createElement('div');
  dom.className = 'script-analysis-documentation';
  for (const [index, text] of [documentation.display, documentation.documentation, documentation.tags.join('\n')].entries()) {
    if (!text) continue;
    const section = document.createElement('div');
    section.className = index === 0 ? 'script-analysis-display' : 'script-analysis-text';
    section.textContent = text;
    dom.appendChild(section);
  }
  return dom;
}

function catalogCompletion(analysis: ScriptCatalogAnalysis) {
  return (context: CompletionContext): CompletionResult | null => {
    const result = analysis.completions(context.state.doc.toString(), context.pos);
    if (!result || result.options.length === 0) return null;
    return {
      from: result.from,
      to: result.to,
      options: result.options.map((option): Completion => ({
        label: option.filterText ?? option.label,
        displayLabel: option.filterText && option.filterText !== option.label ? option.label : undefined,
        type: option.type,
        detail: option.detail,
        apply: (view, completion, from, to) => {
          const cursor = Math.max(0, Math.min(option.cursor ?? option.insert.length, option.insert.length));
          view.dispatch({
            changes: { from, to, insert: option.insert },
            selection: { anchor: from + cursor },
            userEvent: 'input.completion',
            annotations: pickedCompletion.of(completion),
          });
        },
        info: () => documentationNode(option.documentation),
      })),
    };
  };
}

function catalogHover(analysis: ScriptCatalogAnalysis): Extension {
  return hoverTooltip((view, pos) => {
    const info = analysis.quickInfo(view.state.doc.toString(), pos);
    if (!info) return null;
    return {
      pos: info.from,
      end: info.to > info.from ? info.to : undefined,
      above: true,
      create: () => ({ dom: documentationNode(info) }),
    };
  });
}

function catalogDiagnostics(analysis: ScriptCatalogAnalysis, client: ScriptVerificationClient, catalogRevision: string): Extension {
  return ViewPlugin.fromClass(class {
    private generation = 0;
    private destroyed = false;

    constructor(view: EditorView) {
      this.load(view);
    }

    update(update: ViewUpdate) {
      if (update.docChanged) this.load(update.view);
    }

    load(view: EditorView) {
      const generation = ++this.generation;
      const source = view.state.doc.toString();
      const localDiagnostics = analysis.diagnostics(source);
      void client.verify(analysis.language, source, catalogRevision).then((diagnostics) => {
        if (this.destroyed || generation !== this.generation || view.state.doc.toString() !== source) return;
        const combined = [...localDiagnostics, ...diagnostics].sort((a, b) => a.from - b.from || a.code.localeCompare(b.code));
        view.dispatch(setDiagnostics(view.state, combined.map((diagnostic) => ({
          from: diagnostic.from,
          to: diagnostic.to,
          severity: diagnostic.severity,
          message: diagnostic.message,
          source: `Karakuri ${diagnostic.code}`,
        }))));
      });
    }

    destroy() {
      this.destroyed = true;
      this.generation++;
    }
  });
}

const setCatalogSignature = StateEffect.define<CatalogSignatureHelp | null>();
const catalogSignatureTooltip = StateField.define<Tooltip | null>({
  create: () => null,
  update(value, transaction) {
    for (const effect of transaction.effects) {
      if (effect.is(setCatalogSignature)) {
        const help = effect.value;
        if (!help) return null;
        return {
          pos: help.from,
          end: help.to,
          above: true,
          create: () => ({ dom: signatureNode(help) }),
        };
      }
    }
    if (transaction.docChanged || transaction.selection) return null;
    return value;
  },
  provide: (field) => showTooltip.from(field),
});

function signatureNode(help: CatalogSignatureHelp): HTMLElement {
  const dom = document.createElement('div');
  dom.className = 'cm-signature-help';
  const signature = document.createElement('div');
  signature.className = 'cm-signature-signature';
  signature.append(document.createTextNode(help.prefix));
  help.parameters.forEach((parameter, index) => {
    if (index > 0) signature.append(document.createTextNode(help.separator));
    const span = document.createElement('span');
    span.textContent = parameter.display;
    if (index === help.activeArgument) span.className = 'cm-signature-active';
    signature.appendChild(span);
  });
  signature.append(document.createTextNode(help.suffix));
  dom.appendChild(signature);

  const documentation = [
    help.parameters[help.activeArgument]?.documentation,
    help.documentation,
  ].filter(Boolean).join('\n\n');
  if (documentation) {
    const text = document.createElement('div');
    text.className = 'cm-signature-documentation';
    text.textContent = documentation;
    dom.appendChild(text);
  }
  return dom;
}

function catalogSignatures(analysis: ScriptCatalogAnalysis): Extension {
  const plugin = ViewPlugin.fromClass(class {
    private timer: ReturnType<typeof setTimeout> | null = null;
    private generation = 0;

    constructor(view: EditorView) {
      this.schedule(view);
    }

    update(update: ViewUpdate) {
      if (update.docChanged || update.selectionSet) this.schedule(update.view);
    }

    schedule(view: EditorView) {
      if (this.timer !== null) clearTimeout(this.timer);
      const generation = ++this.generation;
      this.timer = setTimeout(() => {
        this.timer = null;
        if (generation !== this.generation) return;
        const help = analysis.signatureHelp(view.state.doc.toString(), view.state.selection.main.head);
        view.dispatch({ effects: setCatalogSignature.of(help) });
      }, 60);
    }

    destroy() {
      this.generation++;
      if (this.timer !== null) clearTimeout(this.timer);
    }
  });

  const escape = keymap.of([{
    key: 'Escape',
    run: (view) => {
      if (!view.state.field(catalogSignatureTooltip)) return false;
      view.dispatch({ effects: setCatalogSignature.of(null) });
      return true;
    },
  }]);
  return [catalogSignatureTooltip, plugin, escape];
}

// scriptCatalogAnalysisExtensions adds flow node assistance without replacing
// the sandbox completions already attached to Python or Bash.
export function scriptCatalogAnalysisExtensions(
  analysis: ScriptCatalogAnalysis,
  verification: ScriptVerificationClient,
  catalogRevision: string,
): Extension[] {
  return [
    withCompletionSource(analysis.language, catalogCompletion(analysis)),
    catalogHover(analysis),
    catalogDiagnostics(analysis, verification, catalogRevision),
    catalogSignatures(analysis),
  ];
}
