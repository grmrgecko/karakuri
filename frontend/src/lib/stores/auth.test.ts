import { beforeEach, describe, expect, it, vi } from 'vitest';

// The API client reads localStorage when it is built, which the test runtime
// lacks, so it is a stand-in that records what the store does to the session.
// vi.mock is hoisted above the imports.
const io = vi.hoisted(() => ({
  token: 'access' as string | null,
  refresh: 'refresh' as string | null,
  me: async (): Promise<unknown> => ({}),
}));
vi.mock('../api/client', () => ({
  api: {
    getToken: () => io.token,
    setToken: (t: string | null) => { io.token = t; },
    setRefreshToken: (t: string | null) => { io.refresh = t; },
    auth: { me: () => io.me() },
  },
}));

import { auth } from './auth';

// A page opened while the server restarts gets no answer to /auth/me: fetch
// rejects with a TypeError, and a refresh that cannot connect rejects with
// the client's NETWORK error. Neither says the session ended, and the client
// keeps the tokens for that reason. init used to treat every failure as a
// refused session and removed both tokens, so a wall panel that reloaded
// during a restart was signed out for good. A reverse proxy in front of the
// stopped service answers 502 or 503 instead, which is no more a refusal.
describe('auth.init', () => {
  beforeEach(() => {
    io.token = 'access';
    io.refresh = 'refresh';
  });

  it.each([
    ['fetch could not connect', new TypeError('Failed to fetch')],
    ['the refresh could not connect', { code: 'NETWORK', message: 'the server is unreachable' }],
    ['a proxy answered for the stopped service', { code: 'UNKNOWN', message: 'Bad Gateway', status: 502 }],
    ['the server failed', { code: 'INTERNAL_ERROR', message: 'internal error', status: 500 }],
  ])('keeps the session when %s', async (_name, failure) => {
    io.me = async () => { throw failure; };
    await auth.init();
    expect(io.token, 'an unreachable server must not cost the access token').toBe('access');
    expect(io.refresh, 'an unreachable server must not cost the refresh token').toBe('refresh');
  });

  it('drops the session the server refused', async () => {
    io.me = async () => { throw { code: 'FORBIDDEN', message: 'account disabled' }; };
    await auth.init();
    expect(io.token).toBeNull();
    expect(io.refresh).toBeNull();
  });
});
