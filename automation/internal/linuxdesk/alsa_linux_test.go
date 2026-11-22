package linuxdesk

import (
	"strconv"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestALSAControlABI pins the hand-laid structs against the kernel's own
// declarations: the sizes and ioctl numbers a C probe over sound/asound.h
// prints (gcc, Arch kernel headers, 2026-08-28: id=64 info=272 value=1224,
// INFO=0xc1105511 READ=0xc4c85512 WRITE=0xc4c85513). C long is Go int on
// every Linux ABI, so the 64-bit numbers hold on amd64 and arm64 while the
// value union shrinks with the word size on 32-bit ARM — which is why the
// ioctl numbers are computed from the struct sizes rather than spelled out.
func TestALSAControlABI(t *testing.T) {
	require.EqualValues(t, 64, unsafe.Sizeof(alsaElemID{}),
		"snd_ctl_elem_id is 64 bytes on every Linux ABI")
	if strconv.IntSize != 64 {
		t.Skip("the C-probe numbers below are the LP64 layout")
	}
	assert.EqualValues(t, 272, unsafe.Sizeof(alsaElemInfo{}),
		"snd_ctl_elem_info must match the kernel's 272-byte layout")
	assert.EqualValues(t, 1224, unsafe.Sizeof(alsaElemValue{}),
		"snd_ctl_elem_value must match the kernel's 1224-byte layout")
	assert.EqualValues(t, 0xc1105511, alsaElemInfoIoctl,
		"ELEM_INFO must encode the size the kernel checks")
	assert.EqualValues(t, 0xc4c85512, alsaElemReadIoctl,
		"ELEM_READ must encode the size the kernel checks")
	assert.EqualValues(t, 0xc4c85513, alsaElemWriteIoctl,
		"ELEM_WRITE must encode the size the kernel checks")
}
