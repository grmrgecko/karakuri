import { Position } from '@xyflow/svelte';
import { editor } from '../../stores/editor';

// Rotation is presentation state stored on a node the way its color is, in
// config under _rotation, in degrees clockwise. Zero is the card as it stood
// before nodes could turn at all, so an absent value needs no migration. Every
// node type that turns reads it through here, because the answers below —
// which side a socket faces, how a drag becomes an angle — have to be the same
// on a card and on a reroute's dot or the two would drift apart.

// nodeRotation is the angle a node was turned to, or zero.
export function nodeRotation(config: Record<string, unknown>): number {
  return typeof config._rotation === 'number' ? config._rotation : 0;
}

// A turned node carries its sockets round with it, so the side a wire should
// leave from is the node's own side turned by the same angle, snapped to the
// quadrant it landed in. This only aims the wire: the socket itself stays on
// the node's edge, which each node's own styles pin it to.
const inputSides = [Position.Left, Position.Top, Position.Right, Position.Bottom];
const outputSides = [Position.Right, Position.Bottom, Position.Left, Position.Top];
const bottomSides = [Position.Bottom, Position.Left, Position.Top, Position.Right];

// handleSides reports which way each side of a turned node now faces. bottom
// is a request node's return socket, which lives on the bottom edge rather
// than either port column.
export function handleSides(rotation: number): { input: Position; output: Position; bottom: Position } {
  const quadrant = ((Math.round(rotation / 90) % 4) + 4) % 4;
  return { input: inputSides[quadrant], output: outputSides[quadrant], bottom: bottomSides[quadrant] };
}

// pointerAngle is the screen angle from a node's centre to the pointer, in the
// same degrees the transform is written in.
function pointerAngle(cx: number, cy: number, x: number, y: number): number {
  return (Math.atan2(y - cy, x - cx) * 180) / Math.PI;
}

// startNodeRotate turns a node for as long as the grip is held. The grips ride
// the node, so the gesture is measured as an angle about its centre rather than
// a distance: the pointer keeps whatever bearing it grabbed at. The grip must be
// a direct child of the element that turns, whose box is what the angle is
// measured about.
export function startNodeRotate(event: PointerEvent, nodeId: string, rotation: number) {
  if (event.button !== 0) return;
  // The grip owns the press. Left to itself it would start a node drag, and
  // the node would be dragged around instead of turned.
  event.preventDefault();
  event.stopPropagation();
  const box = (event.currentTarget as HTMLElement).parentElement?.getBoundingClientRect();
  if (!box) return;
  // A turned node's box is still centred on the point it turns about, so the
  // centre read here survives the rotation the drag is about to apply.
  const cx = box.left + box.width / 2;
  const cy = box.top + box.height / 2;
  const grab = pointerAngle(cx, cy, event.clientX, event.clientY) - rotation;

  const move = (e: PointerEvent) => {
    // A press released outside the window leaves no button down; the node
    // stays where it was rather than following the pointer around.
    if (e.buttons === 0) {
      end();
      return;
    }
    const turned = pointerAngle(cx, cy, e.clientX, e.clientY) - grab;
    // Shift locks to quarter turns. Those are the angles a graph is actually
    // laid out on — a column of nodes reading downward — and they are the only
    // ones where a socket lands exactly on a side, so the lock is what makes
    // both the turn and the way back to square exact.
    const deg = e.shiftKey ? Math.round(turned / 90) * 90 : Math.round(turned);
    editor.requestNodeRotate(nodeId, ((deg % 360) + 360) % 360);
  };
  const end = () => {
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', end);
    window.removeEventListener('pointercancel', end);
  };
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', end);
  window.addEventListener('pointercancel', end);
}
