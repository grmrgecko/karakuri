// The editor clipboard for copied canvas fragments. Module-level rather than a
// store: nothing renders from it, and holding it here lets a fragment survive
// navigation between flows so a copy can be pasted into another graph.

import type { GraphDoc } from '../stores/flows';
import type { EditorNode, EditorEdge } from '../components/editor/types';

// ClipboardFragment is a self-contained slice of a graph: the copied nodes,
// the edges wholly between them, and the local subgraph definitions any of
// them reference (recursively), so a copied subflow node still resolves after
// pasting into a different flow.
export interface ClipboardFragment {
  nodes: EditorNode[];
  edges: EditorEdge[];
  subgraphs: Record<string, GraphDoc>;
}

let fragment: ClipboardFragment | null = null;
// pasteCount staggers repeated pastes of the same fragment so each lands
// beside the last instead of on top of it.
let pasteCount = 0;

export function setClipboard(f: ClipboardFragment) {
  fragment = structuredClone(f);
  pasteCount = 0;
}

export function getClipboard(): ClipboardFragment | null {
  return fragment ? structuredClone(fragment) : null;
}

// nextPasteOffset returns the canvas offset for the next paste of the held
// fragment, advancing the stagger.
export function nextPasteOffset(): { x: number; y: number } {
  pasteCount++;
  return { x: 40 * pasteCount, y: 40 * pasteCount };
}
