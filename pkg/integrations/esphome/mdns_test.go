package esphome

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

// answerPacket builds the response an ESPHome node sends to a browse: PTR,
// SRV, TXT, and A records for one instance.
func answerPacket(t *testing.T, name, host string, ip [4]byte, port uint16, txt []string) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	require.NoError(t, b.StartAnswers())
	svc := dnsmessage.MustNewName(serviceName)
	inst := dnsmessage.MustNewName(name + "." + serviceName)
	target := dnsmessage.MustNewName(host)
	hdr := func(n dnsmessage.Name, typ dnsmessage.Type) dnsmessage.ResourceHeader {
		return dnsmessage.ResourceHeader{Name: n, Type: typ, Class: dnsmessage.ClassINET, TTL: 120}
	}
	require.NoError(t, b.PTRResource(hdr(svc, dnsmessage.TypePTR), dnsmessage.PTRResource{PTR: inst}))
	require.NoError(t, b.SRVResource(hdr(inst, dnsmessage.TypeSRV), dnsmessage.SRVResource{Port: port, Target: target}))
	require.NoError(t, b.TXTResource(hdr(inst, dnsmessage.TypeTXT), dnsmessage.TXTResource{TXT: txt}))
	require.NoError(t, b.AResource(hdr(target, dnsmessage.TypeA), dnsmessage.AResource{A: ip}))
	packet, err := b.Finish()
	require.NoError(t, err)
	return packet
}

// TestParseAnswer covers reading a node's announcement: name, address from
// the A record, port, and the TXT fields, and falling back to the sender's
// address when the response carries no A record.
func TestParseAnswer(t *testing.T) {
	found := map[string]*FoundNode{}
	parseAnswer(answerPacket(t, "garage-controller", "garage-controller.local.", [4]byte{10, 0, 20, 14}, 6053,
		[]string{"mac=246f28aabbcc", "version=2026.10.1", "board=esp32dev", "friendly_name=Garage"}), nil, found)
	require.Contains(t, found, "garage-controller")
	assert.Equal(t, FoundNode{Name: "garage-controller", Host: "10.0.20.14", Port: 6053, MAC: "246f28aabbcc",
		Version: "2026.10.1", Board: "esp32dev", Friendly: "Garage"}, *found["garage-controller"])

	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	require.NoError(t, b.StartAnswers())
	inst := dnsmessage.MustNewName("porch." + serviceName)
	require.NoError(t, b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Type: dnsmessage.TypeSRV, Class: dnsmessage.ClassINET},
		dnsmessage.SRVResource{Port: 6054, Target: dnsmessage.MustNewName("porch.local.")}))
	packet, err := b.Finish()
	require.NoError(t, err)
	found = map[string]*FoundNode{}
	parseAnswer(packet, &net.UDPAddr{IP: net.IPv4(10, 0, 20, 15)}, found)
	assert.Equal(t, "10.0.20.15", found["porch"].Host)
	assert.Equal(t, 6054, found["porch"].Port)
}
