(() => {
  "use strict";

  const refreshMs = Math.max(2, Number(document.body.dataset.refresh || 10)) * 1000;
  const state = {
    hours: 24,
    samples: [],
    status: null,
    stats: null,
    online: true,
    error: null,
  };

  const $ = (id) => document.getElementById(id);
  const nf = (value, digits = 1) =>
    value == null || Number.isNaN(Number(value)) ? "—" : Number(value).toFixed(digits);

  const palette = {
    input: "#38bdf8",
    output: "#c084fc",
    load: "#fbbf24",
    charge: "#34d399",
  };

  async function fetchJSON(url) {
    const response = await fetch(url, {
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (!response.ok) {
      throw new Error(`${url} → HTTP ${response.status}`);
    }
    return response.json();
  }

  function statusInfo(sample, status) {
    const tokens = (sample?.status || "").toUpperCase().split(/\s+/).filter(Boolean);
    if (status?.stale) return { text: "No recent data", cls: "unknown" };
    if (tokens.includes("LB")) return { text: "Low battery", cls: "bad" };
    if (tokens.includes("OB")) return { text: "On battery", cls: "warn" };
    if (tokens.includes("BYPASS")) return { text: "Bypass", cls: "warn" };
    if (tokens.includes("OL")) return { text: "Online", cls: "ok" };
    if (tokens.includes("CHRG")) return { text: "Charging", cls: "ok" };
    return { text: tokens[0] || "Unknown", cls: "unknown" };
  }

  function renderStatus() {
    const status = state.status;
    const sample = status?.sample || null;
    const info = statusInfo(sample, status);

    const pill = $("statusPill");
    pill.textContent = info.text;
    pill.className = `pill ${info.cls}`;

    const connection = $("connection");
    const collector = status?.collector;
    if (!state.online) {
      connection.textContent = "offline";
      connection.className = "connection bad";
      connection.title = state.error || "";
    } else if (collector?.last_error) {
      connection.textContent = "collector error";
      connection.className = "connection warn";
      connection.title = collector.last_error;
    } else {
      connection.textContent = "live";
      connection.className = "connection ok";
      connection.title = "";
    }

    if (sample) {
      const raw = sample.raw || {};
      const model = raw["ups.model"] || raw["device.model"] || "";
      const vendor = raw["ups.mfr"] || raw["device.mfr"] || "";
      const identity = [vendor, model].filter(Boolean).join(" ") || sample.ups;
      $("subtitle").textContent = `${identity} · updated ${fmtAge(status.age_seconds)}`;
      $("footerUps").textContent = model ? `${sample.ups} · ${model}` : sample.ups;
    } else {
      $("subtitle").textContent = state.error || "Waiting for the first sample…";
    }
  }

  function fmtAge(seconds) {
    if (seconds == null) return "no data yet";
    if (seconds < 90) return `${Math.round(seconds)}s ago`;
    if (seconds < 5400) return `${Math.round(seconds / 60)}m ago`;
    if (seconds < 172800) return `${Math.round(seconds / 3600)}h ago`;
    return `${Math.round(seconds / 86400)}d ago`;
  }

  function renderCards() {
    const sample = state.status?.sample;
    const set = (id, value, digits = 1) => {
      $(id).textContent = nf(value, digits);
    };

    set("inputVoltage", sample?.input_voltage);
    set("outputVoltage", sample?.output_voltage);
    set("loadPercent", sample?.load_percent, 0);
    set("batteryCharge", sample?.battery_charge, 0);
    set("runtime", sample?.battery_runtime_seconds == null ? null : sample.battery_runtime_seconds / 60, 0);
    set("estPower", sample?.estimated_power_w, 0);
    set("temperature", sample?.temperature_c, 1);

    $("inputSub").textContent = sample?.input_frequency != null
      ? `${nf(sample.input_frequency, 1)} Hz`
      : "line in";
    $("outputSub").textContent = sample?.output_frequency != null
      ? `${nf(sample.output_frequency, 1)} Hz`
      : "to the load";
    $("loadSub").textContent = sample?.nominal_power_w != null
      ? `nominal ${nf(sample.nominal_power_w, 0)} W`
      : "utilization";
    $("chargeSub").textContent = sample?.battery_voltage != null
      ? `battery ${nf(sample.battery_voltage, 1)} V`
      : "state of charge";
    $("runtimeSub").textContent = (sample?.status || "").includes("OB")
      ? "on battery"
      : "estimated";
    $("powerSub").textContent = sample?.estimated_power_w != null
      ? "load × nominal"
      : "unavailable";
    $("tempSub").textContent = sample?.temperature_c != null ? "enclosure" : "sensor";
  }

  function renderStats() {
    const stats = state.stats?.stats;
    const list = $("stats");
    list.replaceChildren();

    if (!stats || !stats.metrics) {
      $("statsWindow").textContent = "No samples in this window.";
      return;
    }

    const metrics = stats.metrics;
    const rows = [
      ["Input voltage", metrics.input_voltage, "V"],
      ["Output voltage", metrics.output_voltage, "V"],
      ["Load", metrics.load_percent, "%"],
      ["Battery charge", metrics.battery_charge, "%"],
      ["Est. power", metrics.estimated_power_w, "W"],
      ["Temperature", metrics.temperature_c, "°C"],
    ];

    for (const [label, metric, unit] of rows) {
      const dt = document.createElement("dt");
      dt.textContent = label;
      const dd = document.createElement("dd");
      dd.textContent = metric && metric.avg != null
        ? `${nf(metric.min, 1)} / ${nf(metric.avg, 1)} / ${nf(metric.max, 1)} ${unit}`
        : "—";
      list.append(dt, dd);
    }

    const dt = document.createElement("dt");
    dt.textContent = "On battery";
    const dd = document.createElement("dd");
    dd.textContent = stats.samples
      ? `${stats.on_battery_samples || 0} of ${stats.samples} samples`
      : "—";
    list.append(dt, dd);

    const from = stats.from ? new Date(stats.from) : null;
    const to = stats.to ? new Date(stats.to) : null;
    $("statsWindow").textContent = from && to
      ? `${from.toLocaleString()} → ${to.toLocaleString()} · min / avg / max`
      : "No samples in this window.";
  }

  function setupCanvas(canvas) {
    const dpr = window.devicePixelRatio || 1;
    const rect = canvas.getBoundingClientRect();
    const width = Math.max(1, rect.width);
    const height = Math.max(1, rect.height);
    canvas.width = Math.round(width * dpr);
    canvas.height = Math.round(height * dpr);
    const ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    return { ctx, width, height };
  }

  function axisTime(timestamp, hours) {
    const date = new Date(timestamp);
    return hours >= 48
      ? date.toLocaleDateString([], { month: "short", day: "numeric" })
      : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  function drawChart(canvas, series, options) {
    const { ctx, width, height } = setupCanvas(canvas);
    const pad = { top: 14, right: 14, bottom: 28, left: 44 };
    const plotWidth = Math.max(1, width - pad.left - pad.right);
    const plotHeight = Math.max(1, height - pad.top - pad.bottom);
    ctx.clearRect(0, 0, width, height);

    const visible = series.flatMap((s) => s.points).filter((p) => p.value != null);
    if (!visible.length) {
      ctx.fillStyle = "rgba(230, 237, 247, 0.55)";
      ctx.font = "13px ui-sans-serif, system-ui, sans-serif";
      ctx.textAlign = "center";
      ctx.textBaseline = "middle";
      ctx.fillText("No data in this window yet", width / 2, height / 2);
      return;
    }

    let t0 = Infinity;
    let t1 = -Infinity;
    let y0 = Infinity;
    let y1 = -Infinity;
    for (const s of series) {
      for (const point of s.points) {
        if (point.value == null) continue;
        t0 = Math.min(t0, point.time);
        t1 = Math.max(t1, point.time);
        y0 = Math.min(y0, point.value);
        y1 = Math.max(y1, point.value);
      }
    }
    if (!Number.isFinite(t0)) return;
    if (t1 === t0) t1 = t0 + 1;

    if (options.min != null) y0 = options.min;
    if (options.max != null) y1 = options.max;
    if (y0 === y1) {
      y0 -= 1;
      y1 += 1;
    }
    const padding = (y1 - y0) * 0.12;
    if (options.min == null) y0 -= padding;
    if (options.max == null) y1 += padding;

    const x = (t) => pad.left + ((t - t0) / (t1 - t0)) * plotWidth;
    const y = (v) => pad.top + (1 - (v - y0) / (y1 - y0)) * plotHeight;

    ctx.strokeStyle = "rgba(148, 163, 184, 0.14)";
    ctx.lineWidth = 1;
    ctx.font = "11px ui-sans-serif, system-ui, sans-serif";
    ctx.fillStyle = "rgba(139, 155, 180, 0.9)";
    ctx.textAlign = "right";
    ctx.textBaseline = "middle";
    for (let i = 0; i <= 4; i++) {
      const gridY = pad.top + (plotHeight / 4) * i;
      ctx.beginPath();
      ctx.moveTo(pad.left, gridY);
      ctx.lineTo(pad.left + plotWidth, gridY);
      ctx.stroke();
      const value = y1 - ((y1 - y0) / 4) * i;
      ctx.fillText(value.toFixed(options.digits ?? 0), pad.left - 8, gridY);
    }

    ctx.textBaseline = "top";
    ctx.textAlign = "left";
    ctx.fillText(axisTime(t0, state.hours), pad.left, pad.top + plotHeight + 9);
    ctx.textAlign = "right";
    ctx.fillText(axisTime(t1, state.hours), pad.left + plotWidth, pad.top + plotHeight + 9);

    for (const s of series) {
      ctx.beginPath();
      ctx.lineWidth = 2;
      ctx.strokeStyle = s.color;
      ctx.lineJoin = "round";
      ctx.lineCap = "round";
      let started = false;
      for (const point of s.points) {
        if (point.value == null) continue;
        const px = x(point.time);
        const py = y(point.value);
        if (!started) {
          ctx.moveTo(px, py);
          started = true;
        } else {
          ctx.lineTo(px, py);
        }
      }
      ctx.stroke();
    }
  }

  function renderCharts() {
    const samples = state.samples;
    const toPoint = (sample, key) => ({
      time: new Date(sample.timestamp).getTime(),
      value: sample[key],
    });

    drawChart(
      $("voltageChart"),
      [
        { color: palette.input, points: samples.map((s) => toPoint(s, "input_voltage")) },
        { color: palette.output, points: samples.map((s) => toPoint(s, "output_voltage")) },
      ],
      { digits: 0 },
    );

    drawChart(
      $("loadChart"),
      [
        { color: palette.load, points: samples.map((s) => toPoint(s, "load_percent")) },
        { color: palette.charge, points: samples.map((s) => toPoint(s, "battery_charge")) },
      ],
      { min: 0, max: 100, digits: 0 },
    );
  }

  async function refresh() {
    try {
      const [status, samples, stats] = await Promise.all([
        fetchJSON("/api/status"),
        fetchJSON(`/api/samples?hours=${state.hours}&limit=2000`),
        fetchJSON(`/api/stats?hours=${state.hours}`),
      ]);
      state.status = status;
      state.samples = samples.samples || [];
      state.stats = stats;
      state.online = true;
      state.error = null;
    } catch (error) {
      state.online = false;
      state.error = String(error?.message || error);
    }

    renderStatus();
    renderCards();
    renderStats();
    renderCharts();
  }

  document.querySelectorAll(".ranges button").forEach((button) => {
    button.addEventListener("click", () => {
      state.hours = Number(button.dataset.hours) || 24;
      document.querySelectorAll(".ranges button").forEach((candidate) => {
        candidate.classList.toggle("active", candidate === button);
      });
      refresh();
    });
  });

  let resizeTimer;
  window.addEventListener("resize", () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(renderCharts, 150);
  });

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refresh();
  });

  refresh();
  setInterval(refresh, refreshMs);
})();
