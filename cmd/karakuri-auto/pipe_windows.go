//go:build windows

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

type residentPipeFile interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	Fd() uintptr
}
type residentPipeConn struct {
	residentPipeFile
	path string
}
type residentPipeAddress string

func (a residentPipeAddress) Network() string    { return "pipe" }
func (a residentPipeAddress) String() string     { return string(a) }
func (c *residentPipeConn) LocalAddr() net.Addr  { return residentPipeAddress(c.path) }
func (c *residentPipeConn) RemoteAddr() net.Addr { return residentPipeAddress(c.path) }
func (c *residentPipeConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

type residentPipeListener struct {
	path        string
	cancel      context.CancelFunc
	connections chan net.Conn
	done        chan struct{}
	err         error
}

func (l *residentPipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		if l.err != nil {
			return nil, l.err
		}
		return nil, net.ErrClosed
	}
}
func (l *residentPipeListener) Close() error { l.cancel(); <-l.done; return nil }

// listenResidentPipe keeps a real pending instance as the name reservation.
// Windows 7 lets clients connect to go-winio's access-less reservation handle,
// where no Accept call can receive them.
func listenResidentPipe(path, acl string) (*residentPipeListener, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var sd *windows.SECURITY_DESCRIPTOR
	if acl != "" {
		sd, err = windows.SecurityDescriptorFromString(acl)
		if err != nil {
			return nil, err
		}
	}
	create := func(first bool) (windows.Handle, error) {
		flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
		if first {
			flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
		}
		sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
		h, err := windows.CreateNamedPipe(name, flags, windows.PIPE_REJECT_REMOTE_CLIENTS, 255, 65536, 65536, 0, &sa)
		runtime.KeepAlive(sd)
		return h, err
	}
	first, err := create(true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &residentPipeListener{path: path, cancel: cancel, connections: make(chan net.Conn), done: make(chan struct{})}
	go func() {
		defer close(l.done)
		pending := first
		defer func() {
			if pending != windows.InvalidHandle {
				_ = windows.CloseHandle(pending)
			}
		}()
		for ctx.Err() == nil {
			if err := connectResidentPipe(ctx, pending); err != nil {
				if errors.Is(err, windows.ERROR_NO_DATA) {
					_ = windows.DisconnectNamedPipe(pending)
					continue
				}
				if ctx.Err() == nil {
					l.err = err
				}
				return
			}
			next, err := create(false)
			if err != nil {
				l.err = err
				return
			}
			connected := pending
			pending = next
			file, err := winio.NewOpenFile(connected)
			if err != nil {
				_ = windows.CloseHandle(connected)
				l.err = err
				return
			}
			connection := &residentPipeConn{residentPipeFile: file.(residentPipeFile), path: path}
			select {
			case l.connections <- connection:
			case <-ctx.Done():
				_ = connection.Close()
				return
			}
		}
	}()
	return l, nil
}

func connectResidentPipe(ctx context.Context, handle windows.Handle) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	operation := windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(handle, &operation)
	if err == nil || errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return err
	}
	var transferred uint32
	for {
		status, err := windows.WaitForSingleObject(event, 100)
		if err != nil || ctx.Err() != nil {
			_ = windows.CancelIoEx(handle, &operation)
			_ = windows.GetOverlappedResult(handle, &operation, &transferred, true)
			if err != nil {
				return err
			}
			return ctx.Err()
		}
		if status == windows.WAIT_OBJECT_0 {
			return windows.GetOverlappedResult(handle, &operation, &transferred, false)
		}
	}
}
