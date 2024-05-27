package eventlog

import (
	"bytes"
	"dns-logtail/checkpoint"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/elastic/beats/winlogbeat/sys"
	"io"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

type WinRecord struct {
	sys.Event
	File   string                   // Source file when eventlog is from a file.
	API    string                   // The eventlog log API type used to read the record.
	XML    string                   // XML representation of the eventlog.
	Offset checkpoint.EventLogState // Position of the record within its source stream.
}

func (w WinRecord) String() string {
	data, _ := json.Marshal(w)
	return string(data)
}

func (w WinRecord) Timestamp() time.Time {
	return w.TimeCreated.SystemTime
}

// UnmarshalEventXML unmarshals the given XML into a new Event.
func UnmarshalEventXML(rawXML []byte) (Event, error) {
	var event Event
	decoder := xml.NewDecoder(newXMLSafeReader(rawXML))
	err := decoder.Decode(&event)
	return event, err
}

// Event holds the data from a log record.
type Event struct {
	// System
	Provider        Provider        `xml:"System>Provider"`
	EventIdentifier EventIdentifier `xml:"System>EventID"`
	Version         Version         `xml:"System>Version"`
	LevelRaw        uint8           `xml:"System>Level"`
	TaskRaw         uint16          `xml:"System>Task"`
	OpcodeRaw       uint8           `xml:"System>Opcode"`
	TimeCreated     TimeCreated     `xml:"System>TimeCreated"`
	RecordID        uint64          `xml:"System>EventRecordID"`
	Correlation     Correlation     `xml:"System>Correlation"`
	Execution       Execution       `xml:"System>Execution"`
	Channel         string          `xml:"System>Channel"`
	Computer        string          `xml:"System>Computer"`
	User            SID             `xml:"System>Security"`

	EventData EventData `xml:"EventData"`
	UserData  UserData  `xml:"UserData"`

	// RenderingInfo
	Message  string   `xml:"RenderingInfo>Message"`
	Level    string   `xml:"RenderingInfo>Level"`
	Task     string   `xml:"RenderingInfo>Task"`
	Opcode   string   `xml:"RenderingInfo>Opcode"`
	Keywords []string `xml:"RenderingInfo>Keywords>Keyword"`

	// ProcessingErrorData
	RenderErrorCode         uint32 `xml:"ProcessingErrorData>ErrorCode"`
	RenderErrorDataItemName string `xml:"ProcessingErrorData>DataItemName"`
	RenderErr               []string
}

// Provider identifies the provider that logged the eventlog. The Name and GUID
// attributes are included if the provider used an instrumentation manifest to
// define its events; otherwise, the EventSourceName attribute is included if a
// legacy eventlog provider (using the Event Logging API) logged the eventlog.
type Provider struct {
	Name            string `xml:"Name,attr"`
	GUID            string `xml:"Guid,attr"`
	EventSourceName string `xml:"EventSourceName,attr"`
}

// Correlation contains activity identifiers that consumers can use to group
// related events together.
type Correlation struct {
	ActivityID        string `xml:"ActivityID,attr"`
	RelatedActivityID string `xml:"RelatedActivityID,attr"`
}

// Execution contains information about the process and thread that logged the
// eventlog.
type Execution struct {
	ProcessID uint32 `xml:"ProcessID,attr"`
	ThreadID  uint32 `xml:"ThreadID,attr"`

	// Only available for events logged to an eventlog tracing log file (.etl file).
	ProcessorID   uint32 `xml:"ProcessorID,attr"`
	SessionID     uint32 `xml:"SessionID,attr"`
	KernelTime    uint32 `xml:"KernelTime,attr"`
	UserTime      uint32 `xml:"UserTime,attr"`
	ProcessorTime uint32 `xml:"ProcessorTime,attr"`
}

// EventIdentifier is the identifer that the provider uses to identify a
// specific eventlog type.
type EventIdentifier struct {
	Qualifiers uint16 `xml:"Qualifiers,attr"`
	ID         uint32 `xml:",chardata"`
}

// TimeCreated contains the system time of when the eventlog was logged.
type TimeCreated struct {
	SystemTime time.Time
}

// UnmarshalXML unmarshals an XML dataTime string.
func (t *TimeCreated) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	attrs := struct {
		SystemTime string `xml:"SystemTime,attr"`
		RawTime    uint64 `xml:"RawTime,attr"`
	}{}

	err := d.DecodeElement(&attrs, &start)
	if err != nil {
		return err
	}

	if attrs.SystemTime != "" {
		// This works but XML dateTime is really ISO8601.
		t.SystemTime, err = time.Parse(time.RFC3339Nano, attrs.SystemTime)
	} else if attrs.RawTime != 0 {
		// The units for RawTime are not specified in the documentation. I think
		// it is only used in eventlog tracing so this shouldn't be a problem.
		err = fmt.Errorf("failed to unmarshal TimeCreated RawTime='%d'", attrs.RawTime)
	}

	return err
}

