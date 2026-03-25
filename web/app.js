// ── Plotly dark theme shared config ─────────────────────────────────────
const DARK = {
  paper_bgcolor: 'transparent',
  plot_bgcolor:  'transparent',
  font:   { color: '#94a3b8', family: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif', size: 11 },
  xaxis:  { gridcolor: '#1e2d45', zerolinecolor: '#1e2d45', tickcolor: '#64748b', linecolor: '#1e2d45' },
  yaxis:  { gridcolor: '#1e2d45', zerolinecolor: '#1e2d45', tickcolor: '#64748b', linecolor: '#1e2d45' },
  yaxis2: { gridcolor: 'rgba(30,45,69,0.4)', zerolinecolor: '#1e2d45', tickcolor: '#64748b', linecolor: '#1e2d45' },
  margin: { t: 20, r: 20, b: 40, l: 50 },
  legend: { bgcolor: 'rgba(17,24,39,0.8)', bordercolor: '#1e2d45', borderwidth: 1 },
  hoverlabel: { bgcolor: '#1a2438', bordercolor: '#2d4a6e', font: { color: '#e2e8f0' } },
};
const PLOTLY_CONFIG = { responsive: true, displayModeBar: true, modeBarButtonsToRemove: ['lasso2d','select2d'], displaylogo: false };

// ── Colours ──────────────────────────────────────────────────────────────
const C = { hrv: '#10b981', atl: '#f59e0b', ctl: '#3b82f6', tsb: '#e2e8f0', vo2: '#8b5cf6', zone: 'rgba(239,68,68,0.08)' };

// ── State ────────────────────────────────────────────────────────────────
let period       = '3month';   // '1month' | '3month' | 'year'
let windowEnd    = new Date();
let windowStart  = shiftDate(windowEnd, period, -1);
let earliestInDB = null;       // oldest date present in DB
let syncInterval = null;
let allMetrics   = [];

// ── Init ─────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', async () => {
  const status = await fetch('/api/status').then(r => r.json());
  if (!status.loggedIn) {
    showLoginModal();
  } else {
    showUserBadge(status.displayName);
    loadData();
    // If a startup auto-sync is already running, attach to it immediately
    const syncStatus = await fetch('/api/sync/status').then(r => r.json());
    if (syncStatus.running) {
      setSyncButtonsDisabled(true);
      document.getElementById('sync-bar').classList.remove('hidden');
      if (!syncInterval) syncInterval = setInterval(pollSync, 800);
    }
  }
});

// ── Auth ─────────────────────────────────────────────────────────────────
function showLoginModal() {
  document.getElementById('login-modal').classList.remove('hidden');
}
function hideLoginModal() {
  document.getElementById('login-modal').classList.add('hidden');
}
function showUserBadge(name) {
  const el = document.getElementById('user-badge');
  if (name) { el.textContent = name; el.classList.remove('hidden'); }
}

async function doLogin() {
  const btn   = document.getElementById('login-btn');
  const err   = document.getElementById('login-error');
  const email = document.getElementById('login-email').value.trim();
  const pass  = document.getElementById('login-password').value;

  err.classList.add('hidden');
  if (!email || !pass) { showError('Please enter email and password'); return; }

  btn.disabled   = true;
  btn.textContent = 'Signing in…';

  try {
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password: pass }),
    });
    if (!res.ok) {
      const msg = await res.text();
      showError(msg || 'Login failed');
      return;
    }
    const data = await res.json();
    hideLoginModal();
    showUserBadge(data.displayName);
    loadData();
  } catch (e) {
    showError('Network error: ' + e.message);
  } finally {
    btn.disabled   = false;
    btn.textContent = 'Sign In';
  }
}

function showError(msg) {
  const el = document.getElementById('login-error');
  el.textContent = msg;
  el.classList.remove('hidden');
}

// Allow Enter key in login form
document.addEventListener('keydown', e => {
  if (e.key === 'Enter' && !document.getElementById('login-modal').classList.contains('hidden')) {
    doLogin();
  }
});

