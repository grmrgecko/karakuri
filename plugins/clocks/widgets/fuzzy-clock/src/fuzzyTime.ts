// Fuzzy time engine for the fuzzy clock widget.
// Converts numeric hours and minutes into natural language text at varying
// levels of precision, from exact minute phrasing to broad time-of-day labels.

export interface FuzzyTimeResult {
  sentence: string;
  hourWord: string;
  minutePhrase: string;
  prefix: string;
  suffix: string;
  period: string;
  words: string[];
}

export interface GridCell {
  letter: string;
  wordId: string | null;
  row: number;
  col: number;
}

export interface ApproximateTimeResult {
  label: string;
  icon: string;
}

const HOURS: string[] = [
  "twelve", "one", "two", "three", "four", "five",
  "six", "seven", "eight", "nine", "ten", "eleven",
];

const HOURS_UPPER: string[] = HOURS.map((h) => h.toUpperCase());

const MINUTE_WORDS: string[] = [
  "", "one", "two", "three", "four", "five",
  "six", "seven", "eight", "nine", "ten",
  "eleven", "twelve", "thirteen", "fourteen", "fifteen",
  "sixteen", "seventeen", "eighteen", "nineteen", "twenty",
  "twenty-one", "twenty-two", "twenty-three", "twenty-four", "twenty-five",
  "twenty-six", "twenty-seven", "twenty-eight", "twenty-nine",
];

const FIVE_MIN_WORDS: string[] = [
  "o'clock", "five past", "ten past", "quarter past",
  "twenty past", "twenty-five past", "half past",
  "twenty-five to", "twenty to", "quarter to",
  "ten to", "five to",
];

/**
 * Extracts hour and minute from the current time in a given timezone using
 * Intl.DateTimeFormat, then delegates to buildFuzzyTime for natural language
 * conversion.
 */
export function getFuzzyTime(timezone: string, fuzziness: number): FuzzyTimeResult {
  const now = new Date();
  const fmt = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone || undefined,
    hour: "numeric",
    minute: "numeric",
    second: "numeric",
    hour12: false,
  });
  const parts = fmt.formatToParts(now);
  let h = 0;
  let m = 0;
  for (const p of parts) {
    if (p.type === "hour") h = parseInt(p.value, 10);
    if (p.type === "minute") m = parseInt(p.value, 10);
  }
  // Handle the edge case where hour 24 is returned instead of 0.
  if (h === 24) h = 0;

  return buildFuzzyTime(h, m, fuzziness);
}

/**
 * Converts a 24-hour time into a natural language fuzzy time object. The
 * returned object contains the full sentence, separated word components for
 * layout rendering, and metadata about which hour word is active.
 */
