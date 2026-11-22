//go:build !windows && !darwin

package linuxdesk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// speech-dispatcher speaks SSIP, a line protocol on a session socket, and
// spd-say is a thin client for it. Speaking through the socket directly means
// nothing to install on a host whose daemon is already running; the daemon not
// running is the connect error, and the caller falls back to spd-say, whose
// library starts the daemon on demand.

// ssipSocketPath returns the daemon's socket address. SPEECHD_ADDRESS
// overrides it the same way it overrides the C library.
func ssipSocketPath() (network, addr string) {
	if env := strings.TrimSpace(os.Getenv("SPEECHD_ADDRESS")); env != "" {
		if path, ok := strings.CutPrefix(env, "unix_socket:"); ok {
			return "unix", path
		}
		if hostPort, ok := strings.CutPrefix(env, "inet_socket:"); ok {
			return "tcp", hostPort
		}
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return "unix", filepath.Join(runtime, "speech-dispatcher", "speechd.sock")
}

// ssipClient is one SSIP connection.
type ssipClient struct {
	conn      net.Conn
	r         *bufio.Reader
	closeStop func() bool
}

// errNoSpeechd marks the daemon not answering its socket, which is the one
// failure that sends the caller to spd-say — whose library starts the daemon —
// rather than being the answer itself.
var errNoSpeechd = errors.New("speech-dispatcher is not running")

// dialSSIP connects to the daemon. The context governs the whole conversation,
// not just the dial: speaking with Wait holds the connection for as long as
// the speech runs, and closing the socket is how a cancelled context ends it.
func dialSSIP(ctx context.Context) (*ssipClient, error) {
	network, addr := ssipSocketPath()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoSpeechd, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	client := &ssipClient{conn: conn, r: bufio.NewReader(conn)}
	// The stop func is kept alive by the connection's own lifetime: Close both
	// releases the watcher and unblocks any read.
	client.closeStop = stop
	return client, nil
}

// command sends one line and reads its reply, failing on any non-2xx code.
func (c *ssipClient) command(line string) error {
	if _, err := c.conn.Write([]byte(line + "\r\n")); err != nil {
		return err
	}
	code, _, err := c.reply()
	if err != nil {
		return err
	}
	if code < 200 || code > 299 {
		return fmt.Errorf("speech-dispatcher answered %d to %q", code, strings.Fields(line)[0])
	}
	return nil
}

// reply reads one SSIP reply: zero or more "NNN-data" continuation lines and a
// final "NNN text" line, all sharing the code.
func (c *ssipClient) reply() (code int, data []string, err error) {
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return 0, nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) < 4 {
			return 0, nil, fmt.Errorf("speech-dispatcher sent an unreadable line %q", line)
		}
		code, err = strconv.Atoi(line[:3])
		if err != nil {
			return 0, nil, fmt.Errorf("speech-dispatcher sent an unreadable line %q", line)
		}
		if line[3] == '-' {
			data = append(data, line[4:])
			continue
		}
		return code, data, nil
	}
}

// speakSSIP queues text with the daemon and, when asked to, stays on the line
// until the daemon reports the message spoken or cancelled.
func speakSSIP(ctx context.Context, text, voice string, rate int, wait bool) error {
	client, err := dialSSIP(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.command(`SET SELF CLIENT_NAME "karakuri:agent:speak"`); err != nil {
		return err
	}
	if rate > 0 {
		if err := client.command("SET SELF RATE " + strconv.Itoa(speechDispatcherRate(rate))); err != nil {
			return err
		}
	}
	if voice != "" {
		if err := client.command("SET SELF SYNTHESIS_VOICE " + voice); err != nil {
			return fmt.Errorf("selecting voice %q: %w", voice, err)
		}
	}
	if wait {
		// END covers the message finishing, CANCEL covers it being swept away
		// by a stop; without the second a cancelled message waits forever.
		if err := client.command("SET SELF NOTIFICATION END on"); err != nil {
			return err
		}
		if err := client.command("SET SELF NOTIFICATION CANCEL on"); err != nil {
			return err
		}
	}

	if _, err := client.conn.Write([]byte("SPEAK\r\n")); err != nil {
		return err
	}
	if code, _, err := client.reply(); err != nil {
		return err
	} else if code != 230 {
		return fmt.Errorf("speech-dispatcher answered %d to SPEAK", code)
	}

	// A line of a single dot ends the text, so a literal dot line is doubled.
	var body strings.Builder
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, ".") {
			line = "." + line
		}
		body.WriteString(line + "\r\n")
	}
	body.WriteString(".\r\n")
	if _, err := client.conn.Write([]byte(body.String())); err != nil {
		return err
	}

	code, data, err := client.reply()
	if err != nil {
		return err
	}
	if code != 225 || len(data) == 0 {
		return fmt.Errorf("speech-dispatcher did not queue the message (code %d)", code)
	}
	messageID := data[0]

	if !wait {
		// The daemon owns the queue, so the speech outlives the connection.
		return nil
	}
	for {
		code, data, err := client.reply()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		// 702 is END and 703 CANCEL; both mean this message is over. Other
		// events (BEGIN, PAUSE, RESUME) are progress, not an ending.
		if (code == 702 || code == 703) && len(data) > 0 && data[0] == messageID {
			return nil
		}
	}
}

// Close hangs up. The daemon keeps playing anything already queued.
func (c *ssipClient) Close() {
	if c.closeStop != nil {
		c.closeStop()
	}
	_ = c.conn.Close()
}
