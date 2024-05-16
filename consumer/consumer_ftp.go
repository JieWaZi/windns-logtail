package consumer

import (
	"archive/zip"
	"bytes"
	"fmt"
	"github.com/jlaffaye/ftp"
	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
	"windns-logtail/checkpoint"
	"windns-logtail/eventlog"
)

type FTP struct {
	name string
	// 目标syslog服务器地址
	remoteAddr string
	// 账号
	username string
	// 密码
	password string
	// 文件路径
	filePath string
	// 文件上传最大值(MB)
	fileMaxSize int64
	// 是否是sftp
	isSftp bool
	// 文件归档前缀
	logFilePrefix string

	pwd string

	state checkpoint.EventLogState
	point *checkpoint.Checkpoint

	lock sync.RWMutex
}

func NewFTPConsumer(pwd, remoteAddr, username, password, filePath string,
	fileMaxSize int64, isSftp bool, logFilePrefix string) (*FTP, error) {
	if !exists(filepath.Join(pwd, ArchiveLogPath)) {
		if err := os.Mkdir(filepath.Join(pwd, ArchiveLogPath), 0666); err != nil {
			return nil, err
		}
	}

	return &FTP{
		pwd:           pwd,
		remoteAddr:    remoteAddr,
		username:      username,
		password:      password,
		filePath:      filePath,
		fileMaxSize:   fileMaxSize,
		isSftp:        isSftp,
		logFilePrefix: logFilePrefix,
		lock:          sync.RWMutex{},
	}, nil
}

func (f *FTP) Name() string {
	return f.name
}

func (f *FTP) SetPoint(point *checkpoint.Checkpoint) {
	f.point = point
}

func (f *FTP) HandleEvents(events []eventlog.Record) error {
	archiveFilePath := filepath.Join(f.pwd, ArchiveLog)
	archiveFile, err := os.OpenFile(archiveFilePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		logrus.Errorln("open backup log file err: ", err.Error())
		return err
	}
	defer archiveFile.Close()

	for _, record := range events {
		systemTime := record.Timestamp()
		_, err = archiveFile.WriteString(record.String())
		_, _ = archiveFile.WriteString("\n")
		if err != nil {
			logrus.Errorln("backup log file write err: ", err.Error())
			return err
		}
		// 成功后更新最新一条的日志时间
		if f.state.Timestamp.UnixNano() < systemTime.UnixNano() {
			f.state.Timestamp = systemTime
		}
	}

	f.point.PersistState(f.state)

	info, err := archiveFile.Stat()
	if err != nil {
		logrus.Errorln("backup log file stat err: ", err.Error())
		return err
	}

	// 当文件大于指定值后将文件转为备份文件中
	if float64(info.Size())/1024/1024 > float64(f.fileMaxSize) {
		// 先释放文件
		_ = archiveFile.Close()
		// 归档文件
		if err := zipArchiveFile(f.pwd, archiveFilePath, f.logFilePrefix); err != nil {
			return err
		}
	}

	// 每次检查一下备份日志文件里面有没有需要上传的文件
	go f.checkArchiveLog()

	logrus.Infof("finish to save eventlog log to %s, total eventlog log is %d, last eventlog log time is %s\n", ArchiveLog, len(events), f.state.Timestamp)
	events = nil
	return nil

}

func (f *FTP) Shutdown() {

}

