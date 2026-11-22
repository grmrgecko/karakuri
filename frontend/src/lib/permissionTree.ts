// A permission tree mirrors the registry's nested map: each segment is a key,
// leaves are action lists, and a node that is both holds its actions under
// "_actions". The role editor and the API key form complete typed permission
// paths against one, so both offer the same choices the same way.

export type PermissionTree = Record<string, unknown>;

/**
 * treeFromPermissions builds a tree from full permission strings, such as the
 * ones a user may hand to an API key, so they complete like the registry's.
 */
export function treeFromPermissions(perms: string[]): PermissionTree {
  const tree: PermissionTree = {};
  for (const p of perms) {
    const parts = p.split(':');
    if (parts.length < 2) continue;
    let node: Record<string, unknown> = tree;
    for (let i = 0; i < parts.length - 2; i++) {
      const key = parts[i];
      let child = node[key];
      if (Array.isArray(child)) {
        child = { _actions: child };
        node[key] = child;
      } else if (!child || typeof child !== 'object') {
        child = {};
        node[key] = child;
      }
      node = child as Record<string, unknown>;
    }
    const leaf = parts[parts.length - 2];
    const action = parts[parts.length - 1];
    const existing = node[leaf];
    if (Array.isArray(existing)) {
      if (!existing.includes(action)) existing.push(action);
    } else if (existing && typeof existing === 'object') {
      const holder = existing as Record<string, unknown>;
      const actions = (holder._actions as string[] | undefined) ?? [];
      if (!actions.includes(action)) actions.push(action);
      holder._actions = actions;
    } else {
      node[leaf] = [action];
    }
  }
  return tree;
}

/** flattenPermissions lists every full permission path in a tree. */
export function flattenPermissions(node: unknown, prefix = ''): string[] {
  if (Array.isArray(node)) {
    return node.map((action) => (prefix ? `${prefix}:${action}` : String(action)));
  }
  if (!node || typeof node !== 'object') return [];
  const paths: string[] = [];
  for (const [key, child] of Object.entries(node as Record<string, unknown>)) {
    if (key === '_actions') {
      for (const action of child as string[]) paths.push(prefix ? `${prefix}:${action}` : action);
    } else {
      paths.push(...flattenPermissions(child, prefix ? `${prefix}:${key}` : key));
    }
  }
  return paths;
}

/**
 * permissionSuggestions walks the tree to the segment being typed and offers the
 * keys and actions at that level, plus a wildcard. When nothing at that level
 * matches it searches full paths, so "mcp" still finds api:mcp:use.
 */
export function permissionSuggestions(tree: PermissionTree, input: string): string[] {
  const parts = input.split(':');
  let node: unknown = tree;
  for (let i = 0; i < parts.length - 1; i++) {
    if (node && typeof node === 'object' && !Array.isArray(node) && parts[i] in (node as object)) {
      node = (node as Record<string, unknown>)[parts[i]];
    } else {
      node = null;
      break;
    }
  }

  let suggestions: string[] = [];
  if (node) {
    const prefix = parts.slice(0, -1).join(':');
    const current = parts[parts.length - 1].toLowerCase();
    let items: string[] = [];
    if (Array.isArray(node)) {
      items = ['*', ...node];
    } else if (typeof node === 'object') {
      const obj = node as Record<string, unknown>;
      items = ['*', ...Object.keys(obj).filter((k) => k !== '_actions')];
      if (Array.isArray(obj._actions)) items = [...items, ...(obj._actions as string[])];
    }
    suggestions = items.filter((k) => k.toLowerCase().startsWith(current)).map((k) => (prefix ? `${prefix}:${k}` : k));
  }

  const query = input.trim().toLowerCase();
  if (suggestions.length === 0 && query !== '') {
    suggestions = flattenPermissions(tree).filter((p) => p.toLowerCase().includes(query));
  }
  return suggestions;
}

/**
 * isTerminalPermission reports whether a suggestion ends a path (an action or
 * a wildcard) rather than naming a level that takes another segment.
 */
export function isTerminalPermission(tree: PermissionTree, suggestion: string): boolean {
  let node: unknown = tree;
  for (const part of suggestion.split(':')) {
    if (part === '*') return true;
    if (node && typeof node === 'object' && !Array.isArray(node) && part in (node as object)) {
      node = (node as Record<string, unknown>)[part];
    } else {
      return true;
    }
  }
  return !(Array.isArray(node) || (typeof node === 'object' && node !== null));
}
