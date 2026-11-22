// CodeMirror wiring for the script editor: the language mode, the karakuri
// API completions layered on top of each language's own, and a theme built
// from the app's CSS variables so the editor matches the rest of the UI.

import { StreamLanguage, type LanguageSupport, type Language } from '@codemirror/language';
import { javascript, javascriptLanguage } from '@codemirror/lang-javascript';
import { json } from '@codemirror/lang-json';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { python, pythonLanguage } from '@codemirror/lang-python';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { yaml } from '@codemirror/legacy-modes/mode/yaml';
import { snippetCompletion } from '@codemirror/autocomplete';
import type { Completion, CompletionContext, CompletionResult, CompletionSource } from '@codemirror/autocomplete';
import { EditorView } from '@codemirror/view';
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language';
import { tags } from '@lezer/highlight';
import type { Extension } from '@codemirror/state';

// bashLanguage is defined from the legacy stream mode; CodeMirror ships no
// dedicated shell package.
const bashLanguage = StreamLanguage.define(shell);

// yamlLanguage comes from the legacy stream mode as well, for the documents
// written as YAML rather than JSON.
const yamlLanguage = StreamLanguage.define(yaml);

// ---- Completions ----
// These describe the values the sandbox injects into every script. The
// language's own completion source still runs, so locals and standard-library
// names keep working; these entries are added to that list.

const jsCompletions: Completion[] = [
  { label: 'payload', type: 'variable', detail: 'any', info: 'The incoming message payload.' },
  { label: 'topic', type: 'variable', detail: 'string', info: 'The incoming message topic.' },
  { label: 'meta', type: 'variable', detail: 'object', info: 'Message metadata. Webhook runs include method, path, query, headers, and body.' },
  { label: 'msg', type: 'variable', detail: '{payload, topic, meta}', info: 'The whole incoming message.' },
  { label: 'input', type: 'variable', detail: 'object', info: 'Every connected input port keyed by the ID declared under Inputs, so input.celsius reads the port named celsius. The first declared port is also payload/topic/meta.' },
  { label: 'vars', type: 'variable', detail: 'object', info: 'Run variables, shared with every other node in this run. Assign to hand values on; set response_code, response_body, or response_headers to shape a synchronous webhook reply.' },
  snippetCompletion('vars.${name} = ${value};', {
    label: 'vars.set',
    type: 'property',
    info: 'Writes a run variable. Assigning null clears it.',
  }),
  snippetCompletion('vars.response_code = ${200};\nvars.response_body = ${body};', {
    label: 'vars.response',
    type: 'property',
    info: 'Sets the reply for a synchronous webhook, used whether a branch returns to the Webhook or the run just ends.',
  }),
  { label: 'flow', type: 'variable', detail: 'object', info: 'Flow variables, shared by every run of this flow and kept across restarts.' },
  { label: 'global', type: 'variable', detail: 'object', info: 'Global variables, shared by every flow and kept across restarts.' },
  snippetCompletion('get(${"name"})', {
    label: 'get',
    type: 'function',
    detail: '(name?, scope?) => any',
    info: 'Reads a variable. The scope defaults to "msg" and the name to "payload"; pass "flow", "global", or "run" for the other scopes.',
  }),
  snippetCompletion('set(${"name"}, ${value})', {
    label: 'set',
    type: 'function',
    detail: '(name, value, scope?) => void',
    info: 'Writes a variable. The scope defaults to "msg", so set("payload", v) rewrites the payload. Pass "flow", "global", or "run" for the other scopes; null clears one.',
  }),
  snippetCompletion('set(${"count"}, (get(${"count"}, "flow") || 0) + 1, "flow");', {
    label: 'set(flow)',
    type: 'function',
    info: 'Increments a flow variable, which survives the run and a restart.',
  }),
  snippetCompletion('http(${url})', {
    label: 'http',
    type: 'function',
    detail: '(url) => response',
    info: 'Performs a GET under the server network policy. Returns {status, ok, headers, body, json()}.',
  }),
  snippetCompletion('http({ method: "${POST}", url: ${url}, body: JSON.stringify(${body}) })', {
    label: 'http({...})',
    type: 'function',
    detail: '(options) => response',
    info: 'Performs a request with a method, headers, and body.',
  }),
  { label: 'console.log', type: 'function', info: 'Writes a line to the run panel.' },
  { label: 'console.warn', type: 'function', info: 'Writes a line to the run panel.' },
  { label: 'console.error', type: 'function', info: 'Writes a line to the run panel.' },
  snippetCompletion('return ${value};', { label: 'return', type: 'keyword', info: 'The returned value becomes the new message payload.' }),
];

