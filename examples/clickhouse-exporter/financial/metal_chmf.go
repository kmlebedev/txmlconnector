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
	exportTables    = map[string]map[string]string{}
	quarterToMonths = map[string]string{
		"Q1": "Three months ended 31 March",
		"Q2": "Six months ended 30 June",
		"Q3": "Nine months ended 30 September",
		"Q4": "Twelve months",
	}
	twelveMonths = "Twelve months"
)

func init() {
	exportTables["Operational results"] = map[string]string{
		"SUMMARY OF KEY PRODUCTION, SALES VOLUMES":                   "productions",
		"SEVERSTAL’S CONSOLIDATED SALES (NET OF INTERCOMPANY SALES)": "consolidated_sales_for_products",
		"Sales price, $/tonne":                                       "prices_for_products",
	}
	exportTables["PL"] = map[string]string{
		"Consolidated income statements": "financial_highlights",
	}
	exportTables["CF"] = map[string]string{
		"Consolidated statements of cash flows": "financial_highlights",
	}
	exportTables["revenue results"] = map[string]string{
		"consolidated_revenue_for_products": "revenue_structure",
		"consolidated_revenue_for_export":   "revenue_structure",
	}
}

func isQuarter(name string) bool {
	return strings.HasPrefix(name, "Q") || strings.HasPrefix(name, twelveMonths)
}

func getTableFromRows(rows *[][]string, name string) *map[string]map[string]float64 {
	table := make(map[string]map[string]float64)
	quarters := map[int]string{}
	quarterIdxs := []int{}
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
							quarterStr := strings.Trim(row[j], " *")
							log.Debugf("%s quarter %s To Months %s", quarterStr, q, m)
							quarters[j] = fmt.Sprintf("%s %s", q, quarterStr[len(m)+1:len(m)+5])
							quarterIdxs = append(quarterIdxs, j)
						}
					}
				}
				log.Debugf("row with months quarters found %+v", quarters)
				continue
			}
			if isQuarter(row[1]) {
				for j := 1; j < len(row); j++ {
					quarter := strings.Trim(row[j], " *")
					if len(row[j]) < 7 || !isQuarter(quarter) {
						continue
					}
					quarters[j] = quarter
					quarterIdxs = append(quarterIdxs, j)
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
		field := strings.Trim(row[0], " *:")
		log.Debugf("row %s", field)
		table[field] = make(map[string]float64)
		for n, j := range quarterIdxs {
			rq := regexp.MustCompile("^(Q[1-4]\\s+\\d+)")
			rqFind := rq.FindStringSubmatch(quarters[j])
			quarter := quarters[j][0:7]
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
				if isQuarterCols {
					if strings.HasPrefix(quarters[j], twelveMonths) {
						if len(quarterIdxs) > n+2 &&
							strings.HasPrefix(quarters[quarterIdxs[n+1]], "Q3") {
							quarter = fmt.Sprintf("Q4 %s", quarters[j][len(quarters[j])-4:])
							log.Debugf("In twelveMonthsFound %s", quarter)
							valQ3, _ := strconv.ParseFloat(row[quarterIdxs[n+1]], 32)
							valQ2, _ := strconv.ParseFloat(row[quarterIdxs[n+2]], 32)
							valQ1, _ := strconv.ParseFloat(row[quarterIdxs[n+3]], 32)
							table[field][quarter] = val - valQ3 - valQ2 - valQ1
						}
					} else if name == "Consolidated income statements" && strings.HasPrefix(quarter, "Q2 2021") {
						valQ1, _ := strconv.ParseFloat(row[quarterIdxs[n+1]], 32)
						table[field][quarter] = val - valQ1
					} else {
						table[field][quarter] = val
					}
				} else if len(rqFind) > 0 {
					quarter := rqFind[1]
					log.Debugf("quarter %s", quarter)
					if strings.HasPrefix(quarter, "Q1") {
						table[field][quarter] = val
						continue
					}
					if len(quarterIdxs) > n+1 && !strings.HasPrefix(quarters[quarterIdxs[n+1]], "Q4") {
						valPrev, err := strconv.ParseFloat(row[quarterIdxs[n+1]], 32)
						if err == nil {
							table[field][quarter] = val - valPrev
						} else {
							log.Debugf("Failed parse valPrev %s", row[quarterIdxs[n+1]])
						}
					}
				}
			} else {
				log.Debugf("Failed parse %s", quarters[j])
			}
		}
	}
	log.Debugf("table %+v", table)
	return &table
}

// CHMF_revenue_structure.xlsx
func loadChmfData(conn *sql.DB, fileName string) error {
	secCode := "CHMF"
	dataBasePath := "data"
	if dir := os.Getenv("FINANCIAL_DATA_DIR"); dir != "" {
		dataBasePath = dir
	}
	xlsxFile, err := excelize.OpenFile(path.Join(dataBasePath, fileName))
	if err != nil {
		return err
	}
	for sheet, tableNames := range exportTables {
		rows, err := xlsxFile.GetRows(sheet)
		if err != nil {
			if strings.HasSuffix(err.Error(), "is not exist") {
				log.Warn(err)
				continue
			}
			return err
		} else if len(rows) < 2 {
			return fmt.Errorf("In table %s not enough row < 2", tableNames)
		}
		for name, table := range tableNames {
			vals := make([]string, TableColumnNums[table])
			for i := 0; i < TableColumnNums[table]; i++ {
				vals = append(vals, "?")
			}
			if err := insertToDB(conn, secCode, "",
				fmt.Sprintf("INSERT INTO %s (*) VALUES (%s)", table, strings.Join(vals, ",")),
				getTableFromRows(&rows, name),
			); err != nil {
				return err
			}
		}
	}
	return nil
}
