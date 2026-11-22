package esphome

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// serviceName is the mDNS service ESPHome nodes announce.
const serviceName = "_esphomelib._tcp.local."

// mdnsGroup is the IPv4 mDNS multicast address.
var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// FoundNode is a node announcing itself on the network.
type FoundNode struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	MAC     string `json:"mac,omitempty"`
	Version string `json:"version,omitempty"`
	Board   string `json:"board,omitempty"`
	// Friendly is the node's friendly name, when it sets one.
	Friendly string `json:"friendly_name,omitempty"`
}

// Browse lists the ESPHome nodes answering an mDNS query until ctx ends.
func Browse(ctx context.Context) ([]FoundNode, error) {
	return query(ctx, serviceName, "")
}

// Resolve finds one node by its name, such as garage-controller.
func Resolve(ctx context.Context, name string) (FoundNode, error) {
	nodes, err := query(ctx, name+"."+serviceName, name)
	if err != nil {
		return FoundNode{}, err
	}
	for _, n := range nodes {
		if n.Name == name {
			return n, nil
		}
	}
	return FoundNode{}, errors.New("node " + name + " did not answer")
}

// query sends one PTR query and collects answers until ctx ends or, when
// want names one node, that node answers.
func query(ctx context.Context, question, want string) ([]FoundNode, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetMulticastTTL(255)

	qtype := dnsmessage.TypePTR
	if want != "" {
		qtype = dnsmessage.TypeSRV
	}
	name, err := dnsmessage.NewName(question)
	if err != nil {
		return nil, err
	}
	msg := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: name, Type: qtype, Class: dnsmessage.ClassINET}}}
	packet, err := msg.Pack()
	if err != nil {
		return nil, err
	}
	if _, err := conn.WriteToUDP(packet, mdnsGroup); err != nil {
		return nil, err
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	_ = conn.SetReadDeadline(deadline)
	found := map[string]*FoundNode{}
	buf := make([]byte, 9000)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		parseAnswer(buf[:n], from, found)
		if want != "" {
			if node, ok := found[want]; ok && node.Host != "" {
				break
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	out := make([]FoundNode, 0, len(found))
	for _, node := range found {
		if node.Host != "" {
			out = append(out, *node)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// parseAnswer reads one mDNS response into the nodes it describes. A node's
// address comes from its A record, or from the packet's source when the
// response leaves it out.
func parseAnswer(packet []byte, from *net.UDPAddr, found map[string]*FoundNode) {
	var p dnsmessage.Parser
	if _, err := p.Start(packet); err != nil {
		return
	}
	if err := p.SkipAllQuestions(); err != nil {
		return
	}
	var records []dnsmessage.Resource
	for _, section := range []func() ([]dnsmessage.Resource, error){p.AllAnswers, p.AllAuthorities, p.AllAdditionals} {
		rs, err := section()
		if err != nil {
			break
		}
		records = append(records, rs...)
	}

	hosts := map[string]string{}
	for _, r := range records {
		if a, ok := r.Body.(*dnsmessage.AResource); ok {
			hosts[strings.ToLower(r.Header.Name.String())] = net.IP(a.A[:]).String()
		}
	}
	node := func(instance string) *FoundNode {
		name := strings.TrimSuffix(instance, "."+serviceName)
		if name == instance {
			return nil
		}
		if found[name] == nil {
			found[name] = &FoundNode{Name: name, Port: DefaultPort}
		}
		return found[name]
	}
	for _, r := range records {
		switch body := r.Body.(type) {
		case *dnsmessage.PTRResource:
			node(body.PTR.String())
		case *dnsmessage.SRVResource:
			n := node(r.Header.Name.String())
			if n == nil {
				continue
			}
			n.Port = int(body.Port)
			if ip, ok := hosts[strings.ToLower(body.Target.String())]; ok {
				n.Host = ip
			} else if from != nil {
				n.Host = from.IP.String()
			}
		case *dnsmessage.TXTResource:
			n := node(r.Header.Name.String())
			if n == nil {
				continue
			}
			for _, kv := range body.TXT {
				k, v, _ := strings.Cut(kv, "=")
				switch k {
				case "mac":
					n.MAC = v
				case "version":
					n.Version = v
				case "board":
					n.Board = v
				case "friendly_name":
					n.Friendly = v
				}
			}
		}
	}
}
