package main

import (
	"database/sql"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	log "github.com/sirupsen/logrus"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var (
	exportQuarter    = map[string]string{}
	exportNLMKTables = map[string]map[string]string{}
)

func init() {
	exportQuarter = map[string]string{
		"Consolidated sales": "NLMK Group",
		"Key Indicators":     "KEY INDICATORS",
		"CashFlow":           "Consolidated statement of cash flows (in M'USD)",
		"PL":                 "Consolidated statement of profit or loss (in M'USD)",
		"Balance Sheet":      "Consolidated statement of financial position (in M'USD)",
		"RFP":                "RUSSIAN FLAT PRODUCTS",
	}
	exportNLMKTables["Consolidated sales"] = map[string]string{
		"SALES BY PRODUCT":   "consolidated_sales_for_products",
		"SALES BY REGION":    "consolidated_sales_for_products",
		"REVENUE BY PRODUCT": "revenue_structure",
		"REVENUE BY REGION":  "revenue_structure",
	}
	exportNLMKTables["CashFlow"] = map[string]string{
		"MULTIPLIES & OTHER INDICATORS":                                "financial_highlights",
		"CASH FLOWS FROM INVESTING ACTIVITIES":                         "financial_highlights",
		"Payments from settlement of derivative financial instruments": "financial_highlights",
		"Changes in operating assets and liabilities":                  "financial_highlights",
	}
	exportNLMKTables["PL"] = map[string]string{
		"MULTIPLIES & OTHER INDICATORS": "financial_highlights",
		"PROFIT AND LOSS":               "financial_highlights",
		"EBITDA":                        "financial_highlights",
		"Sales volume, '000 t":          "financial_ratios",
	}
	exportNLMKTables["Key Indicators"] = map[string]string{
		"Profitability": "financial_ratios",
		"Multiples":     "operational_highlights",
	}
	exportNLMKTables["Balance Sheet"] = map[string]string{
		"Current assets": "financial_ratios",
	}
	exportNLMKTables["RFP"] = map[string]string{
		"COST OF SALES": "cost_of_sales_structure",
		"SEGMENT SALES": "revenue_structure",
	}
	exportNLMKTables["Current assets"] = map[string]string{
		"Current assets": "financial_ratios",
	}
}
func getOPQuarters(rows *[][]string, quarterRowsName string) (map[int]string, []int) {
	quarters := map[int]string{}
	quarterIdxs := []int{}
	quarterRowsFound := false
	re, _ := regexp.Compile("([1-4]кв) [0-9]{4}")
	for _, row := range *rows {
		for j, cell := range row {
			if len(cell) == 0 {
				continue
			}
			if cell == quarterRowsName {
				quarterRowsFound = true
				continue
			}
			if quarterRowsFound && re.MatchString(cell) {
				quarters[j] = fmt.Sprintf("Q%s %s", cell[0:1], cell[len(cell)-4:])
				quarterIdxs = append(quarterIdxs, j)
			}
		}
		if quarterRowsFound {
			break
		}
	}
	return quarters, quarterIdxs
}

func getQuarters(rows *[][]string, quarterRowsName string, diff bool) (map[int]string, []int) {
	quarters := map[int]string{}
	quarterIdxs := []int{}
	quarterRowsFound := false
	for _, row := range *rows {
		for j, cell := range row {
			if len(cell) == 0 {
				continue
			}
			if cell == quarterRowsName {
				quarterRowsFound = true
				continue
			}

			if quarterRowsFound && isQuarter(cell) {
				quarters[j] = strings.Trim(cell, " *")
				quarterIdxs = append(quarterIdxs, j)
			} else if diff {
				re, _ := regexp.Compile("([0-9]M|FY) [0-9]{4}")
				var quarter string
				if re.MatchString(cell) {
					switch cell[0:2] {
					case "3M":
						quarter = "Q1"
					case "6M":
						quarter = "Q2"
					case "9M":
						quarter = "Q3"
					case "FY":
						quarter = "Q4"
					}
					quarters[j] = quarter + " " + cell[3:]
					quarterIdxs = append(quarterIdxs, j)
				}
			}
		}
		//if quarterRowsFound {
		//	break
		//}
	}
	return quarters, quarterIdxs
}

func getElasticTableFromRows(rows *[][]string, tableName string, quarterRowsName string, diff bool) *map[string]map[string]float64 {
	table := make(map[string]map[string]float64)
	quarters, quarterIdxs := getQuarters(rows, quarterRowsName, diff)
	log.Debugf("quarters row %+v", quarters)
	if len(quarterIdxs) == 0 {
		log.Warn("Quarter rows not found name ", quarterRowsName)
	}
	tableNameFound := false
	tableFieldIdx := 0
	for _, row := range *rows {
		tableField := ""
		tableFieldFound := false
		for j, cell := range row {
			if !tableNameFound && cell == tableName {
				tableFieldIdx = j
				tableNameFound = true
				if len(row) > len(quarterIdxs) {
					tableField = tableName
					table[tableField] = make(map[string]float64)
					tableFieldFound = true
					break
				}
			}
			if tableNameFound && j >= tableFieldIdx && cell != "" && len(tableField) == 0 {
				tableField = strings.Trim(cell, " :*")
				table[tableField] = make(map[string]float64)
				tableFieldFound = true
				break
			}
		}
		if tableNameFound && len(row) > tableFieldIdx && row[tableFieldIdx] != "" && row[tableFieldIdx] != tableName {
			if tableFieldFound {
				log.Debugf("end table row %+s", row[tableFieldIdx])
				break
			}
			//log.Debugf("skip row %+v", row)
			//continue
		}
		if tableNameFound && len(tableField) > 0 {
			valOld := float64(0)
			for _, idx := range quarterIdxs {
				if idx > len(row) {
					continue
				}
				quarterName := quarters[idx]
				val, _ := strconv.ParseFloat(row[idx], 32)
				if diff {
					valNew := val
					if quarterName[0:2] != "Q1" {
						val = valNew - valOld
						log.Debugf("%s =  %d - %d", quarterName, int(valNew), int(valOld))
					} else {
						log.Debugf("%s =  %d", quarterName, int(val))
					}
					valOld = valNew
				}
				table[tableField][quarterName] = val
			}
		}
	}
	log.Debugf("table %+v", table)
	return &table
}

// NLMK_Operating_Results_4Q_2021_RUS.xlsx
func loadNlmkOPData(conn *sql.DB, fileName string) error {
	secCode := "NLMK"
	dataBasePath := "data"
	if dir := os.Getenv("FINANCIAL_DATA_DIR"); dir != "" {
		dataBasePath = dir
	}
	xlsxFile, err := excelize.OpenFile(path.Join(dataBasePath, fileName))
	if err != nil {
		return err
	}
	rows, err := xlsxFile.GetRows(xlsxFile.GetSheetName(0))
	if err != nil {
		return err
	}
	quarters, quarterIdxs := getOPQuarters(&rows, "Производство, млн т")
	var tx_op, _ = conn.Begin()
	var stmt_op, _ = tx_op.Prepare("INSERT INTO operational_databook (*) VALUES (?)")

	rowFieldNum := 1
	rowFirstValueNum := quarterIdxs[0]
	rowFields := map[string]bool{
		"КЛЮЧЕВЫЕ ОПЕРАЦИОННЫЕ ПОКАЗАТЕЛИ": true,
		"ПРОДАЖИ":      true,
		"ПРОИЗВОДСТВО": true,
	}
	var tableName string
	var category string
	for _, row := range rows {
		if len(row) < rowFirstValueNum+1 {
			continue
		}
		if row[rowFirstValueNum] == "" {
			if row[rowFieldNum] == strings.ToUpper(row[rowFieldNum]) && rowFields[row[rowFieldNum]] {
				tableName = row[rowFieldNum]
				continue
			}
			if row[rowFieldNum] != "" {
				category = row[rowFieldNum]
			}
			continue
		}
		if strings.Contains(row[rowFirstValueNum], "кв") {
			continue
		}
		for _, idx := range quarterIdxs {
			quarter := quarters[idx]
			value, _ := strconv.ParseFloat(strings.Trim(strings.TrimSpace(row[idx]), "%"), 32)
			if _, err := stmt_op.Exec(secCode, tableName, category, quarterToDate(quarter), row[rowFieldNum], value); err != nil {
				return err
			}
		}
	}
	if err := tx_op.Commit(); err != nil {
		return err
	}
	return nil
}

// financial_and_operating_data_1q_2021.xlsx
func loadNlmkData(conn *sql.DB, fileName string) error {
	secCode := "NLMK"
	dataBasePath := "data"
	if dir := os.Getenv("FINANCIAL_DATA_DIR"); dir != "" {
		dataBasePath = dir
	}
	xlsxFile, err := excelize.OpenFile(path.Join(dataBasePath, fileName))
	if err != nil {
		return err
	}
	for sheet, tableNames := range exportNLMKTables {
		rows, err := xlsxFile.GetRows(sheet)
		if err != nil {
			if strings.HasSuffix(err.Error(), "is not exist") {
				log.Warn(err)
				continue
			}
			return err
		}
		for name, table := range tableNames {
			vals := make([]string, TableColumnNums[table])
			for i := 0; i < TableColumnNums[table]; i++ {
				vals = append(vals, "?")
			}
			if err := insertToDB(conn, secCode, "",
				fmt.Sprintf("INSERT INTO %s (*) VALUES (%s)", table, strings.Join(vals, ",")),
				getElasticTableFromRows(&rows, name, exportQuarter[sheet], false),
			); err != nil {
				return err
			}
		}
	}
	return nil
}
