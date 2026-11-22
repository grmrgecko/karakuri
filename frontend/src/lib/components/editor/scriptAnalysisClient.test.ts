import { describe, expect, it } from 'vitest';
import type {
  ScriptAnalysisRequest,
  ScriptAnalysisResponse,
} from './scriptAnalysisService';
import {
  ScriptAnalysisClient,
  StaleScriptAnalysisError,
  type AnalysisWorker,
} from './scriptAnalysisClient';

class FakeWorker implements AnalysisWorker {
  onmessage: ((event: MessageEvent<ScriptAnalysisResponse>) => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;
  onmessageerror: ((event: unknown) => void) | null = null;
  requests: ScriptAnalysisRequest[] = [];
  terminated = false;

  postMessage(request: ScriptAnalysisRequest): void {
    this.requests.push(request);
  }

  respond(request: ScriptAnalysisRequest, result: unknown): void {
    this.onmessage?.({
      data: {
        id: request.id,
        documentRevision: request.documentRevision,
        catalogRevision: request.catalogRevision,
        result,
      },
    } as MessageEvent<ScriptAnalysisResponse>);
  }

  terminate(): void {
    this.terminated = true;
  }
}

describe('script analysis client', () => {
  // An older edit must never replace diagnostics for the document now on screen.
  it('rejects a document response that arrives after a newer edit', async () => {
    const worker = new FakeWorker();
    const client = new ScriptAnalysisClient({ workerFactory: () => worker });
    client.setCatalog('catalog-one', 'declarations one');
    client.setDocument('nodes.old');
    const old = client.diagnostics();
    const oldRejection = expect(old).rejects.toBeInstanceOf(StaleScriptAnalysisError);

    client.setDocument('nodes.new');
    const current = client.diagnostics();
    expect(worker.requests).toHaveLength(2);
    expect(worker.requests[0].declarations).toBe('declarations one');
    expect(worker.requests[1].declarations).toBeUndefined();

    worker.respond(worker.requests[0], []);
    await oldRejection;
    worker.respond(worker.requests[1], [{ from: 0, to: 1, severity: 'error', code: 1, message: 'current' }]);
    await expect(current).resolves.toEqual([{ from: 0, to: 1, severity: 'error', code: 1, message: 'current' }]);
    client.dispose();
  });

  // Catalog responses carry their revision so plugin changes invalidate old analysis.
  it('rejects analysis from an old catalog and sends the new declaration revision once', async () => {
    const worker = new FakeWorker();
    const client = new ScriptAnalysisClient({ workerFactory: () => worker });
    client.setCatalog('catalog-one', 'declarations one');
    const old = client.diagnostics();
    const oldRejection = expect(old).rejects.toBeInstanceOf(StaleScriptAnalysisError);

    client.setCatalog('catalog-two', 'declarations two');
    const current = client.diagnostics();
    worker.respond(worker.requests[0], []);
    await oldRejection;
    worker.respond(worker.requests[1], []);
    await expect(current).resolves.toEqual([]);
    expect(worker.requests[1].catalogRevision).toBe('catalog-two');
    expect(worker.requests[1].declarations).toBe('declarations two');
    client.dispose();
  });

  // Closing the editor terminates analysis instead of leaving work and responses behind.
  it('starts lazily, rejects pending work on close, and terminates the worker', async () => {
    let starts = 0;
    const worker = new FakeWorker();
    const client = new ScriptAnalysisClient({
      workerFactory: () => {
        starts++;
        return worker;
      },
    });
    expect(starts).toBe(0);

    const pending = client.diagnostics();
    const rejection = expect(pending).rejects.toThrow('script analysis is closed');
    expect(starts).toBe(1);
    client.dispose();
    await rejection;
    expect(worker.terminated).toBe(true);
  });
});
