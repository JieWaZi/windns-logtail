package reader

import (
	"dns-logtail/checkpoint"
	"dns-logtail/eventlog"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"path/filepath"
	"time"
)

const Checkpoint = "reader_checkpoint.yml"

type Reader interface {
	Init() error
	Shutdown()
	SetCron(cron *cron.Cron, internal int64) error
	SetMaxRead(maxRead int)
	SetPoint(point *checkpoint.Checkpoint)
	GetRecords() chan []eventlog.Record
}

type Manager struct {
	Readers []Reader
	cron    *cron.Cron
	point   *checkpoint.Checkpoint
}

func NewManager(pwd string) (*Manager, error) {
	point, err := checkpoint.NewCheckpoint(filepath.Join(pwd, Checkpoint), 1, time.Second)
	if err != nil {
		logrus.Errorf("new checkpoint err: %s", err.Error())
		return nil, err
	}
	return &Manager{
		cron:  cron.New(cron.WithSeconds()),
		point: point,
	}, nil
}

func (m *Manager) AddReader(reader Reader, maxRead, internal int64) error {
	reader.SetMaxRead(int(maxRead))
	reader.SetPoint(m.point)
	if err := reader.Init(); err != nil {
		return err
	}
	if err := reader.SetCron(m.cron, internal); err != nil {
		return err
	}
	m.Readers = append(m.Readers, reader)
	return nil
}

func (m *Manager) Checkpoint() *checkpoint.Checkpoint {
	return m.point
}

func (m *Manager) Start() error {
	if len(m.Readers) == 0 {
		return errors.New("no reader")
	}
	m.cron.Start()

	return nil
}

func (m *Manager) Shutdown() {
	m.cron.Stop()
	m.point.Shutdown()
	for _, reader := range m.Readers {
		go reader.Shutdown()
	}
}