// ── Helpers ───────────────────────────────────────────────────────────────
function fmtDate(d) {
  return d.toISOString().split('T')[0];
}

// Shift a date forward (+1) or backward (-1) by the current period
function shiftDate(d, p, dir) {
  const r = new Date(d);
  if (p === 'year')   r.setFullYear(r.getFullYear() + dir);
  if (p === '3month') r.setMonth(r.getMonth() + dir * 3);
  if (p === '1month') r.setMonth(r.getMonth() + dir * 1);
  return r;
}

function periodLabel() {
  const mo = (d) => d.toLocaleDateString('en-US', { month: 'short', year: 'numeric' });
  if (period === '1month') return mo(windowStart);
  return `${mo(windowStart)}  –  ${mo(windowEnd)}`;
}

// ── Period selector ───────────────────────────────────────────────────────
function setPeriod(p) {
  period      = p;
  windowStart = shiftDate(windowEnd, p, -1);
  document.querySelectorAll('.period-btn').forEach(b =>
    b.classList.toggle('active', b.dataset.period === p));
  loadData();
}

// ── Navigation ────────────────────────────────────────────────────────────
function prevPeriod() {
  windowEnd   = new Date(windowStart);
  windowStart = shiftDate(windowEnd, period, -1);
  loadData();
}

function nextPeriod() {
  const today = new Date(); today.setHours(23, 59, 59, 0);
  windowStart = new Date(windowEnd);
  windowEnd   = shiftDate(windowStart, period, +1);
  if (windowEnd > today) {
    windowEnd   = new Date();
    windowStart = shiftDate(windowEnd, period, -1);
  }
  loadData();
}

function updateNav() {
  document.getElementById('range-label').textContent = periodLabel();

  const today = new Date(); today.setHours(0, 0, 0, 0);

  // ‹ disabled when there is no data before the current window start
  document.getElementById('prev-btn').disabled =
    !earliestInDB || fmtDate(windowStart) <= earliestInDB;

  // › disabled when the window already ends at today (or beyond)
  document.getElementById('next-btn').disabled =
    fmtDate(windowEnd) >= fmtDate(today);
}

// ── Data Loading ─────────────────────────────────────────────────────────
async function loadData() {
  try {
    const [dataRes, statsRes] = await Promise.all([
      fetch(`/api/data?start=${fmtDate(windowStart)}&end=${fmtDate(windowEnd)}`),
      fetch('/api/stats'),
    ]);
    const body  = await dataRes.json();
    const stats = statsRes.ok ? await statsRes.json() : null;
    allMetrics   = body.metrics || [];
    earliestInDB = body.earliestInDB || null;
    updateDBRangeBadge(body.earliestInDB, body.latestInDB);
    updateNav();

    if (allMetrics.length === 0) {
      document.getElementById('empty-state').classList.remove('hidden');
      showDiagnostics(stats);
      return;
    }
    document.getElementById('empty-state').classList.add('hidden');

    updateKPIs(allMetrics);
    drawOverview(allMetrics);
    drawPMC(allMetrics);
    drawHRVvsATL(allMetrics);
    drawHRVvsVO2(allMetrics);
    drawRunningChart(allMetrics);
    drawVO2Trend(allMetrics);
  } catch (e) {
    console.error('Failed to load data:', e);
  }
}

