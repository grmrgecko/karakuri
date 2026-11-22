import { describe, expect, it, vi } from 'vitest';

// The client reads localStorage and the page address when it is built, so
// both exist before it is imported. vi.hoisted runs above the imports.
const storage = vi.hoisted(() => {
  const items = new Map<string, string>();
  const page = { location: { href: '/', search: '', protocol: 'http:' } };
  globalThis.localStorage = {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => void items.set(k, v),
    removeItem: (k: string) => void items.delete(k),
  } as Storage;
  (globalThis as any).window = page;
  return { items, page };
});

import { api } from './client';

// A reverse proxy answers 502 or 503 while the service restarts. A request
// whose access token had expired then got that answer from /auth/refresh,
// and the client treated it as a refused refresh: it removed both tokens and
// sent the page to /login, so a wall panel was signed out by a restart.
describe('api session refresh', () => {
  it('keeps the session when the refresh gets a gateway error', async () => {
    storage.items.set('access_token', 'expired');
    storage.items.set('refresh_token', 'refresh');
    api.setToken('expired');
    const answers = [
      new Response(JSON.stringify({ error: 'UNAUTHORIZED' }), { status: 401 }),
      new Response('Service Unavailable', { status: 503, statusText: 'Service Unavailable' }),
    ];
    vi.stubGlobal('fetch', async () => answers.shift());

    await expect(api.get('/auth/me')).rejects.toBeTruthy();
    expect(storage.items.get('refresh_token'), 'a 503 says nothing about the session').toBe('refresh');
    expect(storage.items.get('access_token')).toBe('expired');
    expect(storage.page.location.href, 'the page must stay where it is').toBe('/');
  });
});
