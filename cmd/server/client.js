// Shared navigation for mouse, keyboard, deep links and account actions.
(function () {
    const tabBtns = [...document.querySelectorAll('.tab-btn[data-tab]')];
    const tabPanels = [...document.querySelectorAll('.tab-panel[data-tab]')];
    let currentTab = '';
    function select(name, { focus = false, updateHash = true } = {}) {
        const target = tabBtns.find(btn => btn.dataset.tab === name);
        if (!target) return;
        const changed = currentTab !== name;
        const previousPanel = tabPanels.find(panel => panel.dataset.tab === currentTab);
        const moveFocus = focus || changed && (previousPanel?.contains(document.activeElement) ||
            document.getElementById('historyActionBar').contains(document.activeElement));
        currentTab = name;
        tabBtns.forEach(btn => {
            const active = btn.dataset.tab === name;
            btn.classList.toggle('active', active);
            btn.setAttribute('aria-selected', String(active));
            btn.tabIndex = active ? 0 : -1;
        });
        tabPanels.forEach(panel => {
            const active = panel.dataset.tab === name;
            panel.classList.toggle('active', active);
            panel.hidden = !active;
        });
        if (updateHash && changed) {
            const hash = name === 'checks' ? '#accountChecksPanel' : `#panel-${name}`;
            window.history.pushState(window.history.state, '', hash);
        }
        if (changed) document.dispatchEvent(new CustomEvent('auth-tab-changed', { detail: { name } }));
        if (moveFocus) target.focus();
    }
    tabBtns.forEach((btn, index) => {
        btn.addEventListener('click', () => select(btn.dataset.tab));
        btn.addEventListener('keydown', event => {
            let next;
            if (event.key === 'ArrowRight') next = (index + 1) % tabBtns.length;
            else if (event.key === 'ArrowLeft') next = (index + tabBtns.length - 1) % tabBtns.length;
            else if (event.key === 'Home') next = 0;
            else if (event.key === 'End') next = tabBtns.length - 1;
            else return;
            event.preventDefault();
            select(tabBtns[next].dataset.tab, { focus: true });
        });
    });
    function followHash() {
        const panel = tabPanels.find(panel => `#${panel.id}` === location.hash);
        select(location.hash === '#accountChecksPanel' ? 'checks' : panel?.dataset.tab || 'login', { updateHash: false });
    }
    window.AuthTabs = { select };
    window.addEventListener('hashchange', followHash);
    window.addEventListener('popstate', followHash);
    followHash();
})();

// ── Action Bar (history tab batch ops) ───────────────────────
const historyActionBar = document.getElementById('historyActionBar');
function updateActionBar(selectedCount, hadFocus = historyActionBar.contains(document.activeElement)) {
    const inHistory = document.getElementById('panel-history').classList.contains('active');
    const hide = selectedCount === 0 || !inHistory;
    const restoreFocus = hide && inHistory && hadFocus;
    historyActionBar.hidden = hide;
    if (restoreFocus) (historySelectAll.disabled ? document.getElementById('tab-history') : historySelectAll).focus();
}

const accountsInput = document.getElementById('accountsInput');
const proxyInput = document.getElementById('proxyInput');
const proxyCheckBtn = document.getElementById('proxyCheckBtn');
const proxyCheckStatus = document.getElementById('proxyCheckStatus');
const accountCount = document.getElementById('accountCount');
const modeSelect = document.getElementById('modeSelect');
const concurrencyInput = document.getElementById('concurrency');
const startBtn = document.getElementById('startBtn');
const retryFailedBtn = document.getElementById('retryFailedBtn');
const importSub2Btn = document.getElementById('importSub2Btn');
const exportBtn = document.getElementById('exportBtn');
const statusGrid = document.getElementById('statusGrid');
const totalCount = document.getElementById('totalCount');
const successCount = document.getElementById('successCount');
const failedCount = document.getElementById('failedCount');
const pendingCount = document.getElementById('pendingCount');
const historyGrid = document.getElementById('historyGrid');
const refreshHistoryBtn = document.getElementById('refreshHistoryBtn');
const historyRefreshStatus = document.getElementById('historyRefreshStatus');
const autoRecoveryToggle = document.getElementById('autoRecoveryToggle');
const autoRecoveryStatus = document.getElementById('autoRecoveryStatus');
const autoRecoveryDetail = document.getElementById('autoRecoveryDetail');
const historySearchInput = document.getElementById('historySearchInput');
const historyStatusFilter = document.getElementById('historyStatusFilter');
const historySort = document.getElementById('historySort');
const historyDetail = document.getElementById('historyDetail');
const historyDetailPanel = historyDetail.querySelector('[role="dialog"]') || historyDetail;
const historyDetailTitle = document.getElementById('historyDetailTitle');
const historyDetailBody = document.getElementById('historyDetailBody');
const historyDetailClose = document.getElementById('historyDetailClose');
const accountPreflight = document.getElementById('accountPreflight');
const historySelectAll = document.getElementById('historySelectAll');
const historyBatchReloginBtn = document.getElementById('historyBatchReloginBtn');
const historySelectionStatus = document.getElementById('historySelectionStatus');
const historyBatchStatus = document.getElementById('historyBatchStatus');
const historyClearSelectionBtn = document.getElementById('historyClearSelectionBtn');
const historyBatchDeleteBtn = document.getElementById('historyBatchDeleteBtn');
const historyBatchImportBtn = document.getElementById('historyBatchImportBtn');
const historyCheckNowBtn = document.getElementById('historyCheckNowBtn');
const historyCheckNowStatus = document.getElementById('historyCheckNowStatus');
const batchImportStatus = document.getElementById('batchImportStatus');
const historyPageInfo = document.getElementById('historyPageInfo');
const historyPageSizeSelect = document.getElementById('historyPageSize');
const historyPrevPageBtn = document.getElementById('historyPrevPageBtn');
const historyNextPageBtn = document.getElementById('historyNextPageBtn');
const actionConfirmDialog = document.getElementById('actionConfirmDialog');
const actionConfirmTitle = document.getElementById('actionConfirmTitle');
const actionConfirmMessage = document.getElementById('actionConfirmMessage');
const actionConfirmCancel = document.getElementById('actionConfirmCancel');
const actionConfirmAccept = document.getElementById('actionConfirmAccept');
let pendingConfirmation = null;

function confirmAction({ title, message, confirmLabel = '继续', danger = false }) {
    // One visible decision owns the dialog; a duplicate caller must not act on it.
    if (pendingConfirmation) return Promise.resolve(false);
    actionConfirmTitle.textContent = title;
    actionConfirmMessage.textContent = message;
    actionConfirmAccept.textContent = confirmLabel;
    actionConfirmAccept.classList.toggle('btn-danger', danger);
    return new Promise(resolve => {
        pendingConfirmation = { resolve, trigger: document.activeElement };
        actionConfirmDialog.showModal();
        actionConfirmCancel.focus({ preventScroll: true });
    });
}

function closeActionConfirmation(confirmed = false) {
    if (!pendingConfirmation) return;
    const { resolve, trigger } = pendingConfirmation;
    pendingConfirmation = null;
    if (actionConfirmDialog.open) actionConfirmDialog.close();
    if (trigger?.isConnected !== false && !trigger?.disabled && !trigger?.closest?.('[hidden]')) {
        trigger?.focus({ preventScroll: true });
    } else {
        document.querySelector('.tab-btn[aria-selected="true"]')?.focus({ preventScroll: true });
    }
    resolve(confirmed);
}

actionConfirmCancel.addEventListener('click', () => closeActionConfirmation());
actionConfirmAccept.addEventListener('click', () => closeActionConfirmation(true));
actionConfirmDialog.addEventListener('cancel', event => {
    event.preventDefault();
    closeActionConfirmation();
});
actionConfirmDialog.addEventListener('close', () => {
    if (!actionConfirmDialog.open) closeActionConfirmation();
});
actionConfirmDialog.addEventListener('keydown', event => {
    if (event.key !== 'Tab') return;
    const active = document.activeElement;
    if (!actionConfirmDialog.contains(active) || active === actionConfirmDialog ||
        (event.shiftKey ? active === actionConfirmCancel : active === actionConfirmAccept)) {
        event.preventDefault();
        (event.shiftKey ? actionConfirmAccept : actionConfirmCancel).focus();
    }
});

let accounts = [];
let results = [];
let historyAccounts = [];
let processing = false;
let proxyChecking = false;
let historyLoading = false;
let historyBusyID = '';
let historyPage = 1;
let historyPageSize = 20;
let historyBatchRunning = false;
let historyBatchStats = null;
let historyBatchSummary = '';
let batchImportProgress = null;
let historyLoadError = '';
let historySub2Configured = true;
let historyImportsAvailable = true;
let historySub2Statuses = new Map();
let historyChecks = new Map();
let historyRecoveries = new Map();
const historyRecoveryRequests = new Set();
const historyRecoveryNotices = new Map();
let recoverySettings = { autoRecoveryEnabled: false };
let recoverySettingsLoading = false;
let recoverySettingsSaving = false;
let historyRefreshTimer = null;
let historyRefreshAt = 0;
let historyRefreshPolling = false;
let historyDetailID = '';
let historyDetailAbort = null;
let historyDetailTrigger = null;
// Keep the fallback conservative if the health endpoint is unavailable or
// served by an older binary; the server's advertised cap raises it later.
let serverMaxConcurrent = 1;
const historySelectedIDs = new Set();
document.addEventListener('auth-tab-changed', event => {
    updateActionBar(historySelectedIDs.size);
    closeActionConfirmation();
    if (event.detail.name !== 'history') closeHistoryDetails();
});
const historyImports = new Map();
// Refresh tokens obtained from a history re-login live only for this page
// session.  The history API intentionally remains redacted.
const historyRefreshTokens = new Map();
const statusRows = new Map();
const stageProgressEstimates = {
    queued: 5,
    configuration: 8,
    browser: 15,
    authorize: 24,
    email: 35,
    password: 46,
    oauth: 56,
    mfa: 67,
    consent: 77,
    token: 86,
    identity: 93,
    complete: 100,
    retry_wait: 5,
    proxy: 12,
    challenge: 20
};
const stageProgressOrder = ['queued', 'configuration', 'browser', 'authorize', 'email', 'password',
    'oauth', 'mfa', 'consent', 'token', 'identity', 'complete'];
let platformPauseUntil = 0;
const stageNames = {queued:'排队', browser:'启动浏览器', authorize:'打开授权页', email:'邮箱验证',
    password:'密码验证', oauth:'等待登录确认', mfa:'二次验证', consent:'确认授权', token:'获取令牌',
    identity:'账号身份', proxy:'连接代理', challenge:'浏览器验证', configuration:'配置',
    retry_wait:'等待重试', complete:'完成', unknown:'登录'};

proxyCheckBtn.addEventListener('click', async () => {
    if (processing || proxyChecking) return;
    proxyChecking = true;
    proxyCheckBtn.disabled = true;
    proxyCheckBtn.innerHTML = '<span class="spinner"></span> 测试中';
    proxyCheckStatus.textContent = '正在测试当前网络...';
    try {
        const response = await fetch('/api/proxy-check', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ proxy: proxyInput.value.trim() })
        });
        const data = await response.json();
        if (!response.ok) throw new Error(data.message || '代理测试失败');
        proxyCheckStatus.textContent = (data.mode === 'direct' ? '✅ 直连：' : '🌐 代理：') + data.message + ' (' + data.elapsed_ms + 'ms)';
    } catch (error) {
        proxyCheckStatus.textContent = '❌ ' + (error.message || '代理测试失败');
    } finally {
        proxyChecking = false;
        proxyCheckBtn.disabled = processing;
        proxyCheckBtn.textContent = '测试代理';
    }
});

accountsInput.addEventListener('input', () => {
    renderAccountPreflight();
});

