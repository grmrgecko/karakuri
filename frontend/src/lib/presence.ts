// presence animates a placement in and out without unmounting it. Hiding a
// widget by removing its element would reload it on every page flip, and a
// rotating dashboard flips every few seconds, so a hidden placement stays in
// the document with visibility: hidden and takes no pointer events, styled by
// the data-presence rules in app.css.

export interface PresenceOptions {
  visible: boolean;
  animateIn?: string;
  animateOut?: string;
  /** duration is in milliseconds; 0 switches without animating. */
  duration?: number;
  /**
   * collapse removes a hidden cell from layout instead of leaving its space,
   * for the region layout where placements stack and the rest should close
   * up behind one that hides.
   */
  collapse?: boolean;
}

/** DEFAULT_ANIMATION is used when a placement names none. */
const DEFAULT_ANIMATION = 'fade';
const DEFAULT_DURATION = 300;

/** keyframesFor returns the frames entering with an animation; leaving plays them reversed. */
function keyframesFor(name: string): Keyframe[] | null {
  switch (name) {
    case 'fade':
      return [{ opacity: 0 }, { opacity: 1 }];
    case 'slide-up':
      return [{ opacity: 0, transform: 'translateY(24px)' }, { opacity: 1, transform: 'none' }];
    case 'slide-down':
      return [{ opacity: 0, transform: 'translateY(-24px)' }, { opacity: 1, transform: 'none' }];
    case 'slide-left':
      return [{ opacity: 0, transform: 'translateX(24px)' }, { opacity: 1, transform: 'none' }];
    case 'slide-right':
      return [{ opacity: 0, transform: 'translateX(-24px)' }, { opacity: 1, transform: 'none' }];
    case 'zoom':
      return [{ opacity: 0, transform: 'scale(0.9)' }, { opacity: 1, transform: 'none' }];
  }
  return null;
}

/** presence is a Svelte action applying PresenceOptions to a placement's cell. */
export function presence(node: HTMLElement, options: PresenceOptions) {
  let visible = options.visible;
  let collapse = options.collapse ?? false;
  let running: Animation | null = null;

  // The state is an attribute rather than inline style: the cell's style
  // attribute carries its geometry and is rewritten whenever that changes.
  const settle = (show: boolean) => {
    if (show) node.removeAttribute('data-presence');
    else node.setAttribute('data-presence', collapse ? 'collapsed' : 'hidden');
    node.setAttribute('aria-hidden', show ? 'false' : 'true');
  };
  settle(visible);

  return {
    update(next: PresenceOptions) {
      collapse = next.collapse ?? false;
      if (next.visible === visible) return;
      visible = next.visible;
      running?.cancel();
      running = null;

      const name = (visible ? next.animateIn : next.animateOut) || DEFAULT_ANIMATION;
      const duration = next.duration ?? DEFAULT_DURATION;
      const frames = keyframesFor(name);
      if (!frames || duration <= 0 || typeof node.animate !== 'function') {
        settle(visible);
        return;
      }
      if (visible) {
        settle(true);
        running = node.animate(frames, { duration, easing: 'ease-out' });
        return;
      }
      // The exit holds its last frame until the cell is hidden, or the
      // widget would flash back at full opacity for a frame.
      const exit = node.animate([...frames].reverse(), { duration, easing: 'ease-in', fill: 'forwards' });
      running = exit;
      exit.onfinish = () => {
        if (!visible) settle(false);
        exit.cancel();
        if (running === exit) running = null;
      };
    },
    destroy() {
      running?.cancel();
    },
  };
}
