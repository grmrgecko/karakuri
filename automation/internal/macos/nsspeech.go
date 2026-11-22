//go:build darwin

package macos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Speech is NSSpeechSynthesizer — the engine the say tool drives — reached
// over the Objective-C runtime, so a speak costs a call into the speech
// server rather than a process start. The rate property is words per minute,
// the same unit the request carries. say stays behind this for a host where
// the class will not resolve.

// errNoNSSpeech marks the one failure that sends the caller to the say tool:
// the synthesizer could not be reached at all. An error from a synthesizer
// that answered — an unknown voice, declined text — is the answer.
var errNoNSSpeech = errors.New("NSSpeechSynthesizer is unavailable")

var (
	nsSpeechOnce sync.Once
	nsSpeechErr  error

	classNSSpeechSynthesizer objc.Class

	selAlloc               objc.SEL
	selInit                objc.SEL
	selAvailableVoices     objc.SEL
	selAttributesForVoice  objc.SEL
	selObjectForKey        objc.SEL
	selCount               objc.SEL
	selObjectAtIndex       objc.SEL
	selSetVoice            objc.SEL
	selSetRate             objc.SEL
	selStartSpeakingString objc.SEL
	selIsSpeaking          objc.SEL
	selStopSpeaking        objc.SEL

	// setRate: takes a C float, which the generic message send cannot marshal,
	// so it goes through a message send bound with that exact signature.
	msgSendSetRate func(objc.ID, objc.SEL, float32)

	// speechMu serializes access to the one synthesizer; sharedSynth lives for
	// the process because releasing a synthesizer stops what it is saying.
	speechMu    sync.Mutex
	sharedSynth objc.ID
)

// loadNSSpeech resolves the speech vocabulary once, on top of AppKit.
func loadNSSpeech() error {
	if err := loadAppKit(); err != nil {
		return fmt.Errorf("%w: %v", errNoNSSpeech, err)
	}
	nsSpeechOnce.Do(func() {
		classNSSpeechSynthesizer = objc.GetClass("NSSpeechSynthesizer")
		if classNSSpeechSynthesizer == 0 {
			nsSpeechErr = fmt.Errorf("%w: AppKit loaded but the class did not resolve", errNoNSSpeech)
			return
		}
		objcLib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			nsSpeechErr = fmt.Errorf("%w: %v", errNoNSSpeech, err)
			return
		}
		purego.RegisterLibFunc(&msgSendSetRate, objcLib, "objc_msgSend")

		selAlloc = objc.RegisterName("alloc")
		selInit = objc.RegisterName("init")
		selAvailableVoices = objc.RegisterName("availableVoices")
		selAttributesForVoice = objc.RegisterName("attributesForVoice:")
		selObjectForKey = objc.RegisterName("objectForKey:")
		selCount = objc.RegisterName("count")
		selObjectAtIndex = objc.RegisterName("objectAtIndex:")
		selSetVoice = objc.RegisterName("setVoice:")
		selSetRate = objc.RegisterName("setRate:")
		selStartSpeakingString = objc.RegisterName("startSpeakingString:")
		selIsSpeaking = objc.RegisterName("isSpeaking")
		selStopSpeaking = objc.RegisterName("stopSpeaking")
	})
	return nsSpeechErr
}

// voiceIdentifier resolves what the author wrote — a display name like
// "Samantha", or a full identifier — to the identifier NSString setVoice:
// takes. An empty request answers 0, which setVoice: reads as the default.
func voiceIdentifier(name string) (objc.ID, error) {
	if strings.TrimSpace(name) == "" {
		return 0, nil
	}
	voices := objc.ID(classNSSpeechSynthesizer).Send(selAvailableVoices)
	count := int(voices.Send(selCount))
	want := strings.ToLower(name)
	for i := 0; i < count; i++ {
		voice := voices.Send(selObjectAtIndex, i)
		identifier := strings.ToLower(goString(voice))
		if identifier == want || strings.HasSuffix(identifier, "."+want) {
			return voice, nil
		}
		attrs := objc.ID(classNSSpeechSynthesizer).Send(selAttributesForVoice, voice)
		if attrs != 0 && strings.EqualFold(goString(attrs.Send(selObjectForKey, nsString("VoiceName"))), name) {
			return voice, nil
		}
	}
	return 0, fmt.Errorf("no voice called %q on this host", name)
}

// speakNS reads text out through the shared synthesizer. A new utterance
// replaces one still playing — the last command wins, the way a control does —
// where overlapping say processes used to talk over each other.
func speakNS(ctx context.Context, text, voice string, rate int, wait bool) error {
	if err := loadNSSpeech(); err != nil {
		return err
	}

	speechMu.Lock()
	if sharedSynth == 0 {
		sharedSynth = objc.ID(classNSSpeechSynthesizer).Send(selAlloc).Send(selInit)
		if sharedSynth == 0 {
			speechMu.Unlock()
			return fmt.Errorf("%w: the synthesizer would not initialize", errNoNSSpeech)
		}
	}
	synth := sharedSynth

	identifier, err := voiceIdentifier(voice)
	if err != nil {
		speechMu.Unlock()
		return err
	}
	if synth.Send(selIsSpeaking) != 0 {
		synth.Send(selStopSpeaking)
	}
	// Setting the voice resets the rate to that voice's default, so the rate
	// is applied after it; identifier 0 is nil, which selects the default
	// voice, so a request with no voice does not keep the previous call's.
	if synth.Send(selSetVoice, identifier) == 0 {
		speechMu.Unlock()
		return fmt.Errorf("the synthesizer refused the voice %q", voice)
	}
	if rate > 0 {
		msgSendSetRate(synth, selSetRate, float32(rate))
	}
	if synth.Send(selStartSpeakingString, nsString(text)) == 0 {
		speechMu.Unlock()
		return fmt.Errorf("the synthesizer declined the text")
	}
	speechMu.Unlock()

	if !wait {
		return nil
	}
	// Speech proceeds in the speech server; polling the synthesizer is how a
	// caller without a delegate run loop learns it finished.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			speechMu.Lock()
			synth.Send(selStopSpeaking)
			speechMu.Unlock()
			return ctx.Err()
		case <-ticker.C:
			speechMu.Lock()
			speaking := synth.Send(selIsSpeaking) != 0
			speechMu.Unlock()
			if !speaking {
				return nil
			}
		}
	}
}
