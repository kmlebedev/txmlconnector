package commands

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

type ServerStatus struct {
	XMLName   xml.Name `xml:"server_status"`
	Id        int      `xml:"id,attr"`                  // ID сервера
	Connected string   `xml:"connected,attr"`           // true/false/error
	Recover   string   `xml:"recover,attr,omitempty"`   // true/атрибут отсутствует
	ServerTZ  string   `xml:"server_tz,attr,omitempty"` // имя таймзоны сервер
	SysVer    int      `xml:"sys_ver,attr,omitempty"`   // версия системы
	Build     int      `xml:"build,attr,omitempty"`     // билд сервера
}

type Command struct {
	XMLName    xml.Name      `xml:"command"`
	Id         string        `xml:"id,attr"`
	Union      string        `xml:"union,attr,omitempty"`
	Client     string        `xml:"client,attr,omitempty"`
	SecId      int           `xml:"secid,attr,omitempty"`
	Period     int           `xml:"period,attr,omitempty"`
	Count      int           `xml:"count,attr,omitempty"`
	Reset      string        `xml:"reset,attr,omitempty"`
	AllTrades  SubAllTrades  `xml:"alltrades,omitempty"`  // подписка на сделки рынка
	Quotations []SubSecurity `xml:"quotations,omitempty"` // подписка на изменения показателей торгов
	Quotes     []SubSecurity `xml:"quotes,omitempty"`     // подписка на изменения «стакана»
}

type SubAllTrades struct {
	XMLName xml.Name `xml:"alltrades,omitempty"`
	Items   []int    `xml:"secid,omitempty"`
}

type SubSecurity struct {
	SecId int `xml:"secid,omitempty"`
	//Security struct {
	//	Board   string `xml:"board,omitempty"`
	//	SecCode string `xml:"seccode,omitempty"`
	//} `xml:"security,omitempty"`
}

type Connect struct {
	XMLName        xml.Name `xml:"command"`
	Id             string   `xml:"id,attr"`
	Login          string   `xml:"login"`
	Password       string   `xml:"password"`
	Host           string   `xml:"host"`
	Port           string   `xml:"port"`
	Language       string   `xml:"language,omitempty"`
	Autopos        bool     `xml:"autopos,omitempty"`
	MicexRegisters bool     `xml:"micex_registers,omitempty"`
	Milliseconds   bool     `xml:"milliseconds,omitempty"`
	UtcTime        bool     `xml:"utc_time,omitempty"`
	Rqdelay        int      `xml:"rqdelay,default:100"`       // Период агрегирования данных
	SessionTimeout int      `xml:"session_timeout,omitempty"` // Таймаут на сессию в секундах
	RequestTimeout int      `xml:"request_timeout,omitempty"` // Таймаут на запрос в секундах
	PushUlimits    int      `xml:"push_u_limits,omitempty"`   // Период в секундах
	PushPosEquity  int      `xml:"push_pos_equity,omitempty"` // Период в секундах
}

type Markets struct {
	XMLName xml.Name `xml:"markets"`
	Items   []struct {
		ID   int    `xml:"id,attr"`
		Name string `xml:",chardata"`
	} `xml:"market"`
}

type CandleKinds struct {
	XMLName xml.Name `xml:"candlekinds"`
	Items   []struct {
		ID     int    `xml:"id"`
		Period int    `xml:"period"`
		Name   string `xml:"name"`
	} `xml:"kind"`
}

// Справочник режимов торгов
type Boards struct {
	XMLName xml.Name `xml:"boards"`
	Items   struct {
		ID     string `xml:"id,attr"` // Идентификатор режима торгов
		Name   string `xml:"name"`    // Наименование режима торгов
		Market int    `xml:"market"`  // Внутренний код рынка
		Type   int    `xml:"type"`    // тип режима торгов 0=FORTS, 1=Т+, 2= Т0
	} `xml:"board"`
}

type Candle struct {
	Date   string  `xml:"date,attr"`
	Open   float64 `xml:"open,attr"`
	Close  float64 `xml:"close,attr"`
	High   float64 `xml:"high,attr"`
	Low    float64 `xml:"low,attr"`
	Volume int64   `xml:"volume,attr"`
}

