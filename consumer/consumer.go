package consumer

import (
	"github.com/elastic/beats/winlogbeat/eventlog"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
	"path/filepath"
	"time"
	"windns-logtail/checkpoint"
)

// dns查询日志的时间格式
const (
	WindowSystemLayout = "2006-01-02T15:04:05.000000000"
	// 记录上一次同步最新的日志时间
	SysLogLastTime = "syslog-last-time.txt"
	// 记录上一次同步最新的日志时间
	FTPLastTime = "ftp-last-time.txt"
	// 备份日志路径
	ArchiveLogPath = "archiveLog"
	// 备份日志名称
	ArchiveLog = "archive-dns-analytical.log"
	// 事件检查点
	Checkpoint = "consumer_checkpoint.yml"
)

type Consumer interface {
	Name() string
	SetPoint(point *checkpoint.Checkpoint)
	HandleEvents(eventLogs []eventlog.Record) error
	Shutdown()
}

type consumeBinder struct {
	consume Consumer
	events  chan []eventlog.Record
}

type Manager struct {
	stopChan  map[string]chan struct{}
	consumers map[string]consumeBinder
	point     *checkpoint.Checkpoint
	pool      *ants.Pool
}

func NewManager(pwd string) (*Manager, error) {
	point, err := checkpoint.NewCheckpoint(filepath.Join(pwd, Checkpoint), 1, time.Second)
	if err != nil {
		logrus.Errorf("new checkpoint err: %s", err.Error())
		return nil, err
	}
	pool, err := ants.NewPool(0)
	if err != nil {
		return nil, err
	}
	return &Manager{
		consumers: make(map[string]consumeBinder),
		stopChan:  make(map[string]chan struct{}),
		pool:      pool,
		point:     point,
	}, nil
}

func (m *Manager) Checkpoint() *checkpoint.Checkpoint {
	return m.point
}

func (m *Manager) AddConsumer(consumer Consumer, events chan []eventlog.Record) {
	consumer.SetPoint(m.point)
	m.consumers[consumer.Name()] = consumeBinder{
		consume: consumer,
		events:  events,
	}
	m.stopChan[consumer.Name()] = make(chan struct{})
}

func (m *Manager) Start() error {
	for topic, binder := range m.consumers {
		m.stopChan[topic] = make(chan struct{})
		err := m.pool.Submit(func(topic string, binder consumeBinder) func() {
			return func() {
				logrus.Infof("start consume topic: %s", topic)
				for {
					select {
					case <-m.stopChan[topic]:
						break
					case events := <-binder.events:
						if err := binder.consume.HandleEvents(events); err != nil {
							logrus.Errorln("handle events err: ", err.Error())
						}
					}
				}
			}
		}(topic, binder))
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Shutdown() {
	for topic, binder := range m.consumers {
		binder.consume.Shutdown()
		m.stopChan[topic] <- struct{}{}
		close(m.stopChan[topic])
	}
	m.pool.Release()
}
