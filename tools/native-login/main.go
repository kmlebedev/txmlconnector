// Command native-login performs the TRANSAQ connect sequence (built-in crypto
// profile) without the vendor DLL and prints the session fields.
//
//	TC_LOGIN=... TC_PASSWORD=... native-login -addr tr1.finam.ru:3900
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"time"

	"github.com/kmlebedev/txmlconnector/internal/session"
)

func main() {
	addr := flag.String("addr", "", "server host:port")
	timeout := flag.Duration("timeout", session.DefaultRequestTimeout, "request timeout")
	flag.Parse()
	login, password := os.Getenv("TC_LOGIN"), os.Getenv("TC_PASSWORD")
	if *addr == "" || login == "" || password == "" {
		fmt.Fprintln(os.Stderr, "usage: TC_LOGIN=... TC_PASSWORD=... native-login -addr host:port")
		os.Exit(2)
	}

	host, _ := os.Hostname()
	userName := ""
	if u, err := user.Current(); err == nil {
		userName = u.Username
	}
	cfg := session.Config{
		Login:          login,
		Password:       password,
		RequestTimeout: *timeout,
		Client: session.ClientInfo{
			ComputerName: host,
			UserName:     userName,
			OS:           runtime.GOOS + "/" + runtime.GOARCH + " ",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2**timeout+5*time.Second)
	defer cancel()
	conn, info, err := session.Dial(ctx, *addr, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "login:", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("%+v\n", *info)
}
