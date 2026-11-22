export interface AutoScaleOptions {
  /** Minimum scale factor. Default: 0.1 */
  minScale?: number;
  /** Maximum scale factor. Default: 10 */
  maxScale?: number;
  /** Inner padding to respect in pixels. Default: 0 */
  padding?: number;
  /** Fit mode: 'contain' fits inside, 'cover' fills container. Default: 'contain' */
  mode?: 'contain' | 'cover';
}

export interface FitTextOptions {
  /** Minimum font size in pixels. Default: 6 */
  minFontSize?: number;
  /** Maximum font size in pixels. Default: 200 */
  maxFontSize?: number;
  /** Precision threshold in pixels to stop binary search. Default: 0.5 */
  precision?: number;
  /** Maximum binary search iterations. Default: 20 */
  maxIterations?: number;
  /** Only constrain to width, ignore height. Default: false */
  widthOnly?: boolean;
  /** Only constrain to height, ignore width. Default: false */
  heightOnly?: boolean;
}

// Measures the content's natural dimensions, then applies transform: scale() to fit
// the container. Scales everything uniformly (text, icons, images, SVGs). Returns a
// cleanup function that disconnects observers.
export function autoScale(
  container: HTMLElement,
  content: HTMLElement,
  options: AutoScaleOptions = {},
): () => void {
  const {
    minScale = 0.1,
    maxScale = 10,
    padding = 0,
    mode = 'contain',
  } = options;

  let rafId = 0;

  function update(): void {
    const containerW = container.clientWidth - padding * 2;
    const containerH = container.clientHeight - padding * 2;
    if (containerW <= 0 || containerH <= 0) return;

    // Shrink-wrap to intrinsic size for measurement. Inline styles override any
    // CSS width/height (e.g. height: 100%) so the element sizes to its content.
    content.style.transform = 'none';
    content.style.width = 'max-content';
    content.style.height = 'max-content';

    const contentW = content.offsetWidth;
    const contentH = content.offsetHeight;
    if (contentW <= 0 || contentH <= 0) return;

    const scaleX = containerW / contentW;
    const scaleY = containerH / contentH;
    const raw = mode === 'contain' ? Math.min(scaleX, scaleY) : Math.max(scaleX, scaleY);
    const scale = Math.min(Math.max(raw, minScale), maxScale);

    // Center the scaled content within the container.
    const scaledW = contentW * scale;
    const scaledH = contentH * scale;
    const tx = (containerW - scaledW) / 2 + padding;
    const ty = (containerH - scaledH) / 2 + padding;

    content.style.transformOrigin = '0 0';
    content.style.transform = `translate(${tx}px, ${ty}px) scale(${scale})`;
  }

  function scheduleUpdate(): void {
    if (rafId) cancelAnimationFrame(rafId);
    rafId = requestAnimationFrame(() => {
      rafId = 0;
      update();
    });
  }

  const resizeObserver = new ResizeObserver(scheduleUpdate);
  resizeObserver.observe(container);

  const mutationObserver = new MutationObserver(scheduleUpdate);
  mutationObserver.observe(content, { childList: true, subtree: true, characterData: true });

  // Save original inline styles so cleanup can restore them.
  const savedStyles = {
    width: content.style.width,
    height: content.style.height,
    transform: content.style.transform,
    transformOrigin: content.style.transformOrigin,
  };

  // Run initial scaling.
  scheduleUpdate();

  return () => {
    if (rafId) cancelAnimationFrame(rafId);
    resizeObserver.disconnect();
    mutationObserver.disconnect();
    content.style.width = savedStyles.width;
    content.style.height = savedStyles.height;
    content.style.transform = savedStyles.transform;
    content.style.transformOrigin = savedStyles.transformOrigin;
  };
}

// Binary search for the optimal font size that fits the content within the container.
// Text reflows naturally at each candidate size. Returns a cleanup function that
// disconnects observers.
export function fitText(
  container: HTMLElement,
  content: HTMLElement,
  options: FitTextOptions = {},
): () => void {
  const {
    minFontSize = 6,
    maxFontSize = 200,
    precision = 0.5,
    maxIterations = 20,
    widthOnly = false,
    heightOnly = false,
  } = options;

  let rafId = 0;

  function update(): void {
    const containerW = container.clientWidth;
    const containerH = container.clientHeight;
    if ((!heightOnly && containerW <= 0) || (!widthOnly && containerH <= 0)) return;

    let low = minFontSize;
    let high = maxFontSize;
    let bestSize = low;

    for (let i = 0; i < maxIterations && (high - low) > precision; i++) {
      const mid = (low + high) / 2;
      content.style.fontSize = mid + 'px';

      const rect = content.getBoundingClientRect();
      const fits = heightOnly
        ? rect.height <= containerH
        : widthOnly
          ? rect.width <= containerW
          : rect.width <= containerW && rect.height <= containerH;

      if (fits) {
        bestSize = mid;
        low = mid;
      } else {
        high = mid;
      }
    }

    content.style.fontSize = bestSize + 'px';
  }

  function scheduleUpdate(): void {
    if (rafId) cancelAnimationFrame(rafId);
    rafId = requestAnimationFrame(() => {
      rafId = 0;
      update();
    });
  }

  const resizeObserver = new ResizeObserver(scheduleUpdate);
  resizeObserver.observe(container);

  const mutationObserver = new MutationObserver(scheduleUpdate);
  mutationObserver.observe(content, { childList: true, subtree: true, characterData: true });

  // Run initial fit.
  scheduleUpdate();

  return () => {
    if (rafId) cancelAnimationFrame(rafId);
    resizeObserver.disconnect();
    mutationObserver.disconnect();
  };
}
