package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/kardianos/service"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type WatcherManager struct {
	// 安装路径
	pwd string
	// 配置文件路径
	configPath string
	cron       *cron.Cron
}

func NewAnalyticalManager(pwd, configPath string) *WatcherManager {
	return &WatcherManager{
		pwd:        pwd,
		configPath: configPath,
	}
}

func (a *WatcherManager) Start(s service.Service) error {
	// 每次启动都需要读取配置文件，保证在服务管理窗口操作也能生效
	config, err := LoadConfig(a.configPath)
	if err != nil {
		logrus.Errorf("init config err: %s", err.Error())
		return err
	}

	a.cron = cron.New(cron.WithSeconds())
	watcher := NewWatcher(a.pwd, config.AnalyticalPath)

	// 支持syslog
	if config.SysLog.Enable {
		ip := net.ParseIP(config.SysLog.RemoteAddr)
		remoteAddr := ""
		if ip.To4() != nil {
			remoteAddr = fmt.Sprintf("%s:%d", config.SysLog.RemoteAddr, config.SysLog.RemotePort)
		} else if ip.To16() != nil {
			remoteAddr = fmt.Sprintf("[%s]:%d", config.SysLog.RemoteAddr, config.SysLog.RemotePort)
		}
		syslog := NewSysLogConsumer(remoteAddr, config.SysLog.Network)
		syslog.SetPWD(a.pwd)
		watcher.AddConsumer(syslog)

	}

	// 支持ftp
	if config.FTP.Enable {
		ip := net.ParseIP(config.FTP.RemoteAddr)
		remoteAddr := ""
		if ip.To4() != nil {
			remoteAddr = fmt.Sprintf("%s:%d", config.FTP.RemoteAddr, config.FTP.RemotePort)
		} else if ip.To16() != nil {
			remoteAddr = fmt.Sprintf("[%s]:%d", config.FTP.RemoteAddr, config.FTP.RemotePort)
		}
		ftp := NewFTPConsumer(remoteAddr,
			config.FTP.Username, config.FTP.Password,
			config.FTP.FilePath, config.FTP.FileMaxSize,
			config.FTP.IsSftp, config.FTP.LogFilePrefix)
		ftp.SetPWD(a.pwd)
		watcher.AddConsumer(ftp)
	}

	// 加载之前存在的日志文件
	watcher.LoadFiles()

	if _, err := a.cron.AddJob(fmt.Sprintf("@every %ds", config.Period), watcher); err != nil {
		logrus.Errorf("add watcher job err: %s", err.Error())
		return err
	}

	a.cron.Start()

	return nil
}

func (a *WatcherManager) Stop(s service.Service) error {
	a.cron.Stop()
	return nil
}

type LogConsumer interface {
	HandleEvents(eventLogs []EventLog) error
	LastLogTime() *time.Time
}

type Watcher struct {
	// 分析日志路径
	analyticalPath string
	pwd            string

	lock   sync.RWMutex
	worker Worker
}

func NewWatcher(pwd, analyticalPath string) *Watcher {
	return &Watcher{
		pwd:            pwd,
		analyticalPath: analyticalPath,
		lock:           sync.RWMutex{},
		worker:         Worker{pwd: pwd, transferChan: make(chan string, 1000), handleChan: make(chan string, 1000)},
	}
}

func (w *Watcher) AddConsumer(consumer LogConsumer) {
	w.worker.consumers = append(w.worker.consumers, consumer)
}

func (w *Watcher) LoadFiles() {
	files, err := listlogruss(filepath.Join(w.pwd, "logs"))
	if err != nil {
		logrus.Error(err.Error())
		return
	}
	for _, file := range files {
		if strings.Contains(file, ".etl") {
			w.worker.transferChan <- file
		}
		if strings.Contains(file, ".xml") {
			w.worker.handleChan <- file
		}
	}

	go w.worker.transfer()
	go w.worker.handle()
}

func (w *Watcher) Run() {
	// 捕获异常到日志
	defer func() {
		if err := recover(); err != nil {
			logrus.Error(err)
		}
	}()

	if !w.lock.TryLock() {
		logrus.Warn("watch task not finished,skip")
		return
	}
	defer w.lock.Unlock()

	// 将日志转存到指定目录
	logPath := filepath.Join(w.pwd, "logs")
	_, err := os.Stat(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err = os.Mkdir(logPath, 0600); err != nil {
				logrus.Errorf("Mkdir log path err: %s", err.Error())
				return
			}
		} else {
			logrus.Errorf("Mkdir log path err: %s", err.Error())
			return
		}
	}

	logFile := filepath.Join(logPath, fmt.Sprintf("analytical%d.etl", time.Now().UnixMicro()))
	if err := copyFile(w.analyticalPath, logFile); err != nil {
		logrus.Errorf("copy analytical file err: %s", err.Error())
		return
	}

	w.worker.transferChan <- logFile
}

