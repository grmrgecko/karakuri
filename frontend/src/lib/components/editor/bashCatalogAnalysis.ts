import { literalEntryAt, literalPairs, type LiteralEntry, type LiteralToken } from './catalogLiteral';
import {
  catalogFunctionDocumentation,
  catalogSignatureParameters,
  genericCallDocumentation,
} from './catalogHelp';
import {
  memberAtPath,
  membersForPath,
  messageMembers,
  outputMembers,
  schemaMembersAt,
  schemaAtPath,
  type CatalogCompletion,
  type CatalogCompletionResult,
  type CatalogDiagnostic,
  type CatalogMember,
  type CatalogNode,
  type CatalogQuickInfo,
  type CatalogSignatureHelp,
  type ScriptCatalogProjection,
} from './scriptCatalogProjection';

interface ShellToken extends LiteralToken {
  substitution?: boolean;
}

interface ShellCommand {
  name: 'node' | 'jsonget';
  nameIndex: number;
  args: number[];
  end: number;
  node?: CatalogNode;
  generic: boolean;
  callIndex: number;
  targetIndex: number;
  target: string;
}

interface ShellParse {
  source: string;
  tokens: ShellToken[];
  pairs: Map<number, number>;
  commands: ShellCommand[];
}

function shellTokens(source: string): ShellToken[] {
  const tokens: ShellToken[] = [];
  let index = 0;
  let wordStart = -1;

  const flush = (end: number) => {
    if (wordStart === -1) return;
    tokens.push({ kind: 'name', text: source.slice(wordStart, end), from: wordStart, to: end });
    wordStart = -1;
  };
  const operator = (text: string, from: number, to: number, substitution = false) => {
    flush(from);
    tokens.push({ kind: 'punct', text, from, to, substitution });
  };

  while (index < source.length) {
    const char = source[index];
    if (char === '#' && wordStart === -1) {
      const newline = source.indexOf('\n', index + 1);
      index = newline === -1 ? source.length : newline;
      continue;
    }
    if (char === ' ' || char === '\t' || char === '\r') {
      flush(index);
      index++;
      continue;
    }
    if (char === '\n' || char === ';') {
      operator(';', index, ++index);
      continue;
    }
    if (char === '|' || char === '&') {
      const next = source[index + 1] === char ? 2 : 1;
      operator(source.slice(index, index + next), index, index + next);
      index += next;
      continue;
    }
    if (char === '>' || char === '<') {
      operator(char, index, ++index);
      continue;
    }
    if (char === '(' || char === ')') {
      operator(char, index, ++index);
      continue;
    }
    if (char === '$' && source[index + 1] === '(') {
      operator('(', index, index + 2, true);
      index += 2;
      continue;
    }
    if (char === '\\') {
      if (wordStart === -1) wordStart = index;
      index += Math.min(2, source.length - index);
      continue;
    }
    if (char === '"' || char === "'") {
      if (wordStart === -1) wordStart = index;
      const quote = char;
      index++;
      while (index < source.length) {
        if (quote === '"' && source[index] === '\\') {
          index += Math.min(2, source.length - index);
          continue;
        }
        if (source[index] === quote) {
          index++;
          break;
        }
        index++;
      }
      continue;
    }
    if (wordStart === -1) wordStart = index;
    index++;
  }
  flush(source.length);
  return tokens;
}

function shellStatic(token: ShellToken | undefined): string | null {
  if (!token || token.kind !== 'name') return null;
  const raw = token.text;
  let result = '';
  let quote = '';
  for (let index = 0; index < raw.length; index++) {
    const char = raw[index];
    if (quote === "'") {
      if (char === "'") quote = '';
      else result += char;
      continue;
    }
    if (quote === '"') {
      if (char === '"') {
        quote = '';
        continue;
      }
      if (char === '\\' && index + 1 < raw.length && ['"', '\\', '$', '`'].includes(raw[index + 1])) {
        result += raw[++index];
        continue;
      }
      if (char === '$' || char === '`') return null;
      result += char;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === '\\' && index + 1 < raw.length) {
      result += raw[++index];
      continue;
    }
    if (char === '$' || char === '`') return null;
    result += char;
  }
  return result;
}