export function buildFuzzyTime(h: number, m: number, fuzziness: number): FuzzyTimeResult {
  const hour12 = h % 12;
  const nextHour = (hour12 + 1) % 12;
  const hourWord = HOURS[hour12];
  const nextHourWord = HOURS[nextHour];
  const period = h < 12 ? "am" : "pm";

  if (fuzziness >= 5) {
    // Hour only: round to the nearest hour.
    const roundedH = m >= 30 ? nextHour : hour12;
    return {
      sentence: `around ${HOURS[roundedH]}`,
      hourWord: HOURS[roundedH],
      minutePhrase: "around",
      prefix: "around",
      suffix: "",
      period,
      words: ["AROUND", HOURS_UPPER[roundedH]],
    };
  }

  if (fuzziness >= 4) {
    // Half-hour precision.
    if (m < 15) {
      return {
        sentence: `${hourWord} o'clock`,
        hourWord,
        minutePhrase: "o'clock",
        prefix: "",
        suffix: "o'clock",
        period,
        words: [hourWord.toUpperCase(), "O'CLOCK"],
      };
    }
    if (m < 45) {
      return {
        sentence: `half past ${hourWord}`,
        hourWord,
        minutePhrase: "half past",
        prefix: "half past",
        suffix: "",
        period,
        words: ["HALF", "PAST", hourWord.toUpperCase()],
      };
    }
    return {
      sentence: `${nextHourWord} o'clock`,
      hourWord: nextHourWord,
      minutePhrase: "o'clock",
      prefix: "",
      suffix: "o'clock",
      period,
      words: [nextHourWord.toUpperCase(), "O'CLOCK"],
    };
  }

  if (fuzziness >= 3) {
    // Quarter-hour precision.
    if (m < 8) {
      return {
        sentence: `${hourWord} o'clock`,
        hourWord,
        minutePhrase: "o'clock",
        prefix: "",
        suffix: "o'clock",
        period,
        words: [hourWord.toUpperCase(), "O'CLOCK"],
      };
    }
    if (m < 23) {
      return {
        sentence: `quarter past ${hourWord}`,
        hourWord,
        minutePhrase: "quarter past",
        prefix: "quarter past",
        suffix: "",
        period,
        words: ["QUARTER", "PAST", hourWord.toUpperCase()],
      };
    }
    if (m < 38) {
      return {
        sentence: `half past ${hourWord}`,
        hourWord,
        minutePhrase: "half past",
        prefix: "half past",
        suffix: "",
        period,
        words: ["HALF", "PAST", hourWord.toUpperCase()],
      };
    }
    if (m < 53) {
      return {
        sentence: `quarter to ${nextHourWord}`,
        hourWord: nextHourWord,
        minutePhrase: "quarter to",
        prefix: "quarter to",
        suffix: "",
        period,
        words: ["QUARTER", "TO", nextHourWord.toUpperCase()],
      };
    }
    return {
      sentence: `${nextHourWord} o'clock`,
      hourWord: nextHourWord,
      minutePhrase: "o'clock",
      prefix: "",
      suffix: "o'clock",
      period,
      words: [nextHourWord.toUpperCase(), "O'CLOCK"],
    };
  }

  if (fuzziness >= 2) {
    // Five-minute precision. Round to the nearest 5 minutes.
    const rounded = Math.round(m / 5) * 5;
    const slot = (rounded / 5) % 12;

    if (slot === 0) {
      // On the hour.
      const effectiveHour = rounded === 60 ? nextHour : hour12;
      const hw = HOURS[effectiveHour];
      return {
        sentence: `${hw} o'clock`,
        hourWord: hw,
        minutePhrase: "o'clock",
        prefix: "",
        suffix: "o'clock",
        period,
        words: [hw.toUpperCase(), "O'CLOCK"],
      };
    }

    if (slot <= 6) {
      // Past the hour.
      const phrase = FIVE_MIN_WORDS[slot];
      return {
        sentence: `${phrase} ${hourWord}`,
        hourWord,
        minutePhrase: phrase,
        prefix: phrase,
        suffix: "",
        period,
        words: [...phrase.toUpperCase().split(" "), hourWord.toUpperCase()],
      };
    }

    // To the next hour.
    const phrase = FIVE_MIN_WORDS[slot];
    return {
      sentence: `${phrase} ${nextHourWord}`,
      hourWord: nextHourWord,
      minutePhrase: phrase,
      prefix: phrase,
      suffix: "",
      period,
      words: [...phrase.toUpperCase().split(" "), nextHourWord.toUpperCase()],
    };
  }

  // Fuzziness 1: exact minute phrasing.
  if (m === 0) {
    return {
      sentence: `${hourWord} o'clock`,
      hourWord,
      minutePhrase: "o'clock",
      prefix: "",
      suffix: "o'clock",
      period,
      words: [hourWord.toUpperCase(), "O'CLOCK"],
    };
  }

  if (m === 15) {
    return {
      sentence: `quarter past ${hourWord}`,
      hourWord,
      minutePhrase: "quarter past",
      prefix: "quarter past",
      suffix: "",
      period,
      words: ["QUARTER", "PAST", hourWord.toUpperCase()],
    };
  }

  if (m === 30) {
    return {
      sentence: `half past ${hourWord}`,
      hourWord,
      minutePhrase: "half past",
      prefix: "half past",
      suffix: "",
      period,
      words: ["HALF", "PAST", hourWord.toUpperCase()],
    };
  }

  if (m === 45) {
    return {
      sentence: `quarter to ${nextHourWord}`,
      hourWord: nextHourWord,
      minutePhrase: "quarter to",
      prefix: "quarter to",
      suffix: "",
      period,
      words: ["QUARTER", "TO", nextHourWord.toUpperCase()],
    };
  }

  if (m <= 30) {
    // "X past Y" for minutes 1-30.
    const minWord = MINUTE_WORDS[m];
    return {
      sentence: `${minWord} past ${hourWord}`,
      hourWord,
      minutePhrase: `${minWord} past`,
      prefix: `${minWord} past`,
      suffix: "",
      period,
      words: [...minWord.toUpperCase().split("-").join(" ").split(" "), "PAST", hourWord.toUpperCase()],
    };
  }

  // "X to Y" for minutes 31-59.
  const remaining = 60 - m;
  const minWord = MINUTE_WORDS[remaining];
  return {
    sentence: `${minWord} to ${nextHourWord}`,
    hourWord: nextHourWord,
    minutePhrase: `${minWord} to`,
    prefix: `${minWord} to`,
    suffix: "",
    period,
    words: [...minWord.toUpperCase().split("-").join(" ").split(" "), "TO", nextHourWord.toUpperCase()],
  };
}

