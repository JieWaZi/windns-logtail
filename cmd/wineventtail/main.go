package main

import (
	"dns-logtail/global"
	"flag"
	"github.com/kardianos/service"
	"github.com/sirupsen/logrus"
	"path/filepath"
)

var (
	serviceParam string
	confFile     string
)

func init() {
	flag.StringVar(&serviceParam, "service", "", "Control the system service.")
	flag.StringVar(&confFile, "conf", global.WinConfigName, "Control the system service.")
}

func main() {
	flag.Parse()

	// 获取安装路径
	installationPath := global.GetCurrentAbPath()

	serviceConfig := &service.Config{
		Name:        "Windows-Log-Service",
		DisplayName: "Windows Log Service",
		Description: "Windows Log Service是一个Windows服务增强插件，提供Windows日志上传备份功能，" +
			"支持通过Syslog、FTP和SFTP方式将Windows日志上传至外部日志服务器，方便管理员运维",
		Option: map[string]interface{}{
			// 开机自启动
			"DelayedAutoStart": true,
		},
		// 设置配置文件路径
		Arguments: []string{"-conf", filepath.Join(installationPath, global.WinConfigName)},
	}
	scheduler, err := NewScheduler(installationPath, confFile)
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