function showDiagnostics(stats) {
  const box  = document.getElementById('diag-box');
  const hint = document.getElementById('empty-hint');
  if (!stats) return;

  const hrv = stats['hrv_data']        || { rows: 0 };
  const ts  = stats['training_status'] || { rows: 0 };
  const vo2 = stats['vo2max_data']     || { rows: 0 };
  const total = hrv.rows + ts.rows + vo2.rows;

  if (total === 0) {
    hint.innerHTML = 'Click <strong>Sync</strong> to fetch your Garmin data.';
    box.classList.add('hidden');
    return;
  }

  hint.textContent = 'Data exists in the database but none falls in the selected range.';
  box.classList.remove('hidden');
  box.innerHTML = `
    <p style="margin:0 0 6px;font-weight:600;color:#94a3b8">Database contents</p>
    <table class="diag-table">
      <tr><th>Table</th><th>Rows</th><th>Date range</th></tr>
      <tr>
        <td>HRV</td>
        <td>${hrv.rows}</td>
        <td>${hrv.rows ? hrv.minDate + ' → ' + hrv.maxDate : '—'}</td>
      </tr>
      <tr>
        <td>Training status</td>
        <td>${ts.rows}</td>
        <td>${ts.rows ? ts.minDate + ' → ' + ts.maxDate : '—'}</td>
      </tr>
      <tr>
        <td>VO₂Max</td>
        <td>${vo2.rows}</td>
        <td>${vo2.rows ? vo2.minDate + ' → ' + vo2.maxDate : '—'}</td>
      </tr>
    </table>
    <p style="margin:10px 0 0;color:#64748b;font-size:12px">
      If all rows are 0, sync ran but Garmin returned no data.<br>
      Visit <a href="/api/probe" target="_blank" style="color:#3b82f6">/api/probe</a> to see the raw API response and share it for diagnosis.
    </p>
  `;
}

// ── Sync ─────────────────────────────────────────────────────────────────
function setSyncButtonsDisabled(disabled) {
  document.getElementById('sync-btn').disabled = disabled;
  document.getElementById('full-sync-btn').disabled = disabled;
}

async function startSync() {
  setSyncButtonsDisabled(true);
  const res = await fetch(`/api/sync?start=${fmtDate(windowStart)}&end=${fmtDate(windowEnd)}`, { method: 'POST' });
  if (!res.ok) {
    alert(await res.text());
    setSyncButtonsDisabled(false);
    return;
  }
  document.getElementById('sync-bar').classList.remove('hidden');
  if (!syncInterval) syncInterval = setInterval(pollSync, 800);
}

async function startFullSync() {
  if (!confirm('This will download your full Garmin history (could take several minutes). Continue?')) return;
  setSyncButtonsDisabled(true);
  const res = await fetch('/api/sync/all', { method: 'POST' });
  if (!res.ok) {
    alert(await res.text());
    setSyncButtonsDisabled(false);
    return;
  }
  const data = await res.json();
  document.getElementById('sync-bar').classList.remove('hidden');
  document.getElementById('sync-label').textContent = `Full history from ${data.from}…`;
  if (!syncInterval) syncInterval = setInterval(pollSync, 800);
}

async function pollSync() {
  const state = await fetch('/api/sync/status').then(r => r.json());
  const fill  = document.getElementById('sync-fill');
  const label = document.getElementById('sync-label');
  const range = document.getElementById('sync-db-range');

  fill.style.width  = (state.percent || 0) + '%';
  label.textContent = state.message || 'Syncing…';

  if (!state.running) {
    clearInterval(syncInterval);
    syncInterval = null;
    setSyncButtonsDisabled(false);

    if (state.error) {
      label.textContent = '✗ ' + state.error;
      setTimeout(() => document.getElementById('sync-bar').classList.add('hidden'), 6000);
    } else {
      fill.style.width  = '100%';
      label.textContent = '✓ Complete';
      // Reload data then update the DB range shown in the bar
      await loadData();
      setTimeout(() => document.getElementById('sync-bar').classList.add('hidden'), 2500);
    }
  }
}

// Update the DB coverage badge in the sync bar
function updateDBRangeBadge(earliest, latest) {
  const el = document.getElementById('sync-db-range');
  if (!el) return;
  if (earliest && latest) {
    el.textContent = `DB: ${earliest} → ${latest}`;
    el.style.display = 'inline';
  } else {
    el.style.display = 'none';
  }
}