// Свечи
type Candles struct {
	XMLName xml.Name `xml:"candles"`
	SecId   int      `xml:"secid,attr"`   // внутренний код
	Board   string   `xml:"board,attr"`   // Идентификатор режима торгов
	SecCode string   `xml:"seccode,attr"` // Код инструмента
	Period  int      `xml:"period,attr"`
	Items   []Candle `xml:"candle"`
	Status  int      `xml:"status,attr"` // 0 - данных больше нет (дочерпали до дна)
	// 1 - заказанное количество выдано, если нужны еще данные – можно выполнять еще команду
	// 2 - продолжение следует, будет еще порция данных по этой команде
	// 3 - требуемые данные недоступны (есть смысл попробовать запросить позже)
}

type OpMask struct {
	UseCredit string `xml:"usecredit,attr"` // yes/no
	ByMarket  string `xml:"bymarket,attr"`  // yes/no
	NoSplit   string `xml:"nosplit,attr"`   // yes/no
	Fok       string `xml:"fok,attr"`       // yes/no
	Ioc       string `xml:"ioc,attr"`       // yes/no
}

type SecTimeZone struct {
	Name string `xml:",cdata"`
}

type Security struct {
	SecId      int         `xml:"secid,attr"`  // внутренний код
	Active     string      `xml:"active,attr"` // true/false
	SecCode    string      `xml:"seccode"`     // Код инструмента
	InstrClass string      `xml:"instrclass"`  // Символ категории (класса) инструмента
	Board      string      `xml:"board"`       // Идентификатор режима торгов по умолчанию
	Market     int         `xml:"market"`      // Идентификатор рынка
	ShortName  string      `xml:"shortname"`   // Наименование бумаги
	Decimals   int         `xml:"decimals"`    // Количество десятичных знаков в цене
	MinStep    float64     `xml:"minstep"`     // Шаг цены
	LotSize    int         `xml:"lotsize"`     // Размер лота
	PointCost  float64     `xml:"point_cost"`  // Стоимость пункта цены
	OpMask     OpMask      `xml:"opmask"`
	SecType    string      `xml:"sectype"`    // Тип бумаги
	SecTZ      SecTimeZone `xml:"sec_tz"`     // Тип бумаги
	QuotesType int         `xml:"quotestype"` // 0 - без стакана, 1 - стакан типа OrderBook, 2 - стакан типа Level2
	MIC        string      `xml:"MIC"`        // код биржи листинга по стандарту ISO
	Ticker     string      `xml:"ticker"`     // тикер на бирже листинга
}

// Список инструментов
type Securities struct {
	XMLName xml.Name   `xml:"securities"`
	Items   []Security `xml:"security"` // внутренний код
}

// Текстовые сообщения
type Messages struct {
	XMLName xml.Name `xml:"messages"`
	Items   []struct {
		Date   string `xml:"date"`   // Дата и время
		Urgent string `xml:"urgent"` // Срочное: Y/N
		From   string `xml:"from"`   // Отправитель
		Text   string `xml:"text"`   // Текст сообщения
	} `xml:"message"`
}

// Текстовые сообщения
type Pits struct {
	XMLName xml.Name `xml:"pits"`
	Items   []struct {
		SecCode   string  `xml:"seccode,attr"` // Код инструмента
		Board     string  `xml:"board,attr"`   // Идентификатор режима торгов
		Market    int     `xml:"market"`       // Внутренний код рынка
		Decimals  int     `xml:"decimals"`     // Количество десятичных знаков в цене
		MinStep   float64 `xml:"minstep"`      // Шаг цены
		LotSize   int     `xml:"lotsize"`      // Размер лота
		PointCost float64 `xml:"point_cost"`   // Стоимость пункта цены
	} `xml:"pit"`
}

