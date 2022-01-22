package main

import (
	"database/sql"
	"fmt"
	"github.com/kmlebedev/txmlconnector/examples/clickhouse-exporter/financial/exporter"
	_ "github.com/kmlebedev/txmlconnector/examples/clickhouse-exporter/financial/exporter"
	"github.com/shakinm/xlsReader/xls"
	"github.com/shakinm/xlsReader/xls/structure"
	log "github.com/sirupsen/logrus"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	isLoadednvestingData bool
)

func quarterToDate(quarter string) time.Time {
	if len(quarter) < 7 {
		return time.Now()
	}
	y, _ := strconv.Atoi(quarter[3:7])
	m, _ := strconv.Atoi(quarter[1:2])
	if m == 4 {
		y = y + 1
		m = 0
	}
	timeQ, _ := time.Parse("2006-01-02", fmt.Sprintf("%d-%02d-01", y, 1+m*3))
	return timeQ
}

func isBlankCell(cell structure.CellData) bool {
	if strings.HasSuffix(cell.GetType(), "Blank") {
		return true
	}
	return false
}

func isQuarterRow(tableRowIdx int, row int) bool {
	if tableRowIdx+1 == row && tableRowIdx > -1 {
		return true
	}
	return false
}

func getTableRowInx(sheet xls.Sheet, tableName string) (int, int) {
	quarterIdx := 0
	for i, row := range sheet.GetRows() {
		if nextCell, _ := row.GetCol(1); strings.HasPrefix(nextCell.GetString(), "Q") {
			quarterIdx = i
		}
		if cell, err := row.GetCol(0); err == nil && cell.GetString() == tableName {
			nextRow, _ := sheet.GetRow(i + 1)
			if nextCell, _ := nextRow.GetCol(1); strings.HasPrefix(nextCell.GetString(), "Q") {
				return i + 2, i + 1
			} else if quarterIdx > 0 {
				return i, quarterIdx
			}
			continue
		}
	}
	return -1, -1
}

func getTable(sheet xls.Sheet, tableName string) *map[string]map[string]float64 {
	table := make(map[string]map[string]float64)
	quarters := map[int]string{}
	tableRowIdx, quarterIdx := getTableRowInx(sheet, tableName)
	log.Debugf("tableName %s, idx: %d", tableName, tableRowIdx)
	blankRowFound := false
	quarterRow, _ := sheet.GetRow(quarterIdx)
	for az, cel := range quarterRow.GetCols() {
		if strings.HasPrefix(cel.GetString(), "Q") {
			log.Debugf("quarter %s", cel.GetString())
			quarters[az] = cel.GetString()
		}
	}
	for n := tableRowIdx; n <= sheet.GetNumberRows(); n++ {
		row, err := sheet.GetRow(n)
		if err != nil {
			log.Warn(err)
			continue
		}
		if cel, _ := row.GetCol(1); isBlankCell(cel) {
			if blankRowFound {
				break
			}
			blankRowFound = true
			continue
		}
		tableKey := ""
		for az, cel := range row.GetCols() {
			if az == 0 {
				if cel.GetString() != "" {
					tableKey = strings.Trim(strings.Replace(cel.GetString(), "including", "", 1), ", *:")
					table[tableKey] = make(map[string]float64)
					log.Debugf("table key %s, name %s", tableKey, cel.GetString())
				}
				continue
			}
			if az > 0 && isBlankCell(cel) {
				break
			}
			if _, ok := quarters[az]; !ok {
				continue
			}
			table[tableKey][quarters[az]] = cel.GetFloat64()
		}
	}
	return &table
}

