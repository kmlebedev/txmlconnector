package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	//log "github.com/sirupsen/logrus"
	log "github.com/golang/glog"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

var (
	moexDataBookUrl   = "https://fs.moex.com/files/16239" // ifrs-databook-3q-2021.xlsx https://www.moex.com/s1347
	tradingVolumesUrl = "https://fs.moex.com/files/4243/" // trading-volumes-2021-nov.xlsx https://www.moex.com/s868
	moexClients       = "https://fs.moex.com/files/22304" // clients-2021-november.xlsx https://www.moex.com/s719
)

type tradingVolumes struct {
	categories []string
	xlsxFile   *excelize.File
	conn       *sql.DB
}

func openxlsxFile(fileName string) (*excelize.File, error) {
	return excelize.OpenFile(path.Join(os.Getenv("FINANCIAL_DATA_DIR"), fileName))
}

func getxlsxFile(url string) (*excelize.File, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	log.Info(moexDataBookUrl)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(body)
	return excelize.OpenReader(reader)
}

func (data *tradingVolumes) Initialize(conn *sql.DB, fileName string) (err error) {
	data.categories = []string{
		"Фондовый рынок",
		"Рынок облигаций",
		"Денежный рынок",
		"Кредитный рынок",
		"Валютный рынок",
		"Срочный рынок",
	}
	data.conn = conn
	//data.xlsxFile, err = getxlsxFile(tradingVolumesUrl)
	data.xlsxFile, err = openxlsxFile(fileName)
	if err != nil {
		log.Error(err)
		return err
	}
	return nil
}

func (data *tradingVolumes) ImportToClickhouse() error {
	rows, err := data.xlsxFile.GetRows("объем")
	if err != nil {
		log.Error(err)
		return err
	}
	var category string
	var subCategory string
	mounths := map[int]time.Time{}
	var tx, _ = data.conn.Begin()
	var stmt, _ = tx.Prepare("INSERT INTO trading_volumes (*) VALUES (?)")
	for i, row := range rows {
		if err != nil {
			log.Error(err)
			return err
		}
		for j, colCell := range row {
			if i == 2 {
				dateTime, derr := time.Parse("Jan-06", colCell)
				if derr != nil {
					continue
				}
				//log.Infof("data %v parsed %v", colCell, dateTime)
				mounths[j] = dateTime
			}
			if j == 0 {
				if colCell == "" {
					continue
				}
				for _, category = range data.categories {
					if category == colCell {
						subCategory = ""
						break
					}
				}
				for _, subCategory = range []string{"Фьючерсы", "Опционы", "Сделки спот", "Сделки своп и форварды", "Рынок акций, ДР и паев", "Рынок облигаций"} {
					if subCategory == colCell {
						break
					}
				}
			}
			if colCell == "" {
				continue
			}
			value, err := strconv.ParseFloat(colCell, 32)
			if err != nil {
				continue
			}
			if _, ok := mounths[j]; !ok {
				continue
			}
			if _, err := stmt.Exec(category, subCategory, mounths[j], row[0], value); err != nil {
				log.Error(err)
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		log.Error(err)
		return err
	}
	return nil
}

type moexDataBook struct {
	exportQuarter map[string]string
	exportTables  map[string]map[string]string
	conn          *sql.DB
	xlsxFile      *excelize.File
}

func (data *moexDataBook) Initialize(conn *sql.DB) (err error) {
	data.conn = conn
	data.exportQuarter = map[string]string{
		"3. P&L - reporting periods": "RUB mln unless stated otherwise",
	}
	data.exportTables = map[string]map[string]string{}
	data.exportTables["3. P&L - reporting periods"] = map[string]string{
		"Fee and commission income":           "databook",
		"General and administrative expenses": "databook",
		"Other operating expenses":            "databook",
		"Income tax expense":                  "databook",
		"Earnings per share, RUB":             "databook",
	}
	data.xlsxFile, err = getxlsxFile(moexDataBookUrl)
	if err != nil {
		return err
	}
	return nil
}

func (data *moexDataBook) GetSecCode() string {
	return "MOEX"
}

func (data *moexDataBook) ImportToClickhouse() error {
	for sheet, tableNames := range data.exportTables {
		rows, err := data.xlsxFile.GetRows(sheet)
		if err != nil {
			if strings.HasSuffix(err.Error(), "is not exist") {
				log.Error(err)
				continue
			}
			return err
		}
		for name, table := range tableNames {
			if err := insertToDB(data.conn, data.GetSecCode(), sheet,
				fmt.Sprintf("INSERT INTO %s (*) VALUES (?)", table),
				getElasticTableFromRows(&rows, name, exportQuarter[sheet], true),
			); err != nil {
				log.Error(err)
				return err
			}
		}
	}
	return nil
}