type SecInfoUpd struct {
	XMLName     xml.Name `xml:"sec_info_upd"`
	SecId       int      `xml:"secid"`                  // идентификатор бумаги
	Market      int      `xml:"market"`                 // Внутренний код рынка
	SecCode     string   `xml:"seccode"`                // Код инструмента
	MinPrice    float32  `xml:"minprice,omitempty"`     // минимальная цена (только FORTS)
	MaxPrice    float32  `xml:"maxprice,omitempty"`     // минимальная цена (только FORTS)
	BuyDeposit  float32  `xml:"buy_deposit,omitempty"`  // ГО покупателя (фьючерсы FORTS, руб.)
	SellDeposit float32  `xml:"sell_deposit,omitempty"` // ГО продавца (фьючерсы FORTS, руб.)
	BgoC        float32  `xml:"bgo_c,omitempty"`        // ГО покрытой позиции (опционы FORTS, руб.)
	BgoNc       float32  `xml:"bgo_nc,omitempty"`       // ГО непокрытой позиции (опционы FORTS, руб.)
	BgoBuy      float32  `xml:"bgo_buy,omitempty"`      // Базовое ГО под покупку маржируемого опциона
	PointCost   float32  `xml:"point_cost,omitempty"`   // Стоимость пункта цены
}

type Position struct {
	Client     string  `xml:"client"`     // Идентификатор клиента
	Union      string  `xml:"union"`      // Код юниона
	Shortname  string  `xml:"shortname"`  // Наименование вида средств
	Saldoin    float64 `xml:"saldoin"`    // Входящий остаток
	Bought     float64 `xml:"bought"`     // Куплено
	Sold       float64 `xml:"sold"`       // Продано
	Saldo      float64 `xml:"saldo"`      // Текущее сальдо
	Ordbuy     float64 `xml:"ordbuy"`     // В заявках на покупку + комиссия
	Ordbuycond float64 `xml:"ordbuycond"` // В условных заявках на покупку
}

type MoneyPosition struct {
	Position
	Register  string  `xml:"register,omitempty"` // Регистр учета
	Currency  string  `xml:"currency,omitempty"` // Код валюты
	Markets   []int   `xml:"markets->market"`    // Внутренний код рынка
	Asset     string  `xml:"asset"`              // Код вида средств
	Comission float64 `xml:"comission"`          // Сумма списанной комиссии
}

func (p MoneyPosition) StringT() string {
	return fmt.Sprintf("%s\t%.2f\t%.2f", p.Shortname, p.Saldoin, p.Saldo)
}

type SecPosition struct {
	Position
	SecId    int     `xml:"secid"`              // идентификатор бумаги
	Market   int     `xml:"market"`             // Внутренний код рынка
	SecCode  string  `xml:"seccode"`            // Код инструмента
	Register string  `xml:"register,omitempty"` // Регистр учета
	Amount   float64 `xml:"amount"`             // Текущая оценка стоимости позиции, в валюте инструмента
	Equity   float64 `xml:"equity"`             // Текущая оценка стоимости позиции, в рублях
}

func (p SecPosition) StringT() string {
	return fmt.Sprintf("%s\t%.2f\t%.2f", p.Shortname, p.Saldoin, p.Amount)
}

type Forts struct {
	Client  string `xml:"client"`          // Идентификатор клиента
	Union   string `xml:"union"`           // Код юниона
	Markets []int  `xml:"markets->market"` // Внутренний код рынка
}

type FortsPosition struct {
	Forts
	SecId             int     `xml:"secid"`              // идентификатор бумаги
	SecCode           string  `xml:"seccode"`            // Код инструмента
	Register          string  `xml:"register,omitempty"` // Регистр учета
	StartNet          int     `xml:"startnet"`           // Входящая позиция по инструменту
	OpenBuys          int     `xml:"openbuys"`           // В заявках на покупку
	OpenSells         int     `xml:"opensells"`          // В заявках на продажу
	TotalNet          int     `xml:"totalnet"`           // Текущая позиция по инструменту
	TodayBuy          int     `xml:"todaybuy"`           // Куплено
	TodaySell         int     `xml:"todaysell"`          // Продано
	OptMargin         float64 `xml:"optmargin"`          // Маржа для маржируемых опционов
	VarMargin         float64 `xml:"varmargin"`          // Вариационная маржа
	Expirationpos     int64   `xml:"expirationpos"`      // Опционов в заявках на исполнение
	UsedSellSpotLimit float64 `xml:"usedsellspotlimit"`  // Объем использованого спот-лимита на продажу
	SellSpotLimit     float64 `xml:"sellspotlimit"`      // текущий спот-лимит на продажу, установленный Брокером
	Netto             float64 `xml:"netto"`              // нетто-позиция по всем инструментам данного спота
	Kgo               rune    `xml:"kgo"`                // коэффициент ГО
}

