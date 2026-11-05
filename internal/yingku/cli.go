package yingku

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

const appVersion = "0.13.17"

// Run starts the Yingku command.
func Run() {
	options, err := parseRuntimeOptions(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	if options.Healthcheck {
		if err := serviceHealth(options.Address); err != nil {
			log.Fatal(err)
		}
		return
	}
	addr, data, open := &options.Address, &options.DataDir, &options.OpenBrowser
	listener, err := listenService(*addr)
	if err != nil {
		if isAddressInUse(err) {
			running, probeErr := runningInstance(*addr, *data)
			if running {
				fmt.Printf("映库已在运行，打开已有服务：%s\n", browserServiceURL(*addr))
				if *open {
					openBrowser(browserServiceURL(*addr))
				}
				return
			}
			if errors.Is(probeErr, errOtherLibrary) {
				log.Fatalf("另一个媒体库的映库正在使用 %s，请用 -addr 为当前媒体库指定一个未被使用的本机端口。", *addr)
			}
			log.Fatalf("端口 %s 已被其他程序占用，无法启动映库。请关闭占用端口的程序，或用 -addr 指定一个未被使用的本机端口。", *addr)
		}
		log.Fatal("启动失败：", err)
	}
	defer listener.Close()
	app, err := newAppWithOptions(*data, appOptions{MediaDir: options.MediaDir, PublicURL: options.PublicURL})
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Handler: app.handler(), ReadHeaderTimeout: 8 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	app.listenAddress = listener.Addr().String()
	go app.startWorkers(ctx)
	go app.backfillDetails(ctx)
	go func() {
		<-ctx.Done()
		app.mu.Lock()
		app.closing = true
		for _, stop := range app.active {
			stop()
		}
		app.mu.Unlock()
		shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = server.Shutdown(shutdown)
		app.closeNetwork()
	}()
	fmt.Printf("\n  映库 YINGKU · %s\n  监听地址：%s\n  本机地址：%s\n  数据目录：%s\n  按 Ctrl+C 停止。\n\n", appVersion, listener.Addr(), browserServiceURL(listener.Addr().String()), app.dir)
	fmt.Printf("  默认下载目录：%s\n", app.s.Config.MediaRoot)
	if app.publicURL != "" {
		fmt.Printf("  部署访问地址：%s\n", app.publicURL)
	}
	for _, address := range lanServiceURLs(listener.Addr().String()) {
		fmt.Printf("  手机地址：%s\n", address)
	}
	if *open {
		openBrowser(browserServiceURL(listener.Addr().String()))
	}
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	// Let cancelled workers flush their checkpoints before the process exits.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		n := len(app.active)
		app.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}
func openBrowser(address string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.Command("open", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait()
	}
}
