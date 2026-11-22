import { bashCompletions, bashDiagnostics, bashQuickInfo, bashSignatureHelp } from './bashCatalogAnalysis';
import { pythonCompletions, pythonDiagnostics, pythonQuickInfo, pythonSignatureHelp } from './pythonCatalogAnalysis';
import {
  projectScriptCatalog,
  type CatalogCompletionResult,
  type CatalogDiagnostic,
  type CatalogQuickInfo,
  type CatalogScriptLanguage,
  type CatalogSignatureHelp,
  type ScriptCatalogProjection,
} from './scriptCatalogProjection';
import type { NodeFunction } from '../../stores/nodeCatalog';

// ScriptCatalogAnalysis runs entirely in the editor. Server verification is a
// separate concern and neither language loads the JavaScript worker.
export class ScriptCatalogAnalysis {
  readonly language: CatalogScriptLanguage;
  readonly projection: ScriptCatalogProjection;

  constructor(language: CatalogScriptLanguage, functions: NodeFunction[]) {
    this.language = language;
    this.projection = projectScriptCatalog(functions);
  }

  completions(source: string, pos: number): CatalogCompletionResult | null {
    return this.language === 'python'
      ? pythonCompletions(source, pos, this.projection)
      : bashCompletions(source, pos, this.projection);
  }

  diagnostics(source: string): CatalogDiagnostic[] {
    return this.language === 'python'
      ? pythonDiagnostics(source, this.projection)
      : bashDiagnostics(source, this.projection);
  }

  quickInfo(source: string, pos: number): CatalogQuickInfo | null {
    return this.language === 'python'
      ? pythonQuickInfo(source, pos, this.projection)
      : bashQuickInfo(source, pos, this.projection);
  }

  signatureHelp(source: string, pos: number): CatalogSignatureHelp | null {
    return this.language === 'python'
      ? pythonSignatureHelp(source, pos, this.projection)
      : bashSignatureHelp(source, pos, this.projection);
  }
}
