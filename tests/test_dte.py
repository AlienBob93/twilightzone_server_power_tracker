"""Tests for the Green Button parser (stdlib only, no Home Assistant needed)."""

from __future__ import annotations

from datetime import UTC, datetime
import importlib.util
from pathlib import Path
import sys
import unittest

DTE_PATH = (
    Path(__file__).resolve().parents[1]
    / "custom_components"
    / "dte_energy"
    / "dte.py"
)
_spec = importlib.util.spec_from_file_location("dte_feed", DTE_PATH)
assert _spec and _spec.loader
dte = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = dte
_spec.loader.exec_module(dte)

FEED = """<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>Energy Delivered (kWh)</title>
    <content>
      <ReadingType xmlns="http://naesb.org/espi">
        <uom>72</uom>
        <powerOfTenMultiplier>0</powerOfTenMultiplier>
      </ReadingType>
    </content>
  </entry>
  <entry>
    <title>Energy Delivered (CCF)</title>
    <content>
      <ReadingType xmlns="http://naesb.org/espi">
        <uom>119</uom>
        <powerOfTenMultiplier>-3</powerOfTenMultiplier>
      </ReadingType>
    </content>
  </entry>
  <entry>
    <title>Electric readings</title>
    <link rel="self" href="RetailCustomer/x/UsagePoint/y/MeterReading/01/IntervalBlock/KWH"/>
    <content>
      <IntervalBlock xmlns="http://naesb.org/espi">
        <interval><duration>86400</duration><start>1758340800</start></interval>
        <IntervalReading>
          <timePeriod><duration>3600</duration><start>1758340800</start></timePeriod>
          <value>1365</value>
          <ReadingQuality><quality>9</quality></ReadingQuality>
        </IntervalReading>
        <IntervalReading>
          <timePeriod><duration>3600</duration><start>1758344400</start></timePeriod>
          <value>940</value>
        </IntervalReading>
      </IntervalBlock>
    </content>
  </entry>
  <entry>
    <title>Gas readings</title>
    <link rel="self" href="RetailCustomer/x/UsagePoint/z/MeterReading/01/IntervalBlock/CCF"/>
    <content>
      <IntervalBlock xmlns="http://naesb.org/espi">
        <IntervalReading>
          <timePeriod><duration>86400</duration><start>1758340800</start></timePeriod>
          <value>2140</value>
        </IntervalReading>
      </IntervalBlock>
    </content>
  </entry>
</feed>
"""


class ParserTest(unittest.TestCase):
    """Electric and gas series extraction."""

    def test_parses_electric_and_gas(self) -> None:
        feed = dte.parse_feed(FEED)

        self.assertEqual(len(feed.electric), 2)
        self.assertEqual(
            feed.electric[0].start, datetime(2025, 9, 20, 4, 0, tzinfo=UTC)
        )
        self.assertAlmostEqual(feed.electric[0].kwh, 1.365)
        self.assertAlmostEqual(feed.electric[1].kwh, 0.94)
        self.assertEqual(feed.electric[0].quality, 9)
        self.assertIsNone(feed.electric[1].quality)
        self.assertEqual(feed.electric[0].duration_s, 3600)
        self.assertEqual(
            feed.electric[0].end, datetime(2025, 9, 20, 5, 0, tzinfo=UTC)
        )

        self.assertEqual(len(feed.gas), 1)
        self.assertAlmostEqual(feed.gas[0].ccf, 2.14)
        self.assertEqual(feed.gas[0].duration_s, 86400)
        self.assertEqual(
            feed.gas[0].start, datetime(2025, 9, 20, 4, 0, tzinfo=UTC)
        )

    def test_detects_electric_without_a_title(self) -> None:
        feed = FEED.replace("<title>Electric readings</title>", "")
        intervals = dte.parse_electric(feed)
        self.assertEqual(len(intervals), 2)

    def test_detects_gas_without_a_title(self) -> None:
        feed = FEED.replace("<title>Gas readings</title>", "")
        intervals = dte.parse_gas(feed)
        self.assertEqual(len(intervals), 1)

    def test_gas_only(self) -> None:
        feed = """<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>Energy Delivered (CCF)</title>
    <content>
      <ReadingType xmlns="http://naesb.org/espi">
        <uom>119</uom>
        <powerOfTenMultiplier>-3</powerOfTenMultiplier>
      </ReadingType>
    </content>
  </entry>
  <entry>
    <title>Gas readings</title>
    <link rel="self" href="RetailCustomer/x/UsagePoint/z/MeterReading/01/IntervalBlock/CCF"/>
    <content>
      <IntervalBlock xmlns="http://naesb.org/espi">
        <IntervalReading>
          <timePeriod><duration>86400</duration><start>1758340800</start></timePeriod>
          <value>2140</value>
        </IntervalReading>
      </IntervalBlock>
    </content>
  </entry>
</feed>
"""
        with self.assertRaises(dte.DteParseError):
            dte.parse_electric(feed)
        gas = dte.parse_gas(feed)
        self.assertEqual(len(gas), 1)
        self.assertAlmostEqual(gas[0].ccf, 2.14)

    def test_rejects_broken_xml(self) -> None:
        with self.assertRaises(dte.DteParseError):
            dte.parse_electric("<feed>")

    def test_rejects_feed_without_electric(self) -> None:
        with self.assertRaises(dte.DteParseError):
            dte.parse_electric("<feed xmlns='http://www.w3.org/2005/Atom'/>")


if __name__ == "__main__":
    unittest.main()
