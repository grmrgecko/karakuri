import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { generateScriptSdkDeclarations } from './scriptSdk';
import { analysisFixtures } from './scriptSdkTestData';
import { ScriptAnalysisService } from './scriptAnalysisService';

const declarations = generateScriptSdkDeclarations(analysisFixtures());

function analyze(service: ScriptAnalysisService, source: string) {
  service.update(source);
  return service.diagnostics();
}

describe('script analysis service', () => {
  let service: ScriptAnalysisService;

  beforeEach(() => {
    service = new ScriptAnalysisService(declarations);
  });

  afterEach(() => {
    service.dispose();
  });

  // These five catalog mistakes must be visible without invoking a node.
  it.each([
    {
      name: 'unknown function',
      source: 'return nodes.notAFunction({});',
      codes: [2339],
      message: 'notAFunction',
    },
    {
      name: 'missing required config',
      source: 'return nodes.haGetState({ connection: "home" });',
      codes: [2345],
      message: 'entity_id',
    },
    {
      name: 'unknown config',
      source: 'return nodes.haGetState({ connection: "home", entity_id: "light.kitchen", bogus: true });',
      codes: [2353, 2345],
      message: 'bogus',
    },
    {
      name: 'wrong config type',
      source: 'return nodes.numberNode({ count: "many" });',
      codes: [2322, 2345],
      message: 'number',
    },
    {
      name: 'unknown closed output property',
      source: 'const result = nodes.haGetState({ connection: "home", entity_id: "light.kitchen" }); return result.out[0].payload.bogus;',
      codes: [2339],
      message: 'bogus',
    },
  ])('reports a localized diagnostic for $name', ({ source, codes, message }) => {
    const diagnostics = analyze(service, source);
    const diagnostic = diagnostics.find((item) => item.message.includes(message) && codes.includes(item.code));
    expect(diagnostic, JSON.stringify(diagnostics)).toBeDefined();
    expect(diagnostic!.from).toBeGreaterThanOrEqual(0);
    expect(diagnostic!.to).toBeLessThanOrEqual(source.length);
    expect(diagnostic!.severity).toBe('error');
  });

  // A required message input is an argument, not an optional value parameter.
  it('requires inputs only for nodes with required message ports', () => {
    const missing = analyze(service, 'nodes.base64Encode({ property: "payload", target: "payload" });');
    expect(missing.some((diagnostic) => [2554, 2345].includes(diagnostic.code) && diagnostic.message.includes('argument'))).toBe(true);
    expect(analyze(service, 'nodes.haGetState({ connection: "home", entity_id: "light.kitchen" });')).toEqual([]);
  });

  // Function entries and their lazy detail text must come from the same catalog.
  it('completes functions and loads documentation only for a selected entry', () => {
    const source = 'nodes.';
    service.update(source);
    const completions = service.completions(source.length);
    const names = completions?.entries.map((entry) => entry.name) ?? [];
    expect(names).toContain('base64-encode');
    expect(names).toContain('base64Encode');
    expect(names).toContain('haGetState');
    expect(names).toContain('call');

    const entry = completions?.entries.find((item) => item.name === 'base64Encode');
    expect(entry).toBeDefined();
    const details = service.completionDetails(source.length, entry!);
    expect(details?.display).toContain('base64Encode');
    expect(details?.display).toContain('config:');
    expect(details?.documentation).toContain('Base64 Encode');
  });

  // Output member completion is driven by payload_schema, not a node-specific editor list.
  it('completes returned payload properties from the output schema', () => {
    const source = 'const state = nodes.haGetState({ connection: "home", entity_id: "light.kitchen" });\nstate.out[0].payload.';
    service.update(source);
    const names = service.completions(source.length)?.entries.map((entry) => entry.name) ?? [];
    expect(names).toEqual(expect.arrayContaining(['entity_id', 'state', 'attributes', 'last_changed', 'last_updated']));
    expect(names).not.toContain('old_state');
  });

  // Hover and signature requests share the same catalog JSDoc and source mapping.
  it('returns hover documentation and signature help with the active argument', () => {
    const hoverSource = 'const read = nodes.haGetState;';
    service.update(hoverSource);
    const hover = service.quickInfo(hoverSource.indexOf('haGetState') + 2);
    expect(hover?.display).toContain('haGetState');
    expect(hover?.documentation).toContain('HA Get State');

    const signatureSource = 'nodes.base64Encode({ property: "payload", target: "payload", encoding: "standard", padding: true }, { in: msg }, { });';
    service.update(signatureSource);
    const signature = service.signatureHelp(signatureSource.indexOf('{ }') + 1);
    expect(signature?.activeArgument).toBe(2);
    expect(signature?.items[0].parameters.map((parameter) => parameter.name)).toEqual(['config', 'inputs', 'options']);
    expect(signature?.items[0].prefix).toContain('base64Encode');
  });

  // The virtual wrapper permits the sandbox's top-level return while ES globals remain available.
  it('accepts top-level return and locally bundled ES libraries', () => {
    const source = 'const values = new Map([["a", 1]]); return { json: JSON.stringify(Object.entries({ a: values.get("a") })), set: new Set([1, 2]) };';
    expect(analyze(service, source)).toEqual([]);
  });

  // Browser and Node declarations would describe capabilities goja does not install.
  it('does not expose browser or host-runtime globals', () => {
    const names = ['fetch', 'window', 'document', 'process', 'Buffer', 'require', 'setTimeout', 'structuredClone', 'TextEncoder'];
    const diagnostics = analyze(service, names.map((name) => `${name};`).join('\n'));
    for (const name of names) {
      expect(diagnostics.some((diagnostic) => diagnostic.message.includes(`'${name}'`)), name).toBe(true);
    }

    service.update('');
    const completions = service.completions(0)?.entries.map((entry) => entry.name) ?? [];
    expect(completions).toContain('JSON');
    for (const name of names) expect(completions).not.toContain(name);
  });

  // Missing detail must not become a closed type and reject valid dynamic payloads.
  it('keeps broad, open, and dynamic payloads free of invented property errors', () => {
    const source = `
const broad = nodes.broadNode({});
const open = nodes.openNode({});
const dynamic = nodes.dynamicNode({});
return [broad.out[0].payload.anything.deep, open.out[0].payload.anything, dynamic.other[0].payload.value];
`;
    expect(analyze(service, source)).toEqual([]);
  });

  // TypeScript and CodeMirror both count the emoji as two UTF-16 code units.
  it('maps diagnostics and hover spans through emoji using UTF-16 offsets', () => {
    const source = 'const face = "😀";\nconst result = nodes.haGetState({ connection: "home", entity_id: "light.kitchen" });\nresult.out[0].payload.missing;';
    const missing = source.indexOf('missing');
    const diagnostic = analyze(service, source).find((item) => item.code === 2339 && item.message.includes('missing'));
    expect(diagnostic?.from).toBe(missing);
    expect(diagnostic?.to).toBe(missing + 'missing'.length);

    const hover = service.quickInfo(source.indexOf('haGetState') + 2);
    expect(hover?.from).toBe(source.indexOf('haGetState'));
    expect(hover?.to).toBe(source.indexOf('haGetState') + 'haGetState'.length);
  });

  // A comment terminator in contributed metadata must not create a real global.
  it('does not let hostile catalog comments inject declarations', () => {
    const [fn] = analysisFixtures();
    fn.meta.label = 'Broken */ declare const injected: string; /* metadata';
    const local = new ScriptAnalysisService(generateScriptSdkDeclarations([fn]), 'injected;');
    try {
      expect(local.diagnostics().some((diagnostic) => diagnostic.message.includes("'injected'"))).toBe(true);
    } finally {
      local.dispose();
    }
  });
});
