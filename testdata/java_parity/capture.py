"""Records which stop, route and service IDs a OneBusAway server returns for
the synthetic feed, for TestJavaParity_SyntheticTwoAgencyFeed.

usage: python3 capture.py BASE_URL > java_ids.json

Run it against Java OneBusAway serving feed/ (see README.md). For each
request it records whether an entity came back and every (key, value) pair
whose value is one of the feed's stop, route or service IDs.
"""
import datetime
import json
import re
import sys
import urllib.error
import urllib.request
from zoneinfo import ZoneInfo

base = sys.argv[1]

tz = ZoneInfo("America/Los_Angeles")
midnight = datetime.datetime(2026, 10, 24, tzinfo=tz)
service_date = int(midnight.timestamp() * 1000)
ten_oh_one = int((midnight + datetime.timedelta(hours=10, minutes=1)).timestamp() * 1000)
date = midnight.date().isoformat()
trip, block = "1_R1-0-08", "1_R1-0-BLK04"
lat, lon = 47.6002, -122.33

requests = [
    "stop-ids-for-agency/40", "stop-ids-for-agency/1",
    "stops-for-agency/40", "stops-for-agency/1",
    "stop/40_A", "stop/1_A", "stop/40_B", "stop/1_B", "stop/40_U", "stop/1_U",
    "stop/40_STA", "stop/1_STA", "stop/40_B2", "stop/1_B2",
    "stops-for-route/40_R40", "stops-for-route/1_R1",
    f"schedule-for-stop/40_A?date={date}", f"schedule-for-stop/1_A?date={date}",
    f"schedule-for-stop/40_B?date={date}", f"schedule-for-stop/1_B?date={date}",
    f"arrivals-and-departures-for-stop/40_A?time={ten_oh_one}",
    f"arrivals-and-departures-for-stop/1_A?time={ten_oh_one}",
    f"arrivals-and-departures-for-stop/40_B?time={ten_oh_one}",
    f"arrivals-and-departures-for-stop/40_U?time={ten_oh_one}",
    f"arrival-and-departure-for-stop/40_A?tripId={trip}&serviceDate={service_date}&time={ten_oh_one}",
    f"arrival-and-departure-for-stop/1_A?tripId={trip}&serviceDate={service_date}&time={ten_oh_one}",
    f"stops-for-location?lat={lat}&lon={lon}&radius=300",
    f"stops-for-location?lat={lat}&lon={lon}&radius=300&query=999",
    f"stops-for-location?lat={lat}&lon={lon}&radius=300&query=250",
    f"routes-for-location?lat={lat}&lon={lon}&radius=300",
    f"trip/{trip}",
    f"trip-details/{trip}?serviceDate={service_date}&time={ten_oh_one}",
    f"trips-for-route/1_R1?time={ten_oh_one}&includeSchedule=true",
    f"trips-for-location?lat={lat}&lon={lon}&latSpan=0.01&lonSpan=0.01&time={ten_oh_one}",
    f"block/{block}", "block/40_R40-0-BLK04",
    f"schedule-for-route/1_R1?date={date}", f"schedule-for-route/40_R40?date={date}",
]

FEED_ID = re.compile(r"^(40|1)_(A|B|B2|C|U|STA|R1|R40|ALL)$")


def fetch(req):
    path, _, query = req.partition("?")
    url = f"{base}/api/where/{path}.json?key=test" + (f"&{query}" if query else "")
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        try:
            return json.load(e)
        except ValueError:
            return None


def feed_ids(node, key, acc):
    if isinstance(node, dict):
        for k, v in node.items():
            feed_ids(v, k, acc)
    elif isinstance(node, list):
        for v in node:
            feed_ids(v, key, acc)
    elif isinstance(node, str) and FEED_ID.match(node):
        acc.add(f"{key}={node}")
    return acc


results = {}
for req in requests:
    body = fetch(req)
    data = body.get("data") if isinstance(body, dict) else None
    found = isinstance(body, dict) and body.get("code") == 200 and data not in (None, {})
    results[req] = {"found": found, "ids": sorted(feed_ids(body, "", set())) if found else []}

json.dump(results, sys.stdout, indent=1)
sys.stdout.write("\n")
