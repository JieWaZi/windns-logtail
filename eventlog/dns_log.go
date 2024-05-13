package eventlog

import (
	"github.com/miekg/dns"
	"net"
	"strconv"
	"strings"
	"time"
)

func DnsRecordFromMsg(msg *dns.Msg, dl *DnsRecord) {
	dl.TransID = msg.Id

	if len(msg.Question) > 0 {
		dl.Domain = msg.Question[0].Name
		dl.QueryClass = dns.ClassToString[msg.Question[0].Qclass]
		dl.QueryType = dns.TypeToString[msg.Question[0].Qtype]
	}

	dl.Response = msg.Response
	dl.Authoritative = msg.Response
	dl.Truncated = msg.Truncated
	dl.RecursionDesired = msg.RecursionDesired
	dl.RecursionAvailable = msg.RecursionAvailable
	dl.Zero = msg.Zero

	rrs2Strings := func(rrs []dns.RR) []string {
		var result []string
		for _, r := range rrs {
			rs := strings.Join(strings.Split(strings.ReplaceAll(r.String(), "\n", ""), "\t"), " ")
			result = append(result, rs)
		}
		return result
	}

	if dl.Response {
		dl.Rcode = dns.RcodeToString[msg.Rcode]
		dl.Answer = rrs2Strings(msg.Answer)
		dl.Authority = rrs2Strings(msg.Ns)
		dl.Additional = rrs2Strings(msg.Extra)
	}
}

type DnsRecord struct {
	PacketTime         time.Time
	SrcIP              net.IP
	DstIP              net.IP
	SrcPort            uint16
	DstPort            uint16
	TransID            uint16
	Domain             string
	QueryClass         string
	QueryType          string
	Rcode              string
	Response           bool
	Authoritative      bool
	Truncated          bool
	RecursionDesired   bool
	RecursionAvailable bool
	Zero               bool
	AuthenticatedData  bool
	CheckingDisabled   bool
	ResolvDuration     time.Duration
	Answer             []string
	Authority          []string
	Additional         []string
}

func (d DnsRecord) String() string {
	bool2Int := func(in bool) string {
		if in {
			return "1"
		}
		return "0"
	}

	getPacketType := func(response bool) string {
		if response {
			return "response"
		}
		return "query"
	}

	ss := []string{
		d.PacketTime.Local().Format("2006-01-02 15:04:05.999999"),
		d.SrcIP.String(),
		d.DstIP.String(),
		strconv.Itoa(int(d.SrcPort)),
		strconv.Itoa(int(d.DstPort)),
		strconv.Itoa(int(d.TransID)),
		getPacketType(d.Response),
		d.Domain,
		d.QueryClass,
		d.QueryType,
		d.Rcode,
		bool2Int(d.Authoritative),
		bool2Int(d.Truncated),
		bool2Int(d.RecursionDesired),
		bool2Int(d.RecursionAvailable),
		bool2Int(d.Zero),
		strconv.FormatInt(d.ResolvDuration.Microseconds(), 10),
		strings.Join(d.Answer, ";"),
		strings.Join(d.Authority, ";"),
		strings.Join(d.Additional, ";"),
	}
	return strings.Join(ss, "|")
}

func (d DnsRecord) Timestamp() time.Time {
	return d.PacketTime
}
