import { writable } from 'svelte/store';
import { api } from '../api/client';
import { dashboardContext } from '../dashboardContext';

type MessageHandler = (data: unknown, topic: string) => void;

// topicMatches mirrors the backend broker's segment matcher: "*" consumes one
// or more colon-separated segments, so "run:*" matches "run:<id>".
function topicMatches(pattern: string, topic: string): boolean {
  if (pattern === topic) return true;
  if (!pattern.includes('*')) return false;
  const pat = pattern.split(':');
  const top = topic.split(':');
  const match = (pi: number, ti: number): boolean => {
    while (pi < pat.length && ti < top.length) {
      if (pat[pi] === '*') {
        for (let consumed = 1; consumed <= top.length - ti; consumed++) {
          if (match(pi + 1, ti + consumed)) return true;
        }
        return false;
      }
      if (pat[pi] !== top[ti]) return false;
      pi++;
      ti++;
    }
    return pi === pat.length && ti === top.length;
  };
  return match(0, 0);
}

interface WsState {
  connected: boolean;
  reconnecting: boolean;
}

function createWebSocketStore() {
  const { subscribe, set } = writable<WsState>({
    connected: false,
    reconnecting: false,
  });

  let ws: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let pingTimer: ReturnType<typeof setInterval> | null = null;
  const subscriptions = new Map<string, Set<MessageHandler>>();
  // Topics the server withdrew from the open socket that the page still
  // wants. They are asked for again once, when the server next accepts a
  // token, so a topic lost for good is not requested in a loop.
  const revoked = new Set<string>();
  let reconnectAttempt = 0;
  const maxReconnectDelay = 30000;
  // The token the open socket authenticated with. It rides the URL, so a
  // sign-in or sign-out has to be reconnected rather than sent down the wire.
  let connectedToken: string | null = null;

  function getWsUrl(): string {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = api.getToken();
    let url = `${proto}//${window.location.host}/ws`;
    if (token) url += `?token=${encodeURIComponent(token)}`;
    return url;
  }

  function connect() {
    if (ws?.readyState === WebSocket.OPEN) return;

    connectedToken = api.getToken();
    // Handlers close over their own socket and stand down once it is no longer
    // the current one: a deliberate close, and the old socket of a reconnect,
    // both land here while a replacement is already opening.
    const sock = new WebSocket(getWsUrl());
    ws = sock;

    sock.onopen = () => {
      reconnectAttempt = 0;
      set({ connected: true, reconnecting: false });

      // Re-subscribe to all active topics.
      revoked.clear();
      for (const topic of subscriptions.keys()) {
        sock.send(JSON.stringify(subscribeFrame(topic)));
      }

      pingTimer = setInterval(() => {
        sock.send(JSON.stringify({ type: 'ping' }));
      }, 30000);
    };

    sock.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        // The server rechecks open sockets. An expired session is answered
        // with a refreshed token over the same socket, so a wall panel that
        // makes no other request keeps its subscriptions.
        if (msg.type === 'auth_expired') {
          void api.refreshSession().then((ok) => {
            if (ok && ws === sock) presentToken(sock, api.getToken());
          }).catch(() => {});
          return;
        }
        if (msg.type === 'revoked') {
          // An expired session makes the socket anonymous, and the same pass
          // withdraws every topic an anonymous socket may not hold.
          if (subscriptions.has(msg.topic)) revoked.add(msg.topic);
          return;
        }
        if (msg.type === 'auth') {
          if (msg.payload !== 'ok') return;
          for (const topic of revoked) {
            if (subscriptions.has(topic)) sock.send(JSON.stringify(subscribeFrame(topic)));
          }
          revoked.clear();
          return;
        }
        if (msg.type === 'error') {
          // A refused subscription names its topic, but its payload is the
          // refusal, not data. Handing it to the topic's handlers would put
          // "permission denied" where a widget expects a reading.
          console.debug('websocket subscription refused', msg.topic, msg.payload);
          return;
        }
        if (msg.topic) {
          // Dispatch to exact and wildcard subscriptions.
          for (const [pattern, handlers] of subscriptions) {
            if (!topicMatches(pattern, msg.topic)) continue;
            for (const handler of handlers) {
              handler(msg.payload, msg.topic);
            }
          }
        }
      } catch {
        // Ignore malformed messages.
      }
    };

    sock.onclose = () => {
      if (ws !== sock) return;
      if (pingTimer) clearInterval(pingTimer);
      set({ connected: false, reconnecting: true });
      scheduleReconnect();
    };

    sock.onerror = () => {
      sock.close();
    };
  }

  // presentToken sends a new access token over an open socket, which keeps its
  // subscriptions and has the server recheck them under the new identity.
  function presentToken(sock: WebSocket, token: string | null) {
    if (!token || sock.readyState !== WebSocket.OPEN) return;
    connectedToken = token;
    sock.send(JSON.stringify({ type: 'auth', payload: token }));
  }

  // A refresh made by any request is passed to the socket too, so it never
  // holds a token older than the page's.
  api.onTokenChange((token) => {
    if (ws && token && connectedToken && token !== connectedToken) presentToken(ws, token);
  });

  // subscribeFrame names the dashboard on screen alongside the topic. A viewer
  // holds no permission for ha:/control:/mqttdevice: topics, so without it the
  // dashboard renders its first values over HTTP and then never moves; with it
  // the server checks the topic against what that dashboard's layout binds to.
  function subscribeFrame(topic: string): Record<string, string> {
    const dashboard = dashboardContext();
    return dashboard ? { type: 'subscribe', topic, dashboard } : { type: 'subscribe', topic };
  }

  function scheduleReconnect() {
    if (reconnectTimer) return;
    const delay = Math.min(1000 * Math.pow(2, reconnectAttempt), maxReconnectDelay);
    reconnectAttempt++;
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, delay);
  }

  function disconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (pingTimer) {
      clearInterval(pingTimer);
      pingTimer = null;
    }
    ws?.close();
    ws = null;
    connectedToken = null;
    set({ connected: false, reconnecting: false });
  }

  return {
    subscribe,

    connect,

    disconnect,

    // reconnect reopens the socket when the identity it was opened with has
    // changed. Signing in or out swaps which topics the server will allow, and
    // the token rides the URL, so an open socket keeps answering as whoever
    // opened it.
    reconnect() {
      if (ws && connectedToken === api.getToken()) return;
      disconnect();
      reconnectAttempt = 0;
      connect();
    },

    subscribeTopic(topic: string, handler: MessageHandler) {
      const known = subscriptions.has(topic);
      if (!known) subscriptions.set(topic, new Set());
      // A withdrawn topic is asked for again: the new subscriber may be a
      // dashboard whose binding grants it.
      if ((!known || revoked.delete(topic)) && ws?.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify(subscribeFrame(topic)));
      }
      subscriptions.get(topic)!.add(handler);

      return () => {
        const handlers = subscriptions.get(topic);
        if (handlers) {
          handlers.delete(handler);
          if (handlers.size === 0) {
            subscriptions.delete(topic);
            revoked.delete(topic);
            if (ws?.readyState === WebSocket.OPEN) {
              ws.send(JSON.stringify({ type: 'unsubscribe', topic }));
            }
          }
        }
      };
    },
  };
}

export const websocket = createWebSocketStore();
