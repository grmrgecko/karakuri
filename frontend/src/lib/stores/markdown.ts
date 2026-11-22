import { api } from '../api/client';

// Note text is markdown, and it is rendered on the server: the frontend carries
// no markdown parser, and the renderer the documentation already runs through
// escapes raw markup rather than passing it into the page. Rendering repeats as
// an author types, so results are cached by source text — two notes holding the
// same text, or a note returning to text it held a moment ago, cost one request
// between them.

interface RenderResult {
  html: string;
}

// cacheLimit bounds the cache. Entries are note-sized documents and the oldest
// go first: editing a note fills the cache with keystroke-by-keystroke
// intermediates that will never be asked for again.
const cacheLimit = 200;

function createMarkdownStore() {
  const cache = new Map<string, string>();
  const inFlight = new Map<string, Promise<string>>();

  function remember(source: string, html: string) {
    cache.set(source, html);
    if (cache.size > cacheLimit) {
      const oldest = cache.keys().next();
      if (!oldest.done) cache.delete(oldest.value);
    }
  }

  return {
    // cached returns an already-rendered source, so a note redrawn with text it
    // has rendered before paints without a round-trip or a blank frame.
    cached(source: string): string | undefined {
      return source === '' ? '' : cache.get(source);
    },

    // render returns the HTML for a markdown source, sharing one request
    // between concurrent callers asking for the same text.
    async render(source: string): Promise<string> {
      if (source === '') return '';
      const hit = cache.get(source);
      if (hit !== undefined) return hit;
      const pending = inFlight.get(source);
      if (pending) return pending;

      const request = (async () => {
        const result = await api.guides.render<RenderResult>(source);
        remember(source, result.html);
        return result.html;
      })().finally(() => {
        // A failed render leaves nothing cached, so the next edit retries
        // rather than replaying the failure.
        inFlight.delete(source);
      });
      inFlight.set(source, request);
      return request;
    },
  };
}

export const markdown = createMarkdownStore();