function analyzeAccountInput(raw) {
    const report = { totalLines: 0, ignored: 0, errors: [], duplicates: [], accounts: [] };
    const seenEmails = new Set();
    const lines = String(raw || '').split(/\r?\n/);
    report.totalLines = lines.length;
    lines.forEach((line, index) => {
        const lineNumber = index + 1;
        const trimmed = line.trim();
        if (!trimmed || trimmed.startsWith('#')) {
            report.ignored += 1;
            return;
        }
        let parts = trimmed.split('----');
        if (parts.length !== 3) parts = trimmed.split('---');
        if (parts.length !== 3 || !parts[0].trim() || !parts[1].trim()) {
            report.errors.push({ line: lineNumber, reason: '格式应为 email---password---totp_secret' });
            return;
        }
        const email = parts[0].trim();
        const normalizedEmail = email.toLowerCase();
        if (!/^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/.test(email)) {
            report.errors.push({ line: lineNumber, reason: '邮箱格式疑似无效' });
            return;
        }
        if (parts[1].trim().length < 8) {
            report.errors.push({ line: lineNumber, reason: '密码长度至少 8 位' });
            return;
        }
        if (seenEmails.has(normalizedEmail)) {
            report.duplicates.push({ line: lineNumber, email });
            return;
        }
        seenEmails.add(normalizedEmail);
        report.accounts.push({
            email,
            password: parts[1].trim(),
            totp_secret: parts[2].trim(),
            lineNumber
        });
    });
    return report;
}

function renderAccountPreflight() {
    const report = analyzeAccountInput(accountsInput.value);
    accountCount.textContent = `${report.accounts.length} 个账号`;
    const issueItems = [...report.errors.map(item => `第 ${item.line} 行：${item.reason}`),
        ...report.duplicates.map(item => `第 ${item.line} 行：${escapeHTML(item.email)} 与前面账号重复`)].slice(0, 8);
    accountPreflight.innerHTML = `
        <div class="preflight-summary">可执行 <strong>${report.accounts.length}</strong> 个 · 格式错误 <strong>${report.errors.length}</strong> 个 · 重复 <strong>${report.duplicates.length}</strong> 个 · 忽略空行/注释 <strong>${report.ignored}</strong> 行</div>
        ${issueItems.length ? `<ul>${issueItems.map(item => `<li>${item}</li>`).join('')}</ul>` : '<div class="preflight-ok">输入检查通过</div>'}`;
    accountPreflight.classList.toggle('preflight-warning', report.errors.length > 0 || report.duplicates.length > 0);
    return report;
}

modeSelect.addEventListener('change', () => {
    concurrencyInput.disabled = modeSelect.value === 'sequential';
});

refreshHistoryBtn.addEventListener('click', () => Promise.all([loadHistory(), loadRecoverySettings()]));
autoRecoveryToggle?.addEventListener('change', () => updateRecoverySetting(Boolean(autoRecoveryToggle.checked)));
function resetHistoryScroll() {
    document.getElementById('panel-history').scrollTop = 0;
}
historySearchInput.addEventListener('input', () => {
    historyPage = 1;
    renderHistory();
    resetHistoryScroll();
});
historyStatusFilter.addEventListener('change', () => {
    historyPage = 1;
    renderHistory();
    resetHistoryScroll();
});
historySort.addEventListener('change', () => {
    historyPage = 1;
    renderHistory();
    resetHistoryScroll();
});
historySelectAll.addEventListener('change', () => {
    if (historyActionsLocked()) return;
    const pageAccounts = getHistoryPageAccounts(getFilteredHistoryAccounts());
    pageAccounts.forEach(account => {
        if (historySelectAll.checked) historySelectedIDs.add(account.id);
        else historySelectedIDs.delete(account.id);
    });
    renderHistory();
});
historyBatchReloginBtn.addEventListener('click', () => batchReloginHistory());
historyClearSelectionBtn.addEventListener('click', () => {
    if (historyActionsLocked()) return;
    historySelectedIDs.clear();
    renderHistory();
});
historyBatchDeleteBtn.addEventListener('click', () => deleteHistoryAccounts([...historySelectedIDs]));
historyBatchImportBtn.addEventListener('click', () => batchImportHistory());
historyCheckNowBtn?.addEventListener('click', () => checkHistoryNow());
historyPageSizeSelect.addEventListener('change', () => {
    historyPageSize = Math.max(1, Number(historyPageSizeSelect.value) || 20);
    historyPage = 1;
    renderHistory();
    resetHistoryScroll();
});
historyPrevPageBtn.addEventListener('click', () => {
    if (historyPage > 1) {
        historyPage -= 1;
        renderHistory();
        resetHistoryScroll();
    }
});
historyNextPageBtn.addEventListener('click', () => {
    const pageCount = getHistoryPageCount(getFilteredHistoryAccounts().length);
    if (historyPage < pageCount) {
        historyPage += 1;
        renderHistory();
        resetHistoryScroll();
    }
});
historyGrid.addEventListener('click', event => {
    const button = event.target.closest('[data-history-action]');
    if (!button || button.disabled) return;
    const id = button.dataset.historyId;
    if (!id) return;
    if (button.dataset.historyAction === 'relogin') {
        reloginHistory(id);
    } else if (button.dataset.historyAction === 'import') {
        importHistoryAccounts([id]);
    } else if (button.dataset.historyAction === 'reconcile') {
        reconcileHistoryImport(id);
    } else if (button.dataset.historyAction === 'delete') {
        deleteHistory(id);
    } else if (button.dataset.historyAction === 'copy-refresh-token') {
        copyHistoryRefreshToken(id);
    } else if (button.dataset.historyAction === 'details') {
        openHistoryDetails(id, button);
    } else if (button.dataset.historyAction === 'check') {
        window.AccountChecks?.startSingle(id);
    } else if (button.dataset.historyAction === 'recover') {
        checkAndRecoverHistory(id);
    }
});
historyGrid.addEventListener('change', event => {
    const checkbox = event.target.closest('[data-history-select]');
    if (!checkbox || checkbox.disabled || historyActionsLocked()) return;
    const id = String(checkbox.dataset.historyId || '');
    if (!id) return;
    if (checkbox.checked) historySelectedIDs.add(id);
    else historySelectedIDs.delete(id);
    renderHistory();
    historyGrid.querySelector(`[data-history-select][data-history-id="${CSS.escape(id)}"]`)?.focus({ preventScroll: true });
});
historyDetailClose.addEventListener('click', closeHistoryDetails);
historyDetail.addEventListener('click', event => {
    if (event.target.closest('[data-history-detail-close]')) closeHistoryDetails();
});
document.addEventListener('keydown', event => {
    if (historyDetail.hidden || actionConfirmDialog.open) return;
    if (event.key === 'Escape') {
        event.preventDefault?.();
        closeHistoryDetails();
        return;
    }
    if (event.key !== 'Tab') return;
    const focusable = [...(historyDetailPanel.querySelectorAll?.('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])') || [])]
        .filter(node => !node.disabled && !node.hidden && typeof node.focus === 'function');
    const nodes = focusable.length ? focusable : [historyDetailClose];
    const first = nodes[0], last = nodes[nodes.length - 1], active = document.activeElement;
    const inside = active === historyDetailPanel || historyDetail.contains(active);
    if (!inside || (event.shiftKey ? active === first : active === last)) {
        event.preventDefault?.();
        (event.shiftKey ? last : first)?.focus({ preventScroll: true });
    }
});

renderAccountPreflight();
loadHistory();
loadServerCapabilities();
loadRecoverySettings();
scheduleHistoryRefresh();

startBtn.addEventListener('click', async () => {
    if (processing || historyBatchRunning) return;
    const report = renderAccountPreflight();
    if (report.accounts.length === 0) {
        alert('请输入账号信息');
        return;
    }
    if (report.errors.length || report.duplicates.length) {
        alert(`输入检查未通过：${report.errors.length} 行格式错误，${report.duplicates.length} 行重复。请修正后再开始。`);
        return;
    }

    accounts = report.accounts.map((account, index) => ({
        id: index,
        email: account.email,
        password: account.password,
        totp_secret: account.totp_secret,
        status: 'pending',
        message: '等待处理',
        result: null
    }));

    results = [];
    await runAccounts(accounts);
});

retryFailedBtn.addEventListener('click', () => {
    return runAccounts(accounts.filter(account => account.status === 'error'));
});

importSub2Btn.addEventListener('click', () => importLoginResultsToSub2());

window.retryAccount = function(id) {
    const account = accounts.find(account => account.id === id && account.status === 'error');
    if (account) return runAccounts([account]);
};

async function loadHistory() {
    if (historyLoading) {
        while (historyLoading) await new Promise(resolve => setTimeout(resolve, 25));
        return;
    }
    historyLoading = true;
    historyLoadError = '';
    historySearchInput.disabled = true;
    refreshHistoryBtn.disabled = true;
    historyRefreshStatus.textContent = '正在加载…';
    renderHistory();
    try {
        const response = await fetch('/api/history', { headers: { 'Accept': 'application/json' }, signal: AbortSignal.timeout(15000) });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `历史请求失败（HTTP ${response.status}）`);
        historySub2Configured = payload.sub2_configured !== false;
        historyImportsAvailable = payload.imports_available !== false;
        historySub2Statuses = normalizeSub2Statuses(payload.sub2_statuses ?? payload.sub2Statuses
            ?? payload.data?.sub2_statuses ?? payload.data?.sub2Statuses);
        historyChecks = new Map(Object.entries(payload.checks || {}));
        historyRecoveries = new Map(Object.entries(payload.recoveries || {}));
        const candidate = Array.isArray(payload) ? payload
            : (Array.isArray(payload.accounts) ? payload.accounts
                : (Array.isArray(payload.history) ? payload.history
                    : (Array.isArray(payload.data) ? payload.data : (payload.data && Array.isArray(payload.data.accounts) ? payload.data.accounts : []))));
        const rows = candidate;
        historyImports.clear();
        const importRows = Array.isArray(payload.imports) ? payload.imports
            : (payload.data && Array.isArray(payload.data.imports) ? payload.data.imports : []);
        importRows.forEach(item => {
            const accountID = String(item.account_id ?? item.accountId ?? '');
            if (accountID) historyImports.set(accountID, item);
        });
        historyAccounts = rows.map(row => normalizeHistoryAccount(row, historySub2Statuses.get(String(row?.id ?? row?.account_id ?? ''))))
            .filter(account => account.id !== '').map(account => {
            account.refreshToken = historyRefreshTokens.get(account.id) || '';
            return account;
        });
        const availableIDs = new Set(historyAccounts.map(account => account.id));
        historySelectedIDs.forEach(id => {
            if (!availableIDs.has(id)) historySelectedIDs.delete(id);
        });
        historyRefreshTokens.forEach((_, id) => {
            if (!availableIDs.has(id)) historyRefreshTokens.delete(id);
        });
        renderHistory();
        document.dispatchEvent(new CustomEvent('auth-history-changed', { detail: { history: payload } }));
    } catch (error) {
        historyLoadError = error.message || '历史加载失败';
        historyRefreshStatus.textContent = historyLoadError;
    } finally {
        historyLoading = false;
        historySearchInput.disabled = processing || historyBatchRunning;
        refreshHistoryBtn.disabled = processing || historyBatchRunning;
        historyRefreshAt = Date.now() + historyRefreshDelay();
        renderHistory();
    }
}

function scheduleHistoryRefresh() {
    if (historyRefreshTimer || typeof setInterval !== 'function') return;
    historyRefreshAt = Date.now() + historyRefreshDelay();
    historyRefreshTimer = setInterval(pollHistory, 5000);
}

function historyRefreshDelay() {
    const checking = [...historyChecks.values()].some(item => ['queued', 'running'].includes(item.latest_task?.state));
    const recovering = [...historyRecoveries.values()].some(item => historyRecoveryActive(item));
    return checking || recovering || historyRecoveryRequests.size || recoverySettings.scanning ? 5000
        : recoverySettings.autoRecoveryEnabled ? 60000 : 5 * 60000;
}

async function pollHistory() {
    if (document.hidden || historyLoading || historyRefreshPolling || processing || historyBatchRunning || Date.now() < historyRefreshAt) return;
    historyRefreshPolling = true;
    try {
        await loadRecoverySettings();
        await loadHistory();
    } finally {
        historyRefreshPolling = false;
    }
}

function applyRecoverySettings(payload) {
    const settings = payload.settings || payload;
    const enabled = settings.auto_recovery_enabled ?? settings.autoRecoveryEnabled;
    if (typeof enabled !== 'boolean') throw new Error('恢复设置响应不完整，请刷新重试');
    recoverySettings = { ...settings, autoRecoveryEnabled: enabled };
    autoRecoveryToggle.checked = recoverySettings.autoRecoveryEnabled;
    renderRecoverySettings();
    historyRefreshAt = Math.min(historyRefreshAt || Infinity, Date.now() + historyRefreshDelay());
}

