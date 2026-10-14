package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

const staleScreenAfter = 5 * time.Second

func screenFreshness(c *ui.Context, stream *sessionViewStream) string {
	if stream == nil {
		return ""
	}
	enabled, confirmed, _ := stream.ScreenStatus()
	if !enabled || confirmed.IsZero() {
		return ""
	}
	age := c.Now().Sub(confirmed)
	if age < staleScreenAfter {
		c.After(staleScreenAfter - age)
		return ""
	}
	c.After(time.Second)
	return fmt.Sprintf("画面更新延迟 · %d 秒未确认", int(age.Seconds()))
}
