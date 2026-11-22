# Data model: Desktop interaction and vision

Drag uses signed desktop points, left/middle/right button, and duration from zero to thirty seconds. Coordinates are bounded to one million in magnitude; interpolation uses bounded steps. Cleanup runs even after a failed press because delivery may be uncertain.

WindowPlacement has independent Restore, Move, and Resize flags, position, and size. At least one flag is required; resize dimensions are positive and at most 100000. Position uses signed desktop coordinates. Observed window geometry is separate from requested placement.

ImageMatch options contain minimum match percentage greater than zero and at most 100, and result count at most 100. Match percentage is 100 times one minus mean absolute RGB difference divided by 255. It is RGB agreement, not recognition probability. Transparent references are rejected. Results merge overlapping detections and carry image-pixel rectangles. A comparison-budget error never becomes no-match.

Image source metadata reports image width/height, coordinate space image_pixels, and selected window metadata when available. Input images have no inferred desktop origin. All entities are per execution; no persisted schema changes.