// EventData contains the eventlog data. The EventData section is used if the
// message provider template does not contain a UserData section.
type EventData struct {
	Pairs []KeyValue `xml:",any"`
}

// UserData contains the eventlog data.
type UserData struct {
	Name  xml.Name
	Pairs []KeyValue
}

// UnmarshalXML unmarshals UserData XML.
func (u *UserData) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	// Assume that UserData has the same general key-value structure as
	// EventData does.
	in := struct {
		Pairs []KeyValue `xml:",any"`
	}{}

	// Read tokens until we find the first StartElement then unmarshal it.
	for {
		t, err := d.Token()
		if err != nil {
			return err
		}

		if se, ok := t.(xml.StartElement); ok {
			err = d.DecodeElement(&in, &se)
			if err != nil {
				return err
			}

			u.Name = se.Name
			u.Pairs = in.Pairs
			d.Skip()
			break
		}
	}

	return nil
}

// KeyValue is a key value pair of strings.
type KeyValue struct {
	Key   string
	Value string
}

// UnmarshalXML unmarshals an arbitrary XML element into a KeyValue. The key
// becomes the name of the element or value of the Name attribute if it exists.
// The value is the character data contained within the element.
func (kv *KeyValue) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	elem := struct {
		XMLName xml.Name
		Name    string `xml:"Name,attr"`
		Value   string `xml:",chardata"`
	}{}

	err := d.DecodeElement(&elem, &start)
	if err != nil {
		return err
	}

	kv.Key = elem.XMLName.Local
	if elem.Name != "" {
		kv.Key = elem.Name
	}
	kv.Value = elem.Value

	return nil
}

// Version contains the version number of the eventlog's definition.
type Version uint8

// UnmarshalXML unmarshals the version number as an xsd:unsignedByte. Invalid
// values are ignored an no error is returned.
func (v *Version) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}

	version, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		// Ignore invalid version values.
		return nil
	}

	*v = Version(version)
	return nil
} // SID represents the Windows Security Identifier for an account.
type SID struct {
	Identifier string `xml:"UserID,attr"`
	Name       string
	Domain     string
	Type       SIDType
}

// String returns string representation of SID.
func (a SID) String() string {
	return fmt.Sprintf("SID Identifier[%s] Name[%s] Domain[%s] Type[%s]",
		a.Identifier, a.Name, a.Domain, a.Type)
}

// SIDType identifies the type of a security identifier (SID).
type SIDType uint32

// SIDType values.
const (
	// Do not reorder.
	SidTypeUser SIDType = 1 + iota
	SidTypeGroup
	SidTypeDomain
	SidTypeAlias
	SidTypeWellKnownGroup
	SidTypeDeletedAccount
	SidTypeInvalid
	SidTypeUnknown
	SidTypeComputer
	SidTypeLabel
)

// sidTypeToString is a mapping of SID types to their string representations.
var sidTypeToString = map[SIDType]string{
	SidTypeUser:           "User",
	SidTypeGroup:          "Group",
	SidTypeDomain:         "Domain",
	SidTypeAlias:          "Alias",
	SidTypeWellKnownGroup: "Well Known Group",
	SidTypeDeletedAccount: "Deleted Account",
	SidTypeInvalid:        "Invalid",
	SidTypeUnknown:        "Unknown",
	SidTypeComputer:       "Computer",
	SidTypeLabel:          "Label",
}

// String returns string representation of SIDType.
func (st SIDType) String() string {
	return sidTypeToString[st]
}

// The type xmlSafeReader escapes UTF control characters in the io.Reader
// it wraps, so that it can be fed to Go's xml parser.
// Characters for which `unicode.IsControl` returns true will be output as
// an hexadecimal unicode escape sequence "\\uNNNN".
type xmlSafeReader struct {
	inner   io.Reader
	backing [256]byte
	buf     []byte
	code    []byte
}

var _ io.Reader = (*xmlSafeReader)(nil)

func output(n int) (int, error) {
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// Read implements the io.Reader interface.
func (r *xmlSafeReader) Read(d []byte) (n int, err error) {
	if len(r.code) > 0 {
		n = copy(d, r.code)
		r.code = r.code[n:]
		return output(n)
	}
	if len(r.buf) == 0 {
		n, _ = r.inner.Read(r.backing[:])
		r.buf = r.backing[:n]
	}
	for i := 0; i < len(r.buf); {
		code, size := utf8.DecodeRune(r.buf[i:])
		if !unicode.IsSpace(code) && unicode.IsControl(code) {
			n = copy(d, r.buf[:i])
			r.buf = r.buf[n+1:]
			r.code = []byte(fmt.Sprintf("\\u%04x", code))
			m := copy(d[n:], r.code)
			r.code = r.code[m:]
			return output(n + m)
		}
		i += size
	}
	n = copy(d, r.buf)
	r.buf = r.buf[n:]
	return output(n)
}

func newXMLSafeReader(rawXML []byte) io.Reader {
	return &xmlSafeReader{inner: bytes.NewReader(rawXML)}
}
