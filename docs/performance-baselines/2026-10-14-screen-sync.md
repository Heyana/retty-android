# 2026-10-14 画面同步限速对照

当前工作区的独立协议测试，Apple M3 / macOS，Ghostty 网格 80×24，传输限速为 1,000,000 bit/s（125,000 byte/s）。不连接真实会话服务、不操作真实任务；通过 `net.Pipe` 与有界传输限速器比较同一份终端状态。数据字节包括帧头，不含 WireGuard / TCP / 中继封装。

| 场景 | 原字节流 | 新画面同步 |
| --- | ---: | ---: |
| 2000 行中文历史后的首屏传输字节 | 42,092 B | 592 B |
| 首屏完整读取，中位数（3 次） | 353.54 ms | 5.71 ms |
| 持续 1 秒、1 MiB/s 的重复重绘传输字节 | 1,048,737 B | 664 B |
| 从开始输出到接收最终画面（含 1 秒产生输出） | 8.626 s | 1.002 s |

首屏传输字节在这个 fixture 中减少约 98.6%，完整读取约快 62 倍。重绘 fixture 连续覆盖同一屏幕，带递增计数、中文和 emoji，因此差分很小；这项流量下降不能推广成普通日志或所有 Agent 输出的固定比例。输出场景各测一次，不能计算可靠 P95。以上不是 GPU 上屏时间，也不包含真实无线 RTT、丢包、网络切换、DERP 限速或移动设备调度。CPU、内存、电量收益尚未测量。

功能验证另覆盖 1000 次未确认更新合并成最新状态、静默会话心跳、旧端降级、短写、拆分的一次性事件、终端查询回复、Unicode / OSC 8 / 光标 / 模式 / 尺寸变化、退出后的最终画面及 DEC 2026 半帧隔离。本地真实 PTY 的持续输出 fixture 可通过 Ctrl+C 中断；其时间不代表弱网下的输入延迟。

复现：

```sh
GOWORK=off RETTY_SCREEN_SYNC_MEASURE=1 \
  go test -tags mygo_noinspector \
  -ldflags '-X github.com/egoist/mygo.production=1' \
  ./internal/rex -run '^TestScreenSyncBandwidthComparison$' -count=1 -v
```

设置 `RETTY_SCREEN_SYNC_REPORT` 可将原始 JSON 保存到指定文件。该测试默认跳过限速测量，普通协议 / UI 测试仍正常运行。原始报告与代码摘要保存在本地 `build/performance/screen-sync-20261014/report.json` 和 `source.json`。部署时需新 daemon 支持协商能力；已有服务和任务保持运行，不能用 GUI 更新替代服务维护。
