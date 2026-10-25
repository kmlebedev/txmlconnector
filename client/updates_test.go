package tcClient

import (
	"context"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/client/commands"
)

const (
	portfolioClientXML     = `<client id="ACC-1" remove="false"><type>1</type><currency>RUB</currency><market>1</market><union>U1</union><forts_acc>F1</forts_acc></client>`
	portfolioSecuritiesXML = `<securities><security secid="1"><seccode>SU1</seccode><board>TQOB</board><lotsize>10</lotsize></security></securities>`
	portfolioPositionsXML  = `<positions><money_position><client>ACC-1</client><union>U1</union><saldo>1000</saldo><ordbuy>10</ordbuy><ordbuycond>5</ordbuycond><currency>RUB</currency></money_position><sec_position><client>ACC-1</client><secid>1</secid><market>1</market><seccode>SU1</seccode><saldo>100</saldo><amount>10100</amount><equity>10100</equity></sec_position><united_limits union="U1"><equity>5000</equity><requirements>1000</requirements><free>4000</free></united_limits></positions>`
	portfolioUnitedXML     = `<united_portfolio union="U1"><equity>5000</equity><asset code="RUB"><security secid="1"><market>1</market><seccode>SU1</seccode><price>101.5</price><balance>100</balance><balance_prc>100.5</balance_prc><unrealized_pnl>100</unrealized_pnl></security></asset></united_portfolio>`
	portfolioLimitsXML     = `<united_limits union="U1"><equity>5000</equity><requirements>1000</requirements><free>4000</free></united_limits>`
)

func TestPortfolioUpdatesReceiveSnapshotsInOrder(t *testing.T) {
	tc := TCClient{
		ctx:              context.Background(),
		ResponseChannel:  make(chan string, 8),
		portfolioUpdates: make(chan PortfolioUpdate, 8),
	}
	for _, message := range []string{
		portfolioClientXML,
		portfolioSecuritiesXML,
		portfolioPositionsXML,
		portfolioUnitedXML,
	} {
		if err := tc.handleMessage(message); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	}

	want := []struct {
		kind PortfolioUpdateKind
		raw  string
	}{
		{UpdateClient, portfolioClientXML},
		{UpdateSecurities, portfolioSecuritiesXML},
		{UpdatePositions, portfolioPositionsXML},
		{UpdateUnitedLimits, portfolioPositionsXML},
		{UpdateUnitedPortfolio, portfolioUnitedXML},
	}
	for i, item := range want {
		select {
		case update := <-tc.PortfolioUpdates():
			if update.Kind != item.kind {
				t.Fatalf("update %d kind = %q, want %q", i, update.Kind, item.kind)
			}
			if update.Raw != item.raw {
				t.Fatalf("update %d raw = %q, want %q", i, update.Raw, item.raw)
			}
			if update.ReceivedAt.IsZero() {
				t.Fatalf("update %d has zero ReceivedAt", i)
			}
			switch item.kind {
			case UpdateClient:
				if update.Client == nil || update.Client.ID != "ACC-1" || update.Client.Currency != "RUB" {
					t.Fatalf("client update = %+v", update.Client)
				}
			case UpdateSecurities:
				if update.Securities == nil || len(update.Securities.Items) != 1 || update.Securities.Items[0].LotSize != 10 {
					t.Fatalf("securities update = %+v", update.Securities)
				}
			case UpdatePositions:
				if update.Positions == nil || len(update.Positions.MoneyPosition) != 1 ||
					update.Positions.MoneyPosition[0].Saldo != 1000 {
					t.Fatalf("positions update = %+v", update.Positions)
				}
			case UpdateUnitedLimits:
				if update.UnitedLimits == nil || update.UnitedLimits.Equity != 5000 ||
					update.UnitedLimits.Requirements != 1000 {
					t.Fatalf("limits update = %+v", update.UnitedLimits)
				}
			case UpdateUnitedPortfolio:
				if update.UnitedPortfolio == nil || len(update.UnitedPortfolio.Asset) != 1 ||
					len(update.UnitedPortfolio.Asset[0].Security) != 1 ||
					update.UnitedPortfolio.Asset[0].Security[0].Balance != 100 {
					t.Fatalf("portfolio update = %+v", update.UnitedPortfolio)
				}
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for update %d (%s)", i, item.kind)
		}
	}
}

func TestPortfolioUpdatesDisabledByDefault(t *testing.T) {
	tc := TCClient{ctx: context.Background(), ResponseChannel: make(chan string, 1)}
	if tc.PortfolioUpdates() != nil {
		t.Fatal("portfolio updates are enabled without PortfolioBuffer")
	}
	if err := tc.handleMessage(portfolioPositionsXML); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
}

func TestPortfolioUpdatesCloseOnStreamEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tc := TCClient{
		ctx:              ctx,
		cancel:           cancel,
		ShutdownChannel:  make(chan bool, 1),
		portfolioUpdates: make(chan PortfolioUpdate, 1),
	}
	stream := &responseStream{
		ctx:         context.Background(),
		messages:    []string{portfolioLimitsXML},
		allReceived: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		tc.loopReadingFromStream(stream)
		close(done)
	}()

	select {
	case update, ok := <-tc.PortfolioUpdates():
		if !ok {
			t.Fatal("portfolio updates closed before the message")
		}
		if update.Kind != UpdateUnitedLimits || update.UnitedLimits == nil || update.UnitedLimits.Equity != 5000 {
			t.Fatalf("update = %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the portfolio update")
	}

	select {
	case _, ok := <-tc.PortfolioUpdates():
		if ok {
			t.Fatal("portfolio updates are not closed after stream EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the portfolio updates to close")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream loop did not stop")
	}
}

func TestCancelUnblocksBlockedPortfolioDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tc := TCClient{
		ctx:              ctx,
		cancel:           cancel,
		ShutdownChannel:  make(chan bool, 1),
		portfolioUpdates: make(chan PortfolioUpdate),
	}
	done := make(chan error, 1)
	go func() {
		done <- tc.handleMessage(portfolioLimitsXML)
	}()

	deadline := time.After(time.Second)
	for {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("handleMessage error = %v, want context.Canceled", err)
			}
			return
		case <-time.After(10 * time.Millisecond):
			cancel()
		case <-deadline:
			t.Fatal("handleMessage stayed blocked after cancellation")
		}
	}
}