type FortsMoney struct {
	Forts
	Shortname string  `xml:"shortname"` // Наименование вида средств
	Current   float64 `xml:"current"`   // Текущие
	Blocked   float64 `xml:"blocked"`   // Заблокировано
	Free      float64 `xml:"free"`      // Свободные
	Varmargin float64 `xml:"varmargin"` // Опер. Маржа
}

type FortsCollaterals struct {
	Forts
	Shortname string  `xml:"shortname"` // Наименование вида средств
	Current   float64 `xml:"current"`   // Текущие
	Blocked   float64 `xml:"blocked"`   // Заблокировано
	Free      float64 `xml:"free"`      // Свободные
}

type SpotLimit struct {
	Client       string  `xml:"client"`          // Идентификатор клиента
	Markets      []int   `xml:"markets->market"` // Внутренний код рынка
	ShortName    string  `xml:"shortname"`       // Наименование вида средств
	BuyLimit     float64 `xml:"buylimit"`        // Текущий лимит
	BuyLimitUsed float64 `xml:"buylimitused"`    // Заблокировано лимита

}
type UnitedLimits struct {
	Union        string  `xml:"union,attr"`   // код юниона
	OpenEquity   float64 `xml:"open_equity"`  // Входящая оценка стоимости единого портфеля
	Equity       float64 `xml:"equity"`       // Текущая оценка стоимости единого портфеля
	Requirements float64 `xml:"requirements"` // Начальные требования
	Free         float64 `xml:"free"`         // Свободные средства
	VM           float64 `xml:"vm"`           // Вариационная маржа FORTS
	FinRes       float64 `xml:"finres"`       // Финансовый результат последнего клиринга FORTS
	GO           float64 `xml:"go"`           // Размер требуемого ГО, посчитанный биржей FORTS
}

type Positions struct {
	MoneyPosition    []MoneyPosition    `xml:"money_position,omitempty"`
	SecPositions     []SecPosition      `xml:"sec_position,omitempty"`
	FortsPosition    []FortsPosition    `xml:"forts_position,omitempty"`
	FortsMoney       []FortsMoney       `xml:"forts_money,omitempty"`       // деньги ФОРТС
	FortsCollaterals []FortsCollaterals `xml:"forts_collaterals,omitempty"` // залоги ФОРТС
	SpotLimit        []SpotLimit        `xml:"spot_limit,omitempty"`
	UnitedLimits     []UnitedLimits     `xml:"united_limits,omitempty"`
}

