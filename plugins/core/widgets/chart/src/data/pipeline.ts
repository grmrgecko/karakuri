// The chart's data pipeline: server answer to frames, transformations, hidden
// queries dropped, field options resolved.

import type { ChartResponse, FieldConfigSource, Frame, Transformation } from "./types";
import { framesFromResponse, type QueryFailure } from "./frames";
import { applyTransformations } from "./transformations";
import { applyFieldConfig, dropHidden } from "./fieldConfig";

export interface Prepared {
  frames: Frame[];
  failures: QueryFailure[];
}

/** prepare turns a data route answer into the frames a visualization draws. */
export function prepare(
  res: ChartResponse | null | undefined,
  opts: { transformations: Transformation[]; fieldConfig: FieldConfigSource; hidden: string[] },
): Prepared {
  const { frames, failures } = framesFromResponse(res);
  let out = applyTransformations(frames, opts.transformations);
  out = dropHidden(out, new Set(opts.hidden));
  out = applyFieldConfig(out, opts.fieldConfig);
  return { frames: out, failures };
}
