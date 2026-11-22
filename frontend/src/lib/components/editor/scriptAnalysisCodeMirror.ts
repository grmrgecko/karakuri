import {
  pickedCompletion,
  type Completion,
  type CompletionContext,
  type CompletionResult,
} from '@codemirror/autocomplete';
import { javascriptLanguage } from '@codemirror/lang-javascript';
import { linter } from '@codemirror/lint';
import { StateEffect, StateField, type Extension } from '@codemirror/state';
import {
  EditorView,
  hoverTooltip,
  keymap,
  showTooltip,
  ViewPlugin,
  type Tooltip,
  type ViewUpdate,
} from '@codemirror/view';
import type {
  AnalysisCompletionDetails,
  AnalysisQuickInfo,
  AnalysisSignatureHelp,
} from './scriptAnalysisService';
import type { ScriptAnalysisClient } from './scriptAnalysisClient';

function completionType(kind: string): string {
  switch (kind) {
    case 'method':
    case 'memberFunctionElement': return 'method';
    case 'function':
    case 'localFunctionElement': return 'function';
    case 'property':
    case 'memberVariableElement':
    case 'memberGetAccessorElement':
    case 'memberSetAccessorElement': return 'property';
    case 'class': return 'class';
    case 'interface': return 'interface';
    case 'type': return 'type';
    case 'keyword': return 'keyword';
    case 'module':
    case 'externalModule': return 'namespace';
    case 'const':
    case 'enum': return 'constant';
    default: return 'variable';
  }
}

function documentationNode(
  display: string,
  documentation: string,
  tags: string[] = [],
): HTMLElement {
  const dom = document.createElement('div');
  dom.className = 'script-analysis-documentation';
  for (const [index, text] of [display, documentation, tags.join('\n')].entries()) {
    if (!text) continue;
    const section = document.createElement('div');
    section.className = index === 0 ? 'script-analysis-display' : 'script-analysis-text';
    section.textContent = text;
    dom.appendChild(section);
  }
  return dom;
}

function detailsNode(details: AnalysisCompletionDetails): HTMLElement {
  return documentationNode(details.display, details.documentation, details.tags);
}

function quickInfoNode(info: AnalysisQuickInfo): HTMLElement {
  return documentationNode(info.display, info.documentation, info.tags);
}

function analysisCompletion(client: ScriptAnalysisClient) {
  return async (context: CompletionContext): Promise<CompletionResult | null> => {
    const word = context.matchBefore(/[\w$]*/);
    if (!word) return null;
    if (!context.explicit && word.from === word.to) {
      const previous = context.state.doc.sliceString(Math.max(0, context.pos - 1), context.pos);
      if (previous !== '.' && previous !== '"' && previous !== "'") return null;
    }

    client.setDocument(context.state.doc.toString());
    const documentRevision = client.currentDocumentRevision;
    const catalogRevision = client.currentCatalogRevision;
    try {
      const result = await client.completions(context.pos);
      if (!result || result.entries.length === 0) return null;
      const defaultFrom = result.optionalReplacementSpan?.from ?? word.from;
      const defaultTo = result.optionalReplacementSpan?.to ?? context.pos;
      const options = result.entries.map((entry): Completion => ({
        label: entry.name,
        type: completionType(entry.kind),
        sortText: entry.sortText,
        commitCharacters: entry.commitCharacters,
        apply: (view, completion, from, to) => {
          const replaceFrom = entry.replacementSpan
            ? entry.replacementSpan.from + from - defaultFrom
            : from;
          const replaceTo = entry.replacementSpan
            ? entry.replacementSpan.to + to - defaultTo
            : to;
          const insert = entry.insertText ?? entry.name;
          view.dispatch({
            changes: { from: replaceFrom, to: replaceTo, insert },
            selection: { anchor: replaceFrom + insert.length },
            userEvent: 'input.completion',
            annotations: pickedCompletion.of(completion),
          });
        },
        info: async () => {
          if (
            client.currentDocumentRevision !== documentRevision ||
            client.currentCatalogRevision !== catalogRevision
          ) return null;
          try {
            const details = await client.completionDetails(context.pos, entry);
            return details ? detailsNode(details) : null;
          } catch {
            return null;
          }
        },
      }));
      return {
        from: defaultFrom,
        to: defaultTo,
        options,
      };
    } catch {
      return null;
    }
  };
}

