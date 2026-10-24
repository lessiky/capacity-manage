package testutil

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/tradingday"

	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// harnessSeq 保证同一时钟节拍内创建的内存库名不碰撞（cache=shared 下同名=同库, 会重复 seed 触发 UNIQUE 冲突）
var harnessSeq uint64

func nextHarnessSeq() uint64 {
	return atomic.AddUint64(&harnessSeq, 1)
}

// gormCfg 与生产一致: 单数表名（persistence.Open 的 SingularTable 策略）
func gormCfg() *gorm.Config {
	return &gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}}
}

// CalHarness is a lightweight in-memory test harness for the trading-day
// calendar: unique in-memory sqlite DB plus an in-process fake Redis server
// (GET returns nil so cache misses fall back to the DB, SET/DEL succeed).
type CalHarness struct {
	DB  *gorm.DB
	RDB *redis.Client
}

func NewCalHarness(t *testing.T) *CalHarness {
	t.Helper()
	name := fmt.Sprintf("caldbsql%d-%d", time.Now().UnixNano(), nextHarnessSeq())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), gormCfg())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&persistence.TradingDayCalendar{}); err != nil {
		t.Fatalf("migrate calendar: %v", err)
	}
	rdb := startFakeRedis(t)
	return &CalHarness{DB: db, RDB: rdb}
}

func (h *CalHarness) Calendar() *tradingday.Calendar {
	return tradingday.NewCalendar(h.DB, h.RDB, nil)
}

// DBHarness is a generic in-memory harness: shared sqlite + fake redis.
// Call Migrate for the models the test needs.
type DBHarness struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// NewDBHarness opens a fresh in-memory sqlite DB and in-process fake redis.
func NewDBHarness(t *testing.T) *DBHarness {
	t.Helper()
	name := fmt.Sprintf("dbsql%d-%d", time.Now().UnixNano(), nextHarnessSeq())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), gormCfg())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return &DBHarness{DB: db, RDB: startFakeRedis(t)}
}

// Migrate auto-migrates the given GORM models.
func (h *DBHarness) Migrate(models ...any) {
	if err := h.DB.AutoMigrate(models...); err != nil {
		panic(err)
	}
}

// SeedOctober2026 seeds SH calendar rows for the whole month of 2026-10:
// all weekdays are trading days except the 13th (holiday).
func (h *CalHarness) SeedOctober2026() {
	for day := 1; day <= 31; day++ {
		d := time.Date(2026, 10, day, 0, 0, 0, 0, time.Local)
		wd := d.Weekday()
		dayType := "trading"
		switch {
		case wd == time.Saturday || wd == time.Sunday:
			dayType = "weekend"
		case day == 13:
			dayType = "holiday"
		}
		row := persistence.TradingDayCalendar{
			Market:    "SH",
			CalDate:   d,
			IsTrading: dayType == "trading",
			DayType:   dayType,
			Source:    "seed",
		}
		if err := h.DB.Create(&row).Error; err != nil {
			panic(err)
		}
	}
}

// startFakeRedis boots a minimal in-process RESP server on an ephemeral
// loopback port and returns a client pointing at it.
func startFakeRedis(t *testing.T) *redis.Client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake redis listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeRedis(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return redis.NewClient(&redis.Options{Addr: ln.Addr().String()})
}

// serveFakeRedis implements just enough RESP for the calendar cache:
// GET -> nil, SET -> OK, DEL -> 1, PING -> PONG, anything else -> error.
func serveFakeRedis(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if !strings.HasPrefix(line, "*") {
			return
		}
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return
		}
		args := make([]string, 0, n)
		for i := 0; i < n; i++ {
			hdr, err := r.ReadString('\n')
			if err != nil {
				return
			}
			hdr = strings.TrimSuffix(strings.TrimSuffix(hdr, "\n"), "\r")
			if !strings.HasPrefix(hdr, "$") {
				return
			}
			l, err := strconv.Atoi(hdr[1:])
			if err != nil {
				return
			}
			buf := make([]byte, l+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			args = append(args, string(buf[:l]))
		}
		if len(args) == 0 {
			return
		}
		var resp string
		switch strings.ToUpper(args[0]) {
		case "PING":
			resp = "+PONG\r\n"
		case "GET":
			resp = ":-\r\n"
		case "SET":
			resp = "+OK\r\n"
		case "DEL":
			resp = ":1\r\n"
		default:
			resp = "-ERR unknown command\r\n"
		}
		if _, err := w.WriteString(resp); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
}
