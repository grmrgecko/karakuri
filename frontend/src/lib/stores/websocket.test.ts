import { describe, expect, it, vi } from 'vitest';

// The API client reads localStorage when it is built, which the test runtime
// lacks, so it is a stand-in whose refresh always succeeds with a new token.
// vi.mock is hoisted above the imports.
const io = vi.hoisted(() => ({ token: 'old' }));
vi.mock('../api/client', () => ({
  api: {
    getToken: () => io.token,
    onTokenChange: () => () => {},
    refreshSession: async () => {
      io.token = 'new';
      return true;
    },
  },
}));

import { websocket } from './websocket';

type Frame = { type: string; topic?: string; payload?: unknown };

// FakeSocket records what the page sends and lets the test speak as the hub.
class FakeSocket {
  static OPEN = 1;
  static last: FakeSocket;
  readyState = 0;
  sent: Frame[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeSocket.last = this;
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {}
  receive(frame: Frame) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

// When an access token expires the hub makes the socket anonymous, sends
// auth_expired, and in the same pass withdraws every subscription an
// anonymous socket may not hold, naming each in a revoked frame
// (pkg/mq/handler.go revalidate). The page presented its refreshed token but
// never asked for the withdrawn topics again, so an editor left open past the
// token's hour received nothing until it was reloaded.
describe('websocket after an expired session', () => {
  it('subscribes again, once, to the topics the hub withdrew', async () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    vi.stubGlobal('window', { location: { protocol: 'http:', host: 'localhost' } });

    websocket.connect();
    const sock = FakeSocket.last;
    sock.readyState = FakeSocket.OPEN;
    sock.onopen!();

    const stop = websocket.subscribeTopic('flow:f1:runs', () => {});
    const subscribes = () => sock.sent.filter((f) => f.type === 'subscribe' && f.topic === 'flow:f1:runs').length;
    expect(subscribes(), 'the first subscriber asks for the topic').toBe(1);

    sock.receive({ type: 'auth_expired' });
    sock.receive({ type: 'revoked', topic: 'flow:f1:runs' });
    await Promise.resolve();
    await Promise.resolve();
    expect(sock.sent.filter((f) => f.type === 'auth'), 'the refreshed token is presented').toEqual([{ type: 'auth', payload: 'new' }]);
    sock.receive({ type: 'auth', payload: 'ok' });
    expect(subscribes(), 'the withdrawn topic is asked for again under the new token').toBe(2);

    // A topic the new identity may not hold is withdrawn again. It must wait
    // for the next accepted token and not be requested in a loop.
    sock.receive({ type: 'revoked', topic: 'flow:f1:runs' });
    expect(subscribes(), 'a withdrawal alone asks for nothing').toBe(2);
    sock.receive({ type: 'auth', payload: 'denied' });
    expect(subscribes(), 'a refused token asks for nothing').toBe(2);

    stop();
    websocket.disconnect();
    vi.unstubAllGlobals();
  });
});

// A refused subscription is answered with an error frame that names the topic
// (pkg/mq/handler.go subscribe). It was dispatched to the topic's handlers as
// if it were a reading, so a crypto widget in a render took the string
// "permission denied" for a quote, threw while drawing it, and was
// photographed on its loading spinner.
describe('websocket refused subscription', () => {
  it('does not hand the refusal to the topic handlers', () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    vi.stubGlobal('window', { location: { protocol: 'http:', host: 'localhost' } });

    websocket.connect();
    const sock = FakeSocket.last;
    sock.readyState = FakeSocket.OPEN;
    sock.onopen!();

    const received: unknown[] = [];
    const stop = websocket.subscribeTopic('market:crypto:coinbase:BTC/USD', (data) => received.push(data));

    sock.receive({ type: 'error', topic: 'market:crypto:coinbase:BTC/USD', payload: 'permission denied' });
    expect(received, 'a refusal is not a reading').toEqual([]);

    sock.receive({ type: 'message', topic: 'market:crypto:coinbase:BTC/USD', payload: { price: 1 } });
    expect(received, 'a published reading still arrives').toEqual([{ price: 1 }]);

    stop();
    websocket.disconnect();
  });
});
