import importlib.util
from pathlib import Path
import socket
import struct
import threading
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("performance_baseline", Path(__file__).with_name("performance-baseline.py"))
perf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(perf)
fixture_spec = importlib.util.spec_from_file_location("perf_fixture", Path(__file__).with_name("perf_fixture.py"))
fixture = importlib.util.module_from_spec(fixture_spec)
fixture_spec.loader.exec_module(fixture)


class MeasurementTests(unittest.TestCase):
    def test_partial_pty_writes_preserve_all_bytes(self):
        delivered = bytearray()
        def partial_write(fd, data):
            self.assertEqual(fd, 1)
            count = min(len(data), 3)
            delivered.extend(data[:count])
            return count
        with mock.patch.object(fixture.os, "write", side_effect=partial_write):
            fixture.write_all(b"colored-output-sequence")
        self.assertEqual(delivered, b"colored-output-sequence")

    def test_delayed_wakeup_preserves_pacing_with_bounded_catchup(self):
        self.assertAlmostEqual(fixture.next_deadline(0, .2, .0625), .0625)
        self.assertAlmostEqual(fixture.next_deadline(0, 10, .0625), 9.8125)

    def test_cpu_uses_cumulative_delta_and_peak_is_distinct_from_settled_memory(self):
        rows = [
            dict(role="gui", pid=12, elapsed_s=t, cpu_ns=cpu, rss_bytes=rss * perf.MIB,
                 footprint_bytes=mem * perf.MIB, idle_wakeups=wakeups)
            for t, cpu, rss, mem, wakeups in [(5, 10e9, 80, 40, 20), (6, 10.5e9, 150, 90, 24), (9, 11e9, 90, 45, 28)]
        ]
        result = perf.summarize(rows)["gui"]
        self.assertEqual(result["cpu_percent"], 25)
        self.assertEqual(result["footprint_mib_median"], 45)
        self.assertEqual(result["footprint_mib_sampled_peak"], 90)
        self.assertEqual(result["rss_mib_sampled_peak"], 150)
        self.assertEqual(result["idle_wakeups_per_s"], 2)

    def test_roles_are_separate_and_zero_duration_is_rejected(self):
        row = dict(role="server", pid=1, elapsed_s=1, cpu_ns=0, rss_bytes=1, footprint_bytes=1, idle_wakeups=0)
        with self.assertRaises(ValueError):
            perf.summarize([row])
        rows = [row, {**row, "elapsed_s": 2}, {**row, "role": "push", "pid": 2},
                {**row, "role": "push", "pid": 2, "elapsed_s": 3, "cpu_ns": 1e9}]
        result = perf.summarize(rows)
        self.assertEqual(result["server"]["cpu_percent"], 0)
        self.assertEqual(result["push"]["cpu_percent"], 50)

    def test_p95_uses_nearest_rank(self):
        self.assertEqual(perf.percentile(list(range(1, 21)), .95), 19)
        self.assertIsNone(perf.percentile([], .95))

    def test_fragmented_frames_do_not_mix_snapshot_and_live_output(self):
        local, peer = socket.socketpair()
        viewer = perf.Viewer.__new__(perf.Viewer)
        viewer.conn = local
        viewer.stream = local.makefile("rb")
        def send():
            for content in ["中文 snapshot".encode(), b"live"]:
                frame = struct.pack(">IHH", len(content), 120, 38) + content
                for byte in frame:
                    peer.sendall(bytes([byte]))
            peer.close()
        thread = threading.Thread(target=send)
        thread.start()
        try:
            self.assertEqual(viewer.frame(), "中文 snapshot".encode())
            self.assertEqual((viewer.cols, viewer.rows), (120, 38))
            self.assertEqual(viewer.frame(), b"live")
            with self.assertRaises(EOFError):
                viewer.frame()
        finally:
            thread.join()
            viewer.stream.close()
            local.close()


if __name__ == "__main__":
    unittest.main()