type valuePart struct {
	OpenBalance float64 `xml:"open_balance"` // Входящая денежная позиция
	Bought      float64 `xml:"bought"`       // Затрачено на покупки
	Sold        float64 `xml:"sold"`         // Выручено от продаж
	Settled     float64 `xml:"settled"`      // Исполнено
	Balance     float64 `xml:"balance"`      // Текущая денежная позиция

}
type UnitedPortfolio struct {
	XMLName    xml.Name `xml:"united_portfolio"`
	Union      string   `xml:"union,attr"`  // код юниона
	OpenEquity float64  `xml:"open_equity"` // Входящая оценка стоимости единого портфеля
	Equity     float64  `xml:"equity"`      // Текущая оценка стоимости единого портфеля
	ChrgoffIr  float64  `xml:"chrgoff_ir"`  // Корреляционный вычет планового риска
	InitReq    float64  `xml:"init_req"`    // Плановый риск (размер начальных требований)
	ChrgoffMr  float64  `xml:"chrgoff_mr"`  // Корреляционный вычет минимальных требований
	MaintReq   float64  `xml:"maint_req"`   // Размер минимальных требований
	RegEquity  float64  `xml:"reg_equity"`  // Размер минимальных требований
	RegIr      float64  `xml:"reg_ir"`      // Размер минимальных требований
	RegMr      float64  `xml:"reg_mr"`      // Размер минимальных требований
	Vm         float64  `xml:"vm"`          // Вариационная маржа FORTS
	FinRes     float64  `xml:"finres"`      // Финансовый результат последнего клиринга FORTS
	Go         float64  `xml:"go"`          // Размер требуемого ГО, посчитанный биржей FORTS
	VmMma      float64  `xml:"vm_mma"`      // Вариационная маржа FORTS MMA
	Money      []struct {
		Name string `xml:"name,attr"`
		valuePart
		Tax       float64 `xml:"tax"` // Уплачено комиссии
		ValuePart []struct {
			Register string `xml:"register,attr"`
			valuePart
		} `xml:"value_part,omitempty"`
	} `xml:"money,omitempty"`
	Asset []struct {
		Code       string  `xml:"code,attr"`
		Name       string  `xml:"name,attr"`
		SetoffRate float64 `xml:"setoff_rate"` // Размер минимальных требований
		InitReq    float64 `xml:"init_req"`    // Плановый риск (размер начальных требований)
		MaintReq   float64 `xml:"maint_req"`   // Размер минимальных требований
		Security   []struct {
			Secid         int     `xml:"secid,attr"`
			Market        int     `xml:"market"`
			SecCode       string  `xml:"seccode"`
			Price         float64 `xml:"price"`
			OpenBalance   int     `xml:"open_balance"`   // Входящая нетто-позиция, штук
			Bought        int     `xml:"bought"`         // Куплено, штук
			Sold          int     `xml:"sold"`           // Продано, штук
			Balance       int     `xml:"balance"`        // Текущая нетто-позиция, штук
			BalancePrc    float64 `xml:"balance_prc"`    // Балансовая цена
			UnrealizedPnl float64 `xml:"unrealized_pnl"` // Нереализованные прибыли/убытки
			Buying        int     `xml:"buying"`         // Продано, штук
			Selling       int     `xml:"selling"`        // Заявлено купить, штук
			Equity        float64 `xml:"equity"`         // Заявлено продать, штук
			RegEquity     float64 `xml:"reg_equity"`     // Стоимость в обеспечении портфеля нормативная
			RiskrateLong  float64 `xml:"riskrate_long"`
			RiskrateShort float64 `xml:"riskrate_short"`
			ReserateLong  float64 `xml:"reserate_long"`
			ReserateShort float64 `xml:"reserate_short"`
			Pl            float64 `xml:"pl"`
			PnlIncome     float64 `xml:"pnl_income"`
			PnlIntraday   float64 `xml:"pnl_intraday"`
			MaxBuy        int     `xml:"maxbuy"`
			MaxSell       int     `xml:"maxsell"`
			ValuePart     []struct {
				Register    string `xml:"register,attr"`
				OpenBalance int    `xml:"open_balance"` // Входящая нетто-позиция, штук
				Bought      int    `xml:"bought"`       // Куплено, штук
				Sold        int    `xml:"sold"`         // Продано, штук
				Settled     int    `xml:"settled"`      // Исполнено, штук
				Balance     int    `xml:"balance"`      // Текущая нетто-позиция, штук
				Buying      int    `xml:"buying"`       // Продано, штук
				Selling     int    `xml:"selling"`      // Заявлено купить, штук
			} `xml:"value_part,omitempty"`
		} `xml:"security,omitempty"`
	} `xml:"asset,omitempty"`
}

type UnitedEquity struct {
	XMLName xml.Name `xml:"united_equity"`
	Union   string   `xml:"union,attr"` // код юниона
	Equity  float64  `xml:"equity"`     // Текущая оценка стоимости единого портфеля
}

