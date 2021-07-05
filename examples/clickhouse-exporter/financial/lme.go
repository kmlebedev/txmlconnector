package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"strconv"
	"time"
)

var (
	lmeMarketDataBaseUrl = "https://www.lme.com/-/media/Files/Market-data/Historic-Data/2021/Cash-settled-futures/Closing-prices/"
	lmeDataUrls          = map[string]string{}
)

func init() {
	lmeDataUrls["HC Closing Prices"] = lmeMarketDataBaseUrl + "HC-Closing-Prices.xlsx"
	lmeDataUrls["HU Closing Prices"] = lmeMarketDataBaseUrl + "HU-Closing-Prices.xlsx"
	lmeDataUrls["UP Closing Prices"] = lmeMarketDataBaseUrl + "UP-Closing-Prices.xlsx"
}

func loadLmeData(conn *sql.DB) error {
	for sheet, dataUrl := range lmeDataUrls {
		resp, err := http.Get(dataUrl)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		reader := bytes.NewReader(body)
		xlsxFile, err := excelize.OpenReader(reader)
		if err != nil {
			return err
		}
		rows, err := xlsxFile.GetRows(sheet)
		if err != nil {
			return err
		}
		var tx, _ = conn.Begin()
		var stmt, _ = tx.Prepare("INSERT INTO lme_closing_prices (*) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		for i, row := range rows {
			if i == 0 || row[0] == "" {
				continue
			}
			date, err := time.Parse("2006-01-02", fmt.Sprintf("20%s-%s", row[0][6:8], row[0][0:5]))
			if err != nil {
				return err
			}
			price := []float32{}
			for j := 1; j < 16; j++ {
				f, err := strconv.ParseFloat(row[j], 32)
				if err != nil {
					log.Errorf("idx %d row %s", j, row[j])
					return err
				}
				price = append(price, float32(f))
			}
			if _, err := stmt.Exec(sheet[0:2], date, price[0], price[1], price[2], price[3], price[4], price[5], price[6], price[7], price[8], price[9], price[10], price[11], price[12], price[13], price[14]); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