func TestConnectUsesConfiguredCredentials(t *testing.T) {
	t.Setenv("TC_LOGIN", "")
	t.Setenv("TC_PASSWORD", "")
	t.Setenv("TC_HOST", "")
	t.Setenv("TC_PORT", "")

	rpc := &clientRPC{commandResponse: `<result success="true"/>`}
	client := &TCClient{
		Client: rpc,
		ctx:    context.Background(),
		options: Options{Connect: &ConnectConfig{
			Login:    "cfg-login",
			Password: "cfg-password",
			Host:     "cfg-host",
			Port:     "12345",
		}},
	}
	if err := client.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	requests := rpc.sentRequests()
	if len(requests) != 1 {
		t.Fatalf("sent requests = %d, want 1", len(requests))
	}
	connect := commands.Connect{}
	if err := xml.Unmarshal([]byte(requests[0]), &connect); err != nil {
		t.Fatalf("decode connect request: %v", err)
	}
	if connect.Login != "cfg-login" || connect.Password != "cfg-password" ||
		connect.Host != "cfg-host" || connect.Port != "12345" {
		t.Fatalf("connect request = %+v", connect)
	}
	if connect.SessionTimeout != 60 || connect.RequestTimeout != 10 ||
		connect.PushUlimits != 30 || connect.PushPosEquity != 30 ||
		connect.Rqdelay != 1000 || !connect.MicexRegisters {
		t.Fatalf("connect defaults = %+v", connect)
	}
}

func TestConnectRequiresConfiguredCredentials(t *testing.T) {
	valid := ConnectConfig{Login: "cfg-login", Password: "cfg-password", Host: "cfg-host", Port: "12345"}
	tests := []struct {
		name    string
		missing string
		mutate  func(*ConnectConfig)
	}{
		{name: "login", missing: "TC_LOGIN", mutate: func(c *ConnectConfig) { c.Login = "" }},
		{name: "password", missing: "TC_PASSWORD", mutate: func(c *ConnectConfig) { c.Password = "" }},
		{name: "host", missing: "TC_HOST", mutate: func(c *ConnectConfig) { c.Host = "" }},
		{name: "port", missing: "TC_PORT", mutate: func(c *ConnectConfig) { c.Port = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			client := &TCClient{ctx: context.Background(), options: Options{Connect: &cfg}}
			err := client.Connect()
			if err == nil || !strings.Contains(err.Error(), test.missing) {
				t.Fatalf("Connect error = %v, want %s required", err, test.missing)
			}
		})
	}
}
