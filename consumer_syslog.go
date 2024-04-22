package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	syslog "github.com/NextronSystems/simplesyslog"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Syslog struct {
	// 目标syslog服务器地址
	remoteAddr string
	// 网络协议(tcp/udp)
	network string
	// 最新一条日志时间
	lastLogTime *time.Time
	pwd         string
}

func NewSysLogConsumer(remoteAddr, network string) *Syslog {
	return &Syslog{
		remoteAddr: remoteAddr,
		network:    network,
	}
}

func (s *Syslog) SetPWD(pwd string) {
	s.pwd = pwd
}

func (s *Syslog) HandleEvents(eventLogs []EventLog) error {
	client, err := syslog.NewClient(syslog.ConnectionType(strings.ToLower(s.network)), s.remoteAddr, &tls.Config{})
	if err != nil {
		logrus.Error("create syslog client  err: ", err.Error())
		return err
	}
	defer client.Close()
	timeFile, err := os.OpenFile(filepath.Join(s.pwd, SysLogLastTime), os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		logrus.Error("create file err: ", err.Error())
		return err
	}
	defer timeFile.Close()

	for i := range eventLogs {
		data, err := json.Marshal(eventLogs[i])
		if err != nil {
			logrus.Error("json marshal err: ", err.Error())
			return err
		}
		if err = send(client, string(data)+"\n", syslog.LOG_INFO); err == nil {
			systemTime, err := formatTime(eventLogs[i].System.TimeCreated.SystemTime)
			if err != nil {
				return err
			}
			// 成功后更新最新一条的日志时间
			if s.lastLogTime.UnixNano() < systemTime.UnixNano() {
				s.lastLogTime = &systemTime
			}

			flushToFile(timeFile, systemTime)
		} else {
			// 出现错误了一般都是syslog访问不通
			logrus.Error("send syslog err: ", err.Error())
			return err
		}
	}

	logrus.Infof("finish to send event log to syslog server, total event log is %d, last event log time is  %s", len(eventLogs), s.lastLogTime)
	return nil
}

func (s *Syslog) LastLogTime() *time.Time {
	if s.lastLogTime == nil {
		lastLogTime, _ := readLastLogTime(s.pwd, SysLogLastTime)
		s.lastLogTime = lastLogTime
	}
	return s.lastLogTime
}

// 读取上一次同步日志时间
func readLastLogTime(pwd, filename string) (*time.Time, error) {
	location, err := time.LoadLocation("Local")
	if err != nil {
		return nil, err
	}
	lastLogTime := time.Now().In(location)
	recordFile := filepath.Join(pwd, filename)
	if _, err := os.Stat(recordFile); err != nil {
		// 不存在该文件就创建
		if os.IsNotExist(err) {
			if _, err := os.Create(recordFile); err != nil {
				logrus.Errorf("create %s file err: %s", recordFile, err.Error())
				return &lastLogTime, err
			}
		} else {
			logrus.Errorf("stat %s file err: %s", recordFile, err.Error())
			return &lastLogTime, err
		}
	}

	data, err := ioutil.ReadFile(recordFile)
	if err != nil {
		logrus.Errorf("readLastLogTime %s file err: %s", recordFile, err.Error())
		return &lastLogTime, err
	}
	if len(data) != 0 && strings.TrimSpace(string(data)) != "" {
		lastLogTime, err = formatTime(strings.TrimSpace(string(data)))
		if err != nil {
			logrus.Errorf("readLastLogTime last log time from %s file err: %s", recordFile, err.Error())
			return &lastLogTime, err
		}
	} else {
		if err := ioutil.WriteFile(recordFile, []byte(lastLogTime.Format(WindowSystemLayout)), 0666); err != nil {
			logrus.Errorf("write last log time to %s file err: %s", recordFile, err.Error())
			return &lastLogTime, err
		}
	}

	return &lastLogTime, nil
}

// 时间写入文件
func flushToFile(file *os.File, lastLogTime time.Time) {
	if _, err := file.WriteAt([]byte(lastLogTime.Format(WindowSystemLayout)), 0); err != nil {
		logrus.Error("write last log time to file err: ", err.Error())
	}
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