function renderRecoverySettings() {
    if (autoRecoveryStatus) autoRecoveryStatus.textContent = recoverySettings.autoRecoveryEnabled
        ? recoverySettings.scanning ? '正在巡检…' : '已开启 · 后台运行' : '已关闭';
    if (!autoRecoveryDetail) return;
    const parts = [recoverySettings.autoRecoveryEnabled
        ? `每 ${Number(recoverySettings.interval_seconds) || 60} 秒巡检 Sub2 异常账号，确认 401 或凭据失效后重新登录、写回凭据并恢复调度。关闭页面后仍继续，开关已保存。`
        : '开启后立即巡检，此后每 60 秒检查 Sub2 异常账号；也可点击账号旁的“检测并恢复”。'];
    if (recoverySettings.last_scan_at) parts.push(`上次巡检：${formatHistoryTime(recoverySettings.last_scan_at)}`);
    if (recoverySettings.autoRecoveryEnabled && recoverySettings.next_scan_at) parts.push(`下次巡检：${formatHistoryTime(recoverySettings.next_scan_at)}`);
    if (recoverySettings.summary) parts.push(recoverySettings.summary);
    if (recoverySettings.last_error) parts.push(`巡检提示：${recoverySettings.last_error}`);
    autoRecoveryDetail.textContent = parts.join(' · ');
}