// ── KPI Cards ────────────────────────────────────────────────────────────
function updateKPIs(metrics) {
  const last7 = metrics.slice(-7);

  // Average HRV (last 7 days)
  const hrvVals = last7.filter(m => m.hrv != null).map(m => m.hrv);
  if (hrvVals.length) {
    const avg = Math.round(hrvVals.reduce((a, b) => a + b, 0) / hrvVals.length);
    document.getElementById('kpi-hrv-val').textContent = avg;
    document.getElementById('kpi-hrv-val').style.color = hrvColor(avg, metrics);
  }

  // Latest VO2Max
  const vo2 = [...metrics].reverse().find(m => m.vo2max != null);
  if (vo2) {
    document.getElementById('kpi-vo2-val').textContent = vo2.vo2max.toFixed(1);
  }

  // Latest TSB (form)
  const latest = [...metrics].reverse().find(m => m.tsb != null);
  if (latest) {
    const tsb = Math.round(latest.tsb);
    document.getElementById('kpi-form-val').textContent = (tsb > 0 ? '+' : '') + tsb;
    document.getElementById('kpi-form-val').style.color = tsb >= 0 ? '#10b981' : tsb < -20 ? '#ef4444' : '#f59e0b';
    document.getElementById('kpi-form-sub').textContent = tsbLabel(tsb);
  }

  // Latest CTL (fitness)
  const ctl = [...metrics].reverse().find(m => m.ctl != null);
  if (ctl) {
    document.getElementById('kpi-ctl-val').textContent = Math.round(ctl.ctl);
  }
}

function tsbLabel(tsb) {
  if (tsb >  25) return 'Very fresh / detraining';
  if (tsb >   5) return 'Fresh & ready';
  if (tsb >  -5) return 'Neutral';
  if (tsb > -20) return 'Slightly fatigued';
  if (tsb > -35) return 'Fatigued';
  return 'Overreaching';
}

function hrvColor(val, metrics) {
  // Use the median as a rough baseline
  const vals = metrics.filter(m => m.hrv != null).map(m => m.hrv).sort((a, b) => a - b);
  if (!vals.length) return '#e2e8f0';
  const median = vals[Math.floor(vals.length / 2)];
  if (val >= median * 1.05) return '#10b981';
  if (val <= median * 0.90) return '#ef4444';
  return '#e2e8f0';
}

// ── Chart 1: Overview ────────────────────────────────────────────────────
function drawOverview(metrics) {
  const dates = metrics.map(m => m.date);
  const hrv   = metrics.map(m => m.hrv ?? null);
  const atl   = metrics.map(m => m.atl ?? null);
  const ctl   = metrics.map(m => m.ctl ?? null);

  const traces = [
    {
      x: dates, y: hrv, name: 'HRV', type: 'scatter', mode: 'lines',
      line: { color: C.hrv, width: 2 },
      yaxis: 'y2',
      hovertemplate: 'HRV: %{y:.0f} ms<extra></extra>',
    },
    {
      x: dates, y: atl, name: 'ATL (Fatigue)', type: 'scatter', mode: 'lines',
      line: { color: C.atl, width: 2 },
      fill: 'tozeroy', fillcolor: 'rgba(245,158,11,0.06)',
      hovertemplate: 'ATL: %{y:.1f}<extra></extra>',
    },
    {
      x: dates, y: ctl, name: 'CTL (Fitness)', type: 'scatter', mode: 'lines',
      line: { color: C.ctl, width: 2.5 },
      hovertemplate: 'CTL: %{y:.1f}<extra></extra>',
    },
  ];

  const layout = {
    ...DARK,
    yaxis:  { ...DARK.yaxis, title: { text: 'Training Load', standoff: 8 } },
    yaxis2: { ...DARK.yaxis2, title: { text: 'HRV (ms)', standoff: 8 }, overlaying: 'y', side: 'right' },
    legend: { ...DARK.legend, orientation: 'h', y: -0.12 },
    hovermode: 'x unified',
  };

  Plotly.newPlot('chart-overview', traces, layout, PLOTLY_CONFIG);
}

