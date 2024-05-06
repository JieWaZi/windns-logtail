package consumer

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	syslog "github.com/NextronSystems/simplesyslog"
	"github.com/elastic/beats/winlogbeat/eventlog"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
	"strings"
	"sync"
	"time"
	"windns-logtail/checkpoint"
)

type Syslog struct {
	name string
	pwd  string
	// 目标syslog服务器地址
	remoteAddr string
	// 网络协议(tcp/udp)
	network string
	// 最近一次写入的状态
	state checkpoint.EventLogState
	point *checkpoint.Checkpoint
	pool  *ants.Pool
}

func NewSysLogConsumer(name, pwd, remoteAddr, network string, state checkpoint.EventLogState) (*Syslog, error) {
	pool, err := ants.NewPool(100, ants.WithPreAlloc(true))
	if err != nil {
		return nil, err
	}
	return &Syslog{
		name:       name,
		pwd:        pwd,
		remoteAddr: remoteAddr,
		network:    network,
		state:      state,
		pool:       pool,
	}, nil
}

func (s *Syslog) Name() string {
	return s.name
}

func (s *Syslog) SetPoint(point *checkpoint.Checkpoint) {
	s.point = point
}

func (s *Syslog) Shutdown() {
	s.pool.Release()
}

func (s *Syslog) HandleEvents(events []eventlog.Record) error {
	startTime := time.Now()

	if len(events) == 0 {
		return nil
	}

	startIndex := 0

	for startIndex < len(events) {
		records, newIndex := readChunk(events, 100, startIndex)
		if s.state.Timestamp.UnixNano() >= records[len(records)-1].TimeCreated.SystemTime.UnixNano() {
			continue
		}

		if err := s.batchSend(records); err != nil {
			logrus.Errorf("send event log to syslog server err: %s", err.Error())
		}

		s.state.Timestamp = records[len(records)-1].TimeCreated.SystemTime
		s.point.PersistState(s.state)
		// 更新起始索引
		startIndex = newIndex
	}

	logrus.Infof("finish to send event log to syslog server, total event log is %d, cost:%dms, last event log time is  %s",
		len(events), time.Now().Sub(startTime).Milliseconds(), s.state.Timestamp)
	return nil
}

func (s *Syslog) batchSend(records []eventlog.Record) error {
	client, err := syslog.NewClient(syslog.ConnectionType(strings.ToLower(s.network)), s.remoteAddr, &tls.Config{})
	if err != nil {
		logrus.Errorln("create syslog client  err: ", err.Error())
		return err
	}
	client.SetDeadline(time.Now().Add(30 * time.Second))
	defer func() {
		if err = client.Close(); err != nil {
			logrus.Errorln("syslog client close err: ", err.Error())
		}
	}()

	var wg = &sync.WaitGroup{}
	for _, record := range records {
		wg.Add(1)
		err := s.pool.Submit(func(record eventlog.Record) func() {
			return func() {
				defer wg.Done()
				data, err := json.Marshal(record.Event)
				if err != nil {
					logrus.Errorln("json marshal err: ", err.Error())
					return
				}
				if s.state.Timestamp.UnixNano() >= record.TimeCreated.SystemTime.UnixNano() {
					return
				}
				_ = send(client, string(data)+"\n", syslog.LOG_INFO)
			}
		}(record))
		if err != nil {
			return err
		}
	}
	wg.Wait()

	return nil
}

func send(client *syslog.Client, message string, priority syslog.Priority) error {
	var timestamp string
	timestamp = time.Now().Format(time.RFC3339)
	var header string
	if client.NoPrio {
		header = fmt.Sprintf("%s %s", timestamp, client.Hostname)
	} else {
		header = fmt.Sprintf("<%d>%s %s", int(priority), timestamp, client.Hostname)
	}
	return client.SendRaw(fmt.Sprintf("%s %s", header, message))
}

func readChunk(arr []eventlog.Record, chunkSize int, startIndex int) ([]eventlog.Record, int) {
	endIndex := startIndex + chunkSize
	if endIndex > len(arr) {
		endIndex = len(arr)
	}
	return arr[startIndex:endIndex], endIndex
}
