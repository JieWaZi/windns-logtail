package reader

import (
	"bytes"
	"container/list"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"github.com/panjf2000/ants/v2"
	"sync"
	"sync/atomic"
	"windns-logtail/checkpoint"

	"github.com/elastic/beats/winlogbeat/eventlog"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// 转换工具命令
	TracerptExe = "tracerpt.exe"
	// powershell命令
	Powershell = "powershell.exe"
	// dns查询日志的时间格式
	WindowSystemLayout = "2006-01-02T15:04:05.000000000"
)

type ETL struct {
	logPath string
	pwd     string
	maxRead int

	etlLogfiles chan string
	sortList    *list.List
	xmlLogfiles chan *list.Element

	events          chan *Events
	records         chan []eventlog.Record
	stopMarshalXML  chan struct{}
	stopReadXML     chan struct{}
	stopTransferETL chan struct{}

	state        checkpoint.EventLogState
	point        *checkpoint.Checkpoint
	transferPool *ants.Pool
	loadPool     *ants.Pool

	count atomic.Int32
	lock  sync.RWMutex
}

func NewETLWorker(pwd, logPath string, state checkpoint.EventLogState) (*ETL, error) {
	transferPool, err := ants.NewPool(2, ants.WithPreAlloc(true))
	if err != nil {
		return nil, err
	}
	loadPool, err := ants.NewPool(3, ants.WithPreAlloc(true))
	if err != nil {
		return nil, err
	}

	reader := &ETL{
		logPath:     logPath,
		pwd:         pwd,
		etlLogfiles: make(chan string, 20),
		xmlLogfiles: make(chan *list.Element, 20),
		events:      make(chan *Events, 20),
		sortList:    list.New(),

		stopReadXML:     make(chan struct{}, 1),
		stopTransferETL: make(chan struct{}, 1),
		stopMarshalXML:  make(chan struct{}, 1),
		records:         make(chan []eventlog.Record, 500),
		state:           state,
		transferPool:    transferPool,
		loadPool:        loadPool,
		lock:            sync.RWMutex{},
	}

	return reader, nil
}

func (w *ETL) SetCron(cron *cron.Cron, internal int64) error {
	_, err := cron.AddJob(fmt.Sprintf("@every %ds", internal), w)
	return err
}

func (w *ETL) SetMaxRead(maxRead int) {
	w.maxRead = maxRead
}

func (w *ETL) Run() {
	if w.count.Load() > 10 {
		logrus.Warnf("logfiles too much, skip")
		return
	}
	w.count.Add(1)

	// 日志文件存放在logs目录下
	logPath := filepath.Join(w.pwd, "logs")
	_, err := os.Stat(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err = os.Mkdir(logPath, 0600); err != nil {
				logrus.Errorln("Mkdir log path err: ", err.Error())
				return
			}
		} else {
			logrus.Errorln("Mkdir log path err: ", err.Error())
			return
		}
	}
	etlLogfile := filepath.Join(logPath, fmt.Sprintf("%d%s", time.Now().Unix(), filepath.Base(w.logPath)))
	// 先将文件转移
	if err := powershell(fmt.Sprintf("Copy-Item %s %s", w.logPath, etlLogfile)); err != nil {
		logrus.Errorf("Copy log file err: \n", err.Error())
		return
	}
	// 先将文件转移
	//if err := copyFile(w.logPath, etlLogfile); err != nil {
	//	logrus.Errorln("Copy log file err: ", err.Error())
	//	return
	//}
	w.etlLogfiles <- etlLogfile
}

func (w *ETL) Init() error {
	logrus.Infof("start read %s", w.logPath)
	if err := w.loadFiles(); err != nil {
		return err
	}

	go w.transferETL()

	go w.unmarshalETL()

	go w.scanXML()

	return nil
}

// 拷贝文件到指定目录
func copyFile(src, dest string) error {
	startTime := time.Now()
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
	logrus.Infof("copy %s cost:%ds", src, int64(time.Now().Sub(startTime).Seconds()))

	return nil
}

func powershell(cmd string) error {
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)
	command := exec.Command(Powershell, cmd)
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		logrus.Errorf("exec %s err: %s", cmd, err.Error())
		return err
	}
	if stderr.String() != "" {
		logrus.Errorf("exec %s err: %s", cmd, stderr.String())
		return errors.New(stderr.String())
	}

	return nil
}

func (w *ETL) GetRecords() chan []eventlog.Record {
	return w.records
}

func (w *ETL) SetPoint(point *checkpoint.Checkpoint) {
	w.point = point
}

func (w *ETL) transferETL() {
	for {
		select {
		case etlLogfile := <-w.etlLogfiles: // 在对文件进行转换
			xmlFile := strings.Replace(etlLogfile, ".etl", ".xml", -1)
			element := w.addSortFile(xmlFile)
			_ = w.transferPool.Submit(func(etlLogfile string, element *list.Element) func() {
				return func() {
					// 处理完了需要删除文件
					defer func() {
						os.RemoveAll(etlLogfile)
						w.count.Add(^int32(0))
					}()
					xmlLogfile := element.Value.(string)
					startTime := time.Now()
					// tracerpt.exe Microsoft-Windows-DNSServer%4Analytical2.etl  -of EVTX -o dns_analytical.evtx -y
					if err := powershell(fmt.Sprintf(`%s %s  -lr -of XML -o %s -y`, TracerptExe, etlLogfile, xmlLogfile)); err != nil {
						logrus.Errorf("transfer %s err: %s", etlLogfile, err.Error())
						w.sortList.Remove(element)
						return
					}
					logrus.Infof("finish transfer %s cost:%ds", etlLogfile, int64(time.Now().Sub(startTime).Seconds()))

					w.xmlLogfiles <- element
				}
			}(etlLogfile, element))
		case <-w.stopTransferETL:
			return
		}
	}
}

