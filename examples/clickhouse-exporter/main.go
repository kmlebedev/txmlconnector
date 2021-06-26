package main

import (
	"database/sql"
	"fmt"
	"github.com/ClickHouse/clickhouse-go"
	"github.com/kmlebedev/txmlconnector/client"
	"github.com/kmlebedev/txmlconnector/client/commands"
	log "github.com/sirupsen/logrus"
	"os"
	"time"
)

const (
	EnvKeyLogLevel      = "LOG_LEVEL"
	GetHistoryDataCount = 1000
)

func main() {
	if lvl, err := log.ParseLevel(os.Getenv(EnvKeyLogLevel)); err == nil {
		log.SetLevel(lvl)
	}
	tc, err := tcClient.NewTCClient()
	if err != nil {
		log.Panic(err)
	}
	defer tc.Disconnect()

	connect, err := sql.Open("clickhouse", "tcp://127.0.0.1:9000")
	if err != nil {
		log.Panic(err)
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := connect.Ping(); err != nil {
		if exception, ok := err.(*clickhouse.Exception); ok {
			fmt.Printf("[%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
		} else {
			fmt.Println(err)
		}
		return
	}
	_, err = connect.Exec(`
		CREATE TABLE IF NOT EXISTS candles (
		   date   DateTime,
		   sec_code FixedString(5),
		   open   Float32,
		   close  Float32,
		   high   Float32,
		   low    Float32,
		   volume UInt64
		) ENGINE = ReplacingMergeTree()
		ORDER BY (date, sec_code)
	`)
	if err != nil {
		log.Fatal(err)
	}
	positions := commands.Positions{}
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
					var stmt, _ = tx.Prepare("INSERT INTO candles (date, sec_code, open, close, high, low, volume) VALUES (?, ?, ?, ?, ?, ?, ?)")
					defer stmt.Close()
					for _, candle := range tc.Data.Candles.Items {
						candleDate, _ := time.Parse("02.01.2006 15:04:05", candle.Date)
						if _, err := stmt.Exec(
							fmt.Sprint(candleDate.Format("2006-01-02 15:04:05")),
							tc.Data.Candles.SecCode,
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
						log.Fatal(err)
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
		log.Debugf(fmt.Sprintf("gethistorydata sec %s", sec.SecCode))
		if err = tc.SendCommand(commands.Command{
			Id:     "gethistorydata",
			Period: 1,
			SecId:  sec.SecId,
			Count:  GetHistoryDataCount,
			Reset:  true,
		}); err != nil {
			log.Error(err)
		}
	}
	// Get subscribe on all sec
	if err = tc.SendCommand(commands.Command{
		Id:         "subscribe",
		Quotations: quotations,
	}); err != nil {
		log.Error("SendCommand: ", err)
	}
	<-tc.ShutdownChannel
}