// ── Chart 2: Performance Management Chart ───────────────────────────────
function drawPMC(metrics) {
  const dates = metrics.map(m => m.date);
  const ctl   = metrics.map(m => m.ctl ?? null);
  const atl   = metrics.map(m => m.atl ?? null);
  const tsb   = metrics.map(m => m.tsb ?? null);

  // Background fatigue zone (TSB < 0)
  const tsbColors = tsb.map(v => v == null ? null : (v >= 0 ? '#10b981' : v < -20 ? '#ef4444' : '#f59e0b'));

  const traces = [
    {
      x: dates, y: ctl, name: 'CTL (Fitness)', type: 'scatter', mode: 'lines',
      line: { color: C.ctl, width: 2.5 },
      hovertemplate: 'Fitness (CTL): %{y:.1f}<extra></extra>',
    },
    {
      x: dates, y: atl, name: 'ATL (Fatigue)', type: 'scatter', mode: 'lines',
      line: { color: C.atl, width: 2 },
      hovertemplate: 'Fatigue (ATL): %{y:.1f}<extra></extra>',
    },
    {
      x: dates, y: tsb, name: 'TSB (Form)', type: 'scatter', mode: 'lines+markers',
      line: { color: '#ffffff', width: 1.5 },
      marker: { color: tsbColors, size: 4 },
      yaxis: 'y2',
      hovertemplate: 'Form (TSB): %{y:.1f}<extra></extra>',
    },
    // Zero line for TSB reference
    {
      x: [dates[0], dates[dates.length - 1]], y: [0, 0],
      type: 'scatter', mode: 'lines',
      line: { color: 'rgba(100,116,139,0.4)', dash: 'dot', width: 1 },
      yaxis: 'y2', showlegend: false,
      hoverinfo: 'skip',
    },
  ];

  const layout = {
    ...DARK,
    yaxis:  { ...DARK.yaxis,  title: { text: 'Load', standoff: 8 } },
    yaxis2: { ...DARK.yaxis2, title: { text: 'Form (TSB)', standoff: 8 }, overlaying: 'y', side: 'right' },
    legend: { ...DARK.legend, orientation: 'h', y: -0.12 },
    hovermode: 'x unified',
  };

  Plotly.newPlot('chart-pmc', traces, layout, PLOTLY_CONFIG);
}

// ── Chart 3: HRV vs ATL Scatter ──────────────────────────────────────────
function drawHRVvsATL(metrics) {
  const pts = metrics.filter(m => m.hrv != null && m.atl != null);
  if (!pts.length) return;

  // Colour by date (older = darker)
  const n = pts.length;
  const colors = pts.map((_, i) => `rgba(59,130,246,${0.2 + 0.7 * (i / n)})`);

  // Simple linear regression for trend line
  const xs = pts.map(m => m.atl);
  const ys = pts.map(m => m.hrv);
  const { slope, intercept } = linReg(xs, ys);
  const xMin = Math.min(...xs), xMax = Math.max(...xs);

  const traces = [
    {
      x: xs, y: ys, text: pts.map(m => m.date),
      type: 'scatter', mode: 'markers',
      marker: { color: colors, size: 7 },
      hovertemplate: '%{text}<br>ATL: %{x:.1f}<br>HRV: %{y:.0f} ms<extra></extra>',
      name: 'Days',
    },
    {
      x: [xMin, xMax], y: [slope * xMin + intercept, slope * xMax + intercept],
      type: 'scatter', mode: 'lines',
      line: { color: 'rgba(59,130,246,0.5)', dash: 'dash', width: 1.5 },
      name: 'Trend', hoverinfo: 'skip',
    },
  ];

  const layout = {
    ...DARK,
    xaxis: { ...DARK.xaxis, title: { text: 'ATL (Fatigue)' } },
    yaxis: { ...DARK.yaxis, title: { text: 'HRV (ms)' } },
  };

  Plotly.newPlot('chart-hrv-atl', traces, layout, PLOTLY_CONFIG);
}

