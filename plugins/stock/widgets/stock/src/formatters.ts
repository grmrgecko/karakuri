// Format a price with appropriate decimal places.
export function formatPrice(price: number, currency: string = "USD"): string {
  if (price >= 1) {
    return price.toFixed(2);
  }
  return price.toFixed(4);
}

// Format a change value with sign.
export function formatChange(change: number): string {
  const sign = change >= 0 ? "+" : "";
  return `${sign}${change.toFixed(2)}`;
}

// Format a percent change with sign and symbol.
export function formatPercent(percent: number): string {
  const sign = percent >= 0 ? "+" : "";
  return `${sign}${percent.toFixed(2)}%`;
}

// Format volume with K/M/B suffixes.
export function formatVolume(volume: number): string {
  if (volume >= 1_000_000_000) {
    return `${(volume / 1_000_000_000).toFixed(1)}B`;
  }
  if (volume >= 1_000_000) {
    return `${(volume / 1_000_000).toFixed(1)}M`;
  }
  if (volume >= 1_000) {
    return `${(volume / 1_000).toFixed(1)}K`;
  }
  return volume.toString();
}

// Format market cap with B/T suffixes.
export function formatMarketCap(cap: number): string {
  if (cap >= 1_000_000_000_000) {
    return `$${(cap / 1_000_000_000_000).toFixed(2)}T`;
  }
  if (cap >= 1_000_000_000) {
    return `$${(cap / 1_000_000_000).toFixed(2)}B`;
  }
  if (cap >= 1_000_000) {
    return `$${(cap / 1_000_000).toFixed(2)}M`;
  }
  return `$${cap.toLocaleString()}`;
}

// Get the CSS color class based on change direction and color logic.
export function changeColor(value: number, colorLogic: string): string {
  if (value === 0) return "neutral";
  if (colorLogic === "green-red") {
    return value > 0 ? "negative" : "positive";
  }
  return value > 0 ? "positive" : "negative";
}
