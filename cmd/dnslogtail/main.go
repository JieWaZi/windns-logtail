package main

import (
	"flag"
	"github.com/kardianos/service"
	"github.com/sirupsen/logrus"
	"os"
	"path/filepath"
)

const (
	confPath = "/etc/dnslogtail/dnslogtail.yml"
)

var (
	serviceParam string
	confFile     string
)

func init() {
	flag.StringVar(&serviceParam, "service", "", "Control the system service.")
	flag.StringVar(&confFile, "conf", confPath, "Service config path")
}

func main() {
	flag.Parse()

	libPath := os.Getenv("LD_LIBRARY_PATH")
	if libPath == "" {
		libPath = "/usr/local/lib:/usr/lib"
	}

	serviceConfig := &service.Config{
		Name:        "dnslogtail",
		DisplayName: "DNS Log Tail",
		Description: "DNS Log Tail是一个DNS解析日志抓包工具，提供DNS解析日志上传备份功能，" +
			"支持通过Syslog、FTP和SFTP方式将日志上传至外部日志服务器，方便管理员运维",
		Option: map[string]interface{}{
			// 开机自启动
			"DelayedAutoStart": true,
		},
		// 设置配置文件路径
		Arguments: []string{"-conf", confFile},
		// 设置环境变量
		EnvVars: map[string]string{
			"LD_LIBRARY_PATH": libPath,
		},
	}

	scheduler, err := NewScheduler(filepath.Dir(confFile), confFile)
	if err != nil {
		panic(err)
	}
	s, err := service.New(scheduler, serviceConfig)
	if err != nil {
		logrus.Error(err)
		panic(err)
	}

	if serviceParam != "" {
		if err := service.Control(s, serviceParam); err != nil {
			logrus.Error(err)
			panic(err)
		}
		return
	}

	if err := s.Run(); err != nil {
		logrus.Error(err)
		panic(err)
	}
}
