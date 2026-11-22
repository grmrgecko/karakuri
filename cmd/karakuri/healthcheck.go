package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
)

// HealthcheckCmd asks the server on this host whether it is serving. It is
// the container image's healthcheck, which has no curl or wget to call the
// health route with, and it exits non-zero when the server does not answer.
type HealthcheckCmd struct {
	Timeout time.Duration `default:"5s" help:"How long to wait for the server to answer."`
}

func (c *HealthcheckCmd) Run() error {
	// A server listening on every interface answers on loopback.
	host := config.C.Server.Host
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	url := "http://" + net.JoinHostPort(host, strconv.Itoa(config.C.Server.Port)) + "/api/health"

	client := &http.Client{Timeout: c.Timeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("the server did not answer at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the server answered %s at %s", resp.Status, url)
	}
	return nil
}
