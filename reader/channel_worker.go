package reader

import (
	"fmt"
	cp1 "github.com/elastic/beats/winlogbeat/checkpoint"
	"github.com/elastic/beats/winlogbeat/eventlog"
	"github.com/elastic/beats/winlogbeat/sys"
	"github.com/elastic/beats/winlogbeat/sys/wineventlog"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
	"io"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"windns-logtail/checkpoint"
)

const (
	RenderBufferSize = 1 << 14
	ReaderAPI        = "ReaderAPI"
)

type Channel struct {
	query       *wineventlog.Query // 查询条件
	channelName string             // 通道或者文件名称
	file        bool

	internal int64 // 读取间隔
	maxRead  int   // 单次最大条数

	start   bool                   // 是否启动
	lock    sync.RWMutex           // 读写锁
	records chan []eventlog.Record // 读取到的事件

	subscription wineventlog.EvtHandle    // 订阅的句柄
	state        checkpoint.EventLogState // 上一次日志信息
	point        *checkpoint.Checkpoint

	render    func(event wineventlog.EvtHandle, out io.Writer) error // 转换XML函数
	renderBuf []byte                                                 // 用于暂存数据的buffer
	outputBuf *sys.ByteBuffer                                        // 接收XML的buffer

	stopIFEmpty bool // 未读到事件则停止
}

type ReaderOptions = func(*Channel)

// WithLevel 指定获取日志级别，不指定level默认所有
func WithLevel(level string) ReaderOptions {
	return func(reader *Channel) {
		reader.query.Level = level
	}
}

// WithEventID 指定事件ID，不指定eventID默认所有，如果指定，格式为区间范围如1-4
func WithEventID(eventID string) ReaderOptions {
	return func(reader *Channel) {
		reader.query.EventID = eventID
	}
}

// WithStop 未获取到事件则退出读取
func WithStop() ReaderOptions {
	return func(reader *Channel) {
		reader.stopIFEmpty = true
	}
}

// NewChannelReader 创建结构体
func NewChannelReader(name string, lastTime int64, options ...ReaderOptions) (*Channel, error) {
	query := &wineventlog.Query{Log: name}
	if lastTime != 0 {
		query.IgnoreOlder = time.Duration(lastTime)
	}
	if filepath.IsAbs(name) {
		name = filepath.Clean(name)
	}
	l := &Channel{
		query:       query,
		channelName: name,
		file:        filepath.IsAbs(name),
		records:     make(chan []eventlog.Record, 500),
		renderBuf:   make([]byte, RenderBufferSize),
		outputBuf:   sys.NewByteBuffer(RenderBufferSize),
	}

	for _, option := range options {
		option(l)
	}

	l.render = func(event wineventlog.EvtHandle, out io.Writer) error {
		return wineventlog.RenderEvent(event, 0, l.renderBuf, nil, out)
	}

	return l, nil
}

// Name 返回通道名称
func (l *Channel) Name() string {
	return l.channelName
}

func (l *Channel) Open(state checkpoint.EventLogState) error {
	var bookmark wineventlog.EvtHandle
	var err error
	if len(state.Bookmark) > 0 {
		bookmark, err = wineventlog.CreateBookmarkFromXML(state.Bookmark)
	} else if state.RecordNumber > 0 {
		bookmark, err = wineventlog.CreateBookmarkFromRecordID(l.channelName, state.RecordNumber)
	}
	if err != nil {
		return err
	}
	defer wineventlog.Close(bookmark)

	if l.file {
		return l.openFile(state, bookmark)
	}

	return l.openChannel(bookmark)
}

func (l *Channel) openChannel(bookmark wineventlog.EvtHandle) error {
	// Using a pull subscription to receive events. See:
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa385771(v=vs.85).aspx#pull
	signalEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(signalEvent)

	var flags wineventlog.EvtSubscribeFlag
	if bookmark > 0 {
		flags = wineventlog.EvtSubscribeStartAfterBookmark
	} else {
		flags = wineventlog.EvtSubscribeStartAtOldestRecord
	}

	filter, err := l.query.Build()
	if err != nil {
		return err
	}
	logrus.Infoln("using subscription query=", filter)
	subscriptionHandle, err := wineventlog.Subscribe(0, signalEvent, "", filter, bookmark, flags)
	if err != nil {
		return err
	}

	l.subscription = subscriptionHandle
	return nil
}