func (w *ETL) unmarshalETL() {
	for {
		select {
		case e := <-w.xmlLogfiles:
			_ = w.loadPool.Submit(func(e *list.Element) func() {
				return func() {
					xmlLogfile := e.Value.(string)
					events := new(Events)
					events.ID = xmlLogfile
					startTime := time.Now()
					data, err := os.ReadFile(xmlLogfile)
					if err != nil {
						w.removeSortFile(e)
						w.events <- events
						logrus.Errorf("read %s xml err: %s", xmlLogfile, err.Error())
						return
					}

					if err := xml.Unmarshal(data, &events); err != nil {
						w.removeSortFile(e)
						w.events <- events
						logrus.Errorf("unmarshal %s xml err: %s", xmlLogfile, err.Error())
						return
					}
					data = nil

					w.wait(e)
					w.events <- events
					logrus.Infof("finish read %s, cost:%ds", xmlLogfile, int64(time.Now().Sub(startTime).Seconds()))
				}
			}(e))

		case <-w.stopReadXML:
			break

		}

	}
}

func (w *ETL) addSortFile(file string) *list.Element {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.sortList.PushBack(file)
}

func (w *ETL) removeSortFile(e *list.Element) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.sortList.Remove(e)
}

func (w *ETL) isFirstFile(e *list.Element) bool {
	w.lock.RLock()
	f := w.sortList.Front()
	w.lock.RUnlock()
	return f.Value.(string) == e.Value.(string)
}

func (w *ETL) wait(e *list.Element) {
	// 保持日志顺序不变进入channel
	sleepTime := 0
	for {
		if w.isFirstFile(e) {
			break
		}
		// 5s输出一次日志
		if sleepTime%5 == 0 {
			logrus.Infof("current %s, wait %s", e.Value.(string), w.sortList.Front().Value.(string))
		}
		sleepTime++
		time.Sleep(1 * time.Second)
	}
	w.removeSortFile(e)
}

func (w *ETL) scanXML() {
	var deltaEvents []eventlog.Record

	for {
		select {
		case events := <-w.events:
			if events == nil {
				continue
			}
			for _, event := range events.EventLogs {
				// 筛选出大于上次更新时的记录
				if w.state.Timestamp.UnixNano() > event.TimeCreated.SystemTime.UnixNano() {
					continue
				} else if w.state.Timestamp.UnixNano() == event.TimeCreated.SystemTime.UnixNano() {
					hash := md5.Sum([]byte(fmt.Sprintf("%+v", event)))
					hashStr := hex.EncodeToString(hash[:])
					if w.state.MD5 == hashStr {
						continue
					}
				}
				deltaEvents = append(deltaEvents, eventlog.Record{Event: event.Event})
			}

			// 处理完了需要删除文件
			if err := os.RemoveAll(events.ID); err != nil {
				logrus.Errorf("rm %s err: %s", events.ID, err.Error())
			}
			logrus.Infof("%s total event is %d, add %d deltaEvents to channel", events.ID, len(events.EventLogs), len(deltaEvents))
			events = nil

			if len(deltaEvents) == 0 {
				continue
			}

			// 循环读取并处理数组
			startIndex := 0
			for startIndex < len(deltaEvents) {
				records, newIndex := readChunk(deltaEvents, w.maxRead, startIndex)
				hash := md5.Sum([]byte(fmt.Sprintf("%+v", records[len(records)-1])))
				hashStr := hex.EncodeToString(hash[:])
				w.state = checkpoint.EventLogState{
					Name:      w.logPath,
					Timestamp: records[len(records)-1].TimeCreated.SystemTime,
					MD5:       hashStr,
				}
				w.point.PersistState(w.state)
				w.records <- records
				// 更新起始索引
				startIndex = newIndex
			}
			deltaEvents = nil
		case <-w.stopReadXML:
			return
		}
	}

}

func (w *ETL) Shutdown() {
	w.transferPool.Release()
	w.loadPool.Release()
	w.stopReadXML <- struct{}{}
	w.stopTransferETL <- struct{}{}
	w.stopMarshalXML <- struct{}{}

	close(w.etlLogfiles)
	close(w.events)
	close(w.xmlLogfiles)
	close(w.stopReadXML)
	close(w.stopTransferETL)
	close(w.stopMarshalXML)
	close(w.records)
}

func (w *ETL) loadFiles() error {
	files, err := listLogfile(filepath.Join(w.pwd, "logs"))
	if err != nil {
		logrus.Errorln(err.Error())
		return err
	}

	sort.Slice(files, func(i, j int) bool {
		return i < j
	})

	for _, file := range files {
		if strings.Contains(file, ".etl") {
			w.etlLogfiles <- file
		}
		if strings.Contains(file, ".xml") {
			w.xmlLogfiles <- w.addSortFile(file)
		}
	}
	return err
}

// 获取日志文件
func listLogfile(dirPath string) ([]string, error) {
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

func readChunk(arr []eventlog.Record, chunkSize int, startIndex int) ([]eventlog.Record, int) {
	endIndex := startIndex + chunkSize
	if endIndex > len(arr) {
		endIndex = len(arr)
	}
	return arr[startIndex:endIndex], endIndex
}
