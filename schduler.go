package main

import (
	"errors"
	"fmt"
	"github.com/kardianos/service"
	"github.com/sirupsen/logrus"
	"net"
	"time"
	"windns-logtail/checkpoint"
	"windns-logtail/consumer"
	"windns-logtail/reader"
)

type Scheduler struct {
	// 安装路径
	pwd string
	// 配置文件路径
	configPath string

	readeManager    *reader.Manager
	consumerManager *consumer.Manager
}

func NewScheduler(pwd, configPath string) (*Scheduler, error) {
	if _, err := loadConfig(configPath); err != nil {
		return nil, err
	}
	return &Scheduler{
		pwd:        pwd,
		configPath: configPath,
	}, nil
}
func loadConfig(configPath string) (*Config, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		logrus.Errorf("init config err: %s", err.Error())
		return nil, err
	}

	// 支持ftp
	//if config.FTP.Enable {
	//	ip := net.ParseIP(config.FTP.RemoteAddr)
	//	remoteAddr := ""
	//	if ip.To4() != nil {
	//		remoteAddr = fmt.Sprintf("%s:%d", config.FTP.RemoteAddr, config.FTP.RemotePort)
	//	} else if ip.To16() != nil {
	//		remoteAddr = fmt.Sprintf("[%s]:%d", config.FTP.RemoteAddr, config.FTP.RemotePort)
	//	}
	//	ftp := NewFTPConsumer(remoteAddr,
	//		config.FTP.Username, config.FTP.Password,
	//		config.FTP.FilePath, config.FTP.FileMaxSize,
	//		config.FTP.IsSftp, config.FTP.LogFilePrefix)
	//	ftp.SetPWD(pwd)
	//	consumers = append(consumers, ftp)
	//}
	return config, nil
}

func (s *Scheduler) Start(service service.Service) error {
	// 每次启动都需要读取配置文件，保证在服务管理窗口操作也能生效
	config, err := loadConfig(s.configPath)
	if err != nil {
		return err
	}

	s.readeManager, err = reader.NewManager(s.pwd)
	if err != nil {
		return err
	}
	s.consumerManager, err = consumer.NewManager(s.pwd)
	if err != nil {
		return err
	}

	// analytical文件
	for _, analytical := range config.AnalyticalEntries {
		r, err := s.addReader(analytical, true)
		if err != nil {
			return err
		}
		if err = s.hookConsumer(analytical.Path, config, r); err != nil {
			return err
		}

	}

	// audit文件
	for _, audit := range config.AuditEntries {
		r, err := s.addReader(audit, false)
		if err != nil {
			return err
		}
		if err = s.hookConsumer(audit.Path, config, r); err != nil {
			return err
		}

	}

	s.readeManager.Start()
	if err := s.consumerManager.Start(); err != nil {
		return err
	}

	return nil
}

func (s *Scheduler) addReader(entry Entry, isETL bool) (reader.Reader, error) {
	var logState checkpoint.EventLogState
	if state, ok := s.readeManager.Checkpoint().States()[entry.Path]; !ok {
		logState = checkpoint.EventLogState{
			Name:      entry.Path,
			Timestamp: time.Now(),
		}
		s.readeManager.Checkpoint().PersistState(logState)
	} else {
		logState = state
	}
	var (
		r       reader.Reader
		err     error
		maxRead int64 = 1000
	)
	if isETL {
		r, err = reader.NewETLWorker(s.pwd, entry.Path, entry.TransferThreads, logState)
		if err != nil {
			return nil, err
		}
		maxRead = 10000
	} else {
		lastTime := logState.Timestamp.Unix()
		var options = []reader.ReaderOptions{reader.WithStop()}
		if entry.Level != "" {
			options = append(options, reader.WithLevel(entry.Level))
		}
		if entry.EventID != "" {
			options = append(options, reader.WithEventID(entry.EventID))
		}
		r, err = reader.NewChannelReader(entry.Path, lastTime, logState, options...)
		if err != nil {
			return nil, err
		}
	}

	if err := s.readeManager.AddReader(r, maxRead, entry.Internal); err != nil {
		return nil, err
	}

	return r, nil
}

func (s *Scheduler) hookConsumer(name string, config *Config, r reader.Reader) error {
	if config.SysLog.Enable {
		var consumerState checkpoint.EventLogState
		if state, ok := s.consumerManager.Checkpoint().States()[name]; !ok {
			consumerState = checkpoint.EventLogState{
				Name:      name,
				Timestamp: time.Now(),
			}
			s.consumerManager.Checkpoint().PersistState(consumerState)
		} else {
			consumerState = state
		}

		ip := net.ParseIP(config.SysLog.RemoteAddr)
		remoteAddr := ""
		if ip.To4() != nil {
			remoteAddr = fmt.Sprintf("%s:%d", config.SysLog.RemoteAddr, config.SysLog.RemotePort)
		} else if ip.To16() != nil {
			remoteAddr = fmt.Sprintf("[%s]:%d", config.SysLog.RemoteAddr, config.SysLog.RemotePort)
		} else {
			return errors.New("invalid remote addr")
		}
		syslog, err := consumer.NewSysLogConsumer(name, s.pwd, remoteAddr, config.SysLog.Network, consumerState)
		if err != nil {
			return err
		}
		s.consumerManager.AddConsumer(syslog, r.GetRecords())
	}

	return nil
}

func (s *Scheduler) Stop(service service.Service) error {
	go s.readeManager.Shutdown()
	go s.consumerManager.Shutdown()

	return nil
}
