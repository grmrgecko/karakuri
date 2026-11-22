package zigbee2mqtt

import (
	"encoding/json"
	"strconv"
	"strings"
)

// xyToHexHelper converts CIE xy, which Zigbee lights report colour in, to
// the hex a light's colour key holds. Brightness is left to the brightness
// key, so the colour is drawn at full intensity.
const xyToHexHelper = `function xyToHex(x, y) {
  const z = 1 - x - y;
  const Y = 1, X = (Y / y) * x, Z = (Y / y) * z;
  let r = X * 1.656492 - Y * 0.354851 - Z * 0.255038;
  let g = -X * 0.707196 + Y * 1.655397 + Z * 0.036152;
  let b = X * 0.051713 - Y * 0.121364 + Z * 1.01153;
  const m = Math.max(r, g, b, 1e-9);
  const gamma = (c) => (c <= 0.0031308 ? 12.92 * c : 1.055 * Math.pow(c, 1 / 2.4) - 0.055);
  const hex = (c) => Math.max(0, Math.min(255, Math.round(gamma(Math.max(0, c / m)) * 255))).toString(16).padStart(2, "0");
  return "#" + hex(r) + hex(g) + hex(b);
}
`

// StateScript writes the state script for a proposal: it reads the kind's
// keys from the device's properties and keeps every other property under its
// own name.
func StateScript(p Proposal) string {
	var b strings.Builder
	b.WriteString("// Generated from the device's Zigbee2MQTT exposes. Edit freely; Regenerate\n")
	b.WriteString("// on the device rewrites it.\n")
	if p.colorXY {
		b.WriteString(xyToHexHelper)
	}
	b.WriteString("let d;\n")
	b.WriteString("try { d = JSON.parse(payload); } catch (e) { return null; }\n")
	b.WriteString("if (!d || typeof d !== \"object\" || Array.isArray(d)) return null;\n")
	b.WriteString("const out = {};\n")
	for _, k := range p.keys {
		if k.Read == "" {
			continue
		}
		b.WriteString("if (" + jsString(k.Prop) + " in d) {\n")
		b.WriteString("  const v = d[" + jsString(k.Prop) + "];\n")
		b.WriteString("  const r = " + k.Read + ";\n")
		b.WriteString("  if (r !== undefined) out." + k.Key + " = r;\n")
		b.WriteString("}\n")
	}
	b.WriteString("const mapped = " + jsArray(p.Mapped) + ";\n")
	b.WriteString("for (const k of Object.keys(d)) {\n")
	b.WriteString("  if (!mapped.includes(k)) out[k] = d[k];\n")
	b.WriteString("}\n")
	b.WriteString("return out;\n")
	return b.String()
}

// PayloadScript writes the payload script for a proposal, or "" for a device
// that takes no commands. It publishes to <base>/<ieee>/set, which
// Zigbee2MQTT accepts in place of the friendly name, so renaming the device
// in Zigbee2MQTT never breaks the script.
func PayloadScript(p Proposal, base, ieee string) string {
	if p.ReadOnly {
		return ""
	}
	var b strings.Builder
	b.WriteString("// Generated from the device's Zigbee2MQTT exposes. Edit freely; Regenerate\n")
	b.WriteString("// on the device rewrites it.\n")
	b.WriteString("const setTopic = " + jsString(base+"/"+ieee+"/set") + ";\n")
	b.WriteString("switch (field) {\n")
	for _, k := range p.keys {
		if k.Write == "" {
			continue
		}
		// A write that maps the value to nothing, such as a cover told a state
		// it only reports, is the null action rather than an empty publish.
		b.WriteString("  case " + jsString(k.Key) + ": {\n")
		b.WriteString("    const out = " + k.Write + ";\n")
		b.WriteString("    return out === undefined ? {} : { topic: setTopic, payload: { " + jsString(k.Prop) + ": out } };\n")
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	b.WriteString("return {};\n")
	return b.String()
}

// jsString renders a Go string as a JavaScript string literal.
func jsString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// jsArray renders a list of strings as a JavaScript array literal.
func jsArray(list []string) string {
	if list == nil {
		list = []string{}
	}
	raw, _ := json.Marshal(list)
	return string(raw)
}

// jsLiteral renders an expose's on or off value, which Zigbee2MQTT gives as
// a string or a boolean, falling back to def when the expose has none.
func jsLiteral(v any, def any) string {
	if v == nil {
		v = def
	}
	raw, err := json.Marshal(v)
	if err != nil {
		raw, _ = json.Marshal(def)
	}
	return string(raw)
}

// num renders a number for a script.
func num(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
