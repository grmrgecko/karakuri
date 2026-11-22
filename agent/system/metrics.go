package system

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Nothing here samples on its own. Every metrics node is an output-only pure
// source, so it runs when a flow pulls it and not otherwise: an agent sitting
// on a NAS with no flow asking about it does no measuring at all, and a run
// that reads the same node from three places still measures once, because the
// engine memoizes a pulled value per run.
//
// CPU utilization is the one reading that cannot come from a single sample,
// since the kernel counts cumulative time rather than a rate. Instead of a
// background sampler the agent keeps the last counters it read and takes the
// delta against them; only a first read, or one whose predecessor is too old
// or too recent to divide by, pays for a short in-line sample.

// CPU sampling bounds. Below the floor the interval is too short to divide by
// and the answer is noise; past the ceiling the delta is an average over
// minutes, which is not what a flow asking for CPU percent means.
const (
	cpuSampleWindow = 250 * time.Millisecond
	cpuSampleFloor  = 100 * time.Millisecond
	cpuSampleCeil   = 90 * time.Second
)

// Node type names this group contributes. They are the names an operator
// writes in the disable list and the names the host namespaces as
// agent.<id>.<name>, so they are constants rather than literals in the metadata.
const (
	nodeHostInfo      = "host-info"
	nodeSystemMetrics = "system-metrics"
	nodeDiskUsage     = "disk-usage"
	nodeProcessList   = "process-list"
	nodeProcessSignal = "process-signal"
	nodeProcessWatch  = "process-watch"
	nodeBattery       = "battery"
)

// CPUSampler turns the kernel's cumulative CPU counters into a percentage
// without anything running between reads. The mutex serializes concurrent
// pulls so two runs cannot both divide by the same interval.
type CPUSampler struct {
	mu    sync.Mutex
	last  cpu.TimesStat
	at    time.Time
	valid bool
}

// percent returns host CPU utilization since the previous reading, taking a
// short sample of its own when there is no usable previous one.
func (s *CPUSampler) percent(ctx context.Context, clock nodeapi.Clock) (float64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now, err := cpuTotals(ctx)
	if err != nil {
		return 0, "", err
	}
	at := clock.Now()
	gap := at.Sub(s.at)

	if s.valid && gap >= cpuSampleFloor && gap <= cpuSampleCeil {
		used := cpuBusyPercent(s.last, now)
		s.last, s.at = now, at
		return used, "delta", nil
	}

	// No usable predecessor, so measure a window here rather than leave
	// something sampling in the background for the one run that asks.
	select {
	case <-ctx.Done():
		return 0, "", ctx.Err()
	case <-clock.After(cpuSampleWindow):
	}
	second, err := cpuTotals(ctx)
	if err != nil {
		return 0, "", err
	}
	s.last, s.at, s.valid = second, clock.Now(), true
	return cpuBusyPercent(now, second), "sample", nil
}

// cpuTotals reads the host's aggregate CPU counters.
func cpuTotals(ctx context.Context) (cpu.TimesStat, error) {
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return cpu.TimesStat{}, fmt.Errorf("reading cpu times: %w", err)
	}
	if len(times) == 0 {
		return cpu.TimesStat{}, fmt.Errorf("the kernel reported no cpu times")
	}
	return times[0], nil
}

// cpuBusyPercent is the share of the interval that was not idle. Idle and
// iowait both count as not busy: a disk-bound host is waiting, not working,
// and reporting it as load hides the actual cause.
func cpuBusyPercent(before, after cpu.TimesStat) float64 {
	total := cpuTotalSeconds(after) - cpuTotalSeconds(before)
	if total <= 0 {
		return 0
	}
	idle := (after.Idle + after.Iowait) - (before.Idle + before.Iowait)
	busy := (total - idle) / total * 100
	switch {
	case busy < 0:
		return 0
	case busy > 100:
		return 100
	}
	return busy
}

// cpuTotalSeconds sums one snapshot of the kernel's CPU counters. Guest and
// guest-nice are left out on purpose: Linux already counts guest time inside
// user and guest-nice inside nice, so adding them makes a busy virtualization
// host report over 100 percent.
func cpuTotalSeconds(t cpu.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
}