async function loadRecoverySettings() {
    if (!autoRecoveryToggle || recoverySettingsLoading || recoverySettingsSaving) return;
    recoverySettingsLoading = true;
    autoRecoveryToggle.disabled = true;
    try {
        const response = await fetch('/api/account-recovery/settings', {
            headers: { 'Accept': 'application/json' }, signal: AbortSignal.timeout(10000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `设置读取失败（HTTP ${response.status}）`);
        applyRecoverySettings(payload);
    } catch (error) {
        if (autoRecoveryStatus) autoRecoveryStatus.textContent = '设置读取失败';
    } finally {
        recoverySettingsLoading = false;
        autoRecoveryToggle.disabled = recoverySettingsSaving;
    }
}

async function updateRecoverySetting(enabled) {
    if (!autoRecoveryToggle || recoverySettingsSaving) return;
    const previous = recoverySettings.autoRecoveryEnabled;
    recoverySettingsSaving = true;
    autoRecoveryToggle.disabled = true;
    if (autoRecoveryStatus) autoRecoveryStatus.textContent = '保存中…';
    try {
        const response = await fetch('/api/account-recovery/settings', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
            body: JSON.stringify({ auto_recovery_enabled: enabled }),
            signal: AbortSignal.timeout(10000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `设置保存失败（HTTP ${response.status}）`);
        applyRecoverySettings(payload);
        await loadHistory();
        if (recoverySettings.autoRecoveryEnabled) historyRefreshAt = Date.now() + 5000;
    } catch (error) {
        recoverySettings.autoRecoveryEnabled = previous;
        autoRecoveryToggle.checked = previous;
        if (autoRecoveryStatus) autoRecoveryStatus.textContent = error.message || '设置保存失败';
    } finally {
        recoverySettingsSaving = false;
        autoRecoveryToggle.disabled = recoverySettingsLoading;
    }
}

async function loadServerCapabilities() {
    try {
        const response = await fetch('/health', { headers: { 'Accept': 'application/json' }, cache: 'no-store' });
        const payload = await response.json().catch(() => ({}));
        const limit = Number(payload.max_concurrent);
        if (!response.ok || !Number.isInteger(limit) || limit < 1) return;
        serverMaxConcurrent = Math.min(10, limit);
        concurrencyInput.max = String(serverMaxConcurrent);
        const selected = Math.min(serverMaxConcurrent, Math.max(1, parseInt(concurrencyInput.value) || 1));
        concurrencyInput.value = String(selected);
        concurrencyInput.title = `当前服务最多同时处理 ${serverMaxConcurrent} 个账号`;
    } catch (_) {
        // The health endpoint is advisory; keep the safe UI fallback.
    }
}

function normalizeSub2Statuses(value) {
    const entries = Array.isArray(value)
        ? value.map(item => [item?.account_id ?? item?.accountId ?? item?.id, item])
        : Object.entries(value || {});
    return new Map(entries.filter(([id, item]) => id !== undefined && id !== null && item)
        .map(([id, item]) => [String(id), normalizeSub2Status(item)]));
}

function normalizeSub2Status(value) {
    const source = value || {};
    return {
        present: Boolean(value),
        configured: source.configured !== false,
        imported: source.imported !== false,
        exists: source.exists !== false,
        sub2AccountID: String(source.sub2_account_id ?? source.sub2AccountId ?? source.id ?? ''),
        status: String(source.status || 'unknown').toLowerCase(),
        schedulable: typeof source.schedulable === 'boolean' ? source.schedulable : null,
        effectiveSchedulable: typeof source.effective_schedulable === 'boolean' ? source.effective_schedulable : null,
        errorMessage: String(source.error_message || source.errorMessage || ''),
        rateLimitedAt: source.rate_limited_at || source.rateLimitedAt || '',
        rateLimitResetAt: source.rate_limit_reset_at || source.rateLimitResetAt || '',
        overloadUntil: source.overload_until || source.overloadUntil || '',
        tempUnschedulableUntil: source.temp_unschedulable_until || source.tempUnschedulableUntil || '',
        tempUnschedulableReason: String(source.temp_unschedulable_reason || source.tempUnschedulableReason || ''),
        expiresAt: source.expires_at ?? source.expiresAt ?? '',
        associationStatus: String(source.association_status || ''),
        checkedAt: source.checked_at || source.checkedAt || '',
        stale: Boolean(source.stale),
        unknown: Boolean(source.unknown),
        error: String(source.error || '')
    };
}

function localAuthFailure(account) {
    const result = historyChecks.get(account.id)?.last_result;
    if (!result || result.freshness !== 'current') return false;
    if (Number(result.http_status) === 401) return true;
    const outcomes = ['credential_missing', 'credential_incomplete', 'access_token_expired', 'token_expired', 'token_revoked', 'credential_revoked', 'unauthorized', 'auth_failed', 'authentication_failed'];
    return [result.outcome, result.error_code, result.stream_error_code]
        .some(value => outcomes.includes(String(value || '').toLowerCase()));
}

function normalizeHistoryAccount(row, sub2Status = null) {
    const source = row || {};
    const status = String(source.status || source.account_status || 'unknown').toLowerCase();
    return {
        id: String(source.id ?? source.account_id ?? ''),
        email: String(source.email || ''),
        status,
        lastSuccessAt: source.last_success_at || source.lastSuccessAt || '',
        lastAttemptAt: source.last_attempt_at || source.lastAttemptAt || '',
        updatedAt: source.updated_at || source.updatedAt || '',
        attemptCount: Number(source.attempt_count ?? source.attemptCount ?? 0),
        lastError: String(source.last_error || source.lastError || ''),
        lastErrorCode: String(source.last_error_code || source.lastErrorCode || ''),
        lastHTTPStatus: Number(source.last_http_status || source.lastHTTPStatus || 0),
        planType: String(source.plan_type || source.planType || ''),
        expiresAt: Number(source.expires_at ?? source.expiresAt ?? 0) || 0,
        refreshToken: '',
        reloginMessage: '',
        sub2Status: sub2Status || normalizeSub2Status(null)
    };
}

function sub2StatusName(status) {
    return ({
        active: '正常', available: '正常', enabled: '正常',
        error: '异常', inactive: '已禁用', disabled: '已禁用', deleted: '已删除',
        paused: '已暂停', unknown: '未知'
    }[status] || status || '未知');
}

function sub2RuntimePause(state) {
    const now = Date.now();
    const future = value => {
        const timestamp = historyTimeValue(value);
        return timestamp > now ? timestamp : 0;
    };
    const temporary = future(state.tempUnschedulableUntil);
    if (temporary) return { kind: 'temporary', label: `临时暂停至 ${formatHistoryTime(temporary)}`, reason: state.tempUnschedulableReason };
    const rateLimit = future(state.rateLimitResetAt);
    if (rateLimit) return { kind: 'rate_limit', label: `限流至 ${formatHistoryTime(rateLimit)}`, reason: 'Sub2 返回 429 限流窗口' };
    const overload = future(state.overloadUntil);
    if (overload) return { kind: 'overload', label: `过载至 ${formatHistoryTime(overload)}`, reason: 'Sub2 暂时过载' };
    const expiry = historyTimeValue(state.expiresAt);
    if (expiry && expiry <= now) return { kind: 'expired', label: 'Sub2 凭据已过期', reason: 'Sub2 账号凭据有效期已到' };
    return null;
}

function renderSub2Status(account) {
    const state = account.sub2Status || normalizeSub2Status(null);
    if (!historySub2Configured) return '<span class="history-status sub2-unknown">Sub2 未配置</span>';
    if (!state.present) return '<span class="history-status sub2-unknown">Sub2 未找到关联</span>';
    if (!state.configured) return '<span class="history-status sub2-unknown">Sub2 未配置</span>';
    const association = { unmatched: '未找到关联', ambiguous: '多条匹配待确认', conflict: '身份不一致' }[state.associationStatus];
    if (association) return `<span class="history-status sub2-unknown" title="${escapeHTML(state.error || '')}">Sub2 ${association}</span>`;
    if (state.stale || state.unknown) return `<span class="history-status sub2-unknown"${state.errorMessage || state.error ? ` title="${escapeHTML(state.errorMessage || state.error)}"` : ''}>Sub2 状态未知${state.checkedAt ? ` · 同步 ${formatHistoryTime(state.checkedAt)}` : ''}</span>`;
    if (!state.imported && !state.sub2AccountID) return '<span class="history-status sub2-unknown">Sub2 未找到关联</span>';
    if (!state.exists) return '<span class="history-status sub2-error">Sub2 已删除</span>';
    const status = state.status;
    const conflict = localAuthFailure(account) && ['active', 'available', 'enabled'].includes(status);
    const runtimePause = sub2RuntimePause(state);
    const effective = state.effectiveSchedulable ?? state.schedulable;
    const className = conflict || runtimePause || effective === false ? 'sub2-warning' : status === 'active' || status === 'available' || status === 'enabled' ? 'sub2-ok'
        : status === 'unknown' || status === 'stale' ? 'sub2-unknown' : 'sub2-error';
    const schedule = effective === true ? '可调度' : effective === false ? '暂不可调度' : '';
    const checked = state.checkedAt ? ` · 同步 ${formatHistoryTime(state.checkedAt)}` : '';
    const reason = state.errorMessage || runtimePause?.reason || state.error;
    const title = reason ? ` title="${escapeHTML(reason)}"` : '';
    const label = runtimePause ? runtimePause.label : conflict ? '正常 · 本地凭据失效待处理' : escapeHTML(sub2StatusName(status));
    const suffix = conflict && runtimePause ? ' · 本地凭据失效待处理' : '';
    return `<span class="history-status ${className}"${title}>Sub2 ${label}${suffix}${schedule ? ` · ${schedule}` : ''}${checked}</span>`;
}

function historyStatusName(status) {
    return {
        success: '最近登录成功',
        active: '最近登录成功',
        running: '登录中',
        pending: '等待处理',
        interrupted: '上次中断',
        error: '登录失败',
        failed: '登录失败',
        deleted: '账号已删除',
        deactivated: '账号已停用',
        deleted_or_deactivated: '账号已删除或停用',
        unknown: '暂无状态'
    }[status] || status || '暂无状态';
}

const historyRecoveryNames = {
    queued: '等待恢复', validating: '正在核对账号', disabling_schedule: '正在暂停调度', logging_in: '正在重新登录',
    login_succeeded: '重新登录成功', identity_verified: '身份已核对', refreshing_credentials: '正在更新凭据',
    applying_credentials: '正在写回 Sub2', credentials_applied: '凭据已写回 Sub2', enabling_schedule: '正在恢复调度',
    completed: '恢复完成', success: '恢复完成', failed: '恢复失败', unknown: '恢复结果待核对', canceled: '恢复已取消'
};

function historyRecoveryActive(task) {
    return Boolean(task && Object.hasOwn(historyRecoveryNames, task.state) && !['completed', 'success', 'failed', 'unknown', 'canceled'].includes(task.state));
}

function historyRecoveryAction(account) {
    const state = account.sub2Status;
    const task = historyRecoveries.get(account.id);
    const resumable = task?.resumable && ['failed', 'unknown'].includes(task.state);
    const checking = ['queued', 'running'].includes(historyChecks.get(account.id)?.latest_task?.state);
    const linked = state.present && (state.imported || Boolean(state.sub2AccountID));
    let reason = '';
    if (!historySub2Configured || !linked) reason = '尚未关联 Sub2 账号';
    else if (state.stale || state.unknown) reason = 'Sub2 状态未知，请刷新后重试';
    else if (!state.exists) reason = '该账号已从 Sub2 删除';
    else if (['inactive', 'disabled', 'deleted'].includes(state.status)) reason = 'Sub2 账号已禁用或删除，请先在 Sub2 中确认';
    else if (sub2RuntimePause(state) && !resumable && !localAuthFailure(account)) reason = sub2RuntimePause(state).label;
    else if (!resumable && ['active', 'available', 'enabled'].includes(state.status) && state.schedulable === false) reason = '账号已人工暂停，保持暂停状态';
    else if (account.status === 'deleted') reason = '该账号已删除';
    else if (account.status === 'running') reason = '账号正在登录';
    if (!reason && !resumable && !localAuthFailure(account) && state.status !== 'error') reason = ['active', 'available', 'enabled'].includes(state.status)
        ? 'Sub2 正常，无需恢复' : '当前 Sub2 状态不支持自动恢复，请先核对';
    const active = historyRecoveryRequests.has(account.id) || checking || historyRecoveryActive(task);
    return {
        visible: linked || Boolean(task),
        disabled: active || Boolean(reason),
        label: historyRecoveryRequests.has(account.id) ? '提交中…' : checking ? '检测中…'
            : historyRecoveryActive(task) ? '恢复中…' : resumable ? '继续恢复' : '检测并恢复',
        reason: reason || (resumable ? '继续已保存的恢复任务，完成凭据写回与调度恢复' : '先检测当前凭据，确认失效后重新登录并恢复 Sub2 调度'),
        resume: Boolean(resumable)
    };
}

function renderHistoryRecovery(account) {
    const summary = historyChecks.get(account.id);
    const latest = summary?.latest_task;
    const result = summary?.last_result;
    const recovery = historyRecoveries.get(account.id);
    const lines = [];
    if (['queued', 'running'].includes(latest?.state)) lines.push(latest.state === 'queued' ? '检测已排队，等待确认凭据状态' : '正在检测当前凭据');
    else if (latest && latest.state !== 'finished') {
        const names = { skipped: '检测已跳过', canceled: '检测已取消', interrupted: '检测中断' };
        const reasons = { account_removed: '账号已删除', no_longer_imported: '不再符合检测条件', login_in_progress: '账号正在登录', cooldown: '检测冷却中' };
        const reason = latest.message || reasons[latest.skip_reason];
        lines.push(`${names[latest.state] || '检测状态待确认'}${reason ? `：${reason}` : ''}`);
    }
    if (result && !['queued', 'running'].includes(latest?.state)) {
        const names = { ok: result.freshness === 'stale' ? '上次凭据调用正常' : '当前凭据调用正常，无需重新登录', unauthorized: '凭据鉴权失败', credential_revoked: '凭据已失效',
            access_token_expired: '访问令牌已过期', credential_missing: '缺少访问令牌', credential_incomplete: '凭据信息不完整',
            account_disabled: '账号已停用', account_deleted: '账号已删除', rate_limited: '请求限流，暂不重新登录',
            quota_exhausted: '额度不足，暂不重新登录', network_error: '网络异常，未确认凭据失效',
            proxy_error: '代理异常，未确认凭据失效', timeout: '检测超时，未确认凭据失效' };
        const resultText = names[result.outcome] || result.message || '检测结束，详见检测详情';
        lines.push(`检测：${resultText}${result.http_status ? `（HTTP ${result.http_status}）` : ''}${result.freshness === 'stale' ? ' · 旧凭据结果' : ''}${result.finished_at ? ` · ${formatHistoryTime(result.finished_at)}` : ''}`);
    }
    if (recovery) lines.push(`${historyRecoveryNames[recovery.state] || '恢复状态待确认'}${recovery.last_error ? `：${recovery.last_error}` : ''}${recovery.updated_at ? ` · ${formatHistoryTime(recovery.updated_at)}` : ''}`);
    if (historyRecoveryNotices.has(account.id)) lines.push(historyRecoveryNotices.get(account.id));
    return lines.map(line => `<div class="history-task-status">${escapeHTML(line)}</div>`).join('');
}

async function checkAndRecoverHistory(id) {
    const key = String(id), account = historyAccounts.find(item => item.id === key);
    if (!account || historyActionsLocked()) return;
    const action = historyRecoveryAction(account);
    if (action.disabled) return;
    historyRecoveryRequests.add(key);
    historyRecoveryNotices.delete(key);
    renderHistory();
    try {
        const response = await fetch(action.resume ? '/api/account-recovery' : '/api/account-recovery/check', {
            method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
            body: JSON.stringify({ account_id: Number(key) }), signal: AbortSignal.timeout(30000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok || payload.success === false) throw new Error(payload.message || `提交失败（HTTP ${response.status}）`);
        await loadHistory();
        historyRefreshAt = Date.now() + 5000;
    } catch (error) {
        historyRecoveryNotices.set(key, error.name === 'TimeoutError' || error instanceof TypeError
            ? '提交结果暂未确认，请刷新核对检测与恢复进度。' : error.message || '检测恢复提交失败');
        historyRefreshAt = Date.now() + 5000;
    } finally {
        historyRecoveryRequests.delete(key);
        renderHistory();
    }
}

function formatHistoryTime(value) {
    if (!value) return '暂无';
    if (String(value).startsWith('0001-01-01')) return '暂无';
    const numeric = typeof value === 'number' || /^\d+$/.test(String(value)) ? Number(value) : NaN;
    const date = Number.isFinite(numeric)
        ? new Date(numeric < 1e12 ? numeric * 1000 : numeric)
        : new Date(value);
    if (Number.isNaN(date.getTime())) return escapeHTML(value);
    return escapeHTML(date.toLocaleString('zh-CN', { dateStyle: 'short', timeStyle: 'short' }));
}

function getFilteredHistoryAccounts() {
    const query = historySearchInput.value.trim().toLowerCase();
    const filter = historyStatusFilter.value;
    const filtered = historyAccounts.filter(account => {
        if (!account.email.toLowerCase().includes(query)) return false;
        if (filter === 'all') return true;
        if (filter === 'available') return ['active', 'success'].includes(account.status);
        if (filter === 'failed') return ['error', 'failed'].includes(account.status);
        if (filter === 'deactivated') return ['deleted', 'deactivated', 'deleted_or_deactivated'].includes(account.status);
        if (filter === 'processing') return ['running', 'pending'].includes(account.status);
        if (filter === 'expired') return historyExpiryState(account).kind === 'expired';
        return !['active', 'success', 'error', 'failed', 'deleted', 'deactivated', 'deleted_or_deactivated', 'running', 'pending'].includes(account.status);
    });
    return filtered.sort(compareHistoryAccounts);
}

function historyTimeValue(value) {
    if (!value) return 0;
    const numeric = typeof value === 'number' || /^\d+$/.test(String(value)) ? Number(value) : NaN;
    const time = Number.isFinite(numeric)
        ? (numeric < 1e12 ? numeric * 1000 : numeric)
        : new Date(value).getTime();
    return Number.isFinite(time) ? time : 0;
}

function historyExpiryEpoch(account) {
    const value = Number(account.expiresAt || 0);
    if (!Number.isFinite(value) || value <= 0) return 0;
    return value < 1e12 ? value * 1000 : value;
}

function historyExpiryState(account) {
    // expires_at is the OAuth access-token exp claim. It does not describe
    // the account lifetime or the refresh-token lifetime.
    const expiry = historyExpiryEpoch(account);
    if (!expiry) return { kind: 'unknown', label: '' };
    const now = Date.now();
    if (expiry <= now) return { kind: 'expired', label: '访问令牌已过期' };
    if (expiry <= now + 7 * 24 * 60 * 60 * 1000) return { kind: 'soon', label: '访问令牌 7 天内到期' };
    return { kind: 'valid', label: `访问令牌有效至 ${formatHistoryTime(expiry)}` };
}

function compareHistoryAccounts(left, right) {
    const sort = historySort.value;
    if (sort === 'email_asc') return left.email.localeCompare(right.email, 'zh-CN');
    if (sort === 'attempt_desc') return Number(right.attemptCount || 0) - Number(left.attemptCount || 0);
    if (sort === 'expires_asc') {
        const leftExpiry = historyExpiryEpoch(left) || Number.MAX_SAFE_INTEGER;
        const rightExpiry = historyExpiryEpoch(right) || Number.MAX_SAFE_INTEGER;
        return leftExpiry - rightExpiry;
    }
    if (sort === 'last_success_desc') return historyTimeValue(right.lastSuccessAt) - historyTimeValue(left.lastSuccessAt);
    return historyTimeValue(right.updatedAt) - historyTimeValue(left.updatedAt) || Number(right.id) - Number(left.id);
}

function getHistoryPageCount(total) {
    return Math.max(1, Math.ceil(total / historyPageSize));
}

function getHistoryPageAccounts(visibleAccounts) {
    const pageCount = getHistoryPageCount(visibleAccounts.length);
    historyPage = Math.min(Math.max(1, historyPage), pageCount);
    const start = (historyPage - 1) * historyPageSize;
    return visibleAccounts.slice(start, start + historyPageSize);
}

function canBatchRelogin(account) {
    return ['failed', 'error', 'interrupted', 'deactivated', 'deleted_or_deactivated'].includes(account.status);
}

function historyImportState(account) {
    const item = historyImports.get(String(account.id));
    return item ? String(item.state || 'unknown').toLowerCase() : '';
}

function canImportToSub2(account) {
    if (!historySub2Configured || !historyImportsAvailable || !account || account.status !== 'active') return false;
    return !['queued', 'sending', 'confirming', 'imported', 'unknown'].includes(historyImportState(account));
}

function historyImportStateName(state) {
    return ({ queued: '排队中', sending: '导入中', confirming: '核对中', imported: '已导入未分组', failed: '导入失败', unknown: '待核对' }[state] || '未导入');
}

function historyActionsLocked() {
    return historyLoading || historyBatchRunning || processing || Boolean(historyLoadError);
}

function updateHistoryControls(visibleAccounts, pageAccounts) {
    // Capture before disabling buttons, which can move browser focus to body.
    const actionBarHadFocus = historyActionBar.contains(document.activeElement);
    const pageCount = getHistoryPageCount(visibleAccounts.length);
    const selected = historyAccounts.filter(account => historySelectedIDs.has(account.id));
    const selectedOnPage = pageAccounts.filter(account => historySelectedIDs.has(account.id)).length;
    const hiddenCount = selected.length - selectedOnPage;
    const reloginCount = selected.filter(canBatchRelogin).length;
    const importCount = selected.filter(canImportToSub2).length;
    const checkCandidates = selected.length
        ? selected.filter(account => historyChecks.get(account.id)?.eligible && account.sub2Status.present && account.sub2Status.exists).length
        : visibleAccounts.filter(account => historyChecks.get(account.id)?.eligible && account.sub2Status.present && account.sub2Status.exists).length;
    const locked = historyActionsLocked();
    historySelectAll.checked = !historyLoadError && pageAccounts.length > 0 && selectedOnPage === pageAccounts.length;
    historySelectAll.indeterminate = !historyLoadError && selectedOnPage > 0 && selectedOnPage < pageAccounts.length;
    historySelectAll.disabled = locked || pageAccounts.length === 0;
    historyClearSelectionBtn.disabled = locked || selected.length === 0;
    historyBatchReloginBtn.disabled = locked || reloginCount === 0;
    historyBatchReloginBtn.textContent = `批量复活${reloginCount ? ` (${reloginCount})` : ''}`;
    historyBatchImportBtn.disabled = locked || importCount === 0;
    historyBatchImportBtn.textContent = `导入 Sub2${importCount ? ` (${importCount})` : ''}`;
    historyBatchImportBtn.title = !historySub2Configured ? 'Sub2 未配置' : !historyImportsAvailable
        ? '导入状态读取失败，请刷新后重试' : '将所选可导入账号加入 Sub2 未分组';
    historyBatchDeleteBtn.disabled = locked || selected.length === 0;
    historyBatchDeleteBtn.textContent = `批量删除${selected.length ? ` (${selected.length})` : ''}`;
    if (historyCheckNowBtn) {
        historyCheckNowBtn.disabled = locked || checkCandidates === 0;
        historyCheckNowBtn.textContent = selected.length ? `立即检测 (${checkCandidates})` : `立即检测全部 (${checkCandidates})`;
        historyCheckNowBtn.title = selected.length ? '检测当前选中且已关联 Sub2 的账号' : '按当前筛选条件检测已关联 Sub2 的账号';
    }
    if (historyCheckNowStatus && !checkCandidates) historyCheckNowStatus.textContent = selected.length ? '选中账号没有可检测项' : '当前筛选没有可检测项';
    historySelectionStatus.textContent = `已选 ${selected.length} 个${hiddenCount ? ` · ${hiddenCount} 个不在当前页` : ''}`;
    historyPageInfo.textContent = visibleAccounts.length
        ? `第 ${historyPage} / ${pageCount} 页 · ${visibleAccounts.length} 个账号`
        : '暂无账号';
    historyPrevPageBtn.disabled = locked || historyPage <= 1;
    historyNextPageBtn.disabled = locked || historyPage >= pageCount;
    historyPageSizeSelect.disabled = locked;
    historySearchInput.disabled = historyLoading || historyBatchRunning || processing;
    historyStatusFilter.disabled = locked;
    historySort.disabled = locked;
    refreshHistoryBtn.disabled = historyLoading || historyBatchRunning || processing;
    startBtn.disabled = processing || historyBatchRunning;
    retryFailedBtn.disabled = processing || historyBatchRunning || !accounts.some(account => account.status === 'error');
    if (historyBatchRunning && historyBatchStats) {
        const stats = historyBatchStats;
        historyBatchStatus.textContent = `${stats.action}：${stats.done} / ${stats.total}，成功 ${stats.success}，失败 ${stats.failed}${stats.skipped ? `，跳过 ${stats.skipped}` : ''}`;
    } else {
        historyBatchStatus.textContent = historyBatchSummary || (!historyImportsAvailable ? '导入状态读取失败，请刷新后重试。' : '');
    }
    historyBatchStatus.hidden = !historyBatchStatus.textContent;
    // Drive action bar visibility
    updateActionBar(selected.length, actionBarHadFocus);
    // Drive history tab badge (total account count)
    const _badge = document.getElementById('tabBadgeHistory');
    if (_badge) _badge.textContent = historyAccounts.length ? String(historyAccounts.length) : '';
}

function renderHistory() {
    const visibleAccounts = getFilteredHistoryAccounts();
    const pageAccounts = getHistoryPageAccounts(visibleAccounts);
    const query = historySearchInput.value.trim().toLowerCase();
    const filtered = query || historyStatusFilter.value !== 'all';
    historyRefreshStatus.textContent = historyLoadError
        ? historyLoadError
        : historyLoading ? '正在加载…' : (filtered
            ? `匹配 ${visibleAccounts.length} / 共 ${historyAccounts.length} 个账号`
            : `共 ${historyAccounts.length} 个账号`);
    updateHistoryControls(visibleAccounts, pageAccounts);
    if (historyLoadError) {
        historyGrid.innerHTML = `<div class="empty-state"><div class="empty-icon">⚠️</div><p>${escapeHTML(historyLoadError)}</p></div>`;
        return;
    }
    if (!historyAccounts.length) {
        historyGrid.innerHTML = '<div class="empty-state"><div class="empty-icon">🗂️</div><p>暂无账号历史，完成一次登录后会自动记录</p></div>';
        return;
    }
    if (!visibleAccounts.length) {
        historyGrid.innerHTML = '<div class="empty-state"><div class="empty-icon">🔎</div><p>没有匹配的账号，请修改或清空搜索条件</p></div>';
        return;
    }
    historyGrid.innerHTML = pageAccounts.map(account => {
        const deleted = account.status === 'deleted';
        const deactivated = ['deactivated', 'deleted_or_deactivated'].includes(account.status);
        const busy = historyBusyID === account.id;
        const selected = historySelectedIDs.has(account.id);
        const importState = historyImportState(account);
        const externallyLinked = account.sub2Status.associationStatus === 'linked' || String(historyImports.get(account.id)?.operation_id || '').startsWith('linked-');
        const importStateText = externallyLinked && importState === 'imported' ? '已关联 Sub2' : historyImportStateName(importState);
        const importError = historyImports.get(String(account.id))?.last_error || '';
        const statusClass = escapeHTML(account.status.replace(/[^a-z_]/g, ''));
        const errorSummary = account.reloginMessage || account.lastError;
        const errorCode = account.lastErrorCode ? ` · ${escapeHTML(account.lastErrorCode)}` : '';
        const errorHTTPStatus = account.lastHTTPStatus > 0 ? ` · HTTP ${account.lastHTTPStatus}` : '';
        const expiry = historyExpiryState(account);
        const expiryBadge = expiry.label ? `<span class="history-expiry ${expiry.kind}" title="这里只表示 OAuth 访问令牌有效期，不代表账号或 Refresh Token 已失效">${escapeHTML(expiry.label)}</span>` : '';
        const recoveryAction = historyRecoveryAction(account);
        const recoveryButton = recoveryAction.visible ? `<button class="btn-secondary" type="button" data-history-action="recover" data-history-id="${escapeHTML(account.id)}" title="${escapeHTML(recoveryAction.reason)}" ${historyActionsLocked() || busy || recoveryAction.disabled ? 'disabled' : ''}>${recoveryAction.label}</button>` : '';
        const copyRefreshButton = account.refreshToken ? `
                    <button class="btn-secondary" type="button" data-history-action="copy-refresh-token" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>
                        复制 Refresh Token
                    </button>` : '';
        const meta = [
            `<span class="history-status ${statusClass}">${escapeHTML(historyStatusName(account.status))}</span>`,
            renderSub2Status(account),
            expiryBadge,
            `尝试 ${Number.isFinite(account.attemptCount) ? account.attemptCount : 0} 次`,
            account.planType ? `方案 ${escapeHTML(account.planType)}` : '',
            `最近成功 ${formatHistoryTime(account.lastSuccessAt)}`,
            `最近尝试 ${formatHistoryTime(account.lastAttemptAt || account.updatedAt)}`
        ].filter(Boolean).join('<span aria-hidden="true">·</span>');
        return `
            <article class="history-item ${selected ? 'history-selected' : ''} ${deleted ? 'history-deleted' : ''}">
                <div class="history-select">
                    <input type="checkbox" data-history-select data-history-id="${escapeHTML(account.id)}" aria-label="选择 ${escapeHTML(account.email || '未知账号')}" ${selected ? 'checked' : ''} ${historyActionsLocked() ? 'disabled' : ''}>
                </div>
                <div class="history-main">
                    <div class="history-email">${escapeHTML(account.email || '未知账号')}</div>
                    <div class="history-meta">${meta}</div>
                    ${deleted ? '<div class="history-error">⚠️ 官方已确认该账号删除；如无需保留凭据，可清理这条历史记录。</div>' : ''}
                    ${deactivated ? '<div class="history-error">⚠️ 登录返回停用或删除提示；记录仍保留，可尝试重新登录确认最新状态。</div>' : ''}
                    ${errorSummary ? `<div class="history-error">${escapeHTML(errorSummary)}${errorCode}${errorHTTPStatus}</div>` : ''}
                    ${importError ? `<div class="history-error">Sub2：${escapeHTML(importError)}</div>` : ''}
                    ${account.sub2Status.errorMessage ? `<div class="history-error">Sub2：${escapeHTML(account.sub2Status.errorMessage)}</div>` : ''}
                    ${renderHistoryRecovery(account)}
                </div>
                <div class="history-actions">
                    ${importState === 'imported' ? `<button class="btn-secondary" type="button" data-history-action="check" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy || account.status === 'running' ? 'disabled' : ''}>检测状态</button>` : ''}
                    ${recoveryButton}
                    <button class="btn-secondary" type="button" data-history-action="details" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>
                        详情
                    </button>
                    ${copyRefreshButton}
                    <button class="btn-secondary" type="button" data-history-action="relogin" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy || deleted ? 'disabled' : ''}>
                        ${busy ? '<span class="spinner"></span> 登录中' : '重新登录'}
                    </button>
                    <button class="btn-secondary" type="button" data-history-action="${importState === 'unknown' || importState === 'confirming' ? 'reconcile' : 'import'}" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy || !canImportToSub2(account) && !['unknown', 'confirming'].includes(importState) ? 'disabled' : ''}>
                        ${escapeHTML(importStateText)}
                    </button>
                    <button class="btn-secondary" type="button" data-history-action="delete" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>
                        ${deleted ? '清理记录' : '删除记录'}
                    </button>
                </div>
            </article>`;
    }).join('');
}

async function checkHistoryNow() {
    if (historyActionsLocked() || !window.AccountChecks?.startMany) return;
    const visible = getFilteredHistoryAccounts();
    const source = historySelectedIDs.size
        ? historyAccounts.filter(account => historySelectedIDs.has(account.id))
        : visible;
    const ids = source.filter(account => historyChecks.get(account.id)?.eligible && account.sub2Status.present && account.sub2Status.exists).map(account => account.id);
    if (!ids.length) return;
    if (historyCheckNowStatus) historyCheckNowStatus.textContent = `正在提交 ${ids.length} 个账号的检测…`;
    historyCheckNowBtn.disabled = true;
    try {
        const submitted = await window.AccountChecks.startMany(ids);
        if (historyCheckNowStatus) historyCheckNowStatus.textContent = submitted
            ? `已提交 ${ids.length} 个，进度请查看检测任务页或账号行`
            : '检测未提交，请查看检测任务页提示';
    } catch (error) {
        if (historyCheckNowStatus) historyCheckNowStatus.textContent = error.message || '检测提交失败';
    } finally {
        renderHistory();
    }
}

async function deleteHistory(id) {
    await deleteHistoryAccounts([String(id)]);
}

async function deleteHistoryAccounts(ids) {
    if (historyActionsLocked()) return;
    // Snapshot immutable record IDs before confirmation; never resolve by email.
    const selected = historyAccounts.filter(account => ids.includes(account.id));
    if (!selected.length) return;
    const pageIDs = new Set(getHistoryPageAccounts(getFilteredHistoryAccounts()).map(account => account.id));
    const hiddenCount = selected.filter(account => !pageIDs.has(account.id)).length;
    const subject = selected.length === 1 ? `账号 ${selected[0].email}` : `选中的 ${selected.length} 个账号`;
    const prompt = `确认删除${subject}的 AUTH 本地凭据和登录历史吗？${hiddenCount ? `\n其中 ${hiddenCount} 个不在当前页（包含其他页或筛选外账号）。` : ''}\n\n此操作不可撤销；不会删除 Sub2 中已导入的账号。正在登录、导入中或导入待核对的账号会保留并提示原因。`;
    if (!await confirmAction({ title: '删除账号记录', message: prompt, confirmLabel: '确认删除', danger: true }) || historyActionsLocked()) return;
    historyBatchRunning = true;
    historyBatchSummary = '';
    historyBatchStats = { action: '批量删除', total: selected.length, done: 0, success: 0, failed: 0, skipped: 0 };
    renderHistory();
    const failures = [];
    try {
        for (const account of selected) {
            try {
                const response = await fetch(`/api/history/${encodeURIComponent(account.id)}`, {
                    method: 'DELETE', signal: AbortSignal.timeout(15000)
                });
                const payload = await response.json().catch(() => ({}));
                if (!response.ok && !(response.status === 404 && payload.code === 'account_not_found')) throw new Error(payload.message || `删除失败（HTTP ${response.status}）`);
                historySelectedIDs.delete(account.id);
                historyRefreshTokens.delete(account.id);
                historyImports.delete(account.id);
                historyAccounts = historyAccounts.filter(item => item.id !== account.id);
                if (historyDetailID === account.id) closeHistoryDetails();
                historyBatchStats.success += 1;
            } catch (error) {
                historyBatchStats.failed += 1;
                historySelectedIDs.add(account.id);
                const message = ['AbortError', 'TimeoutError'].includes(error.name)
                    ? '请求超时，结果待确认，请刷新核对后重试'
                    : (error.message || '请求失败，请刷新后重试');
                failures.push(`记录 #${account.id}：${message}`);
            }
            historyBatchStats.done += 1;
            renderHistory();
        }
        historyBatchSummary = `删除完成：已删除 ${historyBatchStats.success} 个，失败 ${historyBatchStats.failed} 个${failures.length ? '。失败项已保留选中，可刷新后重试。\n' + failures.join('\n') : '。'}`;
        await loadHistory();
    } finally {
        historyBatchRunning = false;
        historyBatchStats = null;
        renderHistory();
    }
}

function historyDetailIsVisible(node) {
    for (let current = node; current; current = current.parentElement) if (current.hidden) return false;
    return true;
}

function historyDetailCanRestoreFocus(trigger) {
    if (!trigger || trigger.disabled || !historyDetailIsVisible(trigger) || trigger.isConnected === false) return false;
    const id = String(trigger.dataset?.historyId || '');
    return !id || historyAccounts.some(account => String(account.id) === id);
}

function focusHistoryDetailFallback() {
    const candidates = [historySelectAll, document.getElementById('tab-history')];
    const target = candidates.find(node => node && !node.disabled && historyDetailIsVisible(node) && typeof node.focus === 'function');
    target?.focus({ preventScroll: true });
}

function closeHistoryDetails() {
    const wasOpen = !historyDetail.hidden;
    if (historyDetailAbort) historyDetailAbort.abort();
    historyDetailAbort = null;
    historyDetailID = '';
    historyDetail.hidden = true;
    const trigger = historyDetailTrigger;
    historyDetailTrigger = null;
    if (!wasOpen) return;
    if (historyDetailCanRestoreFocus(trigger)) trigger.focus({ preventScroll: true });
    else focusHistoryDetailFallback();
}

async function openHistoryDetails(id, trigger = null) {
    const account = historyAccounts.find(item => item.id === id);
    if (!account) return;
    if (historyDetailAbort) historyDetailAbort.abort();
    historyDetailTrigger = trigger && typeof trigger.focus === 'function' ? trigger : null;
    historyDetailID = String(id);
    historyDetail.hidden = false;
    historyDetailPanel.focus?.({ preventScroll: true });
    historyDetailTitle.textContent = account.email || '账号详情';
    historyDetailBody.innerHTML = '<div class="empty-state"><p>正在加载详情…</p></div>';
    const controller = new AbortController();
    let timedOut = false;
    const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 15000);
    historyDetailAbort = controller;
    try {
        const response = await fetch(`/api/history/${encodeURIComponent(id)}`, {
            headers: { 'Accept': 'application/json' },
            signal: controller.signal
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `详情加载失败（HTTP ${response.status}）`);
        if (historyDetailID !== String(id)) return;
        renderHistoryDetail(payload.account || account, Array.isArray(payload.attempts) ? payload.attempts : []);
    } catch (error) {
        if (historyDetailID !== String(id)) return;
        if (error.name === 'AbortError' && !timedOut) return;
        const message = timedOut ? '详情读取超时，请重试' : (error.message || '详情加载失败');
        historyDetailBody.innerHTML = `<div class="empty-state"><div class="empty-icon">⚠️</div><p>${escapeHTML(message)}</p></div>`;
    } finally {
        clearTimeout(timeout);
    }
}

function renderHistoryDetail(account, attempts) {
    const expiry = historyExpiryState({ expiresAt: Number(account.expires_at ?? account.expiresAt ?? 0) });
    const expiryText = expiry.label || '有效期未知';
    const plan = account.plan_type || account.planType || '未知';
    const status = String(account.status || 'unknown').toLowerCase();
    const attemptHTML = attempts.length ? attempts.map(attempt => {
        const stage = stageNames[attempt.stage] || attempt.stage || '登录';
        const state = historyStatusName(String(attempt.status || 'unknown').toLowerCase());
        const code = attempt.code ? ` · ${escapeHTML(attempt.code)}` : '';
        const http = Number(attempt.http_status || 0) > 0 ? ` · HTTP ${Number(attempt.http_status)}` : '';
        const duration = Number(attempt.duration_ms || 0) > 0 ? `${(Number(attempt.duration_ms) / 1000).toFixed(1)}s` : '—';
        return `<div class="history-attempt">
            <div class="history-attempt-header"><span>${escapeHTML(formatHistoryTime(attempt.started_at))} · ${escapeHTML(state)}</span><span>${escapeHTML(stage)} · ${duration}</span></div>
            <div class="history-attempt-message">${attempt.retryable ? '可重试' : '不可重试'}${code}${http}${attempt.message ? ` · ${escapeHTML(attempt.message)}` : ''}</div>
        </div>`;
    }).join('') : '<div class="empty-state"><p>暂无登录尝试记录</p></div>';
    historyDetailBody.innerHTML = `
        <div class="history-detail-summary">
            <div class="history-detail-stat">状态<br><strong>${escapeHTML(historyStatusName(status))}</strong></div>
            <div class="history-detail-stat">方案<br><strong>${escapeHTML(String(plan))}</strong></div>
            <div class="history-detail-stat">访问令牌<br><strong>${escapeHTML(expiryText)}</strong></div>
            <div class="history-detail-stat">尝试次数<br><strong>${Number(account.attempt_count ?? account.attemptCount ?? 0)}</strong></div>
            <div class="history-detail-stat">最近成功<br><strong>${escapeHTML(formatHistoryTime(account.last_success_at || account.lastSuccessAt))}</strong></div>
            <div class="history-detail-stat">最近尝试<br><strong>${escapeHTML(formatHistoryTime(account.last_attempt_at || account.lastAttemptAt || account.updated_at || account.updatedAt))}</strong></div>
        </div>
        <div class="history-detail-attempts"><p class="hint">列表中的有效期来自 OAuth 访问令牌；访问令牌过期后，Refresh Token 是否可用不由此字段判断，通常仍可用于重新登录。最近 ${attempts.length} 次尝试</p>${attemptHTML}</div>`;
}

async function performHistoryRelogin(id) {
    const account = historyAccounts.find(item => item.id === id);
    if (!account) return { success: false, error: '账号不存在' };
    historyRefreshTokens.delete(String(id));
    account.refreshToken = '';
    historyBusyID = id;
    account.reloginMessage = '正在重新登录…';
    renderHistory();
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 210000);
    try {
        const response = await fetch('/api/history/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
            body: JSON.stringify({ account_id: Number(id), proxy: proxyInput.value.trim() }),
            signal: controller.signal
        });
        const contentType = response.headers.get('content-type') || '';
        if (!response.ok || !contentType.includes('text/event-stream')) {
            const payload = contentType.includes('json') ? await response.json().catch(() => ({})) : {};
            throw new Error(payload.message || `重新登录失败（HTTP ${response.status}）`);
        }
        const finalData = await consumeLoginStream(response, {
            stage: '',
            stageStartedAt: null,
            message: account.reloginMessage,
            timings: {}
        });
        if (!finalData.success) {
            throw new Error(formatLoginError(finalData, { stage: '' }));
        }
        const result = finalData.Data || finalData.data || {};
        const refreshToken = getRefreshToken(result);
        if (refreshToken) historyRefreshTokens.set(id, refreshToken);
        account.reloginMessage = '登录成功，历史已更新';
        return { success: true };
    } catch (error) {
        account.reloginMessage = error.name === 'AbortError' ? '重新登录超时，请刷新后核对结果' : (error.message || '重新登录失败');
        renderHistory();
        return { success: false, error: account.reloginMessage };
    } finally {
        clearTimeout(timeout);
        historyBusyID = '';
        renderHistory();
    }
}

async function reloginHistory(id) {
    if (historyActionsLocked() || historyBusyID) return;
    processing = true;
    historyBatchSummary = '';
    startBtn.disabled = true;
    startBtn.innerHTML = '<span class="spinner"></span><span>处理中</span>';
    proxyCheckBtn.disabled = true;
    exportBtn.disabled = true;
    refreshHistoryBtn.disabled = true;
    historySearchInput.disabled = true;
    document.body.classList.add('processing');
    try {
        await performHistoryRelogin(id);
        // Refresh once so the row reflects the persisted status and a deleted
        // account disappears immediately.
        await loadHistory();
    } finally {
        processing = false;
        startBtn.disabled = false;
        startBtn.innerHTML = '<span>▶️</span><span>开始处理</span>';
        document.body.classList.remove('processing');
        proxyCheckBtn.disabled = proxyChecking;
        exportBtn.disabled = results.length === 0;
        refreshHistoryBtn.disabled = false;
        historySearchInput.disabled = false;
        renderHistory();
    }
}

async function batchReloginHistory() {
    if (historyActionsLocked()) return;
    const ids = [...historySelectedIDs].filter(id => {
        const account = historyAccounts.find(item => item.id === id);
        return account && canBatchRelogin(account);
    });
    if (!ids.length) return;
    const skipped = historySelectedIDs.size - ids.length;
    const proxyLabel = proxyInput.value.trim() ? '使用当前已配置代理' : '使用服务器默认网络配置';
    const estimate = `默认最长约 ${ids.length * 3} 分钟`;
    const summary = `将按顺序复活 ${ids.length} 个账号，${proxyLabel}，${estimate}（按单账号 3 分钟上限）。${skipped > 0 ? `另有 ${skipped} 个选中账号当前不可复活，将跳过。` : ''}\n\n是否继续？`;
    if (!await confirmAction({ title: '批量复活账号', message: summary, confirmLabel: '开始复活' }) || historyActionsLocked()) return;

    historyBatchRunning = true;
    historyBatchSummary = '';
    historyBatchStats = { action: '批量复活', total: ids.length, done: 0, success: 0, failed: 0, skipped };
    processing = true;
    startBtn.disabled = true;
    startBtn.innerHTML = '<span class="spinner"></span><span>批量复活中</span>';
    proxyCheckBtn.disabled = true;
    exportBtn.disabled = true;
    refreshHistoryBtn.disabled = true;
    historySearchInput.disabled = true;
    document.body.classList.add('processing');
    renderHistory();
    try {
        for (const id of ids) {
            if (!historyAccounts.some(account => account.id === id && canBatchRelogin(account))) {
                historyBatchStats.done += 1;
                historyBatchStats.skipped += 1;
                renderHistory();
                continue;
            }
            const result = await performHistoryRelogin(id);
            historyBatchStats.done += 1;
            if (result.success) {
                historyBatchStats.success += 1;
                historySelectedIDs.delete(id);
            }
            else historyBatchStats.failed += 1;
            renderHistory();
        }
        historyBatchSummary = `批量复活完成：成功 ${historyBatchStats.success}，失败 ${historyBatchStats.failed}${historyBatchStats.skipped ? `，跳过 ${historyBatchStats.skipped}` : ''}`;
        await loadHistory();
    } finally {
        processing = false;
        historyBatchRunning = false;
        historyBatchStats = null;
        historyBusyID = '';
        startBtn.disabled = false;
        startBtn.innerHTML = '<span>▶️</span><span>开始处理</span>';
        document.body.classList.remove('processing');
        proxyCheckBtn.disabled = proxyChecking;
        exportBtn.disabled = results.length === 0;
        refreshHistoryBtn.disabled = false;
        historySearchInput.disabled = false;
        renderHistory();
    }
}

async function importHistoryAccounts(ids, { waitForCompletion = false } = {}) {
    if (historyActionsLocked()) return;
    const uniqueIDs = [...new Set(ids.map(String))].filter(id => {
        const account = historyAccounts.find(item => item.id === id);
        return account && canImportToSub2(account);
    });
    if (!uniqueIDs.length) return;
    historyBatchRunning = true;
    historyBatchSummary = `正在提交 ${uniqueIDs.length} 个账号…`;
    renderHistory();
    let accepted = 0;
    let submitted = 0;
    const acceptedIDsForWait = [];
    if (waitForCompletion) {
        batchImportProgress = { total: uniqueIDs.length, done: 0, success: 0, failed: 0, pending: uniqueIDs.length, running: true };
        renderBatchImportStatus();
    }
    try {
        // The existing import endpoint accepts at most 100 IDs per request.
        for (let offset = 0; offset < uniqueIDs.length; offset += 100) {
            const batchIDs = uniqueIDs.slice(offset, offset + 100);
            const response = await fetch('/api/sub2/import', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
                body: JSON.stringify({ account_ids: batchIDs.map(Number) }),
                signal: AbortSignal.timeout(30000)
            });
            const payload = await response.json().catch(() => ({}));
            if (!response.ok) throw new Error(payload.message || `导入失败（HTTP ${response.status}）`);
            const acceptedIDs = new Set((Array.isArray(payload.imports) ? payload.imports : []).map(item => String(item.account_id)));
            batchIDs.forEach(id => {
                if (acceptedIDs.has(id)) { historySelectedIDs.delete(id); accepted += 1; acceptedIDsForWait.push(id); }
            });
            submitted += batchIDs.length;
            historyBatchSummary = `正在提交导入：${submitted} / ${uniqueIDs.length}，已受理 ${accepted} 个`;
            renderHistory();
        }
        if (waitForCompletion && accepted) {
            const outcome = await waitForImportCompletion(acceptedIDsForWait, 90000, {
                total: uniqueIDs.length,
                initialFailed: uniqueIDs.length - accepted
            });
            historyBatchSummary = outcome.pending ? `已受理 ${accepted} 个；${outcome.message}`
                : `Sub2 导入完成：成功 ${outcome.success} 个${outcome.failed ? `，失败 ${outcome.failed} 个` : ''}。`;
        } else if (waitForCompletion) {
            batchImportProgress = {
                total: uniqueIDs.length, done: uniqueIDs.length, success: 0,
                failed: uniqueIDs.length, pending: 0, running: false,
                error: true, message: '没有账号被 Sub2 受理'
            };
            renderBatchImportStatus();
            historyBatchSummary = `没有账号被 Sub2 受理；${uniqueIDs.length} 个未受理，已保留选中。`;
        } else {
            historyBatchSummary = accepted
                ? `已加入 Sub2 导入队列 ${accepted} 个${uniqueIDs.length > accepted ? `；${uniqueIDs.length - accepted} 个未受理，已保留选中` : ''}。`
                : `没有账号被 Sub2 受理；${uniqueIDs.length} 个未受理，已保留选中。`;
        }
    } catch (error) {
        historyBatchSummary = `已受理 ${accepted} 个；其余选中账号已保留。${error.message || '提交导入失败，请刷新后核对'}`;
        if (waitForCompletion) {
            batchImportProgress = {
                total: uniqueIDs.length,
                done: uniqueIDs.length - accepted,
                success: 0,
                failed: uniqueIDs.length - accepted,
                pending: accepted,
                running: false,
                error: true,
                message: error.message || '提交导入失败，请刷新后核对'
            };
            renderBatchImportStatus();
        }
    } finally {
        await loadHistory();
        historyBatchRunning = false;
        renderHistory();
        renderBatchImportStatus();
    }
}

async function batchImportHistory() {
    if (historyActionsLocked()) return;
    const ids = historyAccounts.filter(account => historySelectedIDs.has(account.id) && canImportToSub2(account)).map(account => account.id);
    const eligible = ids.length;
    if (!eligible) return;
    const skipped = historySelectedIDs.size - eligible;
    const message = `将 ${eligible} 个账号导入到 Sub2 未分组。${skipped ? `另有 ${skipped} 个选中账号不满足导入条件，将跳过并保留选中。` : ''}\n\n是否继续？`;
    if (!await confirmAction({ title: '导入 Sub2', message, confirmLabel: '确认导入' }) || historyActionsLocked()) return;
    await importHistoryAccounts(ids);
}

async function importLoginResultsToSub2() {
    if (processing || historyBatchRunning || results.length === 0) return;
    importSub2Btn.disabled = true;
    historyBatchRunning = true;
    batchImportProgress = null;
    renderBatchImportStatus();
    historyBatchSummary = '正在准备批量登录结果导入…';
    renderHistory();
    try {
        await loadHistory();
        if (historyLoadError) {
            const message = '账号历史读取失败，请刷新后重试';
            historyBatchSummary = message;
            batchImportProgress = { total: results.length, done: 0, success: 0, failed: 0,
                pending: results.length, running: false, error: true, message };
            renderBatchImportStatus();
            window.alert(message);
            return;
        }
        if (!historySub2Configured) {
            const message = 'Sub2 未配置，请先设置服务端 SUB2API_BASE_URL 和 SUB2API_ADMIN_API_KEY';
            historyBatchSummary = message;
            batchImportProgress = { total: results.length, done: 0, success: 0, failed: 0,
                pending: results.length, running: false, error: true, message };
            renderBatchImportStatus();
            window.alert(message);
            return;
        }
        if (!historyImportsAvailable) {
            const message = '导入状态读取失败，请刷新后重试';
            historyBatchSummary = message;
            batchImportProgress = { total: results.length, done: 0, success: 0, failed: 0,
                pending: results.length, running: false, error: true, message };
            renderBatchImportStatus();
            window.alert(message);
            return;
        }

        const accountsByEmail = new Map(historyAccounts.map(account => [account.email.toLowerCase(), account]));
        const ids = [];
        const seen = new Set();
        results.forEach(result => {
            const email = String(result.Email || result.email || '').trim().toLowerCase();
            const account = accountsByEmail.get(email);
            if (account && canImportToSub2(account) && !seen.has(account.id)) {
                seen.add(account.id);
                ids.push(account.id);
            }
        });
        if (!ids.length) {
            const message = '没有可导入的批量登录成功账号，可能已经导入 Sub2 或状态尚未可用';
            historyBatchSummary = message;
            batchImportProgress = { total: results.length, done: results.length, success: 0, failed: results.length,
                pending: 0, running: false, error: true, message };
            renderBatchImportStatus();
            window.alert(message);
            return;
        }
        const skipped = results.length - ids.length;
        const message = `将 ${ids.length} 个批量登录成功账号导入到 Sub2 未分组。${skipped ? `另有 ${skipped} 个结果已导入、不可用或未找到对应历史记录，将跳过。` : ''}\n\n是否继续？`;
        if (!await confirmAction({ title: '导入 Sub2', message, confirmLabel: '确认导入' })) return;
        historyBatchRunning = false;
        renderHistory();
        await importHistoryAccounts(ids, { waitForCompletion: true });
    } finally {
        historyBatchRunning = false;
        importSub2Btn.disabled = processing || results.length === 0;
        renderStatus();
        renderBatchImportStatus();
        renderHistory();
    }
}

async function reconcileHistoryImport(id) {
    if (historyActionsLocked()) return;
    const item = historyImports.get(String(id));
    if (!item || !item.id) return;
    historyBatchRunning = true;
    historyBatchSummary = '正在向 Sub2 核对账号…';
    renderHistory();
    try {
        const response = await fetch(`/api/sub2/import/${encodeURIComponent(item.id)}/reconcile`, {
            method: 'POST', headers: { 'Accept': 'application/json' }, signal: AbortSignal.timeout(45000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `核对未完成（HTTP ${response.status}）`);
        historyBatchSummary = '导入状态已更新。';
        await loadHistory();
    } catch (error) {
        historyBatchSummary = error.message || '核对失败';
    } finally {
        historyBatchRunning = false;
        renderHistory();
    }
}

async function runAccounts(targetAccounts) {
    if (processing || historyBatchRunning || targetAccounts.length === 0) return;
    processing = true;
    startBtn.disabled = true;
    startBtn.innerHTML = '<span class="spinner"></span> 处理中';
    document.body.classList.add('processing');
    proxyCheckBtn.disabled = true;
    importSub2Btn.disabled = true;
    exportBtn.disabled = true;
    refreshHistoryBtn.disabled = true;
    const proxy = proxyInput.value.trim();
    // A previous batch may have hit the IP limit. Do not carry its local
    // countdown into a new batch; the server remains the source of truth.
    platformPauseUntil = 0;
    for (const account of targetAccounts) {
        account.status = 'pending';
        account.message = '等待处理';
        account.duration = '';
        account.stage = '';
        account.stageStartedAt = null;
        account.timings = {};
    }
    renderHistory();
    renderStatus();
    updateStats();
    const elapsedTimer = setInterval(renderStatus, 1000);

    try {
        if (modeSelect.value === 'sequential') {
            await processSequential(targetAccounts, proxy);
        } else {
            await processParallel(targetAccounts, proxy);
        }
    } finally {
        clearInterval(elapsedTimer);
        processing = false;
        startBtn.disabled = false;
        startBtn.innerHTML = '<span>▶️</span><span>开始处理</span>';
        document.body.classList.remove('processing');
        proxyCheckBtn.disabled = proxyChecking;
        exportBtn.disabled = results.length === 0;
        refreshHistoryBtn.disabled = false;
        await loadHistory();
        importSub2Btn.disabled = results.length === 0;
        renderStatus();
        updateStats();
    }
}

async function processSequential(targetAccounts, proxy) {
    for (const account of targetAccounts) {
        await processAccount(account, proxy);
    }
}

async function processParallel(targetAccounts, proxy) {
    const concurrency = Math.min(serverMaxConcurrent, 10, Math.max(1, parseInt(concurrencyInput.value) || 1));
    const queue = [...targetAccounts];
    const workers = [];

    for (let i = 0; i < concurrency; i++) {
        workers.push(async () => {
            while (queue.length > 0) {
                const account = queue.shift();
                if (account) {
                    await processAccount(account, proxy);
                }
            }
        });
    }

    await Promise.all(workers.map(w => w()));
}

async function processAccount(account, proxy) {
    account.status = 'processing';
    account.message = '正在登录...';
    account.startTime = Date.now();
    renderStatus();
    updateStats();

    try {
        const data = await loginAccount(account, proxy);
        account.timings = data.timings || {};

        if (data.success) {
            account.status = 'success';
            account.message = '登录成功 ✨';
            account.result = data.data;
            results.push({
                email: account.email,
                ...data.data
            });
        } else {
            throw new Error(formatLoginError(data, account));
        }
    } catch (error) {
        account.status = 'error';
        account.message = error.message;
    }

    account.endTime = Date.now();
    account.duration = ((account.endTime - account.startTime) / 1000).toFixed(1) + 's';
    renderStatus();
    updateStats();
}

async function loginAccount(account, proxy) {
    let response;
    for (let attempt = 0; attempt < 4; attempt++) {
        while (Date.now() < platformPauseUntil) {
            account.stage = 'queued';
            account.message = '平台限流，' + Math.ceil((platformPauseUntil - Date.now()) / 1000) + ' 秒后继续';
            renderStatus();
            await new Promise(resolve => setTimeout(resolve, Math.min(1000, platformPauseUntil - Date.now())));
        }
        response = await fetch('/api/login/stream', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                email: account.email,
                password: account.password,
                totp_secret: account.totp_secret,
                proxy
            })
        });
        const contentType = response.headers.get('content-type') || '';
        if (response.ok && contentType.includes('text/event-stream')) break;
        const data = contentType.includes('json') ? await response.json() : {};
        if (response.status === 429 && ['platform_rate_limit', 'platform_busy'].includes(data.code) && attempt < 3) {
            const delay = Number(data.retry_after || response.headers.get('retry-after'));
            if (Number.isFinite(delay) && delay > 0 && delay <= 600) {
                if (data.code === 'platform_rate_limit') {
                    // This limit is shared by the client IP, so pause all
                    // workers together. A full login slot is request-local;
                    // pausing every worker for it creates a retry storm.
                    platformPauseUntil = Math.max(platformPauseUntil, Date.now() + delay * 1000);
                } else {
                    account.stage = 'queued';
                    account.message = '服务并发已满，' + delay + ' 秒后继续';
                    renderStatus();
                    await new Promise(resolve => setTimeout(resolve, delay * 1000));
                }
                continue;
            }
        }
        throw new Error(data.message || '登录请求失败（HTTP ' + response.status + '）');
    }
    if (!response.body) {
        throw new Error('实时进度连接不可用');
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let finalData = null;
    const consume = (block) => {
        const line = block.split(/\r?\n/).find(line => line.startsWith('data:'));
        if (!line) return;
        const event = JSON.parse(line.slice(5).trim());
        if (event.type === 'progress') {
            const stage = event.stage || '';
            const stageMS = Number.isFinite(event.stage_ms) && event.stage_ms >= 0 ? event.stage_ms : 0;
            const startedAt = Date.now() - stageMS;
            account.stageStartedAt = account.stage !== stage || account.stageStartedAt == null
                ? startedAt : Math.min(account.stageStartedAt, startedAt);
            account.stage = stage;
            account.message = event.message || '正在处理...';
            renderStatus();
            updateStats();
        } else if (event.type === 'result') {
            finalData = event;
        }
    };
    try {
        while (!finalData) {
            const { value, done } = await reader.read();
            buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
            const blocks = buffer.split(/\r?\n\r?\n/);
            buffer = blocks.pop() || '';
            blocks.forEach(consume);
            if (buffer.length > 2 * 1024 * 1024) throw new Error('实时进度响应格式异常');
            if (done) break;
        }
    } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
    }
    if (!finalData) {
        throw new Error('实时进度连接意外中断');
    }
    return finalData;
}

