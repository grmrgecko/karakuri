import { literalEntryAt, literalPairs, stringReplacement, type LiteralToken } from './catalogLiteral';
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

const pythonKeywords = new Set([
  'False', 'None', 'True', 'and', 'as', 'assert', 'async', 'await', 'break',
  'class', 'continue', 'def', 'del', 'elif', 'else', 'except', 'finally',
  'for', 'from', 'global', 'if', 'import', 'in', 'is', 'lambda', 'nonlocal',
  'not', 'or', 'pass', 'raise', 'return', 'try', 'while', 'with', 'yield',
]);

export function validPythonDotAlias(name: string): boolean {
  return /^[_\p{ID_Start}][_\p{ID_Continue}]*$/u.test(name) && !pythonKeywords.has(name);
}

function pythonStringValue(raw: string): string | null {
  let value = '';
  for (let index = 0; index < raw.length; index++) {
    const char = raw[index];
    if (char !== '\\') {
      value += char;
      continue;
    }
    if (++index >= raw.length) return null;
    const escaped = raw[index];
    const simple: Record<string, string> = {
      '\\': '\\', "'": "'", '"': '"', n: '\n', r: '\r', t: '\t',
    };
    if (!(escaped in simple)) return null;
    value += simple[escaped];
  }
  return value;
}

function pythonTokens(source: string): LiteralToken[] {
  const tokens: LiteralToken[] = [];
  let index = 0;
  while (index < source.length) {
    const char = source[index];
    if (/\s/.test(char)) {
      index++;
      continue;
    }
    if (char === '#') {
      const newline = source.indexOf('\n', index + 1);
      index = newline === -1 ? source.length : newline + 1;
      continue;
    }
    if (char === '"' || char === "'") {
      const quote = char;
      const triple = source.slice(index, index + 3) === quote.repeat(3);
      const delimiter = triple ? quote.repeat(3) : quote;
      const from = index;
      const contentFrom = index + delimiter.length;
      index = contentFrom;
      let closed = false;
      while (index < source.length) {
        if (!triple && (source[index] === '\n' || source[index] === '\r')) break;
        if (source[index] === '\\') {
          index += Math.min(2, source.length - index);
          continue;
        }
        if (source.startsWith(delimiter, index)) {
          closed = true;
          break;
        }
        index++;
      }
      const contentTo = index;
      const raw = source.slice(contentFrom, contentTo);
      if (closed) index += delimiter.length;
      tokens.push({
        kind: 'string',
        text: source.slice(from, index),
        from,
        to: index,
        value: pythonStringValue(raw) ?? undefined,
        quote,
        contentFrom,
        contentTo,
        closed,
      });
      continue;
    }
    if (/[_\p{ID_Start}]/u.test(char)) {
      const from = index++;
      while (index < source.length && /[_\p{ID_Continue}]/u.test(source[index])) index++;
      tokens.push({ kind: 'name', text: source.slice(from, index), from, to: index });
      continue;
    }
    if (/\d/.test(char)) {
      const from = index++;
      while (index < source.length && /[\d.]/.test(source[index])) index++;
      tokens.push({
        kind: 'number',
        text: source.slice(from, index),
        value: source.slice(from, index),
        from,
        to: index,
      });
      continue;
    }
    tokens.push({
      kind: '()[]{},: .='.includes(char) ? 'punct' : 'other',
      text: char,
      from: index,
      to: ++index,
    });
  }
  return tokens;
}

interface PythonCall {
  node?: CatalogNode;
  generic: boolean;
  bracket: boolean;
  prefixIndex: number;
  targetIndex: number;
  openIndex: number;
  closeIndex?: number;
  target: string;
}

interface PythonReference {
  node?: CatalogNode;
  token: LiteralToken;
  generic: boolean;
}

interface PythonParse {
  tokens: LiteralToken[];
  pairs: Map<number, number>;
  calls: PythonCall[];
  references: PythonReference[];
}

function nodesPrefix(tokens: LiteralToken[], index: number): boolean {
  if (tokens[index]?.kind !== 'name' || tokens[index].text !== 'nodes') return false;
  if (tokens[index - 1]?.text === '.') {
    return tokens[index - 2]?.kind === 'name' && tokens[index - 2].text === 'karakuri'
      && tokens[index - 3]?.text !== '.';
  }
  return tokens[index - 1]?.text !== '.';
}