// hostInfoNode reports what this machine is. It is the cheapest of the
// metrics nodes and the one a flow fanning out across agents routes on.
type hostInfoNode struct {
	sess    *automation.Session
	initErr error
}

func (hostInfoNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeHostInfo,
		Folder:            folder,
		Label:             "Host info",
		Description:       "Reports this agent host's name, platform, uptime, and addresses",
		Icon:              "las la-server",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
	}
}

// hostFacts is the host identity the node reports, read natively on macOS and
// through the library everywhere else.
type hostFacts struct {
	Hostname        string
	OS              string
	Platform        string
	PlatformVersion string
	KernelVersion   string
	KernelArch      string
	Uptime          uint64
	BootTime        time.Time
}

// Execute reads the host's identity.
func (n hostInfoNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	info, err := hostIdentity(ctx)
	if err != nil {
		return nil, err
	}
	cores, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		cores = runtime.NumCPU()
	}
	addresses, interfaces := hostNetwork()
	payload := map[string]any{
		"hostname":         info.Hostname,
		"os":               info.OS,
		"platform":         info.Platform,
		"platform_version": info.PlatformVersion,
		"kernel":           info.KernelVersion,
		"arch":             info.KernelArch,
		"cpus":             cores,
		"uptime_seconds":   info.Uptime,
		"boot_time":        info.BootTime.UTC().Format(time.RFC3339),
		"addresses":        addresses,
		"interfaces":       interfaces,
		"agent_version":    version.Version,
	}
	if gateway := defaultGateway(); gateway != nil {
		payload["default_gateway"] = gateway
	}
	if n.sess != nil {
		env := n.sess.Describe()
		payload["automation"] = map[string]any{
			"available": true, "display": env.Display, "compositor": env.Compositor,
			"delegate": env.Delegate, "backends": env.Backends,
		}
	} else if n.initErr != nil {
		payload["automation"] = map[string]any{"available": false, "error": n.initErr.Error()}
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: payload,
		Topic:   info.Hostname,
		Meta:    map[string]any{"source": "host-info"},
	}), nil
}

// hostAddresses lists the host's routable addresses, skipping loopback and
// interfaces that are down. It is the answer to "where is this machine", which
// is why link-local addresses are left in: on a segment without DHCP they are
// the only address there is.
func hostNetwork() ([]any, []any) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, nil
	}
	var addresses []any
	var details []any
	for _, iface := range interfaces {
		item := map[string]any{"name": iface.Name, "index": iface.Index, "mtu": iface.MTU,
			"mac": iface.HardwareAddr.String(), "up": iface.Flags&net.FlagUp != 0,
			"loopback": iface.Flags&net.FlagLoopback != 0}
		addrs, err := iface.Addrs()
		if err != nil {
			details = append(details, item)
			continue
		}
		var ifaceAddresses []any
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			entry := map[string]any{"address": ipNet.IP.String(), "cidr": ipNet.String(), "scope": addressScope(ipNet.IP)}
			ifaceAddresses = append(ifaceAddresses, entry)
			if iface.Flags&net.FlagUp != 0 && !ipNet.IP.IsLoopback() {
				addresses = append(addresses, ipNet.IP.String())
			}
		}
		item["addresses"] = ifaceAddresses
		details = append(details, item)
	}
	return addresses, details
}

func addressScope(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return "link-local"
	case ip.IsPrivate():
		return "private"
	case ip.IsUnspecified():
		return "unspecified"
	case ip.IsMulticast():
		return "multicast"
	default:
		return "global"
	}
}

// systemMetricsNode reports load, memory, and CPU utilization.
type systemMetricsNode struct {
	sampler *CPUSampler
}

func (systemMetricsNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeSystemMetrics,
		Folder:            folder,
		Label:             "System metrics",
		Description:       "Reads CPU, memory, swap, and load on this agent host when a flow asks",
		Icon:              "las la-tachometer-alt",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
	}
}

