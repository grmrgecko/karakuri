//go:build linux

package system

import (
	"encoding/hex"
	"os"
	"strings"
)

func defaultGateway() map[string]any {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "00000000" || fields[3] == "0000" {
			continue
		}
		raw, err := hex.DecodeString(fields[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		return map[string]any{"address": strings.Join([]string{byteDecimal(raw[3]), byteDecimal(raw[2]), byteDecimal(raw[1]), byteDecimal(raw[0])}, "."), "interface": fields[0]}
	}
	return nil
}

func byteDecimal(value byte) string {
	const digits = "0123456789"
	if value >= 100 {
		return string([]byte{digits[value/100], digits[(value/10)%10], digits[value%10]})
	}
	if value >= 10 {
		return string([]byte{digits[value/10], digits[value%10]})
	}
	return string(digits[value])
}
