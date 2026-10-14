package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"retty/internal/atomicfile"
	"retty/internal/rex"
	"retty/internal/selfupdate"
)

// The bridge controls MyGo's existing updater. It supplies only the selected
// version; MyGo owns the feed, signing key, downloads, installation and quit.
type desktopUpdateService struct {
	mu       sync.Mutex
	dir      string
	status   selfupdate.Status
	check    func(context.Context) (selfupdate.Status, error)
	install  func(context.Context, string) error
	relaunch func()
}

func newDesktopUpdateService(relaunch func()) *desktopUpdateService {
	u := &desktopUpdateService{dir: rex.Dir(), relaunch: relaunch, install: updater.InstallUpdate, status: selfupdate.Status{Kind: "desktop", Current: mygo.App.Version(), Phase: "idle"}}
	u.check = func(ctx context.Context) (selfupdate.Status, error) {
		status := selfupdate.Status{Kind: "desktop", Current: mygo.App.Version(), Latest: mygo.App.Version(), Phase: "checked"}
		update, err := mygo.Updater.Check(ctx)
		if errors.Is(err, mygo.ErrUpdatesDisabled) {
			return status, errors.New("远端当前安装不支持更新，请使用已启用原生更新器的正式应用")
		}
		if err != nil {
			return status, err
		}
		if update != nil {
			status.Latest = update.Version
			status.Available = true
		}
		return status, nil
	}
	var previous selfupdate.Status
	if data, err := os.ReadFile(filepath.Join(u.dir, "desktop-update.json")); err == nil && json.Unmarshal(data, &previous) == nil {
		if previous.Latest == u.status.Current {
			u.status.Latest = previous.Latest
			u.status.Phase = "updated"
			_ = u.saveLocked()
		} else if previous.Busy() {
			u.status.Latest = previous.Latest
			u.status.Phase = "failed"
			u.status.Error = "上次更新未完成，请重新检查版本。"
			_ = u.saveLocked()
		}
	}
	return u
}

func (u *desktopUpdateService) saveLocked() error {
	u.status.Updated = time.Now()
	data, _ := json.Marshal(u.status)
	return atomicfile.Write(filepath.Join(u.dir, "desktop-update.json"), data)
}

func (u *desktopUpdateService) request(ctx context.Context, req selfupdate.Request) (selfupdate.Status, error) {
	u.mu.Lock()
	if req.Action == "status" || req.Action == "check" && u.status.Busy() {
		status := u.status
		u.mu.Unlock()
		return status, nil
	}
	if req.Action == "check" {
		u.mu.Unlock()
		return u.check(ctx)
	}
	defer u.mu.Unlock()
	if req.Action != "apply" || req.Version == "" {
		return u.status, errors.New("请先检查并选择更新版本")
	}
	if u.status.Busy() {
		return u.status, errors.New("远端更新正在进行，请等待完成")
	}
	status, err := u.check(ctx)
	if err != nil {
		return u.status, err
	}
	if !status.Available || status.Latest != req.Version {
		return status, errors.New("远端版本已变化，请重新检查")
	}
	u.status = status
	u.status.Phase = "downloading"
	if err = u.saveLocked(); err != nil {
		u.status.Phase = "failed"
		u.status.Error = err.Error()
		return u.status, err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := u.install(ctx, req.Version)
		u.mu.Lock()
		if err != nil {
			u.status.Phase = "failed"
			u.status.Error = err.Error()
		} else {
			u.status.Phase = "restarting"
		}
		if saveErr := u.saveLocked(); err == nil && saveErr != nil {
			err = saveErr
			u.status.Phase = "failed"
			u.status.Error = saveErr.Error()
		}
		u.mu.Unlock()
		if err == nil {
			u.relaunch()
		}
	}()
	return u.status, nil
}
