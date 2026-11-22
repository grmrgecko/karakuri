export interface StockQuote {
  symbol: string;
  name: string;
  price: number;
  change: number;
  change_percent: number;
  open: number;
  high: number;
  low: number;
  previous_close: number;
  volume: number;
  market_cap?: number;
  timestamp: number;
  currency: string;
  exchange: string;
  market_state: string;
}

export interface OHLCCandle {
  t: number;
  o: number;
  h: number;
  l: number;
  c: number;
  v: number;
}

export interface OHLCData {
  symbol: string;
  interval: string;
  candles: OHLCCandle[];
}

export interface SearchResult {
  symbol: string;
  name: string;
  type: string;
  exchange: string;
}

export interface PortfolioHolding {
  symbol: string;
  quantity: number;
}

export interface StockConfig {
  symbols: string[];
  layout: string;
  interval: string;
  colorLogic: string;
  provider: string;
  sourceMode?: string;
  topCount?: number;
  holdings?: PortfolioHolding[];
}

export interface ProviderEntry {
  name: string;
  endpoint: string;
  display_name?: string;
}
