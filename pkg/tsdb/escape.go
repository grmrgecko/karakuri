package tsdb

import (
	"regexp"
	"strings"
)

// EscapeValue escapes a variable value for the string literals of a query
// language, without adding quotes: the author writes the quotes, and a value
// cannot close them. PromQL and Flux strings are double-quoted with
// backslash escapes; InfluxQL strings are single-quoted with backslash
// escapes; SQL strings are single-quoted with doubled quotes.
func EscapeValue(lang, value string) string {
	switch lang {
	case LangPromQL:
		return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
	case LangFlux:
		// Flux also interpolates ${...} inside strings.
		return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "${", `\${`).Replace(value)
	case LangInfluxQL:
		return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value)
	case LangSQL:
		return strings.ReplaceAll(value, `'`, `''`)
	}
	return value
}

// RegexEscape escapes regular-expression metacharacters, for ${name:regex},
// and then escapes the result for where the language writes a regex: inside
// a string literal for PromQL and SQL, and inside a /.../ literal for
// InfluxQL and Flux. Without the second step a quote or slash in a value
// would end the literal, and PromQL would refuse the backslashes.
func RegexEscape(lang, value string) string {
	quoted := regexp.QuoteMeta(value)
	switch lang {
	case LangInfluxQL, LangFlux:
		return strings.ReplaceAll(quoted, "/", `\/`)
	}
	return EscapeValue(lang, quoted)
}

// SQLString quotes a value as a SQL string literal, for ${name:sqlstring}.
func SQLString(value string) string {
	return "'" + strings.ReplaceAll(value, `'`, `''`) + "'"
}

var (
	invalidMetric = regexp.MustCompile(`[^a-zA-Z0-9_:]`)
	invalidLabel  = regexp.MustCompile(`[^a-zA-Z0-9_]`)
)

// SanitizeMetric makes a name a valid Prometheus metric name by replacing
// characters outside [a-zA-Z0-9_:] with underscores and prefixing one when
// the name starts with a digit.
func SanitizeMetric(name string) string {
	name = invalidMetric.ReplaceAllString(name, "_")
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "_" + name
	}
	return name
}

// SanitizeLabel makes a name a valid Prometheus label name.
func SanitizeLabel(name string) string {
	name = invalidLabel.ReplaceAllString(name, "_")
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "_" + name
	}
	return name
}

var legendRef = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`)

// ApplyLegend fills a legend format such as "{{instance}}" from labels.
// Labels the series lacks become empty.
func ApplyLegend(format string, labels map[string]string) string {
	return legendRef.ReplaceAllStringFunc(format, func(m string) string {
		return labels[legendRef.FindStringSubmatch(m)[1]]
	})
}