function commandPosition(tokens: ShellToken[], index: number): boolean {
  const previous = tokens[index - 1];
  if (!previous) return true;
  if (previous.kind === 'punct') return [';', '(', '|', '||', '&&'].includes(previous.text);
  return previous.kind === 'name' && ['if', 'then', 'else', 'elif', 'while', 'until', 'do', '!'].includes(previous.text);
}

function parseShell(source: string, projection: ScriptCatalogProjection): ShellParse {
  const tokens = shellTokens(source);
  const pairs = literalPairs(tokens);
  const commands: ShellCommand[] = [];
  for (let index = 0; index < tokens.length; index++) {
    if (!commandPosition(tokens, index)) continue;
    const word = shellStatic(tokens[index]);
    if (word !== 'node' && word !== 'jsonget') continue;
    const args: number[] = [];
    let cursor = index + 1;
    for (; cursor < tokens.length; cursor++) {
      if (tokens[cursor].kind === 'punct') break;
      args.push(cursor);
    }
    const end = cursor < tokens.length ? tokens[cursor].from : source.length;
    const command: ShellCommand = {
      name: word,
      nameIndex: index,
      args,
      end,
      generic: false,
      callIndex: -1,
      targetIndex: -1,
      target: '',
    };
    if (word === 'node') {
      const first = args[0];
      const firstValue = shellStatic(tokens[first]);
      command.callIndex = firstValue === 'call' ? first : -1;
      command.generic = firstValue === 'call';
      command.targetIndex = command.generic ? args[1] : first;
      command.target = shellStatic(tokens[command.targetIndex]) ?? '';
      command.node = command.target
        ? command.generic ? projection.byType.get(command.target) : projection.byType.get(command.target) ?? projection.byAlias.get(command.target)
        : undefined;
    }
    commands.push(command);
  }
  return { source, tokens, pairs, commands };
}

function containingCommand(parse: ShellParse, pos: number, name: 'node' | 'jsonget'): ShellCommand | undefined {
  let selected: ShellCommand | undefined;
  for (const command of parse.commands) {
    if (command.name !== name) continue;
    const start = parse.tokens[command.nameIndex].from;
    if (start >= pos || pos > command.end) continue;
    if (!selected || start > parse.tokens[selected.nameIndex].from) selected = command;
  }
  return selected;
}

function activeArgument(parse: ShellParse, command: ShellCommand, pos: number): number {
  let active = 0;
  for (const index of command.args) {
    if (parse.tokens[index].to < pos) active++;
  }
  return Math.min(active, command.args.length);
}

function argumentToken(parse: ShellParse, command: ShellCommand, argument: number): ShellToken | undefined {
  const index = command.args[argument];
  return index === undefined ? undefined : parse.tokens[index];
}

export function shellQuote(value: string): string {
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(value) && value !== '') return value;
  return `'${value.replace(/'/g, `'"'"'`)}'`;
}

function targetCompletion(node: CatalogNode, languageAlias?: string): CatalogCompletion {
  const label = languageAlias ?? node.fn.name;
  return {
    label,
    insert: shellQuote(label),
    type: 'function',
    detail: node.fn.node_type,
    documentation: catalogFunctionDocumentation('bash', node),
  };
}

