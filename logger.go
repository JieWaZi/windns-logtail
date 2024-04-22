package main

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
	logger := logrus.New()

	//打印文件和行号
	logger.SetReportCaller(true)
	// use json format
	// TextFormatter, JSONFormatter
	if isJson {
		logger.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logger.SetFormatter(&LogFormatter{})
	}

	if toStdout {
		logger.SetOutput(os.Stdout)
	} else {
		l := &lumberjack.Logger{
			Filename:   path.Join(filepath),
			MaxSize:    50,
			MaxBackups: 2,
			Compress:   true,
		}
		logger.SetOutput(l)
	}

	//set log level
	switch strings.ToLower(level) {
	case "debug":
		logger.SetLevel(logrus.DebugLevel)
	case "info":
		logger.SetLevel(logrus.InfoLevel)
	case "warn":
		logger.SetLevel(logrus.WarnLevel)
	case "error":
		logger.SetLevel(logrus.ErrorLevel)
	case "fatal":
		logger.SetLevel(logrus.FatalLevel)
	case "panic":
		logger.SetLevel(logrus.PanicLevel)
	default:
		fmt.Println("LogLevel error, exit.")
		os.Exit(-1)
	}
}
