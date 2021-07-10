package main

import (
	"database/sql"
	"github.com/shakinm/xlsReader/xls"
	log "github.com/sirupsen/logrus"
	"os"
	"path"
)

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
		switch {
		case sheetName == "Highlights":
			if err := insertToDB(conn, secCode,
				"INSERT INTO operational_highlights (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "OPERATIONAL HIGHLIGHTS"),
			); err != nil {
				return err
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO financial_highlights (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "FINANCIAL HIGHLIGHTS"),
			); err != nil {
				return err
			}
		case sheetName == "CONS Prices":
			if err := insertToDB(conn, secCode,
				"INSERT INTO consolidated_prices_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: CONSOLIDATED PRICES FOR METAL PRODUCTS"),
			); err != nil {
				return err
			}
			if err := insertToDB(conn, "",
				"INSERT INTO fob_prices (*) VALUES (?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: FOB PRICES FOR HRC"),
			); err != nil {
				return err
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO slab_cash_cost_structure (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: SLAB CASH COST STRUCTURE"),
			); err != nil {
				return err
			}
		case sheetName == "CONS Sales structure":
			if err := insertToDB(conn, secCode,
				"INSERT INTO consolidated_sales_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: CONSOLIDATED SALES"),
			); err != nil {
				return err
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO export_sales_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP SALES: EXPORT"),
			); err != nil {
				return err
			}
		case sheetName == "COS breakdown":
			if err := insertToDB(conn, secCode,
				"INSERT INTO cost_of_sales_structure (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: COST OF SALES STRUCTURE"),
			); err != nil {
				return err
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO material_cost_structure (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "MMK GROUP: MATERIAL COSTS STRUCTURE"),
			); err != nil {
				return err
			}
		case sheetName == "Production breakdown":
			if err := insertToDB(conn, secCode,
				"INSERT INTO productions (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "PJSC MMK PRODUCTION"),
			); err != nil {
				return err
			}
			if err := insertToDB(conn, secCode,
				"INSERT INTO prices_for_products (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "PJSC MMK PRICES FOR FINISHED PRODUCTS"),
			); err != nil {
				return err
			}
		case sheetName == "Ratios":
			if err := insertToDB(conn, secCode,
				"INSERT INTO financial_ratios (*) VALUES (?, ?, ?, ?, ?)",
				getTable(sheet, "FINANCIAL RATIOS"),
			); err != nil {
				return err
			}
		default:
			log.Debugf("skip sheet name %s", sheetName)
			continue
		}
	}
	return nil
}
