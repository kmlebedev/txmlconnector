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

func getTableRowInx(sheet xls.Sheet, tableName string) int {
	for i, row := range sheet.GetRows() {
		if cell, err := row.GetCol(0); err == nil && cell.GetString() == tableName {
			nextRow, _ := sheet.GetRow(i + 1)
			if nextCell, _ := nextRow.GetCol(1); strings.HasPrefix(nextCell.GetString(), "Q") {
				return i
			}
			continue
		}
	}
	return -1
}

func getTable(sheet xls.Sheet, tableName string) *map[string]map[string]float64 {
	table := make(map[string]map[string]float64)
	quarters := map[int]string{}
	tableRowIdx := getTableRowInx(sheet, tableName)
	blankRowFound := false
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
					tableKey = cel.GetString()
					table[cel.GetString()] = make(map[string]float64)
					log.Debugf("table kye name %s", cel.GetString())
				}
				continue
			} else if isQuarterRow(tableRowIdx, n) {
				if strings.HasPrefix(cel.GetString(), "Q") {
					log.Debugf("quarter %s", cel.GetString())
					quarters[az] = cel.GetString()
				}
				continue
			}
			if az > 0 && isBlankCell(cel) {
				break
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

	if err := loadChmfData(conn); err != nil {
		log.Error(err)
	}
	return
	if err := loadCSV(conn); err != nil {
		log.Error(err)
	}
	if err := loadLmeData(conn); err != nil {
		log.Error(err)
	}
	if err := loadInvestingData(conn); err != nil {
		log.Error(err)
	}
	if err := loadMagnData(conn); err != nil {
		log.Error(err)
	}
}
