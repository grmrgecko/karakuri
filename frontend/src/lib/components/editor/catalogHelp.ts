import type {
  CatalogDocumentation,
  CatalogNode,
  CatalogScriptLanguage,
  CatalogSignatureParameter,
} from './scriptCatalogProjection';
import {
  fieldDocumentation,
  fieldType,
  functionDocumentation,
  inputDocumentation,
  inputsRequired,
} from './scriptCatalogProjection';

function configDocumentation(node: CatalogNode): string {
  const lines = ['Node configuration using stored field IDs.'];
  for (const field of node.config) {
    const doc = fieldDocumentation(field);
    lines.push(`${field.spec.id}: ${fieldType(field)}\n${doc.documentation}`);
  }
  if (node.config.length === 0) lines.push('This node takes an empty configuration object.');
  return lines.join('\n\n');
}

function inputDocs(node: CatalogNode): string {
  const lines = ['Complete input messages keyed by stored port ID.'];
  for (const port of node.inputs) {
    const doc = inputDocumentation(port);
    lines.push(`${port.spec.id}\n${doc.documentation}`);
  }
  if (node.fn.meta.dynamic_ports) lines.push('This node has dynamic input ports.');
  if (node.inputs.length === 0 && !node.fn.meta.dynamic_ports) lines.push('This node has no message inputs.');
  return lines.join('\n\n');
}

function optionDocs(node: CatalogNode): string {
  const lines = ['Invocation identity, value parameters, and retry policy.'];
  lines.push(node.fn.identity_required
    ? 'id is required and must remain stable across runs.'
    : 'id is optional.');
  if (node.params.length) {
    lines.push(`params accepts stored field IDs: ${node.params.map((field) => field.spec.id).join(', ')}.`);
  }
  if (node.fn.meta.retryable) {
    lines.push('retry accepts max_attempts, delay_ms, backoff, max_delay_ms, and jitter.');
    if (node.fn.meta.default_retry) lines.push(`Default retry: ${JSON.stringify(node.fn.meta.default_retry)}.`);
  } else {
    lines.push('This node does not accept a per-call retry override.');
  }
  return lines.join('\n\n');
}

export function catalogSignatureParameters(
  language: CatalogScriptLanguage,
  node: CatalogNode,
  generic: boolean,
): CatalogSignatureParameter[] {
  const bash = language === 'bash';
  const parameters: CatalogSignatureParameter[] = [];
  if (generic) {
    parameters.push({
      name: bash ? 'node-type' : 'node_type',
      display: bash ? '<node-type>' : 'node_type: str',
      documentation: `Exact registered node type. ${node.fn.node_type} selects ${node.fn.meta.label || node.fn.name}.`,
      optional: false,
    });
  }
  parameters.push({
    name: bash ? 'config-json' : 'config',
    display: bash ? '<config-json>' : 'config: dict',
    documentation: configDocumentation(node),
    optional: false,
  });
  parameters.push({
    name: bash ? 'inputs-json' : 'inputs',
    display: bash ? '[inputs-json]' : `inputs${inputsRequired(node.fn.meta) ? '' : '=None'}: dict`,
    documentation: inputDocs(node),
    optional: !inputsRequired(node.fn.meta),
  });
  parameters.push({
    name: bash ? 'options-json' : 'options',
    display: bash ? '[options-json]' : 'options=None: dict',
    documentation: optionDocs(node),
    optional: true,
  });
  return parameters;
}

export function catalogFunctionDocumentation(
  language: CatalogScriptLanguage,
  node: CatalogNode,
): CatalogDocumentation {
  const documentation = functionDocumentation(node);
  return {
    ...documentation,
    documentation: `${documentation.documentation}\n${language === 'python'
      ? 'Failures raise karakuri.NodeCallError with node identity and attempt details.'
      : 'Failures return nonzero status and a JSON error document with node identity and attempt details.'}`,
  };
}

export function genericCallDocumentation(language: CatalogScriptLanguage): CatalogDocumentation {
  return {
    display: language === 'python' ? 'nodes.call(node_type, config, inputs=None, options=None)' : 'node call <node-type> <config-json> [inputs-json [options-json]]',
    documentation: 'Calls a node by its exact registered type. Literal known types retain their documented config, input, option, and output contracts. Computed types are checked at invocation.',
    tags: [],
  };
}
