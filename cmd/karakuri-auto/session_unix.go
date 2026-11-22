//go:build !windows

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/grmrgecko/karakuri/automation"
)

const residentBackendEnv = "KARAKURI_AUTO_BACKEND_SOCKET"

func commandSession(ctx context.Context, opts automation.Options) (*automation.Session, error) {
	if path := os.Getenv(residentBackendEnv); path != "" {
		var dial net.Dialer
		conn, err := dial.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, err
		}
		return automation.NewRemoteSession(ctx, conn)
	}
	return automation.New(ctx, opts)
}

// startResidentBackend runs under the resident's directory lock. Command
// subprocesses keep their own cwd and output while sharing desktop connections.
func startResidentBackend(ctx context.Context, directory string) (string, func(), error) {
	path := filepath.Join(directory, "backend.sock")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return "", nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	var running sync.WaitGroup
	var session *automation.Session
	var server *automation.SessionServer
	var once sync.Once
	clients := make(chan struct{}, 16)
	running.Add(1)
	go func() {
		defer running.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case clients <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			running.Add(1)
			go func() {
				defer running.Done()
				defer func() { <-clients }()
				defer conn.Close()
				once.Do(func() {
					created, createErr := automation.New(ctx, automationOptions())
					session = created
					if createErr == nil {
						server = automation.NewSessionServer(session)
					}
				})
				if server != nil {
					_ = server.Serve(ctx, conn)
				}
			}()
		}
	}()
	return path, func() {
		cancel()
		_ = listener.Close()
		running.Wait()
		if session != nil {
			_ = session.Close()
		}
	}, nil
}
