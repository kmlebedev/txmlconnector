package main

import (
	"database/sql"
	"fmt"
	"github.com/shakinm/xlsReader/xls"
	"github.com/shakinm/xlsReader/xls/structure"
	log "github.com/sirupsen/logrus"
	"os"
	"strconv"
	"strings"
	"time"
)

func quarterToDate(quarter string) time.Time {
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
	blankRowFound := false
	quarterRow, _ := sheet.GetRow(quarterIdx)
	for az, cel := range quarterRow.GetCols() {
		if strings.HasPrefix(cel.GetString(), "Q") {
			log.Debugf("quarter %s", cel.GetString())
			quarters[az] = cel.GetString()
		}
	}
	for n := tableRowIdx + 1; n <= sheet.GetNumberRows(); n++ {
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
					log.Debugf("table key name %s", cel.GetString())
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

func insertToDB(conn *sql.DB, secCode string, query string, table *map[string]map[string]float64) error {
	var tx, _ = conn.Begin()
	var stmt, _ = tx.Prepare(query)
	for key, quarterValues := range *table {
		for quarter, value := range quarterValues {
			if secCode == "" {
				if _, err := stmt.Exec(quarterToDate(quarter), quarter, key, value); err != nil {
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

func main() {
	if lvl, err := log.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		log.SetLevel(lvl)
	}
	conn := initDB()
	log.Debugf("connected %+v", conn.Stats())

	if err := loadMagnData(conn, "MMK_operating_m_financial_data_Q2_2021.xls"); err != nil {
		log.Error(err)
	}
	if err := loadMagnData(conn, "MMK_operating_e_financial_data_Q1_2021.xls"); err != nil {
		log.Error(err)
	}
	if err := loadInvestingData(conn); err != nil {
		log.Error(err)
	}
	return
	if err := loadCSV(conn); err != nil {
		log.Error(err)
	}
	if err := loadLmeData(conn); err != nil {
		log.Error(err)
	}
	if err := loadChmfData(conn, "Q1_2021-Financial_and_operational_data-Severstal_Final.xlsx"); err != nil {
		log.Error(err)
	}
	if err := loadChmfData(conn, "CHMF_revenue_structure.xlsx"); err != nil {
		log.Error(err)
	}
	if err := loadNlmkData(conn, "financial_and_operating_data_1q_2021.xlsx"); err != nil {
		log.Error(err)
	}

}
