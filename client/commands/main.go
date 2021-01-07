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
	XMLName xml.Name `xml:"command"`
	Id      string   `xml:"id,attr"`
	Union   string   `xml:"union,attr,omitempty"`
	Client  string   `xml:"client,attr,omitempty"`
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

type Candlekinds struct {
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
	Market     string      `xml:"market"`      // Идентификатор рынка
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

type SecInfo struct {
	SecId   int    `xml:"secid"`   // идентификатор бумаги
	Market  int    `xml:"market"`  // Внутренний код рынка
	SecCode string `xml:"seccode"` // Код инструмента
}

type SecInfoUpd struct {
	SecInfo
	MinPrice    float32 `xml:"minprice,omitempty"`     // минимальная цена (только FORTS)
	MaxPrice    float32 `xml:"maxprice,omitempty"`     // минимальная цена (только FORTS)
	BuyDeposit  float32 `xml:"buy_deposit,omitempty"`  // ГО покупателя (фьючерсы FORTS, руб.)
	SellDeposit float32 `xml:"sell_deposit,omitempty"` // ГО продавца (фьючерсы FORTS, руб.)
	PointCost   float32 `xml:"point_cost,omitempty"`   // Стоимость пункта цены
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
	Currency  string  `xml:"currency,omitempty"` // Код валюты
	Markets   []int   `xml:"markets->market"`    // Внутренний код рынка
	Register  int     `xml:"register"`           // Регистр учета
	Asset     string  `xml:"asset"`              // Код вида средств
	Comission float64 `xml:"comission"`          // Сумма списанной комиссии
}

func (p MoneyPosition) StringT() string {
	return fmt.Sprintf("%s\t%.2f\t%.2f", p.Shortname, p.Saldoin, p.Saldo)
}

type SecPosition struct {
	Position
	SecInfo
	Amount float64 `xml:"amount"` // Текущая оценка стоимости позиции, в валюте инструмента
	Equity float64 `xml:"equity"` // Текущая оценка стоимости позиции, в рублях
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
	SecId             int     `xml:"secid"`             // идентификатор бумаги
	SecCode           string  `xml:"seccode"`           // Код инструмента
	StartNet          int     `xml:"startnet"`          // Входящая позиция по инструменту
	OpenBuys          int     `xml:"openbuys"`          // В заявках на покупку
	OpenSells         int     `xml:"opensells"`         // В заявках на продажу
	TotalNet          int     `xml:"totalnet"`          // Текущая позиция по инструменту
	TodayBuy          int     `xml:"todaybuy"`          // Куплено
	TodaySell         int     `xml:"todaysell"`         // Продано
	OptMargin         float64 `xml:"optmargin"`         // Маржа для маржируемых опционов
	VarMargin         float64 `xml:"varmargin"`         // Вариационная маржа
	Expirationpos     int64   `xml:"expirationpos"`     // Опционов в заявках на исполнение
	UsedSellSpotLimit float64 `xml:"usedsellspotlimit"` // Объем использованого спот-лимита на продажу
	SellSpotLimit     float64 `xml:"sellspotlimit"`     // текущий спот-лимит на продажу, установленный Брокером
	Netto             float64 `xml:"netto"`             // нетто-позиция по всем инструментам данного спота
	Kgo               rune    `xml:"kgo"`               // коэффициент ГО
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
	MoneyPosition    []MoneyPosition  `xml:"money_position,omitempty"`
	SecPositions     []SecPosition    `xml:"sec_position,omitempty"`
	FortsPosition    FortsPosition    `xml:"forts_position,omitempty"`
	FortsMoney       FortsMoney       `xml:"forts_money,omitempty"`       // деньги ФОРТС
	FortsCollaterals FortsCollaterals `xml:"forts_collaterals,omitempty"` // залоги ФОРТС
	SpotLimit        SpotLimit        `xml:"spot_limit,omitempty"`
	UnitedLimits     []UnitedLimits   `xml:"united_limits,omitempty"` //
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

// Encodes the request into XML format.
func EncodeRequest(request interface{}) string {
	var bytesBuffer bytes.Buffer
	e := xml.NewEncoder(&bytesBuffer)
	e.Encode(request)
	return bytesBuffer.String()
}
