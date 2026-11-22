import ts from 'typescript';
import { defaultScriptLibrary, scriptLibraries, scriptLibrary } from './scriptAnalysisLibs';

export const scriptFileName = '/script.js';
export const scriptDeclarationsFileName = '/karakuri-script.d.ts';

const wrapperPrefix = '(function(msg, payload, topic, meta, input) {\n';
const wrapperSuffix = '\n})';

export interface AnalysisSpan {
  from: number;
  to: number;
}

// ScriptSourceMap is the only conversion between editor and virtual offsets.
// JavaScript, TypeScript, and CodeMirror offsets are all UTF-16 code-unit based.
export class ScriptSourceMap {
  readonly source: string;
  readonly virtualSource: string;

  constructor(source: string) {
    this.source = source;
    this.virtualSource = `${wrapperPrefix}${source}${wrapperSuffix}`;
  }

  toVirtual(position: number): number {
    return wrapperPrefix.length + Math.max(0, Math.min(position, this.source.length));
  }

  toOriginal(position: number): number {
    return Math.max(0, Math.min(position - wrapperPrefix.length, this.source.length));
  }

  span(start: number, length: number): AnalysisSpan | null {
    if (start < wrapperPrefix.length) return null;
    const from = this.toOriginal(start);
    const to = this.toOriginal(start + Math.max(0, length));
    return { from, to };
  }
}

export interface AnalysisDiagnostic extends AnalysisSpan {
  severity: 'error' | 'warning' | 'info';
  code: number;
  message: string;
}

export interface AnalysisCompletionEntry {
  name: string;
  kind: string;
  kindModifiers?: string;
  sortText: string;
  insertText?: string;
  filterText?: string;
  replacementSpan?: AnalysisSpan;
  source?: string;
  commitCharacters?: string[];
  data?: unknown;
}

export interface AnalysisCompletions {
  isMemberCompletion: boolean;
  isIncomplete: boolean;
  optionalReplacementSpan?: AnalysisSpan;
  entries: AnalysisCompletionEntry[];
}

export interface AnalysisCompletionDetails {
  display: string;
  documentation: string;
  tags: string[];
}

export interface AnalysisQuickInfo extends AnalysisSpan {
  display: string;
  documentation: string;
  tags: string[];
}

export interface AnalysisSignatureParameter {
  name: string;
  display: string;
  documentation: string;
  optional: boolean;
}

export interface AnalysisSignatureItem {
  prefix: string;
  suffix: string;
  separator: string;
  documentation: string;
  parameters: AnalysisSignatureParameter[];
}

export interface AnalysisSignatureHelp extends AnalysisSpan {
  items: AnalysisSignatureItem[];
  activeItem: number;
  activeArgument: number;
  argumentCount: number;
}

export type ScriptAnalysisMethod = 'diagnostics' | 'completions' | 'completionDetails' | 'quickInfo' | 'signatureHelp';

export interface ScriptAnalysisRequest {
  id: number;
  method: ScriptAnalysisMethod;
  documentRevision: number;
  catalogRevision: string;
  source: string;
  declarations?: string;
  position?: number;
  entry?: AnalysisCompletionEntry;
}

export interface ScriptAnalysisResponse {
  id: number;
  documentRevision: number;
  catalogRevision: string;
  result?: unknown;
  error?: string;
}

function documentation(parts: ts.SymbolDisplayPart[] | undefined): string {
  return ts.displayPartsToString(parts).trim();
}

function tags(items: ts.JSDocTagInfo[] | undefined): string[] {
  return (items ?? []).map((tag) => `${tag.name}${tag.text ? ` ${documentation(tag.text)}` : ''}`.trim());
}

const compilerOptions: ts.CompilerOptions = {
  allowJs: true,
  checkJs: true,
  strict: false,
  noEmit: true,
  target: ts.ScriptTarget.ES2023,
  lib: [defaultScriptLibrary],
  types: [],
  typeRoots: [],
  skipLibCheck: true,
  skipDefaultLibCheck: true,
  allowNonTsExtensions: true,
};

