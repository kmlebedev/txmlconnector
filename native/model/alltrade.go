package model

// AllTrade flag bits: optional fields of R_ALLTRADE, read in this order by
// FUN_1803bf700.
const (
	AllTradePeriod       uint16 = 0x0001 // B: <period> (O, N, C)
	AllTradeField2       uint16 = 0x0002 // L, then G after Field4 (not in <alltrades>)
	AllTradeField4       uint16 = 0x0004 // G (not in <alltrades>)
	AllTradeBuySell      uint16 = 0x0008 // B: <buysell> (B or S)
	AllTradeText         uint16 = 0x0010 // Z12 (not in <alltrades>)
	AllTradeBlock20      uint16 = 0x0020 // L L G G (not in <alltrades>)
	AllTradeOpenInterest uint16 = 0x0040 // L: <openinterest>
	AllTradeCurrency     uint16 = 0x0080 // Z4 (not in <alltrades>)
	AllTradeMillis       uint16 = 0x0100 // W: milliseconds of <time>, 1000 when absent
)

// AllTrade is an R_ALLTRADE record (tag 0x08): one trade of the market feed
// subscribed with <alltrades>, as decoded from the wire. It is a plain record
// type: native/records decodes it into Trade.Raw, so consumers that need
// fields which <alltrades> does not emit (Field2, Text, ...) still have them.
// Prices are integers in units of 10^-decimals of the trade's board.
type AllTrade struct {
	SecID      uint16
	Board      uint16 // board number from RQ_STARTUP
	TradeNo    int64
	Time       int64 // seconds, server wall clock as Unix time; 0: not set
	Price      int64 // in units of 10^-decimals of Board
	Quantity   int64
	Flags      uint16
	Period     byte // 0 when absent
	BuySell    byte // 0 when absent
	OpenInt    int64
	Millis     uint16 // 1000 when absent
	Field2     int32
	Field2Long int64
	Field4     int64
	Text       string
	Block20    [4]int64
	Currency   string
}
