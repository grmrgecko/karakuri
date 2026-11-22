import { describe, expect, it } from 'vitest';
import type { NodeFunction } from '../../stores/nodeCatalog';
import { generateScriptSdkDeclarations, jsonSchemaType } from './scriptSdk';
import { analysisFixtures } from './scriptSdkTestData';

describe('script SDK declarations', () => {
  // Stored graph IDs and invocation metadata must survive declaration generation unchanged.
  it('preserves stored names, conditional requiredness, and per-node invocation options', () => {
    const declarations = generateScriptSdkDeclarations(analysisFixtures());

    expect(declarations).toContain('"base64-encode"(config: ScriptNode1Config');
    expect(declarations).toContain('"base64Encode"(config: ScriptNode1Config');
    expect(declarations).toContain('(config: ScriptNode1Config, inputs: ScriptNode1Inputs, options?: ScriptNode1Options)');
    expect(declarations).toContain('(config: ScriptNode0Config, inputs?: ScriptNode0Inputs, options?: ScriptNode0Options)');
    expect(declarations).toContain('"entity_id": string;');
    expect(declarations).toContain('"encoding"?: "standard" | "url";');
    expect(declarations).toContain('"line_width"?: number;');
    expect(declarations).toContain('"out": ScriptMessage<string>[];');
    expect(declarations).toContain('interface ScriptNode1Options {\n  id?: string;\n  params?: ScriptNode1Params;\n}');
    expect(declarations).toContain('interface ScriptNode5Options {\n  id: string;\n  retry?: ScriptNodeRetry;');
    expect(declarations).toContain('Default: "standard".');
    expect(declarations).toContain('Pure value evaluation.');
    expect(declarations).toContain('Supports per-call retry.');
    expect(declarations).toContain('interface ScriptNode6Outputs {\n  [port: string]: ScriptMessage[];\n}');
  });

  // JSON Schema objects are open by default, unlike the closed node config interfaces.
  it('closes only schemas that forbid additional properties', () => {
    const closed = jsonSchemaType({
      type: 'object',
      properties: { state: { type: 'string' } },
      required: ['state'],
      additionalProperties: false,
    });
    const open = jsonSchemaType({
      type: 'object',
      properties: { state: { type: 'string' } },
    });
    const additional = jsonSchemaType({ type: 'object', additionalProperties: { type: 'number' } });

    expect(closed).toContain('"state": string;');
    expect(closed).not.toContain('[key: string]: any');
    expect(open).toContain('[key: string]: any');
    expect(additional).toBe('{ [key: string]: number }');
    expect(jsonSchemaType({ $ref: '#/definitions/not-loaded' })).toBe('any');
    expect(jsonSchemaType({ type: 'binary' })).toBe('string');
  });

  // scriptsdk.ValidateCall takes options.params for every field flow.FieldBindable
  // names, and that includes the ha-target fields of the Home Assistant nodes.
  // The declaration kept its own list without ha-target, so a valid
  // params: { area_id } was reported as an unknown property. A field with no
  // single value, such as a rule list, still stays out.
  it('declares an ha-target field as a string value parameter', () => {
    const service: NodeFunction = {
      name: 'ha-call-service',
      node_type: 'ha-call-service',
      identity_required: false,
      meta: {
        type: 'ha-call-service',
        label: 'Call Service',
        description: '',
        icon: '',
        retryable: false,
        config_spec: [
          { id: 'area_id', label: 'Target areas', type: 'ha-target', required: false },
          { id: 'rules', label: 'Rules', type: 'rules', required: false },
        ],
        outputs: [],
      },
    };

    const declarations = generateScriptSdkDeclarations([service]);
    const params = /interface ScriptNode0Params \{[^}]*\}/.exec(declarations)?.[0] ?? '';
    const config = /interface ScriptNode0Config \{[^}]*\}/.exec(declarations)?.[0] ?? '';

    expect(params).toContain('"area_id"?: string;');
    expect(params).not.toContain('"rules"');
    expect(config).toContain('"area_id"?: string;');
    expect(config).toContain('"rules"?: any;');
  });

  // Plugin metadata is data, even when it contains TypeScript comment syntax.
  it('quotes hostile identifiers and neutralizes comment terminators in contributed metadata', () => {
    const hostile: NodeFunction = {
      name: 'bad-*/-node',
      aliases: ['badAlias'],
      node_type: 'bad-*/-node',
      identity_required: false,
      meta: {
        type: 'bad-*/-node',
        label: 'Bad */ declare const injected: string; /* label',
        description: 'Description with a close */ and a newline\nnext line.',
        icon: '',
        retryable: false,
        config_spec: [{
          id: 'field-*/-"quoted"',
          label: 'Field */ still comment text',
          type: 'string',
          required: true,
          help: 'Help */ must not close the declaration comment.',
        }],
        outputs: [],
      },
    };

    const declarations = generateScriptSdkDeclarations([hostile]);
    expect(declarations).not.toContain('Bad */ declare');
    expect(declarations).toContain('Bad * / declare');
    expect(declarations).toContain('"field-*/-\\"quoted\\"": string;');
    expect(declarations).toContain('"bad-*/-node"(config:');
    expect(declarations).toContain('Description with a close * / and a newline next line.');
  });
});
