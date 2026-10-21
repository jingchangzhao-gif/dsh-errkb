//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/webshell"
)

// runWeb serves the embedded toolbox UI on the loopback interface and opens
// it in the system browser's app mode. The shell talks to the daemon over the
// local pipe, so the daemon must already be running (the installer sets it up).
func runWeb(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ui --web", flag.ContinueOnError)
	flags.SetOutput(stderr)
	noBrowser := flags.Bool("no-browser", false, "print the URL instead of opening a window")
	port := flags.Int("port", 0, "fixed loopback port (default: the system picks a free one)")
	dsn := flags.String("dsn", os.Getenv("ERRKB_DSN"), "connection string used only when the daemon must be started")
	migrations := flags.String("migrations", "db/migrations", "migration directory used only when the daemon must be started")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := ensureDaemon(ctx, *dsn, *migrations, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "errkb ui: %v\n", err)
		return exitUnavailable
	}
	defer client.Close()

	server, err := webshell.New(func(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
		return client.Search(ctx, request)
	})
	if err != nil {
		fmt.Fprintf(stderr, "errkb ui: %v\n", err)
		return exitInternal
	}
	listener, err := server.Listen(*port)
	if err != nil {
		fmt.Fprintf(stderr, "errkb ui: listen on loopback: %v\n", err)
		return exitInternal
	}
	url := server.URL(listener)
	fmt.Fprintf(stdout, "toolbox: %s\n", url)

	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpServer.Serve(listener) }()
	if !*noBrowser {
		openToolboxWindow(url)
	}
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	return exitOK
}

// ensureDaemon reuses a running daemon or starts one detached, then waits for
// the pipe. The daemon outlives the window, so closing the toolbox keeps capture.
func ensureDaemon(ctx context.Context, dsn, migrations string, stderr io.Writer) (*service.Client, error) {
	client, err := service.Dial(ctx)
	if err == nil {
		return client, nil
	}
	fmt.Fprintf(stderr, "errkb ui: the daemon is not running; starting it\n")
	command := exec.Command(os.Args[0], "daemon", "--dsn", dsn, "--migrations", migrations)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if startErr := command.Start(); startErr != nil {
		return nil, fmt.Errorf("start the daemon: %w", startErr)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if client, err = service.Dial(ctx); err == nil {
			return client, nil
		}
	}
	return nil, fmt.Errorf("the daemon did not become reachable; run `errkb daemon` manually to read its error")
}

// openToolboxWindow prefers an app-mode Edge window (no address bar, no tabs).
func openToolboxWindow(url string) {
	if edge, err := exec.LookPath("msedge"); err == nil {
		if err := exec.Command(edge, "--app="+url).Start(); err == nil {
			return
		}
	}
	_ = exec.Command("cmd", "/c", "start", "", url).Start()
}
