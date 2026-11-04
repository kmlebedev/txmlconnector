package natsout

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/proto/marketdata"
)

type published struct {
	subject string
	data    []byte
}

type fakePublisher struct {
	msgs []published
	err  error
}

func (f *fakePublisher) Publish(subject string, data []byte) error {
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, published{subject, append([]byte(nil), data...)})
	return nil
}

func TestPublishTrades(t *testing.T) {
	pub := &fakePublisher{}
	sink := NewTradeSink(pub, "")
	trades := []model.Trade{
		{SecID: 26, Board: "TQBR", SecCode: "SBER", TradeNo: 9000000001, Price: model.Decimal{Units: 30512, Scale: 2}, Quantity: 3,
			BuySell: 'B', Period: 'N', Time: time.Date(2026, 11, 3, 20, 53, 22, 250e6, time.UTC)},
		{SecID: 20583, Board: "FUT", SecCode: "SiZ6", TradeNo: 1, Price: model.Decimal{Units: -5}, Quantity: 1, BuySell: 'S',
			HasOpenInterest: true, OpenInterest: 777, Time: time.Unix(0, 0)},
		{SecID: 1, Board: "MCT", SecCode: "1093.XHKG", TradeNo: 2},
	}
	if err := sink.Publish(trades); err != nil {
		t.Fatal(err)
	}
	wantSubjects := []string{"transaq.alltrades.TQBR.SBER", "transaq.alltrades.FUT.SiZ6", "transaq.alltrades.MCT.1093_XHKG"}
	if len(pub.msgs) != 3 {
		t.Fatalf("%d messages", len(pub.msgs))
	}
	for i, want := range wantSubjects {
		if pub.msgs[i].subject != want {
			t.Errorf("subject %d = %s, want %s", i, pub.msgs[i].subject, want)
		}
	}

	var m marketdata.Trade
	if err := proto.Unmarshal(pub.msgs[0].data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Board != "TQBR" || m.Seccode != "SBER" || m.Tradeno != 9000000001 || m.Price != 30512 || m.Decimals != 2 ||
		m.Quantity != 3 || m.Side != marketdata.Side_SIDE_BUY || m.Period != 'N' || m.TimeMs != 1793739202250 ||
		m.OpenInterest != nil || m.Secid != 26 {
		t.Fatalf("SBER = %v", &m)
	}
	if err := proto.Unmarshal(pub.msgs[1].data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Side != marketdata.Side_SIDE_SELL || m.GetOpenInterest() != 777 || m.Price != -5 {
		t.Fatalf("SiZ6 = %v", &m)
	}
	if len(pub.msgs[0].data) > 40 {
		t.Errorf("SBER message is %d bytes", len(pub.msgs[0].data))
	}
}

func TestPublishStopsOnError(t *testing.T) {
	pub := &fakePublisher{err: errors.New("closed")}
	if err := NewTradeSink(pub, "x").Publish([]model.Trade{{Board: "TQBR", SecCode: "SBER"}}); err == nil {
		t.Fatal("expected error")
	}
}
