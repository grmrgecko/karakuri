package mqtt

import "strings"

// Match reports whether an MQTT topic filter matches a concrete topic.
// Filters use '/' separated levels where '+' matches exactly one level and
// '#' (only valid as the final level) matches the remaining levels including
// the parent level itself, per the MQTT specification. A filter starting
// with a wildcard never matches a topic starting with '$', such as a broker's
// $SYS tree.
func Match(filter, topic string) bool {
	if filter == topic {
		return true
	}
	if strings.HasPrefix(topic, "$") && (strings.HasPrefix(filter, "#") || strings.HasPrefix(filter, "+")) {
		return false
	}
	fl := strings.Split(filter, "/")
	tl := strings.Split(topic, "/")

	for i, f := range fl {
		if f == "#" {
			// '#' must be the last filter level; it matches the parent level
			// and everything below it.
			return i == len(fl)-1
		}
		if i >= len(tl) {
			return false
		}
		if f != "+" && f != tl[i] {
			return false
		}
	}
	return len(fl) == len(tl)
}
