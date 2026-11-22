//go:build linux

package unixauth

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	pamSuccess     = 0
	pamBufErr      = 5
	pamAuthErr     = 7
	pamUserUnknown = 10
	pamConvErr     = 19

	pamPromptEchoOff = 1
	pamPromptEchoOn  = 2
	pamErrorMsg      = 3
	pamTextInfo      = 4
)

type pamConversation struct {
	Callback uintptr
	Data     unsafe.Pointer
}

type pamMessage struct {
	Style int32
	Text  unsafe.Pointer
}

type pamResponse struct {
	Text    unsafe.Pointer
	Retcode int32
}

type pamCredentials struct {
	User   unsafe.Pointer
	Secret unsafe.Pointer
}

var pamLibrary struct {
	once sync.Once
	err  error

	start        func(string, string, *pamConversation, *unsafe.Pointer) int32
	end          func(unsafe.Pointer, int32) int32
	authenticate func(unsafe.Pointer, int32) int32
	account      func(unsafe.Pointer, int32) int32
	strerror     func(unsafe.Pointer, int32) string
	calloc       func(uintptr, uintptr) unsafe.Pointer
	strdupString func(string) unsafe.Pointer
	strdupPtr    func(unsafe.Pointer) unsafe.Pointer
	free         func(unsafe.Pointer)
	callback     uintptr
}

// AuthenticateUser passes a password through the host's PAM login stack. PAM
// is required here because modules such as pam_systemd_home and pam_mount use
// the login credential to unlock encrypted home storage; comparing /etc/shadow
// cannot perform that work or honor network identity and lockout policy.
func AuthenticateUser(user, password string) (bool, error) {
	if err := loadPAM(); err != nil {
		return false, err
	}

	credentials := pamLibrary.calloc(1, unsafe.Sizeof(pamCredentials{}))
	if credentials == nil {
		return false, fmt.Errorf("allocating PAM credentials")
	}
	defer pamLibrary.free(credentials)
	stored := (*pamCredentials)(credentials)
	stored.User = pamLibrary.strdupString(user)
	stored.Secret = pamLibrary.strdupString(password)
	if stored.User == nil || stored.Secret == nil {
		if stored.User != nil {
			pamLibrary.free(stored.User)
		}
		if stored.Secret != nil {
			pamLibrary.free(stored.Secret)
		}
		return false, fmt.Errorf("copying PAM credentials")
	}
	defer pamLibrary.free(stored.User)
	defer pamLibrary.free(stored.Secret)

	conversation := pamConversation{Callback: pamLibrary.callback, Data: credentials}
	var handle unsafe.Pointer
	status := pamLibrary.start("login", user, &conversation, &handle)
	if status != pamSuccess {
		return false, pamError(handle, status, "starting authentication")
	}
	defer func() { _ = pamLibrary.end(handle, status) }()

	status = pamLibrary.authenticate(handle, 0)
	if status == pamAuthErr || status == pamUserUnknown {
		return false, nil
	}
	if status != pamSuccess {
		return false, pamError(handle, status, "authenticating account")
	}
	status = pamLibrary.account(handle, 0)
	if status != pamSuccess {
		return false, pamError(handle, status, "checking account")
	}
	return true, nil
}

func loadPAM() error {
	pamLibrary.once.Do(func() {
		handle, err := purego.Dlopen("libpam.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			pamLibrary.err = fmt.Errorf("loading libpam.so.0: %w", err)
			return
		}
		purego.RegisterLibFunc(&pamLibrary.start, handle, "pam_start")
		purego.RegisterLibFunc(&pamLibrary.end, handle, "pam_end")
		purego.RegisterLibFunc(&pamLibrary.authenticate, handle, "pam_authenticate")
		purego.RegisterLibFunc(&pamLibrary.account, handle, "pam_acct_mgmt")
		purego.RegisterLibFunc(&pamLibrary.strerror, handle, "pam_strerror")
		purego.RegisterLibFunc(&pamLibrary.calloc, purego.RTLD_DEFAULT, "calloc")
		purego.RegisterLibFunc(&pamLibrary.strdupString, purego.RTLD_DEFAULT, "strdup")
		purego.RegisterLibFunc(&pamLibrary.strdupPtr, purego.RTLD_DEFAULT, "strdup")
		purego.RegisterLibFunc(&pamLibrary.free, purego.RTLD_DEFAULT, "free")
		pamLibrary.callback = purego.NewCallback(pamConversationCallback)
	})
	return pamLibrary.err
}

func pamConversationCallback(messageCount int32, messages unsafe.Pointer, response *unsafe.Pointer, data unsafe.Pointer) int32 {
	if messageCount <= 0 || messages == nil || response == nil || data == nil {
		return pamConvErr
	}
	credentials := (*pamCredentials)(data)
	allocated := pamLibrary.calloc(uintptr(messageCount), unsafe.Sizeof(pamResponse{}))
	if allocated == nil {
		return pamBufErr
	}
	responses := unsafe.Slice((*pamResponse)(allocated), int(messageCount))
	messagePointers := unsafe.Slice((**pamMessage)(messages), int(messageCount))
	for i, messagePointer := range messagePointers {
		if messagePointer == nil {
			freePAMResponses(allocated, responses)
			return pamConvErr
		}
		message := messagePointer
		var answer unsafe.Pointer
		switch message.Style {
		case pamPromptEchoOff:
			answer = credentials.Secret
		case pamPromptEchoOn:
			answer = credentials.User
		case pamErrorMsg, pamTextInfo:
			continue
		default:
			freePAMResponses(allocated, responses)
			return pamConvErr
		}
		responses[i].Text = pamLibrary.strdupPtr(answer)
		if responses[i].Text == nil {
			freePAMResponses(allocated, responses)
			return pamBufErr
		}
	}
	*response = allocated
	return pamSuccess
}

func freePAMResponses(allocated unsafe.Pointer, responses []pamResponse) {
	for _, response := range responses {
		if response.Text != nil {
			pamLibrary.free(response.Text)
		}
	}
	pamLibrary.free(allocated)
}

func pamError(handle unsafe.Pointer, status int32, operation string) error {
	message := "unknown PAM error"
	if text := pamLibrary.strerror(handle, status); text != "" {
		message = text
	}
	return fmt.Errorf("%s: %s", operation, message)
}
