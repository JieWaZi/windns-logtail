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
	// 配置名称
	ConfigName = "win-logtail.yml"
	// 备份日志路径
	ArchiveLogPath = "archiveLog"
	// 日志文件
	AnalyticalLog = "win-logtail.log"
)

type Config struct {
	// 安装路径
	InstallationPath string `yaml:"-"`
	// analytical类型文件
	AnalyticalEntries []Entry `yaml:"analytical_entries"`
	// audit类型文件
	AuditEntries []Entry `yaml:"audit_entries"`
	// syslog服务器配置
	SysLog SysLogConfig `yaml:"syslog"`
	// ftp服务器
	FTP FTPConfig `yaml:"ftp"`
}

type Entry struct {
	//  文件路径
	Path string `yaml:"path"`
	// 文件拷贝间隔
	Internal int64 `yaml:"internal"`
	// 日志登记
	Level string `yaml:"level"`
	// 事件ID范围
	EventID string `yaml:"event_id"`
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

	if c.FTP.Enable {
		_ = os.MkdirAll(filepath.Join(installationPath, ArchiveLogPath), 0666)
		// 文件最大值默认为1M
		if c.FTP.FileMaxSize <= 0 {
			c.FTP.FileMaxSize = 1
		}
	}

	InitLogger(filepath.Join(installationPath, AnalyticalLog), logrus.InfoLevel.String(), false, false)
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
