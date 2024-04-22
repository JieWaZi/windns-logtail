package main

import "encoding/xml"

type Events struct {
	XMLName   xml.Name   `xml:"Events" json:"-"`
	EventLogs []EventLog `xml:"Event" json:"EventLogs"`
}

type EventLog struct {
	XMLName       xml.Name      `xml:"Event" json:"-"`
	System        System        `xml:"System" json:"System"`
	EventData     EventData     `xml:"EventData" json:"EventData"`
	RenderingInfo RenderingInfo `xml:"RenderingInfo"`
}

type System struct {
	XMLName       xml.Name    `xml:"System" json:"-"`
	Provider      Provider    `xml:"Provider" json:"Provider"`
	EventID       string      `xml:"EventID" json:"EventID"`
	Version       string      `xml:"Version" json:"Version"`
	Level         string      `xml:"Level" json:"Level"`
	Task          string      `xml:"Task" json:"Task"`
	Keywords      string      `xml:"Keywords" json:"Keywords"`
	Opcode        string      `xml:"Opcode" json:"Opcode"`
	TimeCreated   TimeCreated `xml:"TimeCreated" json:"TimeCreated"`
	EventRecordID string      `xml:"EventRecordID" json:"EventRecordID"`
	Channel       string      `xml:"Channel" json:"Channel"`
	Computer      string      `xml:"Computer" json:"Computer"`
}

type Provider struct {
	XMLName xml.Name `xml:"Provider" json:"-"`
	Name    string   `xml:"Name,attr" json:"Name"`
	Guid    string   `xml:"Guid,attr" json:"Guid"`
}

type TimeCreated struct {
	XMLName    xml.Name `xml:"TimeCreated" json:"-"`
	SystemTime string   `xml:"SystemTime,attr" json:"SystemTime"`
}

type EventData struct {
	XMLName xml.Name `xml:"EventData" json:"-"`
	Data    []Field  `xml:"Data" json:"Data"`
}

type Field struct {
	XMLName xml.Name `xml:"Data" json:"-"`
	Name    string   `xml:"Name,attr" json:"Name"`
	Value   string   `xml:",innerxml" json:"Value"`
}

type RenderingInfo struct {
	XMLName  xml.Name `xml:"RenderingInfo" json:"-"`
	Keywords Keywords `xml:"Keywords" json:"Keywords"`
	Task     string   `xml:"Task" json:"Task"`
	Message  string   `xml:"Message" json:"Message"`
}

type Keywords struct {
	XMLName  xml.Name  `xml:"Keywords" json:"-"`
	Keywords []Keyword `xml:"Keyword" json:"Keywords"`
}

type Keyword struct {
	XMLName xml.Name `xml:"Keyword" json:"-"`
	Value   string   `xml:",innerxml" json:"Value"`
}

func ParseEventLogXML(data []byte) (EventLog, error) {
	log := EventLog{}
	err := xml.Unmarshal(data, &log)
	if err != nil {
		return log, err
	}

	return log, nil
}
