package main

import (
	"database/sql"
	"fmt"
	"github.com/shakinm/xlsReader/xls"
	log "github.com/sirupsen/logrus"
	"os"
	"path"
)

var (
	exportMAGNTables = map[string]map[string]string{}
)

func init() {
	exportMAGNTables["Production & Sales"] = map[string]string{
		"MMK GROUP: CONSOLIDATED SALES":      "operational_highlights",
		"MMK GROUP: CONSOLIDATED PRODUCTION": "productions",
	}
	exportMAGNTables["Highlights"] = map[string]string{
		"OPERATIONAL HIGHLIGHTS": "operational_highlights",
		"FINANCIAL HIGHLIGHTS":   "financial_highlights",
	}
	exportMAGNTables["CONS Prices"] = map[string]string{
		"MMK GROUP: CONSOLIDATED PRICES FOR METAL PRODUCTS": "consolidated_prices_for_products",
		"MMK GROUP: SLAB CASH COST STRUCTURE":               "slab_cash_cost_structure",
		"MMK GROUP: FOB PRICES FOR HRC":                     "fob_prices",
	}
	exportMAGNTables["CONS Sales structure"] = map[string]string{
		"MMK GROUP: CONSOLIDATED SALES": "consolidated_sales_for_products",
		"MMK GROUP SALES: EXPORT":       "export_sales_for_products",
	}
	exportMAGNTables["COS breakdown"] = map[string]string{
		"MMK GROUP: COST OF SALES STRUCTURE":  "cost_of_sales_structure",
		"MMK GROUP: MATERIAL COSTS STRUCTURE": "material_cost_structure",
	}
	exportMAGNTables["Production breakdown"] = map[string]string{
		"PJSC MMK PRODUCTION":                     "productions",
		"COAL MINING SEGMENT PRODUCTION":          "productions",
		"MMK METALURJI (STEEL TURKEY) PRODUCTION": "productions",
		"PJSC MMK PRICES FOR FINISHED PRODUCTS":   "prices_for_products",
	}
	exportMAGNTables["Ratios"] = map[string]string{
		"FINANCIAL RATIOS": "financial_ratios",
	}
	exportMAGNTables["Balance Sheet"] = map[string]string{
		"Current assets": "financial_ratios",
	}
}

func loadMagnData(conn *sql.DB, fileName string) error {
	secCode := "MAGN"
	dataBasePath := "data"
	if dir := os.Getenv("FINANCIAL_DATA_DIR"); dir != "" {
		dataBasePath = dir
	}
	workbook, err := xls.OpenFile(path.Join(dataBasePath, fileName))
	if err != nil {
		return err
	}
	log.Debugf("workbook sheets number %+v", workbook.GetNumberSheets())
	for _, sheet := range workbook.GetSheets() {
		sheetName := sheet.GetName()
		if _, ok := exportMAGNTables[sheetName]; !ok {
			log.Debugf("skip sheet name %s", sheetName)
			continue
		}
		for tableName, table := range exportMAGNTables[sheetName] {
			tableData := getTable(sheet, tableName)
			secCodeIns := secCode
			if table == "fob_prices" {
				secCodeIns = ""
			}
			if table == "productions" {
				if err := insertToDB(conn, secCodeIns, tableName,
					fmt.Sprintf("INSERT INTO %s (*) VALUES (%s)", table, "?"),
					tableData,
				); err != nil {
					log.Debug(tableData)
					return err
				}
			} else {
				if err := insertToDB(conn, secCodeIns, "",
					fmt.Sprintf("INSERT INTO %s (*) VALUES (%s)", table, "?"),
					tableData,
				); err != nil {
					log.Debug(tableData)
					return err
				}
			}
		}
	}
	return nil
}
