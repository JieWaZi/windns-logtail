package consumer

import (
	"dns-logtail/checkpoint"
	"dns-logtail/eventlog"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
	"path/filepath"
	"sync"
	"time"
)

// dns查询日志的时间格式
const (
	// 备份日志路径
	ArchiveLogPath = "archive-log"
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
	consumes []Consumer
	events   chan []eventlog.Record
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

func (m *Manager) AddConsumer(topic string, consumer Consumer, events chan []eventlog.Record) {
	consumer.SetPoint(m.point)
	if binder, ok := m.consumers[topic]; ok {
		binder.consumes = append(binder.consumes, consumer)
		m.consumers[topic] = binder
	} else {
		m.consumers[topic] = consumeBinder{
			consumes: []Consumer{consumer},
			events:   events,
		}
	}

	m.stopChan[topic] = make(chan struct{}, 1)
}

func (m *Manager) Start() error {
	for topic, binder := range m.consumers {
		err := m.pool.Submit(func(topic string, binder consumeBinder) func() {
			return func() {
				logrus.Infof("start consume topic: %s", topic)
				for {
					select {
					case <-m.stopChan[topic]:
						break
					case events := <-binder.events:
						var wg = &sync.WaitGroup{}
						wg.Add(len(binder.consumes))
						for _, consume := range binder.consumes {
							go func(consume Consumer, events []eventlog.Record) {
								defer wg.Done()
								if err := consume.HandleEvents(events); err != nil {
									logrus.Errorln("handle events err: ", err.Error())
								}
							}(consume, events)
						}
						wg.Wait()
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
		for _, consume := range binder.consumes {
			go consume.Shutdown()
		}
		m.stopChan[topic] <- struct{}{}
		close(m.stopChan[topic])
	}
	m.pool.Release()
}
