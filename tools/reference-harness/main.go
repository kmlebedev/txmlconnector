// Command reference-harness loads the TRANSAQ DLL (Windows build; run under
// Wine on macOS/Linux), sends XML commands and prints results and callbacks
// with monotonic timestamps. Without arguments it sends offline commands only.
//
//	harness.exe [-wait 20s] '<command id="connect">...</command>' ...
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kmlebedev/txmlconnector/connector"
)

func main() {
	wait := flag.Duration("wait", time.Second, "time to keep receiving callbacks after the last command")
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

	c, err := connector.New(connector.ConfigFromEnv())
	if err != nil {
		fmt.Println("new:", err)
		os.Exit(1)
	}
	if err := c.Start(func(m string) { fmt.Println(stamp(), "CALLBACK:", m) }); err != nil {
		fmt.Println("start:", err)
		os.Exit(1)
	}
	defer c.Close()
	for _, cmd := range cmds {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		r, err := c.SendCommand(ctx, cmd)
		cancel()
		fmt.Printf("%s CMD: %s -> %s err=%v\n", stamp(), cmd, r, err)
	}
	time.Sleep(*wait)
}