func (l *Channel) openFile(state checkpoint.EventLogState, bookmark wineventlog.EvtHandle) error {
	path := l.channelName

	filter, err := l.query.Build()
	if err != nil {
		return err
	}
	h, err := wineventlog.EvtQuery(0, path, filter, wineventlog.EvtQueryFilePath|wineventlog.EvtQueryReverseDirection)
	if err != nil {
		return errors.Wrapf(err, "failed to get handle to event log file %v", path)
	}

	if bookmark > 0 {
		logrus.Infof("Seeking to bookmark. timestamp=%v bookmark=%v", state.Timestamp, state.Bookmark)

		// This seeks to the last read event and strictly validates that the
		// bookmarked record number exists.
		if err = wineventlog.EvtSeek(h, 0, bookmark, wineventlog.EvtSeekRelativeToBookmark|wineventlog.EvtSeekStrict); err == nil {
			// Then we advance past the last read event to avoid sending that
			// event again. This won't fail if we're at the end of the file.
			err = errors.Wrap(
				wineventlog.EvtSeek(h, 1, bookmark, wineventlog.EvtSeekRelativeToBookmark),
				"failed to seek past bookmarked position")
		} else {
			logrus.Warnf("Failed to seek to bookmarked location in %v (error: %v). "+
				"Recovering by reading the log from the beginning. (Did the file "+
				"change since it was last read?)", path, err)
			err = errors.Wrap(
				wineventlog.EvtSeek(h, 0, 0, wineventlog.EvtSeekRelativeToFirst),
				"failed to seek to beginning of log")
		}

		if err != nil {
			return err
		}
	}

	l.subscription = h
	return nil
}

func (l *Channel) Read() ([]eventlog.Record, error) {
	handles, _, err := l.eventHandles(l.maxRead)
	if err != nil || len(handles) == 0 {
		return nil, err
	}
	defer func() {
		for _, h := range handles {
			wineventlog.Close(h)
		}
	}()
	logrus.Debugf("EventHandles returned %d handles", len(handles))

	var records []eventlog.Record
	for _, h := range handles {
		l.outputBuf.Reset()
		err := l.render(h, l.outputBuf)
		if bufErr, ok := err.(sys.InsufficientBufferError); ok {
			logrus.Debugln("Increasing render buffer size to", bufErr.RequiredSize)
			l.renderBuf = make([]byte, bufErr.RequiredSize)
			l.outputBuf.Reset()
			err = l.render(h, l.outputBuf)
		}
		if err != nil && l.outputBuf.Len() == 0 {
			logrus.Errorln("Dropping event with rendering err:", err)
			continue
		}

		r, _ := l.buildRecordFromXML(l.outputBuf.Bytes(), err)
		r.Offset = cp1.EventLogState{
			Name:         l.channelName,
			RecordNumber: r.RecordID,
			Timestamp:    r.TimeCreated.SystemTime,
		}
		if r.Offset.Bookmark, err = l.createBookmarkFromEvent(h); err != nil {
			logrus.Warnln("Failed creating bookmark: ", err)
		}
		records = append(records, r)
		l.state = checkpoint.EventLogState{
			Name:         l.channelName,
			RecordNumber: r.RecordID,
			Timestamp:    r.TimeCreated.SystemTime,
		}
	}

	logrus.Debugf("Read() is returning %d records", len(records))
	return records, nil
}

func (l *Channel) Close() error {
	logrus.Debugf("Closing handle")
	return wineventlog.Close(l.subscription)
}