// ScriptAnalysisService owns one persistent TypeScript language service. Source
// and declaration versions let TypeScript reuse its ES declaration program
// while the document changes.
export class ScriptAnalysisService {
  private mapper: ScriptSourceMap;
  private sourceVersion = 0;
  private declarations: string;
  private declarationsVersion = 0;
  private languageService: ts.LanguageService;

  constructor(declarations: string, source = '') {
    this.declarations = declarations;
    this.mapper = new ScriptSourceMap(source);

    const host: ts.LanguageServiceHost = {
      getCompilationSettings: () => compilerOptions,
      getCurrentDirectory: () => '/',
      getDefaultLibFileName: () => `/${defaultScriptLibrary}`,
      getNewLine: () => '\n',
      getProjectVersion: () => `${this.declarationsVersion}:${this.sourceVersion}`,
      getScriptFileNames: () => [scriptFileName, scriptDeclarationsFileName],
      getScriptVersion: (fileName) => {
        if (fileName === scriptFileName) return String(this.sourceVersion);
        if (fileName === scriptDeclarationsFileName) return String(this.declarationsVersion);
        return '0';
      },
      getScriptKind: (fileName) => fileName === scriptFileName ? ts.ScriptKind.JS : ts.ScriptKind.TS,
      getScriptSnapshot: (fileName) => {
        const source = this.file(fileName);
        return source === undefined ? undefined : ts.ScriptSnapshot.fromString(source);
      },
      useCaseSensitiveFileNames: () => true,
      readFile: (fileName) => this.file(fileName),
      fileExists: (fileName) => this.file(fileName) !== undefined,
      readDirectory: () => [],
      getDirectories: () => [],
      getTypeRootsVersion: () => 0,
      resolveModuleNames: (names) => names.map(() => undefined),
      resolveTypeReferenceDirectives: (names) => names.map(() => undefined),
    };
    this.languageService = ts.createLanguageService(host);
  }

  private file(fileName: string): string | undefined {
    if (fileName === scriptFileName) return this.mapper.virtualSource;
    if (fileName === scriptDeclarationsFileName) return this.declarations;
    return scriptLibrary(fileName) ?? scriptLibraries[fileName];
  }

  update(source: string, declarations = this.declarations): void {
    if (source !== this.mapper.source) {
      this.mapper = new ScriptSourceMap(source);
      this.sourceVersion++;
    }
    if (declarations !== this.declarations) {
      this.declarations = declarations;
      this.declarationsVersion++;
    }
  }

  diagnostics(): AnalysisDiagnostic[] {
    const diagnostics = [
      ...this.languageService.getSyntacticDiagnostics(scriptFileName),
      ...this.languageService.getSemanticDiagnostics(scriptFileName),
    ];
    const mapped: AnalysisDiagnostic[] = [];
    const seen = new Set<string>();
    for (const diagnostic of diagnostics) {
      if (diagnostic.file?.fileName !== scriptFileName || diagnostic.start === undefined) continue;
      const span = this.mapper.span(diagnostic.start, diagnostic.length ?? 0);
      if (!span) continue;
      const severity = diagnostic.category === ts.DiagnosticCategory.Error
        ? 'error'
        : diagnostic.category === ts.DiagnosticCategory.Warning ? 'warning' : 'info';
      const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n');
      const key = `${diagnostic.code}:${span.from}:${span.to}:${message}`;
      if (seen.has(key)) continue;
      seen.add(key);
      mapped.push({ ...span, severity, code: diagnostic.code, message });
    }
    return mapped.sort((a, b) => a.from - b.from || a.to - b.to || a.code - b.code);
  }

