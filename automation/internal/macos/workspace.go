//go:build darwin

package macos

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Opening a URL and launching an application are NSWorkspace, reached over
// the Objective-C runtime the same way the pasteboard is. The launch path
// rides launchApplicationAtURL:options:configuration:error: — deprecated in
// favor of a block-taking variant purego cannot express, but still the
// synchronous call open(1) wraps — with name resolution through
// fullPathForApplication:, the lookup `open -a` performs. The open tool
// stays behind it for whatever these entry points eventually refuse.

// nsWorkspaceLaunchDefault is NSWorkspaceLaunchAsync | AllowingClassicStartup,
// from NSWorkspace.h.
const nsWorkspaceLaunchDefault = 0x00030000

var (
	workspaceOnce sync.Once
	workspaceErr  error

	selSharedWorkspace         objc.SEL
	selOpenURL                 objc.SEL
	selURLWithString           objc.SEL
	selFileURLWithPath         objc.SEL
	selFullPathForApplication  objc.SEL
	selLaunchApplicationAtURL  objc.SEL
	selArray                   objc.SEL
	selAddObject               objc.SEL
	selDictionaryWithObjectKey objc.SEL

	classNSWorkspace    objc.Class
	classNSURL          objc.Class
	classNSMutableArray objc.Class
	classNSDictionary   objc.Class

	// launchConfigArgumentsKey is the NSWorkspaceLaunchConfigurationArguments
	// constant, dereferenced from AppKit rather than spelled out.
	launchConfigArgumentsKey objc.ID
)

// loadWorkspace resolves the NSWorkspace vocabulary once, on top of AppKit.
func loadWorkspace() error {
	if err := loadAppKit(); err != nil {
		return err
	}
	workspaceOnce.Do(func() {
		appKit, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			workspaceErr = fmt.Errorf("opening AppKit: %w", err)
			return
		}
		selSharedWorkspace = objc.RegisterName("sharedWorkspace")
		selOpenURL = objc.RegisterName("openURL:")
		selURLWithString = objc.RegisterName("URLWithString:")
		selFileURLWithPath = objc.RegisterName("fileURLWithPath:")
		selFullPathForApplication = objc.RegisterName("fullPathForApplication:")
		selLaunchApplicationAtURL = objc.RegisterName("launchApplicationAtURL:options:configuration:error:")
		selArray = objc.RegisterName("array")
		selAddObject = objc.RegisterName("addObject:")
		selDictionaryWithObjectKey = objc.RegisterName("dictionaryWithObject:forKey:")
		classNSWorkspace = objc.GetClass("NSWorkspace")
		classNSURL = objc.GetClass("NSURL")
		classNSMutableArray = objc.GetClass("NSMutableArray")
		classNSDictionary = objc.GetClass("NSDictionary")
		if classNSWorkspace == 0 || classNSURL == 0 || classNSMutableArray == 0 || classNSDictionary == 0 {
			workspaceErr = fmt.Errorf("AppKit loaded but NSWorkspace did not resolve")
			return
		}
		if sym, err := purego.Dlsym(appKit, "NSWorkspaceLaunchConfigurationArguments"); err == nil && sym != 0 {
			launchConfigArgumentsKey = *(*objc.ID)(unsafe.Pointer(sym))
		}
	})
	return workspaceErr
}

// workspaceOpenURL hands a URL to the default handler through NSWorkspace.
func workspaceOpenURL(url string) error {
	if err := loadWorkspace(); err != nil {
		return err
	}
	nsurl := objc.ID(classNSURL).Send(selURLWithString, nsString(url))
	if nsurl == 0 {
		return fmt.Errorf("not a valid URL: %q", url)
	}
	workspace := objc.ID(classNSWorkspace).Send(selSharedWorkspace)
	if workspace == 0 {
		return fmt.Errorf("no workspace in this session")
	}
	if workspace.Send(selOpenURL, nsurl) == 0 {
		return fmt.Errorf("the system declined to open %q", url)
	}
	return nil
}

// workspaceLaunchApp starts an application by name or path, with arguments,
// and leaves it running — what `open -a ... --args ...` does.
func workspaceLaunchApp(app string, args []string) error {
	if err := loadWorkspace(); err != nil {
		return err
	}
	workspace := objc.ID(classNSWorkspace).Send(selSharedWorkspace)
	if workspace == 0 {
		return fmt.Errorf("no workspace in this session")
	}

	// A bare name resolves through Launch Services; a path is taken as given.
	path := app
	if !strings.ContainsRune(app, '/') {
		resolved := workspace.Send(selFullPathForApplication, nsString(app))
		if resolved == 0 {
			return fmt.Errorf("no application called %q is installed", app)
		}
		path = goString(resolved)
	}
	nsurl := objc.ID(classNSURL).Send(selFileURLWithPath, nsString(path))
	if nsurl == 0 {
		return fmt.Errorf("not a usable application path: %q", path)
	}

	// The configuration carries the arguments; with none, an empty dictionary
	// keeps one code path.
	config := objc.ID(0)
	if len(args) > 0 && launchConfigArgumentsKey != 0 {
		list := objc.ID(classNSMutableArray).Send(selArray)
		for _, arg := range args {
			list.Send(selAddObject, nsString(arg))
		}
		config = objc.ID(classNSDictionary).Send(selDictionaryWithObjectKey, list, launchConfigArgumentsKey)
	}

	running := workspace.Send(selLaunchApplicationAtURL, nsurl, uintptr(nsWorkspaceLaunchDefault), config, objc.ID(0))
	if running == 0 {
		return fmt.Errorf("the system declined to launch %q", app)
	}
	return nil
}
