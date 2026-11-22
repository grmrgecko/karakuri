// Type overrides for svelte-routing v2 to work with Svelte 5 component types.
// svelte-routing expects `typeof SvelteComponent<any>` but Svelte 5 components
// are `Component` (function-based). The library works at runtime via Svelte 5's
// legacy compatibility mode; these declarations fix the type errors.
declare module 'svelte-routing' {
  import type { Component, Snippet } from 'svelte';

  interface RouterProps {
    basepath?: string;
    url?: string;
    children?: Snippet;
  }

  interface LinkProps {
    to: string;
    replace?: boolean;
    state?: object;
    getProps?: (props: {
      location: { pathname: string; search: string; hash: string };
      href: string;
      isPartiallyCurrent: boolean;
      isCurrent: boolean;
    }) => object;
    children?: Snippet;
    [key: string]: any;
  }

  interface RouteProps {
    path?: string;
    component?: Component<any> | (() => Promise<{ default: Component<any> }>);
    children?: Snippet<[{ params: Record<string, string>; location: any }]>;
    [key: string]: any;
  }

  export const Router: Component<RouterProps>;
  export const Route: Component<RouteProps>;
  export const Link: Component<LinkProps>;

  export function navigate(to: string, options?: { replace?: boolean; state?: object }): void;
  export function link(node: HTMLAnchorElement): { destroy(): void };
  export function links(node: HTMLElement): { destroy(): void };

  export function useLocation(): {
    subscribe(fn: (location: { pathname: string; search: string; hash: string }) => void): () => void;
  };

  export function useResolve(): (path: string) => string;
}