// ── Chart 4: HRV vs VO2Max scatter ───────────────────────────────────────
function drawHRVvsVO2(metrics) {
  const pts = metrics.filter(m => m.hrv != null && m.vo2max != null);
  if (!pts.length) {
    document.getElementById('chart-hrv-vo2').innerHTML =
      '<p style="color:#64748b;padding:20px;text-align:center">No VO₂Max data available</p>';
    return;
  }

  const n = pts.length;
  const colors = pts.map((_, i) => `rgba(139,92,246,${0.25 + 0.65 * (i / n)})`);

  const xs = pts.map(m => m.hrv);
  const ys = pts.map(m => m.vo2max);
  const { slope, intercept } = linReg(xs, ys);
  const xMin = Math.min(...xs), xMax = Math.max(...xs);

  const traces = [
    {
      x: xs, y: ys, text: pts.map(m => m.date),
      type: 'scatter', mode: 'markers',
      marker: { color: colors, size: 7 },
      hovertemplate: '%{text}<br>HRV: %{x:.0f} ms<br>VO₂Max: %{y:.1f}<extra></extra>',
      name: 'Days',
    },
    {
      x: [xMin, xMax], y: [slope * xMin + intercept, slope * xMax + intercept],
      type: 'scatter', mode: 'lines',
      line: { color: 'rgba(139,92,246,0.5)', dash: 'dash', width: 1.5 },
      name: 'Trend', hoverinfo: 'skip',
    },
  ];

  const layout = {
    ...DARK,
    xaxis: { ...DARK.xaxis, title: { text: 'HRV (ms)' } },
    yaxis: { ...DARK.yaxis, title: { text: 'VO₂Max (ml/kg/min)' } },
  };

  Plotly.newPlot('chart-hrv-vo2', traces, layout, PLOTLY_CONFIG);
}

// ── Chart: Running Distance ───────────────────────────────────────────────
function drawRunningChart(metrics) {
  const runDays = metrics.filter(m => m.kmRun != null && m.kmRun > 0);
  const el = document.getElementById('chart-running');

  if (!runDays.length) {
    el.innerHTML = '<p style="color:#64748b;padding:20px;text-align:center">No running data in this range — sync to fetch activity history</p>';
    return;
  }

  const allDates = metrics.map(m => m.date);
  const allKm    = metrics.map(m => m.kmRun ?? 0);

  // 7-day rolling average over run days only (skip rest days)
  const km7dAvg = allKm.map((_, i) => {
    const slice = allKm.slice(Math.max(0, i - 6), i + 1).filter(v => v > 0);
    return slice.length ? slice.reduce((a, b) => a + b, 0) / slice.length : null;
  });

  // Period average km per run (horizontal reference line)
  const totalKm = runDays.reduce((a, m) => a + m.kmRun, 0);
  const avgKm   = totalKm / runDays.length;

  // VO2Max line
  const vo2Dates = metrics.filter(m => m.vo2max != null).map(m => m.date);
  const vo2Vals  = metrics.filter(m => m.vo2max != null).map(m => m.vo2max);

  const traces = [
    {
      x: runDays.map(m => m.date), y: runDays.map(m => m.kmRun),
      type: 'bar', name: 'Daily km',
      marker: { color: 'rgba(16,185,129,0.45)' },
      hovertemplate: '%{x}<br>Run: %{y:.2f} km<extra></extra>',
    },
    {
      x: allDates, y: km7dAvg,
      type: 'scatter', mode: 'lines', name: '7-day avg km',
      line: { color: '#10b981', width: 2 },
      hovertemplate: '7d avg: %{y:.2f} km<extra></extra>',
    },
    {
      x: [allDates[0], allDates[allDates.length - 1]],
      y: [avgKm, avgKm],
      type: 'scatter', mode: 'lines', name: `Period avg: ${avgKm.toFixed(1)} km`,
      line: { color: 'rgba(16,185,129,0.7)', dash: 'dash', width: 1.5 },
      hoverinfo: 'skip',
    },
    {
      x: vo2Dates, y: vo2Vals,
      type: 'scatter', mode: 'lines', name: 'VO₂Max',
      line: { color: C.vo2, width: 2 },
      yaxis: 'y2',
      hovertemplate: 'VO₂Max: %{y:.1f}<extra></extra>',
    },
  ];

  const layout = {
    ...DARK,
    barmode: 'overlay',
    yaxis:  { ...DARK.yaxis,  title: { text: 'km per run' } },
    yaxis2: { ...DARK.yaxis2, title: { text: 'VO₂Max (ml/kg/min)' }, overlaying: 'y', side: 'right', showgrid: false },
    legend: { ...DARK.legend, orientation: 'h', y: -0.12 },
    hovermode: 'x unified',
  };

  Plotly.newPlot('chart-running', traces, layout, PLOTLY_CONFIG);
}

