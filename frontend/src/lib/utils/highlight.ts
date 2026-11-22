// Minimal syntax highlighter for the script editor. It exists instead of a
// highlighting library because the editor needs exactly three languages and a
// dependency-free build: a single tokenizer pass is a few hundred bytes where
// a library is a few hundred kilobytes.
//
// The tokenizer scans left to right with one combined regular expression per
// language. Alternation order encodes precedence, so comments and strings are
// matched before anything that could appear inside them.

// TokenPattern is one named alternative in a language's combined expression.
interface TokenPattern {
  name: string;
  source: string;
}

// escapeHtml makes source text safe to interpolate into the highlight layer.
export function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

const JS_KEYWORDS =
  'break|case|catch|class|const|continue|debugger|default|delete|do|else|export|extends|' +
  'finally|for|function|if|import|in|instanceof|let|new|of|return|super|switch|this|throw|' +
  'try|typeof|var|void|while|with|yield|async|await|static|get|set';

const JS_LITERALS = 'true|false|null|undefined|NaN|Infinity';

const JS_BUILTINS =
  'console|JSON|Math|Date|Object|Array|String|Number|Boolean|RegExp|Error|Promise|Map|Set|' +
  'parseInt|parseFloat|isNaN|http|payload|topic|meta|msg';

const PY_KEYWORDS =
  'and|as|assert|async|await|break|class|continue|def|del|elif|else|except|finally|for|from|' +
  'global|if|import|in|is|lambda|nonlocal|not|or|pass|raise|return|try|while|with|yield';

const PY_LITERALS = 'True|False|None';

const PY_BUILTINS =
  'print|len|range|str|int|float|bool|list|dict|set|tuple|sum|min|max|abs|round|sorted|' +
  'enumerate|zip|map|filter|open|isinstance|type|json|karakuri|result|payload|topic|meta|msg';

const SH_KEYWORDS =
  'if|then|else|elif|fi|for|while|until|do|done|case|esac|function|in|select|time|return|' +
  'break|continue|local|export|readonly|declare|shift|exit';

const SH_BUILTINS = 'echo|printf|read|test|log|http|jsonget|set|unset|eval|source|trap|cd|pwd|true|false';

// patternsFor returns the ordered token alternatives for a language.
function patternsFor(language: string): TokenPattern[] {
  switch (language) {
    case 'json':
      // Property names are the strings a colon follows, so they are matched
      // ahead of plain strings and keep the colon out of the span.
      return [
        { name: 'property', source: '"(?:\\\\[\\s\\S]|[^"\\\\\\n])*"(?=\\s*:)' },
        { name: 'string', source: '"(?:\\\\[\\s\\S]|[^"\\\\\\n])*"' },
        { name: 'literal', source: '\\b(?:true|false|null)\\b' },
        { name: 'number', source: '-?\\b\\d+(?:\\.\\d+)?(?:[eE][+-]?\\d+)?\\b' },
      ];
    case 'yaml':
      // Keys carry to the colon, so they are matched before the strings and
      // scalars that can follow one. Comments come first, as always.
      return [
        { name: 'comment', source: '#[^\\n]*' },
        { name: 'property', source: '(?:^|(?<=\\n))\\s*-?\\s*[A-Za-z_][\\w.-]*(?=\\s*:)' },
        { name: 'string', source: '"(?:\\\\[\\s\\S]|[^"\\\\\\n])*"|\'[^\'\\n]*\'' },
        { name: 'literal', source: '\\b(?:true|false|null|yes|no|on|off|~)\\b' },
        { name: 'number', source: '-?\\b\\d+(?:\\.\\d+)?(?:[eE][+-]?\\d+)?\\b' },
      ];
    case 'plain':
      // Nothing to colour: every character comes through escaped as-is.
      return [];
    case 'python':
      return [
        { name: 'comment', source: '#[^\\n]*' },
        {
          name: 'string',
          source:
            '(?:[rbfu]{0,2})(?:"""[\\s\\S]*?"""|\'\'\'[\\s\\S]*?\'\'\'' +
            '|"(?:\\\\[\\s\\S]|[^"\\\\\\n])*"|\'(?:\\\\[\\s\\S]|[^\'\\\\\\n])*\')',
        },
        { name: 'literal', source: `\\b(?:${PY_LITERALS})\\b` },
        { name: 'keyword', source: `\\b(?:${PY_KEYWORDS})\\b` },
        { name: 'builtin', source: `\\b(?:${PY_BUILTINS})\\b` },
        { name: 'number', source: '\\b\\d[\\d_]*(?:\\.\\d+)?(?:[eE][+-]?\\d+)?\\b' },
        { name: 'decorator', source: '@[A-Za-z_][\\w.]*' },
      ];
    case 'bash':
      return [
        { name: 'comment', source: '#[^\\n]*' },
        {
          name: 'string',
          source: '"(?:\\\\[\\s\\S]|[^"\\\\])*"|\'[^\']*\'',
        },
        { name: 'variable', source: '\\$(?:\\{[^}]*\\}|[A-Za-z_]\\w*|[@*#?$!0-9-])' },
        { name: 'keyword', source: `\\b(?:${SH_KEYWORDS})\\b` },
        { name: 'builtin', source: `\\b(?:${SH_BUILTINS})\\b` },
        { name: 'number', source: '\\b\\d+\\b' },
      ];
    default:
      return [
        { name: 'comment', source: '//[^\\n]*|/\\*[\\s\\S]*?\\*/' },
        {
          name: 'string',
          source:
            '`(?:\\\\[\\s\\S]|[^`\\\\])*`' +
            '|"(?:\\\\[\\s\\S]|[^"\\\\\\n])*"' +
            "|'(?:\\\\[\\s\\S]|[^'\\\\\\n])*'",
        },
        { name: 'literal', source: `\\b(?:${JS_LITERALS})\\b` },
        { name: 'keyword', source: `\\b(?:${JS_KEYWORDS})\\b` },
        { name: 'builtin', source: `\\b(?:${JS_BUILTINS})\\b` },
        { name: 'number', source: '\\b\\d[\\d_]*(?:\\.\\d+)?(?:[eE][+-]?\\d+)?\\b' },
      ];
  }
}

