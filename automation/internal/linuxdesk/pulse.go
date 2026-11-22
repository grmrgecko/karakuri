//go:build !windows && !darwin

package linuxdesk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jfreymuth/pulse/proto"
)

// The volume is set over PulseAudio's own socket protocol. PipeWire ships the
// same protocol through pipewire-pulse, so one client covers both servers —
// which is nearly every Linux desktop — without wpctl or pactl installed. The
// connection is opened per call rather than held: volume changes are rare, and
// a standing socket would be one more thing to reconnect when the sound server
// restarts.

// errNoPulse marks the one failure that sends the caller to the mixer tools:
// no server socket answered. An error after the handshake is real and is
// reported as the answer.
var errNoPulse = errors.New("no PulseAudio or PipeWire socket answered")

// pulseVolume performs one volume action against the default sink.
func pulseVolume(ctx context.Context, action string, volume float64) error {
	client, conn, err := proto.Connect("")
	if err != nil {
		return fmt.Errorf("%w: %v", errNoPulse, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		client.SetTimeout(time.Until(deadline))
	}

	props := proto.PropList{"application.name": proto.PropListString("karakuri")}
	if err := client.Request(&proto.SetClientName{Props: props}, &proto.SetClientNameReply{}); err != nil {
		return fmt.Errorf("introducing the client: %w", err)
	}

	// The @DEFAULT_SINK@ alias is resolved by the C client library, not the
	// server, so the default sink is read by name here the same way.
	var server proto.GetServerInfoReply
	if err := client.Request(&proto.GetServerInfo{}, &server); err != nil {
		return fmt.Errorf("reading the server info: %w", err)
	}
	sink := server.DefaultSinkName

	var info proto.GetSinkInfoReply
	if err := client.Request(&proto.GetSinkInfo{SinkIndex: proto.Undefined, SinkName: sink}, &info); err != nil {
		return fmt.Errorf("reading sink %s: %w", sink, err)
	}

	switch action {
	case mediaMute, mediaUnmute, mediaMuteToggle:
		mute := true
		switch action {
		case mediaUnmute:
			mute = false
		case mediaMuteToggle:
			mute = !info.Mute
		}
		err := client.Request(&proto.SetSinkMute{SinkIndex: proto.Undefined, SinkName: sink, Mute: mute}, nil)
		if err != nil {
			return fmt.Errorf("setting mute on %s: %w", sink, err)
		}
		return nil

	case mediaVolumeSet, mediaVolumeUp, mediaVolumeDown:
		target := volume / 100
		if action != mediaVolumeSet {
			// Up and down step from the channels' average, and every channel is
			// set to the result: the same flattening the mixer tools do.
			current := info.ChannelVolumes.Avg().Norm()
			step := float64(volumeStep) / 100
			if action == mediaVolumeUp {
				target = current + step
			} else {
				target = current - step
			}
			if target > 1 {
				target = 1
			}
		}
		if target < 0 {
			target = 0
		}
		volumes := make(proto.ChannelVolumes, len(info.ChannelVolumes))
		for i := range volumes {
			volumes[i] = proto.NormVolume(target)
		}
		err := client.Request(&proto.SetSinkVolume{SinkIndex: proto.Undefined, SinkName: sink, ChannelVolumes: volumes}, nil)
		if err != nil {
			return fmt.Errorf("setting the volume on %s: %w", sink, err)
		}
		return nil
	}
	return fmt.Errorf("unknown volume action %q", action)
}
