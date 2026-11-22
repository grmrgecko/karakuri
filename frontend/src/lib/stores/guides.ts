import { writable, get } from 'svelte/store';
import { api } from '../api/client';
import { languageLabel } from '../utils/highlight';

// A guide is a reference page rendered on the server from markdown compiled
// into the binary. The frontend carries no markdown parser: it asks for HTML
// and shows it, so the documentation always matches the server it came from.
interface Guide {
  id: string;
  title: string;
  html: string;
}

// GuideEntry is one guide as the help index lists it.
interface GuideEntry {
  id: string;
  title: string;
  group: string;
  summary: string;
}

// GuideRef is what a field offers as its reference: which page, and where in
// it to land. It is a value rather than a boolean so the editor can offer any
// page — a script field offers the sandbox reference, a widget's command
// payload offers the MQTT one — without the editor knowing about either.
interface GuideRef {
  guide: string;
  // title overrides the page's own heading in the viewer's bar.
  title?: string;
  // sections are the headings worth jumping to, by the anchor goldmark made.
  sections?: Array<{ anchor: string; label: string }>;
  // current is the section to open on.
  current?: string;
}

// SCRIPT_SECTIONS are the sandbox reference's language headings, the only page
// so far long enough to need jump targets.
const SCRIPT_SECTIONS = ['javascript', 'python', 'bash'];

// scriptGuide is the reference a script field offers, opened on the language
// being edited: the languages in the sandbox are not quite the languages they
// are named after, which is the question an author has at that moment.
export function scriptGuide(language: string): GuideRef {
  return {
    guide: 'scripts',
    title: 'Scripting reference',
    sections: SCRIPT_SECTIONS.map((anchor) => ({ anchor, label: languageLabel(anchor) })),
    current: language,
  };
}

interface GuideState {
  // byId holds the pages fetched so far. A page never changes for the lifetime
  // of a server process, so one fetch per session is enough.
  byId: Record<string, Guide>;
}

function createGuideStore() {
  const store = writable<GuideState>({ byId: {} });
  // Concurrent openings of the same page share one request.
  const inFlight: Record<string, Promise<Guide> | undefined> = {};
  // The index never changes for the lifetime of a server process either.
  let index: Promise<GuideEntry[]> | null = null;

  return {
    subscribe: store.subscribe,

    // index lists every guide in display order, fetched once per session.
    index(): Promise<GuideEntry[]> {
      if (!index) {
        index = api.guides.list<GuideEntry[]>().catch((e) => {
          index = null;
          throw e;
        });
      }
      return index;
    },

    // load fetches a guide, returning the cached copy when it already has one.
    async load(id: string): Promise<Guide> {
      const cached = get(store).byId[id];
      if (cached) return cached;
      const pending = inFlight[id];
      if (pending) return pending;

      const request = (async () => {
        const guide = await api.guides.get<Guide>(id);
        store.update((s) => ({ byId: { ...s.byId, [id]: guide } }));
        return guide;
      })().finally(() => {
        // A failed fetch leaves nothing cached, so opening the page again
        // retries rather than replaying the failure.
        delete inFlight[id];
      });
      inFlight[id] = request;
      return request;
    },
  };
}

export const guides = createGuideStore();
export type { Guide, GuideEntry, GuideRef };