const pythonCompletions: Completion[] = [
  { label: 'payload', type: 'variable', detail: 'Any', info: 'The incoming message payload.' },
  { label: 'topic', type: 'variable', detail: 'str', info: 'The incoming message topic.' },
  { label: 'meta', type: 'variable', detail: 'dict', info: 'Message metadata. Webhook runs include method, path, query, headers, and body.' },
  { label: 'msg', type: 'variable', detail: 'dict', info: 'The whole incoming message.' },
  { label: 'input', type: 'variable', detail: 'dict', info: 'Every connected input port keyed by the ID declared under Inputs, so input["celsius"] reads the port named celsius. The first declared port is also payload/topic/meta. Shadows the builtin input(), which stays reachable as builtins.input.' },
  { label: 'vars', type: 'variable', detail: 'dict', info: 'Run variables, shared with every other node in this run. Assign to hand values on; set response_code, response_body, or response_headers to shape a synchronous webhook reply. Shadows the builtin vars(), which stays reachable as builtins.vars.' },
  snippetCompletion('vars["${name}"] = ${value}', {
    label: 'vars.set',
    type: 'property',
    info: 'Writes a run variable. Assigning None clears it.',
  }),
  snippetCompletion('vars["response_code"] = ${200}\nvars["response_body"] = ${body}', {
    label: 'vars.response',
    type: 'property',
    info: 'Sets the reply for a synchronous webhook, used whether a branch returns to the Webhook or the run just ends.',
  }),
  { label: 'flow', type: 'variable', detail: 'dict', info: 'Flow variables, shared by every run of this flow and kept across restarts.' },
  { label: 'global', type: 'variable', detail: 'dict', info: 'Global variables, shared by every flow and kept across restarts.' },
  snippetCompletion('get(${"name"})', {
    label: 'get',
    type: 'function',
    detail: '(name="payload", scope="msg") -> Any',
    info: 'Reads a variable. Pass "flow", "global", or "run" for the other scopes. Shadows the builtin get; karakuri.get is the HTTP helper, not this.',
  }),
  snippetCompletion('set(${"name"}, ${value})', {
    label: 'set',
    type: 'function',
    detail: '(name, value, scope="msg") -> None',
    info: 'Writes a variable. The scope defaults to "msg". Pass "flow", "global", or "run" for the other scopes; None clears one. Shadows the builtin set(), which stays reachable as builtins.set.',
  }),
  { label: 'result', type: 'variable', info: 'Assign to this to set the new message payload.' },
  { label: 'karakuri', type: 'namespace', info: 'Host bridge module: get, post, http, HTTPError.' },
  snippetCompletion('karakuri.get(${url})', {
    label: 'karakuri.get',
    type: 'function',
    detail: '(url, headers=None) -> Response',
    info: 'Performs a GET under the server network policy.',
  }),
  snippetCompletion('karakuri.post(${url}, ${body})', {
    label: 'karakuri.post',
    type: 'function',
    detail: '(url, body=None, headers=None) -> Response',
    info: 'Performs a POST with a JSON body.',
  }),
  snippetCompletion('karakuri.http("${POST}", ${url}, json_body=${body})', {
    label: 'karakuri.http',
    type: 'function',
    detail: '(method, url, headers=None, body="", json_body=None) -> Response',
    info: 'Performs a request with an explicit method.',
  }),
  { label: 'karakuri.HTTPError', type: 'class', info: 'Raised when the host refused or could not complete a request.' },
  snippetCompletion('def handle(msg):\n    ${return None}', {
    label: 'handle',
    type: 'function',
    info: 'Optional entry point. When defined, its return value becomes the payload.',
  }),
];

