package logx

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

func InitLogger() {
	log.Default().SetOutput(log.Writer())
}

func output(level level, msg string) {
	_, file, line, _ := runtime.Caller(2)
	var b strings.Builder
	b.WriteByte(' ')
	b.WriteString(level.String())
	b.WriteByte(' ')
	b.WriteString(filepath.Base(file))
	b.WriteByte(':')
	b.WriteString(fmt.Sprint(line))
	b.WriteByte(' ')
	b.WriteString(msg)
	log.Println(strings.TrimSpace(b.String()))
}

type LoggerLevel struct{ level level }

func (l LoggerLevel) Print(v ...any)            { output(l.level, fmt.Sprint(v...)) }
func (l LoggerLevel) Println(v ...any)          { output(l.level, fmt.Sprint(v...)) }
func (l LoggerLevel) Printf(f string, v ...any) { output(l.level, fmt.Sprintf(f, v...)) }

type RecoverLevel struct{ level level }

func (r RecoverLevel) Recover(v ...any) {
	if errr := recover(); errr != nil {
		msg := fmt.Sprintf("panic[%s] recovered: %v\n%s", v, r, string(debug.Stack()))
		output(r.level, msg)
	}
}

type FatalLevel struct{ level level }

func (f FatalLevel) Fatal(v ...any) {
	output(f.level, fmt.Sprint(v...))
	os.Exit(1)
}

func (f FatalLevel) Fatalf(format string, v ...any) {
	output(f.level, fmt.Sprintf(format, v...))
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
