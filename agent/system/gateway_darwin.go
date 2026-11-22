//go:build darwin

package system

import (
	osexec "os/exec"
	"strings"
)

func defaultGateway() map[string]any {
	out, err := osexec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return nil
	}
	result := map[string]any{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch key {
		case "gateway":
			result["address"] = strings.TrimSpace(value)
		case "interface":
			result["interface"] = strings.TrimSpace(value)
		}
	}
	if result["address"] == nil {
		return nil
	}
	return result
}