function parsePython(source: string, projection: ScriptCatalogProjection): PythonParse {
  const tokens = pythonTokens(source);
  const pairs = literalPairs(tokens);
  const calls: PythonCall[] = [];
  const references: PythonReference[] = [];

  for (let index = 0; index < tokens.length; index++) {
    if (!nodesPrefix(tokens, index)) continue;
    let targetIndex = -1;
    let generic = false;
    let bracket = false;
    let node: CatalogNode | undefined;
    let target = '';
    let after = -1;

    if (tokens[index + 1]?.text === '.' && tokens[index + 2]?.kind === 'name') {
      targetIndex = index + 2;
      target = tokens[targetIndex].text;
      generic = target === 'call';
      if (generic || validPythonDotAlias(target)) {
        node = generic ? undefined : projection.byAlias.get(target);
        references.push({ node, token: tokens[targetIndex], generic });
        after = targetIndex + 1;
      }
    } else if (tokens[index + 1]?.text === '[' && tokens[index + 2]?.kind === 'string' && tokens[index + 3]?.text === ']') {
      targetIndex = index + 2;
      bracket = true;
      target = tokens[targetIndex].value ?? '';
      node = target ? projection.byType.get(target) : undefined;
      references.push({ node, token: tokens[targetIndex], generic: false });
      after = index + 4;
    }
    if (after === -1 || tokens[after]?.text !== '(') continue;

    const openIndex = after;
    const paired = pairs.get(openIndex);
    const closeIndex = paired !== undefined && paired > openIndex ? paired : undefined;
    if (generic) {
      targetIndex = openIndex + 1;
      const targetToken = tokens[targetIndex];
      target = targetToken?.kind === 'string' ? targetToken.value ?? '' : '';
      node = target ? projection.byType.get(target) : undefined;
      if (targetToken?.kind === 'string') references.push({ node, token: targetToken, generic: false });
    }
    calls.push({
      node,
      generic,
      bracket,
      prefixIndex: tokens[index - 1]?.text === '.' ? index - 2 : index,
      targetIndex,
      openIndex,
      closeIndex,
      target,
    });
  }
  return { tokens, pairs, calls, references };
}

function containingCall(parse: PythonParse, pos: number): PythonCall | undefined {
  let selected: PythonCall | undefined;
  for (const call of parse.calls) {
    const open = parse.tokens[call.openIndex];
    const close = call.closeIndex === undefined ? undefined : parse.tokens[call.closeIndex];
    if (open.from >= pos || (close && pos > close.from)) continue;
    if (!selected || open.from > parse.tokens[selected.openIndex].from) selected = call;
  }
  return selected;
}

function activeArgument(parse: PythonParse, call: PythonCall, pos: number): number {
  const end = call.closeIndex ?? parse.tokens.length;
  const nesting = { '(': 0, '[': 0, '{': 0 };
  const closing: Record<string, keyof typeof nesting> = { ')': '(', ']': '[', '}': '{' };
  let argument = 0;
  for (let index = call.openIndex + 1; index < end; index++) {
    const token = parse.tokens[index];
    if (token.from >= pos) break;
    if (token.text in nesting) {
      nesting[token.text as keyof typeof nesting]++;
      continue;
    }
    if (token.text in closing) {
      const open = closing[token.text];
      nesting[open] = Math.max(0, nesting[open] - 1);
      continue;
    }
    if (token.text === ',' && Object.values(nesting).every((depth) => depth === 0)) argument++;
  }
  return argument;
}

function pythonStringContent(value: string, quote = '"'): string {
  let result = '';
  for (const char of value) {
    if (char === '\\' || char === quote) result += '\\';
    if (char === '\n') result += '\\n';
    else if (char === '\r') result += '\\r';
    else if (char === '\t') result += '\\t';
    else result += char;
  }
  return result;
}

function memberCompletion(
  member: CatalogMember,
  insert: string,
  type = 'property',
  cursor?: number,
): CatalogCompletion {
  return {
    label: member.label,
    insert,
    type,
    detail: member.type,
    documentation: member.documentation,
    cursor,
  };
}

function functionCompletions(
  nodes: CatalogNode[],
  insert: (name: string) => string,
): CatalogCompletion[] {
  return nodes.map((node) => ({
    label: node.fn.name,
    insert: insert(node.fn.name),
    type: 'function',
    detail: node.fn.node_type,
    documentation: catalogFunctionDocumentation('python', node),
  }));
}