/*
func (p Positions) StringT() string {
	out := []string{
		fmt.Sprintf("%s Оценка портфеля: %.2f Свободно: %.2f Прибыль, день: %.2f", p.UnitedLimits.Union, p.UnitedLimits.Equity, p.UnitedLimits.Free, p.UnitedLimits.OpenEquity - p.UnitedLimits.Equity),
		//fmt.Sprintf("Инструмент\tПозиция\tКотировка\tОценка\tМаржа\tОбеспечение\tСредняя\tПрибыль,день\tПрибыль"),
		p.MoneyPosition.StringT(),
		p.SecPosition.StringT(),
	}
	return strings.Join(out, "\n")
}
*/
// Клиентские счета
type Client struct {
	XMLName  xml.Name `xml:"client"`
	ID       string   `xml:"id,attr"`     // CLIENT_ID
	Remove   string   `xml:"remove,attr"` // true/false
	Type     string   `xml:"type"`        // тип клиента
	Currency string   `xml:"currency"`    // валюта фондового портфеля клиента
	Market   int      `xml:"market"`      // id рынка
	Union    string   `xml:"union"`       // код юниона
	FortsAcc string   `xml:"forts_acc"`   // счет FORTS
}

// Юнионы, находящиеся в управлении клиента
type Union struct {
	XMLName xml.Name `xml:"union"`
	Id      string   `xml:"id,attr"`     //код юниона
	Remove  string   `xml:"remove,attr"` // true/false
}

// Режим кредитования
type Overnight struct {
	XMLName xml.Name `xml:"overnight"`
	Status  string   `xml:"status,attr"` // true/false
}
type Result struct {
	XMLName xml.Name `xml:"result"`
	Success string   `xml:"success,attr"` // true/false
	Message string   `xml:"message"`      // error message
}

type NewsHeader struct {
	XMLName   xml.Name `xml:"news_header"`
	Id        int      `xml:"id"`              // порядковый номер новости
	TimeStamp string   `xml:"time_stamp"`      // дата-время новости (от источника)
	Source    string   `xml:"source,charData"` // источник новости
	Title     string   `xml:"title,charData"`  // заголовок новости
}

type Quotations struct {
	XMLName xml.Name    `xml:"quotations"`
	Items   []Quotation `xml:"quotation"`
}

