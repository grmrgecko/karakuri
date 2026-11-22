//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"os"
	"sync"

	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

var autoKernel = windows.NewLazySystemDLL("kernel32.dll")
var consoleSessionProc = autoKernel.NewProc("WTSGetActiveConsoleSessionId")

const residentProtocol = 1

// systemSID is LocalSystem's well-known SID. A process holding it is the one
// identity that can start a process in another session, so it delegates rather
// than forwarding to a resident.
const systemSID = "S-1-5-18"

type residentRequest struct {
	Version   int
	Args      []string
	Directory string
	Timeout   time.Duration
}

type residentResponse struct {
	Version        int
	Stdout, Stderr string
	Code           int
}

func currentWindowsSession() (uint32, error) {
	var session uint32
	err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session)
	return session, err
}

func processIdentity(pid uint32) (string, bool, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false, err
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err = windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", false, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", false, err
	}
	return user.User.Sid.String(), token.IsElevated(), nil
}

func residentPipe(session uint32) string {
	return fmt.Sprintf(`\\.\pipe\karakuri-auto-v1-session-%d`, session)
}

func residentPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "karakuri-auto", "resident.exe"), nil
}

func platformPreflight(ctx context.Context, args []string) (bool, error) {
	if args[0] == "resident" {
		if len(args) != 2 {
			return true, fmt.Errorf("usage: resident <install|uninstall|serve>")
		}
		switch args[1] {
		case "serve":
			return true, serveResident(context.Background())
		case "serve-hidden":
			hwnd, _, _ := autoKernel.NewProc("GetConsoleWindow").Call()
			if hwnd != 0 {
				windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindow").Call(hwnd, 0)
			}
			return true, serveResident(context.Background())
		case "install":
			return true, installResident(ctx, false)
		case "uninstall":
			return true, installResident(ctx, true)
		default:
			return true, fmt.Errorf("unknown resident action %q", args[1])
		}
	}
	if os.Getenv("KARAKURI_AUTO_LOCAL") == "1" {
		return false, nil
	}
	// A SYSTEM caller reaches any session's desktop in its own process, the way
	// a root agent does on Unix: automation.New starts a helper as the console
	// user through CreateProcessAsUser. It needs no resident and must not forward
	// to one — session 0 has none, and only SYSTEM can make the crossing anyway —
	// so it composes locally and lets New delegate.
	if sid, _, err := processIdentity(uint32(os.Getpid())); err == nil && sid == systemSID {
		return false, nil
	}
	switch args[0] {
	case "version", "image-match", "sessions", "lock", "unlock", "login", "logout", "activate", "greeter", "serve-helper", "secure-input":
		return false, nil
	}
	session, err := currentWindowsSession()
	if err != nil {
		return true, err
	}
	console, _, _ := consoleSessionProc.Call()
	if session == uint32(console) {
		return false, nil
	}
	if uint32(console) == 0xffffffff {
		return true, fmt.Errorf("no console session is attached")
	}
	return true, forwardResident(ctx, uint32(console), args)
}

