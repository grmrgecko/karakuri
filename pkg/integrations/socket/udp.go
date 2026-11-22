package socket

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"net/netip"
)

func (d *Driver) startUDP() error {
	address, e := net.ResolveUDPAddr(d.cfg.Family, d.cfg.Address)
	if e != nil {
		return e
	}
	c, e := net.ListenUDP(d.cfg.Family, address)
	if e != nil {
		return fmt.Errorf("udp bind: %w", e)
	}
	d.mu.Lock()
	d.udp = c
	d.mu.Unlock()
	var iface *net.Interface
	if d.cfg.Interface != "" {
		iface, e = net.InterfaceByName(d.cfg.Interface)
		if e != nil {
			return e
		}
	}
	loopback := d.cfg.Loopback == nil || *d.cfg.Loopback
	if d.cfg.Family == "udp4" {
		p := ipv4.NewPacketConn(c)
		if e = p.SetMulticastTTL(d.cfg.TTL); e != nil {
			return e
		}
		if e = p.SetMulticastLoopback(loopback); e != nil {
			return e
		}
		if iface != nil {
			if e = p.SetMulticastInterface(iface); e != nil {
				return e
			}
		}
		if d.cfg.MulticastGroup != "" {
			if e = p.JoinGroup(iface, &net.UDPAddr{IP: net.ParseIP(d.cfg.MulticastGroup)}); e != nil {
				return e
			}
		}
		if e = setBroadcast(c, d.cfg.Broadcast); e != nil {
			return e
		}
	} else {
		p := ipv6.NewPacketConn(c)
		if e = p.SetMulticastHopLimit(d.cfg.TTL); e != nil {
			return e
		}
		if e = p.SetMulticastLoopback(loopback); e != nil {
			return e
		}
		if iface != nil {
			if e = p.SetMulticastInterface(iface); e != nil {
				return e
			}
		}
		if d.cfg.MulticastGroup != "" {
			if e = p.JoinGroup(iface, &net.UDPAddr{IP: net.ParseIP(d.cfg.MulticastGroup)}); e != nil {
				return e
			}
		}
	}
	d.setStatus(integrations.StateConnected, nil)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		buf := make([]byte, d.cfg.MaxBytes+1)
		for {
			n, peer, e := c.ReadFromUDP(buf)
			if e != nil && datagramTooLarge(e) {
				n = len(buf)
			} else if e != nil {
				if d.ctx.Err() == nil {
					d.setStatus(integrations.StateError, e)
				}
				return
			}
			if n > d.cfg.MaxBytes {
				d.deps.Logger.Error("udp datagram exceeds configured byte limit")
				continue
			}
			b := append([]byte(nil), buf[:n]...)
			d.publish(Frame{Data: b, Remote: peer.String(), Local: c.LocalAddr().String(), Type: "binary"})
		}
	}()
	return nil
}

func (d *Driver) sendUDP(parent context.Context, target Target, b []byte) error {
	destination := target.Destination
	switch target.Mode {
	case "reply":
		if target.ConnectionID != d.id.String() || destination == "" {
			return fmt.Errorf("udp reply requires a peer from this connection")
		}
	case "", "peer":
		if destination == "" {
			destination = d.cfg.Destination
		}
	default:
		return fmt.Errorf("invalid udp target")
	}
	if destination == "" {
		return fmt.Errorf("udp destination is required")
	}
	ctx, cancel := context.WithTimeout(parent, d.cfg.writeTimeout)
	defer cancel()
	address, e := resolveUDP(ctx, d.cfg.Family, destination)
	if e != nil {
		return e
	}
	select {
	case d.udpGate <- struct{}{}:
		defer func() { <-d.udpGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	stopped := d.stopped
	d.mu.Unlock()
	if stopped || d.udp == nil {
		return fmt.Errorf("connection is stopped")
	}
	deadline, _ := ctx.Deadline()
	if e = d.udp.SetWriteDeadline(deadline); e != nil {
		return e
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = d.udp.SetWriteDeadline(time.Now()); close(done) })
	n, e := d.udp.WriteToUDP(b, address)
	if !stop() {
		<-done
	}
	if e != nil {
		return fmt.Errorf("udp write: %w", e)
	}
	if n != len(b) {
		return fmt.Errorf("incomplete udp write")
	}
	return nil
}

func resolveUDP(ctx context.Context, family, address string) (*net.UDPAddr, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 {
		return nil, fmt.Errorf("destination port must be between 1 and 65535")
	}
	if parsed, err := netip.ParseAddr(host); err == nil {
		if parsed.Is4() != (family == "udp4") {
			return nil, fmt.Errorf("destination address does not match the configured family")
		}
		return &net.UDPAddr{IP: net.IP(parsed.AsSlice()), Zone: parsed.Zone(), Port: p}, nil
	}

	network := "ip4"
	if family == "udp6" {
		network = "ip6"
	}
	ips, e := net.DefaultResolver.LookupIP(ctx, network, host)
	if e != nil {
		return nil, e
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("destination has no address in configured family")
	}
	return &net.UDPAddr{IP: ips[0], Port: p}, nil
}
