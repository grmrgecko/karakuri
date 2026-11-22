import { writable } from 'svelte/store';

type ConfirmState = {
  show: boolean;
  message: string;
  resolve: ((value: boolean) => void) | null;
};

const store = writable<ConfirmState>({ show: false, message: '', resolve: null });

export function confirm(message: string): Promise<boolean> {
  return new Promise((resolve) => {
    store.set({ show: true, message, resolve });
  });
}

export const confirmDialog = { subscribe: store.subscribe };

export function resolveConfirm(value: boolean) {
  store.update((s) => {
    s.resolve?.(value);
    return { show: false, message: '', resolve: null };
  });
}
