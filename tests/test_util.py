"""Tests for the non-secret config-entry identifier."""

from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import unittest

UTIL_PATH = (
    Path(__file__).resolve().parents[1]
    / "custom_components"
    / "dte_energy"
    / "util.py"
)
_spec = importlib.util.spec_from_file_location("dte_util", UTIL_PATH)
assert _spec and _spec.loader
util = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = util
_spec.loader.exec_module(util)


class UrlUniqueIdTest(unittest.TestCase):
    """The share token must not end up in the unique ID."""

    def test_is_stable(self) -> None:
        url = "https://usagedata.dteenergy.com/link/ABCDEF-123456"
        self.assertEqual(util.url_unique_id(url), util.url_unique_id(url))
        self.assertEqual(len(util.url_unique_id(url)), 64)

    def test_differs_per_link(self) -> None:
        first = "https://usagedata.dteenergy.com/link/AAAA"
        second = "https://usagedata.dteenergy.com/link/BBBB"
        self.assertNotEqual(util.url_unique_id(first), util.url_unique_id(second))

    def test_does_not_contain_the_token(self) -> None:
        token = "ABCDEF-123456"
        unique_id = util.url_unique_id(
            f"https://usagedata.dteenergy.com/link/{token}"
        )
        self.assertNotIn(token, unique_id)


if __name__ == "__main__":
    unittest.main()
