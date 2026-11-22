import { websocket } from './websocket';

// watchDocument reloads a committed draft after a notification or reconnect.
// Returning false defers a reload while a local gesture or save is in progress.
export function watchDocument(kind: 'flow' | 'dashboard', id: string, refresh: () => Promise<boolean>, onerror: (error: unknown) => void) {
  let stopped = false;
  let running = false;
  let pending = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  function schedule() {
    pending = true;
    if (stopped || running || timer) return;
    timer = setTimeout(async () => {
      timer = undefined;
      if (stopped) return;
      running = true;
      pending = false;
      try { if (!await refresh()) pending = true; }
      catch (error) { onerror(error); }
      finally {
        running = false;
        if (pending && !stopped) schedule();
      }
    }, 150);
  }
  const unsubscribe = websocket.subscribeTopic(`editor:${kind}:${id}`, schedule);
  let connected = false;
  const unsubscribeConnection = websocket.subscribe(state => {
    if (state.connected && !connected) schedule();
    connected = state.connected;
  });
  return () => {
    stopped = true;
    if (timer) clearTimeout(timer);
    unsubscribe();
    unsubscribeConnection();
  };
}