function pythonFunctionCompletion(parse: PythonParse, projection: ScriptCatalogProjection, pos: number): CatalogCompletionResult | null {
  const { tokens } = parse;
  for (let index = 0; index < tokens.length; index++) {
    if (!nodesPrefix(tokens, index)) continue;
    if (tokens[index + 1]?.text === '.') {
      const name = tokens[index + 2];
      const dot = tokens[index + 1];
      if (name?.kind === 'name' ? name.from <= pos && pos <= name.to : dot.from < pos && pos <= dot.to) {
        const options: CatalogCompletion[] = [];
        const seen = new Set<string>();
        for (const node of projection.nodes) {
          for (const alias of node.fn.aliases ?? []) {
            if (!validPythonDotAlias(alias) || alias === 'call' || seen.has(alias)) continue;
            seen.add(alias);
            options.push({
              label: alias,
              insert: alias,
              type: 'function',
              detail: node.fn.node_type,
              documentation: catalogFunctionDocumentation('python', node),
            });
          }
        }
        options.push({
          label: 'call',
          insert: 'call',
          type: 'function',
          detail: 'dynamic node call',
          documentation: genericCallDocumentation('python'),
        });
        return { from: name?.from ?? pos, to: name?.to ?? pos, options };
      }
    }
    if (tokens[index + 1]?.text === '[' && tokens[index + 2]?.kind === 'string') {
      const token = tokens[index + 2];
      if (token.contentFrom !== undefined && token.contentTo !== undefined
        && token.contentFrom <= pos && pos <= token.contentTo) {
        return {
          ...stringReplacement(token, pos),
          options: functionCompletions(projection.nodes, (name) => pythonStringContent(name, token.quote)),
        };
      }
    }
  }

  const call = containingCall(parse, pos);
  if (call?.generic && activeArgument(parse, call, pos) === 0) {
    const token = parse.tokens[call.targetIndex];
    if (token?.kind === 'string') {
      return {
        ...stringReplacement(token, pos),
        options: functionCompletions(projection.nodes, (name) => pythonStringContent(name, token.quote)),
      };
    }
    return { from: pos, to: pos, options: functionCompletions(projection.nodes, (name) => JSON.stringify(name)) };
  }
  return null;
}

