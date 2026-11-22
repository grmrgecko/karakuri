import type {
  ScriptVerificationLanguage,
  ScriptVerificationRequest,
  ScriptVerificationResult,
} from '../../api/client';

export interface EditorScriptDiagnostic {
  severity: 'error' | 'warning';
  code: string;
  message: string;
  from: number;
  to: number;
  line: number;
  column: number;
  nodeType?: string;
}

function utf8Length(char: string): number {
  const point = char.codePointAt(0) ?? 0;
  return point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4;
}

// utf8ByteOffsetToUTF16 maps the verifier's original-source byte offsets to
// CodeMirror offsets. An offset inside a multibyte code point maps to its start.
export function utf8ByteOffsetToUTF16(source: string, offset: number): number {
  if (!Number.isFinite(offset) || offset <= 0) return 0;
  const target = Math.floor(offset);
  let bytes = 0;
  let utf16 = 0;
  for (const char of source) {
    if (bytes >= target) break;
    const length = utf8Length(char);
    if (bytes + length > target) return utf16;
    bytes += length;
    utf16 += char.length;
  }
  return utf16;
}

interface PendingVerification {
  key: string;
  language: ScriptVerificationLanguage;
  source: string;
  generation: number;
  ready: boolean;
  promise: Promise<EditorScriptDiagnostic[]>;
  resolve: (diagnostics: EditorScriptDiagnostic[]) => void;
}

export interface ScriptVerificationClientOptions {
  request?: (body: ScriptVerificationRequest) => Promise<ScriptVerificationResult>;
  delay?: number;
}

// ScriptVerificationClient keeps one request in flight and queues only the
// newest edit. Source, language, and catalog changes invalidate older responses.
export class ScriptVerificationClient {
  private readonly request: (body: ScriptVerificationRequest) => Promise<ScriptVerificationResult>;
  private readonly delay: number;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private inFlight: PendingVerification | null = null;
  private queued: PendingVerification | null = null;
  private generation = 0;
  private disposed = false;

  constructor(options: ScriptVerificationClientOptions = {}) {
    this.request = options.request ?? (async (body) => {
      const { api } = await import('../../api/client');
      return api.scripts.verify(body);
    });
    this.delay = options.delay ?? 300;
  }

  verify(language: ScriptVerificationLanguage, source: string, catalogRevision: string): Promise<EditorScriptDiagnostic[]> {
    if (this.disposed) return Promise.resolve([]);
    const key = JSON.stringify([language, catalogRevision, source]);
    if (this.queued?.key === key) return this.queued.promise;
    if (this.inFlight?.key === key && !this.queued) return this.inFlight.promise;

    const generation = ++this.generation;
    this.queued?.resolve([]);
    let resolve!: (diagnostics: EditorScriptDiagnostic[]) => void;
    const promise = new Promise<EditorScriptDiagnostic[]>((done) => {
      resolve = done;
    });
    const pending: PendingVerification = {
      key,
      language,
      source,
      generation,
      ready: false,
      promise,
      resolve,
    };
    this.queued = pending;
    this.arm(pending);
    return promise;
  }

  private arm(pending: PendingVerification): void {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = null;
      if (this.disposed || this.queued !== pending) return;
      pending.ready = true;
      this.startQueued();
    }, this.delay);
  }

  private startQueued(): void {
    if (this.disposed || this.inFlight || !this.queued?.ready) return;
    const pending = this.queued;
    this.queued = null;
    this.inFlight = pending;
    void Promise.resolve()
      .then(() => this.disposed || this.inFlight !== pending
        ? null
        : this.request({ language: pending.language, source: pending.source, pure: false }))
      .then(
        (result) => this.finish(pending, result),
        () => this.finish(pending, null),
      );
  }

  private finish(pending: PendingVerification, result: ScriptVerificationResult | null): void {
    if (this.inFlight !== pending) return;
    this.inFlight = null;
    const current = !this.disposed && pending.generation === this.generation;
    const diagnostics = current && result && Array.isArray(result.diagnostics)
      ? result.diagnostics.map((diagnostic) => {
        const from = utf8ByteOffsetToUTF16(pending.source, diagnostic.from);
        const to = Math.max(from, utf8ByteOffsetToUTF16(pending.source, diagnostic.to));
        return {
          severity: diagnostic.severity,
          code: diagnostic.code,
          message: diagnostic.message,
          from,
          to,
          line: diagnostic.line,
          column: diagnostic.column,
          nodeType: diagnostic.node_type,
        };
      })
      : [];
    pending.resolve(diagnostics);
    this.startQueued();
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.generation++;
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
    this.queued?.resolve([]);
    this.queued = null;
    this.inFlight?.resolve([]);
    this.inFlight = null;
  }
}