// Consume the SSE response used by a history-account re-login. The normal
// batch path keeps its richer progress bookkeeping; this helper only updates
// the history row while preserving the same result/error payload.
async function consumeLoginStream(response, account) {
    if (!response.body) throw new Error('实时进度连接不可用');
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let finalData = null;
    const consume = block => {
        const line = block.split(/\r?\n/).find(item => item.startsWith('data:'));
        if (!line) return;
        let event;
        try {
            event = JSON.parse(line.slice(5).trim());
        } catch (_) {
            throw new Error('实时进度响应格式异常');
        }
        if (event.type === 'progress') {
            account.stage = event.stage || account.stage || '';
            account.message = event.message || '正在处理…';
            const row = historyAccounts.find(item => item.id === historyBusyID);
            if (row) row.reloginMessage = account.message;
            renderHistory();
        } else if (event.type === 'result') {
            finalData = event;
        }
    };
    try {
        while (!finalData) {
            const { value, done } = await reader.read();
            buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
            const blocks = buffer.split(/\r?\n\r?\n/);
            buffer = blocks.pop() || '';
            for (const block of blocks) consume(block);
            if (buffer.length > 2 * 1024 * 1024) throw new Error('实时进度响应格式异常');
            if (done) break;
        }
    } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
    }
    if (!finalData) throw new Error('实时进度连接意外中断');
    return finalData;
}