function bashFunctionCompletion(parse: ShellParse, projection: ScriptCatalogProjection, pos: number): CatalogCompletionResult | null {
  const command = containingCommand(parse, pos, 'node');
  if (!command) return null;
  const active = activeArgument(parse, command, pos);
  const genericTarget = command.generic && active === 1;
  const namedTarget = !command.generic && active === 0;
  if (!genericTarget && !namedTarget) return null;
  const token = argumentToken(parse, command, active);
  const replacement = token ? { from: token.from, to: token.to } : { from: pos, to: pos };
  const first = token?.text[0];
  const quote = first === '"' || first === "'" ? first : '';
  const withFilter = (option: CatalogCompletion): CatalogCompletion => ({
    ...option,
    filterText: `${quote}${option.label}`,
  });
  if (genericTarget) {
    return { ...replacement, options: projection.nodes.map((node) => withFilter(targetCompletion(node))) };
  }
  const options: CatalogCompletion[] = [];
  const seen = new Set<string>();
  for (const node of projection.nodes) {
    for (const name of [node.fn.name, ...(node.fn.aliases ?? [])]) {
      if (seen.has(name)) continue;
      seen.add(name);
      options.push(withFilter(targetCompletion(node, name)));
    }
  }
  if (!seen.has('call')) {
    options.push(withFilter({
      label: 'call',
      insert: 'call',
      type: 'function',
      detail: 'dynamic node call',
      documentation: genericCallDocumentation('bash'),
    }));
  }
  return { ...replacement, options };
}

interface ShellJSON {
  text: string;
  map: number[];
  quote: 'single' | 'double';
  token: ShellToken;
}

function decodeShellJSON(token: ShellToken): ShellJSON | null {
  const raw = token.text;
  const quote = raw[0];
  if (quote !== '"' && quote !== "'") return null;
  let trailingEscapes = 0;
  for (let index = raw.length - 2; index > 0 && raw[index] === '\\'; index--) trailingEscapes++;
  const closed = raw.length > 1 && raw[raw.length - 1] === quote && (quote === "'" || trailingEscapes % 2 === 0);
  const content = raw.slice(1, closed ? -1 : undefined);
  const contentFrom = token.from + 1;
  if (quote === "'") {
    if (content.includes("'")) return null;
    return {
      text: content,
      map: Array.from({ length: content.length + 1 }, (_, index) => contentFrom + index),
      quote: 'single',
      token,
    };
  }

  let text = '';
  const map = [contentFrom];
  for (let index = 0; index < content.length; index++) {
    const char = content[index];
    if (char === '$' || char === '`' || char === '"') return null;
    if (char === '\\' && index + 1 < content.length && ['"', '\\', '$', '`'].includes(content[index + 1])) {
      text += content[++index];
      map.push(contentFrom + index + 1);
      continue;
    }
    text += char;
    map.push(contentFrom + index + 1);
  }
  return { text, map, quote: 'double', token };
}

function decodedPosition(json: ShellJSON, pos: number): number | null {
  if (pos < json.map[0] || pos > json.map[json.map.length - 1]) return null;
  let result = 0;
  for (let index = 0; index < json.map.length; index++) {
    if (json.map[index] > pos) break;
    result = index;
  }
  return result;
}

function jsonStringValue(raw: string): string | null {
  try {
    return JSON.parse(`"${raw}"`) as string;
  } catch {
    return null;
  }
}

function jsonTokens(source: string): LiteralToken[] {
  const tokens: LiteralToken[] = [];
  let index = 0;
  while (index < source.length) {
    const char = source[index];
    if (/\s/.test(char)) {
      index++;
      continue;
    }
    if (char === '"') {
      const from = index++;
      const contentFrom = index;
      let closed = false;
      while (index < source.length) {
        if (source[index] === '\\') {
          index += Math.min(2, source.length - index);
          continue;
        }
        if (source[index] === '"') {
          closed = true;
          break;
        }
        index++;
      }
      const contentTo = index;
      const raw = source.slice(contentFrom, contentTo);
      if (closed) index++;
      tokens.push({
        kind: 'string',
        text: source.slice(from, index),
        value: jsonStringValue(raw) ?? undefined,
        quote: '"',
        contentFrom,
        contentTo,
        closed,
        from,
        to: index,
      });
      continue;
    }
    if (/\d/.test(char) || char === '-') {
      const from = index++;
      while (index < source.length && /[\d.eE+-]/.test(source[index])) index++;
      tokens.push({ kind: 'number', text: source.slice(from, index), value: source.slice(from, index), from, to: index });
      continue;
    }
    const from = index++;
    tokens.push({
      kind: '{}[],:'.includes(char) ? 'punct' : 'other',
      text: char,
      from,
      to: index,
    });
  }
  return tokens;
}

