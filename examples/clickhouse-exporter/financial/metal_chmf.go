package main

import (
	"database/sql"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	log "github.com/sirupsen/logrus"
	"os"
	"strconv"
	"strings"
)

var (
	exportTables    = map[string]map[string]string{}
	quarterToMonths = map[string]string{
		"Q1": "Three months ended 31 March",
		"Q2": "Six months ended 30 June",
		"Q3": "Nine months ended 30 September",
		"Q4": "Twelve months",
	}
)

func init() {
	exportTables["Operational results"] = map[string]string{
		"Sales volumes, thousands of tonnes": "consolidated_sales_for_products",
		"Sales price, $/tonne":               "prices_for_products",
	}
	exportTables["PL"] = map[string]string{
		"Consolidated income statements": "financial_highlights",
	}
	exportTables["CF"] = map[string]string{
		"Consolidated statements of cash flows": "financial_highlights",
	}
}

func getTableFromRows(rows *[][]string, name string) *map[string]map[string]float64 {
	table := make(map[string]map[string]float64)
	quarters := map[int]string{}
	tableFound := false
	QuarterFound := false
	tableBlankRows := 0
	isQuarterCols := false
	for _, row := range *rows {
		if len(row) == 0 {
			continue
		}
		if (row[0] == name || tableFound) && !QuarterFound {
			for _, m := range quarterToMonths {
				if strings.HasPrefix(row[1], m) {
					QuarterFound = true
				}
			}
			if QuarterFound {
				for j := 1; j < len(row); j++ {
					for q, m := range quarterToMonths {
						if strings.HasPrefix(row[j], m) {
							log.Debugf("%s quarter %s To Months %s", row[j], q, m)
							quarters[j] = fmt.Sprintf("%s %s", q, row[j][len(m)+1:len(m)+5])
						}
					}
				}
				log.Debugf("row with months quarters found %+v", quarters)
				continue
			}
			if strings.HasPrefix(row[1], "Q") {
				for j := 1; j < len(row); j++ {
					if len(row[j]) < 7 || !strings.HasPrefix(row[j], "Q") {
						continue
					}
					quarters[j] = row[j][0:7]
				}
				isQuarterCols = true
				QuarterFound = true
				log.Debugf("row with quarters found %+v", quarters)
			}
			tableFound = true
			log.Debugf("Table name %s found", name)
			continue
		}
		if !tableFound || !QuarterFound {
			continue
		}
		if row[0] == "" {
			if tableBlankRows > 1 {
				break
			}
			tableBlankRows += 1
		} else {
			tableBlankRows = 0
		}
		log.Debugf("row %s", row[0])
		table[row[0]] = make(map[string]float64)
		quarterIdxs := []int{}
		for j, _ := range quarters {
			quarterIdxs = append(quarterIdxs, j)
		}
		for n, j := range quarterIdxs {
			quarter := quarters[j]
			if len(row) < quarterIdxs[len(quarterIdxs)-1] {
				continue
			}
			if len(row[j]) == 0 {
				continue
			}
			value := row[j]
			if strings.HasSuffix(value, "%") {
				value = row[j][:len(value)-1]
			}
			if val, err := strconv.ParseFloat(value, 32); err == nil {
				if isQuarterCols || strings.HasPrefix(quarter, "Q1") {
					table[row[0]][quarter] = val
				} else if len(quarterIdxs) > n+1 && !strings.HasPrefix(quarters[quarterIdxs[n+1]], "Q4") {
					valPrev, err := strconv.ParseFloat(row[quarterIdxs[n+1]], 32)
					if err == nil {
						table[row[0]][quarter] = val - valPrev
					} else {
						log.Debugf("Failed parse valPrev %s", row[quarterIdxs[n+1]])
					}
				}
			} else {
				log.Debugf("Failed parse %s", value)
			}

		}
	}
	log.Debugf("table %+v", table)
	return &table
}

func loadChmfData(conn *sql.DB) error {
	secCode := "CHMF"
	dataBasePath := "data"
	if dir := os.Getenv("FINANCIAL_DATA_DIR"); dir != "" {
		dataBasePath = dir
	}
	xlsxFile, err := excelize.OpenFile(dataBasePath + "/Q1_2021-Financial_and_operational_data-Severstal_Final.xlsx")
	if err != nil {
		return err
	}
	for sheet, tableNames := range exportTables {
		rows, err := xlsxFile.GetRows(sheet)
		if err != nil {
			return err
		} else if len(rows) < 2 {
			return fmt.Errorf("In table %s not enough row < 2", tableNames)
		}
		for name, table := range tableNames {
			vals := make([]string, TableColumnNums[table])
			for i := 0; i < TableColumnNums[table]; i++ {
				vals = append(vals, "?")
			}
			if err := insertToDB(conn, secCode,
				fmt.Sprintf("INSERT INTO %s (*) VALUES (%s)", table, strings.Join(vals, ",")),
				getTableFromRows(&rows, name),
			); err != nil {
				return err
			}
		}
	}
	return nil
}