function analysisHover(client: ScriptAnalysisClient): Extension {
  return hoverTooltip(async (view, pos) => {
    client.setDocument(view.state.doc.toString());
    try {
      const info = await client.quickInfo(pos);
      if (!info) return null;
      return {
        pos: info.from,
        end: info.to > info.from ? info.to : undefined,
        above: true,
        create: () => ({ dom: quickInfoNode(info) }),
      };
    } catch {
      return null;
    }
  });
}

function analysisDiagnostics(client: ScriptAnalysisClient): Extension {
  return linter(async (view) => {
    client.setDocument(view.state.doc.toString());
    try {
      const diagnostics = await client.diagnostics();
      return diagnostics.map((diagnostic) => ({
        from: diagnostic.from,
        to: diagnostic.to,
        severity: diagnostic.severity,
        message: diagnostic.message,
        source: `TypeScript ${diagnostic.code}`,
      }));
    } catch {
      return [];
    }
  }, { delay: 300 });
}

interface SignatureTooltipState {
  help: AnalysisSignatureHelp;
}

const setSignatureTooltip = StateEffect.define<SignatureTooltipState | null>();
const signatureTooltip = StateField.define<Tooltip | null>({
  create: () => null,
  update(value, transaction) {
    for (const effect of transaction.effects) {
      if (effect.is(setSignatureTooltip)) {
        const state = effect.value;
        if (!state) return null;
        return {
          pos: state.help.from,
          end: state.help.to,
          above: true,
          create: () => ({ dom: signatureNode(state.help) }),
        };
      }
    }
    if (transaction.docChanged || transaction.selection) return null;
    return value;
  },
  provide: (field) => showTooltip.from(field),
});

function signatureNode(help: AnalysisSignatureHelp): HTMLElement {
  const item = help.items[help.activeItem] ?? help.items[0];
  const dom = document.createElement('div');
  dom.className = 'cm-signature-help';
  const signature = document.createElement('div');
  signature.className = 'cm-signature-signature';
  signature.append(document.createTextNode(item.prefix));
  item.parameters.forEach((parameter, index) => {
    if (index > 0) signature.append(document.createTextNode(`${item.separator} `));
    const span = document.createElement('span');
    span.textContent = parameter.display;
    if (index === help.activeArgument) span.className = 'cm-signature-active';
    signature.appendChild(span);
  });
  signature.append(document.createTextNode(item.suffix));
  dom.appendChild(signature);

  const active = item.parameters[help.activeArgument];
  const documentation = active?.documentation || item.documentation;
  if (documentation) {
    const text = document.createElement('div');
    text.className = 'cm-signature-documentation';
    text.textContent = documentation;
    dom.appendChild(text);
  }
  return dom;
}

function analysisSignature(client: ScriptAnalysisClient): Extension {
  const plugin = ViewPlugin.fromClass(class {
    private timer: ReturnType<typeof setTimeout> | null = null;
    private generation = 0;
    private destroyed = false;

    constructor(view: EditorView) {
      this.schedule(view);
    }

    update(update: ViewUpdate) {
      if (update.docChanged || update.selectionSet) this.schedule(update.view);
    }

    schedule(view: EditorView) {
      if (this.timer !== null) clearTimeout(this.timer);
      const generation = ++this.generation;
      this.timer = setTimeout(() => void this.load(view, generation), 100);
    }

    async load(view: EditorView, generation: number) {
      const position = view.state.selection.main.head;
      client.setDocument(view.state.doc.toString());
      const documentRevision = client.currentDocumentRevision;
      try {
        const help = await client.signatureHelp(position);
        if (
          this.destroyed ||
          generation !== this.generation ||
          client.currentDocumentRevision !== documentRevision
        ) return;
        view.dispatch({ effects: setSignatureTooltip.of(help ? { help } : null) });
      } catch {
        // A newer document or a stopped worker invalidates the pending tooltip.
      }
    }

    destroy() {
      this.destroyed = true;
      this.generation++;
      if (this.timer !== null) clearTimeout(this.timer);
    }
  });

  const escape = keymap.of([{
    key: 'Escape',
    run: (view) => {
      if (!view.state.field(signatureTooltip)) return false;
      view.dispatch({ effects: setSignatureTooltip.of(null) });
      return true;
    },
  }]);
  return [signatureTooltip, plugin, escape];
}

// scriptAnalysisExtensions adds catalog analysis without replacing the
// sandbox's existing JavaScript completion source.
export function scriptAnalysisExtensions(client: ScriptAnalysisClient): Extension[] {
  return [
    javascriptLanguage.data.of({ autocomplete: analysisCompletion(client) }),
    analysisHover(client),
    analysisDiagnostics(client),
    analysisSignature(client),
  ];
}
