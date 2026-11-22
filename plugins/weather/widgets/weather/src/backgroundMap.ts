// Maps OWM icon codes to background categories for condition-based backgrounds.
// Supports two modes: CSS gradients (lightweight) and CC0 photographs.

type BackgroundCategory =
  | "clear-day"
  | "clear-night"
  | "partly-cloudy-day"
  | "partly-cloudy-night"
  | "overcast"
  | "rain"
  | "thunderstorm"
  | "snow"
  | "fog";

const conditionMap: Record<string, BackgroundCategory> = {
  "01d": "clear-day",
  "01n": "clear-night",
  "02d": "partly-cloudy-day",
  "02n": "partly-cloudy-night",
  "03d": "partly-cloudy-day",
  "03n": "partly-cloudy-night",
  "04d": "overcast",
  "04n": "overcast",
  "09d": "rain",
  "09n": "rain",
  "10d": "rain",
  "10n": "rain",
  "11d": "thunderstorm",
  "11n": "thunderstorm",
  "13d": "snow",
  "13n": "snow",
  "50d": "fog",
  "50n": "fog",
};

const gradientMap: Record<BackgroundCategory, string> = {
  "clear-day": "linear-gradient(135deg, #f6d365 0%, #fda085 100%)",
  "clear-night": "linear-gradient(135deg, #0f0c29 0%, #1a1a4e 50%, #24243e 100%)",
  "partly-cloudy-day": "linear-gradient(135deg, #89b4fa 0%, #c9a86c 100%)",
  "partly-cloudy-night": "linear-gradient(135deg, #1e2a4a 0%, #4a5568 100%)",
  "overcast": "linear-gradient(135deg, #636e7b 0%, #8e99a4 100%)",
  "rain": "linear-gradient(135deg, #2c3e50 0%, #4ca1af 100%)",
  "thunderstorm": "linear-gradient(135deg, #1a0a2e 0%, #5b2c8e 50%, #7b4fc4 100%)",
  "snow": "linear-gradient(135deg, #c9d6e3 0%, #e8eef5 50%, #b0c4de 100%)",
  "fog": "linear-gradient(135deg, #7f8c8d 0%, #95a5a6 50%, #bdc3c7 100%)",
};

// Returns the background category for an OWM icon code.
export function getCondition(iconCode: string): BackgroundCategory {
  return conditionMap[iconCode] || "overcast";
}

// Returns a CSS gradient string for the given weather icon code.
export function getGradient(iconCode: string): string {
  const category = getCondition(iconCode);
  return gradientMap[category];
}

// Returns the URL for a background photograph matching the weather condition.
export function getBackgroundImageUrl(iconCode: string): string {
  const category = getCondition(iconCode);
  return `/widgets/weather/backgrounds/${category}.jpg`;
}