func insertToDB(conn *sql.DB, secCode string, name string, query string, table *map[string]map[string]float64) error {
	var tx, _ = conn.Begin()
	var stmt, _ = tx.Prepare(query)
	for key, quarterValues := range *table {
		for quarter, value := range quarterValues {
			if secCode == "" && name == "" {
				if _, err := stmt.Exec(quarterToDate(quarter), quarter, key, value); err != nil {
					return err
				}
			} else if name != "" {
				if _, err := stmt.Exec(secCode, name, quarterToDate(quarter), quarter, key, value); err != nil {
					return err
				}
			} else {
				if _, err := stmt.Exec(secCode, quarterToDate(quarter), quarter, key, value); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func loadAllInvestingData(conn *sql.DB) {
	if isLoadednvestingData {
		return
	}
	for code, _ := range exporter.CodeToId {
		if err := exporter.LoadHistoricalData(conn, code, "07/07/2014", time.Now().Format("01/02/2006"), "Daily"); err != nil {
			log.Error(err)
		}
	}
	isLoadednvestingData = true
}

func main() {
	if lvl, err := log.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		log.SetLevel(lvl)
	}
	ticker := os.Getenv("TICKER")
	conn := initDB()
	log.Debugf("connected %+v", conn.Stats())
	if ticker == "MOEX" || ticker == "ALL" {
		var m moexDataBook
		var t tradingVolumes
		if err := t.Initialize(conn, "trading-volumes-2021-nov.xlsx"); err != nil {
			log.Fatal(err)
		}
		if err := t.ImportToClickhouse(); err != nil {
			log.Fatal(err)
		}
		if err := m.Initialize(conn); err != nil {
			log.Fatal(err)
		}
		if err := m.ImportToClickhouse(); err != nil {
			log.Fatal(err)
		}
	}
	if ticker == "VTBR" || ticker == "ALL" {
		if err := crawFinanceResults(conn); err != nil {
			log.Error(err)
		}
		if err := crawCbr(conn); err != nil {
			log.Error(err)
		}
		if err := loadCSV(conn); err != nil {
			log.Error(err)
		}
	}
	if ticker == "CHMF" || ticker == "ALL" {
		// Financial and Operating data SVST 3Q_21.xlsx
		// https://www.severstal.com/files/74962/Financial%20and%20Operating%20data%20SVST%203Q_21.xlsx
		if err := loadChmfData(conn, "Financial and Operating data SVST 3Q_21.xlsx"); err != nil {
			log.Error(err)
		}
		// Данные из отчета
		if err := loadChmfData(conn, "CHMF_revenue_structure.xlsx"); err != nil {
			log.Error(err)
		}
		loadAllInvestingData(conn)
	}
	if ticker == "MAGN" || ticker == "ALL" {
		//if err := loadMagnData(conn, "MMK_operating_m_financial_data_Q2_2021.xls"); err != nil {
		//	log.Error(err)
		//}
		if err := loadMagnData(conn, "MMK_operating_financial_data_Q3_2021.xls"); err != nil {
			log.Error(err)
		}
		loadAllInvestingData(conn)
		if err := craw(conn, "yuzd", "yuzd", "6194", "Погрузка на Южно-Уральской железной дороге"); err != nil {
			log.Error(err)
		}
		if err := craw(conn, "yuzd", "chel", "6194", "Погрузка на железной дороге в Челябинской области"); err != nil {
			log.Error(err)
		}
	}
	if ticker == "NLMK" || ticker == "ALL" {
		if err := loadNlmkData(conn, "financial_and_operating_data_3q_2021.xlsx"); err != nil {
			log.Error(err)
		}
		if err := loadNlmkOPData(conn, "NLMK_Operating_Results_4Q_2021_RUS.xlsx"); err != nil {
			log.Error(err)
		}
		loadAllInvestingData(conn)
	}
	if ticker == "Exports" || ticker == "ALL" {
		//if err := crawExports(conn); err != nil {
		//	log.Error(err)
		//}
		for code, _ := range exporter.CodeToId {
			if err := exporter.LoadHistoricalData(conn, code, "07/07/2014", time.Now().Format("01/02/2006"), "Daily"); err != nil {
				log.Error(err)
			}
		}
		if err := loadInvestingData(conn); err != nil {
			log.Error(err)
		}
		//if err := craw(conn, "szd", "szd", "5319", "Погрузка на Северной железной дороге"); err != nil {
		//if err := craw(conn, "szd", "5319", "Погрузка на железной дороге в Вологодской области"); err != nil {
		//	log.Error(err)
		//}

		//if err := loadLmeData(conn); err != nil {
		//	log.Error(err)
		//}
	}
	if ticker == "YUZD" {
		if err := craw(conn, "yuzd", "yuzd", "6194", "Погрузка на Южно-Уральской железной дороге"); err != nil {
			log.Error(err)
		}
		if err := craw(conn, "yuzd", "chel", "6194", "Погрузка на железной дороге в Челябинской области"); err != nil {
			log.Error(err)
		}
	}
}
