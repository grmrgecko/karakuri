export interface PromptAttachment {
  filename: string;
  type: string;
  data: string;
}

export interface PromptAssetURL {
  url: string;
  filename: string;
  type: string;
}

export const maxAttachmentBytes = 8 * 1024 * 1024;
export const maxAttachmentTotal = 32 * 1024 * 1024;
export const maxAttachments = 20;
export const promptImageTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];

export function promptAttachmentSet(value: unknown): Record<string, PromptAttachment> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return Object.fromEntries(Object.entries(value).filter(([key, item]) => {
    const a = item as PromptAttachment;
    return /^[A-Za-z0-9._-]{1,64}$/.test(key) && a && typeof a.filename === 'string'
      && typeof a.type === 'string' && typeof a.data === 'string'
      && /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(a.data);
  })) as Record<string, PromptAttachment>;
}

export function attachmentSize(a: PromptAttachment): number {
  return Math.floor(a.data.length / 4) * 3 - (a.data.endsWith('==') ? 2 : a.data.endsWith('=') ? 1 : 0);
}

export function attachmentKey(filename: string, taken: string[]): string {
  const base = filename.replace(/[^A-Za-z0-9._-]+/g, '-').slice(0, 54) || 'file';
  let key = base;
  for (let i = 2; taken.includes(key); i++) key = `${base}-${i}`;
  return key;
}

export async function readPromptAttachment(file: File): Promise<PromptAttachment> {
  if (file.size > maxAttachmentBytes) throw new Error(`${file.name} exceeds 8 MiB.`);
  if (!file.name || new TextEncoder().encode(file.name).length > 255 || /[\x00-\x1f\x7f/\\]/.test(file.name)) {
    throw new Error('Choose a file with a name of at most 255 bytes without path separators.');
  }
  const bytes = new Uint8Array(await file.arrayBuffer());
  const chunks: string[] = [];
  for (let i = 0; i < bytes.length; i += 32768) chunks.push(String.fromCharCode(...bytes.subarray(i, i + 32768)));
  return { filename: file.name, type: file.type || 'application/octet-stream', data: btoa(chunks.join('')) };
}

export function promptAttachmentReference(key: string, file: PromptAttachment, image: boolean): string {
  const label = file.filename.replace(/[\[\]\\]/g, '\\$&');
  return `${image && promptImageTypes.includes(file.type) ? '!' : ''}[${label}](attachment:${key})`;
}

// resolvePromptAssets touches only renderer-produced attachment destinations.
export function resolvePromptAssets(html: string, assets: Record<string, PromptAssetURL>): string {
  const doc = new DOMParser().parseFromString(html, 'text/html');
  for (const img of doc.querySelectorAll('img[src^="attachment:"]')) {
    const asset = assets[(img.getAttribute('src') || '').slice('attachment:'.length)];
    if (asset && promptImageTypes.includes(asset.type)) img.setAttribute('src', asset.url);
    else img.replaceWith(doc.createTextNode(img.getAttribute('alt') || 'Missing image'));
  }
  for (const anchor of doc.querySelectorAll('a')) {
    const href = anchor.getAttribute('href') || '';
    anchor.setAttribute('rel', 'noopener noreferrer');
    if (!href.startsWith('attachment:')) { anchor.setAttribute('target', '_blank'); continue; }
    const asset = assets[href.slice('attachment:'.length)];
    if (!asset) { anchor.removeAttribute('href'); continue; }
    anchor.setAttribute('href', asset.url);
    anchor.setAttribute('download', asset.filename);
  }
  return doc.body.innerHTML;
}

export function localPromptAssets(attachments: Record<string, PromptAttachment>): Record<string, PromptAssetURL> {
  return Object.fromEntries(Object.entries(attachments).map(([key, file]) => [key, {
    url: `data:${promptImageTypes.includes(file.type) ? file.type : 'application/octet-stream'};base64,${file.data}`,
    filename: file.filename, type: file.type,
  }]));
}
