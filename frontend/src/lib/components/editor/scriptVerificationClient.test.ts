import { describe, expect, it } from 'vitest';
import type { ScriptVerificationRequest, ScriptVerificationResult } from '../../api/client';
import {
  ScriptVerificationClient,
  utf8ByteOffsetToUTF16,
} from './scriptVerificationClient';

describe('script verification source mapping', () => {
  // The server counts UTF-8 bytes while CodeMirror counts UTF-16 code units, including astral characters.
  it('maps multibyte and astral boundaries without splitting a code point', () => {
    const source = 'aé😀b';
    expect(utf8ByteOffsetToUTF16(source, 0)).toBe(0);
    expect(utf8ByteOffsetToUTF16(source, 1)).toBe(1);
    expect(utf8ByteOffsetToUTF16(source, 3)).toBe(2);
    expect(utf8ByteOffsetToUTF16(source, 5)).toBe(2);
    expect(utf8ByteOffsetToUTF16(source, 7)).toBe(4);
    expect(utf8ByteOffsetToUTF16(source, 8)).toBe(5);
    expect(utf8ByteOffsetToUTF16(source, 100)).toBe(5);
  });
});

interface PendingRequest {
  body: ScriptVerificationRequest;
  resolve: (result: ScriptVerificationResult) => void;
  reject: (error: Error) => void;
}

describe('script verification client', () => {
  // Only the newest edit may publish diagnostics, and a slow response must not overlap the next request.
  it('queues newest edits across language and catalog changes and maps the current response', async () => {
    const requests: PendingRequest[] = [];
    let active = 0;
    let maximumActive = 0;
    const client = new ScriptVerificationClient({
      delay: 0,
      request: (body) => {
        active++;
        maximumActive = Math.max(maximumActive, active);
        return new Promise<ScriptVerificationResult>((resolve, reject) => {
          requests.push({
            body,
            resolve: (result) => {
              active--;
              resolve(result);
            },
            reject: (error) => {
              active--;
              reject(error);
            },
          });
        });
      },
    });

    const old = client.verify('python', 'aé😀b old', 'catalog-one');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(requests).toHaveLength(1);
    expect(requests[0].body).toEqual({ language: 'python', source: 'aé😀b old', pure: false });

    const discarded = client.verify('python', 'newer edit', 'catalog-one');
    const current = client.verify('bash', 'aé😀b current', 'catalog-two');
    await expect(discarded).resolves.toEqual([]);
    requests[0].resolve({
      diagnostics: [{ severity: 'error', code: 'old-code', message: 'old', from: 0, to: 1, line: 1, column: 1 }],
    });
    await expect(old).resolves.toEqual([]);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(requests).toHaveLength(2);
    expect(requests[1].body).toEqual({ language: 'bash', source: 'aé😀b current', pure: false });
    requests[1].resolve({
      diagnostics: [{ severity: 'error', code: 'node-config', message: 'current', from: 3, to: 7, line: 1, column: 3, node_type: 'base64-encode' }],
    });
    await expect(current).resolves.toEqual([{
      severity: 'error',
      code: 'node-config',
      message: 'current',
      from: 2,
      to: 4,
      line: 1,
      column: 3,
      nodeType: 'base64-encode',
    }]);
    expect(maximumActive).toBe(1);

    const activeDisposed = client.verify('python', 'active at close', 'catalog-two');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(requests).toHaveLength(3);
    const queuedDisposed = client.verify('bash', 'not sent', 'catalog-two');
    client.dispose();
    await expect(activeDisposed).resolves.toEqual([]);
    await expect(queuedDisposed).resolves.toEqual([]);
    requests[2].resolve({ diagnostics: [] });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(requests).toHaveLength(3);
  });

  // A verifier outage clears remote diagnostics without affecting the local catalog analyzers.
  it('returns no diagnostics after a transport failure', async () => {
    const client = new ScriptVerificationClient({
      delay: 0,
      request: () => Promise.reject(new Error('offline')),
    });
    await expect(client.verify('python', 'nodes.', 'catalog')).resolves.toEqual([]);
    client.dispose();
  });
});
