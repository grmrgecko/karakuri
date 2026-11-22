// A script field may offer a Test button: the editor shows the sample the
// script reads as an editable JSON document, runs it, and reports what it
// printed and what it returned. The editor knows neither what a sample looks
// like nor how to run one — both belong to whoever owns the field, so both
// arrive as a value.

/** ScriptTestResult is what one run produced. */
export interface ScriptTestResult {
  value?: unknown;
  output?: string[];
  // error is the script failing, which is the thing being looked at rather than
  // a problem with the request — so it arrives as a result, not a rejection.
  error?: string;
}

/** ScriptTest is what a field hands the editor to make Test work. */
export interface ScriptTest {
  title?: string;
  hint?: string;
  /**
   * sample is the JSON document the script is run against, seeded with the
   * shape it reads. The author edits it in place, so it doubles as the
   * documentation of what arrives.
   */
  sample: string;
  run: (source: string, sample: unknown) => Promise<ScriptTestResult>;
}
