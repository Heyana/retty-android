//go:build retty_cli && (darwin || linux)

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"retty/internal/atomicfile"
	"retty/internal/rex"
	"retty/internal/selfupdate"
)

type gatewayUpdates struct {
	mu      sync.Mutex
	engine  *selfupdate.Engine
	status  selfupdate.Status
	restart func()
	check   func(context.Context) (selfupdate.Status, error)
	install func(context.Context, string, func(string)) (string, error)
}

func newGatewayUpdates(restart func()) *gatewayUpdates {
	u := &gatewayUpdates{restart: restart, status: selfupdate.Status{Current: version, Phase: "idle"}}
	exe, err := os.Executable()
	if err == nil {
		u.engine, err = selfupdate.New(version, exe)
	}
	if err != nil {
		u.status.Error = err.Error()
		return u
	}
	u.check, u.install = u.engine.Check, u.engine.Install
	var saved selfupdate.Status
	if data, err := os.ReadFile(filepath.Join(rex.Dir(), "update.json")); err == nil && json.Unmarshal(data, &saved) == nil {
		if saved.Latest == version {
			u.status = saved
			u.status.Current = version
			u.status.Available = false
			u.status.Phase = "updated"
			u.status.Error = ""
			u.saveLocked()
		} else if saved.Busy() {
			u.status = saved
			u.status.Current = version
			u.status.Phase = "failed"
			u.status.Error = "previous update was interrupted; check updates again"
			u.saveLocked()
		}
	}
	return u
}

func (u *gatewayUpdates) saveLocked() error {
	u.status.Updated = time.Now()
	data, _ := json.Marshal(u.status)
	return atomicfile.Write(filepath.Join(rex.Dir(), "update.json"), data)
}

func (u *gatewayUpdates) request(ctx context.Context, req selfupdate.Request) (selfupdate.Status, error) {
	if req.Action == "status" {
		u.mu.Lock()
		defer u.mu.Unlock()
		return u.status, nil
	}
	if u.check == nil {
		return selfupdate.Status{}, errors.New("this CLI installation cannot update itself")
	}
	if req.Action == "check" {
		u.mu.Lock()
		current := u.status
		u.mu.Unlock()
		if current.Busy() {
			return current, nil
		}
		status, err := u.check(ctx)
		return status, err
	}
	if req.Action != "apply" {
		return selfupdate.Status{}, errors.New("invalid update action")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.status.Busy() {
		return u.status, errors.New("an update is already running")
	}
	if req.Version == "" {
		return u.status, errors.New("check updates before selecting a version")
	}
	// This lock serializes acceptance; the filesystem lock serializes installation.
	status, err := u.check(ctx)
	if err != nil {
		return u.status, err
	}
	if !status.Available || status.Latest != req.Version {
		return status, errors.New("update version changed or is not newer; check again")
	}
	u.status = status
	u.status.Phase = "downloading"
	if err := u.saveLocked(); err != nil {
		u.status.Phase = "failed"
		u.status.Error = err.Error()
		return u.status, err
	}
	go func() {
		// An accepted update continues independently of the requesting tunnel.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := u.install(ctx, req.Version, func(phase string) { u.mu.Lock(); u.status.Phase = phase; u.saveLocked(); u.mu.Unlock() })
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
			u.restart()
		}
	}()
	return u.status, nil
}

func updateGatewayRequest(ctx context.Context, req selfupdate.Request) (selfupdate.Status, error) {
	data, _ := json.Marshal(req)
	state, err := gatewayRequest(ctx, "update:"+string(data))
	if err != nil {
		return selfupdate.Status{}, err
	}
	if state.Update == nil {
		return selfupdate.Status{}, errors.New("running gateway does not support update; install a newer CLI first")
	}
	if req.Action == "status" && !state.Ready {
		return selfupdate.Status{}, errors.New("updated gateway is still starting")
	}
	if state.Update.Error != "" && state.Update.Phase != "failed" {
		return *state.Update, errors.New(state.Update.Error)
	}
	return *state.Update, nil
}

func updateCommand(ctx context.Context, out io.Writer, checkOnly bool) error {
	req := selfupdate.Request{Action: "check"}
	_, gatewayErr := gatewayRequest(ctx, "status")
	var engine *selfupdate.Engine
	var status selfupdate.Status
	var err error
	if gatewayErr == nil {
		status, err = updateGatewayRequest(ctx, req)
	} else {
		if !errors.Is(gatewayErr, os.ErrNotExist) && !errors.Is(gatewayErr, unix.ECONNREFUSED) {
			return gatewayErr
		}
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		engine, err = selfupdate.New(version, exe)
		if err == nil {
			status, err = engine.Check(ctx)
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Current: %s · latest: %s\n", status.Current, status.Latest)
	if checkOnly || !status.Available {
		if !status.Available {
			fmt.Fprintln(out, "No newer stable CLI release is available.")
		}
		return nil
	}
	if engine != nil {
		_, err = engine.Install(ctx, status.Latest, func(phase string) { fmt.Fprintln(out, phase) })
		if err == nil {
			fmt.Fprintln(out, "CLI updated; gateway was stopped and sessions were preserved.")
		}
		return err
	}
	_, err = updateGatewayRequest(ctx, selfupdate.Request{Action: "apply", Version: status.Latest})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Updating gateway; existing sessions continue. The update survives this connection closing.")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		s, err := updateGatewayRequest(ctx, selfupdate.Request{Action: "status"})
		if err != nil {
			continue
		} // The gateway briefly disappears while execing.
		if s.Phase == "failed" {
			return errors.New(s.Error)
		}
		if s.Phase == "updated" && s.Current == status.Latest {
			fmt.Fprintln(out, "Gateway updated; session server and tasks were preserved.")
			return nil
		}
	}
}
