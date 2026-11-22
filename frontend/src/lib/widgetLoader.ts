const loadedWidgets = new Set<string>();
const loadingPromises = new Map<string, Promise<void>>();

export async function loadWidget(packageName: string, entryPoint: string = 'index.js'): Promise<void> {
  if (loadedWidgets.has(packageName)) return;

  // Coalesce concurrent loads for the same widget.
  if (loadingPromises.has(packageName)) {
    return loadingPromises.get(packageName);
  }

  const promise = (async () => {
    try {
      await import(/* @vite-ignore */ `/widgets/${packageName}/${entryPoint}`);
      loadedWidgets.add(packageName);
    } catch (error) {
      console.error(`Failed to load widget: ${packageName}`, error);
      throw error;
    } finally {
      loadingPromises.delete(packageName);
    }
  })();

  loadingPromises.set(packageName, promise);
  return promise;
}

export function isWidgetLoaded(packageName: string): boolean {
  return loadedWidgets.has(packageName);
}
