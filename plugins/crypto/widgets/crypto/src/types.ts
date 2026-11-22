export interface CryptoQuote {
  pair: string;
  base: string;
  quote: string;
  price: number;
  change_24h: number;
  change_pct_24h: number;
  high_24h: number;
  low_24h: number;
  volume_24h: number;
  market_cap?: number;
  rank?: number;
  timestamp: number;
  name?: string;
  image?: string;
  sparkline_data?: number[];
}

export interface PricePoint {
  t: number;
  p: number;
}

export interface SearchResult {
  pair: string;
  base: string;
  quote: string;
  name: string;
  image?: string;
  exchange?: string;
}

export interface CryptoConfig {
  pair: string;
  layout: string;
  provider: string;
  topCount: number;
  quoteCurrency: string;
  holdings: PortfolioHolding[];
  sourceMode?: string;
  pairs?: string[];
  timeline?: string;
}

export interface PortfolioHolding {
  pair: string;
  quantity: number;
}

export interface ProviderEntry {
  name: string;
  endpoint: string;
  display_name?: string;
}