func installResident(ctx context.Context, remove bool) error {
	sid, _, err := processIdentity(uint32(os.Getpid()))
	if err != nil {
		return err
	}
	task := "KarakuriAuto-" + sid
	path, err := residentPath()
	if err != nil {
		return err
	}
	manifest := filepath.Join(filepath.Dir(path), "task-name")
	oldTask := task
	if data, readErr := os.ReadFile(manifest); readErr == nil {
		oldTask = strings.TrimSpace(string(data))
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if !strings.HasPrefix(oldTask, task) {
		return fmt.Errorf("invalid resident task name in %s", manifest)
	}
	if remove {
		console, _, _ := consoleSessionProc.Call()
		if uint32(console) != 0xffffffff {
			err = forwardResident(ctx, uint32(console), []string{"__resident-stop"})
			if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
				return err
			}
		}
		output, err := exec.CommandContext(ctx, "schtasks", "/Delete", "/TN", oldTask, "/F").CombinedOutput()
		if err != nil {
			return fmt.Errorf("removing resident task: %s: %w", output, err)
		}
		_ = os.Remove(manifest)
		fmt.Println("resident task removed; launched applications keep running")
		return nil
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 16)
	task += "-" + suffix
	path = filepath.Join(filepath.Dir(path), "resident-"+suffix+".exe")
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(path, data, 0700); err != nil {
		return err
	}
	output, err := exec.CommandContext(ctx, "whoami").Output()
	if err != nil {
		return err
	}
	user := strings.TrimSpace(string(output))
	xml := fmt.Sprintf(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers><Principals><Principal id="User"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings><Actions Context="User"><Exec><Command>%s</Command><Arguments>resident serve-hidden</Arguments></Exec></Actions></Task>`, sid, sid, html.EscapeString(path))
	definition, err := os.CreateTemp("", "karakuri-resident-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(definition.Name())
	if _, err = definition.WriteString(xml); err != nil {
		definition.Close()
		return err
	}
	if err = definition.Close(); err != nil {
		return err
	}
	output, err = exec.CommandContext(ctx, "schtasks", "/Create", "/TN", task, "/XML", definition.Name(), "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("installing resident task: %s: %w", output, err)
	}
	installed := false
	defer func() {
		if !installed {
			_ = exec.Command("schtasks", "/Delete", "/TN", task, "/F").Run()
			_ = os.Remove(path)
		}
	}()
	console, _, _ := consoleSessionProc.Call()
	if uint32(console) != 0xffffffff {
		err = forwardResident(ctx, uint32(console), []string{"__resident-stop"})
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return err
		}
	}
	output, err = exec.CommandContext(ctx, "schtasks", "/Run", "/TN", task).CombinedOutput()
	if err != nil {
		return fmt.Errorf("starting resident task: %s: %w", output, err)
	}
	if err = os.WriteFile(manifest, []byte(task), 0600); err != nil {
		return err
	}
	installed = true
	// Deleting a task leaves its running job alive, including applications
	// launched by an earlier resident. Ending the task would kill those apps.
	_ = exec.CommandContext(ctx, "schtasks", "/Delete", "/TN", oldTask, "/F").Run()
	fmt.Printf("resident installed for %s at %s\n", user, path)
	deadline := time.Now().Add(10 * time.Second)
	for uint32(console) != 0xffffffff && time.Now().Before(deadline) {
		err := forwardResident(ctx, uint32(console), []string{"version"})
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("resident is not running in the console yet; it will start when this user signs in")
	return nil
}

// forwardResident never replays a request after writing it: a lost reply may
// mean that a click or process launch already happened.
func forwardResident(ctx context.Context, session uint32, args []string) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f, err := winio.DialPipeContext(dialCtx, residentPipe(session))
	if err != nil {
		return fmt.Errorf("console session %d has no accessible resident; run karakuri-auto resident install as its user: %w", session, err)
	}
	defer f.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = f.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	handle := windows.Handle(f.(interface{ Fd() uintptr }).Fd())
	var pid uint32
	if err = windows.GetNamedPipeServerProcessId(handle, &pid); err != nil {
		return err
	}
	var serverSession uint32
	if err = windows.ProcessIdToSessionId(pid, &serverSession); err != nil {
		return err
	}
	serverSID, serverElevated, err := processIdentity(pid)
	if err != nil {
		return err
	}
	ownSID, ownElevated, err := processIdentity(uint32(os.Getpid()))
	if err != nil {
		return err
	}
	if serverSession != session || serverSID != ownSID || ownElevated && !serverElevated {
		return fmt.Errorf("resident identity does not match the requested desktop user and privilege level")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	timeout := 30 * time.Minute
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	if err = json.NewEncoder(f).Encode(residentRequest{residentProtocol, args, cwd, timeout}); err != nil {
		return err
	}
	var response residentResponse
	if err = json.NewDecoder(io.LimitReader(f, 12*residentOutputLimit+65536)).Decode(&response); err != nil {
		return fmt.Errorf("reading resident reply (command was not retried): %w", err)
	}
	_, _ = f.Write([]byte{1})
	if response.Version != residentProtocol {
		return fmt.Errorf("resident protocol mismatch; reinstall the resident")
	}
	fmt.Fprint(os.Stdout, response.Stdout)
	fmt.Fprint(os.Stderr, response.Stderr)
	if response.Code != 0 {
		return commandExit{response.Code}
	}
	return nil
}

func serveResident(ctx context.Context) error {
	session, err := currentWindowsSession()
	if err != nil {
		return err
	}
	if session == 0 {
		return fmt.Errorf("resident serve must run in an interactive user session")
	}
	sid, elevated, err := processIdentity(uint32(os.Getpid()))
	if err != nil {
		return err
	}
	acl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	if !elevated {
		acl += "(A;;GA;;;" + sid + ")"
	}
	listener, err := listenResidentPipe(residentPipe(session), acl)
	if err != nil {
		return err
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	slots := make(chan struct{}, 16)
	var running sync.WaitGroup
	defer running.Wait()
	var stopped bool
	var stopMu sync.Mutex
	stopResident := func() error {
		stopMu.Lock()
		if len(slots) > 1 {
			stopMu.Unlock()
			return fmt.Errorf("resident has running commands; retry after they finish")
		}
		stopped = true
		stopMu.Unlock()
		return listener.Close()
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			stopMu.Lock()
			wasStopped := stopped
			stopMu.Unlock()
			if wasStopped {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		stopMu.Lock()
		if stopped {
			stopMu.Unlock()
			_ = connection.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
			running.Add(1)
			go func() {
				defer running.Done()
				defer func() { <-slots }()
				answerResident(ctx, connection, stopResident)
			}()
		default:
			_ = connection.Close()
		}
		stopMu.Unlock()
	}
}

func answerResident(ctx context.Context, f net.Conn, stopResident func() error) {
	defer f.Close()
	_ = f.SetReadDeadline(time.Now().Add(10 * time.Second))
	var req residentRequest
	line, err := bufio.NewReader(io.LimitReader(f, 1<<20)).ReadBytes('\n')
	if err != nil {
		return
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	response := residentResponse{Version: residentProtocol}
	if req.Version != residentProtocol || len(req.Args) == 0 || req.Args[0] == "resident" {
		response.Code, response.Stderr = 2, "invalid resident request\n"
	} else if req.Args[0] == "__resident-stop" && stopResident != nil {
		if err := stopResident(); err != nil {
			response.Code = 1
			response.Stderr = err.Error() + "\n"
		} else {
			response.Stdout = "resident stopped; launched applications continue\n"
		}
	} else {
		timeout := req.Timeout
		if timeout <= 0 || timeout > 24*time.Hour {
			timeout = 30 * time.Minute
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		exe, err := os.Executable()
		if err != nil {
			return
		}
		command := exec.CommandContext(callCtx, exe, req.Args...)
		command.Dir = req.Directory
		command.Env = append(os.Environ(), "KARAKURI_AUTO_LOCAL=1")
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		out, errout := &limitedOutput{}, &limitedOutput{}
		command.Stdout, command.Stderr = out, errout
		err = command.Run()
		response.Stdout, response.Stderr = out.String(), errout.String()
		response.Code = exitStatus(err)
		if err != nil && response.Stderr == "" {
			response.Stderr = err.Error() + "\n"
		}
	}
	_ = f.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if json.NewEncoder(f).Encode(response) == nil {
		// A named pipe may discard buffered output when its server handle closes.
		// Wait for the client to acknowledge receipt before closing the handle.
		_ = f.SetReadDeadline(time.Now().Add(10 * time.Second))
		var ack [1]byte
		_, _ = io.ReadFull(f, ack[:])
	}
}

func prepareBackgroundProcess(c *exec.Cmd) {}
