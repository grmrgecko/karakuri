import { parseAttr } from "../../shared/dashboardLive";

export interface WifiQrConfig {
  ssid: string;
  password: string;
  security: "WPA" | "WEP" | "nopass";
  hidden: boolean;
  /** label is the line above the code; empty draws "Join {ssid}". */
  label: string;
  /** showPassword prints the password under the code. */
  showPassword: boolean;
  /** colors is "plain" for black on white, or "theme" for the theme's text on its base. */
  colors: "plain" | "theme";
}

export const DEFAULT_CONFIG: WifiQrConfig = {
  ssid: "",
  password: "",
  security: "WPA",
  hidden: false,
  label: "",
  showPassword: false,
  colors: "plain",
};

const SECURITIES = ["WPA", "WEP", "nopass"];

/** parseConfig reads the host's config attribute, falling back field by field. */
export function parseConfig(attr: string | Record<string, unknown>): WifiQrConfig {
  const fields = parseAttr(attr);
  const str = (v: unknown, fallback: string) => (typeof v === "string" ? v : fallback);
  return {
    ssid: str(fields.ssid, DEFAULT_CONFIG.ssid),
    password: str(fields.password, DEFAULT_CONFIG.password),
    security: SECURITIES.includes(String(fields.security))
      ? (fields.security as WifiQrConfig["security"])
      : DEFAULT_CONFIG.security,
    hidden: fields.hidden === true,
    label: str(fields.label, DEFAULT_CONFIG.label),
    showPassword: fields.showPassword === true,
    colors: fields.colors === "theme" ? "theme" : "plain",
  };
}

// escapeField backslash-escapes the characters the Wi-Fi URI format reserves
// (ZXing's MECARD-style grammar, which phone cameras follow).
function escapeField(value: string): string {
  return value.replace(/[\\;,:"]/g, (c) => "\\" + c);
}

/**
 * wifiPayload is the string phones recognise as a network to join. An open
 * network carries no password field.
 */
export function wifiPayload(cfg: WifiQrConfig): string {
  let s = `WIFI:T:${cfg.security};S:${escapeField(cfg.ssid)};`;
  if (cfg.security !== "nopass") s += `P:${escapeField(cfg.password)};`;
  s += `H:${cfg.hidden ? "true" : "false"};;`;
  return s;
}