function formatLoginError(data, account) {
    if (!data.error) return data.message || '登录失败';
    const detail = data.error;
    const stage = detail.stage === 'unknown' ? account.stage : detail.stage;
    const parts = [stageNames[stage] || '登录'];
    const accountStatusNames = {
        deleted: '账号已删除',
        deactivated: '账号已停用',
        deleted_or_deactivated: '账号已删除或停用'
    };
    if (accountStatusNames[detail.account_status]) parts.unshift(accountStatusNames[detail.account_status]);
    if (detail.http_status) parts.push('HTTP ' + detail.http_status);
    if (detail.code) parts.push(detail.code);
    return parts.join(' · ') + '\n' + (detail.message || data.message || '登录失败');
}

function renderStatus() {
    if (accounts.length === 0) {
        if (statusGrid.dataset.signature !== 'empty') {
            statusGrid.innerHTML = '<div class="empty-state"><div class="empty-icon">📝</div><p>在左侧输入账号信息后开始处理</p></div>';
            statusRows.clear();
            statusGrid.dataset.signature = 'empty';
        }
        return;
    }

    const signature = JSON.stringify(accounts.map(account => String(account.id)));
    if (statusGrid.dataset.signature !== signature) {
        statusGrid.innerHTML = accounts.map(account => `
            <div class="status-item" data-account-id="${escapeHTML(account.id)}">
                <div class="status-icon" data-status-role="icon">
                </div>
                <div class="status-info">
                    <div class="status-email" data-status-role="email"></div>
                    <div class="status-message" data-status-role="message"></div>
                    <div class="status-message" data-status-role="stage" hidden></div>
                    <div class="status-progress" data-status-role="progress-container" hidden>
                        <div class="status-progress-track">
                            <div class="status-progress-bar" data-status-role="progress-bar" role="progressbar" aria-label="阶段进度（估计）" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"></div>
                        </div>
                        <div class="status-progress-label" data-status-role="progress-label"></div>
                    </div>
                    <div class="status-message" data-status-role="timings" hidden></div>
                </div>
                <div class="status-time" data-status-role="time"></div>
                <div class="status-actions" data-status-role="actions"></div>
            </div>
        `).join('');
        statusRows.clear();
        statusGrid.querySelectorAll('.status-item').forEach(row => {
            statusRows.set(row.dataset.accountId, {
                row,
                icon: row.querySelector('[data-status-role="icon"]'),
                email: row.querySelector('[data-status-role="email"]'),
                message: row.querySelector('[data-status-role="message"]'),
                stage: row.querySelector('[data-status-role="stage"]'),
                progressContainer: row.querySelector('[data-status-role="progress-container"]'),
                progressBar: row.querySelector('[data-status-role="progress-bar"]'),
                progressLabel: row.querySelector('[data-status-role="progress-label"]'),
                timings: row.querySelector('[data-status-role="timings"]'),
                time: row.querySelector('[data-status-role="time"]'),
                actions: row.querySelector('[data-status-role="actions"]'),
                actionsKey: ''
            });
        });
        statusGrid.dataset.signature = signature;
    }

    accounts.forEach(account => updateStatusRow(account, statusRows.get(String(account.id))));
}

