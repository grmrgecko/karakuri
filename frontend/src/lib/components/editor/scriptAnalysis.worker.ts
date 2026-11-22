import { runScriptAnalysis, ScriptAnalysisService } from './scriptAnalysisService';
import type { ScriptAnalysisRequest, ScriptAnalysisResponse } from './scriptAnalysisService';

const workerScope = globalThis as unknown as {
  onmessage: ((event: MessageEvent<ScriptAnalysisRequest>) => void) | null;
  postMessage: (message: ScriptAnalysisResponse) => void;
};

let service: ScriptAnalysisService | null = null;
let catalogRevision = '';

workerScope.onmessage = (event) => {
  const request = event.data;
  try {
    if (request.declarations !== undefined) {
      if (!service) service = new ScriptAnalysisService(request.declarations, request.source);
      catalogRevision = request.catalogRevision;
    }
    if (!service) throw new Error('script analysis has no catalog');
    if (catalogRevision !== request.catalogRevision) throw new Error('script analysis catalog revision is not loaded');
    const result = runScriptAnalysis(request, service);
    workerScope.postMessage({
      id: request.id,
      documentRevision: request.documentRevision,
      catalogRevision: request.catalogRevision,
      result,
    });
  } catch (error: any) {
    workerScope.postMessage({
      id: request.id,
      documentRevision: request.documentRevision,
      catalogRevision: request.catalogRevision,
      error: error?.message || 'script analysis failed',
    });
  }
};
