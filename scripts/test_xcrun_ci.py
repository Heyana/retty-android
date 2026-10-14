import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("xcrun_ci", Path(__file__).with_name("xcrun-ci.py"))
xcrun_ci = importlib.util.module_from_spec(spec)
spec.loader.exec_module(xcrun_ci)


def result(code, output):
    return subprocess.CompletedProcess([], code, stdout=output)


class StaplerRetryTests(unittest.TestCase):
    def invoke(self, args, results):
        output = io.StringIO()
        with mock.patch.object(xcrun_ci.subprocess, "run", side_effect=results) as execute, \
                mock.patch.object(xcrun_ci.time, "sleep") as sleep, \
                contextlib.redirect_stdout(output):
            status = xcrun_ci.run(args)
        return status, execute, sleep, output.getvalue()

    def test_transient_transport_errors_retry_staple_and_validate(self):
        for operation in ("staple", "validate"):
            for code in (1001, 1003, 1004, 1005, 1009):
                with self.subTest(operation=operation, code=code):
                    args = ["stapler", operation, "build/Retty App.app"]
                    status, execute, sleep, output = self.invoke(args, [
                        result(68, f"NSURLErrorDomain Code=-{code} network failure\n"),
                        result(0, "The action worked!\n"),
                    ])
                    self.assertEqual(status, 0)
                    self.assertEqual(execute.call_count, 2)
                    for call in execute.call_args_list:
                        self.assertEqual(call.args[0], ["/usr/bin/xcrun", *args])
                    sleep.assert_called_once_with(2)
                    self.assertIn("::warning::", output)
                    self.assertIn("The action worked!", output)

    def test_ticket_and_other_failures_are_not_retried(self):
        for code, output in (
            (68, "Ticket validation failed"),
            (65, "NSURLErrorDomain Code=-1005"),
            (68, "NSURLErrorDomain Code=-1200 TLS validation failed"),
            (68, "NSURLErrorDomain Code=-10050"),
        ):
            with self.subTest(code=code, output=output):
                status, execute, sleep, _ = self.invoke(
                    ["stapler", "validate", "Retty.dmg"], [result(code, output)],
                )
                self.assertEqual(status, code)
                execute.assert_called_once()
                sleep.assert_not_called()

    def test_network_failure_exhaustion_preserves_failure(self):
        status, execute, sleep, _ = self.invoke(
            ["stapler", "staple", "Retty.app"],
            [result(68, "NSURLErrorDomain Code=-1005")] * 5,
        )
        self.assertEqual(status, 68)
        self.assertEqual(execute.call_count, 5)
        self.assertEqual(sleep.call_args_list, [mock.call(n) for n in (2, 4, 8, 16)])

    def test_other_xcrun_commands_are_delegated_unchanged(self):
        for args in ([], ["--find", "stapler"], ["notarytool", "submit", "Retty.zip"], ["stapler", "help"]):
            with self.subTest(args=args), \
                    mock.patch.object(xcrun_ci.os, "execv") as delegate, \
                    mock.patch.object(xcrun_ci.subprocess, "run") as execute:
                self.assertEqual(xcrun_ci.run(args), 0)
                delegate.assert_called_once_with("/usr/bin/xcrun", ["/usr/bin/xcrun", *args])
                execute.assert_not_called()


if __name__ == "__main__":
    unittest.main()
