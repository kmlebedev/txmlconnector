package exporter

import (
	"database/sql"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	log "github.com/sirupsen/logrus"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// curl 'https://www.investing.com/members-admin/auth/signInByEmail/' \
//  -H 'user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/92.0.4515.107 Safari/537.36' \
//  -H 'content-type: application/x-www-form-urlencoded' \
//  --data-raw 'email=lebedev.k.m%40gmail.com&password=qotawf0316&logintoken=6abc2c3cee048de2e039e8e94a80a1d3&onAuthCompleteAction=%7B%22type%22%3A%22topBar%22%2C%22location%22%3A%7B%22path%22%3A%22%2F%22%2C%22search%22%3A%22%22%2C%22hash%22%3A%22%22%7D%2C%22mmID%22%3A1%7D' \
//  --compressed
var (
	client            http.Client
	host              = "www.investing.com"
	contentType       = "application/x-www-form-urlencoded"
	signInUrl         = fmt.Sprintf("https://%s/members-admin/auth/signInByEmail/", host)
	historicalDataUrl = fmt.Sprintf("https://%s/instruments/HistoricalDataAjax", host)
	signIn            = false
	CodeToId          = map[string][]string{
		"MHCc1":    {"1128865", "27886385"},
		"SHHCc1":   {"996735", "301529"},
		"SGXIOSc1": {"992748", "301009"},
		"DJMc1":    {"961743", "301177"},
		"DCIOU1":   {"961741", "301009"}, // Iron ore fines 62% Fe
	}
)

type AddHeaderTransport struct {
	T http.RoundTripper
}

func (adt *AddHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/92.0.4515.107 Safari/537.36'")
	return adt.T.RoundTrip(req)
}

func NewAddHeaderTransport(T http.RoundTripper) *AddHeaderTransport {
	if T == nil {
		T = http.DefaultTransport
	}
	return &AddHeaderTransport{T}
}

func init() {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatalf("Got error while creating cookie jar %s", err.Error())
	}
	client = http.Client{
		Jar:       jar,
		Transport: NewAddHeaderTransport(nil),
	}
}

func login() {
	if signIn {
		return
	}
	payload := strings.NewReader("email=" + url.QueryEscape(os.Getenv("INVESTING_EMAIL")) + "&password=" + url.QueryEscape(os.Getenv("INVESTING_PASSWORD")))
	_, err := client.Post(signInUrl, contentType, payload)
	if err != nil {
		log.Error(err)
	}
	signIn = true
}

func getDataRealValue(s *goquery.Selection) float32 {
	priceStr, ok := s.Attr("data-real-value")
	if !ok {
		log.Error("not attr data-real-value %+v", s)
		return 0
	}
	if strings.Contains(priceStr, ",") {
		priceStr = strings.Replace(priceStr, ",", "", -1)
	}
	if price, err := strconv.ParseFloat(priceStr, 32); err == nil {
		return float32(price)
	} else {
		log.Error(err)
	}
	return 0
}

//curl 'https://www.investing.com/instruments/HistoricalDataAjax' \
//  --data-raw 'curr_id=1128865&smlID=27886385&header=STEEL+HRC+FOB+CHINA+Futures+Historical+Data&st_date=07%2F07%2F2020&end_date=08%2F07%2F2021&interval_sec=Daily&sort_col=date&sort_ord=DESC&action=historical_data' \
func getHistoricalData(code string, startDate string, endDate string, interval string) (map[time.Time][]float32, error) {
	login()
	params := url.Values{
		"curr_id":      {CodeToId[code][0]},
		"smlID":        {CodeToId[code][1]},
		"st_date":      {startDate},
		"end_date":     {endDate},
		"interval_sec": {interval},
		"sort_col":     {"date"},
		"sort_ord":     {"DESC"},
		"action":       {"historical_data"},
	}
	params.Encode()
	req, err := http.NewRequest("POST", historicalDataUrl, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("status code error: %d %s", res.StatusCode, res.Status)
	}
	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, err
	}
	out := map[time.Time][]float32{}
	doc.Find("tbody tr").Each(func(i int, s *goquery.Selection) {
		col0 := s.Children()
		unixTimeSting, ok := col0.Attr("data-real-value")
		if !ok {
			return
		}
		ut, err := strconv.ParseInt(unixTimeSting, 10, 64)
		if err != nil {
			log.Error(err)
		}
		date := time.Unix(ut, 0)
		col1 := col0.Next() // Price
		col2 := col1.Next() // Open
		col3 := col2.Next() // High
		col4 := col3.Next() // Low
		col5 := col4.Next() //Vol

		out[date] = []float32{
			getDataRealValue(col1),
			getDataRealValue(col2),
			getDataRealValue(col3),
			getDataRealValue(col4),
			getDataRealValue(col5),
		}
	})
	return out, nil
}

func LoadHistoricalData(conn *sql.DB, code string, startDate string, endDate string, interval string) error {
	var tx, _ = conn.Begin()
	var stmt, _ = tx.Prepare("INSERT INTO stock_prices (*) VALUES (?, ?, ?, ?, ?, ?, ?)")
	date, err := getHistoricalData(code, startDate, endDate, interval)
	if err != nil {
		return err
	}
	for t, v := range date {
		if _, err := stmt.Exec(code, t, v[0], v[1], v[2], v[3], int(v[4])); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