// Compiled expressions are cached per language; the editor re-highlights on
// every keystroke and recompiling each time is wasted work.
const compiled = new Map<string, { re: RegExp; names: string[] }>();

function expressionFor(language: string) {
  const cached = compiled.get(language);
  if (cached) return cached;

  const patterns = patternsFor(language);
  const re = new RegExp(patterns.map((p) => `(${p.source})`).join('|'), 'g');
  const entry = { re, names: patterns.map((p) => p.name) };
  compiled.set(language, entry);
  return entry;
}

// highlight renders source into HTML with token spans. The output preserves
// every character of the input, including whitespace and newlines, so it can
// be layered exactly behind a textarea holding the same text.
export function highlight(source: string, language: string): string {
  const { re, names } = expressionFor(language);
  // A language with no patterns compiles to an empty expression, which matches
  // at every position; text with nothing to colour is escaped and returned.
  if (names.length === 0) return escapeHtml(source) + '\n';
  re.lastIndex = 0;

  let html = '';
  let last = 0;
  for (const match of source.matchAll(re)) {
    const index = match.index ?? 0;
    if (index > last) {
      html += escapeHtml(source.slice(last, index));
    }
    // Find which alternative matched; group N+1 corresponds to names[N].
    let name = 'plain';
    for (let i = 0; i < names.length; i++) {
      if (match[i + 1] !== undefined) {
        name = names[i];
        break;
      }
    }
    html += `<span class="tok-${name}">${escapeHtml(match[0])}</span>`;
    last = index + match[0].length;
  }
  html += escapeHtml(source.slice(last));

  // A trailing newline would otherwise collapse in the pre layer and shift
  // the highlight out of alignment with the textarea's last line.
  return html + '\n';
}

// previewLines returns the first few non-empty lines of a script, for the
// collapsed preview shown in the config panel.
export function previewLines(source: string, limit = 6): string[] {
  const lines = source.split('\n');
  // Drop leading blank lines so the preview starts on real content.
  while (lines.length > 0 && lines[0].trim() === '') lines.shift();
  return lines.slice(0, limit);
}

// languageLabel is the display name for a language identifier.
export function languageLabel(language: string): string {
  switch (language) {
    case 'python':
      return 'Python';
    case 'bash':
      return 'Bash';
    case 'json':
      return 'JSON';
    case 'yaml':
      return 'YAML';
    case 'markdown':
      return 'Markdown';
    case 'plain':
      return 'Text';
    default:
      return 'JavaScript';
  }
}
