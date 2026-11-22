// uid generates a short random identifier suitable for node and edge IDs.
const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789';

export function uid(length = 6): string {
  const bytes = crypto.getRandomValues(new Uint8Array(length));
  let out = '';
  for (let i = 0; i < length; i++) {
    out += alphabet[bytes[i] % alphabet.length];
  }
  return out;
}
