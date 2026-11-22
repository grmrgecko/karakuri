// Shared type definitions for weather data structures.

export interface CurrentWeather {
  dt?: number;
  temp: number;
  feels_like: number;
  humidity: number;
  pressure: number;
  wind_speed: number;
  wind_deg: number;
  wind_gust?: number;
  uvi: number;
  clouds?: number;
  visibility: number;
  dew_point: number;
  description: string;
  icon: string;
  weather_code?: string;
  sunrise?: number;
  sunset?: number;
  moon_phase?: number;
}

export interface HourlyWeather {
  dt: number;
  temp: number;
  icon: string;
  pop: number;
  wind_speed: number;
  description?: string;
}

export interface DailyWeather {
  dt: number;
  temp_max: number;
  temp_min: number;
  icon: string;
  pop: number;
  sunrise?: number;
  sunset?: number;
  moonrise?: number;
  moonset?: number;
  moon_phase?: number;
  description?: string;
}

export interface MinutelyWeather {
  dt: number;
  precipitation: number;
}

export interface WeatherAlert {
  sender_name: string;
  event: string;
  start: number;
  end: number;
  description: string;
}

// Pollen counts in grains/m³. A missing field has no reading for the location.
export interface Pollen {
  alder?: number;
  birch?: number;
  grass?: number;
  mugwort?: number;
  olive?: number;
  ragweed?: number;
}

export interface AirQualityHour {
  dt: number;
  us_aqi?: number;
  european_aqi?: number;
}

// AirQuality is current air quality in µg/m³ (metric in both unit systems).
// A missing field has no reading; pollen is absent outside Europe.
export interface AirQuality {
  dt: number;
  us_aqi?: number;
  european_aqi?: number;
  pm2_5?: number;
  pm10?: number;
  ozone?: number;
  nitrogen_dioxide?: number;
  carbon_monoxide?: number;
  sulphur_dioxide?: number;
  uv_index?: number;
  pollen?: Pollen;
  hourly?: AirQualityHour[];
}

export interface ForecastData {
  current: CurrentWeather | null;
  hourly: HourlyWeather[];
  daily: DailyWeather[];
  minutely: MinutelyWeather[];
  alerts: WeatherAlert[];
  air_quality?: AirQuality;
}

export interface WeatherConfig {
  latitude: number;
  longitude: number;
  locationName: string;
  units: string;
  language: string;
  iconTheme: string;
  layout: string;
  provider: string;
  backgroundStyle: string;
  /** aqiScale picks the index the air quality layout leads with. */
  aqiScale: "us" | "european";
}

export interface GeoResult {
  name: string;
  lat: number;
  lon: number;
  country: string;
  state?: string;
}

export interface ProviderEntry {
  name: string;
  endpoint: string;
  display_name?: string;
}
