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
	dataBasePath := "data"
	if dir := os.Getenv("FINANCIAL_DATA_DIR"); dir != "" {
		dataBasePath = dir
	}
	conn := initDB()
	log.Debugf("connected %+v", conn.Stats())

	if err := loadLmeData(conn); err != nil {
		log.Error(err)
	}
	if err := loadInvestingData(conn); err != nil {
		log.Error(err)
	}
	secCode := "MMK"
	workbook, err := xls.OpenFile(dataBasePath + "/MMK_operating_e_financial_data_Q1_2021.xls")
	if err != nil {
		log.Error(err)
	}
	log.Debugf("workbook sheets number %+v", workbook.GetNumberSheets())
	for _, sheet := range workbook.GetSheets() {
		sheetName := sheet.GetName()
		switch {
		case sheetName == "Highlights":
			if err := insertToDB(conn, secCode,
				"INSERT INTO operational_highlights (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "OPERATIONAL HIGHLIGHTS"),
			); err != nil {
				log.Error(err)
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO financial_highlights (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "FINANCIAL HIGHLIGHTS"),
			); err != nil {
				log.Error(err)
			}
		case sheetName == "CONS Prices":
			if err := insertToDB(conn, secCode,
				"INSERT INTO consolidated_prices_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: CONSOLIDATED PRICES FOR METAL PRODUCTS"),
			); err != nil {
				log.Error(err)
			}
			if err := insertToDB(conn, "",
				"INSERT INTO fob_prices (*) VALUES (?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: FOB PRICES FOR HRC"),
			); err != nil {
				log.Error(err)
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO slab_cash_cost_structure (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: SLAB CASH COST STRUCTURE"),
			); err != nil {
				log.Error(err)
			}
		case sheetName == "CONS Sales structure":
			if err := insertToDB(conn, secCode,
				"INSERT INTO consolidated_sales_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: CONSOLIDATED SALES"),
			); err != nil {
				log.Error(err)
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO export_sales_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP SALES: EXPORT"),
			); err != nil {
				log.Error(err)
			}
		//
		case sheetName == "COS breakdown":
			if err := insertToDB(conn, secCode,
				"INSERT INTO cost_of_sales_structure (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: COST OF SALES STRUCTURE"),
			); err != nil {
				log.Error(err)
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO material_cost_structure (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: MATERIAL COSTS STRUCTURE"),
			); err != nil {
				log.Error(err)
			}
		case sheetName == "Production breakdown":
			if err := insertToDB(conn, secCode,
				"INSERT INTO productions (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "PJSC MMK PRODUCTION"),
			); err != nil {
				log.Error(err)
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO prices_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "PJSC MMK PRICES FOR FINISHED PRODUCTS"),
			); err != nil {
				log.Error(err)
			}
		case sheetName == "Ratios":
			if err := insertToDB(conn, secCode,
				"INSERT INTO financial_ratios (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "FINANCIAL RATIOS"),
			); err != nil {
				log.Error(err)
			}
		default:
			log.Debugf("skip sheet name %s", sheetName)
			continue
		}
	}
}