function shellEncodeJSON(value: string, quote: 'single' | 'double'): string {
  if (quote === 'single') return value;
  return value.replace(/[\\"$`]/g, (char) => `\\${char}`);
}

function jsonContent(value: string): string {
  return JSON.stringify(value).slice(1, -1);
}

interface JSONContext {
  json: ShellJSON;
  tokens: LiteralToken[];
  pairs: Map<number, number>;
  pos: number;
  entry: LiteralEntry;
}

function jsonContext(parse: ShellParse, command: ShellCommand, argument: number, pos: number): JSONContext | null {
  const token = argumentToken(parse, command, argument);
  if (!token || token.from > pos || pos > token.to) return null;
  const json = decodeShellJSON(token);
  if (!json) return null;
  const decoded = decodedPosition(json, pos);
  if (decoded === null) return null;
  const tokens = jsonTokens(json.text);
  const pairs = literalPairs(tokens);
  const entry = literalEntryAt(tokens, pairs, decoded);
  return entry ? { json, tokens, pairs, pos: decoded, entry } : null;
}

function sourceSpan(json: ShellJSON, from: number, to: number): { from: number; to: number } {
  return {
    from: json.map[Math.max(0, Math.min(from, json.map.length - 1))],
    to: json.map[Math.max(0, Math.min(to, json.map.length - 1))],
  };
}

function replacementForEntry(context: JSONContext, active: LiteralToken | undefined): { from: number; to: number } {
  if (!active || active.contentFrom === undefined || active.contentTo === undefined) {
    return sourceSpan(context.json, context.pos, context.pos);
  }
  return sourceSpan(context.json, active.contentFrom, active.contentTo);
}

function memberCompletion(member: CatalogMember, insert: string, cursor?: number): CatalogCompletion {
  return {
    label: member.label,
    insert,
    type: 'property',
    detail: member.type,
    documentation: member.documentation,
    cursor,
  };
}

function newObjectCompletions(members: CatalogMember[]): CatalogCompletion[] {
  return members.map((member) => {
    const insert = shellQuote(`{"${jsonContent(member.label)}": }`);
    return memberCompletion(member, insert, insert.length - 2);
  });
}

function bashObjectCompletion(parse: ShellParse, pos: number): CatalogCompletionResult | null {
  const command = containingCommand(parse, pos, 'node');
  if (!command?.node) return null;
  const active = activeArgument(parse, command, pos);
  const sectionIndex = command.generic ? active - 2 : active - 1;
  const section = sectionIndex === 0 ? 'config' : sectionIndex === 1 ? 'inputs' : sectionIndex === 2 ? 'options' : null;
  if (!section) return null;
  const token = argumentToken(parse, command, active);
  if (!token) {
    if (pos < command.end && parse.source.slice(pos, command.end).trim()) return null;
    return { from: pos, to: pos, options: newObjectCompletions(membersForPath(command.node, section, [])) };
  }
  const context = jsonContext(parse, command, active, pos);
  if (!context || context.entry.area === 'other') return null;
  const { entry, json } = context;
  const members = membersForPath(command.node, section, entry.path);

  if (entry.area === 'key') {
    const activeToken = entry.active ?? entry.key;
    const insideString = activeToken?.kind === 'string'
      && activeToken.contentFrom !== undefined && activeToken.contentFrom <= context.pos
      && context.pos <= (activeToken.contentTo ?? -1);
    if (!insideString && entry.key) return null;
    const replacement = insideString ? replacementForEntry(context, activeToken) : sourceSpan(json, context.pos, context.pos);
    return {
      ...replacement,
      options: members.map((member) => memberCompletion(
        member,
        shellEncodeJSON(insideString
          ? jsonContent(member.label)
          : `${JSON.stringify(member.label)}${entry.colon ? '' : ': '}`, json.quote),
      )),
    };
  }

  if (!entry.key?.value) return null;
  const member = memberAtPath(members, entry.key.value);
  if (member?.field?.spec.type !== 'select' || !member.field.spec.options?.length) return null;
  const activeToken = entry.active;
  const insideString = activeToken?.kind === 'string'
    && activeToken.contentFrom !== undefined && activeToken.contentFrom <= context.pos
    && context.pos <= (activeToken.contentTo ?? -1);
  if (!insideString && entry.value) return null;
  return {
    ...(insideString ? replacementForEntry(context, activeToken) : sourceSpan(json, context.pos, context.pos)),
    options: member.field.spec.options.map((option) => ({
      label: option.value,
      insert: shellEncodeJSON(insideString ? jsonContent(option.value) : JSON.stringify(option.value), json.quote),
      type: 'constant',
      detail: option.label,
      documentation: {
        display: `${option.value}: ${option.label}`,
        documentation: member.documentation.documentation,
        tags: [],
      },
    })),
  };
}

interface Assignment {
  name: string;
  node: CatalogNode;
  at: number;
}

function assignments(parse: ShellParse): Assignment[] {
  const result: Assignment[] = [];
  for (const command of parse.commands) {
    if (command.name !== 'node' || !command.node) continue;
    const open = parse.tokens[command.nameIndex - 1];
    if (!open?.substitution || open.text !== '(') continue;
    const assignment = parse.tokens[command.nameIndex - 2];
    if (!assignment || assignment.to !== open.from || !assignment.text.endsWith('=')) continue;
    const name = assignment.text.slice(0, -1);
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) continue;
    const close = parse.pairs.get(command.nameIndex - 1);
    if (close === undefined || close < command.nameIndex) continue;
    result.push({ name, node: command.node, at: parse.tokens[close].to });
  }
  return result;
}

function directCall(token: ShellToken | undefined, projection: ScriptCatalogProjection): CatalogNode | undefined {
  if (!token) return undefined;
  let raw = token.text;
  if (raw.startsWith("'")) return undefined;
  if (raw.length >= 2 && raw[0] === '"' && raw.endsWith('"')) raw = raw.slice(1, -1);
  const start = raw.indexOf('$(');
  if (start === -1 || !raw.endsWith(')')) return undefined;
  const inner = raw.slice(start + 2, -1);
  const nested = parseShell(inner, projection);
  return nested.commands.find((command) => command.name === 'node' && command.node)?.node;
}

function jsonSourceNode(parse: ShellParse, command: ShellCommand, projection: ScriptCatalogProjection): CatalogNode | undefined {
  const sourceToken = argumentToken(parse, command, 1);
  if (!sourceToken) return undefined;
  const raw = sourceToken.text;
  const variable = raw.match(/^"?\$([A-Za-z_][A-Za-z0-9_]*)"?$/)
    ?? raw.match(/^"?\$\{([A-Za-z_][A-Za-z0-9_]*)\}"?$/);
  if (variable) {
    const assignment = assignments(parse)
      .filter((candidate) => candidate.name === variable[1] && candidate.at < sourceToken.from)
      .sort((a, b) => b.at - a.at)[0];
    if (!assignment) return undefined;
    const reassigned = parse.tokens.some((token) => token.kind === 'name'
      && token.text === `${variable[1]}=` && token.from >= assignment.at && token.from < sourceToken.from);
    return reassigned ? undefined : assignment.node;
  }
  return directCall(sourceToken, projection);
}

function traversalMembers(node: CatalogNode, segments: (string | number)[]): CatalogMember[] {
  if (segments.length === 0) return outputMembers(node);
  const outputName = segments[0];
  if (typeof outputName !== 'string') return [];
  const port = node.outputs.find((candidate) => candidate.spec.id === outputName);
  if (!port) return [];
  if (segments.length === 1) return [];
  if (typeof segments[1] !== 'number') return [];
  if (segments.length === 2) return messageMembers(port);
  if (segments[2] !== 'payload') return [];
  return schemaMembersAt(port.payload, segments.slice(3));
}

function pathSegments(path: string): (string | number)[] | null {
  if (!path) return [];
  const result: (string | number)[] = [];
  for (const segment of path.split('.')) {
    if (!segment) return null;
    result.push(/^\d+$/.test(segment) ? Number(segment) : segment);
  }
  return result;
}

export function bashDiagnostics(source: string, projection: ScriptCatalogProjection): CatalogDiagnostic[] {
  const parse = parseShell(source, projection);
  const diagnostics: CatalogDiagnostic[] = [];
  for (const command of parse.commands) {
    if (command.name !== 'jsonget') continue;
    const token = argumentToken(parse, command, 0);
    if (!token) continue;
    const node = jsonSourceNode(parse, command, projection);
    if (!node) continue;
    const path = shellStatic(token);
    if (!path) continue;
    const segments = pathSegments(path);
    if (!segments || segments.length < 4 || segments[2] !== 'payload') continue;
    const outputName = segments[0];
    if (typeof outputName !== 'string') continue;
    const port = node.outputs.find((candidate) => candidate.spec.id === outputName);
    if (!port) continue;
    const property = segments[segments.length - 1];
    if (typeof property !== 'string') continue;
    const parent = schemaAtPath(port.payload, segments.slice(3, -1));
    if (!parent?.closed || parent.properties.has(property)) continue;
    diagnostics.push({
      from: token.from,
      to: token.to,
      severity: 'error',
      code: 'node-output-unknown',
      message: `unknown output property ${JSON.stringify(property)} for node type ${JSON.stringify(node.fn.node_type)}`,
      nodeType: node.fn.node_type,
    });
  }
  return diagnostics;
}

function bashTraversalCompletion(parse: ShellParse, projection: ScriptCatalogProjection, pos: number): CatalogCompletionResult | null {
  const command = containingCommand(parse, pos, 'jsonget');
  if (!command || activeArgument(parse, command, pos) !== 0) return null;
  const node = jsonSourceNode(parse, command, projection);
  if (!node) return null;
  const token = argumentToken(parse, command, 0);
  const path = token ? shellStatic(token) : '';
  if (path === null) return null;
  const lastDot = path.lastIndexOf('.');
  const prefix = lastDot === -1 ? '' : path.slice(0, lastDot);
  const segments = pathSegments(prefix);
  if (!segments) return null;
  const members = traversalMembers(node, segments).filter((member) => member.label && !member.label.includes('.'));
  const replacement = token ? { from: token.from, to: token.to } : { from: pos, to: pos };
  return {
    ...replacement,
    options: members.map((member) => {
      const path = `${prefix}${prefix ? '.' : ''}${member.label}`;
      const first = token?.text[0];
      const quote = first === '"' || first === "'" ? first : '';
      return {
        ...memberCompletion(member, shellQuote(path)),
        filterText: `${quote}${path}`,
      };
    }),
  };
}

function bashRootCompletion(parse: ShellParse, pos: number): CatalogCompletionResult | null {
  const index = parse.tokens.findIndex((token) => token.kind === 'name' && token.from <= pos && pos <= token.to);
  if (index === -1 || !commandPosition(parse.tokens, index)) return null;
  const token = parse.tokens[index];
  return {
    from: token.from,
    to: token.to,
    options: [{
      label: 'node',
      insert: 'node',
      type: 'function',
      documentation: {
        display: 'node <name> <config-json> [inputs-json [options-json]]',
        documentation: 'Calls a server flow catalog node. Use node call with an exact type for a computed target. Results and failures are JSON documents; failures also return nonzero status.',
        tags: [],
      },
    }],
  };
}

export function bashCompletions(source: string, pos: number, projection: ScriptCatalogProjection): CatalogCompletionResult | null {
  if (pos < 0 || pos > source.length) return null;
  const parse = parseShell(source, projection);
  return bashFunctionCompletion(parse, projection, pos)
    ?? bashTraversalCompletion(parse, projection, pos)
    ?? bashObjectCompletion(parse, pos)
    ?? bashRootCompletion(parse, pos);
}

function infoAt(from: number, to: number, documentation: Omit<CatalogQuickInfo, 'from' | 'to'>): CatalogQuickInfo {
  return { ...documentation, from, to };
}

function bashObjectHover(parse: ShellParse, pos: number): CatalogQuickInfo | null {
  const command = containingCommand(parse, pos, 'node');
  if (!command?.node) return null;
  const active = activeArgument(parse, command, pos);
  const sectionIndex = command.generic ? active - 2 : active - 1;
  const section = sectionIndex === 0 ? 'config' : sectionIndex === 1 ? 'inputs' : sectionIndex === 2 ? 'options' : null;
  if (!section) return null;
  const context = jsonContext(parse, command, active, pos);
  if (!context?.entry.key?.value) return null;
  const member = memberAtPath(membersForPath(command.node, section, context.entry.path), context.entry.key.value);
  if (!member) return null;
  const token = context.entry.area === 'value' ? context.entry.active ?? context.entry.key : context.entry.key;
  if (token.from > context.pos || context.pos > token.to) return null;
  const span = sourceSpan(context.json, token.from, token.to);
  return infoAt(span.from, span.to, member.documentation);
}

function bashTraversalHover(parse: ShellParse, projection: ScriptCatalogProjection, pos: number): CatalogQuickInfo | null {
  const command = containingCommand(parse, pos, 'jsonget');
  if (!command) return null;
  const token = argumentToken(parse, command, 0);
  if (!token || token.from > pos || pos > token.to) return null;
  const node = jsonSourceNode(parse, command, projection);
  if (!node) return null;
  const path = shellStatic(token);
  if (!path) return null;
  const segments = pathSegments(path);
  if (!segments || segments.length === 0) return null;
  const last = segments[segments.length - 1];
  if (typeof last !== 'string') return null;
  const member = memberAtPath(traversalMembers(node, segments.slice(0, -1)), last);
  return member ? infoAt(token.from, token.to, member.documentation) : null;
}

export function bashQuickInfo(source: string, pos: number, projection: ScriptCatalogProjection): CatalogQuickInfo | null {
  if (pos < 0 || pos > source.length) return null;
  const parse = parseShell(source, projection);
  const command = containingCommand(parse, pos, 'node');
  if (command) {
    const call = parse.tokens[command.callIndex];
    if (call && call.from <= pos && pos <= call.to) return infoAt(call.from, call.to, genericCallDocumentation('bash'));
    const target = parse.tokens[command.targetIndex];
    if (target && target.from <= pos && pos <= target.to && command.node) {
      return infoAt(target.from, target.to, catalogFunctionDocumentation('bash', command.node));
    }
  }
  return bashTraversalHover(parse, projection, pos) ?? bashObjectHover(parse, pos);
}

export function bashSignatureHelp(source: string, pos: number, projection: ScriptCatalogProjection): CatalogSignatureHelp | null {
  if (pos < 0 || pos > source.length) return null;
  const parse = parseShell(source, projection);
  const command = containingCommand(parse, pos, 'node');
  if (!command?.node) return null;
  const target = parse.tokens[command.targetIndex] ?? parse.tokens[command.nameIndex];
  const parameters = catalogSignatureParameters('bash', command.node, command.generic);
  const argument = activeArgument(parse, command, pos);
  const active = Math.min(Math.max(0, argument - 1), parameters.length - 1);
  return {
    from: target.from,
    to: target.to,
    activeArgument: active,
    parameters,
    prefix: `node ${command.generic ? 'call ' : ''}${command.target} `,
    suffix: '  # result JSON on stdout',
    separator: ' ',
    documentation: catalogFunctionDocumentation('bash', command.node).documentation,
  };
}
