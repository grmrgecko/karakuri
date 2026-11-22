// Vite embeds TypeScript's ES declarations in the worker. The full ES bundles
// are excluded because they reference DOM declarations; no host library or
// remote declaration request is part of analysis.
const declarationModules = import.meta.glob(
  [
    '../../../../node_modules/typescript/lib/lib.es*.d.ts',
    '../../../../node_modules/typescript/lib/lib.decorators*.d.ts',
    '!../../../../node_modules/typescript/lib/lib.es*.full.d.ts',
  ],
  // exhaustive is required because Vite otherwise ignores node_modules globs.
  { eager: true, exhaustive: true, query: '?raw', import: 'default' },
) as Record<string, string>;

export const defaultScriptLibrary = 'lib.es2023.d.ts';

export const scriptLibraries: Record<string, string> = {};
for (const [path, source] of Object.entries(declarationModules)) {
  const name = path.split('/').pop();
  if (name) scriptLibraries[name] = source;
}

export function scriptLibrary(fileName: string): string | undefined {
  return scriptLibraries[fileName.split('/').pop() ?? fileName];
}