func (l *Channel) eventHandles(maxRead int) ([]wineventlog.EvtHandle, int, error) {
	handles, err := wineventlog.EventHandles(l.subscription, maxRead)
	switch err {
	case nil:
		if l.maxRead > maxRead {
			logrus.Errorf("Recovered from RPC_S_INVALID_BOUND error (errno 1734) "+
				"by decreasing batch_read_size to %v", maxRead)
		}
		return handles, maxRead, nil
	case wineventlog.ERROR_NO_MORE_ITEMS:
		logrus.Infoln("no more events")
		if l.stopIFEmpty {
			return nil, maxRead, io.EOF
		}
		return nil, maxRead, nil
	case wineventlog.RPC_S_INVALID_BOUND:
		if err := l.Close(); err != nil {
			return nil, 0, errors.Wrap(err, "failed to recover from RPC_S_INVALID_BOUND")
		}
		if err := l.Open(l.state); err != nil {
			return nil, 0, errors.Wrap(err, "failed to recover from RPC_S_INVALID_BOUND")
		}
		return l.eventHandles(maxRead / 2)
	default:
		logrus.Warnf("EventHandles returned error %v", err)
		return nil, 0, err
	}
}

func (l *Channel) buildRecordFromXML(x []byte, recoveredErr error) (eventlog.Record, error) {
	e, err := sys.UnmarshalEventXML(x)
	if err != nil {
		e.RenderErr = append(e.RenderErr, err.Error())
	}

	err = sys.PopulateAccount(&e.User)
	if err != nil {
		logrus.Errorf("SID %s account lookup failed. %v", e.User.Identifier, err)
	}

	if e.RenderErrorCode != 0 {
		e.RenderErr = append(e.RenderErr, syscall.Errno(e.RenderErrorCode).Error())
	} else if recoveredErr != nil {
		e.RenderErr = append(e.RenderErr, recoveredErr.Error())
	}

	if e.Level == "" {
		// Fallback on LevelRaw if the Level is not set in the RenderingInfo.
		e.Level = wineventlog.EventLevel(e.LevelRaw).String()
	}

	r := eventlog.Record{
		API:   ReaderAPI,
		Event: e,
	}

	if l.file {
		r.File = l.channelName
	}

	return r, nil
}

func (l *Channel) createBookmarkFromEvent(evtHandle wineventlog.EvtHandle) (string, error) {
	bmHandle, err := wineventlog.CreateBookmarkFromEvent(evtHandle)
	if err != nil {
		return "", err
	}
	l.outputBuf.Reset()
	err = wineventlog.RenderBookmarkXML(bmHandle, l.renderBuf, l.outputBuf)
	wineventlog.Close(bmHandle)
	return string(l.outputBuf.Bytes()), err
}

func (l *Channel) SetCron(cron *cron.Cron, internal int64) error {
	l.internal = internal
	_, err := cron.AddJob(fmt.Sprintf("@every 60s"), l)
	return err
}

func (l *Channel) Run() {
	if l.start {
		return
	}
	l.lock.Lock()
	l.start = true
	l.lock.Unlock()

	err := l.Open(l.state)
	if err != nil {
		logrus.Warnf("open %s err: %s", l.Name(), err.Error())
		return
	}
	defer func() {
		logrus.Infof("stop read %s", l.Name())

		if err := l.Close(); err != nil {
			logrus.Errorf("close %s err: %v", l.Name(), err.Error())
			return
		}
	}()

	// 每秒循环读取日志
	for l.start {
		records, err := l.Read()
		switch err {
		case nil:
		case io.EOF:
			l.lock.Lock()
			l.start = false
			l.lock.Unlock()
		default:
			l.lock.Lock()
			l.start = false
			l.lock.Unlock()
			logrus.Warnf("read %s error: %s", l.Name(), err.Error())
			return
		}

		logrus.Infof("%s read %d records", l.Name(), len(records))
		if len(records) == 0 {
			time.Sleep(time.Duration(l.internal))
			continue
		}
		l.point.PersistState(l.state)
		l.records <- records
	}
}

func (l *Channel) Init() error {
	return nil
}

func (l *Channel) SetMaxRead(maxRead int) {
	l.maxRead = maxRead
}
func (l *Channel) GetRecords() chan []eventlog.Record {
	return l.records
}
func (l *Channel) Shutdown() {
	l.start = false
	close(l.records)
}

func (l *Channel) SetPoint(point *checkpoint.Checkpoint) {
	l.point = point
}
