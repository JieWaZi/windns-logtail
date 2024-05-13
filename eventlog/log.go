package eventlog

import "time"

type Record interface {
	String() string
	Timestamp() time.Time
}
