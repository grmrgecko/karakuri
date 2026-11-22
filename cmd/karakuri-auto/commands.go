package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/automation"
)

type commandExit struct{ code int }

func (e commandExit) Error() string { return fmt.Sprintf("command exited with code %d", e.code) }
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var code commandExit
	if errors.As(err, &code) {
		return code.code
	}
	var process *exec.ExitError
	if errors.As(err, &process) && process.ExitCode() >= 0 {
		return process.ExitCode()
	}
	return 1
}

func seconds(value string) (time.Duration, error) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 86400 {
		return 0, fmt.Errorf("duration must be seconds between 0 and 86400")
	}
	return time.Duration(n * float64(time.Second)), nil
}

func sleepCommand(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: sleep <seconds>")
	}
	d, err := seconds(args[0])
	if err != nil {
		return err
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runProcess(ctx context.Context, args []string) error {
	wait := false
	if len(args) > 0 && args[0] == "--wait" {
		wait = true
		args = args[1:]
	}
	if len(args) > 0 && args[len(args)-1] == "--wait" {
		wait = true
		args = args[:len(args)-1]
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: run <exe> [args] [--wait]")
	}
	command := exec.Command(args[0], args[1:]...)
	if wait {
		command = exec.CommandContext(ctx, args[0], args[1:]...)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
	}
	if !wait {
		prepareBackgroundProcess(command)
	}
	if err := command.Start(); err != nil {
		return err
	}
	fmt.Printf("pid=%d\n", command.Process.Pid)
	if !wait {
		return command.Process.Release()
	}
	err := command.Wait()
	code := exitStatus(err)
	fmt.Printf("exit_code=%d\n", code)
	if err != nil {
		return commandExit{code}
	}
	return nil
}

func waitWindow(ctx context.Context, sess *automation.Session, args []string) error {
	if len(args) < 2 || args[0] != "window" {
		return fmt.Errorf("usage: wait window <match> [--timeout seconds] [--gone]")
	}
	match := args[1]
	timeout := 30 * time.Second
	gone := false
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--gone":
			gone = true
		case "--timeout":
			i++
			if i == len(args) {
				return fmt.Errorf("--timeout needs seconds")
			}
			var err error
			timeout, err = seconds(args[i])
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown wait option %q", args[i])
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		windows, err := sess.Window.List(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, w := range windows {
			if strings.Contains(w.ID, match) || strings.Contains(w.Title, match) || strings.Contains(w.Class, match) {
				found = true
				break
			}
		}
		if found != gone {
			fmt.Printf("window=%q gone=%t\n", match, gone)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for window %q (gone=%t): %w", match, gone, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// commandWords preserves Windows path separators; quotes group arguments and
// doubled quotes inside a quoted value represent one literal quote.
func commandWords(line string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	started := false
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			if c == quote {
				if i+1 < len(runes) && runes[i+1] == quote {
					word.WriteRune(c)
					i++
				} else {
					quote = 0
				}
			} else {
				word.WriteRune(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			started = true
		case ' ', '\t':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

// scriptLineCommand returns the arguments one command-file line runs this
// executable with, and the command they name. The executable's own name is
// optional at the start of a line. The command is read past a leading --local,
// which the executable strips before dispatching, so the flag cannot carry a
// nested script or a resident command through.
func scriptLineCommand(words []string) ([]string, string, error) {
	if len(words) > 0 && (words[0] == "karakuri-auto" || words[0] == "karakuri-auto.exe") {
		words = words[1:]
	}
	command := words
	if len(command) > 0 && command[0] == "--local" {
		command = command[1:]
	}
	if len(command) == 0 || command[0] == "script" || command[0] == "resident" {
		return nil, "", fmt.Errorf("nested scripts and resident management are not allowed")
	}
	return words, command[0], nil
}

func scriptCommand(ctx context.Context, args []string) error {
	if len(args) != 1 && !(len(args) == 3 && args[1] == "--log") {
		return fmt.Errorf("usage: script <file> [--log file]")
	}
	file, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer file.Close()
	var output io.Writer = os.Stdout
	if len(args) == 3 {
		if in, e := file.Stat(); e == nil {
			if out, e := os.Stat(args[2]); e == nil && os.SameFile(in, out) {
				return fmt.Errorf("script and log must be different files")
			}
		}
		log, err := os.OpenFile(args[2], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer log.Close()
		output = io.MultiWriter(os.Stdout, log)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		words, err := commandWords(text)
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		if len(words) == 0 {
			continue
		}
		words, name, err := scriptLineCommand(words)
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		fmt.Fprintf(output, "%s line=%d command=%q\n", time.Now().Format(time.RFC3339Nano), line, name)
		command := exec.CommandContext(ctx, exe, words...)
		command.Stdout, command.Stderr = output, output
		err = command.Run()
		fmt.Fprintf(output, "%s line=%d exit_code=%d\n", time.Now().Format(time.RFC3339Nano), line, exitStatus(err))
		if err != nil {
			return commandExit{exitStatus(err)}
		}
	}
	return scanner.Err()
}
