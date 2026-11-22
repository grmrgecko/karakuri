// Dotted-path access to nested option objects, for the generated option
// forms. Setting undefined removes the key, so a cleared field falls back to
// its default rather than saving an empty value.

/** getPath reads a.b.c from an object. */
export function getPath(root: unknown, path: string): any {
  let v: any = root;
  for (const key of path.split(".")) {
    if (v === null || v === undefined || typeof v !== "object") return undefined;
    v = v[key];
  }
  return v;
}

/** setPath returns a copy of root with a.b.c set, copying each object on the way. A numeric key indexes a list. */
export function setPath<T extends Record<string, any>>(root: T, path: string, value: unknown): T {
  const keys = path.split(".");
  const out: Record<string, any> = { ...(root ?? {}) };
  let target = out;
  for (let i = 0; i < keys.length - 1; i++) {
    const next = target[keys[i]];
    if (Array.isArray(next)) target[keys[i]] = [...next];
    else if (next && typeof next === "object") target[keys[i]] = { ...next };
    else target[keys[i]] = /^\d+$/.test(keys[i + 1]) ? [] : {};
    target = target[keys[i]];
  }
  const last = keys[keys.length - 1];
  if (value === undefined) delete target[last];
  else target[last] = value;
  return out as T;
}
