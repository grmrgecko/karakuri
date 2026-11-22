// The widget is one binding drawn large, so its config is an EntitySource plus
// how much of it to show. Nothing here resolves a source or builds a command:
// that lives in ../../shared, shared with the Entities Card, so a light bound
// here and a light bound in a card behave the same.

import type { EntitySource } from "../../shared/entitySource";

export type {
  CommandRequest,
  EntitySource,
  Resolved,
  SourceKind,
  TriggerPress,
} from "../../shared/entitySource";

export {
  BLANK_RESOLVED,
  commandRequest,
  configuredPresses,
  displayOnly,
  formatValue,
  haDomain,
  haService,
  loadControlKinds,
  pressable,
  primaryAttribute,
  resolveControl,
  resolveDevice,
  applyDeviceMessage,
  resolveHA,
  triggerPresses,
  truthy,
} from "../../shared/entitySource";

export interface TileConfig extends EntitySource {
  /** showName draws the name above the control; a labelled cell may not need it. */
  showName: boolean;
  /** showState draws the current reading beside the control. */
  showState: boolean;
}

export const DEFAULT_CONFIG: TileConfig = {
  source: "control",
  showName: true,
  showState: true,
};

/** parseConfig reads the host's config attribute, whatever form it arrives in. */
export function parseConfig(attr: string | Record<string, unknown>): TileConfig {
  let parsed: Record<string, unknown> = {};
  if (typeof attr === "string") {
    try {
      parsed = JSON.parse(attr);
    } catch {
      parsed = {};
    }
  } else if (attr && typeof attr === "object") {
    parsed = attr;
  }
  // The host injects the placement's IDs on the way in; they are its
  // bookkeeping, not the binding's, so they never become part of what the
  // config panel edits and dispatches back.
  const { dashboardId, dashboardWidgetId, ...fields } = parsed;
  void dashboardId;
  void dashboardWidgetId;

  return {
    ...DEFAULT_CONFIG,
    ...(fields as Partial<TileConfig>),
    // A placement saved before these existed reads as showing both, which is
    // what an unconfigured tile looked like.
    showName: fields.showName !== false,
    showState: fields.showState !== false,
  };
}
