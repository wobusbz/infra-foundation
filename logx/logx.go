package logx

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

var _BufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func InitLogger() {
	log.SetFlags(log.LstdFlags)
}

func formatPrefix(b *bytes.Buffer, level level, file string, line int) {
	b.WriteString(level.String())
	b.WriteByte(' ')

	shortFile := file
	if idx := strings.LastIndexByte(file, '/'); idx >= 0 {
		shortFile = file[idx+1:]
	} else if idx := strings.LastIndexByte(file, '\\'); idx >= 0 {
		shortFile = file[idx+1:]
	}
	b.WriteString(shortFile)
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(line))
	b.WriteByte(' ')
}

func output(level level, v ...any) {
	b := _BufferPool.Get().(*bytes.Buffer)
	b.Reset()

	_, file, line, ok := runtime.Caller(2)
	if !ok {
		file = "???"
		line = 0
	}
	formatPrefix(b, level, file, line)

	fmt.Fprint(b, v...)
	log.Output(2, b.String())
	_BufferPool.Put(b)
}

func outputf(level level, format string, v ...any) {
	b := _BufferPool.Get().(*bytes.Buffer)
	b.Reset()

	_, file, line, ok := runtime.Caller(2)
	if !ok {
		file = "???"
		line = 0
	}
	formatPrefix(b, level, file, line)

	fmt.Fprintf(b, format, v...)
	log.Output(2, b.String())
	_BufferPool.Put(b)
}

type LoggerLevel struct{ level level }

func (l LoggerLevel) Print(v ...any)            { output(l.level, v...) }
func (l LoggerLevel) Println(v ...any)          { output(l.level, v...) }
func (l LoggerLevel) Printf(f string, v ...any) { outputf(l.level, f, v...) }

type RecoverLevel struct{ level level }

func (r RecoverLevel) Recover(v ...any) {
	if errr := recover(); errr != nil {
		msg := fmt.Sprintf("panic[%s] recovered: %v\n%s", fmt.Sprint(v...), errr, string(debug.Stack()))
		output(r.level, msg)
	}
}

type FatalLevel struct{ level level }

func (f FatalLevel) Fatal(v ...any) {
	output(f.level, v...)
	os.Exit(1)
}

func (f FatalLevel) Fatalf(format string, v ...any) {
	outputf(f.level, format, v...)
	os.Exit(1)
}

type level int8

func (l level) String() string {
	switch l {
	case dbgLevel:
		return "[DBG]"
	case infLevel:
		return "[INF]"
	case warLevel:
		return "[WAR]"
	case errLevel:
		return "[ERR]"
	case fatLevel:
		return "[FAT]"
	case recLevel:
		return "[REC]"
	}
	return "[UNKNOWN]"
}

const (
	dbgLevel = iota
	infLevel
	warLevel
	errLevel
	fatLevel
	recLevel
)

var (
	Dbg = LoggerLevel{level: dbgLevel}
	Inf = LoggerLevel{level: infLevel}
	War = LoggerLevel{level: warLevel}
	Err = LoggerLevel{level: errLevel}
	Rec = RecoverLevel{level: recLevel}
	Fat = FatalLevel{level: fatLevel}
)