function renderBatchImportStatus() {
    if (!batchImportStatus) return;
    if (!batchImportProgress) {
        batchImportStatus.hidden = true;
        batchImportStatus.textContent = '';
        return;
    }
    const p = batchImportProgress;
    const terminal = p.failed === 0 && p.pending === 0;
    const prefix = p.error ? 'Sub2 导入未完成' : p.running ? '正在同步到 Sub2' : terminal ? 'Sub2 导入完成' : p.failed ? 'Sub2 导入部分失败' : 'Sub2 导入仍在处理';
    batchImportStatus.textContent = `${prefix}：${p.done} / ${p.total}，成功 ${p.success}，${p.failed ? `失败 ${p.failed}` : '失败 0'}${p.pending ? `，处理中 ${p.pending}` : ''}${p.message ? ` · ${p.message}` : ''}`;
    batchImportStatus.hidden = false;
}

async function waitForImportCompletion(ids, timeoutMs = 90000, { total = ids.length, initialFailed = 0 } = {}) {
    const wanted = new Set(ids.map(String));
    const started = Date.now();
    let last = null;
    while (Date.now() - started < timeoutMs) {
        await loadHistory();
        if (historyLoadError) {
            last = { total, done: initialFailed, success: 0, failed: initialFailed,
                pending: wanted.size, running: false, error: true,
                message: '历史读取失败，请刷新后重试' };
            batchImportProgress = last;
            renderBatchImportStatus();
            return last;
        }
        const states = [...wanted].map(id => String(historyImports.get(id)?.state || 'unknown').toLowerCase());
        const terminal = states.filter(state => ['imported', 'completed', 'failed', 'error', 'rejected'].includes(state));
        const success = terminal.filter(state => ['imported', 'completed'].includes(state)).length;
        const failed = terminal.filter(state => ['failed', 'error', 'rejected'].includes(state)).length;
        const pending = states.length - terminal.length;
        last = { total, done: initialFailed + terminal.length, success, failed: initialFailed + failed,
            pending, running: pending > 0 };
        batchImportProgress = last;
        renderBatchImportStatus();
        if (!pending) return last;
        await new Promise(resolve => setTimeout(resolve, 2500));
    }
    const result = last || { total, done: initialFailed, success: 0, failed: initialFailed,
        pending: wanted.size, running: false };
    result.running = false;
    result.message = '部分任务仍在队列中，请稍后刷新历史';
    batchImportProgress = result;
    renderBatchImportStatus();
    return result;
}