const bashCompletions: Completion[] = [
  { label: '$PAYLOAD', type: 'variable', info: 'The incoming message payload as JSON.' },
  { label: '$TOPIC', type: 'variable', info: 'The incoming message topic.' },
  { label: '$META', type: 'variable', info: 'Message metadata as JSON.' },
  { label: '$INPUT', type: 'variable', info: 'Every connected input port as JSON, keyed by the ID declared under Inputs. Read one with jsonget <port> "$INPUT". The first declared port is also $PAYLOAD/$TOPIC/$META.' },
  { label: '$MSG', type: 'variable', info: 'The complete first input message as JSON, including its ID, payload, topic, metadata, grouping, and continuation state.' },
  { label: '$VARS', type: 'variable', info: 'Run variables as JSON. Read one with jsonget <name> "$VARS".' },
  { label: '$FLOW', type: 'variable', info: 'Flow variables as JSON. Read one with jsonget <name> "$FLOW", or use getvar -s flow <name>.' },
  { label: '$GLOBAL', type: 'variable', info: 'Global variables as JSON. Read one with jsonget <name> "$GLOBAL", or use getvar -s global <name>.' },
  snippetCompletion('setvar ${name} ${value}', {
    label: 'setvar',
    type: 'function',
    detail: '[-s <scope>] <name> [value]',
    info: 'Writes a variable. The scope defaults to msg, so setvar payload <value> rewrites the payload; pass -s flow, -s global, or -s run for the others. JSON values are stored decoded; omitting the value clears it.',
  }),
  snippetCompletion('getvar ${name}', {
    label: 'getvar',
    type: 'function',
    detail: '[-s <scope>] [name]',
    info: 'Prints a variable so it can be captured: count=$(getvar -s flow count). Defaults to the message payload.',
  }),
  snippetCompletion('log ${message}', {
    label: 'log',
    type: 'function',
    detail: '<message>',
    info: 'Writes to the run panel. Standard output is the result, so log instead of echo for messages.',
  }),
  snippetCompletion('http ${GET} ${url}', {
    label: 'http',
    type: 'function',
    detail: '<method> <url> [body] [header:value ...]',
    info: 'Performs a request under the server network policy and prints the response body.',
  }),
  snippetCompletion('jsonget ${path} "$PAYLOAD"', {
    label: 'jsonget',
    type: 'function',
    detail: '<path> [json]',
    info: 'Reads a dot-separated path out of a JSON document. Reads stdin when no document is given.',
  }),
];

// karakuriSource turns a completion list into a source. It fires on word
// characters plus $ and . so shell variables and dotted names complete.
function karakuriSource(options: Completion[]) {
  return (context: CompletionContext): CompletionResult | null => {
    const word = context.matchBefore(/[\w$.]*/);
    if (!word) return null;
    if (word.from === word.to && !context.explicit) return null;
    return { from: word.from, options, validFor: /^[\w$.]*$/ };
  };
}

// withCompletions attaches a completion source to a language without
// replacing the language's own, so both sets are offered.
function withCompletions(language: Language, options: Completion[]): Extension {
  return language.data.of({ autocomplete: karakuriSource(options) });
}

// withCompletionSource adds a scoped source through the same language objects
// as the sandbox completions. Catalog callers supply the source only in editors
// whose runtime has the corresponding capability.
export function withCompletionSource(language: string, source: CompletionSource): Extension {
  switch (language) {
    case 'python':
      return pythonLanguage.data.of({ autocomplete: source });
    case 'bash':
      return bashLanguage.data.of({ autocomplete: source });
    default:
      return javascriptLanguage.data.of({ autocomplete: source });
  }
}

// shebangLanguage reads an editor language from a script's first line. It backs
// the agent's run-script node, whose interpreter is the shebang rather than a
// language picker, so the editor highlights and completes for whatever the
// author declared. Returns null when there is no usable shebang.
export function shebangLanguage(source: string): string | null {
  const firstLine = source.slice(0, source.indexOf('\n') === -1 ? source.length : source.indexOf('\n'));
  const line = firstLine.replace(/\r$/, '');
  if (!line.startsWith('#!')) return null;
  const parts = line.slice(2).trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return null;
  let cmd = parts[0].split(/[\\/]/).pop() || parts[0];
  if (cmd === 'env' && parts.length > 1) {
    cmd = parts[1].split(/[\\/]/).pop() || parts[1];
  }
  // A trailing version number is common (python3.11); match the leading name.
  if (/^python/.test(cmd)) return 'python';
  if (cmd === 'node' || cmd === 'nodejs' || cmd === 'deno' || cmd === 'bun') return 'javascript';
  if (cmd === 'bash' || cmd === 'sh' || cmd === 'zsh' || cmd === 'dash') return 'bash';
  return 'plain';
}

