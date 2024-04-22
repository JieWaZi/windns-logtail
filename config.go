package main

import (
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// etl转换成xml的文件名
	AnalyticalFileName = "dns-analytical.xml"
	// 配置名称
	ConfigName = "dns-analytical.yml"
	// 转换工具命令
	TracerptExe = "tracerpt.exe"
	// powershell命令
	Powershell = "powershell.exe"

	// 记录上一次同步最新的日志时间
	SysLogLastTime = "syslog-last-time.txt"
	// 记录上一次同步最新的日志时间
	FTPLastTime = "ftp-last-time.txt"
	// 备份日志路径
	ArchiveLogPath = "archiveLog"
	// 备份日志名称
	ArchiveLog = "archive-dns-analytical.log"

	// 日志文件
	AnalyticalLog = "win-dns-analytical.log"
	// dns查询日志的时间格式
	WindowSystemLayout = "2006-01-02T15:04:05.000000000"
)

type Config struct {
	// 安装路径
	InstallationPath string `yaml:"-"`
	// dns analytical日志文件
	AnalyticalPath string `yaml:"analytical_path"`
	// 监控周期(秒)
	Period int64 `yaml:"period"`
	// syslog服务器配置
	SysLog SysLogConfig `yaml:"syslog"`
	// ftp服务器
	FTP FTPConfig `yaml:"ftp"`
}

type SysLogConfig struct {
	// 地址
	RemoteAddr string `yaml:"remote_addr"`
	// 端口
	RemotePort int `yaml:"remote_port"`
	// 网络协议(udp/tcp)
	Network string `yaml:"network"`
	// 是否启用
	Enable bool `yaml:"enable"`
}

type FTPConfig struct {
	// 地址
	RemoteAddr string `yaml:"remote_addr"`
	// 端口
	RemotePort int `yaml:"remote_port"`
	// 账号
	Username string `yaml:"username"`
	// 密码
	Password string `yaml:"password"`
	// 文件路径
	FilePath string `yaml:"file_path"`
	// 文件上传最大值(MB)
	FileMaxSize int64 `yaml:"file_max_size"`
	// 是否启用
	Enable bool `yaml:"enable"`
	// 是否是sftp
	IsSftp bool `yaml:"is_sftp"`
	// 日志文件前缀
	LogFilePrefix string `yaml:"log_file_prefix"`
}

func (c *Config) init() {
	installationPath := getCurrentAbPath()
	// 周期默认为30s
	if c.Period <= 0 {
		c.Period = 30
	}

	if c.FTP.Enable {
		_ = os.MkdirAll(filepath.Join(installationPath, ArchiveLogPath), 0666)
		// 文件最大值默认为1M
		if c.FTP.FileMaxSize <= 0 {
			c.FTP.FileMaxSize = 1
		}
	}

	InitLogger(filepath.Join(installationPath, ArchiveLog), logrus.InfoLevel.String(), false, false)
}

func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	config.init()

	return &config, nil
}

// 获取安装的路径，对于window使用pwd得到的不一定是安装路径，而是C:\Window\System32
func getCurrentAbPath() string {
	dir := getCurrentAbPathByExecutable()
	tmpDir, _ := filepath.EvalSymlinks(os.TempDir())
	if strings.Contains(dir, tmpDir) {
		return getCurrentAbPathByCaller()
	}
	return dir
}

// 获取当前执行文件绝对路径
func getCurrentAbPathByExecutable() string {
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	res, _ := filepath.EvalSymlinks(filepath.Dir(exePath))
	return res
}

// 获取当前执行文件绝对路径
func getCurrentAbPathByCaller() string {
	var abPath string
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		abPath = path.Dir(filename)
	}
	return abPath
}
