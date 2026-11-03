// Command native-login runs a TRANSAQ session without the vendor DLL: login,
// RQ_TIME, RQ_STARTUP, RQ_MAIN polling until the server queue is drained,
// then RQ_LOGOUT. It prints the session fields and load statistics.
//
//	TC_LOGIN=... TC_PASSWORD=... native-login -addr tr1.finam.ru:3900
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kmlebedev/txmlconnector/native"
	"github.com/kmlebedev/txmlconnector/native/session"
)

func main() {
	addr := flag.String("addr", "", "server host:port")
	timeout := flag.Duration("timeout", session.DefaultRequestTimeout, "request timeout")
	load := flag.Bool("load", true, "load initial data before logout")
	maxPolls := flag.Int("max-polls", 5000, "RQ_MAIN poll limit while loading")
	flag.Parse()
	login, password := os.Getenv("TC_LOGIN"), os.Getenv("TC_PASSWORD")
	if *addr == "" || login == "" || password == "" {
		fmt.Fprintln(os.Stderr, "usage: TC_LOGIN=... TC_PASSWORD=... native-login -addr host:port")
		os.Exit(2)
	}
	if err := run(*addr, login, password, *timeout, *load, *maxPolls); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(addr, login, password string, timeout time.Duration, load bool, maxPolls int) error {
	cfg := session.Config{
		Login:          login,
		Password:       password,
		RequestTimeout: timeout,
		Client:         native.DefaultClientInfo(),
	}

	ctx := context.Background()
	start := time.Now()
	conn, info, err := session.Dial(ctx, addr, cfg)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	fmt.Printf("login ok in %v: UID=%d SID=%d SYS=%d BLD=%d PRO=%d session_timeout=%d\n",
		time.Since(start).Round(time.Millisecond), info.UID, info.SID, info.SysVersion, info.Build, info.Protocol, info.SessionTimeout)
	if !load {
		return conn.Logout(ctx)
	}

	st, err := conn.Time(ctx)
	if err != nil {
		conn.Close()
		return err
	}
	fmt.Printf("server time %s (zone % x)\n", st.Wall.Format(time.DateTime), st.Zone[:4])

	startup, err := conn.Startup(ctx)
	if err != nil {
		conn.Close()
		return err
	}
	fmt.Printf("RQ_STARTUP: %d bytes\n", len(startup))

	polls, total := 0, 0
	for polls < maxPolls {
		r, err := conn.Main(ctx)
		if err != nil {
			conn.Close()
			return err
		}
		polls++
		total += len(r.Payload)
		if !r.More {
			break
		}
	}
	fmt.Printf("RQ_MAIN: %d polls, %d bytes, queue drained in %v\n", polls, total, time.Since(start).Round(time.Millisecond))

	if err := conn.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	fmt.Println("logout sent")
	return nil
}
