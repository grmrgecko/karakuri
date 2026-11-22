export interface LiteralToken {
  kind: 'name' | 'string' | 'number' | 'punct' | 'other';
  text: string;
  from: number;
  to: number;
  value?: string;
  quote?: string;
  contentFrom?: number;
  contentTo?: number;
  closed?: boolean;
}

export interface LiteralEntry {
  openIndex: number;
  closeIndex?: number;
  path: string[];
  area: 'key' | 'value' | 'other';
  key?: LiteralToken;
  colon?: LiteralToken;
  value?: LiteralToken;
  active?: LiteralToken;
}

const opening: Record<string, string> = { '(': ')', '[': ']', '{': '}' };

export function literalPairs(tokens: LiteralToken[]): Map<number, number> {
  const pairs = new Map<number, number>();
  const stacks: Record<string, number[]> = { '(': [], '[': [], '{': [] };
  for (let index = 0; index < tokens.length; index++) {
    const text = tokens[index].text;
    if (text in opening) {
      stacks[text].push(index);
      continue;
    }
    for (const [open, close] of Object.entries(opening)) {
      if (text !== close) continue;
      const start = stacks[open].pop();
      if (start !== undefined) {
        pairs.set(start, index);
        pairs.set(index, start);
      }
      break;
    }
  }
  return pairs;
}

function contains(tokens: LiteralToken[], openIndex: number, closeIndex: number | undefined, pos: number): boolean {
  return tokens[openIndex].from < pos && (closeIndex === undefined || pos < tokens[closeIndex].to);
}

function parentPath(tokens: LiteralToken[], pairs: Map<number, number>, openIndex: number): string[] {
  if (openIndex < 2 || tokens[openIndex - 1].text !== ':') return [];
  const key = tokens[openIndex - 2];
  if (key.kind !== 'string' || key.value === undefined) return [];
  let parent: number | undefined;
  for (let index = 0; index < openIndex - 2; index++) {
    if (tokens[index].text !== '{') continue;
    const close = pairs.get(index);
    if (close !== undefined && close < openIndex) continue;
    parent = index;
  }
  return [...(parent === undefined ? [] : parentPath(tokens, pairs, parent)), key.value];
}

function directTokens(tokens: LiteralToken[], pairs: Map<number, number>, openIndex: number, closeIndex: number): number[] {
  const result: number[] = [];
  for (let index = openIndex + 1; index < closeIndex; index++) {
    result.push(index);
    const close = pairs.get(index);
    if (close !== undefined && ['{', '[', '('].includes(tokens[index].text) && close < closeIndex) {
      index = close;
    }
  }
  return result;
}

// literalEntryAt recognizes only object-key and value positions. It does not
// decide whether the surrounding source is valid Python, JSON, or shell.
export function literalEntryAt(
  tokens: LiteralToken[],
  pairs: Map<number, number>,
  pos: number,
  minimum = 0,
  maximum = tokens.length,
): LiteralEntry | null {
  let selected: { open: number; close?: number } | null = null;
  for (let index = Math.max(0, minimum); index < Math.min(tokens.length, maximum); index++) {
    if (tokens[index].text !== '{') continue;
    const paired = pairs.get(index);
    const close = paired !== undefined && paired > index ? paired : undefined;
    if (close !== undefined && close >= maximum) continue;
    if (!contains(tokens, index, close, pos)) continue;
    selected = { open: index, close };
  }
  if (!selected) return null;

  const { open, close } = selected;
  const last = close ?? Math.min(tokens.length, maximum);
  const direct = directTokens(tokens, pairs, open, last);
  const commas = direct.filter((index) => tokens[index].text === ',');
  const prior = commas.filter((index) => tokens[index].from < pos);
  const next = commas.find((index) => tokens[index].from >= pos);
  const entryIndexes = direct.filter((index) => {
    const afterPrior = prior.length === 0 || index > prior[prior.length - 1];
    return afterPrior && (next === undefined || index < next);
  });
  const colonIndex = entryIndexes.find((index) => tokens[index].text === ':');
  const keyIndex = entryIndexes.find((index) => tokens[index].kind === 'string' && (colonIndex === undefined || index < colonIndex));
  const valueIndex = colonIndex === undefined ? undefined : entryIndexes.find((index) => index > colonIndex);
  const active = tokens.find((token) => token.kind === 'string'
    && token.contentFrom !== undefined && token.contentTo !== undefined
    && token.contentFrom <= pos && pos <= token.contentTo
    && token.from >= tokens[open].from && (close === undefined || token.to <= tokens[close].to));
  let area: LiteralEntry['area'] = colonIndex === undefined || pos <= tokens[colonIndex].from ? 'key' : 'value';
  if (area === 'key' && keyIndex === undefined && entryIndexes.length > 0) area = 'other';
  return {
    openIndex: open,
    closeIndex: close,
    path: parentPath(tokens, pairs, open),
    area,
    key: keyIndex === undefined ? undefined : tokens[keyIndex],
    colon: colonIndex === undefined ? undefined : tokens[colonIndex],
    value: valueIndex === undefined ? undefined : tokens[valueIndex],
    active,
  };
}

export function stringReplacement(token: LiteralToken | undefined, pos: number): { from: number; to: number } {
  if (!token || token.contentFrom === undefined || token.contentTo === undefined) return { from: pos, to: pos };
  return { from: token.contentFrom, to: token.contentTo };
}
