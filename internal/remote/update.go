package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"retty/internal/rex"
	"retty/internal/selfupdate"
)

var ErrUpdateUnsupported = errors.New("远端尚不支持远程更新，请先安装一次支持该入口的桌面版或 CLI")

func (b *Bridge) serveUpdate(conn net.Conn, line []byte) {
	conn.SetDeadline(time.Now().Add(25 * time.Second))
	b.mu.Lock()
	if b.updateGate == nil {
		b.updateGate = make(chan struct{}, 2)
	}
	gate := b.updateGate
	b.mu.Unlock()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	default:
		_ = json.NewEncoder(conn).Encode(rex.Response{Error: "更新请求繁忙，请稍后重试"})
		return
	}
	res := rex.Response{}
	var req selfupdate.Request
	if len(line) > 4096 || json.Unmarshal(line, &req) != nil || req.Action != "check" && req.Action != "apply" && req.Action != "status" {
		res.Error = "更新请求无效"
	} else if b.onUpdate == nil {
		res.Error = ErrUpdateUnsupported.Error()
	} else {
		ctx, cancel := context.WithTimeout(b.imageContext, 25*time.Second)
		defer cancel()
		conn.SetDeadline(time.Now().Add(25 * time.Second))
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		status, err := b.onUpdate(ctx, req)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Data, _ = json.Marshal(status)
		}
	}
	_ = json.NewEncoder(conn).Encode(res)
}

// Update uses an independent authenticated stream, never terminal input.
func Update(ctx context.Context, client *rex.Client, req selfupdate.Request) (selfupdate.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := client.DialExtension(ctx)
	if err != nil {
		return selfupdate.Status{}, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	req.Op = "self-update"
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return selfupdate.Status{}, err
	}
	var res rex.Response
	if err = json.NewDecoder(io.LimitReader(conn, 16384)).Decode(&res); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() && !time.Now().Before(deadline) {
			err = context.DeadlineExceeded
		}
		return selfupdate.Status{}, err
	}
	if res.Error != "" {
		if strings.Contains(res.Error, "unknown op") || res.Error == ErrUpdateUnsupported.Error() {
			return selfupdate.Status{}, ErrUpdateUnsupported
		}
		return selfupdate.Status{}, errors.New(res.Error)
	}
	var status selfupdate.Status
	err = json.Unmarshal(res.Data, &status)
	return status, err
}
