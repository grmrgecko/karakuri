// copyText puts text on the system clipboard. The Clipboard API exists only in
// a secure context, so a server reached over plain HTTP by a LAN address has
// no navigator.clipboard; there the text is copied from a hidden text area,
// which browsers still allow inside a click.

/** copyText copies text and reports whether the browser accepted it. */
export async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // A denied permission falls through to the selection copy.
    }
  }
  return copyBySelection(text);
}

function copyBySelection(text: string): boolean {
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.position = 'fixed';
  area.style.top = '0';
  area.style.left = '0';
  area.style.opacity = '0';
  document.body.appendChild(area);
  const active = document.activeElement as HTMLElement | null;
  area.select();
  area.setSelectionRange(0, text.length);
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch {
    ok = false;
  }
  area.remove();
  active?.focus?.();
  return ok;
}