type Worker struct {
	pwd string

	consumers []LogConsumer

	transferChan chan string
	handleChan   chan string
}

func (w *Worker) transfer() {
	logrus.Info("start transfer etl.....")
	for {
		select {
		case f := <-w.transferChan:
			// 将etl文件转换成xml文件
			tmpEvt, err := transfer(w.pwd, f)
			if err != nil {
				logrus.Errorf("transfer etl file err: %s", err.Error())
				continue
			}
			w.handleChan <- tmpEvt

			// 处理完了需要删除文件
			if err := os.RemoveAll(f); err != nil {
				logrus.Errorf("rm %s err: %s", f, err.Error())
			}
		}
	}

}

func (w *Worker) handle() {
	logrus.Info("start handle xml.....")
	for {
		select {
		case f := <-w.handleChan:
			var group = &errgroup.Group{}

			for i := range w.consumers {
				consumer := w.consumers[i]
				group.Go(func() error {
					// 读取xml文件获取更新的日志
					logs, err := scanXML(f, consumer.LastLogTime())
					if err != nil {
						return err
					}
					// 处理增量日志
					return consumer.HandleEvents(logs)
				})
			}

			if err := group.Wait(); err != nil {
				logrus.Errorf("HandleEvents err: %s", err.Error())
			}

			// 处理完了需要删除文件
			if err := os.RemoveAll(f); err != nil {
				logrus.Errorf("rm %s err: %s", f, err.Error())
			}
		}
	}

}

// 拷贝文件到指定目录
func copyFile(src, dest string) error {
	// 打开源文件
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %v", err)
	}
	defer srcFile.Close()

	// 创建目标文件
	destFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %v", err)
	}
	defer destFile.Close()

	// 拷贝文件内容
	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy file: %v", err)
	}

	return nil
}

// 获取日志文件
func listlogruss(dirPath string) ([]string, error) {
	// 打开目录
	dir, err := os.Open(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to open directory: %v", err)
	}
	defer dir.Close()

	// 读取目录下的文件和子目录
	fileInfos, err := dir.Readdir(-1)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %v", err)
	}

	var filenames []string
	for _, file := range fileInfos {
		if !file.IsDir() {
			filenames = append(filenames, filepath.Join(dirPath, file.Name()))
		}
	}

	return filenames, nil
}

// 将etl文件转化为xml文件
func transfer(pwd, analyticalPath string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	// 记录开始时间
	startTime := time.Now()

	tmpEvt := strings.Replace(analyticalPath, ".etl", ".xml", -1)
	// tracerpt.exe Microsoft-Windows-DNSServer%4Analytical2.etl  -of XML -o dns_analytical.evtx -y
	cmd := exec.Command(Powershell, fmt.Sprintf(`%s %s -of XML -o %s -y`, TracerptExe, analyticalPath, tmpEvt))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		logrus.Errorf("exec %s err: %s", cmd, err.Error())
		return "", err
	}
	if stderr.String() != "" {
		logrus.Errorf("exec %s err: %s", cmd, stderr.String())
		return "", errors.New(stderr.String())
	}

	logrus.Infof("finish to transfer etl to evtx, cost: %v", time.Since(startTime))

	return tmpEvt, nil
}

// 解析xml文件，获取增量日志
func scanXML(file string, lastLogTime *time.Time) ([]EventLog, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		logrus.Errorf("scan %s xml err: %s", file, err.Error())
		return nil, err
	}
	events := new(Events)
	if err := xml.Unmarshal(data, events); err != nil {
		logrus.Errorf("unmarshal %s xml err: %s", file, err.Error())
		return nil, err
	}

	// 筛选出大于上次更新时的记录
	var eventLogs []EventLog
	for _, event := range events.EventLogs {
		systemTime, err := formatTime(event.System.TimeCreated.SystemTime)
		if err != nil {
			logrus.Error(event.System.TimeCreated.SystemTime, "parse time err: ", err.Error())
			continue
		}
		if lastLogTime.UnixNano() >= systemTime.UnixNano() {
			continue
		}
		eventLogs = append(eventLogs, event)
	}

	return eventLogs, nil
}

func formatTime(date string) (time.Time, error) {
	var (
		formatTime time.Time
		err        error
	)
	// 通过XML获取的时间会携带时区，统一按照当前环境的时区进行处理
	if len(date) > len(WindowSystemLayout) {
		date = date[:len(WindowSystemLayout)]
	}
	location, err := time.LoadLocation("Local")
	if err != nil {
		return formatTime, err
	}
	formatTime, err = time.ParseInLocation(WindowSystemLayout, date, location)
	if err != nil {
		return formatTime, err
	}

	return formatTime, nil
}
