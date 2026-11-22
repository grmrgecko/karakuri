package portal

import (
	"context"
	"fmt"
	"image"
	_ "image/png" // decode the capture the portal's backend writes
	"net/url"
	"os"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/grmrgecko/karakuri/automation/internal/dbusconn"
)

// The Screenshot portal photographs the whole desktop through the compositor
// itself, which is the one route on Wayland that needs nothing installed:
// GNOME, KDE, and the wlroots compositors each ship a backend for it. Like
// RemoteDesktop, the first request may raise a consent dialog the user answers
// once; after that the permission store makes every capture silent.

// screenshotTimeout bounds one capture, sized for a person answering the
// first-use consent dialog rather than for the capture itself.
const screenshotTimeout = 90 * time.Second

// CaptureScreenshot photographs the desktop through the Screenshot portal and
// returns the decoded image. The portal writes a file and answers with its
// URI; the file is read back and removed, since the screenshot's home is the
// message payload rather than the user's disk.
func CaptureScreenshot(ctx context.Context) (image.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, screenshotTimeout)
	defer cancel()

	// A private connection keeps the portal's Response signal out of the shared
	// bus connection the other backends listen on.
	conn, err := dbusconn.SessionPrivate()
	if err != nil {
		return nil, fmt.Errorf("no session bus for the Screenshot portal: %w", err)
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return nil, err
	}
	if err := conn.Hello(); err != nil {
		return nil, err
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return nil, err
	}
	sigc := make(chan *dbus.Signal, 4)
	conn.Signal(sigc)

	// One request per connection, so a fixed token cannot collide.
	options := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant("karakuri_screenshot"),
		"interactive":  dbus.MakeVariant(false),
	}
	var request dbus.ObjectPath
	err = conn.Object(portalService, portalPath).
		CallWithContext(ctx, "org.freedesktop.portal.Screenshot.Screenshot", 0, "", options).
		Store(&request)
	if err != nil {
		return nil, fmt.Errorf("the Screenshot portal did not take the request: %w", err)
	}

	results, err := waitScreenshotResponse(ctx, sigc, request)
	if err != nil {
		return nil, err
	}
	uriVar, ok := results["uri"]
	if !ok {
		return nil, fmt.Errorf("the portal reported success but returned no screenshot")
	}
	uri, _ := uriVar.Value().(string)
	path, err := fileURIPath(uri)
	if err != nil {
		return nil, err
	}
	// The portal's backend wrote the file for this request, so it is cleaned up
	// here whether or not it decodes.
	defer func() { _ = os.Remove(path) }()

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("the portal's screenshot is unreadable: %w", err)
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("the portal's screenshot did not decode: %w", err)
	}
	return img, nil
}

// waitScreenshotResponse blocks for the Response signal on one Request path,
// the same dance the RemoteDesktop backend does on its own connection.
func waitScreenshotResponse(ctx context.Context, sigc chan *dbus.Signal, request dbus.ObjectPath) (map[string]dbus.Variant, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("no answer from the Screenshot portal (approval declined or timed out): %w", ctx.Err())
		case sig := <-sigc:
			if sig == nil || sig.Path != request || sig.Name != "org.freedesktop.portal.Request.Response" {
				continue
			}
			if len(sig.Body) < 2 {
				return nil, fmt.Errorf("malformed portal response")
			}
			code, _ := sig.Body[0].(uint32)
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			switch code {
			case 0:
				return results, nil
			case 1:
				return nil, fmt.Errorf("the screenshot was declined")
			default:
				return nil, fmt.Errorf("the portal ended the screenshot request (code %d)", code)
			}
		}
	}
}

// fileURIPath resolves the file: URI the portal answers with into a path,
// decoding the percent-escapes a space in the backend's filename produces.
func fileURIPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", fmt.Errorf("the portal answered with %q, not a file URI", uri)
	}
	return u.Path, nil
}
