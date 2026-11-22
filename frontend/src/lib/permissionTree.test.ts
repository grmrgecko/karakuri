import { describe, expect, it } from 'vitest';
import { isTerminalPermission, permissionSuggestions, treeFromPermissions } from './permissionTree';

// The API key form completes against the permissions its user holds, built
// into the same tree shape the registry serves the role editor.
describe('treeFromPermissions', () => {
  const tree = treeFromPermissions(['api:flows:read', 'api:flows:write', 'api:mcp:use']);

  it('builds the registry shape from full strings', () => {
    expect(tree).toEqual({ api: { flows: ['read', 'write'], mcp: ['use'] } });
  });

  it('keeps a level that is both an action holder and a parent', () => {
    expect(treeFromPermissions(['api:plugins:read', 'api:plugins:weather:read'])).toEqual({
      api: { plugins: { _actions: ['read'], weather: ['read'] } },
    });
  });

  it('completes the typed segment with a wildcard first', () => {
    expect(permissionSuggestions(tree, 'api:fl')).toEqual(['api:flows']);
    expect(permissionSuggestions(tree, 'api:flows:')).toEqual(['api:flows:*', 'api:flows:read', 'api:flows:write']);
  });

  it('falls back to searching full paths', () => {
    expect(permissionSuggestions(tree, 'mcp')).toEqual(['api:mcp:use']);
  });

  it('tells a level from an action', () => {
    expect(isTerminalPermission(tree, 'api:flows')).toBe(false);
    expect(isTerminalPermission(tree, 'api:flows:read')).toBe(true);
    expect(isTerminalPermission(tree, 'api:*')).toBe(true);
  });
});