// languageExtensions returns the mode and completions for a language. The
// document modes carry no karakuri completions: a test payload is data, and
// offering it the sandbox API would be offering names it cannot use.
export function languageExtensions(language: string): Extension[] {
  switch (language) {
    case 'python':
      return [python() as LanguageSupport, withCompletions(pythonLanguage, pythonCompletions)];
    case 'bash':
      return [bashLanguage, withCompletions(bashLanguage, bashCompletions)];
    case 'json':
      return [json() as LanguageSupport];
    case 'yaml':
      return [yamlLanguage];
    case 'markdown':
      return [markdown({ base: markdownLanguage, completeHTMLTags: false, codeLanguages: (info) => {
        switch (info.split(/\s+/)[0].toLowerCase()) {
          case 'js': case 'javascript': return javascriptLanguage;
          case 'json': return json().language;
          case 'py': case 'python': return pythonLanguage;
          case 'sh': case 'shell': case 'bash': return bashLanguage;
          case 'yaml': case 'yml': return yamlLanguage;
          default: return null;
        }
      } })];
    case 'plain':
      // Text with no syntax to colour: the editor is still worth having for
      // the room and the line numbers.
      return [];
    default:
      return [javascript() as LanguageSupport, withCompletions(javascriptLanguage, jsCompletions)];
  }
}

// ---- Theme ----
// Colors come from the app's CSS variables rather than literal values, so the
// editor follows any future theme change with the rest of the UI. Fallbacks
// keep it readable if a variable is ever missing.

const V = {
  base: 'var(--theme-base, #1e1e2e)',
  crust: 'var(--theme-crust, #11111b)',
  mantle: 'var(--theme-mantle, #181825)',
  text: 'var(--theme-text, #cdd6f4)',
  muted: 'var(--theme-text-muted, #a6adc8)',
  surface0: 'var(--theme-surface0, #313244)',
  surface1: 'var(--theme-surface1, #45475a)',
  surface2: 'var(--theme-surface2, #585b70)',
  overlay0: 'var(--theme-overlay0, #6c7086)',
  accent: 'var(--theme-accent, #89b4fa)',
  success: 'var(--theme-success, #a6e3a1)',
  warning: 'var(--theme-warning, #f9e2af)',
  error: 'var(--theme-error, #f38ba8)',
  info: 'var(--theme-info, #94e2d5)',
  mono: "var(--theme-font-mono, 'JetBrains Mono', 'Fira Code', monospace)",
};

