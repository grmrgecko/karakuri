// Images attached to a node are held in its config as data URIs, keyed by
// name, and referred to from markdown as `![alt](image:name)`. The bytes ride
// along with the graph, which is what makes an exported flow show the same
// pictures somewhere else with nothing to fetch. Mirrors pkg/flow/images.go:
// the same names, the same value shape, and the same limits, because a save
// the server refuses is worse than a paste the editor refuses.

// maxImageBytes caps one image at 2 MiB, and maxImages caps how many a node
// may hold. Both mirror flow.MaxImageBytes and flow.MaxImagesPerNode.
export const maxImageBytes = 2 * 1024 * 1024;
export const maxImages = 20;

// imageTypes are the types every browser renders and goldmark already allows
// through as a data URI. An SVG is markup, not a picture, and is not on the
// list for the same reason raw HTML is escaped.
export const imageTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];

// imageDataPattern is the only value shape trusted into an img src: a base64
// data URI over the exact alphabet, so a stored value cannot carry a quote and
// escape the attribute it is written into.
const imageDataPattern = /^data:image\/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/]+={0,2}$/;

// imageRefPattern matches the src the renderer emits for an image reference.
// The name charset matches what a markdown destination survives unescaped, so
// what comes back out is what went in.
const imageRefPattern = /src="image:([A-Za-z0-9._-]{1,64})"/g;

// isImageData reports whether a value may be placed in an img src.
export function isImageData(value: unknown): value is string {
  return typeof value === 'string' && imageDataPattern.test(value);
}

// imageSet reads a node's images config as a name-to-data-URI map, dropping
// anything that does not belong there. A graph is a stored document that a
// hand edit or an older client could have written, so what the editor renders
// is filtered here rather than trusted.
export function imageSet(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  const images: Record<string, string> = {};
  for (const [name, data] of Object.entries(value as Record<string, unknown>)) {
    if (/^[A-Za-z0-9._-]{1,64}$/.test(name) && isImageData(data)) images[name] = data;
  }
  return images;
}

// resolveImages swaps attached images into rendered HTML. The server never
// sees the bytes — it renders `image:name` into an img src and the picture is
// substituted here — so a note re-renders on every keystroke without carrying
// megabytes to the server and back. A reference with no image behind it is
// left alone, so a missing attachment reads as a broken image rather than
// silently rendering nothing.
export function resolveImages(html: string, images: Record<string, string>): string {
  return html.replace(imageRefPattern, (whole, name: string) => {
    const data = images[name];
    return isImageData(data) ? `src="${data}"` : whole;
  });
}

// imageMarkdown is the reference that displays an attached image.
export function imageMarkdown(name: string): string {
  return `![${name}](image:${name})`;
}

// imageRefRegExp matches every markdown reference to one image, for removing
// them when the image is detached.
export function imageRefRegExp(name: string): RegExp {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return new RegExp(`!\\[[^\\]]*\\]\\(image:${escaped}\\)`, 'g');
}

// imageName turns a file name into one an image can be stored under: the
// allowed charset, with a suffix when the note already holds that name so a
// second paste never replaces the first.
export function imageName(fileName: string, type: string, taken: string[]): string {
  const ext = type.slice('image/'.length).replace('jpeg', 'jpg');
  let base = fileName
    .replace(/\.[^.]*$/, '')
    .replace(/[^A-Za-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48);
  if (!base) base = 'image';

  let name = `${base}.${ext}`;
  for (let i = 2; taken.includes(name); i++) name = `${base}-${i}.${ext}`;
  return name;
}

// readImage reads a pasted or dropped file into a data URI, refusing anything
// that is not an image the note can carry. The message is the one shown to the
// author, so it says which rule was hit.
export function readImage(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!imageTypes.includes(file.type)) {
      reject(new Error(`${file.name || 'That file'} is not a PNG, JPEG, GIF, or WebP image.`));
      return;
    }
    if (file.size > maxImageBytes) {
      const mb = (file.size / (1024 * 1024)).toFixed(1);
      reject(new Error(`${file.name || 'That image'} is ${mb} MB; the limit is 2 MB.`));
      return;
    }
    const reader = new FileReader();
    reader.onerror = () => reject(new Error('The image could not be read.'));
    reader.onload = () => {
      const data = String(reader.result ?? '');
      // The reader produces the data URI directly, but a file whose bytes do
      // not match its declared type would produce something else; the same
      // check the server applies decides whether it is stored.
      if (!isImageData(data)) {
        reject(new Error('The image could not be read as a PNG, JPEG, GIF, or WebP.'));
        return;
      }
      resolve(data);
    };
    reader.readAsDataURL(file);
  });
}

// imageBytes reports the decoded size of a stored image, for the attachment
// list. It is computed from the length rather than by decoding, which mirrors
// what the server does.
export function imageBytes(data: string): number {
  const payload = data.slice(data.indexOf(',') + 1);
  const n = Math.floor(payload.length / 4) * 3;
  if (payload.endsWith('==')) return n - 2;
  if (payload.endsWith('=')) return n - 1;
  return n;
}
