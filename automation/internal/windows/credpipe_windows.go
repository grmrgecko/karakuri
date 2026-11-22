//go:build windows

package windows

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
	"unicode/utf16"
)

// The credential provider hosts a pipe per session —
// \\.\pipe\karakuri-unlock-s<id> — and the agent is its only client. The
// frames are little-endian u32s with UTF-16 strings length-prefixed in
// bytes, which is what keeps password characters free of escaping: the
// provider's parser is a handful of reads, not a JSON dialect.
//
// One connection arms one credential: the client sends the request, and the
// provider answers once winlogon has reported trying it — accepted, refused,
// or timed out without being submitted. The answer's status codes are the
// provider's own; the NTSTATUS rides along for the message.

const (
	cpFrameMagic   = 0x4b554e4c // "KUNL"
	cpFrameVersion = 1

	cpResultOK      = 0
	cpResultRefused = 1
	cpResultTimeout = 2
)

// armCredentialProvider pushes one credential at the provider hosted in the
// named session and waits for the outcome. The dial retries briefly, since
// LogonUI may still be coming up on a screen that just locked, and a
// connection that dies before the answer (a LogonUI restart) re-arms: a
// fresh credential provider process starts with an empty queue, so the
// credential has to arrive again for its first tile enumeration to submit
// it. The answer's status codes are the provider's own; the NTSTATUS rides
// along for the message.
func armCredentialProvider(sessionID uint32, username, secret string, timeout time.Duration) (err error) {
	frame := cpArmFrame(username, secret, timeout)
	deadline := time.Now().Add(timeout + 4*time.Second)
	for {
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("the credential was not submitted within %s", timeout)
			}
			return err
		}
		err = armOnce(sessionID, frame, timeout, deadline)
		if err == nil {
			return nil
		}
		var opErr *net.OpError
		var pathErr *os.PathError
		broken := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
			errors.As(err, &opErr) || errors.As(err, &pathErr)
		if !broken {
			return err
		}
		// A dead connection is a LogonUI restart; re-arm on the next one.
		time.Sleep(250 * time.Millisecond)
	}
}

// armOnce connects, hands over one credential, and waits for the outcome.
func armOnce(sessionID uint32, frame []byte, timeout time.Duration, deadline time.Time) (err error) {
	var pipe *os.File
	for {
		// A byte-mode pipe answers OpenFile like a file; the provider
		// created it with PIPE_TYPE_BYTE and one instance, so there is
		// nothing to negotiate.
		pipe, err = os.OpenFile(fmt.Sprintf(`\\.\pipe\karakuri-unlock-s%d`, sessionID), os.O_RDWR, 0)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the karakuri credential provider is not answering in session %d: %w", sessionID, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	defer pipe.Close()

	pipe.SetDeadline(time.Now().Add(timeout + 15*time.Second))
	if _, err = pipe.Write(frame); err != nil {
		return fmt.Errorf("handing the credential to the provider: %w", err)
	}
	var out [12]byte
	if _, err = readFully(pipe, out[:]); err != nil {
		return fmt.Errorf("reading the provider's answer: %w", err)
	}
	if binary.LittleEndian.Uint32(out[0:4]) != cpFrameMagic {
		return fmt.Errorf("the provider's answer is not a karakuri frame")
	}
	switch binary.LittleEndian.Uint32(out[4:8]) {
	case cpResultOK:
		return nil
	case cpResultTimeout:
		return fmt.Errorf("the credential was not submitted within %s", timeout)
	case cpResultRefused:
		return fmt.Errorf("windows refused the credential (%#x)", binary.LittleEndian.Uint32(out[8:12]))
	default:
		return fmt.Errorf("the provider returned an unknown result %d", binary.LittleEndian.Uint32(out[4:8]))
	}
}

// cpArmFrame encodes one arm request: the magic, the version, the timeout in
// seconds, and the two strings length-prefixed as UTF-16 bytes.
func cpArmFrame(username, secret string, timeout time.Duration) []byte {
	user := utf16.Encode([]rune(username))
	pass := utf16.Encode([]rune(secret))
	payloadLen := 4 + len(user)*2 + 4 + len(pass)*2
	frame := make([]byte, 16+payloadLen)
	binary.LittleEndian.PutUint32(frame[0:4], cpFrameMagic)
	binary.LittleEndian.PutUint32(frame[4:8], cpFrameVersion)
	binary.LittleEndian.PutUint32(frame[8:12], uint32(timeout/time.Second))
	binary.LittleEndian.PutUint32(frame[12:16], uint32(payloadLen))
	at := 16
	binary.LittleEndian.PutUint32(frame[at:], uint32(len(user)*2))
	at += 4
	for _, u := range user {
		binary.LittleEndian.PutUint16(frame[at:], u)
		at += 2
	}
	binary.LittleEndian.PutUint32(frame[at:], uint32(len(pass)*2))
	at += 4
	for _, p := range pass {
		binary.LittleEndian.PutUint16(frame[at:], p)
		at += 2
	}
	return frame
}

// readFully reads until the buffer is full, which is what the fixed-size
// answer frame needs from a stream pipe.
func readFully(pipe io.Reader, buf []byte) (int, error) {
	done := 0
	for done < len(buf) {
		n, err := pipe.Read(buf[done:])
		if err != nil {
			return done, err
		}
		done += n
	}
	return done, nil
}
