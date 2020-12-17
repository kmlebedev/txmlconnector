package commands

import "encoding/xml"

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