// <quotations><quotation secid="36116"><board>FUT</board><seccode>SiU9</seccode><last>63384</last><change>210</change><priceminusprevwaprice>210</priceminusprevwaprice><biddepth>38</biddepth><biddeptht>32115</biddeptht><offerdepth>33</offerdepth><offerdeptht>60513</offerdeptht><voltoday>519036</voltoday><numtrades>77015</numtrades><valtoday>32873.706</valtoday><openpositions>2273956</openpositions><deltapositions>14836</deltapositions></quotation></quotations>
type Quotation struct {
	XMLName               xml.Name `xml:"quotation"`
	SecId                 int      `xml:"secid,attr"`                      // внутренний код
	Board                 string   `xml:"board,omitempty"`                 // Идентификатор режима торгов
	SecCode               string   `xml:"seccode,omitempty"`               // Код инструмента
	Open                  float64  `xml:"open,omitempty"`                  // Цена первой сделки
	WapPice               float64  `xml:"waprice,omitempty"`               // Средневзвешенная цена
	Last                  float64  `xml:"last,omitempty"`                  // Цена последней сделки
	Quantity              int      `xml:"quantity,omitempty"`              // Объем последней сделки, в лотах
	Time                  string   `xml:"time,omitempty"`                  // Время заключения последней сделки
	Change                float64  `xml:"change,omitempty"`                // Абсолютное изменение цены последней сделки по отношению к цене последней сделки предыдущего торгового дня
	PriceMinusPrevwaPrice float64  `xml:"priceminusprevwaprice,omitempty"` // Цена последней сделки к оценке предыдущего дня
	Bid                   float64  `xml:"bid,omitempty"`                   // Лучшая цена на покупку
	BidDepth              int      `xml:"biddepth,omitempty"`              // Кол-во лотов на покупку по лучшей цене
	BidDeptht             int      `xml:"biddeptht,omitempty"`             // Совокупный спрос
	NumBids               int      `xml:"numbids,omitempty"`               // Заявок на покупку
	OfferDepth            int      `xml:"offerdepth,omitempty"`            // Кол-во лотов на продажу по лучшей цене
	OfferDeptht           int      `xml:"offerdeptht,omitempty"`           // Совокупное предложение
	NumOffers             int      `xml:"numoffers,omitempty"`             // Заявок на продажу
	NumTrades             int      `xml:"numtrades,omitempty"`             // Сделок
	VolToday              int      `xml:"voltoday,omitempty"`              // Объем совершенных сделок в лотах
	OpenPositions         int      `xml:"openpositions,omitempty"`         // Общее количество открытых позиций(FORTS)
	DeltaPositions        int      `xml:"deltapositions,omitempty"`        // Изм.открытых позиций(FORTS)
	ValToday              float64  `xml:"valtoday,omitempty"`              // Объем совершенных сделок, млн. руб
	Yield                 float64  `xml:"yield,omitempty"`                 // Доходность, по цене последней сделки
	YieldatwaPrice        float64  `xml:"yieldatwaprice,omitempty"`        // Доходность по средневзвешенной цене
	MarketPriceToday      float64  `xml:"marketpricetoday,omitempty"`      // Рыночная цена по результатам торгов сегодняшнего дня
	HighBid               float64  `xml:"highbid,omitempty"`               // Наибольшая цена спроса в течение торговой сессии
	LowOffer              float64  `xml:"lowoffer,omitempty"`              // Наименьшая цена предложения в течение торговой сессии
	High                  float64  `xml:"high,omitempty"`                  // Максимальная цена сделки
	Low                   float64  `xml:"low,omitempty"`                   // Минимальная цена сделки
	ClosePrice            float64  `xml:"closeprice,omitempty"`            // Цена закрытия
	CloseYield            float64  `xml:"closeyield,omitempty"`            // Доходность по цене закрытия
	Status                string   `xml:"status,omitempty"`                // Статус «торговые операции разрешены/запрещены»
	TradingStatus         string   `xml:"tradingstatus,omitempty"`         // Состояние торговой сессии по инструменту
	BuyDeposit            float64  `xml:"buydeposit,omitempty"`            // ГО покупок/покр
	SellDeposit           float64  `xml:"selldeposit,omitempty"`           // ГО продаж/непокр
	Volatility            float64  `xml:"volatility,omitempty"`            // Волатильность
	TheoreticalPrice      float64  `xml:"theoreticalprice,omitempty"`      // Теоретическая цена
	BgoBuy                float64  `xml:"bgo_buy,omitempty"`               // Базовое ГО под покупку маржируемого опциона
	PointCost             float64  `xml:"point_cost,omitempty"`            // Стоимость пункта цены
	LCurrentPrice         float64  `xml:"lcurrentprice,omitempty"`         // Официальная текущая цена Биржи
}

type trade struct {
	XMLName      xml.Name `xml:"trade"`
	SecId        int      `xml:"secid,attr"`             // внутренний код
	SecCode      string   `xml:"seccode,omitempty"`      // Код инструмента
	TradeNo      int64    `xml:"tradeno,omitempty"`      // Биржевой номер сделки
	Time         string   `xml:"time,omitempty"`         // Время сделки :date
	Board        string   `xml:"board,omitempty"`        // Идентификатор режима торгов
	Pice         float64  `xml:"price,omitempty"`        // Цена сделки
	Quantity     int      `xml:"quantity,omitempty"`     // Объем сделки, в лотах
	BuySell      string   `xml:"buysell,omitempty"`      // покупка (B) / продажа (S)
	OpenInterest int      `xml:"openinterest,omitempty"` // Открытый интерес
	Period       string   `xml:"period,omitempty"`       // Период торгов (O - открытие, N - торги, С - закрытие)
}

type AllTrades struct {
	XMLName xml.Name `xml:"alltrades"`
	Items   []trade  `xml:"trade"`
}

