package sandboxagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const JobsDir = "/run/cellar-jobs"

// RunJobCLI dispatches `cellar-agent job …` subcommands. Returns whether the
// args were handled as a job command (true) so the caller can skip PID-1 mode.
func RunJobCLI(args []string) (handled bool, err error) {
	if len(args) < 1 || args[0] != "job" {
		return false, nil
	}
	if len(args) < 2 {
		return true, fmt.Errorf("usage: cellar-agent job <start|stop|status|logs> …")
	}
	switch args[1] {
	case "start":
		return true, jobStart(args[2:])
	case "stop":
		return true, jobStop(args[2:])
	case "status":
		return true, jobStatus(args[2:])
	case "logs":
		return true, jobLogs(args[2:])
	default:
		return true, fmt.Errorf("unknown job subcommand %q", args[1])
	}
}

func jobStart(args []string) error {
	var id string
	var cmdArgs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			if i+1 >= len(args) {
				return fmt.Errorf("--id requires a value")
			}
			id = args[i+1]
			i++
		case "--":
			cmdArgs = args[i+1:]
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %q", args[i])
			}
			cmdArgs = args[i:]
			i = len(args)
		}
	}
	if id == "" {
		return fmt.Errorf("--id is required")
	}
	if len(cmdArgs) == 0 {
		return fmt.Errorf("command required after --")
	}
	if err := os.MkdirAll(JobsDir, 0o755); err != nil {
		return err
	}

	outPath := filepath.Join(JobsDir, id+".out")
	errPath := filepath.Join(JobsDir, id+".err")
	pidPath := filepath.Join(JobsDir, id+".pid")

	outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer outFile.Close()
	errFile, err := os.OpenFile(errPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer errFile.Close()

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	_ = os.WriteFile(filepath.Join(JobsDir, id+".exit"), []byte(strconv.Itoa(exitCode)+"\n"), 0o644)
	_ = os.Remove(pidPath)
	return nil
}

func jobStop(args []string) error {
	id, signalName, timeout := "", "TERM", 10*time.Second
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--signal":
			if i+1 >= len(args) {
				return fmt.Errorf("--signal requires a value")
			}
			signalName = strings.TrimPrefix(strings.ToUpper(args[i+1]), "SIG")
			i++
		case "--timeout":
			if i+1 >= len(args) {
				return fmt.Errorf("--timeout requires a value")
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				sec, err2 := strconv.Atoi(args[i+1])
				if err2 != nil {
					return fmt.Errorf("invalid --timeout: %w", err)
				}
				d = time.Duration(sec) * time.Second
			}
			timeout = d
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %q", args[i])
			}
			if id == "" {
				id = args[i]
			} else {
				return fmt.Errorf("unexpected argument %q", args[i])
			}
		}
	}
	if id == "" {
		return fmt.Errorf("job id required")
	}
	pid, err := readPID(id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already gone
		}
		return err
	}
	sig := syscall.SIGTERM
	if signalName == "KILL" {
		sig = syscall.SIGKILL
	}
	// Negative PID signals the process group created by Setsid.
	_ = syscall.Kill(-pid, sig)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			_ = os.Remove(filepath.Join(JobsDir, id+".pid"))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = os.Remove(filepath.Join(JobsDir, id+".pid"))
	return nil
}

func jobStatus(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: cellar-agent job status <id>")
	}
	id := args[0]
	pidPath := filepath.Join(JobsDir, id+".pid")
	if _, err := os.Stat(pidPath); err == nil {
		pid, err := readPID(id)
		if err != nil {
			fmt.Printf("status=unknown\n")
			return nil
		}
		if err := syscall.Kill(pid, 0); err == nil {
			fmt.Printf("status=running pid=%d\n", pid)
			return nil
		}
	}
	exitPath := filepath.Join(JobsDir, id+".exit")
	if b, err := os.ReadFile(exitPath); err == nil {
		fmt.Printf("status=exited exit_code=%s\n", strings.TrimSpace(string(b)))
		return nil
	}
	fmt.Printf("status=unknown\n")
	return nil
}

func jobLogs(args []string) error {
	follow := false
	var id string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--follow", "-f":
			follow = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %q", args[i])
			}
			id = args[i]
		}
	}
	if id == "" {
		return fmt.Errorf("job id required")
	}
	outPath := filepath.Join(JobsDir, id+".out")
	errPath := filepath.Join(JobsDir, id+".err")
	if follow {
		return followFiles(outPath, errPath)
	}
	if b, err := os.ReadFile(outPath); err == nil {
		_, _ = os.Stdout.Write(b)
	}
	if b, err := os.ReadFile(errPath); err == nil {
		_, _ = os.Stderr.Write(b)
	}
	return nil
}

func followFiles(outPath, errPath string) error {
	out, err := os.OpenFile(outPath, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	er, err := os.OpenFile(errPath, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer er.Close()

	buf := make([]byte, 4096)
	for {
		n, _ := out.Read(buf)
		if n > 0 {
			_, _ = os.Stdout.Write(buf[:n])
		}
		n, _ = er.Read(buf)
		if n > 0 {
			_, _ = os.Stderr.Write(buf[:n])
		}
		// Stop following once the job has exited and both streams are drained.
		if _, err := os.Stat(filepath.Join(filepath.Dir(outPath), strings.TrimSuffix(filepath.Base(outPath), ".out")+".pid")); os.IsNotExist(err) {
			// Final drain
			for {
				n, _ := out.Read(buf)
				if n == 0 {
					break
				}
				_, _ = os.Stdout.Write(buf[:n])
			}
			for {
				n, _ := er.Read(buf)
				if n == 0 {
					break
				}
				_, _ = os.Stderr.Write(buf[:n])
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func readPID(id string) (int, error) {
	b, err := os.ReadFile(filepath.Join(JobsDir, id+".pid"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// RunInit is the PID-1 path: reap zombies until signaled.
func RunInit() error {
	reapStop := make(chan struct{})
	go ReapZombies(reapStop)
	defer close(reapStop)

	<-NotifyShutdown()
	return nil
}