// Execute takes one reading of the host's load.
func (n *systemMetricsNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	cpuPercent, how, err := n.sampler.percent(ctx, ec.Clock())
	if err != nil {
		return nil, err
	}
	cores, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		cores = runtime.NumCPU()
	}
	virtual, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading memory: %w", err)
	}
	uptime, err := host.UptimeWithContext(ctx)
	if err != nil {
		uptime = 0
	}

	payload := map[string]any{
		"cpu_percent":    round2(cpuPercent),
		"cpu_count":      cores,
		"uptime_seconds": uptime,
		"memory": map[string]any{
			"total":        virtual.Total,
			"available":    virtual.Available,
			"used":         virtual.Used,
			"used_percent": round2(virtual.UsedPercent),
		},
	}
	// Swap and load are reported when the platform has them. A host with swap
	// off answers zero, which is different from a platform that has no such
	// thing to report.
	if swap, err := mem.SwapMemoryWithContext(ctx); err == nil {
		payload["swap"] = map[string]any{
			"total":        swap.Total,
			"used":         swap.Used,
			"used_percent": round2(swap.UsedPercent),
		}
	}
	if avg, ok := loadAverage(ctx); ok {
		payload["load"] = map[string]any{
			"load1":  round2(avg[0]),
			"load5":  round2(avg[1]),
			"load15": round2(avg[2]),
		}
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: payload,
		Meta:    map[string]any{"source": "system-metrics", "cpu_reading": how},
	}), nil
}

// diskUsageNode reports how full a filesystem is.
type diskUsageNode struct{}

func (diskUsageNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type:              nodeDiskUsage,
		Folder:            folder,
		Label:             "Disk usage",
		Description:       "Reads how full one filesystem or every mounted filesystem is",
		Icon:              "las la-hdd",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "path",
				Label:       "Path",
				Type:        flow.FieldString,
				Placeholder: "/",
				Help: "A path on the filesystem to measure, which does not have to be its mount " +
					"point. Empty reports every mounted filesystem as an array.",
			},
		},
	}
}

// diskUsageConfig is the decoded disk configuration.
type diskUsageConfig struct {
	Path string `json:"path"`
}

// Execute measures one filesystem, or all of them when no path is named.
func (diskUsageNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg diskUsageConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	path, err := agentnode.Interpolate(ec, cfg.Path, nil)
	if err != nil {
		return nil, err
	}

	if path != "" {
		usage, err := disk.UsageWithContext(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		return nodeapi.One("out", &nodeapi.Msg{
			Payload: diskPayload(usage.Path, "", usage.Fstype, usage),
			Topic:   usage.Path,
			Meta:    map[string]any{"source": "disk-usage", "path": usage.Path},
		}), nil
	}

	// Physical partitions only: the pseudo-filesystems a Linux host mounts by
	// the dozen have no free space to report and would bury the real ones.
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("listing filesystems: %w", err)
	}
	filesystems := make([]any, 0, len(parts))
	for _, part := range parts {
		usage, err := disk.UsageWithContext(ctx, part.Mountpoint)
		if err != nil {
			// A mount the agent's user cannot stat is skipped rather than
			// failing the read, which would lose every other filesystem.
			ec.Logger().Debug("skipping unreadable filesystem")
			continue
		}
		filesystems = append(filesystems, diskPayload(part.Mountpoint, part.Device, part.Fstype, usage))
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: filesystems,
		Meta:    map[string]any{"source": "disk-usage", "count": len(filesystems)},
	}), nil
}

// diskPayload shapes one filesystem's reading.
func diskPayload(mount, device, fstype string, usage *disk.UsageStat) map[string]any {
	out := map[string]any{
		"path":         mount,
		"fstype":       fstype,
		"total":        usage.Total,
		"used":         usage.Used,
		"free":         usage.Free,
		"used_percent": round2(usage.UsedPercent),
	}
	// Measuring one path asks the kernel about the filesystem rather than the
	// mount table, and that answer carries no device name. An empty string
	// would read as a filesystem with no backing device.
	if device != "" {
		out["device"] = device
	}
	return out
}

// toFloat reads a number back out of a payload map for sorting.
func toFloat(value any) float64 {
	if f, ok := value.(float64); ok {
		return f
	}
	return 0
}

// round2 trims a reading to two decimals, which is more precision than any of
// these measurements actually carry and enough to compare against a threshold.
func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}