// ── Chart 5: VO2Max Trend ────────────────────────────────────────────────
function drawVO2Trend(metrics) {
  const vo2pts = metrics.filter(m => m.vo2max != null);
  if (!vo2pts.length) {
    document.getElementById('chart-vo2max').innerHTML =
      '<p style="color:#64748b;padding:20px;text-align:center">No VO₂Max data available — ensure your Garmin device supports VO₂Max estimation</p>';
    return;
  }

  const dates = vo2pts.map(m => m.date);
  const vo2   = vo2pts.map(m => m.vo2max);

  // Rolling 7-day average VO2Max
  const vo2Rolling = rollingAvg(vo2, 7);

  // ATL as background context
  const atlDates = metrics.filter(m => m.atl != null).map(m => m.date);
  const atlVals  = metrics.filter(m => m.atl != null).map(m => m.atl);

  const traces = [
    {
      x: atlDates, y: atlVals, name: 'ATL (Fatigue)',
      type: 'bar', marker: { color: 'rgba(245,158,11,0.18)' },
      yaxis: 'y2',
      hovertemplate: 'ATL: %{y:.1f}<extra></extra>',
    },
    {
      x: dates, y: vo2, name: 'VO₂Max (daily)',
      type: 'scatter', mode: 'markers',
      marker: { color: C.vo2, size: 5, opacity: 0.5 },
      hovertemplate: 'VO₂Max: %{y:.1f}<extra></extra>',
    },
    {
      x: dates, y: vo2Rolling, name: 'VO₂Max (7d avg)',
      type: 'scatter', mode: 'lines',
      line: { color: C.vo2, width: 2.5 },
      hovertemplate: 'VO₂Max 7d: %{y:.1f}<extra></extra>',
    },
  ];

  const layout = {
    ...DARK,
    barmode: 'overlay',
    yaxis:  { ...DARK.yaxis, title: { text: 'VO₂Max (ml/kg/min)' } },
    yaxis2: { ...DARK.yaxis2, title: { text: 'ATL' }, overlaying: 'y', side: 'right', showgrid: false },
    legend: { ...DARK.legend, orientation: 'h', y: -0.12 },
    hovermode: 'x unified',
  };

  Plotly.newPlot('chart-vo2max', traces, layout, PLOTLY_CONFIG);
}

// ── Helpers ──────────────────────────────────────────────────────────────
function linReg(xs, ys) {
  const n = xs.length;
  if (n < 2) return { slope: 0, intercept: ys[0] || 0 };
  const sumX  = xs.reduce((a, b) => a + b, 0);
  const sumY  = ys.reduce((a, b) => a + b, 0);
  const sumXY = xs.reduce((a, x, i) => a + x * ys[i], 0);
  const sumX2 = xs.reduce((a, x) => a + x * x, 0);
  const denom = n * sumX2 - sumX * sumX;
  if (denom === 0) return { slope: 0, intercept: sumY / n };
  const slope = (n * sumXY - sumX * sumY) / denom;
  const intercept = (sumY - slope * sumX) / n;
  return { slope, intercept };
}

function rollingAvg(arr, window) {
  return arr.map((_, i) => {
    const slice = arr.slice(Math.max(0, i - window + 1), i + 1).filter(v => v != null);
    if (!slice.length) return null;
    return slice.reduce((a, b) => a + b, 0) / slice.length;
  });
}
