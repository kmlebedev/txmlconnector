// Command reference-harness sends XML commands to a connector and prints
// results and callbacks with monotonic timestamps. The default backend is the
// TRANSAQ DLL (Windows build; run under Wine on macOS/Linux); -backend native
// runs the same script against the Go implementation for comparison. Without
// arguments it sends offline commands only. An argument "sleep:<duration>"
// pauses between commands.
//
//	harness.exe [-wait 20s] '<command id="connect">...</command>' sleep:10s ...
//	go run ./tools/reference-harness -backend native '<command id="connect">...</command>' ...
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kmlebedev/txmlconnector/connector"
	"github.com/kmlebedev/txmlconnector/native"
)

func main() {
	wait := flag.Duration("wait", time.Second, "time to keep receiving callbacks after the last command")
	backend := flag.String("backend", "dll", "connector backend: dll or native")
	flag.Parse()
	cmds := flag.Args()
	if len(cmds) == 0 {
		cmds = []string{
			`<command id="get_connector_version"/>`,
			`<command id="bogus"/>`,
		}
	}

	start := time.Now()
	stamp := func() string { return fmt.Sprintf("%9.3f", time.Since(start).Seconds()) }

	var c connector.Connector
	switch *backend {
	case "dll":
		var err error
		if c, err = connector.New(connector.ConfigFromEnv()); err != nil {
			fmt.Println("new:", err)
			os.Exit(1)
		}
	case "native":
		c = native.New(native.Config{})
	default:
		fmt.Println("unknown backend", *backend)
		os.Exit(2)
	}
	if err := c.Start(func(m string) { fmt.Println(stamp(), "CALLBACK:", m) }); err != nil {
		fmt.Println("start:", err)
		os.Exit(1)
	}
	defer c.Close()
	for _, cmd := range cmds {
		if d, ok := strings.CutPrefix(cmd, "sleep:"); ok {
			pause, err := time.ParseDuration(d)
			if err != nil {
				fmt.Println("sleep:", err)
				os.Exit(2)
			}
			time.Sleep(pause)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		r, err := c.SendCommand(ctx, cmd)
		cancel()
		fmt.Printf("%s CMD: %s -> %s err=%v\n", stamp(), cmd, r, err)
	}
	time.Sleep(*wait)
}
