// Detects embeddable video platforms and returns an embed URL if applicable.

export interface EmbedResult {
  platform: string;
  embedUrl: string;
}

interface EmbedPattern {
  name: string;
  match: RegExp;
  embed: (id: string) => string;
}

const patterns: EmbedPattern[] = [
  {
    // youtube.com/watch?v=ID or youtube.com/embed/ID
    name: "youtube",
    match: /(?:youtube\.com\/(?:watch\?.*v=|embed\/)|youtu\.be\/)([a-zA-Z0-9_-]{11})/,
    embed: (id: string) => `https://www.youtube.com/embed/${id}`,
  },
  {
    // vimeo.com/ID
    name: "vimeo",
    match: /vimeo\.com\/(\d+)/,
    embed: (id: string) => `https://player.vimeo.com/video/${id}`,
  },
  {
    // dailymotion.com/video/ID
    name: "dailymotion",
    match: /dailymotion\.com\/video\/([a-zA-Z0-9]+)/,
    embed: (id: string) => `https://www.dailymotion.com/embed/video/${id}`,
  },
];

// Returns { platform, embedUrl } if the URL matches a known platform, or null otherwise.
export function detectEmbed(url: string): EmbedResult | null {
  if (!url) return null;
  for (const p of patterns) {
    const m = url.match(p.match);
    if (m) {
      return { platform: p.name, embedUrl: p.embed(m[1]) };
    }
  }
  return null;
}
