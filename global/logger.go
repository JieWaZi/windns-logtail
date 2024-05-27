package global

import (
	"bytes"
	"fmt"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"path"
	"strings"
)

type LogFormatter struct{}

func (m *LogFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b *bytes.Buffer
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}

	timestamp := entry.Time.Format("2006-01-02 15:04:05")
	newLog := fmt.Sprintf("%s %s %s\n", timestamp, entry.Level, entry.Message)

	b.WriteString(newLog)
	return b.Bytes(), nil
}

func InitLogger(filepath, level string, toStdout, isJson bool) {
	//打印文件和行号
	logrus.SetReportCaller(true)
	// use json format
	// TextFormatter, JSONFormatter
	if isJson {
		logrus.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logrus.SetFormatter(&LogFormatter{})
	}

	if toStdout {
		logrus.SetOutput(os.Stdout)
	} else {
		l := &lumberjack.Logger{
			Filename:   path.Join(filepath),
			MaxSize:    50,
			MaxBackups: 2,
			Compress:   true,
		}
		logrus.SetOutput(l)
	}

	//set log level
	switch strings.ToLower(level) {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "info":
		logrus.SetLevel(logrus.InfoLevel)
	case "warn":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	case "fatal":
		logrus.SetLevel(logrus.FatalLevel)
	case "panic":
		logrus.SetLevel(logrus.PanicLevel)
	default:
		fmt.Println("LogLevel error, exit.")
		os.Exit(-1)
	}
}
