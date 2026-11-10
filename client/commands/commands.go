// Package commands is a deprecated alias of
// github.com/kmlebedev/txmlconnector-native/commands, where the XML command
// and callback structures of TXmlConnector now live (moved 2026-11-10).
//
// Deprecated: import github.com/kmlebedev/txmlconnector-native/commands.
package commands

import nc "github.com/kmlebedev/txmlconnector-native/commands"

// Types are aliases, so values are interchangeable with the new package.
type (
	ServerStatus     = nc.ServerStatus
	Command          = nc.Command
	SubAllTrades     = nc.SubAllTrades
	SubSecurity      = nc.SubSecurity
	Connect          = nc.Connect
	Market           = nc.Market
	Markets          = nc.Markets
	CandleKind       = nc.CandleKind
	CandleKinds      = nc.CandleKinds
	Board            = nc.Board
	Boards           = nc.Boards
	Candle           = nc.Candle
	Candles          = nc.Candles
	OpMask           = nc.OpMask
	SecTimeZone      = nc.SecTimeZone
	Security         = nc.Security
	Securities       = nc.Securities
	Messages         = nc.Messages
	Pits             = nc.Pits
	SecInfoUpd       = nc.SecInfoUpd
	Position         = nc.Position
	MoneyPosition    = nc.MoneyPosition
	SecPosition      = nc.SecPosition
	Forts            = nc.Forts
	FortsPosition    = nc.FortsPosition
	FortsMoney       = nc.FortsMoney
	FortsCollaterals = nc.FortsCollaterals
	SpotLimit        = nc.SpotLimit
	UnitedLimits     = nc.UnitedLimits
	Positions        = nc.Positions
	UnitedPortfolio  = nc.UnitedPortfolio
	UnitedEquity     = nc.UnitedEquity
	Client           = nc.Client
	Union            = nc.Union
	Overnight        = nc.Overnight
	Result           = nc.Result
	NewsHeader       = nc.NewsHeader
	Quotations       = nc.Quotations
	Quotation        = nc.Quotation
	Trade            = nc.Trade
	AllTrades        = nc.AllTrades
	SecInfo          = nc.SecInfo
	Quotes           = nc.Quotes
	Quote            = nc.Quote
)

// EncodeRequest is nc.EncodeRequest.
func EncodeRequest(request interface{}) string { return nc.EncodeRequest(request) }

// EncodeRequestE is nc.EncodeRequestE.
func EncodeRequestE(request interface{}) (string, error) { return nc.EncodeRequestE(request) }
