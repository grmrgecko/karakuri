import { describe, expect, it } from 'vitest';
import { analysisFixtures } from './scriptSdkTestData';
import { ScriptCatalogAnalysis } from './scriptCatalogAnalysis';

function labels(result: ReturnType<ScriptCatalogAnalysis['completions']>): string[] {
  return result?.options.map((option) => option.label) ?? [];
}

describe('Python catalog analysis', () => {
  // Python attributes accept only identifiers, while item and generic calls retain exact registry IDs.
  it('completes valid dot aliases, exact types, config keys, and select values', () => {
    const functions = analysisFixtures();
    functions[0].aliases = [...(functions[0].aliases ?? []), 'class', 'bad-alias', 'valid_alias'];
    const analysis = new ScriptCatalogAnalysis('python', functions);

    expect(labels(analysis.completions('nod', 3))).toEqual(['nodes', 'karakuri.nodes']);
    const dot = labels(analysis.completions('nodes.', 6));
    expect(dot).toEqual(expect.arrayContaining(['base64Encode', 'haGetState', 'valid_alias', 'call']));
    expect(dot).not.toContain('base64-encode');
    expect(dot).not.toContain('class');
    expect(dot).not.toContain('bad-alias');
    expect(labels(analysis.completions('nodes["', 7))).toContain('base64-encode');
    expect(labels(analysis.completions('nodes.call("', 12))).toContain('ha-get-state');

    const configSource = 'nodes.base64Encode({"';
    expect(labels(analysis.completions(configSource, configSource.length))).toEqual(
      expect.arrayContaining(['property', 'target', 'encoding', 'line_width']),
    );
    const valueSource = 'nodes.base64Encode({"property":"payload","target":"payload","encoding":"';
    const values = analysis.completions(valueSource, valueSource.length);
    expect(labels(values)).toEqual(['standard', 'url']);
    expect(values?.options[0].insert).toBe('standard');
    const computedValue = 'nodes.base64Encode({"encoding": choice';
    expect(analysis.completions(computedValue, computedValue.length)).toBeNull();

    const inputSource = 'nodes.base64Encode({"property":"payload","target":"payload"}, {"';
    expect(labels(analysis.completions(inputSource, inputSource.length))).toEqual(['in']);
    const optionSource = 'nodes.haGetState({"connection":"home","entity_id":"light.kitchen"}, None, {"params": {"';
    expect(labels(analysis.completions(optionSource, optionSource.length))).toEqual(['entity_id']);

    expect(analysis.completions('# nodes.', 9)).toBeNull();
    expect(analysis.completions('"nodes."', 7)).toBeNull();
  });

  // Python and Bash share one projection of which config fields a call may
  // supply through options.params. It mirrors flow.FieldBindable, which counts
  // an ha-target field; the projection left it out, so the Home Assistant
  // nodes' areas, floors, devices, and labels were never offered there.
  it('offers an ha-target field as a value parameter', () => {
    const functions = analysisFixtures();
    functions[0].meta.config_spec = [
      ...(functions[0].meta.config_spec ?? []),
      { id: 'area_id', label: 'Areas', type: 'ha-target', required: false },
      { id: 'rules', label: 'Rules', type: 'rules', required: false },
    ];
    const analysis = new ScriptCatalogAnalysis('python', functions);

    const source = 'nodes.haGetState({"connection":"home"}, None, {"params": {"';
    expect(labels(analysis.completions(source, source.length))).toEqual(['entity_id', 'area_id']);
    const hoverSource = 'nodes.haGetState({"area_id":"kitchen"})';
    expect(analysis.quickInfo(hoverSource, hoverSource.indexOf('area_id') + 2)?.display).toBe('area_id: string');
  });

  // Returned dictionaries use stored port and payload-schema keys, with no invented broad-payload members.
  it.each([
    {
      name: 'direct assignment',
      source: 'state = nodes.haGetState({"connection":"home","entity_id":"light.kitchen"})\nstate["out"][0]["payload"]["',
    },
    {
      name: 'direct call',
      source: 'nodes.haGetState({"connection":"home","entity_id":"light.kitchen"})["out"][0]["payload"]["',
    },
  ])('completes closed output members from a $name', ({ source }) => {
    const analysis = new ScriptCatalogAnalysis('python', analysisFixtures());
    const result = analysis.completions(source, source.length);
    expect(labels(result)).toEqual(expect.arrayContaining(['entity_id', 'state', 'attributes', 'last_changed', 'last_updated']));
    expect(labels(result)).not.toContain('old_state');

    const broad = 'broad = nodes.broadNode({})\nbroad["out"][0]["payload"]["';
    expect(labels(analysis.completions(broad, broad.length))).toEqual([]);
  });

  // Closed output schemas diagnose only statically traceable unknown properties.
  it('diagnoses unknown closed output properties without guessing broad payloads', () => {
    const analysis = new ScriptCatalogAnalysis('python', analysisFixtures());
    const source = 'state = nodes.haGetState({"connection":"home","entity_id":"light.kitchen"})\nstate["out"][0]["payload"]["statee"]';
    const diagnostics = analysis.diagnostics(source);
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0]).toMatchObject({ code: 'node-output-unknown', nodeType: 'ha-get-state', from: source.indexOf('"statee"') });
    expect(analysis.diagnostics('broad = nodes.broadNode({})\nbroad["out"][0]["payload"]["dynamic"]')).toEqual([]);
  });

  // Hover and signatures expose catalog metadata rather than per-node editor documentation.
  it('shows invocation identity, retry, output, and active-option documentation', () => {
    const analysis = new ScriptCatalogAnalysis('python', analysisFixtures());
    const hoverSource = 'reader = nodes.statefulNode';
    const hover = analysis.quickInfo(hoverSource, hoverSource.indexOf('statefulNode') + 2);
    expect(hover?.documentation).toContain('options.id is required');
    expect(hover?.documentation).toContain('Supports per-call retry');
    expect(hover?.documentation).toContain('Default retry');
    expect(hover?.documentation).toContain('Output out');

    const fieldSource = 'nodes.haGetState({"connection":"home","entity_id":"light.kitchen"})';
    const entity = analysis.quickInfo(fieldSource, fieldSource.indexOf('entity_id') + 2);
    expect(entity?.documentation).toContain('Entity');
    expect(entity?.documentation).toContain('Single entity to read.');
    expect(entity?.documentation).toContain('Required.');
    const encodingSource = 'nodes.base64Encode({"property":"payload","target":"payload","encoding":"standard"})';
    const encoding = analysis.quickInfo(encodingSource, encodingSource.indexOf('encoding') + 2);
    expect(encoding?.documentation).toContain('Default: "standard".');

    const signatureSource = 'nodes.statefulNode({}, None, {';
    const signature = analysis.signatureHelp(signatureSource, signatureSource.length);
    expect(signature?.activeArgument).toBe(2);
    expect(signature?.parameters.map((parameter) => parameter.name)).toEqual(['config', 'inputs', 'options']);
    expect(signature?.parameters[2].documentation).toContain('id is required');
    expect(signature?.parameters[2].documentation).toContain('max_attempts');
  });
});

