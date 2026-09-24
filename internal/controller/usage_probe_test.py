"""Parser checks with synthetic local responses; no account or network access."""

import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import mock_open, patch


spec = importlib.util.spec_from_file_location("usage_probe", Path(__file__).with_name("usage_probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class UsageProbeTests(unittest.TestCase):
    def capture(self, function, *args):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            function(*args)
        return json.loads(output.getvalue())

    def test_claude_usage_report(self):
        report = {"type": "assistant", "local_command_run": {"command": "usage"},
                  "usage_report": {"rate_limits": {"limits": [
                      {"kind": "session", "group": "session", "percent": 4,
                       "resets_at": "2026-09-24T04:20:00Z", "scope": None},
                      {"kind": "weekly_scoped", "group": "weekly", "percent": 0,
                       "scope": {"model": {"display_name": "Fable"}}}]}}}
        result = subprocess.CompletedProcess([], 0, stdout=json.dumps(report) + "\n")
        with patch.object(probe.subprocess, "run", return_value=result):
            output = self.capture(probe.claude)
        self.assertEqual(output["windows"][0]["usedPercent"], 4)
        self.assertEqual(output["windows"][1]["scope"], "Fable")

    def test_opencode_provider_limits(self):
        credentials = '{"openrouter":{"type":"api","key":"synthetic-secret"}}'
        with patch("builtins.open", mock_open(read_data=credentials)), patch.object(
                probe, "get_json", return_value={"data": {"limit": 100, "limit_remaining": 74.5,
                                                          "usage": 25.5, "limit_reset": "monthly"}}) as request:
            output = self.capture(probe.opencode, "", "")
        self.assertEqual(output["spend"]["remaining"], 74.5)
        self.assertEqual(request.call_args.args[0], "https://openrouter.ai/api/v1/key")
        self.assertNotIn("synthetic-secret", json.dumps(output))

    def test_codex_multi_bucket_response(self):
        fake_server = """import json,sys
for line in sys.stdin:
 item=json.loads(line)
 if item.get('id')==2:
  print(json.dumps({'id':2,'result':{'rateLimitsByLimitId':{
   'codex':{'primary':{'usedPercent':25,'windowDurationMins':15,'resetsAt':1730947200}},
   'other':{'secondary':{'usedPercent':42,'windowDurationMins':60,'resetsAt':1730950800}}}}}),flush=True)
  break
"""
        real_popen = subprocess.Popen

        def local_server(*_args, **kwargs):
            return real_popen([sys.executable, "-u", "-c", fake_server], **kwargs)

        with patch.object(probe.subprocess, "Popen", side_effect=local_server):
            output = self.capture(probe.codex)
        self.assertEqual([(window["name"], window["group"], window["usedPercent"])
                          for window in output["windows"]],
                         [("codex", "primary", 25), ("other", "secondary", 42)])

    def test_venice_filters_rates_to_selected_model(self):
        credentials = '{"venice":{"type":"api","key":"synthetic-secret"}}'
        response = {"data": {"balances": {"USD": 50.23}, "rateLimits": [
            {"apiModelId": "chosen", "rateLimits": [{"type": "RPM", "amount": 100}]},
            {"apiModelId": "other", "rateLimits": [{"type": "RPM", "amount": 5}]}]}}
        with patch("builtins.open", mock_open(read_data=credentials)), patch.object(
                probe, "get_json", return_value=response):
            output = self.capture(probe.opencode, "venice", "chosen")
        self.assertEqual(output["balances"], [{"unit": "USD", "amount": 50.23}])
        self.assertEqual(output["rateCaps"], [{"model": "chosen", "type": "RPM", "amount": 100}])

    def test_unexpected_exception_text_is_not_returned(self):
        with patch.object(probe.sys, "argv", ["usage_probe.py", "opencode", "openrouter", ""]), patch.object(
                probe, "opencode", side_effect=ValueError("synthetic-secret")):
            output = self.capture(probe.main)
        self.assertEqual(output, {"error": "usage provider unavailable"})


if __name__ == "__main__":
    unittest.main()
