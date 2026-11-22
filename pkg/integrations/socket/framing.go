package socket

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Framing defines the boundary of one TCP message or request response.
type Framing struct {
	Mode             string
	Delimiter        string
	IncludeDelimiter bool
	Length           int
	MaxBytes         int
	Idle             time.Duration
}

// Validate checks bounds before a reader or request starts.
func (f Framing) Validate(request bool) error {
	if f.MaxBytes < 1 || f.MaxBytes > MaxBytes {
		return fmt.Errorf("frame limit must be between 1 and %d", MaxBytes)
	}
	switch f.Mode {
	case "chunks":
		if request {
			return fmt.Errorf("requests require a response boundary")
		}
	case "delimiter":
		_, e := Delimiter(f.Delimiter)
		return e
	case "fixed":
		if f.Length < 1 || f.Length > f.MaxBytes {
			return fmt.Errorf("frame length must fit the byte limit")
		}
	case "close":
	case "idle":
		if !request || f.Idle <= 0 || f.Idle > 120*time.Second {
			return fmt.Errorf("idle framing requires a positive request idle gap of at most 120s")
		}
	default:
		return fmt.Errorf("invalid tcp framing mode")
	}
	return nil
}

func readFrame(r *bufio.Reader, f Framing) ([]byte, error) {
	switch f.Mode {
	case "chunks":
		b := make([]byte, f.MaxBytes)
		n, e := r.Read(b)
		// Queued frames are accounted by length, so do not retain the full buffer.
		return bytes.Clone(b[:n]), e
	case "fixed":
		b := make([]byte, f.Length)
		_, e := io.ReadFull(r, b)
		return b, e
	case "close":
		b, e := io.ReadAll(io.LimitReader(r, int64(f.MaxBytes)+1))
		if len(b) > f.MaxBytes {
			return nil, errFrameLimit
		}
		return b, e
	case "delimiter":
		delimiter, e := Delimiter(f.Delimiter)
		if e != nil {
			return nil, e
		}
		// ReadByte preserves bytes after the boundary and handles delimiters split
		// across transport reads without retaining an unbounded partial frame.
		b := make([]byte, 0, 256)
		for {
			v, e := r.ReadByte()
			if e != nil {
				if e == io.EOF && len(b) > 0 {
					return nil, io.ErrUnexpectedEOF
				}
				return nil, e
			}
			b = append(b, v)
			if len(b) > f.MaxBytes {
				return nil, errFrameLimit
			}
			if bytes.HasSuffix(b, delimiter) {
				if !f.IncludeDelimiter {
					b = b[:len(b)-len(delimiter)]
				}
				return b, nil
			}
		}
	}
	return nil, fmt.Errorf("invalid stream framing")
}

func decode(b []byte, format string) (any, error) {
	switch format {
	case "", "text":
		return string(b), nil
	case "binary":
		return bytes.Clone(b), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(b), nil
	case "json":
		var v any
		e := json.Unmarshal(b, &v)
		return v, e
	default:
		return nil, fmt.Errorf("invalid payload format")
	}
}

// Encode converts a payload to wire bytes without coercing incompatible values.
func Encode(v any, format string) ([]byte, bool, error) {
	switch format {
	case "", "text":
		s, ok := v.(string)
		if !ok {
			return nil, false, fmt.Errorf("text output requires a string payload")
		}
		return []byte(s), false, nil
	case "binary":
		b, ok := v.([]byte)
		if !ok {
			return nil, true, fmt.Errorf("binary output requires a byte payload")
		}
		return b, true, nil
	case "base64":
		s, ok := v.(string)
		if !ok {
			return nil, true, fmt.Errorf("base64 output requires a string payload")
		}
		b, e := base64.StdEncoding.DecodeString(s)
		return b, true, e
	case "json":
		b, e := json.Marshal(v)
		return b, false, e
	default:
		return nil, false, fmt.Errorf("invalid payload format")
	}
}