func (f *FTP) checkArchiveLog() error {
	if !f.lock.TryLock() {
		logrus.Warnln("check backup log not finished,skip")
		return nil
	}
	defer f.lock.Unlock()

	// 读取所有归档文件
	files, err := ioutil.ReadDir(filepath.Join(f.pwd, ArchiveLogPath))
	if err != nil {
		logrus.Errorln("readLastLogTime back log dir err :", err.Error())
		return err
	}

	if len(files) == 0 {
		return nil
	}

	// 最多保留10个文件
	if len(files) > 10 {
		sort.Slice(files, func(i, j int) bool {
			return files[i].ModTime().Before(files[j].ModTime())
		})
		removeFiles := files[:len(files)-10]
		files = files[len(files)-10:]
		for _, file := range removeFiles {
			archiveFile := filepath.Join(f.pwd, ArchiveLogPath, file.Name())
			_ = os.Remove(archiveFile)
		}
	}

	if f.isSftp {
		config := &ssh.ClientConfig{
			User: f.username,
			Auth: []ssh.AuthMethod{
				ssh.Password(f.password),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
		conn, err := ssh.Dial("tcp", f.remoteAddr, config)
		if err != nil {
			logrus.Errorln("sftp dial err: ", err.Error())
			return err
		}
		client, err := sftp.NewClient(conn)
		if err != nil {
			logrus.Errorln("sftp new client err: ", err.Error())
			return err
		}

		defer client.Close()

		for _, file := range files {
			if file.IsDir() {
				continue
			}
			// 读取归档文件信息
			archiveFile := filepath.Join(f.pwd, ArchiveLogPath, file.Name())
			data, err := os.ReadFile(archiveFile)
			if err != nil {
				logrus.Errorf("open %s err: %s\n", archiveFile, err.Error())
				return err
			}
			// 在sftp对应目录创建对应文件
			sftpFilePath := pathJoin(f.filePath, file.Name())
			sftFile, err := client.Create(sftpFilePath)
			if err != nil {
				logrus.Errorf("sftp create file %s err: %s\n", sftpFilePath, err.Error())
				return err
			}
			// 写入数据
			if _, err = sftFile.Write(data); err != nil {
				logrus.Errorf("sftp upload file %s to %s err: %s\n", archiveFile, sftpFilePath, err.Error())
				_ = sftFile.Close()
				return err
			}
			logrus.Infof("success to upload %s file to %s\n", archiveFile, sftpFilePath)
			_ = sftFile.Close()
			_ = os.Remove(archiveFile)
		}

	} else {
		client, err := ftp.Dial(f.remoteAddr, ftp.DialWithTimeout(5*time.Second))
		if err != nil {
			logrus.Errorln("ftp dial err: ", err.Error())
			return err
		}
		err = client.Login(f.username, f.password)
		if err != nil {
			logrus.Errorln("ftp login err: ", err.Error())
			return err
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}
			archiveFile := filepath.Join(f.pwd, ArchiveLogPath, file.Name())
			data, err := os.ReadFile(archiveFile)
			if err != nil {
				logrus.Errorf("open %s err: %s\n", archiveFile, err.Error())
				return err
			}
			ftpFilePath := pathJoin(f.filePath, file.Name())
			if err = client.Stor(ftpFilePath, bytes.NewBuffer(data)); err != nil {
				logrus.Errorf("ftp upload %s to %s err: %s\n", archiveFile, ftpFilePath, err.Error())
				return err
			}
			logrus.Infof("success to upload %s file to %s\n", archiveFile, ftpFilePath)
			_ = os.Remove(archiveFile)
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path) //os.Stat获取文件信息
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}

// 根据path的格式来判断拼接方式
func pathJoin(path, filename string) string {
	// window路径正则表达式
	pattern := `^[a-zA-Z]:\\(?:[^\\/:*?"<>|]+\\)*[^\\/:*?"<>|]*$`
	matched, err := regexp.Match(pattern, []byte(path))
	if err != nil || !matched {
		return path + "/" + filename
	}
	return path + "\\" + filename
}

// 将归档日志进行压缩
func zipArchiveFile(pwd, filePath, filePrefix string) error {
	timeFormat := time.Now().Format("20060102150405")
	archiveFileName := fmt.Sprintf("%s.log", timeFormat)
	archiveFile := filepath.Join(pwd, ArchiveLogPath, archiveFileName)
	// 先将文件移动到归档文件夹中
	if err := os.Rename(filePath, archiveFile); err != nil {
		logrus.Errorln("rename backup log file err: ", err.Error())
		return err
	}

	// 创建zip文件
	zipName := ""
	if filePrefix != "" {
		zipName = filePrefix + "_"
	}
	zipName += fmt.Sprintf("%s_1.zip", timeFormat)
	zipFile, err := os.Create(filepath.Join(pwd, ArchiveLogPath, zipName))
	if err != nil {
		logrus.Errorln("create zip file err: ", err.Error())
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// 在zip中创建日志文件
	logFile, err := zipWriter.Create(archiveFileName)
	if err != nil {
		logrus.Errorln("create archive file in zip file err: ", err.Error())
		return err
	}
	// 读取归档文件内容
	data, err := ioutil.ReadFile(archiveFile)
	if err != nil {
		logrus.Errorf("read file %s err: %s\n", archiveFile, err.Error())
		return err
	}
	// 将内容写入zip的.log文件中
	if _, err = logFile.Write(data); err != nil {
		logrus.Errorln("write to zip file err: ", err.Error())
		return err
	}

	// 删除归档文件
	_ = os.Remove(archiveFile)

	return nil
}
