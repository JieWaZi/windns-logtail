package reader

import (
	"dns-logtail/checkpoint"
	"dns-logtail/eventlog"
	"fmt"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/miekg/dns"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	snapshotLen = 1500
	promiscuous = true
	timeout     = 3 * time.Second
	bpfFilter   = "udp and port 53"
)

type DnsPacketReader struct {
	deviceName string
	filterIPs  []net.IP
	maxRead    int

	state checkpoint.EventLogState
	point *checkpoint.Checkpoint

	handle     *pcap.Handle
	source     *gopacket.PacketSource
	records    chan []eventlog.Record
	tmpRecords []eventlog.Record
	stopChan   chan struct{}
	running    bool
	lock       sync.RWMutex
}

func NewDnsPacketReader(deviceName string, filterIPs []net.IP, state checkpoint.EventLogState) *DnsPacketReader {
	return &DnsPacketReader{
		deviceName: deviceName,
		filterIPs:  filterIPs,
		records:    make(chan []eventlog.Record, 10000),
		stopChan:   make(chan struct{}, 1),
		lock:       sync.RWMutex{},
		state:      state,
	}
}

func (r *DnsPacketReader) Init() error {
	fmt.Println("init dns packet reader")
	handle, err := pcap.OpenLive(r.deviceName, snapshotLen, promiscuous, timeout)
	if err != nil {
		logrus.Errorf("open pcap device %s failed %s", r.deviceName, err)
		return err
	}
	r.handle = handle

	bpf := getBpfFilterString(r.filterIPs)
	if err := handle.SetBPFFilter(bpf); err != nil {
		logrus.Errorf("set bfp filter failed [%s] %s", bpf, err)
		return err
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	r.source = packetSource
	fmt.Println("finish init dns packet reader")
	return nil
}

func (r *DnsPacketReader) Run() {
	if r.running {
		return
	} else {
		r.running = true
	}
	ticker := time.NewTicker(time.Second)
	for {
		select {
		case p, ok := <-r.source.Packets():
			ticker.Reset(time.Second)
			if !ok {
				logrus.Infof("handle groutinue exiting by no packets")
				return
			}

			record, err := unpack(p)
			if err != nil {
				logrus.Warnf("unpack dns packet failed %s", err)
				continue
			}
			p = nil
			r.tmpRecords = append(r.tmpRecords, record)
			if r.maxRead <= len(r.tmpRecords) {
				r.save()
			}
		case <-ticker.C:
			if len(r.tmpRecords) > 0 {
				r.save()
			}
			ticker.Reset(time.Second)
		case <-r.stopChan:
			ticker.Stop()
			break
		}
	}
}

func (r *DnsPacketReader) Shutdown() {
	logrus.Info("stop dns packet reader")
	r.handle.Close()
	r.stopChan <- struct{}{}
	close(r.records)
	close(r.stopChan)
	r.running = false
	r.tmpRecords = nil
}

func (r *DnsPacketReader) GetRecords() chan []eventlog.Record {
	return r.records
}

func (r *DnsPacketReader) save() {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.state = checkpoint.EventLogState{
		Name:      "DNSLog" + r.deviceName,
		Timestamp: r.tmpRecords[len(r.tmpRecords)-1].Timestamp(),
	}
	r.point.PersistState(r.state)
	logrus.Infof("add %d dns packet records to channel", len(r.tmpRecords))
	r.records <- r.tmpRecords
	r.tmpRecords = nil
	r.tmpRecords = make([]eventlog.Record, 0)
}

func (r *DnsPacketReader) SetPoint(point *checkpoint.Checkpoint) {
	r.point = point
}
func (r *DnsPacketReader) SetCron(cron *cron.Cron, internal int64) error {
	_, err := cron.AddJob(fmt.Sprintf("@every %ds", internal), r)
	return err
}

func (r *DnsPacketReader) SetMaxRead(maxRead int) {
	r.maxRead = maxRead
}

func getBpfFilterString(ips []net.IP) string {
	if len(ips) == 0 {
		return bpfFilter
	}

	var (
		hosts  []string
		filter string
	)
	for _, ip := range ips {
		hosts = append(hosts, fmt.Sprintf("host %s ", ip.String()))
	}
	filter += strings.Join(hosts, "or ")
	filter += "and " + bpfFilter

	return filter
}

func unpack(p gopacket.Packet) (*eventlog.DnsRecord, error) {
	dl := &eventlog.DnsRecord{}
	if p.Metadata() == nil {
		return nil, fmt.Errorf("packet metadata missing")
	}
	dl.PacketTime = p.Metadata().Timestamp

	ipLayer := p.Layer(layers.LayerTypeIPv4)
	if ipLayer != nil {
		ip, ok := ipLayer.(*layers.IPv4)
		if !ok {
			return nil, fmt.Errorf("packet convert ip layer to ipv4 failed")
		}
		dl.SrcIP = ip.SrcIP
		dl.DstIP = ip.DstIP
	} else {
		ipLayer := p.Layer(layers.LayerTypeIPv6)
		if ipLayer == nil {
			return nil, fmt.Errorf("packet missing ip layer")
		}
		ip, ok := ipLayer.(*layers.IPv6)
		if !ok {
			return dl, fmt.Errorf("packet convert ip layer to ipv6 failed")
		}
		dl.SrcIP = ip.SrcIP
		dl.DstIP = ip.DstIP
	}

	udpLayer := p.Layer(layers.LayerTypeUDP)
	if udpLayer == nil {
		return dl, fmt.Errorf("packet missing udp layer")
	}
	udp, ok := udpLayer.(*layers.UDP)
	if !ok {
		return dl, fmt.Errorf("packet convert udp layer to udp failed")
	}
	dl.SrcPort = uint16(udp.SrcPort)
	dl.DstPort = uint16(udp.DstPort)

	msg := new(dns.Msg)
	if err := msg.Unpack(udp.Payload); err != nil {
		return dl, fmt.Errorf("packet unpack to dns msg failed %s", err)
	}

	eventlog.DnsRecordFromMsg(msg, dl)
	return dl, nil
}

func (r *DnsPacketReader) GetState() checkpoint.EventLogState {
	return r.state
}

func (r *DnsPacketReader) SetState(state checkpoint.EventLogState) {
	r.state = state
}
