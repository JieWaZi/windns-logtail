package consumer

import (
	"crypto/tls"
	"dns-logtail/checkpoint"
	"dns-logtail/eventlog"
	"encoding/json"
	"fmt"
	syslog "github.com/NextronSystems/simplesyslog"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
	"strings"
	"sync"
	"time"
)

type Syslog struct {
	name string
	pwd  string
	// 目标syslog服务器地址
	remoteAddr string
	// 网络协议(tcp/udp)
	network string
	// 日志格式
	logFormat string
	// 最近一次写入的状态
	state checkpoint.EventLogState
	point *checkpoint.Checkpoint
	pool  *ants.Pool

	wg *sync.WaitGroup
}

func NewSysLogConsumer(name, pwd, remoteAddr, network, logFormat string, state checkpoint.EventLogState) (*Syslog, error) {
	pool, err := ants.NewPool(100, ants.WithPreAlloc(true))
	if err != nil {
		return nil, err
	}
	return &Syslog{
		name:       name,
		pwd:        pwd,
		remoteAddr: remoteAddr,
		network:    network,
		logFormat:  logFormat,
		state:      state,
		pool:       pool,
		wg:         &sync.WaitGroup{},
	}, nil
}

func (s *Syslog) Name() string {
	return s.name
}

func (s *Syslog) SetPoint(point *checkpoint.Checkpoint) {
	s.point = point
}

func (s *Syslog) Shutdown() {
	s.wg = nil
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
		if s.state.Timestamp.UnixNano() >= records[len(records)-1].Timestamp().UnixNano() {
			continue
		}

		if err := s.batchSend(records); err != nil {
			logrus.Errorf("send eventlog log to syslog server err: %s", err.Error())
		}

		s.state.Timestamp = records[len(records)-1].Timestamp()
		s.point.PersistState(s.state)
		// 更新起始索引
		startIndex = newIndex
	}

	logrus.Infof("finish to send eventlog log to syslog server, total eventlog log is %d, cost:%dms, last eventlog log time is  %s",
		len(events), time.Now().Sub(startTime).Milliseconds(), s.state.Timestamp)
	events = nil
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

	for _, record := range records {
		s.wg.Add(1)
		err := s.pool.Submit(func(record eventlog.Record) func() {
			return func() {
				defer s.wg.Done()
				if s.state.Timestamp.UnixNano() >= record.Timestamp().UnixNano() {
					return
				}
				if s.logFormat == "json" {
					data, _ := json.Marshal(record)
					_ = send(client, string(data)+"\n", syslog.LOG_INFO)
				} else {
					_ = send(client, string(record.String())+"\n", syslog.LOG_INFO)
				}
			}
		}(record))
		if err != nil {
			return err
		}
	}
	s.wg.Wait()

	return nil
}

func send(client *syslog.Client, message string, priority syslog.Priority) error {
	timer := time.NewTimer(10 * time.Second)
	done := make(chan struct{}, 1)
	defer close(done)

	go func() {
		var timestamp string
		timestamp = time.Now().Format(time.RFC3339)
		var header string
		if client.NoPrio {
			header = fmt.Sprintf("%s %s", timestamp, client.Hostname)
		} else {
			header = fmt.Sprintf("<%d>%s %s", int(priority), timestamp, client.Hostname)
		}
		if err := client.SendRaw(fmt.Sprintf("%s %s", header, message)); err != nil {
			logrus.Errorf("send %s to syslog server err: %s", message, err.Error())
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
		timer.Stop()
		return nil
	case <-timer.C:
		logrus.Errorf("send %s to syslog server timeout", message)
		return nil
	}
}

func readChunk(arr []eventlog.Record, chunkSize int, startIndex int) ([]eventlog.Record, int) {
	endIndex := startIndex + chunkSize
	if endIndex > len(arr) {
		endIndex = len(arr)
	}
	return arr[startIndex:endIndex], endIndex
}
