package native

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// pendingCommands is empty: all 45 DLL commands are handled (T19 completed the
// last two, moveorder/makeordown) and must not answer not-implemented.
var pendingCommands = map[string]bool{}

// TestEveryDLLCommandIsHandled: all 45 DLL command names are either handled
// (DLL no-connection/after-connect behavior) or in the explicit pending list.
func TestEveryDLLCommandIsHandled(t *testing.T) {
	if len(dllCommands) != 45 {
		t.Fatalf("dllCommands = %d names, want 45", len(dllCommands))
	}
	c := New(Config{})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// (a) Without a connection: the DLL "Cannot process..." behavior for every
	// name except the lifecycle commands and pending crypto-provider hooks.
	for name, needsConn := range dllCommands {
		cmd := "<command id=" + quote + name + quote + "/>"
		switch {
		case name == "connect":
			if got := send(t, c, cmd); !strings.Contains(got, "host, port and login are required") {
				t.Fatalf("connect without args = %s", got)
			}
		case name == "disconnect":
			if got := send(t, c, cmd); !strings.Contains(got, "Соединение не установлено") {
				t.Fatalf("disconnect without connection = %s", got)
			}
		case name == "set_csp":
			// No csp element = no-op success (D+O).
			if got := send(t, c, cmd); !strings.Contains(got, "success="+quote+"true"+quote) {
				t.Fatalf("set_csp without csp = %s", got)
			}
		case name == "authenticate":
			// Default provider: Illegal context for sendpin, no connection needed.
			if got := send(t, c, cmd); !strings.Contains(got, "Illegal context for") {
				t.Fatalf("authenticate without connection = %s", got)
			}
		case needsConn:
			// Pending commands are still DLL commands: without a connection the
			// DLL answers the connection diagnostic (the pending check is the
			// after-connect "not implemented" part, below).
			want := "<result success=" + quote + "false" + quote + "><message>" + msgNeedsConnection + "</message></result>"
			if got := send(t, c, cmd); got != want {
				t.Fatalf("%s without connection = %s, want %s", name, got, want)
			}
		case pendingCommands[name]:
			if got := send(t, c, cmd); !strings.Contains(got, msgNotImplemented) {
				t.Fatalf("%s pending = %s", name, got)
			}
		default:
			// get_connector_version: local command, no connection required.
			if got := send(t, c, cmd); !strings.Contains(got, "success") {
				t.Fatalf("%s local = %s", name, got)
			}
		}
	}
}

// TestEveryDLLCommandAfterConnect: after connect, every non-pending name answers
// something other than the not-implemented/unknown-command diagnostic. Commands
// needing args answer their DLL local errors; network ones get a synthetic
// empty-success reply from the testserver.
func TestEveryDLLCommandAfterConnect(t *testing.T) {
	srv := newRunServer(t)
	srv.Startup = readFixture(t, "startup-demo.hex")
	// Register empty-success replies for request types the commands may send
	// after the connect (the generic command has no arguments, so most fail
	// locally before any network access).
	for _, typ := range []byte{
		session.RqSubscribe, session.RqClientHoldings, session.RqClientLimits,
		session.RqHistoryExt, session.RqTransact, session.RqSecSpec,
		session.RqGetNews, session.RqOptionFamilies, session.RqFamilyStrikes,
		session.RqFamiliyOptions, session.RqCertainOptions, session.RqTicks,
	} {
		srv.Handle(typ, func([]byte) (byte, []byte) {
			return wire.KindReply | wire.FlagPlain, nil
		})
	}
	c := New(Config{Dial: srv.Dial})
	rec := newRecorder()
	if err := c.Start(rec.handle); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, connectCommand())
	rec.next(t, "<server_status")

	for name := range dllCommands {
		if name == "connect" || name == "disconnect" {
			continue // lifecycle covered by the no-connection part / other tests
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		got, err := c.SendCommand(ctx, "<command id="+quote+name+quote+"/>")
		cancel()
		if err != nil {
			t.Fatalf("%s after connect: %v", name, err)
		}
		if pendingCommands[name] {
			if !strings.Contains(got, msgNotImplemented) {
				t.Fatalf("%s pending after connect = %s", name, got)
			}
			continue
		}
		if strings.Contains(got, msgNotImplemented) || strings.Contains(got, "Unknown command.") {
			t.Fatalf("%s after connect = %s, want a real DLL answer", name, got)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("%s after connect returned an empty result", name)
		}
	}
}

// expectedDLLCommands is the plan's 45-name table (task 29; task-index §8.1).
var expectedDLLCommands = []string{
	"set_csp", "connect", "authenticate", "disconnect", "gethistorydata",
	"subscribe", "unsubscribe", "makeordown", "neworder", "moveorder",
	"change_pass", "newcondorder", "newstoporder", "newrpsorder", "newrepoorder",
	"newmrepoorder", "cancelorder", "cancelstoporder", "cancelnegdeal",
	"newreport4", "cancelreport4", "get_forts_positions", "get_client_limits",
	"get_markets", "server_status", "get_securities", "get_servtime_difference",
	"get_portfolio", "get_portfolio_mct", "get_united_portfolio",
	"get_mc_portfolio", "get_max_buy_sell_tplus", "get_max_buy_sell",
	"subscribe_ticks", "get_old_news", "get_news_body", "get_server_id",
	"get_connector_version", "get_securities_info", "get_united_equity",
	"get_united_go", "get_cln_sec_permissions", "get_options",
	"get_option_families", "get_family_strikes",
}

// TestDLLCommandCountAgainstDocs pins the whole 45-name table: every planned
// name is dispatched, no extra names exist, and commands.md keeps the two
// historical outliers documented (T28/T29, task-index §8.1).
func TestDLLCommandCountAgainstDocs(t *testing.T) {
	if len(dllCommands) != 45 {
		t.Fatalf("dllCommands = %d names, want 45", len(dllCommands))
	}
	if len(expectedDLLCommands) != 45 {
		t.Fatalf("expected list = %d names, want 45", len(expectedDLLCommands))
	}
	seen := map[string]bool{}
	for _, name := range expectedDLLCommands {
		if _, ok := dllCommands[name]; !ok {
			t.Errorf("dllCommands is missing %s", name)
		}
		seen[name] = true
	}
	for name := range dllCommands {
		if !seen[name] {
			t.Errorf("dllCommands has an unknown name %q", name)
		}
	}
	raw, err := os.ReadFile("../docs/protocol/commands.md")
	if err != nil {
		t.Skipf("commands.md not available: %v", err)
	}
	text := string(raw)
	for _, name := range []string{"newreport4", "cancelreport4"} {
		if !strings.Contains(text, name) {
			t.Errorf("commands.md does not mention %s (45-name table)", name)
		}
	}
}
