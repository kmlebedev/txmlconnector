package main

import (
	"context"
	"fmt"
	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/kmlebedev/txmlconnector/client"
	"github.com/kmlebedev/txmlconnector/client/commands"
	log "github.com/sirupsen/logrus"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	EnvKeyLogLevel          = "LOG_LEVEL"
	ExportCandleCount       = 0
	ChCandlesInsertQuery    = "INSERT INTO transaq_candles"
	ChSecuritiesInsertQuery = "INSERT INTO transaq_securities"
	ChTradesInsertQuery     = "INSERT INTO transaq_trades VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

	candlesDDL = `CREATE TABLE IF NOT EXISTS transaq_candles (
		date   DateTime('Europe/Moscow'),
		sec_code FixedString(16),
		period UInt8,
		open   Float32,
		close  Float32,
		high   Float32,
		low    Float32,
		volume UInt64
		) ENGINE = ReplacingMergeTree()
		ORDER BY (date, sec_code, period)`

	securitiesDDL = `CREATE TABLE IF NOT EXISTS transaq_securities (
			secid   UInt16,
			seccode FixedString(16),
			instrclass String,
			board String,
			market UInt8,
			shortname String,
			decimals UInt8,
			minstep Float32,
			lotsize UInt8,
			point_cost Float32,
			sectype String,
			quotestype UInt8
		) ENGINE = ReplacingMergeTree()
		ORDER BY (secid, seccode, board)`

	tradesDDL = `CREATE TABLE IF NOT EXISTS transaq_trades (
		time   DateTime('Europe/Moscow'),
		secid   UInt16,
		sec_code LowCardinality(FixedString(16)),
        trade_no Int64,
		board LowCardinality(String),
		price   Float32,
		quantity UInt32,
        buy_sell LowCardinality(FixedString(1)),
        open_interest Int32,
        period LowCardinality(FixedString(1)),
		) ENGINE = ReplacingMergeTree()
		ORDER BY (secid, sec_code, trade_no, time, buy_sell)`
)

var (
	ctx                  = context.Background()
	lvl                  log.Level
	tc                   *tcClient.TCClient
	connect              driver.Conn
	positions            = commands.Positions{}
	quotationCandles     = make(map[int]commands.Candle)
	dataCandleCount      = ExportCandleCount
	dataCandleCountLock  = sync.RWMutex{}
	isAllTradesPositions = false
	allTrades            = commands.SubAllTrades{}
)

