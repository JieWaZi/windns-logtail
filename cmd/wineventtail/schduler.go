package main

import (
	"dns-logtail/checkpoint"
	"dns-logtail/consumer"
	"dns-logtail/global"
	"dns-logtail/reader"
	"errors"
	"fmt"
	"github.com/kardianos/service"
	"github.com/sirupsen/logrus"
	"net"
	"time"
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
func loadConfig(configPath string) (*global.Config, error) {
	config, err := global.LoadConfig(configPath)
	if err != nil {
		logrus.Errorf("init config err: %s", err.Error())
		return nil, err
	}

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
		if err = s.hookConsumer(analytical, r); err != nil {
			return err
		}

	}

	// audit文件
	for _, audit := range config.AuditEntries {
		r, err := s.addReader(audit, false)
		if err != nil {
			return err
		}
		if err = s.hookConsumer(audit, r); err != nil {
			return err
		}

	}

	// dns抓包
	for _, packet := range config.DNSPackets {
		if err := s.addDNSPacket(packet); err != nil {
			return err
		}
	}

	if err := s.readeManager.Start(); err != nil {
		return err
	}
	if err := s.consumerManager.Start(); err != nil {
		return err
	}

	return nil
}

func (s *Scheduler) addReader(entry global.Entry, isETL bool) (reader.Reader, error) {
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
		var options = []reader.WinEventOptions{reader.WithStop()}
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

func (s *Scheduler) addDNSPacket(packet global.DNSPacket) error {
	for _, deviceName := range packet.DeviceName {
		name := "DNSLog" + deviceName
		var logState checkpoint.EventLogState
		if state, ok := s.readeManager.Checkpoint().States()[name]; !ok {
			logState = checkpoint.EventLogState{
				Name:      name,
				Timestamp: time.Now(),
			}
			s.readeManager.Checkpoint().PersistState(logState)
		} else {
			logState = state
		}

		var ips []net.IP
		for _, ipString := range packet.FilterIPS {
			ip := net.ParseIP(ipString)
			if ip == nil {
				logrus.Warnf("parse ip failed %s", ipString)
			}
			ips = append(ips, ip)
		}
		r := reader.NewDnsPacketReader(deviceName, ips, logState)

		if err := s.readeManager.AddReader(r, 1000, 1); err != nil {
			return err
		}

		if err := s.hookConsumer(global.Entry{Path: name, SysLog: packet.SysLog, FTP: packet.FTP, LogFormat: packet.LogFormat}, r); err != nil {
			return err
		}
	}

	return nil
}

func (s *Scheduler) hookConsumer(entry global.Entry, r reader.Reader) error {
	if entry.SysLog.Enable {
		var consumerState checkpoint.EventLogState
		if state, ok := s.consumerManager.Checkpoint().States()[entry.Path]; !ok {
			consumerState = checkpoint.EventLogState{
				Name:      entry.Path,
				Timestamp: time.Now(),
			}
			s.consumerManager.Checkpoint().PersistState(consumerState)
		} else {
			consumerState = state
		}

		ip := net.ParseIP(entry.SysLog.RemoteAddr)
		if ip == nil {
			return errors.New("invalid remote addr")
		}
		remoteAddr := ""
		if ip.To4() != nil {
			remoteAddr = fmt.Sprintf("%s:%d", entry.SysLog.RemoteAddr, entry.SysLog.RemotePort)
		} else if ip.To16() != nil {
			remoteAddr = fmt.Sprintf("[%s]:%d", entry.SysLog.RemoteAddr, entry.SysLog.RemotePort)
		} else {
			return errors.New("invalid remote addr")
		}
		syslog, err := consumer.NewSysLogConsumer(entry.Path, s.pwd, remoteAddr, entry.SysLog.Network, entry.LogFormat, consumerState)
		if err != nil {
			return err
		}
		s.consumerManager.AddConsumer(entry.Path, syslog, r.GetRecords())
	}

	// 支持ftp
	if entry.FTP.Enable {
		ip := net.ParseIP(entry.FTP.RemoteAddr)
		if ip == nil {
			return errors.New("invalid remote addr")
		}
		remoteAddr := ""
		if ip.To4() != nil {
			remoteAddr = fmt.Sprintf("%s:%d", entry.FTP.RemoteAddr, entry.FTP.RemotePort)
		} else if ip.To16() != nil {
			remoteAddr = fmt.Sprintf("[%s]:%d", entry.FTP.RemoteAddr, entry.FTP.RemotePort)
		}
		if entry.FTP.FileMaxSize <= 0 {
			entry.FTP.FileMaxSize = 10
		}
		ftp, err := consumer.NewFTPConsumer(entry.Path, s.pwd, remoteAddr,
			entry.FTP.Username, entry.FTP.Password,
			entry.FTP.FilePath, entry.FTP.FileMaxSize,
			entry.FTP.IsSftp, entry.FTP.LogFilePrefix, entry.LogFormat)
		if err != nil {
			return err
		}
		s.consumerManager.AddConsumer(entry.Path, ftp, r.GetRecords())
	}

	return nil
}

func (s *Scheduler) Stop(service service.Service) error {
	go s.readeManager.Shutdown()
	go s.consumerManager.Shutdown()

	return nil
}
