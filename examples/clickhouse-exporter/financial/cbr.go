package main

import (
	"database/sql"
	"fmt"
	"github.com/gocolly/colly/v2"
	log "github.com/sirupsen/logrus"
	"strconv"
	"strings"
	"time"
)

var (
	cbrUrl = "https://www.cbr.ru/hd_base/KeyRate/?UniDbQuery.Posted=True&UniDbQuery.From=17.09.2013&UniDbQuery.To=%s"
)

func loadCbrRate(conn *sql.DB, table map[time.Time]float32) error {
	var tx_cbrRates, _ = conn.Begin()
	var stmt_cbrRates, _ = tx_cbrRates.Prepare("INSERT INTO cbr_rates (*) VALUES (?, ?)")
	for date, rate := range table {
		if _, err := stmt_cbrRates.Exec(date, rate); err != nil {
			return err
		}
	}
	if err := tx_cbrRates.Commit(); err != nil {
		return err
	}
	return nil
}
func crawCbr(conn *sql.DB) error {
	c := colly.NewCollector()
	table := make(map[time.Time]float32)
	c.OnHTML(".table-wrapper .table .data tr", func(e *colly.HTMLElement) {
		date := e.DOM.Children().First().Text()
		rate := e.DOM.Children().Next().Text()
		dateTime, derr := time.Parse("02.01.2006", date)
		if derr != nil {
			log.Error(derr)
			return
		}
		rateFloat, ferr := strconv.ParseFloat(strings.Replace(rate, ",", ".", -1), 32)
		if ferr != nil {
			log.Error(ferr)
			return
		}
		table[dateTime] = float32(rateFloat)
	})
	if err := c.Visit(fmt.Sprintf(cbrUrl, time.Now().Format("01-02-2006"))); err != nil {
		return err
	}
	if err := loadCbrRate(conn, table); err != nil {
		return err
	}
	return nil
}