func init() {
	var err error

	if lvl, err = log.ParseLevel(os.Getenv(EnvKeyLogLevel)); err == nil {
		log.SetLevel(lvl)
	}
	clickhouseUrl := "tcp://127.0.0.1:9000"
	if chUrl := os.Getenv("CLICKHOUSE_URL"); chUrl != "" {
		clickhouseUrl = chUrl
	}
	clickhouseOptions, _ := clickhouse.ParseDSN(clickhouseUrl)
	for i := 0; i < 10; i++ {
		log.Infof("Try connect to clickhouse %s", clickhouseUrl)
		if connect, err = clickhouse.Open(clickhouseOptions); err != nil {
			log.Fatal(err)
		}
		if err := connect.Ping(ctx); err != nil {
			if exception, ok := err.(*clickhouse.Exception); ok {
				log.Infof("[%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
			}
			log.Warn(err)
		} else {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if err = connect.Exec(ctx, candlesDDL); err != nil {
		log.Fatal(err)
	}
	if err = connect.Exec(ctx, securitiesDDL); err != nil {
		log.Fatal(err)
	}
	if err = connect.Exec(ctx, tradesDDL); err != nil {
		log.Fatal(err)
	}
	if tc, err = tcClient.NewTCClient(); err != nil {
		log.Fatal(err)
	}
}

func processTransaq() {
	var status commands.ServerStatus
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case upd := <-tc.SecInfoUpdChan:
			log.Infof("secInfoUpd %+v", upd)
		case status = <-tc.ServerStatusChan:
			switch status.Connected {
			case "true":
				log.Infof("server status is true")
			case "error":
				log.Warnf("txmlconnector not connected %+v\n", status)
			default:
				log.Infof("Status %+v", status)
			}
		case <-ticker.C:
			if status.Connected != "error" {
				continue
			}
			if err := tc.Connect(); err != nil {
				log.Fatal(err)
			}
		case trades := <-tc.AllTradesChan:
			//go func(t *commands.AllTrades) {
			for _, trade := range trades.Items {
				tradeTime, _ := time.Parse("02.01.2006 15:04:05", trade.Time)
				if err := connect.AsyncInsert(ctx, ChTradesInsertQuery, false,
					fmt.Sprint(tradeTime.Format("2006-01-02 15:04:05")),
					trade.SecId,
					trade.SecCode,
					trade.TradeNo,
					trade.Board,
					trade.Pice,
					trade.Quantity,
					trade.BuySell,
					trade.OpenInterest,
					trade.Period); err != nil {
					log.Errorf("trades async insert trade: %+v: %+v\n trades: %+v", trade, err)
				}
			}
			//}(&trades)
		case resp := <-tc.ResponseChannel:
			switch resp {
			case "united_portfolio":
				log.Infof(fmt.Sprintf("UnitedPortfolio: ```\n%+v\n```", tc.Data.UnitedPortfolio))
			case "united_equity":
				log.Infof(fmt.Sprintf("UnitedEquity: ```\n%+v\n```", tc.Data.UnitedEquity))
			case "positions":
				// Todo avoid overwrite if only change field
				if tc.Data.Positions.UnitedLimits != nil && len(tc.Data.Positions.UnitedLimits) > 0 {
					positions.UnitedLimits = tc.Data.Positions.UnitedLimits
				}
				if tc.Data.Positions.SecPositions != nil && len(tc.Data.Positions.SecPositions) > 0 {
					positions.SecPositions = tc.Data.Positions.SecPositions
				}
				if tc.Data.Positions.FortsMoney != nil && len(tc.Data.Positions.FortsMoney) > 0 {
					positions.FortsMoney = tc.Data.Positions.FortsMoney
				}
				if tc.Data.Positions.MoneyPosition != nil && len(tc.Data.Positions.MoneyPosition) > 0 {
					positions.MoneyPosition = tc.Data.Positions.MoneyPosition
				}
				if tc.Data.Positions.FortsPosition != nil && len(tc.Data.Positions.FortsPosition) > 0 {
					positions.FortsPosition = tc.Data.Positions.FortsPosition
				}
				if tc.Data.Positions.FortsCollaterals != nil && len(tc.Data.Positions.FortsCollaterals) > 0 {
					positions.FortsCollaterals = tc.Data.Positions.FortsCollaterals
				}
				if tc.Data.Positions.SpotLimit != nil && len(tc.Data.Positions.SpotLimit) > 0 {
					positions.SpotLimit = tc.Data.Positions.SpotLimit
				}
				if isAllTradesPositions {
					for _, secPosition := range tc.Data.Positions.SecPositions {
						allTrades.Items = append(allTrades.Items, secPosition.SecInfo.SecId)
					}
				}
				log.Infof("Positions: \n%+v\n", tc.Data.Positions)
			case "candles":
				batch, _ := connect.PrepareBatch(ctx, ChCandlesInsertQuery)
				dataCandleCountLock.Lock()
				dataCandleCount = len(tc.Data.Candles.Items)
				dataCandleCountLock.Unlock()
				for _, candle := range tc.Data.Candles.Items {
					candleDate, _ := time.Parse("02.01.2006 15:04:05", candle.Date)
					if err := batch.Append(
						fmt.Sprint(candleDate.Format("2006-01-02 15:04:05")),
						tc.Data.Candles.SecCode,
						uint8(tc.Data.Candles.Period),
						float32(candle.Open),
						float32(candle.Close),
						float32(candle.High),
						float32(candle.Low),
						uint64(candle.Volume),
					); err != nil {
						log.Error(err)
					}
				}
				if err := batch.Send(); err != nil {
					log.Error(err)
				}
			case "quotations":
				timeNow := time.Now()
				batch, _ := connect.PrepareBatch(ctx, ChCandlesInsertQuery)
				for _, quotation := range tc.Data.Quotations.Items {
					quotationCandle, quotationCandleExist := quotationCandles[quotation.SecId]
					if strings.HasSuffix(quotation.Time, ":00") && quotation.Last > 0 && quotationCandleExist {
						if err := batch.Append(
							fmt.Sprintf("%s %s", timeNow.Format("2006-01-02"), quotation.Time),
							quotation.SecCode,
							uint8(1),
							float32(quotationCandles[quotation.SecId].Open),
							float32(quotation.Last), // Close
							float32(quotationCandles[quotation.SecId].High),
							float32(quotationCandles[quotation.SecId].Low),
							uint64(quotationCandles[quotation.SecId].Volume),
						); err != nil {
							log.Fatal(err)
						}
						quotationCandles[quotation.SecId] = commands.Candle{}
					} else {
						if quotationCandleExist {
							if quotationCandle.Open == 0 && quotation.Open != 0 {
								quotationCandle.Open = quotation.Open
							}
							if quotation.Last > quotationCandle.High {
								quotationCandle.High = quotation.Last
							}
							if quotation.Last < quotationCandle.Low || quotationCandle.Low == 0 {
								quotationCandle.Low = quotation.Last
							}
							quotationCandle.Volume += int64(quotation.Quantity)
						} else {
							quotationCandles[quotation.SecId] = commands.Candle{
								Open:   quotation.Last,
								Low:    quotation.Last,
								High:   quotation.Last,
								Volume: int64(quotation.Quantity),
							}
						}
					}
				}
				if err := batch.Send(); err != nil {
					log.Error(err)
				}
			default:
				log.Debugf(fmt.Sprintf("receive %s", resp))
			}
		}
	}
}

func main() {
	defer func() {
		tc.Disconnect()
		tc.Close()
		connect.Close()
	}()

	exportAllTradesSec := []string{}
	if envAllTrades := os.Getenv("EXPORT_ALL_TRADES"); envAllTrades != "" {
		for _, sec := range strings.Split(envAllTrades, ",") {
			if sec == "positions" {
				isAllTradesPositions = true
				continue
			}
			exportAllTradesSec = append(exportAllTradesSec, sec)
		}
	}

	go processTransaq()

	log.Infof("Wait txmlconnector ")
	for {
		if tc.Data.ServerStatus.Connected == "true" {
			log.Infof(" connected\n")
			break
		}
		fmt.Printf(".")
		time.Sleep(5 * time.Second)
	}

	// Get History data for all sec
	quotations := []commands.SubSecurity{}
	exportCandleCount := ExportCandleCount
	if eCandleCount, err := strconv.Atoi(os.Getenv("EXPORT_CANDLE_COUNT")); err == nil && eCandleCount > -2 {
		exportCandleCount = eCandleCount
	}
	exportSecBoards := []string{"TQBR"}
	if eSecBoards := os.Getenv("EXPORT_SEC_BOARDS"); eSecBoards != "" {
		exportSecBoards = strings.Split(eSecBoards, ",")
	}
	exportSecCodes := []string{}
	if eSecCodes := os.Getenv("EXPORT_SEC_CODES"); eSecCodes != "" {
		exportSecCodes = strings.Split(eSecCodes, ",")
	}
	exportPeriodSeconds := []string{}
	if ePeriodSeconds := os.Getenv("EXPORT_PERIOD_SECONDS"); ePeriodSeconds != "" {
		exportPeriodSeconds = strings.Split(ePeriodSeconds, ",")
	}
	batchSec, err := connect.PrepareBatch(ctx, ChSecuritiesInsertQuery)
	if err != nil {
		log.Error(err)
	}

	for _, sec := range tc.Data.Securities.Items {
		exportSecBoardFound := false
		for _, exportSecBoard := range exportSecBoards {
			if exportSecBoard == sec.Board || exportSecBoard == "ALL" {
				exportSecBoardFound = true
				break
			}
		}
		for _, exportSecCode := range exportAllTradesSec {
			if exportSecCode == sec.SecCode {
				allTrades.Items = append(allTrades.Items, sec.SecId)
			}
		}
		if sec.SecId == 0 || sec.Active != "true" || len(sec.SecCode) > 16 {
			continue
		}
		log.Debugf("%+v", sec)

		if err := batchSec.Append(uint16(sec.SecId),
			sec.SecCode,
			sec.InstrClass,
			sec.Board,
			uint8(sec.Market),
			sec.ShortName,
			uint8(sec.Decimals),
			float32(sec.MinStep),
			uint8(sec.LotSize),
			float32(sec.PointCost),
			sec.SecType,
			uint8(sec.QuotesType)); err != nil {
			log.Error(err)
		}
		if !exportSecBoardFound {
			continue
		}
		if len(exportSecCodes) == 0 {
			continue
		}
		exportSecCodeFound := false
		for _, exportSecCode := range exportSecCodes {
			if exportSecCode == sec.SecCode || strings.Contains(sec.SecCode, exportSecCode) || exportSecCode == sec.ShortName || exportSecCode == "ALL" {
				exportSecCodeFound = true
				break
			}
		}
		if !exportSecCodeFound {
			continue
		}
		quotations = append(quotations, commands.SubSecurity{SecId: sec.SecId})
		for _, kind := range tc.Data.CandleKinds.Items {
			if len(exportPeriodSeconds) > 0 {
				exportPeriodSecondFound := false
				for _, exportPeriodSecond := range exportPeriodSeconds {
					if exportPeriodSecond == strconv.Itoa(kind.Period) {
						exportPeriodSecondFound = true
					}
				}
				if !exportPeriodSecondFound {
					continue
				}
			}
			if exportCandleCount == 0 {
				continue
			} else if exportCandleCount > 0 {
				log.Debugf(fmt.Sprintf("gethistorydata sec %s period %d name %s seconds %d", sec.SecCode, kind.ID, kind.Name, kind.Period))
				if err = tc.SendCommand(commands.Command{
					Id:     "gethistorydata",
					Period: kind.ID,
					SecId:  sec.SecId,
					Count:  exportCandleCount,
					Reset:  "true",
				}); err != nil {
					log.Error(err)
				}
				// Export All Candles
			} else {
				for ExportCandleCount == dataCandleCount {
					log.Debugf("loop get history %d == %d", ExportCandleCount, dataCandleCount)
					if err = tc.SendCommand(commands.Command{
						Id:     "gethistorydata",
						Period: kind.ID,
						SecId:  sec.SecId,
						Count:  ExportCandleCount,
						Reset:  "false",
					}); err != nil {
						log.Error(err)
					}
					time.Sleep(2 * time.Second)
				}
				log.Debugf("exit loop get history %d == %d", ExportCandleCount, dataCandleCount)
				dataCandleCountLock.Lock()
				dataCandleCount = ExportCandleCount
				dataCandleCountLock.Unlock()
			}
		}
	}
	if batchSec.Rows() > 0 {
		if err := batchSec.Send(); err != nil {
			log.Error(err)
		}
	}
	// receive <quotations><quotation secid="21"><board>TQBR</board><seccode>GMKN</seccode><last>24954</last><quantity>4</quantity><time>11:24:00</time><change>220</change><priceminusprevwaprice>432</priceminusprevwaprice><bid>24950</bid><biddepth>35</biddepth><biddeptht>16188</biddeptht><numbids>1563</numbids><offer>24962</offer><offerdepth>51</offerdepth><offerdeptht>25222</offerdeptht><numoffers>1154</numoffers><voltoday>54772</voltoday><numtrades>6273</numtrades><valtoday>1364.723</valtoday></quotation></quotations>
	// Get subscribe on all sec
	if err = tc.SendCommand(commands.Command{Id: "get_mc_portfolio", Union: "377620R2555"}); err != nil {
		log.Error("SendCommand get_mc_portfolio: ", err)
	}
	time.Sleep(10 * time.Second)
	if err = tc.SendCommand(commands.Command{
		Id:         "subscribe",
		Quotations: quotations,
		AllTrades:  allTrades,
	}); err != nil {
		log.Error("SendCommand subscribe: ", err)
	}
	<-tc.ShutdownChannel
}