export const scriptTheme = EditorView.theme(
  {
    '&': {
      color: V.text,
      backgroundColor: V.crust,
      height: '100%',
      fontSize: '13px',
    },
    '.cm-content': {
      fontFamily: V.mono,
      padding: '10px 0',
caretColor: V.text,
    },
    '.cm-scroller': {
      fontFamily: V.mono,
      lineHeight: '1.55',
      overflow: 'auto',
    },
    '&.cm-focused': { outline: 'none' },
    '.cm-cursor, .cm-dropCursor': { borderLeftColor: V.text },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
      backgroundColor: V.surface1,
    },
    '.cm-activeLine': { backgroundColor: 'rgba(255,255,255,0.03)' },
    '.cm-gutters': {
      backgroundColor: V.mantle,
      color: V.overlay0,
      border: 'none',
      borderRight: `1px solid ${V.surface0}`,
    },
    '.cm-activeLineGutter': { backgroundColor: V.surface0, color: V.text },
    '.cm-foldPlaceholder': {
      backgroundColor: V.surface1,
      border: 'none',
      color: V.muted,
    },
    '.cm-matchingBracket, &.cm-focused .cm-matchingBracket': {
      backgroundColor: V.surface2,
      outline: `1px solid ${V.accent}`,
    },
    '.cm-nonmatchingBracket': { color: V.error },
    '.cm-selectionMatch': { backgroundColor: V.surface0 },

    // Search and replace panel.
    '.cm-panels': {
      backgroundColor: V.mantle,
      color: V.text,
      borderTop: `1px solid ${V.surface1}`,
    },
    '.cm-panel.cm-search input, .cm-panel.cm-search button, .cm-panel.cm-search label': {
      fontSize: '12px',
    },
    '.cm-panel.cm-search input[type=text]': {
      backgroundColor: V.crust,
      color: V.text,
      border: `1px solid ${V.surface1}`,
      borderRadius: '4px',
      padding: '2px 6px',
    },
    '.cm-panel.cm-search button': {
      backgroundColor: V.surface0,
      backgroundImage: 'none',
      color: V.text,
      border: `1px solid ${V.surface1}`,
      borderRadius: '4px',
      padding: '2px 8px',
      cursor: 'pointer',
    },
    '.cm-panel.cm-search button:hover': { backgroundColor: V.surface1 },
    '.cm-searchMatch': { backgroundColor: 'rgba(249, 226, 175, 0.25)' },
    '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'rgba(137, 180, 250, 0.4)' },

    // Autocomplete popup.
    '.cm-tooltip': {
      backgroundColor: V.mantle,
      border: `1px solid ${V.surface1}`,
      borderRadius: '6px',
      color: V.text,
    },
    '.cm-tooltip.cm-tooltip-autocomplete > ul': {
      fontFamily: V.mono,
      fontSize: '12px',
      maxHeight: '16em',
    },
    '.cm-tooltip.cm-tooltip-autocomplete > ul > li[aria-selected]': {
      backgroundColor: V.surface1,
      color: V.text,
    },
    '.cm-completionLabel': { color: V.text },
    '.cm-completionDetail': { color: V.muted, fontStyle: 'normal', marginLeft: '8px' },
    '.cm-completionInfo': {
      backgroundColor: V.mantle,
      border: `1px solid ${V.surface1}`,
      borderRadius: '6px',
      padding: '6px 8px',
      fontFamily: 'inherit',
      maxWidth: '320px',
    },
    '.cm-completionIcon': { color: V.muted, paddingRight: '10px' },
    '.script-analysis-documentation': {
      maxWidth: '420px',
      maxHeight: '18em',
      overflow: 'auto',
      padding: '7px 9px',
      fontFamily: 'inherit',
      fontSize: '12px',
      whiteSpace: 'pre-wrap',
    },
    '.cm-completionInfo .script-analysis-documentation': { padding: '0' },
    '.script-analysis-display': {
      fontFamily: V.mono,
      fontSize: '11px',
      color: V.text,
    },
    '.script-analysis-display + .script-analysis-text, .script-analysis-text + .script-analysis-text': {
      marginTop: '6px',
      color: V.muted,
    },
    '.cm-signature-help': {
      maxWidth: '460px',
      padding: '7px 9px',
      whiteSpace: 'pre-wrap',
    },
    '.cm-signature-signature': { fontFamily: V.mono, fontSize: '12px' },
    '.cm-signature-active': {
      color: V.accent,
      fontWeight: '600',
      textDecoration: 'underline',
      textUnderlineOffset: '2px',
    },
    '.cm-signature-documentation': {
      marginTop: '6px',
      color: V.muted,
      fontSize: '12px',
    },
    '.cm-diagnostic': {
      padding: '6px 8px',
      fontSize: '12px',
      whiteSpace: 'pre-wrap',
    },
    '.cm-lintRange-error': {
      backgroundImage: 'none',
      textDecoration: `underline wavy ${V.error}`,
      textUnderlineOffset: '3px',
    },
  },
  { dark: true },
);

// scriptHighlightStyle maps syntax tags onto the theme palette. It mirrors the
// colors used by the collapsed preview in the config panel so a script looks
// the same in both places.
export const scriptHighlightStyle = HighlightStyle.define([
  { tag: [tags.comment, tags.lineComment, tags.blockComment], color: V.overlay0, fontStyle: 'italic' },
  { tag: [tags.string, tags.special(tags.string)], color: V.success },
  { tag: [tags.number, tags.bool, tags.null, tags.atom], color: V.warning },
  { tag: [tags.keyword, tags.modifier, tags.controlKeyword, tags.operatorKeyword], color: V.error },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName)], color: V.accent },
  { tag: [tags.definition(tags.variableName), tags.definition(tags.propertyName)], color: V.text },
  { tag: [tags.propertyName, tags.attributeName], color: V.info },
  { tag: [tags.className, tags.typeName, tags.namespace], color: V.warning },
  { tag: [tags.variableName, tags.labelName], color: V.text },
  { tag: [tags.operator, tags.punctuation, tags.separator, tags.bracket], color: V.muted },
  { tag: [tags.meta, tags.processingInstruction], color: V.info },
  { tag: tags.invalid, color: V.error },
  { tag: tags.heading, color: V.accent, fontWeight: 'bold' },
  { tag: tags.strong, fontWeight: 'bold' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: tags.link, color: V.info, textDecoration: 'underline' },
  { tag: tags.monospace, color: V.success },
  { tag: tags.quote, color: V.muted },
  { tag: tags.strikethrough, textDecoration: 'line-through' },
]);

// scriptSyntaxHighlighting is the highlighter extension for the editor.
export const scriptSyntaxHighlighting: Extension = syntaxHighlighting(scriptHighlightStyle);
