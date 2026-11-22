// Maps OpenWeatherMap icon codes to Makin-Things weather-icons filenames.
// Icon source: https://github.com/Makin-Things/weather-icons (MIT License).
// OWM icons use format like "01d" (clear sky day), "01n" (clear sky night), etc.

const iconMapping: Record<string, string> = {
  "01d": "clear-day",
  "01n": "clear-night",
  "02d": "cloudy-1-day",
  "02n": "cloudy-1-night",
  "03d": "cloudy-2-day",
  "03n": "cloudy-2-night",
  "04d": "cloudy-3-day",
  "04n": "cloudy-3-night",
  "09d": "rainy-1-day",
  "09n": "rainy-1-night",
  "10d": "rainy-2-day",
  "10n": "rainy-2-night",
  "11d": "isolated-thunderstorms-day",
  "11n": "isolated-thunderstorms-night",
  "13d": "snowy-2-day",
  "13n": "snowy-2-night",
  "50d": "fog-day",
  "50n": "fog-night",
};

// Returns the URL for a weather icon given an OWM icon code.
export function getIconUrl(iconCode: string, theme: string = "static"): string {
  const name = iconMapping[iconCode] || "cloudy";
  return `/widgets/weather/icons/${theme}/${name}.svg`;
}

// Returns a human-readable weather description for an icon code.
// Used as alt text and fallback when icons fail to load.
export function getIconDescription(iconCode: string): string {
  const descriptions: Record<string, string> = {
    "01d": "Clear sky",
    "01n": "Clear night",
    "02d": "Partly cloudy",
    "02n": "Partly cloudy night",
    "03d": "Cloudy",
    "03n": "Cloudy",
    "04d": "Overcast",
    "04n": "Overcast",
    "09d": "Drizzle",
    "09n": "Drizzle",
    "10d": "Rain",
    "10n": "Rain",
    "11d": "Thunderstorms",
    "11n": "Thunderstorms",
    "13d": "Snow",
    "13n": "Snow",
    "50d": "Fog",
    "50n": "Fog",
  };
  return descriptions[iconCode] || "Unknown weather";
}