function pythonObjectCompletion(parse: PythonParse, pos: number): CatalogCompletionResult | null {
  const call = containingCall(parse, pos);
  if (!call?.node) return null;
  const argument = activeArgument(parse, call, pos);
  const sectionIndex = call.generic ? argument - 1 : argument;
  const section = sectionIndex === 0 ? 'config' : sectionIndex === 1 ? 'inputs' : sectionIndex === 2 ? 'options' : null;
  if (!section) return null;
  const entry = literalEntryAt(parse.tokens, parse.pairs, pos, call.openIndex + 1, call.closeIndex ?? parse.tokens.length);
  if (!entry || entry.area === 'other') return null;
  const members = membersForPath(call.node, section, entry.path);

  if (entry.area === 'key') {
    const active = entry.active ?? entry.key;
    const insideString = active?.kind === 'string'
      && active.contentFrom !== undefined && active.contentFrom <= pos && pos <= (active.contentTo ?? -1);
    if (!insideString && entry.key) return null;
    const replacement = insideString ? stringReplacement(active, pos) : { from: pos, to: pos };
    return {
      ...replacement,
      options: members.map((member) => memberCompletion(
        member,
        insideString
          ? pythonStringContent(member.label, active.quote)
          : `${JSON.stringify(member.label)}${entry.colon ? '' : ': '}`,
      )),
    };
  }

  if (!entry.key?.value) return null;
  const member = memberAtPath(members, entry.key.value);
  if (member?.field?.spec.type !== 'select' || !member.field.spec.options?.length) return null;
  const active = entry.active;
  const insideString = active?.kind === 'string'
    && active.contentFrom !== undefined && active.contentFrom <= pos && pos <= (active.contentTo ?? -1);
  if (!insideString && entry.value) return null;
  return {
    ...(insideString ? stringReplacement(active, pos) : { from: pos, to: pos }),
    options: member.field.spec.options.map((option) => ({
      label: option.value,
      insert: insideString ? pythonStringContent(option.value, active.quote) : JSON.stringify(option.value),
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

function assignments(parse: PythonParse): Assignment[] {
  const result: Assignment[] = [];
  for (const call of parse.calls) {
    if (!call.node || call.closeIndex === undefined) continue;
    if (parse.tokens[call.prefixIndex - 1]?.text !== '=') continue;
    const name = parse.tokens[call.prefixIndex - 2];
    if (name?.kind !== 'name') continue;
    result.push({ name: name.text, node: call.node, at: parse.tokens[call.closeIndex].to });
  }
  return result;
}

function traversalBase(parse: PythonParse, openIndex: number, pos: number): { node: CatalogNode; segments: (string | number)[] } | null {
  const segments: (string | number)[] = [];
  let index = openIndex - 1;
  while (parse.tokens[index]?.text === ']') {
    const open = parse.pairs.get(index);
    if (open === undefined || open >= index) return null;
    const values = parse.tokens.slice(open + 1, index);
    if (values.length !== 1) return null;
    const token = values[0];
    if (token.kind === 'string' && token.value !== undefined) segments.unshift(token.value);
    else if (token.kind === 'number' && /^\d+$/.test(token.text)) segments.unshift(Number(token.text));
    else return null;
    index = open - 1;
  }

  let node: CatalogNode | undefined;
  const base = parse.tokens[index];
  if (base?.kind === 'name') {
    const assignment = assignments(parse)
      .filter((candidate) => candidate.name === base.text && candidate.at < base.from && candidate.at < pos)
      .sort((a, b) => b.at - a.at)[0];
    if (assignment) {
      const reassigned = parse.tokens.some((token, tokenIndex) => token.kind === 'name'
        && token.text === base.text && token.from >= assignment.at && token.from < base.from
        && parse.tokens[tokenIndex + 1]?.text === '=');
      if (reassigned) return null;
    }
    node = assignment?.node;
  } else if (base?.text === ')') {
    const open = parse.pairs.get(index);
    const call = parse.calls.find((candidate) => candidate.openIndex === open && candidate.closeIndex === index);
    node = call?.node;
  }
  return node ? { node, segments } : null;
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

export function pythonDiagnostics(source: string, projection: ScriptCatalogProjection): CatalogDiagnostic[] {
  const parse = parsePython(source, projection);
  const diagnostics: CatalogDiagnostic[] = [];
  for (let index = 0; index < parse.tokens.length; index++) {
    if (parse.tokens[index].text !== '[') continue;
    const key = parse.tokens[index + 1];
    if (key?.kind !== 'string' || !key.value) continue;
    const base = traversalBase(parse, index, key.from);
    if (!base || base.segments.length < 3 || base.segments[2] !== 'payload') continue;
    const outputName = base.segments[0];
    if (typeof outputName !== 'string') continue;
    const port = base.node.outputs.find((candidate) => candidate.spec.id === outputName);
    if (!port) continue;
    const parent = schemaAtPath(port.payload, base.segments.slice(3));
    if (!parent?.closed || parent.properties.has(key.value)) continue;
    diagnostics.push({
      from: key.from,
      to: key.to,
      severity: 'error',
      code: 'node-output-unknown',
      message: `unknown output property ${JSON.stringify(key.value)} for node type ${JSON.stringify(base.node.fn.node_type)}`,
      nodeType: base.node.fn.node_type,
    });
  }
  return diagnostics;
}

function pythonTraversalCompletion(parse: PythonParse, pos: number): CatalogCompletionResult | null {
  const { tokens } = parse;
  for (let index = 0; index < tokens.length; index++) {
    if (tokens[index].text !== '[') continue;
    const key = tokens[index + 1];
    if (key?.kind !== 'string' || key.contentFrom === undefined || key.contentTo === undefined
      || key.contentFrom > pos || pos > key.contentTo) continue;
    const base = traversalBase(parse, index, pos);
    if (!base) continue;
    const members = traversalMembers(base.node, base.segments);
    return {
      ...stringReplacement(key, pos),
      options: members.map((member) => memberCompletion(member, pythonStringContent(member.label, key.quote))),
    };
  }
  return null;
}

function pythonRootCompletion(parse: PythonParse, pos: number): CatalogCompletionResult | null {
  if (containingCall(parse, pos)) return null;
  const index = parse.tokens.findIndex((token) => token.kind === 'name' && token.from <= pos && pos <= token.to);
  if (index === -1 || parse.tokens[index - 1]?.text === '.') return null;
  const token = parse.tokens[index];
  const documentation = {
    display: 'nodes: flow node catalog',
    documentation: 'Server flow node functions. Use an alias attribute, an exact type in brackets, or nodes.call with an exact type. The same catalog is available as karakuri.nodes.',
    tags: [],
  };
  return {
    from: token.from,
    to: token.to,
    options: [
      { label: 'nodes', insert: 'nodes', type: 'namespace', documentation },
      { label: 'karakuri.nodes', insert: 'karakuri.nodes', type: 'namespace', documentation },
    ],
  };
}

export function pythonCompletions(source: string, pos: number, projection: ScriptCatalogProjection): CatalogCompletionResult | null {
  if (pos < 0 || pos > source.length) return null;
  const parse = parsePython(source, projection);
  return pythonFunctionCompletion(parse, projection, pos)
    ?? pythonTraversalCompletion(parse, pos)
    ?? pythonObjectCompletion(parse, pos)
    ?? pythonRootCompletion(parse, pos);
}

function infoAt(token: LiteralToken, documentation: CatalogQuickInfo | Omit<CatalogQuickInfo, 'from' | 'to'>): CatalogQuickInfo {
  return { ...documentation, from: token.from, to: token.to };
}

function pythonObjectHover(parse: PythonParse, pos: number): CatalogQuickInfo | null {
  const call = containingCall(parse, pos);
  if (!call?.node) return null;
  const argument = activeArgument(parse, call, pos);
  const sectionIndex = call.generic ? argument - 1 : argument;
  const section = sectionIndex === 0 ? 'config' : sectionIndex === 1 ? 'inputs' : sectionIndex === 2 ? 'options' : null;
  if (!section) return null;
  const entry = literalEntryAt(parse.tokens, parse.pairs, pos, call.openIndex + 1, call.closeIndex ?? parse.tokens.length);
  if (!entry?.key?.value) return null;
  const members = membersForPath(call.node, section, entry.path);
  const member = memberAtPath(members, entry.key.value);
  if (!member) return null;
  if (entry.key.from <= pos && pos <= entry.key.to) return infoAt(entry.key, member.documentation);
  if (entry.area === 'value' && entry.active && entry.active.from <= pos && pos <= entry.active.to) {
    return infoAt(entry.active, member.documentation);
  }
  return null;
}

function pythonTraversalHover(parse: PythonParse, pos: number): CatalogQuickInfo | null {
  const { tokens } = parse;
  for (let index = 0; index < tokens.length; index++) {
    if (tokens[index].text !== '[') continue;
    const key = tokens[index + 1];
    if (key?.kind !== 'string' || key.from > pos || pos > key.to) continue;
    const base = traversalBase(parse, index, pos);
    if (!base || !key.value) continue;
    const member = memberAtPath(traversalMembers(base.node, base.segments), key.value);
    if (member) return infoAt(key, member.documentation);
  }
  return null;
}

export function pythonQuickInfo(source: string, pos: number, projection: ScriptCatalogProjection): CatalogQuickInfo | null {
  if (pos < 0 || pos > source.length) return null;
  const parse = parsePython(source, projection);
  const reference = parse.references.find((candidate) => candidate.token.from <= pos && pos <= candidate.token.to);
  if (reference?.generic) return infoAt(reference.token, genericCallDocumentation('python'));
  if (reference?.node) return infoAt(reference.token, catalogFunctionDocumentation('python', reference.node));
  return pythonTraversalHover(parse, pos) ?? pythonObjectHover(parse, pos);
}

export function pythonSignatureHelp(source: string, pos: number, projection: ScriptCatalogProjection): CatalogSignatureHelp | null {
  if (pos < 0 || pos > source.length) return null;
  const parse = parsePython(source, projection);
  const call = containingCall(parse, pos);
  if (!call?.node) return null;
  const target = parse.tokens[call.targetIndex];
  const open = parse.tokens[call.openIndex];
  const parameters = catalogSignatureParameters('python', call.node, call.generic);
  const argumentIndex = Math.min(activeArgument(parse, call, pos), parameters.length - 1);
  const targetText = call.generic ? 'nodes.call' : call.bracket
    ? `nodes[${JSON.stringify(call.target)}]`
    : `nodes.${call.target}`;
  return {
    from: target?.from ?? open.from,
    to: open.to,
    activeArgument: argumentIndex,
    parameters,
    prefix: `${targetText}(`,
    suffix: ') -> dict[str, list[dict]]',
    separator: ', ',
    documentation: catalogFunctionDocumentation('python', call.node).documentation,
  };
}
