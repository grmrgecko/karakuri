//go:build windows

package exec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// treePlatform holds the job object that contains the command and every
// process it starts. A child cannot leave a job unless the job allows
// breakaway, and this one does not. job is guarded by processTree.mu, since
// cancellation runs on the goroutine os/exec uses to watch the context.
type treePlatform struct {
	cmd *exec.Cmd
	job windows.Handle
}

// newProcessTree terminates the command's job object when a context cancels
// it. taskkill's tree mode and Process.Kill remain the fallback for a command
// the job could not contain, such as one launched inside a job that refuses
// nesting.
func newProcessTree(cmd *exec.Cmd, rawGrace string) (*processTree, error) {
	if _, err := processTerminationGrace(rawGrace); err != nil {
		return nil, err
	}
	tree := &processTree{treePlatform: treePlatform{cmd: cmd}}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// The process starts suspended so it cannot start a child before it is in
	// the job; run resumes it once it is assigned.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.Cancel = func() error {
		tree.markCanceled()
		if cmd.Process == nil {
			return nil
		}
		return tree.terminate()
	}
	cmd.WaitDelay = 5 * time.Second
	return tree, nil
}

// run starts the command inside its job, resumes it, and waits for it. The
// job handle closes after Wait without killing anything, so a process the
// command started to outlive it keeps running, as it would on Unix.
func (t *processTree) run() error {
	if err := t.cmd.Start(); err != nil {
		return err
	}
	pid := uint32(t.cmd.Process.Pid)
	// A command the job cannot hold still runs; terminate falls back to
	// taskkill for it.
	if job, err := assignJob(pid); err == nil {
		t.mu.Lock()
		t.job = job
		t.mu.Unlock()
		defer func() {
			t.mu.Lock()
			t.job = 0
			t.mu.Unlock()
			windows.CloseHandle(job)
		}()
	}
	if err := resumeProcess(pid); err != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
		return fmt.Errorf("resuming process: %w", err)
	}
	return t.cmd.Wait()
}

// terminate ends the job, or the tree and then the parent when there is no
// job. A taskkill failure is reported even when killing the parent succeeds,
// because the descendants it should have reached may still be running.
func (t *processTree) terminate() error {
	t.mu.Lock()
	job := t.job
	var err error
	if job != 0 {
		err = windows.TerminateJobObject(job, 1)
	}
	t.mu.Unlock()
	if job != 0 {
		if err == nil {
			return nil
		}
		t.record(fmt.Errorf("terminating job object: %w", err))
	}
	if err := exec.Command("taskkill", "/PID", fmt.Sprint(t.cmd.Process.Pid), "/T", "/F").Run(); err != nil {
		t.record(fmt.Errorf("taskkill: %w", err))
	}
	if err := t.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return t.record(fmt.Errorf("killing process: %w", err))
	}
	return nil
}

// assignJob creates a job object and places the process in it.
func assignJob(pid uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// resumeProcess resumes the threads of a process created suspended, which has
// only its initial thread. os.StartProcess closes the thread handle
// CreateProcess returns, so the thread is found through a snapshot.
func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := false
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if err != nil {
			return err
		}
		resumed = true
	}
	if !resumed {
		return fmt.Errorf("no thread found for process %d", pid)
	}
	return nil
}
