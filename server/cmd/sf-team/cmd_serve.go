package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func runServe(args []string) int {
	args = reorderFlags(args, map[string]bool{})
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 7890, "port")
	host := fs.String("host", "127.0.0.1", "bind host")
	itemsDir := fs.String("items-dir", "docs/backlog/items", "items directory to watch")
	_ = fs.Parse(args)

	if err := ensureHome(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	cfg, err := loadOrInitConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	dbp, err := dbPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	sqldb, err := openDB(dbp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	defer func() { _ = sqldb.Close() }()

	d := newDB(sqldb)
	srv := newServer(d, serverConfig{Token: cfg.Token, Host: *host, Port: *port, ItemsDir: *itemsDir})

	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	httpSrv := &http.Server{Addr: addr, Handler: srv.handler(), ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if st, err := os.Stat(srv.cfg.ItemsDir); err == nil && st.IsDir() {
		go watchItems(ctx, srv.cfg.ItemsDir, srv.bus)
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	fmt.Printf("sf-team serve: http://%s  (token: %s)\n", addr, cfg.Token)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}
