//go:build windows

package system

import (
	"encoding/json"
	osexec "os/exec"
)

func defaultGateway() map[string]any {
	script := `Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric | Select-Object -First 1 NextHop,InterfaceAlias | ConvertTo-Json -Compress`
	out, err := osexec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil
	}
	var route struct {
		NextHop        string `json:"NextHop"`
		InterfaceAlias string `json:"InterfaceAlias"`
	}
	if json.Unmarshal(out, &route) != nil || route.NextHop == "" {
		return nil
	}
	return map[string]any{"address": route.NextHop, "interface": route.InterfaceAlias}
}