// Word clock grid definition. Each row is an array of word objects
// containing the text and its column span in the 11-column grid. Filler
// letters occupy single-character slots and are never highlighted.

const GRID_ROWS: Array<Array<{ text: string; id: string | null; len: number }>> = [
  // Row 0: I T L I S A S A M P M
  [
    { text: "IT",  id: "IT",  len: 2 },
    { text: "L",   id: null,  len: 1 },
    { text: "IS",  id: "IS",  len: 2 },
    { text: "A",   id: null,  len: 1 },
    { text: "S",   id: null,  len: 1 },
    { text: "AM",  id: "AM",  len: 2 },
    { text: "PM",  id: "PM",  len: 2 },
  ],
  // Row 1: A C Q U A R T E R D C
  [
    { text: "AC",  id: null,  len: 2 },
    { text: "QUARTER", id: "QUARTER", len: 7 },
    { text: "DC",  id: null,  len: 2 },
  ],
  // Row 2: T W E N T Y F I V E X
  [
    { text: "TWENTY", id: "TWENTY", len: 6 },
    { text: "FIVE",   id: "MFIVE", len: 4 },
    { text: "X",      id: null,   len: 1 },
  ],
  // Row 3: H A L F S T E N F T O
  [
    { text: "HALF", id: "HALF", len: 4 },
    { text: "S",    id: null,   len: 1 },
    { text: "TEN",  id: "MTEN", len: 3 },
    { text: "F",    id: null,   len: 1 },
    { text: "TO",   id: "TO",   len: 2 },
  ],
  // Row 4: P A S T E R U N I N E
  [
    { text: "PAST",  id: "PAST",  len: 4 },
    { text: "ERU",   id: null,    len: 3 },
    { text: "NINE",  id: "NINE",  len: 4 },
  ],
  // Row 5: O N E S I X T H R E E
  [
    { text: "ONE",   id: "ONE",   len: 3 },
    { text: "SIX",   id: "SIX",   len: 3 },
    { text: "THREE", id: "THREE", len: 5 },
  ],
  // Row 6: F O U R F I V E T W O
  [
    { text: "FOUR", id: "FOUR", len: 4 },
    { text: "FIVE", id: "FIVE", len: 4 },
    { text: "TWO",  id: "TWO",  len: 3 },
  ],
  // Row 7: E I G H T E L E V E N
  [
    { text: "EIGHT",  id: "EIGHT",  len: 5 },
    { text: "ELEVEN", id: "ELEVEN", len: 6 },
  ],
  // Row 8: S E V E N T W E L V E
  [
    { text: "SEVEN",  id: "SEVEN",  len: 5 },
    { text: "TWELVE", id: "TWELVE", len: 6 },
  ],
  // Row 9: T E N S E O ' C L O C K
  [
    { text: "TEN",     id: "TEN",     len: 3 },
    { text: "SE",      id: null,      len: 2 },
    { text: "O'CLOCK", id: "OCLOCK",  len: 6 },
  ],
];

/**
 * Flattens the word grid into individual letter cells, each annotated with
 * its parent word identifier. Returns an array of 110 cells (10 rows x 11
 * columns) used by the WordCloud layout for rendering and highlighting.
 */
export function getGridCells(): GridCell[] {
  const cells: GridCell[] = [];
  for (let row = 0; row < GRID_ROWS.length; row++) {
    let col = 0;
    for (const word of GRID_ROWS[row]) {
      for (let i = 0; i < word.text.length; i++) {
        // The apostrophe in O'CLOCK counts as a character in the text but
        // does not occupy its own grid column. We skip it during layout.
        if (word.text[i] === "'") continue;
        cells.push({
          letter: word.text[i],
          wordId: word.id,
          row,
          col,
        });
        col++;
      }
    }
  }
  return cells;
}

// Mapping from hour number (0-11) to the word ID used in the grid.
const HOUR_GRID_MAP: Record<number, string> = {
  0: "TWELVE",
  1: "ONE",
  2: "TWO",
  3: "THREE",
  4: "FOUR",
  5: "FIVE",
  6: "SIX",
  7: "SEVEN",
  8: "EIGHT",
  9: "NINE",
  10: "TEN",
  11: "ELEVEN",
};

