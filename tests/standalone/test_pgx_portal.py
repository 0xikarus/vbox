"""Ownership and upgrade checks for the host portal hub controls (no Docker)."""
import importlib.util
import io
import json
from pathlib import Path
import re
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('pgx_portal', Path(__file__).resolve().parents[2] / 'deploy/pgx-portal/install-hub-start.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

PORTAL = '''SERVICE_ACTIONS = {}
GLOBAL_ACTIONS = {}
def comfy_free():
    pass
class Handler:
    def do_POST(self):
        if self.path == "/api/action":
            svc, act = "vbox", "start"
            if (svc, act) == ("comfy", "free"):
                pass
'''


class PortalControls(unittest.TestCase):
    def test_fresh_and_start_only_upgrade_are_idempotent(self):
        legacy = PORTAL.replace('GLOBAL_ACTIONS = ', 'SERVICE_ACTIONS["vbox"] = {"start": ("Start hub", None, False)}\nGLOBAL_ACTIONS = ').replace('def comfy_free():', 'def start_vbox_hub():\n    pass\n\ndef comfy_free():').replace('            if (svc, act) == ("comfy", "free"):', installer.LEGACY_DISPATCH + '            if (svc, act) == ("comfy", "free"):')
        for source in [PORTAL, legacy]:
            result = installer.patch(source)
            self.assertEqual(installer.patch(result), result)
            self.assertEqual(result.count('def stop_vbox_hub():'), 1)
            self.assertIn('"stop": ("Stop hub", None, True)', result)
            self.assertIn(installer.DISPATCH, result)

    def controls(self, label=None, hub_id='hub-' + 'a' * 20):
        run = Mock(return_value=SimpleNamespace(returncode=0, stdout=json.dumps([{'Config': {'Labels': {'dev.vmbox.hub': label or hub_id}}}])))
        env = {'json': json, 'os': SimpleNamespace(path=SimpleNamespace(expanduser=lambda p: p)), 're': re, 'subprocess': SimpleNamespace(run=run), 'port_open': Mock(), 'time': SimpleNamespace(sleep=Mock())}
        exec(installer.HELPERS, env)
        return env, run, json.dumps({'id': hub_id})

    def test_stop_targets_only_owned_manager(self):
        env, run, config = self.controls()
        with patch('builtins.open', return_value=io.StringIO(config)):
            self.assertIn('retained', env['stop_vbox_hub']())
        self.assertEqual([c.args[0] for c in run.call_args_list], [['docker', 'inspect', 'vbox-hub-' + 'a' * 20], ['docker', 'stop', '--time', '30', 'vbox-hub-' + 'a' * 20]])

    def test_wrong_owner_and_invalid_identity_never_stop(self):
        for label, hub_id in [('another-hub', 'hub-' + 'a' * 20), (None, 'hub-../other')]:
            env, run, config = self.controls(label, hub_id)
            with patch('builtins.open', return_value=io.StringIO(config)):
                with self.assertRaises(RuntimeError):
                    env['stop_vbox_hub']()
            self.assertFalse(any('stop' in c.args[0] for c in run.call_args_list))


if __name__ == '__main__':
    unittest.main()