  completions(position: number): AnalysisCompletions | null {
    const result = this.languageService.getCompletionsAtPosition(scriptFileName, this.mapper.toVirtual(position), {
      includeCompletionsWithInsertText: true,
      includeCompletionsForImportStatements: false,
      includeCompletionsForModuleExports: false,
      includeCompletionsWithClassMemberSnippets: false,
    });
    if (!result) return null;
    return {
      isMemberCompletion: result.isMemberCompletion,
      isIncomplete: result.isIncomplete === true,
      optionalReplacementSpan: result.optionalReplacementSpan
        ? this.mapper.span(result.optionalReplacementSpan.start, result.optionalReplacementSpan.length) ?? undefined
        : undefined,
      entries: result.entries.map((entry) => ({
        name: entry.name,
        kind: entry.kind,
        kindModifiers: entry.kindModifiers,
        sortText: entry.sortText,
        insertText: entry.insertText,
        filterText: entry.filterText,
        replacementSpan: entry.replacementSpan
          ? this.mapper.span(entry.replacementSpan.start, entry.replacementSpan.length) ?? undefined
          : undefined,
        source: entry.source,
        commitCharacters: entry.commitCharacters ? [...entry.commitCharacters] : undefined,
        data: entry.data,
      })),
    };
  }

  completionDetails(position: number, entry: AnalysisCompletionEntry): AnalysisCompletionDetails | null {
    const result = this.languageService.getCompletionEntryDetails(
      scriptFileName,
      this.mapper.toVirtual(position),
      entry.name,
      undefined,
      entry.source,
      undefined,
      entry.data as ts.CompletionEntryData | undefined,
    );
    if (!result) return null;
    return {
      display: documentation(result.displayParts),
      documentation: documentation(result.documentation),
      tags: tags(result.tags),
    };
  }

  quickInfo(position: number): AnalysisQuickInfo | null {
    const result = this.languageService.getQuickInfoAtPosition(scriptFileName, this.mapper.toVirtual(position));
    if (!result) return null;
    const span = this.mapper.span(result.textSpan.start, result.textSpan.length);
    if (!span) return null;
    return {
      ...span,
      display: documentation(result.displayParts),
      documentation: documentation(result.documentation),
      tags: tags(result.tags),
    };
  }

  signatureHelp(position: number): AnalysisSignatureHelp | null {
    const result = this.languageService.getSignatureHelpItems(scriptFileName, this.mapper.toVirtual(position), undefined);
    if (!result || result.items.length === 0) return null;
    const span = this.mapper.span(result.applicableSpan.start, result.applicableSpan.length);
    if (!span) return null;
    return {
      ...span,
      activeItem: result.selectedItemIndex,
      activeArgument: result.argumentIndex,
      argumentCount: result.argumentCount,
      items: result.items.map((item) => ({
        prefix: documentation(item.prefixDisplayParts),
        suffix: documentation(item.suffixDisplayParts),
        separator: documentation(item.separatorDisplayParts),
        documentation: documentation(item.documentation),
        parameters: item.parameters.map((parameter) => ({
          name: parameter.name,
          display: documentation(parameter.displayParts),
          documentation: documentation(parameter.documentation),
          optional: parameter.isOptional,
        })),
      })),
    };
  }

  dispose(): void {
    this.languageService.dispose();
  }
}

export function runScriptAnalysis(request: ScriptAnalysisRequest, service: ScriptAnalysisService): unknown {
  if (request.declarations !== undefined) service.update(request.source, request.declarations);
  else service.update(request.source);

  switch (request.method) {
    case 'diagnostics': return service.diagnostics();
    case 'completions': return service.completions(request.position ?? 0);
    case 'completionDetails':
      if (!request.entry) throw new Error('completion details require an entry');
      return service.completionDetails(request.position ?? 0, request.entry);
    case 'quickInfo': return service.quickInfo(request.position ?? 0);
    case 'signatureHelp': return service.signatureHelp(request.position ?? 0);
  }
}
