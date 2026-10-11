package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	interval = 10 * time.Second
	cmdPath  = "./cmd"
	appName  = "instera"
)

type Watcher struct {
	root   string
	remote string
	branch string
	binary string
	app    *exec.Cmd
}

func command(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf(
			"%s %s: %w\n%s",
			name, strings.Join(args, " "), err, out,
		)
	}
	return out, nil
}

func git(ctx context.Context, root string, args ...string) (string, error) {
	out, err := command(ctx, root, "git", args...)
	return strings.TrimSpace(string(out)), err
}

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	root, err = filepath.Abs(root)
	if err != nil {
		fatal(err)
	}

	upstream, err := git(
		ctx, root,
		"rev-parse",
		"--abbrev-ref",
		"--symbolic-full-name",
		"@{u}",
	)
	if err != nil {
		fatal(fmt.Errorf(
			"configure a Git upstream first, e.g. git push -u origin main: %w",
			err,
		))
	}

	parts := strings.SplitN(upstream, "/", 2)
	if len(parts) != 2 {
		fatal(fmt.Errorf("invalid upstream: %s", upstream))
	}

	binaryName := appName
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	w := &Watcher{
		root:   root,
		remote: parts[0],
		branch: parts[1],
		binary: filepath.Join(root, "bin", binaryName),
	}

	if err := w.buildAndRestart(ctx); err != nil {
		fatal(err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf(
		"Watching %s/%s every %s\n",
		w.remote, w.branch, interval,
	)

	for {
		select {
		case <-ctx.Done():
			w.stopApp()
			fmt.Println("Watcher stopped")
			return

		case <-ticker.C:
			if err := w.check(ctx); err != nil {
				fmt.Printf("[watch] %v\n", err)
			}
		}
	}
}

func (w *Watcher) check(ctx context.Context) error {
	fmt.Println("[git] Fetching updates...")

	if _, err := git(
		ctx, w.root,
		"fetch", "--prune", w.remote, w.branch,
	); err != nil {
		return err
	}

	local, err := git(ctx, w.root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	remoteRef := w.remote + "/" + w.branch
	remote, err := git(ctx, w.root, "rev-parse", remoteRef)
	if err != nil {
		return err
	}

	if local == remote {
		fmt.Println("[git] Already up to date")
		return nil
	}

	if _, err := git(
		ctx, w.root,
		"merge-base", "--is-ancestor", "HEAD", remoteRef,
	); err != nil {
		return fmt.Errorf(
			"branches diverged or local history differs; refusing automatic update",
		)
	}

	if _, err := git(
		ctx, w.root,
		"merge", "--ff-only", remoteRef,
	); err != nil {
		return err
	}

	fmt.Println("[git] Updated successfully")
	return w.buildAndRestart(ctx)
}

func (w *Watcher) buildAndRestart(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(w.binary), 0755); err != nil {
		return err
	}

	fmt.Println("[build] Building application...")

	if _, err := command(
		ctx, w.root,
		"go", "build", "-o", w.binary, cmdPath,
	); err != nil {
		return err
	}

	w.stopApp()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	fmt.Println("[app] Starting application...")

	w.app = exec.Command(w.binary)
	w.app.Dir = w.root
	w.app.Stdout = os.Stdout
	w.app.Stderr = os.Stderr
	w.app.Stdin = os.Stdin

	if err := w.app.Start(); err != nil {
		w.app = nil
		return err
	}

	go func(app *exec.Cmd) {
		err := app.Wait()
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Printf("[app] Process exited: %v\n", err)
		} else {
			fmt.Println("[app] Process exited")
		}
		if w.app == app {
			w.app = nil
		}
	}(w.app)

	return nil
}

func (w *Watcher) stopApp() {
	if w.app == nil || w.app.Process == nil {
		return
	}

	fmt.Println("[app] Stopping previous process...")
	_ = w.app.Process.Kill()
	w.app = nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}
