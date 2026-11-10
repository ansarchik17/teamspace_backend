package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/telemetry"
)

// APIMonitoringHandler serves the API monitoring dashboard and telemetry endpoints
type APIMonitoringHandler struct {
	tracker *telemetry.APITracker
}

// NewAPIMonitoringHandler creates a new APIMonitoringHandler instance
func NewAPIMonitoringHandler(tracker *telemetry.APITracker) *APIMonitoringHandler {
	return &APIMonitoringHandler{
		tracker: tracker,
	}
}

// GetStats returns the current telemetry statistics as JSON (supports ?origin= filter)
func (h *APIMonitoringHandler) GetStats(c *gin.Context) {
	if h.tracker == nil {
		respondJSON(c, http.StatusOK, gin.H{"error": "Tracker not initialized"})
		return
	}
	originFilter := c.Query("origin")
	respondJSON(c, http.StatusOK, h.tracker.GetStats(originFilter))
}

// ServeDashboard renders the mini-monitoring HTML dashboard UI
func (h *APIMonitoringHandler) ServeDashboard(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Security-Policy", "default-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://fonts.gstatic.com; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; script-src 'self' 'unsafe-inline'; font-src 'self' https://fonts.gstatic.com")
	c.String(http.StatusOK, dashboardHTML)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="ru">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>ALEM API Telemetry & Third-Party Monitoring</title>
  <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
    body { background-color: #0f172a !important; color: #f8fafc !important; padding: 24px; min-height: 100vh; }
    .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; padding-bottom: 16px; border-bottom: 1px solid #334155; }
    .header h1 { font-size: 24px; font-weight: 700; color: #38bdf8; display: flex; align-items: center; gap: 10px; }
    .badge { background: #0284c7; color: white; padding: 4px 10px; border-radius: 20px; font-size: 12px; font-weight: 600; }
    .refresh-text { color: #94a3b8; font-size: 13px; display: flex; align-items: center; gap: 6px; }
    .dot { width: 8px; height: 8px; background: #22c55e; border-radius: 50%; display: inline-block; animation: pulse 1.5s infinite; }
    @keyframes pulse { 0% { opacity: 0.4; } 50% { opacity: 1; } 100% { opacity: 0.4; } }

    .filter-bar { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 14px 20px; margin-bottom: 24px; display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
    .filter-input-wrap { display: flex; align-items: center; gap: 10px; flex: 1; min-width: 280px; }
    .filter-input { width: 100%; background: #0f172a; border: 1px solid #334155; color: #38bdf8; padding: 8px 14px; border-radius: 8px; font-size: 13px; outline: none; }
    .filter-input:focus { border-color: #38bdf8; }
    .filter-badge { background: rgba(56, 189, 248, 0.15); border: 1px solid #38bdf8; color: #38bdf8; padding: 6px 12px; border-radius: 8px; font-size: 13px; font-weight: 600; display: none; align-items: center; gap: 8px; }
    .btn-clear { background: #f43f5e; color: white; border: none; padding: 6px 12px; border-radius: 6px; font-size: 12px; cursor: pointer; font-weight: 600; }
    .btn-clear:hover { background: #e11d48; }

    .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 16px; margin-bottom: 24px; }
    .stat-card { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 20px; }
    .stat-title { color: #94a3b8; font-size: 13px; font-weight: 500; margin-bottom: 8px; }
    .stat-value { font-size: 28px; font-weight: 700; color: #f8fafc; }
    .stat-sub { font-size: 12px; color: #64748b; margin-top: 4px; }
    .text-alert { color: #f43f5e; }
    .text-success { color: #22c55e; }
    .text-sky { color: #38bdf8; }

    .grid-2 { display: grid; grid-template-columns: repeat(auto-fit, minmax(400px, 1fr)); gap: 20px; margin-bottom: 24px; }
    .panel { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 20px; }
    .panel-title { font-size: 16px; font-weight: 600; color: #e2e8f0; margin-bottom: 16px; display: flex; justify-content: space-between; align-items: center; }

    table { width: 100%; border-collapse: collapse; text-align: left; font-size: 13px; }
    th { color: #94a3b8; font-weight: 600; padding: 10px 12px; border-bottom: 1px solid #334155; background: #0f172a; }
    td { padding: 10px 12px; border-bottom: 1px solid #334155; color: #cbd5e1; }
    tr.clickable-row { cursor: pointer; transition: background 0.15s; }
    tr.clickable-row:hover td { background: #334155 !important; color: #38bdf8; }

    .tag { padding: 3px 8px; border-radius: 6px; font-size: 11px; font-weight: 600; display: inline-block; }
    .tag-third-party { background: rgba(244, 63, 94, 0.2); color: #f43f5e; border: 1px solid rgba(244, 63, 94, 0.4); }
    .tag-official { background: rgba(34, 197, 94, 0.2); color: #22c55e; border: 1px solid rgba(34, 197, 94, 0.4); }
    .tag-script { background: rgba(234, 179, 8, 0.2); color: #eab308; border: 1px solid rgba(234, 179, 8, 0.4); }
    .tag-site { background: rgba(168, 85, 247, 0.2); color: #c084fc; border: 1px solid rgba(168, 85, 247, 0.4); }

    .code-text { font-family: monospace; color: #38bdf8; word-break: break-all; }
  </style>
</head>
<body style="background-color: #0f172a; color: #f8fafc; padding: 24px;">
  <div class="header">
    <h1><span>🛡️</span> ALEM API Telemetry & Third-Party Monitoring</h1>
    <div class="refresh-text"><span class="dot"></span> Живой мониторинг (Обновление каждые 3с)</div>
  </div>

  <div class="filter-bar">
    <div class="filter-input-wrap">
      <span style="font-size:14px; color:#94a3b8;">🔍 Фильтр:</span>
      <input type="text" id="originInput" class="filter-input" placeholder="Введите домен источника (например: aitugovworkspace.kz)" onkeyup="onFilterKeyup(event)">
    </div>
    <div id="filterBadge" class="filter-badge">
      <span id="filterText">Фильтр по домену</span>
      <button class="btn-clear" onclick="clearFilter()">✕ Сбросить</button>
    </div>
  </div>

  <div class="stats-grid">
    <div class="stat-card">
      <div class="stat-title">Всего запросов</div>
      <div class="stat-value text-sky" id="totalReqs">0</div>
      <div class="stat-sub">Общий объем HTTP трафика</div>
    </div>
    <div class="stat-card">
      <div class="stat-title">Официальный трафик</div>
      <div class="stat-value text-success" id="officialReqs">0</div>
      <div class="stat-sub">alem-frontend (с X-App-Client)</div>
    </div>
    <div class="stat-card">
      <div class="stat-title">Сторонний трафик (Конкуренты/Парсеры)</div>
      <div class="stat-value text-alert" id="thirdPartyReqs">0</div>
      <div class="stat-sub" id="thirdPartyPercent">0% от общего объема</div>
    </div>
  </div>

  <div class="grid-2">
    <div class="panel">
      <div class="panel-title" id="endpointsTitle">🔥 Топ Эндпоинтов у сторонних разработчиков</div>
      <table>
        <thead>
          <tr>
            <th>API Endpoint</th>
            <th style="text-align: right;">Запросы</th>
          </tr>
        </thead>
        <tbody id="endpointsTable">
          <tr><td colspan="2" style="text-align:center; color:#64748b;">Загрузка данных...</td></tr>
        </tbody>
      </table>
    </div>

    <div class="panel">
      <div class="panel-title">🌐 Топ Сторонних Источников (Кликните для фильтрации)</div>
      <table>
        <thead>
          <tr>
            <th>Origin / Сайт источника</th>
            <th style="text-align: right;">Вызовы</th>
          </tr>
        </thead>
        <tbody id="originsTable">
          <tr><td colspan="2" style="text-align:center; color:#64748b;">Загрузка данных...</td></tr>
        </tbody>
      </table>
    </div>
  </div>

  <div class="panel">
    <div class="panel-title" id="feedTitle">📡 Лента последних запросов (Real-time feed)</div>
    <table>
      <thead>
        <tr>
          <th>Время</th>
          <th>Метод & Путь</th>
          <th>Тип Клиента</th>
          <th>Origin</th>
          <th>User-Agent / IP</th>
          <th>Статус</th>
        </tr>
      </thead>
      <tbody id="recentTable">
        <tr><td colspan="6" style="text-align:center; color:#64748b;">Ожидание вызовов API...</td></tr>
      </tbody>
    </table>
  </div>

  <script>
    let activeFilter = '';

    function setFilter(domain) {
      activeFilter = domain.trim();
      document.getElementById('originInput').value = activeFilter;
      updateFilterUI();
      fetchTelemetry();
    }

    function clearFilter() {
      activeFilter = '';
      document.getElementById('originInput').value = '';
      updateFilterUI();
      fetchTelemetry();
    }

    function onFilterKeyup(e) {
      activeFilter = e.target.value.trim();
      updateFilterUI();
      fetchTelemetry();
    }

    function updateFilterUI() {
      const badge = document.getElementById('filterBadge');
      const filterText = document.getElementById('filterText');
      const epTitle = document.getElementById('endpointsTitle');
      const feedTitle = document.getElementById('feedTitle');

      if (activeFilter) {
        badge.style.display = 'flex';
        filterText.innerText = 'Фильтр по источнику: ' + activeFilter;
        epTitle.innerText = '🔥 Эндпоинты, используемые: ' + activeFilter;
        feedTitle.innerText = '📡 Лента запросов источника: ' + activeFilter;
      } else {
        badge.style.display = 'none';
        epTitle.innerText = '🔥 Топ Эндпоинтов у сторонних разработчиков';
        feedTitle.innerText = '📡 Лента последних запросов (Real-time feed)';
      }
    }

    async function fetchTelemetry() {
      try {
        let url = '/api/admin/monitoring/stats';
        if (activeFilter) {
          url += '?origin=' + encodeURIComponent(activeFilter);
        }
        const res = await fetch(url);
        const data = await res.json();

        document.getElementById('totalReqs').innerText = (data.total_requests || 0).toLocaleString();
        document.getElementById('officialReqs').innerText = (data.official_requests || 0).toLocaleString();
        document.getElementById('thirdPartyReqs').innerText = (data.third_party_requests || 0).toLocaleString();

        const pct = data.total_requests > 0 ? ((data.third_party_requests / data.total_requests) * 100).toFixed(1) : 0;
        document.getElementById('thirdPartyPercent').innerText = pct + '% от общего трафика';

        // Render Top Endpoints
        const epBody = document.getElementById('endpointsTable');
        if (data.top_endpoints && data.top_endpoints.length > 0) {
          epBody.innerHTML = data.top_endpoints.map(function(e) {
            return '<tr>' +
              '<td class="code-text">' + e.path + '</td>' +
              '<td style="text-align: right; font-weight: 600;">' + e.count.toLocaleString() + '</td>' +
            '</tr>';
          }).join('');
        } else {
          epBody.innerHTML = '<tr><td colspan="2" style="text-align:center; color:#64748b;">Эндпоинты для выбранного источника не найдены</td></tr>';
        }

        // Render Top Origins (Clickable)
        const ogBody = document.getElementById('originsTable');
        if (data.top_origins && data.top_origins.length > 0) {
          ogBody.innerHTML = data.top_origins.map(function(o) {
            const isSelected = activeFilter && o.key.toLowerCase().includes(activeFilter.toLowerCase());
            const bgStyle = isSelected ? 'background: #334155; font-weight: 600;' : '';
            return '<tr class="clickable-row" style="' + bgStyle + '" onclick="setFilter(\'' + o.key.replace(/'/g, "\\'") + '\')">' +
              '<td class="code-text">🌐 ' + o.key + '</td>' +
              '<td style="text-align: right; font-weight: 600;">' + o.count.toLocaleString() + ' ➔</td>' +
            '</tr>';
          }).join('');
        } else {
          ogBody.innerHTML = '<tr><td colspan="2" style="text-align:center; color:#64748b;">Источники пока не зафиксированы</td></tr>';
        }

        // Render Recent Logs
        const recBody = document.getElementById('recentTable');
        if (data.recent_logs && data.recent_logs.length > 0) {
          recBody.innerHTML = data.recent_logs.slice(0, 30).map(function(l) {
            const timeStr = new Date(l.timestamp).toLocaleTimeString();
            let tagClass = l.is_third_party ? 'tag-third-party' : 'tag-official';
            let clientLabel = l.client_type;
            if (l.client_type === 'competitor_web_site') tagClass = 'tag-site';
            if (l.client_type === 'external_script_bot') tagClass = 'tag-script';

            const methodColor = l.method === 'GET' ? '#38bdf8' : '#eab308';
            const statusColor = l.status_code < 400 ? '#22c55e' : '#f43f5e';
            const originVal = l.origin || l.referer || '-';
            const uaSub = l.user_agent ? l.user_agent.substring(0, 40) : '';

            return '<tr>' +
              '<td style="color:#94a3b8;">' + timeStr + '</td>' +
              '<td><strong style="color:' + methodColor + ';">' + l.method + '</strong> <span class="code-text">' + l.path + '</span></td>' +
              '<td><span class="tag ' + tagClass + '">' + clientLabel + '</span></td>' +
              '<td class="code-text">' + originVal + '</td>' +
              '<td style="font-size:12px; color:#94a3b8;">' + l.ip + ' <br><span style="color:#64748b;">' + uaSub + '</span></td>' +
              '<td><span style="color:' + statusColor + '; font-weight:600;">' + l.status_code + '</span></td>' +
            '</tr>';
          }).join('');
        } else {
          recBody.innerHTML = '<tr><td colspan="6" style="text-align:center; color:#64748b;">Вызовы с выбранным фильтром пока не зафиксированы</td></tr>';
        }
      } catch (err) {
        console.error("Telemetry fetch failed", err);
      }
    }

    fetchTelemetry();
    setInterval(fetchTelemetry, 3000);
  </script>
</body>
</html>`
