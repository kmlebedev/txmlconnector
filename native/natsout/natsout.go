// Package natsout publishes native market data to NATS Core as protobuf
// messages (proto/marketdata). It depends only on a Publish method, which
// *nats.Conn from github.com/nats-io/nats.go provides.
package natsout

import (
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/proto/marketdata"
)

// DefaultTradePrefix is the subject prefix for trades.
const DefaultTradePrefix = "transaq.alltrades"

// Publisher is the subset of *nats.Conn used here.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// TradeSink publishes each trade to <prefix>.<board>.<seccode>, so
// subscribers can filter by subject (transaq.alltrades.TQBR.>).
type TradeSink struct {
	pub    Publisher
	prefix string
}

func NewTradeSink(pub Publisher, prefix string) *TradeSink {
	if prefix == "" {
		prefix = DefaultTradePrefix
	}
	return &TradeSink{pub: pub, prefix: prefix}
}

// Subject returns the subject a trade is published to.
func (s *TradeSink) Subject(t model.Trade) string {
	return s.prefix + "." + token(t.Board) + "." + token(t.SecCode)
}

// Publish sends trades in order; it stops at the first error.
func (s *TradeSink) Publish(trades []model.Trade) error {
	for _, t := range trades {
		data, err := proto.Marshal(Message(t))
		if err != nil {
			return err
		}
		if err := s.pub.Publish(s.Subject(t), data); err != nil {
			return err
		}
	}
	return nil
}

// Message converts a trade to its protobuf form.
func Message(t model.Trade) *marketdata.Trade {
	m := &marketdata.Trade{
		Board:    t.Board,
		Seccode:  t.SecCode,
		Tradeno:  t.TradeNo,
		TimeMs:   t.Time.UnixMilli(),
		Price:    t.Price.Units,
		Decimals: uint32(t.Price.Scale),
		Quantity: t.Quantity,
		Period:   uint32(t.Period),
		Secid:    uint32(t.SecID),
	}
	switch t.BuySell {
	case 'B':
		m.Side = marketdata.Side_SIDE_BUY
	case 'S':
		m.Side = marketdata.Side_SIDE_SELL
	}
	if t.HasOpenInterest {
		m.OpenInterest = proto.Int64(t.OpenInterest)
	}
	return m
}

// token makes a subject token: NATS reserves '.', '*', '>' and whitespace.
// Unknown securities get "_".
func token(s string) string {
	if s == "" {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '.', '*', '>', ' ', '\t', '\n', '\r':
			return '_'
		}
		return r
	}, s)
}
