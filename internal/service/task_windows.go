//go:build windows

package service

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/capnspacehook/taskmaster"
	"github.com/rickb777/date/period"
	"golang.org/x/sys/windows"
)

// taskScoped reports whether the definition installs as a logon scheduled
// task rather than a Windows service. A user-scope install has nowhere else
// to go here: services have no per-user form, and they run outside the
// desktop session whatever account they use, so the agent's desktop and GUI
// nodes would reach nothing from one.
func taskScoped(def Definition) bool {
	return def.UserService
}

// userTaskControl dispatches one service action against the Task Scheduler,
// so `service install` and friends behave the same whichever manager the
// scope resolved to.
func userTaskControl(def Definition, action string) error {
	svc, err := taskmaster.Connect()
	if err != nil {
		return fmt.Errorf("connecting to the task scheduler: %w", err)
	}
	defer svc.Disconnect()

	path := `\` + def.Name

	switch action {
	case "install":
		err = installUserTask(&svc, def, path)
	case "uninstall":
		// Deleting a task leaves a running instance running, so end it first.
		// A task that is not running answers Stop with nothing to do.
		if task, lookupErr := svc.GetRegisteredTask(path); lookupErr == nil {
			_ = task.Stop()
			task.Release()
		}
		err = svc.DeleteTask(path)
	case "start":
		err = runUserTask(&svc, path)
	case "stop":
		err = stopUserTask(&svc, path)
	case "restart":
		if err = stopUserTask(&svc, path); err == nil {
			err = runUserTask(&svc, path)
		}
	case "status":
		return userTaskStatus(&svc, def, path)
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s %s complete.\n", def.Name, action)
	return nil
}

// installUserTask registers the logon task: this binary with the definition's
// arguments, run as the installing user in their interactive session whenever
// they log on. Registering a task for yourself at least privilege needs no
// elevation, which is the point of the user scope.
func installUserTask(svc *taskmaster.TaskService, def Definition, path string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("resolving the current user: %w", err)
	}

	d := svc.NewTaskDefinition()
	d.RegistrationInfo.Description = def.Description
	d.AddAction(taskmaster.ExecAction{Path: exe, Args: joinArgs(def.Arguments)})
	d.AddTrigger(taskmaster.LogonTrigger{
		TaskTrigger: taskmaster.TaskTrigger{Enabled: true},
		UserID:      sid,
	})

	// The principal is the installing user's interactive token at least
	// privilege: the task runs inside their desktop session, unelevated, the
	// counterpart of a systemd --user unit.
	d.Principal.UserID = sid
	d.Principal.LogonType = taskmaster.TASK_LOGON_INTERACTIVE_TOKEN
	d.Principal.RunLevel = taskmaster.TASK_RUNLEVEL_LUA

	// The library's defaults describe a batch job. This is a daemon: it must
	// not stop on battery, on idle end, or at the 72-hour execution limit
	// (zero renders as P0D, which the scheduler reads as no limit), and a
	// crash restarts it rather than waiting for the next logon. Priority 7
	// would run it below normal; 5 is the normal process class, which is
	// where a process driving the desktop belongs.
	d.Settings.DontStartOnBatteries = false
	d.Settings.StopIfGoingOnBatteries = false
	d.Settings.StopOnIdleEnd = false
	d.Settings.TimeLimit = period.Period{}
	d.Settings.RestartCount = 999
	d.Settings.RestartInterval = period.NewHMS(0, 1, 0)
	d.Settings.MultipleInstances = taskmaster.TASK_INSTANCES_IGNORE_NEW
	d.Settings.Priority = 5

	_, _, err = svc.CreateTask(path, d, true)
	return err
}

// runUserTask starts the registered task now, in the session it is bound to.
func runUserTask(svc *taskmaster.TaskService, path string) error {
	task, err := svc.GetRegisteredTask(path)
	if err != nil {
		return fmt.Errorf("looking up scheduled task %s: %w", path, err)
	}
	defer task.Release()

	running, err := task.Run()
	if err != nil {
		return err
	}
	running.Release()
	return nil
}

// stopUserTask ends every running instance of the task.
func stopUserTask(svc *taskmaster.TaskService, path string) error {
	task, err := svc.GetRegisteredTask(path)
	if err != nil {
		return fmt.Errorf("looking up scheduled task %s: %w", path, err)
	}
	defer task.Release()
	return task.Stop()
}

// userTaskStatus prints what the scheduler reports, in the same phrasing the
// service managers get. The COM interface reports a missing task only as an
// error, so a failed lookup reads as not installed; asking is how an operator
// finds out.
func userTaskStatus(svc *taskmaster.TaskService, def Definition, path string) error {
	task, err := svc.GetRegisteredTask(path)
	if err != nil {
		fmt.Printf("%s is not installed.\n", def.Name)
		return nil
	}
	defer task.Release()

	switch task.State {
	case taskmaster.TASK_STATE_RUNNING:
		fmt.Printf("%s is running.\n", def.Name)
	case taskmaster.TASK_STATE_READY, taskmaster.TASK_STATE_QUEUED:
		fmt.Printf("%s is stopped.\n", def.Name)
	case taskmaster.TASK_STATE_DISABLED:
		fmt.Printf("%s is installed but disabled.\n", def.Name)
	default:
		fmt.Printf("%s is in an unknown state.\n", def.Name)
	}
	return nil
}

// currentUserSID identifies the installing user to the scheduler. The SID
// rather than the account name, so a rename cannot orphan the task.
func currentUserSID() (string, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return tu.User.Sid.String(), nil
}

// joinArgs renders the arguments as the single command-line string an exec
// action takes, quoted the way CreateProcess splits.
func joinArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	return strings.Join(quoted, " ")
}
