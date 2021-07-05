package main

import (
	"database/sql"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	log "github.com/sirupsen/logrus"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	investingDataBasePath = "data"
	investingDataPaths    = map[string]string{}
	investingDataCodes    = map[string]string{}
)

func init() {
	if dir := os.Getenv("FINANCIAL_DATA_DIR"); dir != "" {
		investingDataBasePath = dir
	}
	investingDataPaths["STEEL HRC FOB CHINA Futures"] = investingDataBasePath + "/STEEL HRC FOB CHINA Futures.xlsx"
	investingDataCodes["STEEL HRC FOB CHINA Futures"] = "MHCc1"
	investingDataPaths["US Midwest Domestic Hot-Rolled "] = investingDataBasePath + "/US Midwest Domestic Hot-Rolled Coil Steel Futures.xlsx"
	investingDataCodes["US Midwest Domestic Hot-Rolled "] = "HRCc1"
	investingDataPaths["Hot Rolled Coil Futures"] = investingDataBasePath + "/Hot Rolled Coil Futures SHHCc1.xlsx"
	investingDataCodes["Hot Rolled Coil Futures"] = "SHHCc1"
	investingDataPaths["Iron Ore CFR China 62% Fe"] = investingDataBasePath + "/62% Fe CFR SGXIOSc1.xlsx"
	investingDataCodes["Iron Ore CFR China 62% Fe"] = "SGXIOSc1"
}

func loadInvestingData(conn *sql.DB) error {
	for sheet, dataPath := range investingDataPaths {
		xlsxFile, err := excelize.OpenFile(dataPath)
		if err != nil {
			return err
		}
		rows, err := xlsxFile.GetRows(sheet)
		if err != nil {
			return err
		}
		var tx, _ = conn.Begin()
		var stmt, _ = tx.Prepare("INSERT INTO stock_prices (*) VALUES (?, ?, ?, ?, ?, ?, ?)")
		for i, row := range rows {
			if i == 0 || row[0] == "" {
				continue
			}
			date, err := time.Parse("2006-01-02", fmt.Sprintf("20%s-%s", row[0][6:8], row[0][0:5]))
			if err != nil {
				return err
			}
			price := []float32{}
			for j := 1; j < 5; j++ {
				priceStr := row[j]
				if strings.Contains(priceStr, ",") {
					priceStr = strings.Replace(strings.Replace(priceStr, ".", "", -1), ",", ".", -1)
				}
				f, err := strconv.ParseFloat(priceStr, 32)
				if err != nil {
					log.Errorf("idx %d row %s orig %s", j, priceStr, row[j])
					return err
				}
				price = append(price, float32(f))
			}
			volume := int64(0)
			if strings.HasSuffix(row[5], "K") {
				f, _ := strconv.ParseFloat(row[5], 32)
				volume = int64(f * 1000)
			}
			if _, err := stmt.Exec(investingDataCodes[sheet], date, price[0], price[1], price[2], price[3], volume); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
