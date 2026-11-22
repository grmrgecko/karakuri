// Get current time components using the Intl API for timezone support.
export function getTimeComponents(timezone: string, format: string): { h: number; m: number; s: number } {
  const now = new Date();
  const fmt = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone || undefined,
    hour: "numeric",
    minute: "numeric",
    second: "numeric",
    hour12: false,
  });
  const parts = fmt.formatToParts(now);
  let h = 0,
    m = 0,
    s = 0;
  for (const p of parts) {
    if (p.type === "hour") h = parseInt(p.value);
    if (p.type === "minute") m = parseInt(p.value);
    if (p.type === "second") s = parseInt(p.value);
  }
  // Intl with hour12:false can return 24 for midnight in some locales.
  if (h === 24) h = 0;
  if (format === "12h") {
    h = h % 12;
    if (h === 0) h = 12;
  }
  return { h, m, s };
}

// Split a number into its two BCD digits.
// e.g., 23 => [2, 3], each digit then convertible to 4-bit binary.
export function toBcdDigits(n: number): [number, number] {
  const tens = Math.floor(n / 10);
  const ones = n % 10;
  return [tens, ones];
}

// Convert a number to an array of bits (MSB first), padded to the specified length.
export function toBits(n: number, length: number): number[] {
  const bits = [];
  for (let i = length - 1; i >= 0; i--) {
    bits.push((n >> i) & 1);
  }
  return bits;
}
