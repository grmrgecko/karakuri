import { writable, derived, get } from 'svelte/store';
import { api } from '../api/client';
import { auth } from './auth';

interface Theme {
  id: number;
  name: string;
  package_name: string;
  version: string;
  description: string;
  author: string;
  variables: string;
}

function createThemeStore() {
  const { subscribe, set, update } = writable<{
    themes: Theme[];
    defaultPackage: string;
    active: Theme | null;
    loading: boolean;
  }>({
    themes: [],
    defaultPackage: '',
    active: null,
    loading: false,
  });

  return {
    subscribe,

    async list(userThemeId?: number | null) {
      update((s) => ({ ...s, loading: true }));
      try {
        const res = await api.get<{ themes: Theme[]; default_theme: string }>('/themes');
        const themes = res.themes;
        const defaultPackage = res.default_theme || '';
        const defaultTheme = themes.find((t) => t.package_name === defaultPackage) || themes[0] || null;
        const userTheme = userThemeId ? themes.find((t) => t.id === userThemeId) : null;
        update((s) => ({
          ...s,
          themes,
          defaultPackage,
          active: s.active || userTheme || defaultTheme,
          loading: false,
        }));
      } catch {
        update((s) => ({ ...s, loading: false }));
      }
    },

    apply(theme: Theme) {
      update((s) => ({ ...s, active: theme }));

      try {
        const variables: Record<string, string> = JSON.parse(theme.variables);
        const root = document.documentElement;
        for (const [key, value] of Object.entries(variables)) {
          root.style.setProperty(`--theme-${key}`, value);
        }
      } catch {
        // Malformed variables JSON; leave current CSS state unchanged.
      }
    },

    applyFromStylesheet(packageName: string) {
      // Drop every previously injected theme stylesheet, not just the one for
      // this package, so switching themes cannot accumulate competing links.
      document.querySelectorAll('link[id^="karakuri-theme-"]').forEach((el) => el.remove());

      const link = document.createElement('link');
      link.rel = 'stylesheet';
      link.href = `/themes/${packageName}/styles.css`;
      link.id = `karakuri-theme-${packageName}`;
      document.head.appendChild(link);
    },

    // Applies a dashboard-specific theme by ID, or restores the user's preferred theme if null.
    applyById(themeId: number | null) {
      let state: { themes: Theme[]; defaultPackage: string; active: Theme | null; loading: boolean };
      const unsub = subscribe((s) => { state = s; });
      unsub();

      if (themeId) {
        const theme = state!.themes.find((t) => t.id === themeId);
        if (theme) {
          this.apply(theme);
          this.applyFromStylesheet(theme.package_name);
          return;
        }
      }

      // Read the user's preferred theme from their stored preferences.
      const authState = get(auth);
      const userThemeId = authState.user?.preferences?.theme_id as number | undefined;
      const userTheme = userThemeId ? state!.themes.find((t) => t.id === userThemeId) : null;
      const restoreTheme = userTheme
        || state!.themes.find((t) => t.package_name === state!.defaultPackage)
        || state!.themes[0]
        || null;
      if (restoreTheme) {
        this.apply(restoreTheme);
        this.applyFromStylesheet(restoreTheme.package_name);
      }
    },
  };
}

export const themes = createThemeStore();
export type { Theme };
