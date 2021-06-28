package main

import (
	"database/sql"
	"fmt"
	"github.com/ClickHouse/clickhouse-go"
	"github.com/kmlebedev/txmlconnector/client"
	"github.com/kmlebedev/txmlconnector/client/commands"
	log "github.com/sirupsen/logrus"
	"os"
	"strings"
	"time"
)

const (
	EnvKeyLogLevel      = "LOG_LEVEL"
	GetHistoryDataCount = 1000
	ChInsertQuery       = "INSERT INTO candles (date, sec_code, period, open, close, high, low, volume) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
)

func main() {
	var err error
	if lvl, err := log.ParseLevel(os.Getenv(EnvKeyLogLevel)); err == nil {
		log.SetLevel(lvl)
	}
	clickhouseUrl := "tcp://127.0.0.1:9000"
	if chUrl := os.Getenv("CLICKHOUSE_URL"); chUrl != "" {
		clickhouseUrl = chUrl
	}
	var connect *sql.DB
	for i := 0; i < 10; i++ {
		log.Infof("Try connect to clickhouse %s", clickhouseUrl)
		if connect, err = sql.Open("clickhouse", clickhouseUrl); err != nil {
			log.Fatal(err)
		}
		if err := connect.Ping(); err != nil {
			if exception, ok := err.(*clickhouse.Exception); ok {
				log.Infof("[%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
				break
			}
			log.Warn(err)
		}
		time.Sleep(10 * time.Second)
	}
	_, err = connect.Exec(`
		CREATE TABLE IF NOT EXISTS candles (
		   date   DateTime,
		   sec_code FixedString(5),
		   period UInt8,
		   open   Float32,
		   close  Float32,
		   high   Float32,
		   low    Float32,
		   volume UInt64
		) ENGINE = ReplacingMergeTree()
		ORDER BY (date, sec_code, period)
	`)
	if err != nil {
		log.Fatal(err)
	}
	tc, err := tcClient.NewTCClient()
	if err != nil {
		log.Panic(err)
	}
	defer tc.Disconnect()
	positions := commands.Positions{}
	quotationCandles := make(map[int]commands.Candle)
	go func() {
		for {
			select {
			case status := <-tc.ServerStatusChan:
				{
					if status.Connected != "true" {
						// Todo try reconect
						log.Fatalf("txmlconnector not connected %+v", status)
					}
				}
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
					log.Debugf(fmt.Sprintf("Positions: \n%+v\n", tc.Data.Positions))
				case "candles":
					var tx, _ = connect.Begin()
					var stmt, _ = tx.Prepare(ChInsertQuery)
					defer stmt.Close()
					for _, candle := range tc.Data.Candles.Items {
						candleDate, _ := time.Parse("02.01.2006 15:04:05", candle.Date)
						if _, err := stmt.Exec(
							fmt.Sprint(candleDate.Format("2006-01-02 15:04:05")),
							tc.Data.Candles.SecCode,
							tc.Data.Candles.Period,
							candle.Open,
							candle.Close,
							candle.High,
							candle.Low,
							candle.Volume,
						); err != nil {
							log.Fatal(err)
						}
					}
					if err := tx.Commit(); err != nil {
						log.Error(err)
					}
				case "quotations":
					timeNow := time.Now()
					var tx, _ = connect.Begin()
					var stmt, _ = tx.Prepare(ChInsertQuery)
					defer stmt.Close()
					for _, quotation := range tc.Data.Quotations.Items {
						quotationCandle, quotationCandleExist := quotationCandles[quotation.SecId]
						if strings.HasSuffix(quotation.Time, ":00") && quotation.Last > 0 && quotationCandleExist {
							if _, err := stmt.Exec(
								fmt.Sprintf("%s %s", timeNow.Format("2006-01-02"), quotation.Time),
								quotation.SecCode,
								1,
								quotationCandles[quotation.SecId].Open,
								quotation.Last, // Close
								quotationCandles[quotation.SecId].High,
								quotationCandles[quotation.SecId].Low,
								quotationCandles[quotation.SecId].Volume,
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
					if err := tx.Commit(); err != nil {
						log.Error(err)
					}
				default:
					log.Debugf(fmt.Sprintf("receive %s", resp))
				}
			}
		}
	}()
	go func() {
		for {
			select {
			case status := <-tc.ServerStatusChan:
				log.Infof("Status %v", status)
				//case upd := <-tc.SecInfoUpdChan:
				//	log.Debugf("secInfoUpd %v", upd)
			}
		}
	}()
	for {
		if tc.Data.ServerStatus.Connected == "true" {
			break
		}
		time.Sleep(5 * time.Second)
	}
	// Get History data for all sec
	quotations := []commands.SubSecurity{}
	for _, sec := range tc.Data.Securities.Items {
		if sec.Board != "TQBR" {
			continue
		}
		if sec.SecId == 0 {
			continue
		}
		quotations = append(quotations, commands.SubSecurity{SecId: sec.SecId})
		for _, kind := range tc.Data.CandleKinds.Items {
			log.Debugf(fmt.Sprintf("gethistorydata sec %s period %s", sec.SecCode, kind.Name))
			if err = tc.SendCommand(commands.Command{
				Id:     "gethistorydata",
				Period: kind.ID,
				SecId:  sec.SecId,
				Count:  GetHistoryDataCount,
				Reset:  true,
			}); err != nil {
				log.Error(err)
			}
		}
	}
	// receive <quotations><quotation secid="21"><board>TQBR</board><seccode>GMKN</seccode><last>24954</last><quantity>4</quantity><time>11:24:00</time><change>220</change><priceminusprevwaprice>432</priceminusprevwaprice><bid>24950</bid><biddepth>35</biddepth><biddeptht>16188</biddeptht><numbids>1563</numbids><offer>24962</offer><offerdepth>51</offerdepth><offerdeptht>25222</offerdeptht><numoffers>1154</numoffers><voltoday>54772</voltoday><numtrades>6273</numtrades><valtoday>1364.723</valtoday></quotation></quotations>
	// Get subscribe on all sec
	if err = tc.SendCommand(commands.Command{
		Id:         "subscribe",
		Quotations: quotations,
	}); err != nil {
		log.Error("SendCommand: ", err)
	}
	<-tc.ShutdownChannel
}
