import type {
  AnalysisCompletionDetails,
  AnalysisCompletionEntry,
  AnalysisCompletions,
  AnalysisDiagnostic,
  AnalysisQuickInfo,
  AnalysisSignatureHelp,
  ScriptAnalysisMethod,
  ScriptAnalysisRequest,
  ScriptAnalysisResponse,
} from './scriptAnalysisService';

export interface AnalysisWorker {
  onmessage: ((event: MessageEvent<ScriptAnalysisResponse>) => void) | null;
  onerror: ((event: ErrorEvent) => void) | null;
  onmessageerror?: ((event: MessageEvent<unknown>) => void) | null;
  postMessage: (message: ScriptAnalysisRequest) => void;
  terminate: () => void;
}

export class StaleScriptAnalysisError extends Error {
  constructor() {
    super('script analysis response is stale');
    this.name = 'StaleScriptAnalysisError';
  }
}

interface PendingRequest {
  documentRevision: number;
  catalogRevision: string;
  resolve: (value: any) => void;
  reject: (error: Error) => void;
}

export interface ScriptAnalysisClientOptions {
  workerFactory?: () => AnalysisWorker;
  onFailure?: (error: Error) => void;
}

function eventError(event: unknown): Error {
  if (event instanceof Error) return event;
  const message = event && typeof event === 'object' && 'message' in event
    ? String((event as { message?: unknown }).message ?? '')
    : '';
  return new Error(message || 'the script analysis worker stopped');
}

// ScriptAnalysisClient starts its worker on the first query. Every pending
// request is tied to both source and catalog revisions, including lazy details.
export class ScriptAnalysisClient {
  private readonly workerFactory: () => AnalysisWorker;
  private readonly onFailure?: (error: Error) => void;
  private worker: AnalysisWorker | null = null;
  private sentCatalogRevision: string | null = null;
  private nextID = 1;
  private pending = new Map<number, PendingRequest>();
  private source = '';
  private declarations = '';
  private catalogRevision = '';
  private documentRevision = 0;
  private disposed = false;
  private failed: Error | null = null;

  constructor(options: ScriptAnalysisClientOptions = {}) {
    this.workerFactory = options.workerFactory ?? (() => new Worker(
      new URL('./scriptAnalysis.worker.ts', import.meta.url),
      { type: 'module', name: 'karakuri-script-analysis' },
    ));
    this.onFailure = options.onFailure;
  }

  get currentDocumentRevision(): number {
    return this.documentRevision;
  }

  get currentCatalogRevision(): string {
    return this.catalogRevision;
  }

  setDocument(source: string): number {
    if (source !== this.source) {
      this.source = source;
      this.documentRevision++;
    }
    return this.documentRevision;
  }

  setCatalog(revision: string, declarations: string): void {
    if (revision === this.catalogRevision && declarations === this.declarations) return;
    const sameRevision = revision === this.catalogRevision;
    this.catalogRevision = revision;
    this.declarations = declarations;
    if (sameRevision) this.documentRevision++;
  }

  private ensureWorker(): AnalysisWorker {
    if (this.worker) return this.worker;
    const worker = this.workerFactory();
    worker.onmessage = (event) => this.receive(event.data);
    worker.onerror = (event) => this.fail(eventError(event));
    worker.onmessageerror = (event) => this.fail(eventError(event));
    this.worker = worker;
    this.sentCatalogRevision = null;
    return worker;
  }

  private receive(response: ScriptAnalysisResponse): void {
    const request = this.pending.get(response.id);
    if (!request) return;
    this.pending.delete(response.id);
    if (
      response.documentRevision !== request.documentRevision ||
      response.catalogRevision !== request.catalogRevision ||
      this.documentRevision !== request.documentRevision ||
      this.catalogRevision !== request.catalogRevision
    ) {
      request.reject(new StaleScriptAnalysisError());
      return;
    }
    if (response.error) {
      request.reject(new Error(response.error));
      return;
    }
    request.resolve(response.result);
  }

  private request(method: ScriptAnalysisMethod, position?: number, entry?: AnalysisCompletionEntry): Promise<any> {
    if (this.disposed) return Promise.reject(new Error('script analysis is closed'));
    if (this.failed) return Promise.reject(this.failed);

    let worker: AnalysisWorker;
    try {
      worker = this.ensureWorker();
    } catch (error: any) {
      const failure = error instanceof Error ? error : new Error('could not start script analysis');
      this.fail(failure);
      return Promise.reject(failure);
    }

    const id = this.nextID++;
    const documentRevision = this.documentRevision;
    const catalogRevision = this.catalogRevision;
    const withCatalog = this.sentCatalogRevision !== catalogRevision;
    const request: ScriptAnalysisRequest = {
      id,
      method,
      documentRevision,
      catalogRevision,
      source: this.source,
      declarations: withCatalog ? this.declarations : undefined,
      position,
      entry,
    };
    const promise = new Promise<any>((resolve, reject) => {
      this.pending.set(id, { documentRevision, catalogRevision, resolve, reject });
    });
    try {
      if (withCatalog) this.sentCatalogRevision = catalogRevision;
      worker.postMessage(request);
    } catch (error: any) {
      const failure = error instanceof Error ? error : new Error('could not contact script analysis');
      this.fail(failure);
    }
    return promise;
  }

  diagnostics(): Promise<AnalysisDiagnostic[]> {
    return this.request('diagnostics');
  }

  completions(position: number): Promise<AnalysisCompletions | null> {
    return this.request('completions', position);
  }

  completionDetails(position: number, entry: AnalysisCompletionEntry): Promise<AnalysisCompletionDetails | null> {
    return this.request('completionDetails', position, entry);
  }

  quickInfo(position: number): Promise<AnalysisQuickInfo | null> {
    return this.request('quickInfo', position);
  }

  signatureHelp(position: number): Promise<AnalysisSignatureHelp | null> {
    return this.request('signatureHelp', position);
  }

  private fail(error: Error): void {
    if (this.disposed || this.failed) return;
    this.failed = error;
    const pending = [...this.pending.values()];
    this.pending.clear();
    for (const request of pending) request.reject(error);
    this.worker?.terminate();
    this.worker = null;
    this.sentCatalogRevision = null;
    this.onFailure?.(error);
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    const error = new Error('script analysis is closed');
    const pending = [...this.pending.values()];
    this.pending.clear();
    for (const request of pending) request.reject(error);
    if (this.worker) {
      this.worker.onmessage = null;
      this.worker.onerror = null;
      if (this.worker.onmessageerror) this.worker.onmessageerror = null;
      this.worker.terminate();
    }
    this.worker = null;
  }
}