describe('Bash catalog analysis', () => {
  // Shell arguments need valid shell quoting in addition to valid JSON string escaping.
  it('completes node targets and JSON fields with shell-aware insertion', () => {
    const analysis = new ScriptCatalogAnalysis('bash', analysisFixtures());
    expect(labels(analysis.completions('nod', 3))).toEqual(['node']);
    expect(labels(analysis.completions('node ', 5))).toEqual(expect.arrayContaining(['base64-encode', 'base64Encode', 'call']));
    expect(labels(analysis.completions('node call ', 10))).toContain('ha-get-state');

    const configSource = 'node base64Encode ';
    const config = analysis.completions(configSource, configSource.length);
    const encoding = config?.options.find((option) => option.label === 'encoding');
    expect(encoding?.insert).toBe('\'{"encoding": }\'');
    expect(encoding).toBeDefined();
    expect(encoding!.cursor).toBe(encoding!.insert.length - 2);

    const singleSource = 'node base64Encode \'{"encoding": ';
    const single = analysis.completions(singleSource, singleSource.length);
    expect(single?.options.find((option) => option.label === 'standard')?.insert).toBe('"standard"');
    const computedValue = 'node base64Encode \'{"encoding": choice';
    expect(analysis.completions(computedValue, computedValue.length)).toBeNull();

    const doubleSource = 'node base64Encode "{\\"encoding\\": ';
    const double = analysis.completions(doubleSource, doubleSource.length);
    expect(double?.options.find((option) => option.label === 'standard')?.insert).toBe('\\"standard\\"');
    const doubleValueSource = 'node base64Encode "{\\"encoding\\": \\"';
    expect(labels(analysis.completions(doubleValueSource, doubleValueSource.length))).toEqual(['standard', 'url']);

    const inputSource = 'node base64Encode \'{}\' \'{"';
    expect(labels(analysis.completions(inputSource, inputSource.length))).toEqual(['in']);
    const optionSource = 'node statefulNode \'{}\' \'{}\' \'{"';
    expect(labels(analysis.completions(optionSource, optionSource.length))).toEqual(['id', 'retry']);

    expect(analysis.completions('echo node ', 10)).toBeNull();
    expect(analysis.completions('# node ', 7)).toBeNull();
  });

  // jsonget path completion follows the same stored output IDs and closed payload schema as Python.
  it.each([
    {
      name: 'direct assignment',
      source: 'state=$(node haGetState "$config")\njsonget out.0.payload. "$state"',
    },
    {
      name: 'direct call',
      source: 'jsonget out.0.payload. "$(node haGetState "$config")"',
    },
  ])('completes jsonget output paths from a $name', ({ source }) => {
    const analysis = new ScriptCatalogAnalysis('bash', analysisFixtures());
    const pos = source.indexOf('out.0.payload.') + 'out.0.payload.'.length;
    const result = analysis.completions(source, pos);
    expect(labels(result)).toEqual(expect.arrayContaining(['entity_id', 'state', 'attributes']));
    expect(labels(result)).not.toContain('old_state');
    expect(result?.options.find((option) => option.label === 'entity_id')?.insert).toBe('out.0.payload.entity_id');
    expect(result?.options.find((option) => option.label === 'entity_id')?.filterText).toBe('out.0.payload.entity_id');
  });

  // Captured command results retain their output schema for jsonget checks.
  it('diagnoses unknown jsonget properties only for closed output schemas', () => {
    const analysis = new ScriptCatalogAnalysis('bash', analysisFixtures());
    const source = 'state=$(node haGetState \'{}\')\njsonget out.0.payload.statee "$state"';
    const diagnostics = analysis.diagnostics(source);
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0]).toMatchObject({ code: 'node-output-unknown', nodeType: 'ha-get-state' });
    expect(analysis.diagnostics('broad=$(node broadNode \'{}\')\njsonget out.0.payload.dynamic "$broad"')).toEqual([]);
  });

  // Bash signature positions count command words, including the generic target word.
  it('shows function hover and the active JSON argument signature', () => {
    const analysis = new ScriptCatalogAnalysis('bash', analysisFixtures());
    const hoverSource = 'node statefulNode "$config"';
    const hover = analysis.quickInfo(hoverSource, hoverSource.indexOf('statefulNode') + 2);
    expect(hover?.documentation).toContain('options.id is required');
    expect(hover?.documentation).toContain('nonzero status');

    const signatureSource = 'node base64Encode "$config" "$inputs" "$options"';
    const signature = analysis.signatureHelp(signatureSource, signatureSource.indexOf('$options') + 2);
    expect(signature?.activeArgument).toBe(2);
    expect(signature?.parameters.map((parameter) => parameter.name)).toEqual(['config-json', 'inputs-json', 'options-json']);
    expect(signature?.prefix).toBe('node base64Encode ');
    expect(signature?.separator).toBe(' ');

    const genericSource = 'node call base64-encode "$config" "$inputs"';
    const generic = analysis.signatureHelp(genericSource, genericSource.indexOf('$inputs') + 2);
    expect(generic?.activeArgument).toBe(2);
    expect(generic?.parameters.map((parameter) => parameter.name)).toEqual(['node-type', 'config-json', 'inputs-json', 'options-json']);
  });
});