function updateStatusRow(account, state) {
    if (!state) return;
    const icons = { pending: '⏳', processing: '⚡', success: '✅', error: '❌' };
    const status = icons[account.status] ? account.status : 'pending';
    state.icon.className = `status-icon ${status}`;
    state.icon.textContent = icons[status];
    state.email.textContent = account.email || '';
    state.message.textContent = account.message || '';

    const isProcessing = account.status === 'processing';
    state.stage.hidden = !isProcessing;
    state.progressContainer.hidden = !isProcessing;
    if (isProcessing) {
        const stageName = stageNames[account.stage] || account.stage || '登录';
        const elapsed = account.stageStartedAt == null ? 0 : Math.max(0, (Date.now() - account.stageStartedAt) / 1000);
        const progress = getEstimatedStageProgress(account, elapsed);
        state.stage.textContent = `当前步骤：${stageName} · ${elapsed.toFixed(1)}s`;
        state.progressBar.style.width = `${progress}%`;
        state.progressBar.setAttribute('aria-valuenow', String(progress));
        state.progressLabel.textContent = `阶段进度（估计） ${progress}%`;
    } else {
        state.progressBar.style.width = '0%';
        state.progressBar.setAttribute('aria-valuenow', '0');
        state.progressLabel.textContent = '';
    }

    const timings = account.timings && Object.keys(account.timings).length
        ? Object.entries(account.timings).map(([stage, ms]) => `${stageNames[stage] || stage} ${(ms / 1000).toFixed(1)}s`).join(' · ')
        : '';
    state.timings.hidden = !timings;
    state.timings.textContent = timings;
    state.time.textContent = isProcessing
        ? `${((Date.now() - (account.startTime || Date.now())) / 1000).toFixed(1)}s`
        : account.duration || '-';

    const actionsKey = `${account.status}|${account.result ? 'result' : ''}|${processing ? 'processing' : 'idle'}`;
    if (state.actionsKey !== actionsKey) {
        state.actions.innerHTML = `${account.status === 'error' ? `
            <button class="btn-secondary" onclick="retryAccount(${account.id})" ${processing ? 'disabled' : ''}>重试</button>
        ` : ''}${account.result ? `
            <button class="btn-icon" onclick="copyToken(${account.id})" title="复制 Refresh Token">📋</button>
        ` : ''}`;
        state.actionsKey = actionsKey;
    }
}

function getEstimatedStageProgress(account, elapsedSeconds) {
    const stage = account.stage || 'queued';
    const base = stageProgressEstimates[stage] ?? 50;
    const index = stageProgressOrder.indexOf(stage);
    const next = index >= 0 && index + 1 < stageProgressOrder.length
        ? stageProgressEstimates[stageProgressOrder[index + 1]]
        : Math.min(95, base + 10);
    const span = Math.max(1, next - base);
    return Math.min(95, Math.max(1, Math.round(base + Math.min(span - 1, elapsedSeconds / 30 * span))));
}

function escapeHTML(value) {
    return String(value ?? '').replace(/[&<>'"]/g, character => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'
    })[character]);
}

function updateStats() {
    totalCount.textContent = accounts.length;
    successCount.textContent = accounts.filter(a => a.status === 'success').length;
    failedCount.textContent = accounts.filter(a => a.status === 'error').length;
    pendingCount.textContent = accounts.filter(a => a.status === 'pending' || a.status === 'processing').length;
    retryFailedBtn.disabled = processing || !accounts.some(account => account.status === 'error');
}

function getRefreshToken(result) {
    if (!result) return '';
    return String(result.RefreshToken || result.refresh_token || result.refreshToken || result.rt || '').trim();
}

async function copySecret(value, label) {
    if (!value) {
        window.alert(`没有可复制的 ${label}`);
        return;
    }
    try {
        if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
            await navigator.clipboard.writeText(value);
        } else {
            const textarea = document.createElement('textarea');
            textarea.value = value;
            textarea.setAttribute('readonly', '');
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            const copied = document.execCommand('copy');
            textarea.remove();
            if (!copied) throw new Error('clipboard unavailable');
        }
        window.alert(`✅ ${label} 已复制到剪贴板`);
    } catch (_) {
        window.alert(`复制 ${label} 失败，请检查浏览器剪贴板权限`);
    }
}

window.copyToken = function(id) {
    const account = accounts.find(a => a.id === id);
    if (account && account.result) {
        copySecret(getRefreshToken(account.result), 'Refresh Token');
    }
};

function copyHistoryRefreshToken(id) {
    copySecret(historyRefreshTokens.get(String(id)) || '', 'Refresh Token');
}

exportBtn.addEventListener('click', () => {
    if (results.length === 0) {
        alert('没有成功的结果可以导出');
        return;
    }

    const output = {
        type: 'sub2api-data',
        version: 1,
        exported_at: new Date().toISOString(),
        proxies: [],
        accounts: results.map(r => ({
            name: `${Date.now()}----${r.Email}`,
            platform: 'openai',
            type: 'oauth',
            credentials: {
                access_token: r.AccessToken,
                refresh_token: r.RefreshToken,
                chatgpt_account_id: r.ChatGPTAccountID,
                organization_id: r.OrganizationID,
                expires_at: r.ExpiresAt,
                expires_in: r.ExpiresIn,
                plan_type: r.PlanType
            },
            extra: {
                email: r.Email,
                recovery: {
                    email: r.Email
                }
            }
        }))
    };

    const blob = new Blob([JSON.stringify(output, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `kkai-accounts-${Date.now()}.json`;
    a.click();
    URL.revokeObjectURL(url);
});