/**
 * Determines which word IDs in the word grid should be highlighted for
 * the given time. Always includes "IT" and "IS", then adds the appropriate
 * minute phrase words and hour word.
 */
export function getWordCloudHighlights(timezone: string): Set<string> {
  const now = new Date();
  const fmt = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone || undefined,
    hour: "numeric",
    minute: "numeric",
    hour12: false,
  });
  const parts = fmt.formatToParts(now);
  let h = 0;
  let m = 0;
  for (const p of parts) {
    if (p.type === "hour") h = parseInt(p.value, 10);
    if (p.type === "minute") m = parseInt(p.value, 10);
  }
  if (h === 24) h = 0;

  const hour12 = h % 12;
  const nextHour = (hour12 + 1) % 12;

  // Round to the nearest 5 minutes for grid display.
  const rounded = Math.round(m / 5) * 5;
  const active = new Set<string>(["IT", "IS"]);

  // Minutes that round up to the next hour take that hour's half of the day,
  // so 11:58 is twelve o'clock PM.
  const isPM = (rounded === 60 ? (h + 1) % 24 : h) >= 12;

  // Add AM/PM indicator.
  active.add(isPM ? "PM" : "AM");

  if (rounded === 0 || rounded === 60) {
    // On the hour.
    const effectiveHour = rounded === 60 ? nextHour : hour12;
    active.add(HOUR_GRID_MAP[effectiveHour]);
    active.add("OCLOCK");
  } else if (rounded === 5) {
    active.add("MFIVE");
    active.add("PAST");
    active.add(HOUR_GRID_MAP[hour12]);
  } else if (rounded === 10) {
    active.add("MTEN");
    active.add("PAST");
    active.add(HOUR_GRID_MAP[hour12]);
  } else if (rounded === 15) {
    active.add("QUARTER");
    active.add("PAST");
    active.add(HOUR_GRID_MAP[hour12]);
  } else if (rounded === 20) {
    active.add("TWENTY");
    active.add("PAST");
    active.add(HOUR_GRID_MAP[hour12]);
  } else if (rounded === 25) {
    active.add("TWENTY");
    active.add("MFIVE");
    active.add("PAST");
    active.add(HOUR_GRID_MAP[hour12]);
  } else if (rounded === 30) {
    active.add("HALF");
    active.add("PAST");
    active.add(HOUR_GRID_MAP[hour12]);
  } else if (rounded === 35) {
    active.add("TWENTY");
    active.add("MFIVE");
    active.add("TO");
    active.add(HOUR_GRID_MAP[nextHour]);
  } else if (rounded === 40) {
    active.add("TWENTY");
    active.add("TO");
    active.add(HOUR_GRID_MAP[nextHour]);
  } else if (rounded === 45) {
    active.add("QUARTER");
    active.add("TO");
    active.add(HOUR_GRID_MAP[nextHour]);
  } else if (rounded === 50) {
    active.add("MTEN");
    active.add("TO");
    active.add(HOUR_GRID_MAP[nextHour]);
  } else if (rounded === 55) {
    active.add("MFIVE");
    active.add("TO");
    active.add(HOUR_GRID_MAP[nextHour]);
  }

  return active;
}

/**
 * Returns a broad time-of-day label and associated icon name based on the
 * hour. Used by the Approximate layout for a loose, artistic time display.
 */
export function getApproximateTime(timezone: string): ApproximateTimeResult {
  const now = new Date();
  const fmt = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone || undefined,
    hour: "numeric",
    hour12: false,
  });
  const parts = fmt.formatToParts(now);
  let h = 0;
  for (const p of parts) {
    if (p.type === "hour") h = parseInt(p.value, 10);
  }
  if (h === 24) h = 0;

  if (h >= 5 && h < 8) return { label: "Early Morning", icon: "sunrise" };
  if (h >= 8 && h < 12) return { label: "Morning", icon: "coffee" };
  if (h >= 12 && h < 14) return { label: "Midday", icon: "sun" };
  if (h >= 14 && h < 17) return { label: "Afternoon", icon: "sun" };
  if (h >= 17 && h < 20) return { label: "Evening", icon: "sunset" };
  if (h >= 20 && h < 23) return { label: "Night", icon: "moon" };
  return { label: "Midnight", icon: "stars" };
}
