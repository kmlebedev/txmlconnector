package main

import (
	"database/sql"
	"github.com/ClickHouse/clickhouse-go"
	log "github.com/sirupsen/logrus"
	"os"
	"time"
)

var (
	TableColumnNums = map[string]int{
		"consolidated_sales_for_products": 5,
	}
	createExportGoodsStats = `
		CREATE TABLE IF NOT EXISTS export_goods (
		   code LowCardinality(String),
           name LowCardinality(String),
		   month Date,
		   quantity Float32,
           value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (code, month)
	`
	createTableRzd = `
		CREATE TABLE IF NOT EXISTS loading_rzd (
		   name FixedString(4),
		   month Date,
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (name, month)
	`
	createTableRevenue = `
		CREATE TABLE IF NOT EXISTS revenue_structure (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   structure LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, structure)
	`
	createTableCsvClosingPrices = `
		CREATE TABLE IF NOT EXISTS csv_closing_prices (
		   name LowCardinality(String),
		   date Date,
		   close Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (name, date)
	`
	createTableLmeClosingPrices = `
		CREATE TABLE IF NOT EXISTS lme_closing_prices (
		   code FixedString(2),
		   date Date,
		   M1 Float32,
		   M2 Float32,
		   M3 Float32,
		   M4 Float32,
		   M5 Float32,
		   M6 Float32,
		   M7 Float32,
		   M8 Float32,
		   M9 Float32,
		   M10 Float32,
		   M11 Float32,
		   M12 Float32,
		   M13 Float32,
		   M14 Float32,
		   M15 Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (code, date)
	`
	createTableStockPrices = `
		CREATE TABLE IF NOT EXISTS stock_prices (
		   code LowCardinality(String),
		   date Date,
		   close Float32,
		   open Float32,
		   max Float32,
		   min Float32,
		   volume UInt64
		) ENGINE = ReplacingMergeTree()
		ORDER BY (code, date)
	`
	createTableProductions = `
		CREATE TABLE IF NOT EXISTS productions (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   production LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, production)
	`
	createTablePriceProducts = `
		CREATE TABLE IF NOT EXISTS prices_for_products (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   product LowCardinality(String),
		   price Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, product)
	`
	createTableCostSales = `
		CREATE TABLE IF NOT EXISTS cost_of_sales_structure (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   structure LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, structure)
	`
	createTableMaterialCostSales = `
		CREATE TABLE IF NOT EXISTS material_cost_structure (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   material LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, material)
	`
	createTableExportSales = `
		CREATE TABLE IF NOT EXISTS export_sales_for_products (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   product LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, product)
	`
	createTableSales = `
		CREATE TABLE IF NOT EXISTS consolidated_sales_for_products (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   product LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, product)
	`
	createTableSlabStructure = `
		CREATE TABLE IF NOT EXISTS slab_cash_cost_structure (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   structure LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, structure)
	`
	createTableFobPrices = `
		CREATE TABLE IF NOT EXISTS fob_prices (
		   quarter Date,
		   quarter_name LowCardinality(String),
		   product LowCardinality(String),
		   price Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (quarter, product)
	`
	createTablePrices = `
		CREATE TABLE IF NOT EXISTS consolidated_prices_for_products (
		   sec_code FixedString(4),
		   quarter Date,
		   quarter_name LowCardinality(String),
		   product LowCardinality(String),
		   price Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, product)
	`
	createTableOperationalHighlights = `
		CREATE TABLE IF NOT EXISTS operational_highlights (
		   sec_code FixedString(4),
		   quarter   Date,
		   quarter_name LowCardinality(String),
		   product LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, product)
	`
	createTableFinancialRatios = `
		CREATE TABLE IF NOT EXISTS financial_ratios (
		   sec_code FixedString(4),
		   quarter   Date,
		   quarter_name LowCardinality(String),
		   indicator LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, indicator)
	`
	createTableFinancialHighlights = `
		CREATE TABLE IF NOT EXISTS financial_highlights (
		   sec_code FixedString(4),
		   quarter   Date,
		   quarter_name LowCardinality(String),
		   indicator LowCardinality(String),
		   value Float32
		) ENGINE = ReplacingMergeTree()
		ORDER BY (sec_code, quarter, indicator)
	`
)

func initDB() *sql.DB {
	clickhouseUrl := "tcp://127.0.0.1:9000"
	if chUrl := os.Getenv("CLICKHOUSE_URL"); chUrl != "" {
		clickhouseUrl = chUrl
	}
	var connect *sql.DB
	var err error
	for i := 0; i < 10; i++ {
		log.Infof("Try connect to clickhouse %s", clickhouseUrl)
		if connect, err = sql.Open("clickhouse", clickhouseUrl); err != nil {
			log.Fatal(err)
		}
		if err := connect.Ping(); err != nil {
			if exception, ok := err.(*clickhouse.Exception); ok {
				log.Infof("[%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
			}
			log.Warn(err)
		} else {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if connect == nil {
		log.Fatal(err)
	}
	for _, query := range []string{createExportGoodsStats, createTableRzd, createTableRevenue, createTableCsvClosingPrices, createTableFinancialRatios, createTableSlabStructure, createTableFobPrices, createTableStockPrices, createTablePriceProducts, createTableProductions, createTableLmeClosingPrices, createTableCostSales, createTableMaterialCostSales, createTableExportSales, createTableSales, createTablePrices, createTableOperationalHighlights, createTableFinancialHighlights} {
		if _, err := connect.Exec(query); err != nil {
			log.Fatal(err)
		}
	}
	return connect
}
