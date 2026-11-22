//go:build windows

package windows

import (
	"context"
	"fmt"
	"runtime"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// Speech is SAPI's SpVoice over COM automation, the same synthesizer the .NET
// class the old PowerShell script constructed sits on — reached directly, so a
// speak costs a COM call rather than a PowerShell start. The voice object
// lives on one locked OS thread for its whole life because COM apartment
// rules attach it to the thread that made it.

// SAPI SpeechVoiceSpeakFlags.
const (
	svsfIsAsync          = 1
	svsfPurgeBeforeSpeak = 2
)

// speakText reads text out through SAPI. A detached speak runs the same
// synchronous path on its own goroutine: the agent process outlives the
// speech, which is what the detached PowerShell process existed to do.
func speakText(ctx context.Context, text, voice string, rate int, wait bool) error {
	if !wait {
		go func() { _ = speakSAPI(context.Background(), text, voice, rate) }()
		return nil
	}
	return speakSAPI(ctx, text, voice, rate)
}

// speakSAPI creates an SpVoice, applies the voice and rate, and speaks. The
// speak is issued asynchronously and polled so a cancelled context can purge
// the queue instead of holding the run until the sentence ends.
func speakSAPI(ctx context.Context, text, voice string, rate int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// S_FALSE means this thread was already initialized, which is fine.
		if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 1 {
			return fmt.Errorf("initializing COM: %w", err)
		}
	} else {
		defer ole.CoUninitialize()
	}

	unknown, err := oleutil.CreateObject("SAPI.SpVoice")
	if err != nil {
		return fmt.Errorf("creating the SAPI voice: %w", err)
	}
	defer unknown.Release()
	sp, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("the SAPI voice has no automation interface: %w", err)
	}
	defer sp.Release()

	if voice != "" {
		if err := selectSAPIVoice(sp, voice); err != nil {
			return err
		}
	}
	if rate > 0 {
		if _, err := oleutil.PutProperty(sp, "Rate", windowsSpeechRate(rate)); err != nil {
			return fmt.Errorf("setting the speech rate: %w", err)
		}
	}

	if _, err := oleutil.CallMethod(sp, "Speak", text, svsfIsAsync); err != nil {
		return fmt.Errorf("speaking: %w", err)
	}
	for {
		done, err := oleutil.CallMethod(sp, "WaitUntilDone", 100)
		if err != nil {
			return fmt.Errorf("waiting for speech to finish: %w", err)
		}
		if done.Value() == true {
			return nil
		}
		select {
		case <-ctx.Done():
			_, _ = oleutil.CallMethod(sp, "Speak", "", svsfIsAsync|svsfPurgeBeforeSpeak)
			return ctx.Err()
		default:
		}
	}
}

// selectSAPIVoice picks an installed voice by its exact name, the same match
// the .NET SelectVoice made, and says which name missed rather than speaking
// in the default voice as if nothing happened.
func selectSAPIVoice(sp *ole.IDispatch, name string) error {
	tokens, err := oleutil.CallMethod(sp, "GetVoices", "Name="+name, "")
	if err != nil {
		return fmt.Errorf("listing voices: %w", err)
	}
	list := tokens.ToIDispatch()
	defer list.Release()
	count, err := oleutil.GetProperty(list, "Count")
	if err != nil {
		return fmt.Errorf("listing voices: %w", err)
	}
	if count.Value().(int32) == 0 {
		return fmt.Errorf("no installed voice is named %q", name)
	}
	item, err := oleutil.CallMethod(list, "Item", 0)
	if err != nil {
		return fmt.Errorf("reading voice %q: %w", name, err)
	}
	token := item.ToIDispatch()
	defer token.Release()
	if _, err := oleutil.PutPropertyRef(sp, "Voice", token); err != nil {
		return fmt.Errorf("selecting voice %q: %w", name, err)
	}
	return nil
}