type SecInfo struct {
	XMLName       xml.Name `xml:"sec_info"`
	SecId         int      `xml:"secid,attr"`               // внутренний код
	SecName       string   `xml:"secname,omitempty"`        // Полное наименование инструмента
	SecCode       string   `xml:"seccode,omitempty"`        // Код инструмента
	Market        int      `xml:"market,omitempty"`         // Внутренний код рынка
	PName         string   `xml:"pname,omitempty"`          // единицы измерения цены
	MatDate       string   `xml:"mat_date,omitempty"`       // дата погашения
	ClearingPrice float32  `xml:"clearing_price,omitempty"` // цена последнего клиринга (только FORTS)
	MinPrice      float32  `xml:"minprice,omitempty"`       // минимальная цена (только FORTS)
	MaxPrice      float32  `xml:"maxprice,omitempty"`       // минимальная цена (только FORTS)
	BuyDeposit    float32  `xml:"buy_deposit,omitempty"`    // ГО покупателя (фьючерсы FORTS, руб.)
	SellDeposit   float32  `xml:"sell_deposit,omitempty"`   // ГО продавца (фьючерсы FORTS, руб.)
	BgoC          float32  `xml:"bgo_c,omitempty"`          // ГО покрытой позиции (опционы FORTS, руб.)
	BgoNc         float32  `xml:"bgo_nc,omitempty"`         // ГО непокрытой позиции (опционы FORTS, руб.)
	BgoBuy        float32  `xml:"bgo_buy,omitempty"`        // Базовое ГО под покупку маржируемого опциона
	AccruedInt    float32  `xml:"accruedint,omitempty"`     // текущий НКД, руб
	CouponValue   float32  `xml:"coupon_value,omitempty"`   // размер купона, руб
	CouponDate    string   `xml:"coupon_date,omitempty"`    // дата погашения купона
	CouponPeriod  int      `xml:"coupon_period,omitempty"`  // период выплаты купона, дни
	FaceValue     float32  `xml:"facevalue,omitempty"`      // номинал облигации или акции, руб
	PutCall       string   `xml:"put_call,omitempty"`       // тип опциона Call(C)/Put(P)
	PointCost     float32  `xml:"point_cost,omitempty"`     // Стоимость пункта цены
	OptType       string   `xml:"opt_type,omitempty"`       // маржинальный(M)/премия(P)
	LotVolume     int      `xml:"lot_volume,omitempty"`     // количество базового актива (FORTS)
	Isin          string   `xml:"isin,omitempty"`           // Международный идентификационный код инструмента
	RegNumber     string   `xml:"regnumber,omitempty"`      // Номер государственной регистрации инструмента
	BuybackPrice  float32  `xml:"buybackprice,omitempty"`   // Цена досрочного выкупа облигации
	BuybackDate   string   `xml:"buybackdate,omitempty"`    // Дата досрочного выкупа облигации
	CurrencyId    string   `xml:"currencyid,omitempty"`     // Валюта расчетов режима торгов по умолчанию
}

type Quotes struct {
	XMLName xml.Name `xml:"quotes"`
	Items   []quote  `xml:"quote"`
}

// Значение «-1» одновременно и в поле sell и в поле buy означает, что строка с данной ценой (или с данным значением пары price + source) удалена из «стакана».
type quote struct {
	XMLName xml.Name `xml:"quote"`
	SecId   int      `xml:"secid,attr"`        // внутренний код
	SecCode string   `xml:"seccode,omitempty"` // Код инструмента
	Board   string   `xml:"board,omitempty"`   // Идентификатор режима торгов
	Pice    float64  `xml:"price,omitempty"`   // Цена
	Source  string   `xml:"source,omitempty"`  // Источник котировки (маркетмейкер)
	Buy     int      `xml:"buy,omitempty"`     // количество бумаг к покупке, значение «-1» - больше нет заявок на покупку
	Sell    int      `xml:"sell,omitempty"`    // количество бумаг к продаже, значение «-1» - больше нет заявок на покупку
}

// Encodes the request into XML format.
func EncodeRequest(request interface{}) string {
	var bytesBuffer bytes.Buffer
	e := xml.NewEncoder(&bytesBuffer)
	e.Encode(request)
	return bytesBuffer.String()
}
