// Shared navigation for mouse, keyboard, deep links and account actions.
(function () {
    const tabBtns = [...document.querySelectorAll('.tab-btn[data-tab]')].filter(btn => !btn.hidden);
    const tabPanels = [...document.querySelectorAll('.tab-panel[data-tab]')];
    let currentTab = '';
    function select(name, { focus = false, updateHash = true } = {}) {
        if (name === 'checks') name = 'history';
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
            window.history.pushState(window.history.state, '', `#panel-${name}`);
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
        select(location.hash === '#accountChecksPanel' ? 'history' : panel?.dataset.tab || 'login', { updateHash: false });
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
const accountCount = document.getElementById('accountCount');
const modeSelect = document.getElementById('modeSelect');
const concurrencyInput = document.getElementById('concurrency');
const startBtn = document.getElementById('startBtn');
const stopBtn = document.getElementById('stopBtn');
const retryFailedBtn = document.getElementById('retryFailedBtn');
const importSub2Btn = document.getElementById('importSub2Btn');
const autoDeliverToggle = document.getElementById('autoDeliverToggle');
const autoDeliverHint = document.getElementById('autoDeliverHint');
const deliveryMerchant = document.getElementById('deliveryMerchant');
const deliveryDeadline = document.getElementById('deliveryDeadline');
const deliveryOptionsDialog = document.getElementById('deliveryOptionsDialog');
const deliveryOptionsForm = document.getElementById('deliveryOptionsForm');
const deliveryOptionsBtn = document.getElementById('deliveryOptionsBtn');
const deliveryOptionsSummary = document.getElementById('deliveryOptionsSummary');
const deliveryOptionsMessage = document.getElementById('deliveryOptionsMessage');
const deliveryOptionsStatus = document.getElementById('deliveryOptionsStatus');
const deliveryOptionsApply = document.getElementById('deliveryOptionsApply');
const deliveryOptionsCancel = document.getElementById('deliveryOptionsCancel');
const deliveryGroups = document.getElementById('deliveryGroups');
const deliveryEgressRoutes = document.getElementById('deliveryEgressRoutes');
const deliveryEgressHint = document.getElementById('deliveryEgressHint');
const deliveryPriority = document.getElementById('deliveryPriority');
const deliveryConcurrency = document.getElementById('deliveryConcurrency');
const deliverySaveDefault = document.getElementById('deliverySaveDefault');
const credentialsDialog = document.getElementById('credentialsDialog');
const credentialsForm = document.getElementById('credentialsForm');
const credentialsPassword = document.getElementById('credentialsPassword');
const credentialsTotp = document.getElementById('credentialsTotp');
const credentialsClearTotp = document.getElementById('credentialsClearTotp');
const credentialsStatus = document.getElementById('credentialsStatus');
const credentialsSave = document.getElementById('credentialsSave');
const exportBtn = document.getElementById('exportBtn');
const statusGrid = document.getElementById('statusGrid');
const totalCount = document.getElementById('totalCount');
const successCount = document.getElementById('successCount');
const failedCount = document.getElementById('failedCount');
const pendingCount = document.getElementById('pendingCount');
const stoppedCount = document.getElementById('stoppedCount');
const checkingCount = document.getElementById('checkingCount');
const historyGrid = document.getElementById('historyGrid');
const accountTableSummary = document.getElementById('accountTableSummary');
const refreshHistoryBtn = document.getElementById('refreshHistoryBtn');
const historyRefreshStatus = document.getElementById('historyRefreshStatus');
const autoRecoveryToggle = document.getElementById('autoRecoveryToggle');
const autoRecoveryStatus = document.getElementById('autoRecoveryStatus');
const autoRecoveryDetail = document.getElementById('autoRecoveryDetail');
const historyAttention = document.getElementById('historyAttention');
const historyAttentionSummary = document.getElementById('historyAttentionSummary');
const historyAttentionList = document.getElementById('historyAttentionList');
const historySearchInput = document.getElementById('historySearchInput');
const historyStatusFilter = document.getElementById('historyStatusFilter');
const historyPlanFilter = document.getElementById('historyPlanFilter');
const historySort = document.getElementById('historySort');
const historyDetail = document.getElementById('historyDetail');
const historyDetailPanel = historyDetail.querySelector('[role="dialog"]') || historyDetail;
const historyDetailTitle = document.getElementById('historyDetailTitle');
const historyDetailBody = document.getElementById('historyDetailBody');
const historyDetailClose = document.getElementById('historyDetailClose');
const historyRecoveryPopover = document.getElementById('historyRecoveryPopover');
const historyRecoveryPopoverTitle = document.getElementById('historyRecoveryPopoverTitle');
const historyRecoveryPopoverBody = document.getElementById('historyRecoveryPopoverBody');
const historyRecoveryPopoverClose = document.getElementById('historyRecoveryPopoverClose');
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
let loginBatchController = null;
let startBatchInFlight = false;
let historyLoading = false;
let historyLoadPromise = null;
let historyRefreshRequested = false;
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
let historyLifecycleEvents = new Map();
let historyChecks = new Map();
let historyRecoveries = new Map();
let historyDeliveries = new Map();
let historyRechecks = new Map();
let historyRepairs = new Map();
// Group conflict choices are deliberately tracked by recovery task ID.  A
// delivery and its recovery task can share an account ID, while the backend
// applies the choice to one immutable task snapshot.
let historyGroupActionRequests = new Set();
let deliverySettings = null;
let deliveryBatchOptions = null;
let deliverySettingsRequest = null;
let pendingDeliveryOptions = null;
let deliveryOptionsSaving = false;
let selectedEgressRouteIDs = [];
let credentialsAccountID = '';
let credentialsTrigger = null;
let credentialsSaving = false;
let autoDeliveryConfigured = null;
const autoDeliveryPreferenceKey = 'kkai-auth:auto-deliver';
let autoDeliveryPreference = null;
try {
    const saved = window.localStorage?.getItem(autoDeliveryPreferenceKey);
    if (saved === 'true' || saved === 'false') autoDeliveryPreference = saved === 'true';
} catch (_) { /* Private browsing may disable local storage. */ }
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
let historyDetailGeneration = 0;
const historyCredentialValues = new Map();
const historyCredentialRequests = new Map();
// Recent Sub2 usage is loaded for the visible page only.  Keep this separate
// from the history payload so a refresh never turns into one request per row.
const historyRecentCosts = new Map();
const historyRecentCostInflight = new Map();
const historyRecentCostTTL = 30000;
let historyRecoveryPopoverID = '';
let historyRecoveryPopoverAbort = null;
let historyRecoveryPopoverTrigger = null;
let historyRecoveryPopoverPinned = false;
let historyRecoveryPopoverTimer = null;
let historyRecoveryPopoverVersion = '';
let historyRecoveryRestoringFocus = false;
// Keep the fallback conservative if the health endpoint is unavailable or
// served by an older binary; the server's advertised cap raises it later.
let serverMaxConcurrent = 1;
const historySelectedIDs = new Set();
document.addEventListener('auth-tab-changed', event => {
    updateActionBar(historySelectedIDs.size);
    closeActionConfirmation();
    closeDeliveryOptions();
    closeCredentialsEditor();
    if (event.detail.name !== 'history') closeHistoryDetails();
    if (event.detail.name !== 'history') closeHistoryRecoveryPopover();
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
const stageNames = {queued:'排队', browser:'启动浏览器', authorize:'打开授权页', email:'邮箱验证', phone:'手机验证',
    password:'密码验证', oauth:'等待登录确认', mfa:'二次验证', consent:'确认授权', token:'获取令牌',
    identity:'账号身份', proxy:'连接代理', challenge:'浏览器验证', configuration:'配置',
    retry_wait:'等待重试', complete:'完成', unknown:'登录'};

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
    accountPreflight.hidden = !accountsInput.value.trim();
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
    renderLoginConnectionSummary();
});

function renderLoginConnectionSummary() {
    const selectedRoute = deliveryBatchOptions?.egress_route_ids?.[0];
    const routeName = deliverySettings?.egress_routes?.find(route => Number(route.id) === selectedRoute)?.name || `出口 #${selectedRoute}`;
    document.getElementById('loginConnectionSummary').textContent =
        `${selectedRoute ? routeName : '服务器默认网络'} · ${modeSelect.value === 'sequential' ? '顺序模式' : '并发模式'}`;
}

refreshHistoryBtn.addEventListener('click', () => Promise.all([loadHistory(), loadRecoverySettings()]));
autoRecoveryToggle?.addEventListener('change', () => updateRecoverySetting(Boolean(autoRecoveryToggle.checked)));
autoDeliverToggle?.addEventListener('change', () => {
    autoDeliveryPreference = Boolean(autoDeliverToggle.checked);
    try { window.localStorage?.setItem(autoDeliveryPreferenceKey, String(autoDeliveryPreference)); } catch (_) { /* Keep the current-page choice. */ }
    renderAutoDeliverySetting();
});
historyAttentionList?.addEventListener('click', event => {
    const groupAction = event.target.closest('[data-attention-group-action]');
    if (groupAction && !groupAction.disabled) {
        const task = groupConflictTaskForID(groupAction.dataset.attentionGroupTask, groupAction.dataset.attentionGroupAccount);
        if (task) resolveGroupConflict(task, groupAction.dataset.attentionGroupAction, groupAction);
        return;
    }
    const edit = event.target.closest('[data-attention-edit]');
    if (edit) { openCredentialsEditor(edit.dataset.attentionEdit, edit); return; }
    const button = event.target.closest('[data-attention-account]');
    if (!button) return;
    const account = historyAccounts.find(item => item.id === button.dataset.attentionAccount);
    if (!account) return;
    historySearchInput.value = account.email;
    historyStatusFilter.value = 'all';
    historyPlanFilter.value = 'all';
    historyPage = 1;
    renderHistory();
    const card = historyGrid.querySelector(`[data-history-card="${CSS.escape(account.id)}"]`);
    const diagnostics = card?.querySelector('details.history-diagnostics');
    if (diagnostics) diagnostics.open = true;
    card?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' });
    card?.querySelector('[data-history-action="details"]')?.focus({ preventScroll: true });
});
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
historyPlanFilter.addEventListener('change', () => {
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
    const groupAction = event.target.closest('[data-history-group-action]');
    if (groupAction && !groupAction.disabled) {
        const task = groupConflictTaskForID(groupAction.dataset.historyGroupTask, groupAction.dataset.historyGroupAccount);
        if (task) resolveGroupConflict(task, groupAction.dataset.historyGroupAction, groupAction);
        return;
    }
    const button = event.target.closest('[data-history-action]');
    if (!button || button.disabled) return;
    const id = button.dataset.historyId;
    if (!id) return;
    if (button.dataset.historyAction === 'recovery-history') {
        toggleHistoryRecoveryPopover(id, button);
    } else if (button.dataset.historyAction === 'relogin') {
        reloginHistory(id);
    } else if (button.dataset.historyAction === 'reimport') {
        reimportRemoteMissingHistory(id);
    } else if (button.dataset.historyAction === 'import') {
        chooseDeliveryOptions('将此账号导入 Sub2。以下参数用于新建账号；已存在账号保留原配置。', true).then(options => {
            if (options) importHistoryAccounts([id], { deliveryOptions: options });
        });
    } else if (button.dataset.historyAction === 'reconcile') {
        reconcileHistoryImport(id);
    } else if (button.dataset.historyAction === 'delete') {
        deleteHistory(id);
    } else if (button.dataset.historyAction === 'copy-refresh-token') {
        copyHistoryRefreshToken(id);
    } else if (button.dataset.historyAction === 'details') {
        openHistoryDetails(id, button);
    } else if (button.dataset.historyAction === 'credentials') {
        openCredentialsEditor(id, button);
    } else if (button.dataset.historyAction === 'check') {
        window.AccountChecks?.startSingle(id);
    } else if (button.dataset.historyAction === 'recover') {
        checkAndRecoverHistory(id);
    } else if (button.dataset.historyAction === 'recheck') {
        retryHistoryRecheck(id, button.dataset.recheckTask);
    }
});
// Keep native <details> menus open across the usage refresh that follows a
// history render. The toggle event does not bubble, so capture the summary
// click and persist the state by account-specific details key.
historyGrid.addEventListener('pointerover', event => {
    const button = event.target.closest('[data-history-action="recovery-history"]');
    if (event.pointerType !== 'touch' && button && !button.disabled) openHistoryRecoveryPopover(button.dataset.historyId, button);
});
historyGrid.addEventListener('focusin', event => {
    const button = event.target.closest('[data-history-action="recovery-history"]');
    if (!historyRecoveryRestoringFocus && button && !button.disabled) openHistoryRecoveryPopover(button.dataset.historyId, button);
});
historyGrid.addEventListener('pointerout', event => {
    if (event.target.closest('[data-history-action="recovery-history"]')) scheduleHistoryRecoveryClose();
});
historyGrid.addEventListener('focusout', event => {
    if (event.target.closest('[data-history-action="recovery-history"]')) scheduleHistoryRecoveryClose();
});
historyGrid.addEventListener('keydown', event => {
    if ((event.key === 'ArrowDown' || event.key === 'Tab' && !event.shiftKey) &&
        event.target.closest('[data-history-action="recovery-history"]')) {
        event.preventDefault();
        openHistoryRecoveryPopover(event.target.dataset.historyId, event.target);
        if (event.key === 'ArrowDown') historyRecoveryPopoverPinned = true;
        historyRecoveryPopoverClose?.focus();
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
historyDetailBody.addEventListener('click', event => {
    const button = event.target.closest('[data-history-credential-action]');
    if (button && !button.disabled) handleHistoryCredentialAction(button.dataset.historyCredentialField, button.dataset.historyCredentialAction, button);
    const edit = event.target.closest('[data-history-detail-edit]');
    if (edit && !edit.disabled) openCredentialsEditor(historyDetailID, edit);
});
document.addEventListener('visibilitychange', () => {
    if (document.hidden) clearHistoryCredentials();
});
historyRecoveryPopoverClose?.addEventListener('click', () => closeHistoryRecoveryPopover(true));
historyRecoveryPopover?.addEventListener('click', event => {
    if (event.target.closest('[data-history-recovery-retry]')) loadHistoryRecoveryRecords();
});
historyRecoveryPopover?.addEventListener('pointerenter', () => clearTimeout(historyRecoveryPopoverTimer));
historyRecoveryPopover?.addEventListener('pointerleave', scheduleHistoryRecoveryClose);
historyRecoveryPopover?.addEventListener('focusout', scheduleHistoryRecoveryClose);
historyRecoveryPopover?.addEventListener('keydown', event => {
    if (event.key === 'Tab' && event.shiftKey && document.activeElement === historyRecoveryPopoverClose) {
        event.preventDefault();
        historyRecoveryPopoverPinned = false;
        historyRecoveryPopoverTrigger?.focus();
    }
});
document.addEventListener('pointerdown', event => {
    if (!historyRecoveryPopover?.hidden && !historyRecoveryPopover.contains(event.target) &&
        !event.target.closest('[data-history-action="recovery-history"]')) closeHistoryRecoveryPopover();
});
window.addEventListener('resize', positionHistoryRecoveryPopover);
document.getElementById('panel-history').addEventListener('scroll', positionHistoryRecoveryPopover);
document.addEventListener('keydown', event => {
    if (actionConfirmDialog.open || deliveryOptionsDialog.open || credentialsDialog.open) return;
    if (!historyRecoveryPopover?.hidden && event.key === 'Escape') {
        event.preventDefault?.();
        closeHistoryRecoveryPopover(true);
        return;
    }
    if (historyDetail.hidden) return;
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
loadDeliverySettings();
loadRecoverySettings();
scheduleHistoryRefresh();

startBtn.addEventListener('click', async () => {
    if (processing || historyBatchRunning || startBatchInFlight) return;
    startBatchInFlight = true;
    startBtn.disabled = true;
    try {
        const report = renderAccountPreflight();
        if (report.accounts.length === 0) {
            alert('请输入账号信息');
            return;
        }
        if (report.errors.length || report.duplicates.length) {
            alert(`输入检查未通过：${report.errors.length} 行格式错误，${report.duplicates.length} 行重复。请修正后再开始。`);
            return;
        }
        const unresolvedEmails = new Set(accounts.filter(account => account.status === 'checking')
            .map(account => account.email.toLowerCase()));
        if (report.accounts.some(account => unresolvedEmails.has(account.email.toLowerCase()))) {
            alert('有账号的上次登录结果尚未核对，请先点击“核对结果”并查看账号历史。');
            return;
        }
        const autoDeliver = autoDeliveryConfigured === true && Boolean(autoDeliverToggle?.checked);
        let autoDeliveryOptions = null;
        if (autoDeliver) {
            if (!await prepareDeliveryBatch()) return;
            autoDeliveryOptions = await chooseDeliveryOptions('开始处理前，请填写本批导入 Sub2 所需的号商和截止时间。');
            if (!autoDeliveryOptions) return;
        }
        if (processing || historyBatchRunning) return;

        accounts = report.accounts.map((account, index) => ({
            id: index,
            email: account.email,
            password: account.password,
            totp_secret: account.totp_secret,
            status: 'pending',
            message: '等待处理',
            result: null,
            autoDeliver,
            deliveryOptions: autoDeliver ? copyDeliveryOptions(autoDeliveryOptions) : null
        }));

        results = [];
        await runAccounts(accounts);
    } finally {
        startBatchInFlight = false;
        if (!processing && !historyBatchRunning) startBtn.disabled = false;
    }
});

stopBtn.addEventListener('click', () => {
    if (!loginBatchController || loginBatchController.signal.aborted) return;
    loginBatchController.abort();
    stopBtn.disabled = true;
    stopBtn.textContent = '停止中…';
    accounts.filter(account => account.status === 'pending').forEach(account => {
        account.status = 'stopped';
        account.message = '未开始，已停止';
    });
    renderStatus();
    updateStats();
});

window.recheckStoppedAccount = async function(id) {
    if (processing || historyBatchRunning || !accounts.some(account => account.id === id && account.status === 'checking')) return;
    await loadHistory();
};

retryFailedBtn.addEventListener('click', () => {
    return runAccounts(accounts.filter(canRetryLogin));
});

importSub2Btn.addEventListener('click', () => importLoginResultsToSub2());

window.retryAccount = function(id) {
    const account = accounts.find(account => account.id === id && canRetryLogin(account));
    if (account) return runAccounts([account]);
};

function canRetryLogin(account) {
    return ['error', 'stopped'].includes(account.status) && !deterministicStopReason(account);
}

async function loadHistory() {
    if (historyLoading) {
        historyRefreshRequested = true;
        return historyLoadPromise;
    }
    historyLoading = true;
    historyLoadPromise = (async () => {
        try {
            do {
                historyRefreshRequested = false;
                await loadHistorySnapshot();
            } while (historyRefreshRequested);
        } finally {
            historyLoading = false;
            historyLoadPromise = null;
            historySearchInput.disabled = processing || historyBatchRunning;
            refreshHistoryBtn.disabled = processing || historyBatchRunning;
            historyRefreshAt = Date.now() + historyRefreshDelay();
            renderHistory();
        }
    })();
    return historyLoadPromise;
}

async function loadHistorySnapshot() {
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
        if (typeof payload.sub2_configured === 'boolean') {
            autoDeliveryConfigured = payload.sub2_configured;
            renderAutoDeliverySetting();
        }
        historyImportsAvailable = payload.imports_available !== false;
        historySub2Statuses = normalizeSub2Statuses(payload.sub2_statuses ?? payload.sub2Statuses
            ?? payload.data?.sub2_statuses ?? payload.data?.sub2Statuses);
        historyLifecycleEvents = new Map(Object.entries(payload.lifecycle_events || payload.lifecycleEvents || {})
            .map(([id, events]) => [String(id), normalizeLifecycleEvents(events)]));
        historyChecks = new Map(Object.entries(payload.checks || {}));
        historyRecoveries = new Map(Object.entries(payload.recoveries || {}));
        historyDeliveries = new Map(Object.entries(payload.deliveries || {}));
        historyRechecks = new Map(Object.entries(payload.rechecks || {}));
        historyRepairs = new Map(Object.entries(payload.repairs || {}));
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
        historyAccounts = rows.map(row => {
            const id = String(row?.id ?? row?.account_id ?? '');
            return normalizeHistoryAccount({ ...row, lifecycle_events: historyLifecycleEvents.get(id) || row?.lifecycle_events }, historySub2Statuses.get(id));
        }).filter(account => account.id !== '');
        for (const account of historyAccounts) {
            if (terminalHistoryAccount(account)) historyRefreshTokens.delete(account.id);
            account.refreshToken = historyRefreshTokens.get(account.id) || '';
        }
        reconcileStoppedAccounts();
        const availableIDs = new Set(historyAccounts.map(account => account.id));
        historySelectedIDs.forEach(id => {
            if (!availableIDs.has(id)) historySelectedIDs.delete(id);
        });
        historyRefreshTokens.forEach((_, id) => {
            if (!availableIDs.has(id)) historyRefreshTokens.delete(id);
        });
        const detailAccount = historyAccounts.find(account => account.id === historyDetailID);
        if (historyDetailID && (!detailAccount || terminalHistoryAccount(detailAccount) && historyDetailBody.querySelector('[data-history-credential-action]'))) closeHistoryDetails();
        if (credentialsAccountID && !canReloginHistory(historyAccounts.find(account => account.id === credentialsAccountID))) {
            credentialsPassword.value = credentialsTotp.value = '';
            closeCredentialsEditor();
        }
        historyRecentCosts.forEach((_, id) => {
            if (!availableIDs.has(id)) historyRecentCosts.delete(id);
        });
        renderHistory();
        syncLoginDeliveries();
        document.dispatchEvent(new CustomEvent('auth-history-changed', { detail: { history: payload } }));
    } catch (error) {
        historyLoadError = error.message || '历史加载失败';
        historyRefreshStatus.textContent = historyLoadError;
    }
}

function scheduleHistoryRefresh() {
    if (historyRefreshTimer || typeof setInterval !== 'function') return;
    historyRefreshAt = Date.now() + historyRefreshDelay();
    historyRefreshTimer = setInterval(pollHistory, 5000);
}

function historyRefreshDelay() {
    if (accounts.some(account => account.status === 'checking')) return 5000;
    const checking = [...historyChecks.values()].some(item => ['queued', 'running'].includes(item.latest_task?.state));
    const recovering = [...historyRecoveries.values()].some(item => historyRecoveryActive(item));
    const delivering = [...historyDeliveries.values()].some(item => ['queued', 'checking', 'importing', 'verifying', 'working'].includes(item.state));
    const repairing = [...historyRepairs.values()].some(item => ['queued', 'checking', 'recovery_queued'].includes(item.state));
    const rechecking = [...historyRechecks.values()].some(item => ['pending', 'checking'].includes(item.state));
    const waiting = [...historyRecoveries.values(), ...historyDeliveries.values(), ...historyRepairs.values()].filter(item => item.next_retry_at && !item.requires_action && !['completed', 'success', 'canceled'].includes(item.state));
    if (checking || recovering || delivering || repairing || rechecking || historyRecoveryRequests.size || recoverySettings.scanning) return 5000;
    if (waiting.length) {
        const nextRetry = Math.min(...waiting.map(item => Date.parse(item.next_retry_at)).filter(Number.isFinite));
        return Number.isFinite(nextRetry) ? Math.max(5000, Math.min(60000, nextRetry - Date.now())) : 60000;
    }
    return recoverySettings.autoRecoveryEnabled ? 60000 : 5 * 60000;
}

async function pollHistory() {
    if (document.hidden || historyLoading || historyRefreshPolling || (processing && !accounts.some(account => account.status === 'success' && account.autoDeliver)) || historyBatchRunning || Date.now() < historyRefreshAt) return;
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
    const enabled = recoverySettings.autoRecoveryEnabled;
    const running = enabled && recoverySettings.running !== false;
    if (autoRecoveryStatus) autoRecoveryStatus.textContent = !enabled ? '已关闭'
        : !running ? '已开启 · 暂停运行' : recoverySettings.scanning ? '正在巡检…' : '已开启 · 后台运行';
    if (!autoRecoveryDetail) return;
    const parts = [running
        ? `每 ${Number(recoverySettings.interval_seconds) || 60} 秒巡检 Sub2 异常账号，确认 401 或凭据失效后重新登录、写回凭据并恢复调度。关闭页面后仍继续，开关已保存。`
        : enabled ? '自动恢复开关已保存，当前暂停运行。'
        : recoverySettings.blocked_reason ? '自动恢复已关闭，运行阻塞仍需处理。'
        : '开启后立即巡检，此后每 60 秒检查 Sub2 异常账号；也可点击账号旁的“检测并恢复”。'];
    if (recoverySettings.blocked_reason) parts.push(`暂停原因：${recoverySettings.blocked_reason}`);
    if (recoverySettings.last_scan_at) parts.push(`上次巡检：${formatHistoryTime(recoverySettings.last_scan_at)}`);
    if (running && recoverySettings.next_scan_at) parts.push(`下次巡检：${formatHistoryTime(recoverySettings.next_scan_at)}`);
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
        if (typeof payload.sub2_configured === 'boolean') {
            autoDeliveryConfigured = payload.sub2_configured;
            renderAutoDeliverySetting();
        }
        concurrencyInput.max = String(serverMaxConcurrent);
        const selected = Math.min(serverMaxConcurrent, Math.max(1, parseInt(concurrencyInput.value) || 1));
        concurrencyInput.value = String(selected);
        concurrencyInput.title = `当前服务最多同时处理 ${serverMaxConcurrent} 个账号`;
    } catch (_) {
        // The health endpoint is advisory; keep the safe UI fallback.
    }
}

function renderAutoDeliverySetting() {
    if (!autoDeliverToggle) return;
    autoDeliverToggle.disabled = processing || autoDeliveryConfigured !== true;
    autoDeliverToggle.checked = autoDeliveryConfigured === true && autoDeliveryPreference !== false;
    if (autoDeliverHint) autoDeliverHint.textContent = autoDeliveryConfigured === null ? '尚未确认 Sub2 配置，自动交付暂不可用。'
        : !autoDeliveryConfigured ? 'Sub2 未配置；本次仅登录并保存账号。'
        : autoDeliverToggle.checked ? '已受理的交付任务在后台继续，关闭页面不受影响。'
        : '本次仅登录并保存账号；之后可手动导入 Sub2。';
    deliveryOptionsBtn.disabled = processing || historyBatchRunning || autoDeliveryConfigured !== true;
}

function copyDeliveryOptions(options) {
    options = options || {};
    const merchant = normalizeDeliveryValue(options.merchant);
    const deadline = normalizeDeliveryValue(options.deadline);
    return {
        group_ids: [...(options.group_ids || [])], priority: Number(options.priority), concurrency: Number(options.concurrency),
        ...((options.egress_route_ids || []).length ? { egress_route_ids: [...options.egress_route_ids] } : {}),
        ...(merchant ? { merchant } : {}), ...(deadline ? { deadline } : {})
    };
}

function normalizeDeliveryValue(value) {
    // Match the server's Unicode whitespace trimming, including U+0085.
    return String(value ?? '').replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
}

function deliveryValueError(value, label, maxLength) {
    if (!value) return `${label}不能为空。`;
    if (Array.from(value).length > maxLength) return `${label}最多 ${maxLength} 个字符。`;
    return /[\u0000-\u001f\u007f-\u009f]/.test(value) ? `${label}不能包含控制字符。` : '';
}

function renderDeliveryOptionsSummary() {
    if (!deliveryBatchOptions) return;
    const names = deliveryBatchOptions.group_ids.map(id => deliverySettings?.groups?.find(group => Number(group.id) === id)?.name || `分组 #${id}`);
    const batch = deliveryBatchOptions.merchant && deliveryBatchOptions.deadline
        ? ` · ${deliveryBatchOptions.merchant} | ${deliveryBatchOptions.deadline}` : '';
    const routes = (deliveryBatchOptions.egress_route_ids || []).map(id => deliverySettings?.egress_routes?.find(route => Number(route.id) === id)?.name || `出口 #${id}`);
    deliveryOptionsSummary.textContent = `${names.length ? names.join('、') : '未分组'} · ${routes.length ? `出口 ${routes.join('、')}（${routes[0]} 登录）` : '默认出口'} · 优先级 ${deliveryBatchOptions.priority} · 账号并发 ${deliveryBatchOptions.concurrency}${batch}`;
}

function renderDeliveryEgressHint() {
    const first = selectedEgressRouteIDs[0];
    const route = deliverySettings?.egress_routes?.find(item => Number(item.id) === first);
    deliveryEgressHint.textContent = first && deliverySettings?.egress_available === false
        ? '暂时无法读取 Sub2 出口；已有选择恢复后才能提交。'
        : first ? `${route?.name || `出口 #${first}`} 用于登录；其余出口由 Sub2 调度。`
            : '不选时登录使用服务器默认网络；Sub2 使用其默认出口。';
    deliveryEgressRoutes.querySelectorAll('[data-egress-primary]').forEach(marker => {
        marker.hidden = Number(marker.dataset.egressPrimary) !== first;
    });
}

function selectableDeliveryEgress(route) {
    return route?.eligible === true && route.kind !== 'direct' && (!route.protocol || String(route.protocol).toLowerCase() === 'http');
}

function deliveryEgressSelectionInvalid(ids) {
    return ids.length > 0 && (deliverySettings?.egress_available === false ||
        ids.some(id => !deliverySettings?.egress_routes?.some(route => Number(route.id) === id && selectableDeliveryEgress(route))));
}

async function loadDeliverySettings(initializeBatch = true) {
    if (deliverySettingsRequest) return deliverySettingsRequest;
    deliverySettingsRequest = (async () => {
        try {
            const response = await fetch('/api/sub2/import-settings', { headers: { Accept: 'application/json' }, signal: AbortSignal.timeout(15000) });
            const payload = await response.json().catch(() => ({}));
            if (!response.ok || !payload.defaults) throw new Error(payload.message || `交付参数读取失败（HTTP ${response.status}）`);
            deliverySettings = payload;
            if (initializeBatch && !deliveryBatchOptions) {
                deliveryBatchOptions = copyDeliveryOptions(payload.defaults);
            }
            renderDeliveryOptionsSummary();
            renderLoginConnectionSummary();
            return true;
        } catch (error) {
            deliveryOptionsSummary.textContent = error.message || '交付参数读取失败，请打开“交付参数”重试';
            return false;
        } finally {
            deliverySettingsRequest = null;
        }
    })();
    return deliverySettingsRequest;
}

async function prepareDeliveryBatch() {
    if (!deliverySettings && !await loadDeliverySettings()) {
        window.alert('交付参数读取失败，请打开“交付参数”重试。');
        return false;
    }
    if (deliverySettings.groups_available === false && deliveryBatchOptions.group_ids.length) {
        window.alert('Sub2 分组暂时无法读取，当前带分组的交付暂不能提交，请打开“交付参数”刷新。');
        return false;
    }
    if (deliveryEgressSelectionInvalid(deliveryBatchOptions.egress_route_ids || [])) {
        window.alert('Sub2 出口暂时不可用，请打开“交付参数”重新选择。');
        return false;
    }
    return true;
}

function closeDeliveryOptions(options = null) {
    if (!pendingDeliveryOptions || deliveryOptionsSaving) return;
    const { resolve, trigger } = pendingDeliveryOptions;
    pendingDeliveryOptions = null;
    deliverySaveDefault.disabled = false;
    const defaultLabel = deliverySaveDefault.closest?.('label');
    if (defaultLabel) defaultLabel.hidden = false;
    if (deliveryOptionsDialog.open) deliveryOptionsDialog.close();
    if (trigger?.isConnected !== false && !trigger?.disabled && !trigger?.closest?.('[hidden]')) trigger?.focus({ preventScroll: true });
    resolve(options);
}

function chooseDeliveryOptions(message = '为接下来的登录批次设置交付参数；已经开始的任务不受影响。', importBatch = false, initialOptions = null, persistOptions = true) {
    if (pendingDeliveryOptions) return Promise.resolve(null);
    deliveryOptionsMessage.textContent = message;
    deliveryOptionsStatus.textContent = '正在读取 Sub2 分组…';
    deliveryOptionsStatus.classList.remove('error');
    deliveryOptionsApply.textContent = importBatch ? '按此配置导入' : '用于本批';
    deliveryOptionsApply.disabled = true;
    deliveryMerchant.disabled = true;
    deliveryDeadline.disabled = true;
    deliverySaveDefault.checked = false;
    deliverySaveDefault.disabled = !persistOptions;
    const defaultLabel = deliverySaveDefault.closest?.('label');
    if (defaultLabel) defaultLabel.hidden = !persistOptions;
    deliveryGroups.innerHTML = '';
    deliveryEgressRoutes.innerHTML = '';
    const promise = new Promise(resolve => { pendingDeliveryOptions = { resolve, trigger: document.activeElement, persistOptions,
        initialOptions: initialOptions ? copyDeliveryOptions(initialOptions) : null }; });
    const pending = pendingDeliveryOptions;
    deliveryOptionsDialog.showModal();
    loadDeliverySettings(persistOptions).then(loaded => {
        if (pendingDeliveryOptions !== pending) return;
        if (!loaded) {
            deliveryOptionsStatus.textContent = `${deliveryOptionsSummary.textContent}。关闭后重新打开可重试。`;
            deliveryOptionsStatus.classList.add('error');
            return;
        }
        const options = copyDeliveryOptions(pending.initialOptions || (persistOptions ? deliveryBatchOptions : deliverySettings.defaults)
            || deliverySettings.defaults || { group_ids: [], priority: 1, concurrency: 1 });
        pending.baseOptions = copyDeliveryOptions(options);
        const groups = [...(deliverySettings.groups || [])];
        for (const id of options.group_ids) if (!groups.some(group => Number(group.id) === id)) groups.push({ id, name: `分组 #${id}` });
        deliveryGroups.innerHTML = groups.map(group => `<label class="delivery-group-option"><input type="checkbox" data-delivery-group="${Number(group.id)}" ${options.group_ids.includes(Number(group.id)) ? 'checked' : ''} ${deliverySettings.groups_available === false ? 'disabled' : ''}><span>${escapeHTML(group.name)}</span></label>`).join('');
        document.getElementById('deliveryGroupsHint').textContent = deliverySettings.groups_available === false ? '暂时无法读取 Sub2 分组；已有选择仅供查看，分组恢复后才能提交带分组的任务。' : groups.length ? '可选多个分组；不选时进入未分组。' : 'Sub2 暂无可用分组，本批进入未分组。';
        selectedEgressRouteIDs = [...(options.egress_route_ids || [])];
        const routes = [...(deliverySettings.egress_routes || [])];
        for (const id of selectedEgressRouteIDs) if (!routes.some(route => Number(route.id) === id)) routes.push({ id, name: `出口 #${id}`, eligible: false });
        deliveryEgressRoutes.innerHTML = routes.map(route => `<label class="delivery-group-option"><input type="checkbox" data-delivery-route="${Number(route.id)}" ${selectedEgressRouteIDs.includes(Number(route.id)) ? 'checked' : ''} ${selectableDeliveryEgress(route) && deliverySettings.egress_available !== false || selectedEgressRouteIDs.includes(Number(route.id)) ? '' : 'disabled'}><span>${escapeHTML(route.name || `出口 #${route.id}`)}${route.public_ip ? ` · ${escapeHTML(route.public_ip)}` : ''}${selectableDeliveryEgress(route) ? '' : ' · 不可用'}</span><strong class="delivery-primary-badge" data-egress-primary="${Number(route.id)}" ${selectedEgressRouteIDs[0] === Number(route.id) ? '' : 'hidden'}>登录使用</strong></label>`).join('');
        renderDeliveryEgressHint();
        deliveryPriority.value = String(options.priority);
        deliveryConcurrency.value = String(options.concurrency);
        deliveryMerchant.value = options.merchant || '';
        deliveryDeadline.value = options.deadline || '';
        deliveryMerchant.disabled = false;
        deliveryDeadline.disabled = false;
        deliveryOptionsStatus.textContent = '';
        deliveryOptionsApply.disabled = (deliverySettings.groups_available === false && options.group_ids.length > 0) || deliveryEgressSelectionInvalid(selectedEgressRouteIDs);
        deliveryMerchant.focus({ preventScroll: true });
    });
    return promise;
}

deliveryOptionsBtn.addEventListener('click', () => chooseDeliveryOptions());
deliveryOptionsCancel.addEventListener('click', () => closeDeliveryOptions());
deliveryOptionsDialog.addEventListener('cancel', event => { event.preventDefault(); closeDeliveryOptions(); });
deliveryOptionsDialog.addEventListener('close', () => { if (!deliveryOptionsDialog.open) closeDeliveryOptions(); });
deliveryEgressRoutes.addEventListener('change', event => {
    const id = Number(event.target.dataset.deliveryRoute);
    if (!Number.isSafeInteger(id) || id <= 0 || (event.target.checked &&
        (deliverySettings.egress_available === false || !deliverySettings.egress_routes?.some(route => Number(route.id) === id && selectableDeliveryEgress(route))))) return;
    selectedEgressRouteIDs = event.target.checked
        ? [...selectedEgressRouteIDs.filter(selected => selected !== id), id]
        : selectedEgressRouteIDs.filter(selected => selected !== id);
    renderDeliveryEgressHint();
    const baseOptions = pendingDeliveryOptions?.baseOptions || deliveryBatchOptions || deliverySettings?.defaults || { group_ids: [], priority: 1, concurrency: 1 };
    deliveryOptionsApply.disabled = (deliverySettings.groups_available === false && baseOptions.group_ids.length > 0) || deliveryEgressSelectionInvalid(selectedEgressRouteIDs);
});
deliveryOptionsForm.addEventListener('submit', async event => {
    event.preventDefault();
    if (!pendingDeliveryOptions || deliveryOptionsSaving || deliveryOptionsApply.disabled) return;
    const baseOptions = pendingDeliveryOptions.baseOptions || deliveryBatchOptions || deliverySettings?.defaults || { group_ids: [], priority: 1, concurrency: 1 };
    const options = {
        group_ids: deliverySettings.groups_available === false ? [...baseOptions.group_ids] : [...deliveryGroups.querySelectorAll('input:checked')].map(input => Number(input.dataset.deliveryGroup)),
        ...(selectedEgressRouteIDs.length ? { egress_route_ids: [...selectedEgressRouteIDs] } : {}),
        priority: Number(deliveryPriority.value), concurrency: Number(deliveryConcurrency.value),
        merchant: normalizeDeliveryValue(deliveryMerchant.value),
        deadline: normalizeDeliveryValue(deliveryDeadline.value)
    };
    const deliveryIdentityError = deliveryValueError(options.merchant, '号商', 32) || deliveryValueError(options.deadline, '截止时间', 64);
    if (deliveryIdentityError) {
        deliveryOptionsStatus.textContent = deliveryIdentityError;
        deliveryOptionsStatus.classList.add('error');
        (options.merchant ? deliveryDeadline : deliveryMerchant).focus();
        return;
    }
    if (!deliveryPriority.value || !Number.isSafeInteger(options.priority) || options.priority < 0 || options.priority > 2147483647 ||
        !deliveryConcurrency.value || !Number.isSafeInteger(options.concurrency) || options.concurrency < 1 || options.concurrency > 10000) {
        deliveryOptionsStatus.textContent = '优先级需为非负整数，账号并发需为 1–10000 的整数。';
        deliveryOptionsStatus.classList.add('error');
        return;
    }
    deliveryOptionsSaving = true;
    deliveryOptionsApply.disabled = true;
    deliveryOptionsCancel.disabled = true;
    try {
        if (pendingDeliveryOptions.persistOptions && deliverySaveDefault.checked) {
            deliveryOptionsStatus.textContent = '正在保存平台默认参数…';
            const response = await fetch('/api/sub2/import-settings', {
                method: 'PUT', headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
                body: JSON.stringify(options), signal: AbortSignal.timeout(15000)
            });
            const payload = await response.json().catch(() => ({}));
            if (!response.ok) throw new Error(payload.message || `默认参数保存失败（HTTP ${response.status}）`);
            deliverySettings.defaults = copyDeliveryOptions(options);
        }
        if (pendingDeliveryOptions.persistOptions) {
            deliveryBatchOptions = copyDeliveryOptions(options);
            renderDeliveryOptionsSummary();
            renderLoginConnectionSummary();
        }
        deliveryOptionsSaving = false;
        closeDeliveryOptions(copyDeliveryOptions(options));
    } catch (error) {
        deliveryOptionsStatus.textContent = error.message || '设置保存失败，请重试';
        deliveryOptionsStatus.classList.add('error');
    } finally {
        deliveryOptionsSaving = false;
        deliveryOptionsApply.disabled = false;
        deliveryOptionsCancel.disabled = false;
    }
});

function openCredentialsEditor(id, trigger = document.activeElement) {
    const account = historyAccounts.find(item => item.id === String(id));
    if (!account || terminalHistoryAccount(account) || credentialsSaving || historyActionsLocked()) return;
    credentialsAccountID = account.id;
    credentialsTrigger = trigger;
    credentialsPassword.value = credentialsTotp.value = '';
    credentialsClearTotp.checked = false;
    credentialsTotp.disabled = false;
    credentialsStatus.textContent = '';
    credentialsStatus.classList.remove('error');
    document.getElementById('credentialsAccount').textContent = historyAccountName(account);
    credentialsDialog.showModal();
    credentialsPassword.focus({ preventScroll: true });
}

function closeCredentialsEditor() {
    if (credentialsSaving) return;
    credentialsAccountID = '';
    credentialsPassword.value = credentialsTotp.value = '';
    if (credentialsDialog.open) credentialsDialog.close();
    if (credentialsTrigger?.isConnected !== false && !credentialsTrigger?.disabled && !credentialsTrigger?.closest?.('[hidden]')) credentialsTrigger?.focus({ preventScroll: true });
    credentialsTrigger = null;
}

credentialsClearTotp.addEventListener('change', () => { credentialsTotp.disabled = credentialsClearTotp.checked; });
document.getElementById('credentialsCancel').addEventListener('click', closeCredentialsEditor);
credentialsDialog.addEventListener('cancel', event => { event.preventDefault(); closeCredentialsEditor(); });
credentialsDialog.addEventListener('close', () => { if (!credentialsDialog.open) closeCredentialsEditor(); });
credentialsForm.addEventListener('submit', async event => {
    event.preventDefault();
    if (!credentialsAccountID || credentialsSaving) return;
    const changes = {};
    if (credentialsPassword.value) changes.password = credentialsPassword.value;
    if (credentialsClearTotp.checked) changes.clear_totp = true;
    else if (credentialsTotp.value.trim()) changes.totp_secret = credentialsTotp.value.trim();
    if (!Object.keys(changes).length) {
        credentialsStatus.textContent = '请填写需要更新的资料，或明确选择要清除的项目。';
        credentialsStatus.classList.add('error');
        return;
    }
    credentialsSaving = true;
    credentialsSave.disabled = true;
    document.getElementById('credentialsCancel').disabled = true;
    credentialsStatus.textContent = '正在保存资料…';
    credentialsStatus.classList.remove('error');
    try {
        const response = await fetch(`/api/history/${encodeURIComponent(credentialsAccountID)}/credentials`, {
            method: 'PATCH', headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
            body: JSON.stringify(changes), signal: AbortSignal.timeout(30000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `资料保存失败（HTTP ${response.status}）`);
        historyBatchSummary = '资料已保存，请查看账号状态与处理进度。';
        credentialsSaving = false;
        closeCredentialsEditor();
        await loadHistory();
        historyRefreshAt = Date.now() + 5000;
    } catch (error) {
        credentialsStatus.textContent = error.name === 'TimeoutError' ? '请求超时，保存结果待确认；可刷新账号状态后核对。' : error.message || '资料保存失败，请重试';
        credentialsStatus.classList.add('error');
    } finally {
        credentialsSaving = false;
        credentialsSave.disabled = false;
        document.getElementById('credentialsCancel').disabled = false;
    }
});

function normalizeSub2Statuses(value) {
    const entries = Array.isArray(value)
        ? value.map(item => [item?.account_id ?? item?.accountId ?? item?.id, item])
        : Object.entries(value || {});
    return new Map(entries.filter(([id, item]) => id !== undefined && id !== null && item)
        .map(([id, item]) => [String(id), normalizeSub2Status(item)]));
}

function normalizeSub2Status(value) {
    const source = value || {};
    const associationStatus = String(source.association_status || source.associationStatus || '').toLowerCase();
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
        associationStatus,
        remoteMissing: source.remote_missing === true || source.remoteMissing === true || source.state === 'remote_missing' || associationStatus === 'missing',
        name: String(source.name || '').trim(),
        nameSource: String(source.name_source || source.nameSource || '').toLowerCase(),
        nameStale: Boolean(source.name_stale ?? source.nameStale),
        quota: normalizeSub2Quota(source.quota || source.usage),
        checkedAt: source.checked_at || source.checkedAt || '',
        stale: Boolean(source.stale),
        unknown: Boolean(source.unknown),
        error: String(source.error || ''),
        usageUnknown: Boolean(source.usage_unknown ?? source.usageUnknown),
        usageStale: Boolean(source.usage_stale ?? source.usageStale),
        usageError: String(source.usage_error || source.usageError || '')
    };
}

function normalizeSub2Quota(value) {
    const source = value || {};
    const window = input => {
        const item = input || {};
        const number = input => input === null || input === undefined || input === '' ? null : Number(input);
        const percent = number(item.used_percent ?? item.usedPercent);
        const resetAfter = number(item.reset_after_seconds ?? item.resetAfterSeconds);
        const minutes = number(item.window_minutes ?? item.windowMinutes);
        const remainingSeconds = number(item.remaining_seconds ?? item.remainingSeconds);
        const usedRequests = number(item.used_requests ?? item.usedRequests);
        const limitRequests = number(item.limit_requests ?? item.limitRequests);
        return {
            usedPercent: Number.isFinite(percent) ? Math.max(0, Math.min(100, percent)) : null,
            resetAt: item.reset_at ?? item.resetAt ?? '',
            resetAfterSeconds: Number.isFinite(resetAfter) ? Math.max(0, resetAfter) : null,
            windowMinutes: Number.isFinite(minutes) ? Math.max(0, minutes) : null,
            remainingSeconds: Number.isFinite(remainingSeconds) ? Math.max(0, remainingSeconds) : null,
            usedRequests: Number.isFinite(usedRequests) ? Math.max(0, usedRequests) : null,
            limitRequests: Number.isFinite(limitRequests) ? Math.max(0, limitRequests) : null
        };
    };
    const creditsSource = source.credits || {};
    const hasWindow = source.five_hour || source.fiveHour || source.seven_day || source.sevenDay
        || source.seven_day_sonnet || source.sevenDaySonnet || source.seven_day_fable || source.sevenDayFable
        || source.thirty_day || source.thirtyDay;
    const hasCredits = Object.keys(creditsSource).length > 0;
    if (!hasWindow && !hasCredits && !source.updated_at && !source.updatedAt) return null;
    return {
        fiveHour: source.five_hour || source.fiveHour ? window(source.five_hour || source.fiveHour) : null,
        sevenDay: source.seven_day || source.sevenDay ? window(source.seven_day || source.sevenDay) : null,
        sevenDaySonnet: source.seven_day_sonnet || source.sevenDaySonnet ? window(source.seven_day_sonnet || source.sevenDaySonnet) : null,
        sevenDayFable: source.seven_day_fable || source.sevenDayFable ? window(source.seven_day_fable || source.sevenDayFable) : null,
        thirtyDay: source.thirty_day || source.thirtyDay ? window(source.thirty_day || source.thirtyDay) : null,
        credits: hasCredits ? {
            balance: String(creditsSource.balance ?? ''),
            hasCredits: typeof creditsSource.has_credits === 'boolean' ? creditsSource.has_credits : typeof creditsSource.hasCredits === 'boolean' ? creditsSource.hasCredits : null,
            unlimited: typeof creditsSource.unlimited === 'boolean' ? creditsSource.unlimited : null
        } : null,
        updatedAt: source.updated_at ?? source.updatedAt ?? '',
        source: String(source.source || '')
    };
}

function normalizeLifecycleEvents(value) {
    if (!Array.isArray(value)) return [];
    return value.map(event => {
        const source = event || {};
        return {
            id: String(source.id ?? ''),
            accountID: String(source.account_id ?? source.accountId ?? ''),
            email: String(source.email || ''),
            source: String(source.source || ''),
            status: String(source.status || '').toLowerCase(),
            reason: String(source.reason || ''),
            code: String(source.code || ''),
            httpStatus: Number(source.http_status ?? source.httpStatus ?? 0) || 0,
            observedAt: source.observed_at || source.observedAt || ''
        };
    }).filter(event => event.email || event.source || event.status || event.reason || event.code || event.httpStatus || event.observedAt);
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
        sub2Policy: String(source.sub2_policy || source.sub2Policy || 'unknown').toLowerCase(),
        lastSuccessAt: source.last_success_at || source.lastSuccessAt || '',
        lastAttemptAt: source.last_attempt_at || source.lastAttemptAt || '',
        updatedAt: source.updated_at || source.updatedAt || '',
        attemptCount: Number(source.attempt_count ?? source.attemptCount ?? 0),
        recoveryCount: Math.max(0, Number(source.recovery_count ?? 0) || 0),
        recoveryAttemptCount: Math.max(0, Number(source.recovery_attempt_count ?? 0) || 0),
        lastError: String(source.last_error || source.lastError || ''),
        lastErrorCode: String(source.last_error_code || source.lastErrorCode || ''),
        lastHTTPStatus: Number(source.last_http_status || source.lastHTTPStatus || 0),
        credentialVersion: Number(source.credential_version || 0),
        requiresAction: source.requires_action === true,
        retryable: typeof source.retryable === 'boolean' ? source.retryable : null,
        nextRetryAt: source.next_retry_at || source.nextRetryAt || '',
        manualReason: String(source.manual_reason || source.manualReason || ''),
        manualAction: String(source.manual_action || ''),
        planType: String(source.plan_type || source.planType || ''),
        expiresAt: Number(source.expires_at ?? source.expiresAt ?? 0) || 0,
        lifecycleEvents: normalizeLifecycleEvents(source.lifecycle_events || source.lifecycleEvents),
        refreshToken: '',
        reloginMessage: '',
        sub2Status: sub2Status || normalizeSub2Status(null)
    };
}

function historyAccountName(account) {
    const name = String(account?.sub2Status?.name || '').trim();
    const email = String(account?.email || '').trim();
    return name && name.toLowerCase() !== email.toLowerCase() ? name : email || '未知账号';
}

function historyPlanLabel(value) {
    const raw = String(value || '').toLowerCase();
    return ({ free: 'Free', plus: 'Plus', pro: 'Pro', team: 'Team', business: 'Business' }[raw] || (raw ? raw : '未知（未识别）'));
}

function renderHistoryPlanFilter() {
    if (!historyPlanFilter) return;
    const current = historyPlanFilter.value || 'all';
    const values = [...new Set(historyAccounts.map(account => String(account.planType || '').toLowerCase() || 'unknown'))].sort((a, b) => a.localeCompare(b));
    if (current !== 'all' && !values.includes(current)) values.push(current);
    historyPlanFilter.innerHTML = ['<option value="all">全部类型</option>', ...values.map(value => {
        const count = historyAccounts.filter(account => (String(account.planType || '').toLowerCase() || 'unknown') === value).length;
        const suffix = value === current && current !== 'all' && count === 0 ? '（0 个匹配）' : '';
        return `<option value="${escapeHTML(value)}">${escapeHTML(historyPlanLabel(value === 'unknown' ? '' : value))}${suffix}</option>`;
    })].join('');
    historyPlanFilter.value = values.includes(current) ? current : 'all';
}

function sub2AutomationBlock(account) {
    const state = account?.sub2Status || {};
    if (discardedHistoryAccount(account)) return { code: 'discarded', label: '账号已废弃', at: account.updatedAt };
    if (account?.sub2Policy === 'free_local') return { code: 'free_local', label: 'Free 账号 · 仅保留在 AUTH', at: account.updatedAt };
    if (state.associationStatus === 'missing' || state.remoteMissing || historyImportState(account) === 'remote_missing') return { code: 'missing', label: 'Sub2 关联账号不存在 · 已停止自动检测和恢复', at: state.checkedAt };
    if (state.associationStatus === 'unmatched') return { code: 'unmatched', label: '未找到 Sub2 关联 · 不参与自动恢复', at: state.checkedAt };
    return null;
}

function deterministicStopReason(account) {
    if (!account) return '';
    const known = loginBlockLabel(account.manualReason || account.manual_reason || account.lastErrorCode || account.error_code || account.code);
    if (known) return known;
    const stopped = (account.requiresAction || account.requires_action) && account.retryable === false && !(account.nextRetryAt || account.next_retry_at);
    return stopped ? account.lastError || account.last_error || account.message || '登录阻塞，原因待确认' : '';
}

function loginTaskSuperseded(account, task) {
    const code = task?.error_code;
    if (task?.failure_stage !== 'logging_in' || task.retry_action !== 'manual' || !['failed', 'unknown'].includes(task.state) ||
        !['email_verification_required', 'phone_verification_required', 'password_rejected', 'totp_rejected', 'verification_code_rejected', 'credentials_rejected', 'auth_page_timeout_exhausted'].includes(code) ||
        !Object.hasOwn(task, 'source_credential_attempt_id') && !Object.hasOwn(task, 'result_credential_attempt_id')) return false;
    const current = Number(account?.credentialVersion ?? account?.credential_version ?? 0);
    const source = Number(task.source_credential_attempt_id || 0), result = Number(task.result_credential_attempt_id || 0);
    return [current, source, result].every(value => Number.isSafeInteger(value) && value >= 0) && current > Math.max(source, result);
}

function automationStopped(account) {
    const sub2Block = sub2AutomationBlock(account);
    if (sub2Block) return sub2Block;
    const reason = ['error', 'failed'].includes(account.status) && historyRepairs.get(account.id)?.state !== 'recovery_queued' ? deterministicStopReason(account) : '';
    if (reason) return { code: 'login', label: `自动处理已暂停 · ${reason}`, at: account.lastAttemptAt || account.updatedAt };
    const tasks = historyAccountTasks(account);
    for (const task of Object.values(tasks)) {
        if (!task || ['completed', 'success', 'canceled'].includes(task.state)) continue;
        const taskReason = deterministicStopReason(task);
        if (taskReason) return { code: 'login', label: `自动处理已暂停 · ${taskReason}`, at: task.updated_at };
    }
    return null;
}

function loginBlockLabel(code) {
    return ({
        email_verification_required: '需要邮箱验证码 · 已停止自动重试',
        phone_verification_required: '需要手机/短信验证 · 已停止自动重试',
        password_rejected: '密码错误 · 已停止自动重试',
        totp_rejected: 'TOTP 验证码被拒绝 · 已停止自动重试',
        verification_code_rejected: '验证码被拒绝，验证类型待确认 · 已停止自动重试',
        credentials_rejected: '登录凭据被拒绝 · 已停止自动重试',
        auth_page_timeout_exhausted: '认证页面等待超时 · 已停止自动重试'
    }[String(code || '')] || '');
}

function lifecycleSourceName(source) {
    return ({ official: '官方检测', sub2: 'Sub2', auth: 'AUTH' }[String(source || '').toLowerCase()] || source || '来源未知');
}

function lifecycleStatusName(status) {
    return ({ deleted: '已删除', deactivated: '已停用', deleted_or_deactivated: '已删除或停用', discarded: '已废弃' }[String(status || '').toLowerCase()] || status || '状态变化');
}

function renderLifecycleEvents(events, available = true) {
    if (!available) return '<div class="history-detail-attempts"><p class="hint">账号状态历史暂不可用，请稍后重试</p></div>';
    const rows = normalizeLifecycleEvents(events).sort((left, right) => historyTimeValue(right.observedAt) - historyTimeValue(left.observedAt) || Number(right.id) - Number(left.id));
    if (!rows.length) return '';
    const items = rows.map(event => {
        const reason = [event.code === 'invalid_username_or_password' ? '账密被拒绝' : event.reason, event.code && event.code !== 'invalid_username_or_password' ? `代码 ${event.code}` : '', event.httpStatus > 0 ? `HTTP ${event.httpStatus}` : ''].filter(Boolean).join(' · ');
        return `<div class="history-attempt"><div class="history-attempt-header"><span>${escapeHTML(event.email || '账号邮箱未知')}</span><span>${escapeHTML(formatHistoryTime(event.observedAt))}</span></div><div class="history-attempt-message">${escapeHTML(`${lifecycleSourceName(event.source)} · ${lifecycleStatusName(event.status)}${reason ? ` · ${reason}` : ''}`)}</div></div>`;
    }).join('');
    return `<div class="history-detail-attempts"><p class="hint">账号状态历史（保留删除或停用的来源证据）</p>${items}</div>`;
}

function sub2StatusName(status) {
    return ({
        active: '正常', available: '正常', enabled: '正常',
        error: '异常', inactive: '已禁用', disabled: '已禁用', deleted: '已删除',
        paused: '已暂停', unknown: '未知'
    }[status] || status || '未知');
}

function historyPoolState(account) {
    const state = account.sub2Status;
    const block = sub2AutomationBlock(account);
    if (block) return { kind: 'absent', label: block.code === 'free_local' ? 'Free · 仅保留在 AUTH' : block.code === 'missing' ? 'Sub2 关联不存在' : '未找到 Sub2 关联' };
    if (!historySub2Configured || !historyImportsAvailable || !state.present || !state.configured || state.stale ||
        ['ambiguous', 'conflict'].includes(state.associationStatus)) return { kind: 'unknown', label: 'Sub2 待核对' };
    if (state.unknown || state.error || state.exists === false || !(state.imported || state.sub2AccountID)) {
        return { kind: 'unknown', label: 'Sub2 待核对' };
    }
    return { kind: 'present', label: 'Sub2 在池' };
}

function recoveryPopoverVersion(account) {
    return `${account.recoveryCount}/${account.recoveryAttemptCount}/${historyRecoveries.get(account.id)?.updated_at || ''}`;
}

function positionHistoryRecoveryPopover() {
    if (!historyRecoveryPopover || historyRecoveryPopover.hidden || !historyRecoveryPopoverTrigger?.getBoundingClientRect) return;
    const rect = historyRecoveryPopoverTrigger.getBoundingClientRect();
    const width = historyRecoveryPopover.offsetWidth, height = historyRecoveryPopover.offsetHeight;
    const viewportWidth = window.innerWidth, viewportHeight = window.innerHeight;
    historyRecoveryPopover.style.left = `${Math.max(12, Math.min(rect.right - width, viewportWidth - width - 12))}px`;
    historyRecoveryPopover.style.top = `${Math.max(12, Math.min(rect.bottom + 8 + height > viewportHeight - 12 ? rect.top - height - 8 : rect.bottom + 8, viewportHeight - height - 12))}px`;
}

function closeHistoryRecoveryPopover(restoreFocus = false) {
    clearTimeout(historyRecoveryPopoverTimer);
    historyRecoveryPopoverAbort?.abort();
    historyRecoveryPopoverAbort = null;
    const trigger = historyRecoveryPopoverTrigger;
    trigger?.setAttribute('aria-expanded', 'false');
    historyRecoveryPopoverID = '';
    historyRecoveryPopoverTrigger = null;
    historyRecoveryPopoverPinned = false;
    if (historyRecoveryPopover) historyRecoveryPopover.hidden = true;
    if (restoreFocus && trigger?.isConnected !== false) {
        historyRecoveryRestoringFocus = true;
        trigger?.focus({ preventScroll: true });
        historyRecoveryRestoringFocus = false;
    }
}

function scheduleHistoryRecoveryClose() {
    clearTimeout(historyRecoveryPopoverTimer);
    historyRecoveryPopoverTimer = setTimeout(() => {
        if (!historyRecoveryPopoverPinned && !historyRecoveryPopover?.contains(document.activeElement) &&
            document.activeElement !== historyRecoveryPopoverTrigger && !historyRecoveryPopover?.matches?.(':hover') &&
            !historyRecoveryPopoverTrigger?.matches?.(':hover')) closeHistoryRecoveryPopover();
    }, 180);
}

function openHistoryRecoveryPopover(id, trigger) {
    if (!historyRecoveryPopover) return;
    clearTimeout(historyRecoveryPopoverTimer);
    if (historyRecoveryPopoverID === String(id)) return;
    const account = historyAccounts.find(item => item.id === String(id));
    if (!account) return;
    closeHistoryRecoveryPopover();
    historyRecoveryPopoverID = account.id;
    historyRecoveryPopoverTrigger = trigger;
    trigger?.setAttribute('aria-expanded', 'true');
    historyRecoveryPopover.hidden = false;
    historyRecoveryPopoverTitle.textContent = historyAccountName(account);
    historyRecoveryPopoverVersion = recoveryPopoverVersion(account);
    loadHistoryRecoveryRecords();
}

function toggleHistoryRecoveryPopover(id, trigger) {
    if (historyRecoveryPopoverID === String(id) && historyRecoveryPopoverPinned) return closeHistoryRecoveryPopover(true);
    openHistoryRecoveryPopover(id, trigger);
    historyRecoveryPopoverPinned = true;
}

async function loadHistoryRecoveryRecords(preserveContent = false) {
    const id = historyRecoveryPopoverID;
    if (!id) return;
    historyRecoveryPopoverAbort?.abort();
    const controller = new AbortController();
    historyRecoveryPopoverAbort = controller;
    const timeout = setTimeout(() => controller.abort(), 15000);
    if (!preserveContent) historyRecoveryPopoverBody.innerHTML = '<p class="recovery-empty" role="status">正在加载复活记录…</p>';
    positionHistoryRecoveryPopover();
    try {
        const response = await fetch(`/api/history/${encodeURIComponent(id)}`, { headers: { Accept: 'application/json' }, signal: controller.signal });
        const payload = await response.json();
        if (!response.ok) throw new Error(payload.message || `记录读取失败（HTTP ${response.status}）`);
        if (controller !== historyRecoveryPopoverAbort || id !== historyRecoveryPopoverID) return;
        if (payload.recovery_history_available === false || !Array.isArray(payload.recovery_history)) throw new Error('复活记录暂不可用，请稍后重试');
        const listedAccount = historyAccounts.find(item => item.id === id) || {};
        const account = normalizeHistoryAccount({ ...listedAccount, ...(payload.account || {}) });
        const records = [...payload.recovery_history].sort((a, b) => historyTimeValue(b.created_at) - historyTimeValue(a.created_at) || Number(b.id) - Number(a.id));
        const scrollTop = historyRecoveryPopoverBody.scrollTop;
        historyRecoveryPopoverBody.innerHTML = `<p class="recovery-count-summary"><strong>${account.recoveryCount} 次完成</strong><span>${account.recoveryAttemptCount} 次尝试</span></p>
            <p class="recovery-count-hint">仅统计 Sub2 完整恢复任务；首次登录与普通重新登录不计入。${payload.recovery_history_truncated ? '下方仅显示最近 100 条。' : ''}</p>
            ${records.length ? `<ol class="recovery-timeline">${records.map(item => {
                const completed = ['completed', 'success'].includes(item.state);
                return `<li class="${completed ? 'recovery-completed' : ['failed', 'unknown', 'canceled'].includes(item.state) ? 'recovery-stopped' : 'recovery-active'}">
                    <div class="recovery-record-heading"><strong>${escapeHTML(historyRecoveryNames[item.state] || '状态待确认')}</strong><span>#${escapeHTML(String(item.id))}</span></div>
                    <time>${formatHistoryTime(item.created_at)}</time>
                    ${item.updated_at ? `<span class="recovery-record-update">更新 ${formatHistoryTime(item.updated_at)}</span>` : ''}
                    ${item.last_error ? `<p>${escapeHTML(item.last_error)}</p>` : ''}
                </li>`;
            }).join('')}</ol>` : '<p class="recovery-empty">暂无复活记录</p>'}`;
        historyRecoveryPopoverBody.scrollTop = scrollTop;
    } catch (error) {
        if (controller !== historyRecoveryPopoverAbort || id !== historyRecoveryPopoverID) return;
        historyRecoveryPopoverBody.innerHTML = `<p class="recovery-empty" role="status">${escapeHTML(error.name === 'AbortError' ? '记录读取超时，请重试' : error.message || '记录读取失败')}</p><button class="btn-secondary" type="button" data-history-recovery-retry>重新加载</button>`;
    } finally {
        clearTimeout(timeout);
        if (controller === historyRecoveryPopoverAbort) positionHistoryRecoveryPopover();
    }
}

function syncHistoryRecoveryPopover(pageAccounts) {
    if (!historyRecoveryPopoverID) return;
    const account = pageAccounts.find(item => item.id === historyRecoveryPopoverID);
    if (!account) return closeHistoryRecoveryPopover();
    const trigger = historyGrid.querySelector(`[data-history-action="recovery-history"][data-history-id="${CSS.escape(account.id)}"]`);
    if (trigger) {
        historyRecoveryPopoverTrigger = trigger;
        trigger.setAttribute('aria-expanded', 'true');
    }
    const version = recoveryPopoverVersion(account);
    if (version !== historyRecoveryPopoverVersion) {
        historyRecoveryPopoverVersion = version;
        loadHistoryRecoveryRecords(true);
    }
    positionHistoryRecoveryPopover();
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

function quotaWindowText(window, label) {
    if (!window) return `${label} 未提供`;
    const details = [];
    if (window.usedPercent !== null) {
        const used = Math.round(window.usedPercent);
        details.push(`已用 ${used}%`, `剩余 ${Math.max(0, 100 - used)}%`);
    }
    if (window.usedRequests !== null && window.limitRequests !== null) {
        details.push(`请求 ${Math.round(window.usedRequests)}/${Math.round(window.limitRequests)}`);
    }
    if (window.remainingSeconds !== null && window.usedPercent === null) details.push(`剩余 ${formatDuration(window.remainingSeconds)}`);
    const reset = window.resetAt ? `重置 ${formatHistoryTime(window.resetAt)}`
        : window.resetAfterSeconds !== null ? `${formatDuration(window.resetAfterSeconds)}后重置` : '';
    if (reset) details.push(reset);
    return details.length ? `${label} ${details.join(' · ')}` : `${label} 未提供`;
}

function formatDuration(seconds) {
    const value = Math.max(0, Math.round(Number(seconds) || 0));
    if (value >= 86400) return `${Math.floor(value / 86400)}天`;
    if (value >= 3600) return `${Math.floor(value / 3600)}小时`;
    if (value >= 60) return `${Math.floor(value / 60)}分钟`;
    return `${value}秒`;
}

function renderQuotaSummary(account) {
    const quota = account.sub2Status?.quota;
    if (account.sub2Status?.usageUnknown || account.sub2Status?.usageStale) {
        const reason = account.sub2Status.usageError || 'Sub2 额度快照暂不可用';
        return `<span class="history-status sub2-unknown" title="${escapeHTML(reason)}">额度待核对</span>`;
    }
    if (!quota) return '<span class="history-status sub2-unknown">额度未提供</span>';
    const parts = [];
    const compactWindow = (item, label) => {
        if (!item) return '';
        if (item.usedPercent !== null) return `${label} ${Math.round(item.usedPercent)}%`;
        if (item.usedRequests !== null && item.limitRequests !== null) return `${label} ${Math.round(item.usedRequests)}/${Math.round(item.limitRequests)}`;
        return `${label} —`;
    };
    for (const [item, label] of [[quota.fiveHour, '5h'], [quota.sevenDay, '7d'], [quota.sevenDaySonnet, 'Sonnet'], [quota.sevenDayFable, 'Fable'], [quota.thirtyDay, '30d']]) {
        if (item) parts.push(compactWindow(item, label));
    }
    if (quota.credits) {
        const credits = quota.credits.unlimited === true ? '无限' : quota.credits.balance || (quota.credits.hasCredits === false ? '0' : '—');
        parts.push(`credits ${credits}`);
    }
    if (!parts.length) return '<span class="history-status sub2-unknown">额度未提供</span>';
    const title = [quotaWindowText(quota.fiveHour, '5 小时'), quotaWindowText(quota.sevenDay, '7 天'), quotaWindowText(quota.sevenDaySonnet, '7 天 Sonnet'), quotaWindowText(quota.sevenDayFable, '7 天 Fable'), quotaWindowText(quota.thirtyDay, '30 天'), quota.credits ? `Credits ${quota.credits.unlimited === true ? '无限' : quota.credits.balance || '未提供'}` : '', quota.updatedAt ? `同步 ${formatHistoryTime(quota.updatedAt)}` : '']
        .filter(Boolean).join(' · ');
    return `<span class="history-quota-summary" title="${escapeHTML(title)}">${parts.map(part => `<span>${escapeHTML(part)}</span>`).join('')}</span>`;
}

function historyRecentCostKey(account) {
    const binding = historyImports.get(account.id);
    return JSON.stringify([historySub2Configured, binding?.sub2_account_id || account.sub2Status?.sub2AccountID || '',
        binding?.operation_id || '', account.status === 'deleted', sub2AutomationBlock(account)?.code || '']);
}

function historyRecentCost(account) {
    const entry = historyRecentCosts.get(account.id);
    return entry?.key === historyRecentCostKey(account) ? entry.stats : null;
}

function recentCostHasAmount(stats) {
    return !stats?.unknown && typeof stats?.total_cost === 'number' && Number.isFinite(stats.total_cost) && stats.total_cost >= 0;
}

async function loadHistoryCosts(pageAccounts) {
    if (!historySub2Configured) return;
    const requested = [], waiting = new Set();
    for (const account of pageAccounts.slice(0, 100)) {
        if (sub2AutomationBlock(account)) continue;
        const key = historyRecentCostKey(account);
        const inflight = historyRecentCostInflight.get(account.id);
        if (inflight?.key === key) { waiting.add(inflight.promise); continue; }
        const previous = historyRecentCosts.get(account.id);
        if (previous?.key === key && previous.expiresAt > Date.now()) continue;
        requested.push({ id: account.id, key, previous: previous?.key === key ? previous.stats : null });
    }
    if (requested.length) {
        const promise = Promise.resolve().then(async () => {
            let stats;
            try {
                const response = await fetch(`/api/history/usage?account_ids=${requested.map(item => encodeURIComponent(item.id)).join(',')}`, {
                    headers: { 'Accept': 'application/json' }, signal: AbortSignal.timeout(12000)
                });
                const payload = await response.json();
                if (!response.ok || payload.success !== true || !payload.stats || typeof payload.stats !== 'object') throw new Error('usage_unavailable');
                stats = payload.stats;
            } catch (_) { /* Keep the last verified amount when a read fails. */ }
            for (const item of requested) {
                const current = historyAccounts.find(account => account.id === item.id);
                if (!current || historyRecentCostKey(current) !== item.key || historyRecentCostInflight.get(item.id)?.promise !== promise) continue;
                let value = stats?.[item.id];
                if (!recentCostHasAmount(value)) {
                    const failed = !value || Boolean(value.error) || value.unknown !== true;
                    value = recentCostHasAmount(item.previous)
                        ? { ...item.previous, stale: true, error: 'Sub2 近90天消耗暂不可用' }
                        : { unknown: true, error: failed ? 'Sub2 近90天消耗暂不可用' : '' };
                }
                historyRecentCosts.set(item.id, { key: item.key, stats: value, expiresAt: Date.now() + historyRecentCostTTL });
            }
        });
        for (const item of requested) historyRecentCostInflight.set(item.id, { key: item.key, promise });
        waiting.add(promise.finally(() => {
            for (const item of requested) {
                if (historyRecentCostInflight.get(item.id)?.promise === promise) historyRecentCostInflight.delete(item.id);
            }
            renderHistory();
        }));
    }
    await Promise.all(waiting);
}

function renderHistoryCost(account, detail = false) {
    const stats = historyRecentCost(account);
    const known = recentCostHasAmount(stats);
    const amount = !known ? '—' : stats.total_cost > 0 && stats.total_cost < 0.01 ? '<$0.01'
        : `$${stats.total_cost.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
    const state = !historySub2Configured ? 'Sub2 未配置' : sub2AutomationBlock(account) ? '关联不可用' : !stats ? '加载中' : !known ? '待核对' : stats.stale ? '上次数据' : '';
    const note = 'Sub2 账号统计费用（USD），近 90 天；按 Sub2 保留的用量记录统计，不是官方账单';
    const time = stats?.checked_at ? `同步 ${formatHistoryTime(stats.checked_at)}` : '';
    if (detail) {
        const counts = known && Number.isSafeInteger(stats.total_requests) && Number.isSafeInteger(stats.total_tokens)
            ? `<br>${stats.total_requests.toLocaleString('en-US')} 次请求 · ${stats.total_tokens.toLocaleString('en-US')} Token` : '';
        return `<div class="history-detail-stat">近90天账号费用<br><strong>${escapeHTML(amount)}${state ? ` · ${escapeHTML(state)}` : ''}</strong>${time ? `<br>${time}` : ''}${counts}<br><span class="hint">${escapeHTML(note)}</span></div>`;
    }
    return `<span class="history-cost-summary${state ? ' is-pending' : ''}" title="${escapeHTML(note)}${time ? ` · ${time}` : ''}">近90天 ${escapeHTML(amount)}${state ? ` <span>${escapeHTML(state)}</span>` : ''}</span>`;
}

function discardedHistoryAccount(account) {
    return account?.status === 'discarded' || account?.sub2Policy === 'discarded';
}

function terminalHistoryAccount(account) {
    return discardedHistoryAccount(account) || Boolean(account && officialAccountStatus(account));
}

function discardReason(account) {
    const event = account.lifecycleEvents?.find(item => item.status === 'discarded');
    if ([event?.code, account.lastErrorCode].includes('invalid_username_or_password')) return '账密被拒绝';
    return event?.reason || account.lastError || '未提供废弃原因';
}

function officialAccountStatus(account) {
    if (discardedHistoryAccount(account)) return '';
    const result = historyChecks.get(account.id)?.last_result;
    const currentResult = result?.freshness === 'current' ? result : null;
    const values = [account.status, currentResult?.outcome, currentResult?.error_code, currentResult?.stream_error_code]
        .map(value => String(value || '').toLowerCase());
    if (values.some(value => ['deleted', 'account_deleted', 'user_deleted'].includes(value))) return 'deleted';
    if (values.some(value => ['deactivated', 'account_deactivated', 'user_deactivated', 'account_disabled', 'user_disabled'].includes(value))) return 'deactivated';
    if (values.includes('deleted_or_deactivated') || account.sub2Policy === 'official_unavailable') return 'deleted_or_deactivated';
    return '';
}

function renderAccountStatusSource(account) {
    if (discardedHistoryAccount(account)) return '<span class="history-source neutral">本地废弃</span>';
    const labels = [];
    const official = officialAccountStatus(account);
    if (official === 'deleted') labels.push('<span class="history-source official">官方删除</span>');
    else if (official === 'deactivated') labels.push('<span class="history-source official">官方停用</span>');
    else if (official === 'deleted_or_deactivated') labels.push('<span class="history-source official">官方删除或停用</span>');
    const state = account.sub2Status || {};
    if (sub2AutomationBlock(account)?.code === 'missing') labels.push('<span class="history-source sub2">Sub2 关联不存在</span>');
    if (labels.length) return labels.join('');
    if (state.stale || state.unknown || state.associationStatus === 'ambiguous' || state.associationStatus === 'conflict') return '<span class="history-source unknown">来源待核对</span>';
    if (state.associationStatus === 'unmatched' || (!state.imported && !state.sub2AccountID)) return '<span class="history-source neutral">未交付 Sub2</span>';
    return '<span class="history-source neutral">未发现删除</span>';
}

function renderSub2Status(account) {
    const state = account.sub2Status || normalizeSub2Status(null);
    const block = sub2AutomationBlock(account);
    if (block) return `<span class="history-status sub2-unknown">${escapeHTML(block.label)}</span>`;
    if (!historySub2Configured) return '<span class="history-status sub2-unknown">Sub2 未配置</span>';
    if (!state.present) return '<span class="history-status sub2-unknown">Sub2 状态待核对</span>';
    if (!state.configured) return '<span class="history-status sub2-unknown">Sub2 未配置</span>';
    const association = { unmatched: '未找到关联', ambiguous: '多条匹配待确认', conflict: '身份不一致' }[state.associationStatus];
    if (association) return `<span class="history-status sub2-unknown" title="${escapeHTML(state.error || '')}">Sub2 ${association}</span>`;
    if (state.stale || state.unknown) return `<span class="history-status sub2-unknown"${state.errorMessage || state.error ? ` title="${escapeHTML(state.errorMessage || state.error)}"` : ''}>Sub2 状态未知${state.checkedAt ? ` · 同步 ${formatHistoryTime(state.checkedAt)}` : ''}</span>`;
    if (!state.imported && !state.sub2AccountID) return '<span class="history-status sub2-unknown">Sub2 状态待核对</span>';
    if (!state.exists) return '<span class="history-status sub2-unknown">Sub2 状态待核对</span>';
    const status = state.status;
    const conflict = localAuthFailure(account) && ['active', 'available', 'enabled'].includes(status);
    const runtimePause = ['active', 'available', 'enabled'].includes(status) ? sub2RuntimePause(state) : null;
    const effective = state.effectiveSchedulable ?? state.schedulable;
    const className = conflict || runtimePause || effective === false ? 'sub2-warning' : status === 'active' || status === 'available' || status === 'enabled' ? 'sub2-ok'
        : status === 'unknown' || status === 'stale' ? 'sub2-unknown' : 'sub2-error';
    const manuallyPaused = ['active', 'available', 'enabled'].includes(status) && state.schedulable === false;
    const schedule = manuallyPaused ? '人工暂停' : runtimePause ? '暂不可调度' : effective === true ? '可调度' : effective === false ? '暂不可调度' : '';
    const checked = state.checkedAt ? `同步 ${formatHistoryTime(state.checkedAt)}` : '';
    const reason = [state.errorMessage, runtimePause?.reason, state.error, checked].filter(Boolean).join(' · ');
    const title = reason ? ` title="${escapeHTML(reason)}"` : '';
    const label = runtimePause ? runtimePause.label : conflict ? '正常 · 本地凭据失效待处理' : escapeHTML(sub2StatusName(status));
    const suffix = conflict && runtimePause ? ' · 本地凭据失效待处理' : '';
    return `<span class="history-status ${className}"${title}>Sub2 ${label}${suffix}${schedule ? ` · ${schedule}` : ''}</span>`;
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
        discarded: '账号已废弃',
        unknown: '暂无状态'
    }[status] || status || '暂无状态';
}

const historyRecoveryNames = {
    queued: '等待恢复', validating: '正在核对账号', disabling_schedule: '正在暂停调度', logging_in: '正在重新登录',
    login_succeeded: '重新登录成功', identity_verified: '身份已核对', refreshing_credentials: '正在更新凭据',
    applying_credentials: '正在写回 Sub2', credentials_applied: '凭据已写回 Sub2', enabling_schedule: '正在恢复调度',
    completed: '恢复完成', success: '恢复完成', failed: '恢复失败', unknown: '恢复结果待核对', canceled: '恢复已取消'
};

const historyFailureStageNames = {
    precheck: '前置检测', validating: '核对账号', disabling_schedule: '暂停调度',
    logging_in: '重新登录', login_succeeded: '登录成功', identity_verified: '身份核对',
    refreshing_credentials: '刷新凭据', applying_credentials: '写回凭据',
    credentials_applied: '凭据已写回', enabling_schedule: '恢复调度', verifying: '验证 Sub2',
    applying_groups: '写入分组'
};

function recoveryGroupIDs(value) {
    const source = Array.isArray(value) ? value : [];
    return [...new Set(source.map(item => Number(item)).filter(item => Number.isSafeInteger(item) && item >= 0))].sort((left, right) => left - right);
}

function recoveryTaskDiagnostic(task) {
    const source = task || {};
    const diagnostic = source.diagnostic || source.diagnostics || source.group_conflict || {};
    const current = recoveryGroupIDs(source.current_group_ids ?? source.currentGroupIDs ?? diagnostic.current_group_ids ?? diagnostic.currentGroupIDs);
    const target = recoveryGroupIDs(source.target_group_ids ?? source.targetGroupIDs ?? diagnostic.target_group_ids ?? diagnostic.targetGroupIDs);
    const taskID = Number(source.task_id ?? source.taskId ?? source.recovery_task_id ?? source.recoveryTaskID ?? source.id ?? 0);
    const httpStatus = Number(source.http_status ?? source.httpStatus ?? 0);
    return {
        taskID: Number.isSafeInteger(taskID) && taskID > 0 ? taskID : 0,
        stage: historyFailureStageNames[String(source.failure_stage ?? source.failureStage ?? '').toLowerCase()] || '',
        code: String(source.error_code ?? source.errorCode ?? '').trim(),
        httpStatus: Number.isInteger(httpStatus) && httpStatus > 0 ? httpStatus : 0,
        currentGroupIDs: current,
        targetGroupIDs: target
    };
}

function isGroupConflictTask(task) {
    if (!task || task.requires_action !== true) return false;
    const diagnostic = recoveryTaskDiagnostic(task);
    return String(task.error_code ?? task.errorCode ?? '').toLowerCase() === 'group_conflict' ||
        diagnostic.currentGroupIDs.length > 0 && diagnostic.targetGroupIDs.length > 0;
}

function groupConflictTaskForAccount(account) {
    if (!account) return null;
    const candidates = [historyDeliveries.get(account.id), historyRecoveries.get(account.id), historyRechecks.get(account.id)];
    return candidates.find(isGroupConflictTask) || null;
}

function recoveryDiagnosticParts(task) {
    if (!task) return [];
    const diagnostic = recoveryTaskDiagnostic(task);
    const parts = [];
    if (diagnostic.taskID) parts.push(`任务 #${diagnostic.taskID}`);
    if (diagnostic.stage) parts.push(`阶段：${diagnostic.stage}`);
    if (diagnostic.code) parts.push(`分类：${diagnostic.code}`);
    if (diagnostic.httpStatus) parts.push(`HTTP ${diagnostic.httpStatus}`);
    if (diagnostic.currentGroupIDs.length || diagnostic.targetGroupIDs.length) {
        parts.push(`当前分组：${diagnostic.currentGroupIDs.length ? diagnostic.currentGroupIDs.join('、') : '无'}；目标分组：${diagnostic.targetGroupIDs.length ? diagnostic.targetGroupIDs.join('、') : '无'}`);
    }
    return parts;
}

function renderGroupConflictActions(account, task, source = 'attention') {
    if (!isGroupConflictTask(task)) return '';
    const diagnostic = recoveryTaskDiagnostic(task);
    if (!diagnostic.taskID) return '';
    const requestKey = String(diagnostic.taskID);
    const pending = historyGroupActionRequests.has(requestKey);
    const disabled = historyActionsLocked() || pending;
    const prefix = source === 'attention' ? 'data-attention-group' : 'data-history-group';
    return `<div class="history-attention-actions history-group-conflict-actions" data-group-conflict-task="${diagnostic.taskID}">
        <button class="btn-secondary" type="button" ${prefix}-action="keep_current" ${prefix}-task="${diagnostic.taskID}" ${prefix}-account="${escapeHTML(account.id)}" ${disabled ? 'disabled' : ''}>${pending ? '处理中…' : '保留当前分组'}</button>
        <button class="btn-secondary" type="button" ${prefix}-action="apply_target" ${prefix}-task="${diagnostic.taskID}" ${prefix}-account="${escapeHTML(account.id)}" ${disabled ? 'disabled' : ''}>${pending ? '处理中…' : '按目标分组修正'}</button>
    </div>`;
}

function groupConflictTaskForID(taskID, accountID = '') {
    const wanted = Number(taskID);
    if (!Number.isSafeInteger(wanted) || wanted <= 0) return null;
    const account = historyAccounts.find(item => item.id === String(accountID));
    const candidates = account ? [historyDeliveries.get(account.id), historyRecoveries.get(account.id), historyRechecks.get(account.id)] :
        [...historyDeliveries.values(), ...historyRecoveries.values(), ...historyRechecks.values()];
    return candidates.find(task => recoveryTaskDiagnostic(task).taskID === wanted && isGroupConflictTask(task)) || null;
}

async function resolveGroupConflict(task, action, trigger = null) {
    const diagnostic = recoveryTaskDiagnostic(task);
    if (!diagnostic.taskID || !['keep_current', 'apply_target'].includes(action) || historyActionsLocked()) return;
    if (action === 'apply_target' && !await confirmAction({
        title: '按目标分组修正',
        message: `将重新核对任务 #${diagnostic.taskID} 的 Sub2 账号，并尝试写入目标分组（${diagnostic.targetGroupIDs.join('、') || '无'}）。远端分组发生变化时操作会被拒绝。`,
        confirmLabel: '确认修正'
    })) return;
    const requestKey = String(diagnostic.taskID);
    if (historyGroupActionRequests.has(requestKey)) return;
    historyGroupActionRequests.add(requestKey);
    if (trigger) trigger.disabled = true;
    renderHistory();
    try {
        const response = await fetch(`/api/account-recovery/${encodeURIComponent(requestKey)}/groups`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
            body: JSON.stringify({
                action,
                current_group_ids: diagnostic.currentGroupIDs,
                target_group_ids: diagnostic.targetGroupIDs
            }),
            signal: AbortSignal.timeout(15000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `分组操作失败（HTTP ${response.status}）`);
        historyBatchSummary = action === 'keep_current' ? '已保留当前分组，后台将继续恢复任务。' : '已提交目标分组修正，后台将重新核对并继续恢复。';
        await loadHistory();
    } catch (error) {
        historyBatchSummary = error.name === 'TimeoutError' ? '分组操作请求超时，请刷新历史确认结果。' : (error.message || '分组操作失败，请刷新后重试');
    } finally {
        historyGroupActionRequests.delete(requestKey);
        renderHistory();
    }
}

function automaticTaskPolicy(task, kind = '恢复') {
    if (!task || ['completed', 'success', 'canceled'].includes(task.state)) return '';
    const blocked = deterministicStopReason(task);
    if (blocked) return `自动处理已暂停 · ${blocked}`;
    if (task.requires_action === true || task.retry_action === 'manual') return [`自动恢复已停止：${task.manual_action || task.last_error || '请查看详情'}`, ...recoveryDiagnosticParts(task)].join(' · ');
    const action = { resume: `继续${kind}`, relogin: '重新登录' }[task.retry_action] || `继续${kind}`;
    if (!task.next_retry_at) return '';
    const retryAt = Date.parse(task.next_retry_at);
    return Number.isFinite(retryAt) && retryAt <= Date.now()
        ? '已到重试时间，等待后台执行'
        : `下次自动${action}：${formatHistoryTime(task.next_retry_at)}`;
}

function deliveryStatusText(task) {
    if (!task) return '交付状态暂未确认，请在账号历史查看持久记录';
    if (!['completed', 'canceled'].includes(task.state) && deterministicStopReason(task)) return `自动处理已暂停 · ${deterministicStopReason(task)}`;
    if (task.blocked_reason && !['completed', 'canceled'].includes(task.state)) return `自动交付已暂停 · ${task.blocked_reason}`;
    const stages = { associating: '正在关联账号', importing: '正在导入 Sub2', applying: '正在写回凭据', verifying: '正在验证 Sub2 可用性', enabling: '正在恢复调度' };
    const states = { queued: '等待自动交付', checking: '正在核对 Sub2 账号', importing: '正在导入 Sub2', verifying: '正在验证 Sub2 可用性', working: stages[task.stage] || '正在自动交付', retry_wait: '等待自动重试', completed: 'Sub2 交付完成', requires_action: '交付需要处理', canceled: '交付已取消' };
    return [states[task.state] || '交付状态待核对', task.state === 'retry_wait' ? task.last_error : '', automaticTaskPolicy(task, '交付')].filter(Boolean).join(' · ');
}

function syncLoginDeliveries() {
    for (const account of accounts) {
        if (!account.autoDeliver || account.status !== 'success') continue;
        if (!account.historyID) {
            const matches = historyAccounts.filter(item => item.email.toLowerCase() === account.email.toLowerCase());
            if (matches.length === 1) account.historyID = matches[0].id;
        }
        account.delivery = historyDeliveries.get(String(account.historyID)) || null;
    }
    renderStatus();
}

function reconcileStoppedAccounts() {
    for (const account of accounts.filter(item => item.status === 'checking')) {
        const row = historyAccounts.find(item => item.email.toLowerCase() === account.email.toLowerCase());
        if (!row || !['active', 'failed', 'error', 'interrupted'].includes(row.status)) continue;
        // History timestamps have one-second precision; the attempt count also
        // guards against mistaking a pre-existing row for this canceled login.
        const startedAt = Math.floor(account.startTime / 1000) * 1000;
        const lastAttemptAt = Date.parse(row.lastAttemptAt);
        if (!Number.isFinite(lastAttemptAt) || lastAttemptAt < startedAt ||
            row.attemptCount <= (account.historyAttemptCount ?? -1)) continue;
        if (row.status === 'active' && Date.parse(row.lastSuccessAt) >= startedAt) {
            account.status = 'success';
            account.historyID = row.id;
            account.message = '登录结果已保存，请在账号历史查看；令牌未返回本页';
        } else if (row.status !== 'active') {
            account.status = 'stopped';
            account.message = '登录已结束，历史中没有本次成功结果，可重试';
        }
    }
    renderStatus();
    updateStats();
}

function loginManualAction(account) {
    const blocked = automationStopped(account);
    if (blocked) return blocked.label;
    if (historyRepairs.get(account.id)?.state === 'recovery_queued') return '';
    if (account.requiresAction && ['error', 'failed'].includes(account.status)) return account.manualAction || account.lastError || '请查看账号详情';
    if (!['error', 'failed'].includes(account.status)) return '';
    if (['invalid_password', 'incorrect_password'].includes(account.lastErrorCode)) return '请更新账号密码后重新登录';
    if (['invalid_mfa', 'invalid_totp'].includes(account.lastErrorCode)) return '验证码被拒绝，验证类型待确认';
    return '';
}

function supersededByRepair(task, repair) {
    return Boolean(repair?.updated_at && repair.state !== 'canceled' && task &&
        new Date(task.updated_at || 0).getTime() <= new Date(repair.updated_at || 0).getTime());
}

function repairStatusText(task) {
    if (deterministicStopReason(task)) return `自动处理已暂停 · ${deterministicStopReason(task)}`;
    if (task.requires_action) return `更新资料后需要处理：${task.manual_action || task.last_error || '请查看账号详情'}`;
    const labels = { queued: '资料已保存，等待后台继续', checking: '正在使用更新后的资料重新登录', recovery_queued: '资料已保存，等待继续恢复', retry_wait: '资料已保存，等待自动重试', completed: '资料更新任务已完成' };
    return `${labels[task.state] || '资料更新任务状态待核对'}${task.next_retry_at ? ` · 下次继续：${formatHistoryTime(task.next_retry_at)}` : ''}`;
}

function renderHistoryAttention() {
    if (!historyAttention) return;
    const notices = historyAccounts.filter(account => !terminalHistoryAccount(account) && account.sub2Policy !== 'free_local').map(account => {
        const { recovery, delivery, recheck, repair } = historyAccountTasks(account);
        const tasks = [recovery?.purpose === 'delivery' && delivery ? null : recovery, delivery, recheck]
            .filter(task => !supersededByRepair(task, repair)).concat(repair)
            .filter(task => task?.requires_action === true && !['completed', 'success', 'canceled'].includes(task.state));
        const loginReason = supersededByRepair({ updated_at: account.updatedAt }, repair) ? '' : loginManualAction(account);
        const reasons = [...new Set([...tasks.flatMap(task => [task.manual_action || task.last_error || '请查看任务详情', ...recoveryDiagnosticParts(task)]), loginReason].filter(Boolean))];
        const groupTask = groupConflictTaskForAccount(account);
        return { account, reasons, groupTask, stopped: automationStopped(account) };
    }).filter(item => item.reasons.length);
    const focusedID = document.activeElement?.dataset?.attentionAccount;
    const focusedEditID = document.activeElement?.dataset?.attentionEdit;
    historyAttention.hidden = notices.length === 0;
    historyAttentionSummary.textContent = `需要处理 ${notices.length} 个账号${historyLoadError ? '（上次记录，待刷新）' : ''}`;
    historyAttentionList.innerHTML = notices.map(({ account, reasons, groupTask, stopped }) => `<li><div><strong>${escapeHTML(historyAccountName(account))}</strong><p>${reasons.map(escapeHTML).join('；')}</p></div><div class="history-attention-actions">${stopped ? '' : `<button class="btn-secondary" type="button" data-attention-edit="${escapeHTML(account.id)}" ${historyActionsLocked() ? 'disabled' : ''}>更新资料</button>`}<button class="btn-secondary" type="button" data-attention-account="${escapeHTML(account.id)}">查看账号</button></div>${stopped ? '' : renderGroupConflictActions(account, groupTask)}</li>`).join('');
    if (focusedEditID) historyAttentionList.querySelector(`[data-attention-edit="${CSS.escape(focusedEditID)}"]`)?.focus({ preventScroll: true });
    if (focusedID) historyAttentionList.querySelector(`[data-attention-account="${CSS.escape(focusedID)}"]`)?.focus({ preventScroll: true });
}

function renderHistoryAutomation(account) {
    const stopped = automationStopped(account);
    if (stopped) return `<div class="history-automation requires-action">${escapeHTML(stopped.label)}${stopped.at ? ` · ${formatHistoryTime(stopped.at)}` : ''}</div>`;
    const { repair, delivery, recovery, recheck } = historyAccountTasks(account);
    const lines = [];
    if (repair && !['completed', 'canceled'].includes(repair.state)) lines.push(`<div class="history-automation${repair.requires_action ? ' requires-action' : ''}">${escapeHTML(repairStatusText(repair))}</div>`);
    if (delivery) lines.push(`<div class="history-automation${delivery.requires_action ? ' requires-action' : ''}">交付：${escapeHTML(deliveryStatusText(delivery))}</div>`);
    const policy = recovery?.purpose === 'delivery' && delivery ? '' : automaticTaskPolicy(recovery);
    if (policy) lines.push(`<div class="history-automation${recovery.requires_action ? ' requires-action' : ''}">${escapeHTML(policy)}</div>`);
    if (recheck && !['passed', 'canceled'].includes(recheck.state)) {
        const label = recheck.requires_action ? `复检需要处理：${recheck.manual_action || recheck.last_error || '请查看账号详情'}`
            : recheck.state === 'recovery_queued' ? '延迟复检确认凭据失效，已自动恢复'
            : recheck.state === 'checking' ? '正在执行延迟复检'
            : `第 ${Number(recheck.round) || 1} 次延迟复检：${recheck.next_check_at ? formatHistoryTime(recheck.next_check_at) : '等待检查'}`;
        lines.push(`<div class="history-automation${recheck.requires_action ? ' requires-action' : ''}">${escapeHTML(label)}</div>`);
        if (recheck.state === 'requires_action' && recheck.requires_action && Number.isSafeInteger(Number(recheck.task_id)) && Number(recheck.task_id) > 0) {
            lines.push(`<button class="btn-secondary" type="button" data-history-action="recheck" data-history-id="${escapeHTML(account.id)}" data-recheck-task="${Number(recheck.task_id)}" ${historyActionsLocked() || historyRecoveryRequests.has(account.id) ? 'disabled' : ''}>${historyRecoveryRequests.has(account.id) ? '提交中…' : '重新复检'}</button>`);
        }
    }
    const pendingImport = historyImports.get(account.id);
    if (['unknown', 'confirming'].includes(pendingImport?.state)) {
        lines.push(`<div class="history-automation requires-action">导入结果待确认：${escapeHTML(pendingImport.last_error || '后台正在与 Sub2 核对，请勿重复导入。')} <button class="btn-secondary" type="button" data-history-action="reconcile" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || !pendingImport.id ? 'disabled' : ''}>重新核对</button></div>`);
    }
    return lines.join('');
}

function historyRecoveryActive(task) {
    return Boolean(task && Object.hasOwn(historyRecoveryNames, task.state) && !['completed', 'success', 'failed', 'unknown', 'canceled'].includes(task.state));
}

function historyRecoveryAction(account) {
    const blocked = sub2AutomationBlock(account);
    if (blocked || terminalHistoryAccount(account)) return { visible: false, disabled: true, label: '查看详情', reason: blocked?.label || '官方账号已删除或停用，自动处理已终止', resume: false };
    const state = account.sub2Status;
    const { recovery: task, delivery } = historyAccountTasks(account);
    const loginPaused = automationStopped(account)?.code === 'login';
    if (delivery && ['queued', 'checking', 'importing', 'verifying', 'working', 'retry_wait'].includes(delivery.state) && !(loginPaused && delivery.state === 'retry_wait')) {
        return { visible: true, disabled: true, label: delivery.state === 'retry_wait' ? '等待自动交付' : '交付中…', reason: deliveryStatusText(delivery), resume: false };
    }
    if (task?.purpose === 'delivery' && delivery?.requires_action && !deterministicStopReason(delivery) && !deterministicStopReason(task)) return { visible: false, disabled: true, reason: deliveryStatusText(delivery), resume: false };
    const resumable = task?.resumable && ['failed', 'unknown'].includes(task.state);
    const checking = ['queued', 'running'].includes(historyChecks.get(account.id)?.latest_task?.state);
    const linked = state.present && (state.imported || Boolean(state.sub2AccountID));
    let reason = '';
    if (!historySub2Configured || !linked) reason = '尚未关联 Sub2 账号';
    else if (state.stale || state.unknown) reason = 'Sub2 状态未知，请刷新后重试';
    else if (!state.exists) reason = 'Sub2 状态待核对';
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
    const stopped = automationStopped(account);
    if (stopped) return `<div class="history-task-status">${escapeHTML(stopped.label)}${stopped.at ? ` · ${formatHistoryTime(stopped.at)}` : ''}${historyRecoveryNotices.has(account.id) ? ` · ${escapeHTML(historyRecoveryNotices.get(account.id))}` : ''}</div>`;
    const summary = historyChecks.get(account.id);
    const latest = summary?.latest_task;
    const result = summary?.last_result;
    const { recovery: latestRecovery, delivery } = historyAccountTasks(account);
    const recovery = latestRecovery?.purpose === 'delivery' ? null : latestRecovery;
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
    if (recovery) lines.push(`${historyRecoveryNames[recovery.state] || '恢复状态待确认'}${recovery.last_error ? `：${recovery.last_error}` : ''}${recovery.updated_at ? ` · ${formatHistoryTime(recovery.updated_at)}` : ''}${recovery.requires_action ? ` · ${recoveryDiagnosticParts(recovery).join(' · ')}` : ''}`);
    if (delivery?.last_error && delivery.state !== 'completed') lines.push(`交付：${delivery.last_error}`);
    if (historyRecoveryNotices.has(account.id)) lines.push(historyRecoveryNotices.get(account.id));
    return lines.map(line => `<div class="history-task-status">${escapeHTML(line)}</div>`).join('');
}

async function checkAndRecoverHistory(id) {
    const key = String(id), account = historyAccounts.find(item => item.id === key);
    if (!account || historyActionsLocked()) return;
    const action = historyRecoveryAction(account);
    if (action.disabled) return;
    const previousTask = historyRecoveries.get(key);
    historyRecoveryRequests.add(key);
    historyRecoveryNotices.delete(key);
    renderHistory();
    try {
        const response = await fetch(action.resume ? '/api/account-recovery' : '/api/account-recovery/check', {
            method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
            body: JSON.stringify({ account_id: Number(key), manual_attempt: true,
                expected_task_id: Number(previousTask?.id || 0), expected_retry_count: Number(previousTask?.retry_count || 0) }), signal: AbortSignal.timeout(30000)
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

async function retryHistoryRecheck(id, taskID) {
    const key = String(id), task = historyRechecks.get(key);
    const account = historyAccounts.find(item => item.id === key);
    if (!account || automationStopped(account)) return;
    if (historyActionsLocked() || historyRecoveryRequests.has(key) || task?.state !== 'requires_action' || !task.requires_action ||
        !Number.isSafeInteger(Number(taskID)) || Number(taskID) <= 0 || Number(task.task_id) !== Number(taskID)) return;
    historyRecoveryRequests.add(key);
    historyRecoveryNotices.delete(key);
    renderHistory();
    try {
        const response = await fetch(`/api/account-recovery/${Number(taskID)}/recheck`, {
            method: 'POST', headers: { Accept: 'application/json' }, signal: AbortSignal.timeout(15000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok || payload.success === false) throw new Error(payload.message || `复检提交失败（HTTP ${response.status}）`);
        await loadHistory();
    } catch (error) {
        historyRecoveryNotices.set(key, error.name === 'TimeoutError' || error instanceof TypeError
            ? '提交结果暂未确认，请刷新核对复检进度。' : error.message || '复检提交失败');
    } finally {
        historyRecoveryRequests.delete(key);
        historyRefreshAt = Date.now() + 5000;
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
    const planFilter = historyPlanFilter?.value || 'all';
    const filtered = historyAccounts.filter(account => {
        const searchable = `${historyAccountName(account)} ${account.email} ${account.id}`.toLowerCase();
        if (!searchable.includes(query)) return false;
        if (planFilter !== 'all' && (String(account.planType || '').toLowerCase() || 'unknown') !== planFilter) return false;
        if (filter === 'all') return true;
        if (discardedHistoryAccount(account)) return filter === 'discarded';
        if (['attention', 'schedulable', 'working', 'unknown'].includes(filter)) return historyAccountMatches(account, filter);
        if (filter === 'available') return ['active', 'success'].includes(account.status);
        if (filter === 'failed') return ['error', 'failed'].includes(account.status);
        if (filter === 'deactivated') return ['deleted', 'deactivated', 'deleted_or_deactivated'].includes(account.status);
        if (filter === 'processing') return ['running', 'pending'].includes(account.status);
        if (filter === 'expired') return historyExpiryState(account).kind === 'expired';
        return !['active', 'success', 'error', 'failed', 'deleted', 'deactivated', 'deleted_or_deactivated', 'running', 'pending'].includes(account.status);
    });
    return filtered.sort(compareHistoryAccounts);
}

function historyAccountTasks(account) {
    const repair = historyRepairs.get(account.id);
    const current = task => supersededByRepair(task, repair) || loginTaskSuperseded(account, task) ? null : task;
    return { repair, delivery: current(historyDeliveries.get(account.id)), recovery: current(historyRecoveries.get(account.id)), recheck: current(historyRechecks.get(account.id)) };
}

function historyRecoveryRetrying(task) {
    return Boolean(task?.next_retry_at && !task.requires_action && ['failed', 'unknown'].includes(task.state));
}

function historyAccountMatches(account, filter) {
    if (terminalHistoryAccount(account) || account.sub2Policy === 'free_local') return false;
    const { delivery, recovery, recheck, repair } = historyAccountTasks(account);
    const check = historyChecks.get(account.id)?.latest_task;
    if (filter === 'working' && automationStopped(account)) return false;
    if (filter === 'working') return ['running', 'pending'].includes(account.status) ||
        ['queued', 'running'].includes(check?.state) || historyRecoveryActive(recovery) || historyRecoveryRetrying(recovery) ||
        ['pending', 'checking', 'recovery_queued'].includes(recheck?.state) ||
        ['queued', 'checking', 'recovery_queued', 'retry_wait'].includes(repair?.state) ||
        ['queued', 'checking', 'importing', 'verifying', 'working', 'retry_wait'].includes(delivery?.state);
    if (filter === 'attention') return Boolean(sub2AutomationBlock(account)) || [delivery, recovery, recheck, repair]
        .some(task => task?.requires_action && !['completed', 'success', 'canceled'].includes(task.state)) ||
        (repair?.state !== 'recovery_queued' && !supersededByRepair({ updated_at: account.updatedAt }, repair) &&
            (['error', 'failed', 'deleted', 'deactivated', 'deleted_or_deactivated'].includes(account.status) || localAuthFailure(account))) ||
        (historyPoolState(account).kind === 'present' && account.sub2Status.status === 'error');
    const pool = historyPoolState(account);
    if (filter === 'unknown') return pool.kind === 'unknown';
    const state = account.sub2Status;
    return pool.kind === 'present' && ['active', 'available', 'enabled'].includes(state.status) &&
        !sub2RuntimePause(state) && (state.effectiveSchedulable ?? state.schedulable) === true;
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

function canReloginHistory(account) {
    return Boolean(account) && !terminalHistoryAccount(account) && account.status !== 'running';
}

function canReimportRemoteMissing(account) {
    return Boolean(account) && account.status !== 'running' && !terminalHistoryAccount(account)
        && account.sub2Policy !== 'free_local' && historyImportState(account) === 'remote_missing' && sub2AutomationBlock(account)?.code === 'missing';
}

function historyReloginDelivers(account) {
    return !['missing', 'free_local'].includes(sub2AutomationBlock(account)?.code) && !terminalHistoryAccount(account) && historyDeliveries.get(account.id)?.requires_action === true;
}

function canBatchRelogin(account) {
    return canReloginHistory(account) && ['failed', 'error', 'interrupted', 'deactivated', 'deleted_or_deactivated'].includes(account.status);
}

function canCheckHistoryAccount(account) {
    return !automationStopped(account) && !terminalHistoryAccount(account) && historyChecks.get(account.id)?.eligible && historyPoolState(account).kind === 'present';
}

function historyImportState(account) {
    const item = historyImports.get(String(account.id));
    return item ? String(item.state || 'unknown').toLowerCase() : '';
}

function canImportToSub2(account) {
    if (!historySub2Configured || !historyImportsAvailable || !account || account.status !== 'active') return false;
    if (account.sub2Policy === 'free_local' || terminalHistoryAccount(account)) return false;
    if (sub2AutomationBlock(account)?.code === 'missing' || automationStopped(account)?.code === 'login') return false;
    if (Object.values(historyAccountTasks(account)).some(task => task && !['completed', 'success', 'canceled'].includes(task.state) && deterministicStopReason(task))) return false;
    return !['queued', 'sending', 'confirming', 'imported', 'unknown'].includes(historyImportState(account));
}

function historyImportStateName(state) {
    return ({ queued: '排队中', sending: '导入中', confirming: '核对中', imported: '已关联 Sub2', failed: '导入失败', unknown: '待核对', cleanup_pending: '正在清理', remote_missing: 'Sub2 关联不存在' }[state] || '未导入');
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
    const checkCandidates = (selected.length ? selected : visibleAccounts).filter(canCheckHistoryAccount).length;
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
        ? '导入状态读取失败，请刷新后重试' : '选择 Sub2 分组与交付参数后导入';
    const deletable = selected.filter(account => !terminalHistoryAccount(account)).length;
    historyBatchDeleteBtn.disabled = locked || deletable === 0;
    historyBatchDeleteBtn.textContent = `批量删除${deletable ? ` (${deletable})` : ''}`;
    if (historyCheckNowBtn) {
        historyCheckNowBtn.disabled = locked || checkCandidates === 0;
        historyCheckNowBtn.textContent = selected.length ? `立即检测 (${checkCandidates})` : `立即检测全部 (${checkCandidates})`;
        historyCheckNowBtn.title = selected.length ? '检测当前选中且已关联 Sub2 的账号' : '按当前筛选条件检测已关联 Sub2 的账号';
    }
    if (historyCheckNowStatus && !checkCandidates) historyCheckNowStatus.textContent = selected.length ? '选中账号没有可检测项' : '当前筛选没有可检测项';
    const blockedCount = selected.filter(account => automationStopped(account)).length;
    historySelectionStatus.textContent = `已选 ${selected.length} 个${hiddenCount ? ` · ${hiddenCount} 个不在当前页` : ''}${blockedCount ? ` · ${blockedCount} 个已暂停自动处理，手动操作按账号状态判断` : ''}`;
    historyPageInfo.textContent = visibleAccounts.length
        ? `第 ${historyPage} / ${pageCount} 页 · ${visibleAccounts.length} 个账号`
        : '暂无账号';
    historyPrevPageBtn.disabled = locked || historyPage <= 1;
    historyNextPageBtn.disabled = locked || historyPage >= pageCount;
    historyPageSizeSelect.disabled = locked;
    historySearchInput.disabled = historyLoading || historyBatchRunning || processing;
    historyStatusFilter.disabled = locked;
    historyPlanFilter.disabled = locked;
    historySort.disabled = locked;
    refreshHistoryBtn.disabled = historyLoading || historyBatchRunning || processing;
    startBtn.disabled = processing || historyBatchRunning;
    retryFailedBtn.disabled = processing || historyBatchRunning || !accounts.some(canRetryLogin);
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
    renderHistoryPlanFilter();
    renderHistoryAttention();
    const focused = document.activeElement;
    const focusID = focused?.dataset?.historyId;
    const focusAction = focused?.dataset?.historyAction;
    const focusWasInGrid = historyGrid.contains(focused);
    const openDetails = Array.from(historyGrid.querySelectorAll?.('details[open][data-history-details]') || [], node => node.dataset.historyDetails);
    const visibleAccounts = getFilteredHistoryAccounts();
    if (accountTableSummary) {
        const counts = { schedulable: 0, attention: 0, working: 0 };
        historyAccounts.forEach(account => Object.keys(counts).forEach(state => { if (historyAccountMatches(account, state)) counts[state]++; }));
        accountTableSummary.textContent = `全部 ${historyAccounts.length} · 可调度 ${counts.schedulable} · 需处理 ${counts.attention} · 处理中 ${counts.working}`;
        accountTableSummary.title = '按各项条件独立统计；处理中或需处理的账号仍可能在 Sub2 可调度';
    }
    const pageAccounts = getHistoryPageAccounts(visibleAccounts);
    const query = historySearchInput.value.trim().toLowerCase();
    const filtered = query || historyStatusFilter.value !== 'all' || historyPlanFilter.value !== 'all';
    historyRefreshStatus.textContent = historyLoadError
        ? historyLoadError
        : historyLoading ? '正在加载…' : (filtered
            ? `匹配 ${visibleAccounts.length} / 共 ${historyAccounts.length} 个账号`
            : `共 ${historyAccounts.length} 个账号`);
    updateHistoryControls(visibleAccounts, pageAccounts);
    if (historyLoadError) {
        closeHistoryRecoveryPopover();
        historyGrid.innerHTML = `<div class="empty-state"><div class="empty-icon">⚠️</div><p>${escapeHTML(historyLoadError)}</p></div>`;
        return;
    }
    if (!historyAccounts.length) {
        closeHistoryRecoveryPopover();
        historyGrid.innerHTML = '<div class="empty-state"><div class="empty-icon">🗂️</div><p>暂无账号历史，完成一次登录后会自动记录</p></div>';
        return;
    }
    if (!visibleAccounts.length) {
        closeHistoryRecoveryPopover();
        historyGrid.innerHTML = '<div class="empty-state"><div class="empty-icon">🔎</div><p>没有匹配的账号，请修改或清空搜索条件</p></div>';
        return;
    }
        historyGrid.innerHTML = pageAccounts.map(account => {
        const deleted = account.status === 'deleted';
        const deactivated = ['deactivated', 'deleted_or_deactivated'].includes(account.status);
        const busy = historyBusyID === account.id;
        const stopped = automationStopped(account);
        const displayName = historyAccountName(account);
        const selected = historySelectedIDs.has(account.id);
        const importState = historyImportState(account);
        const externallyLinked = account.sub2Status.associationStatus === 'linked' || String(historyImports.get(account.id)?.operation_id || '').startsWith('linked-');
        const importStateText = externallyLinked && importState === 'imported' ? '已关联 Sub2' : historyImportStateName(importState);
        const importError = historyImports.get(String(account.id))?.last_error || '';
        const statusClass = escapeHTML(account.status.replace(/[^a-z_]/g, ''));
        const discarded = discardedHistoryAccount(account);
        const errorSummary = discarded ? discardReason(account) : account.lastErrorCode === 'invalid_username_or_password' ? '账密被拒绝' : account.reloginMessage || account.lastError;
        const errorCode = account.lastErrorCode ? ` · ${escapeHTML(account.lastErrorCode)}` : '';
        const errorHTTPStatus = account.lastHTTPStatus > 0 ? ` · HTTP ${account.lastHTTPStatus}` : '';
        const expiry = historyExpiryState(account);
        const expiryBadge = ['soon', 'expired'].includes(expiry.kind) ? `<span class="history-expiry ${expiry.kind}" title="这里只表示 OAuth 访问令牌有效期，不代表账号或 Refresh Token 已失效">${escapeHTML(expiry.label)}</span>` : '';
        const recoveryAction = historyRecoveryAction(account);
        const recoveryButton = recoveryAction.visible && !recoveryAction.disabled ? `<button class="btn-secondary history-primary-action" type="button" data-history-action="recover" data-history-id="${escapeHTML(account.id)}" title="${escapeHTML(recoveryAction.reason)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>${recoveryAction.label}</button>` : '';
        const copyRefreshButton = account.refreshToken ? `
                    <button class="btn-secondary" type="button" data-history-action="copy-refresh-token" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>
                        复制 Refresh Token
                    </button>` : '';
        const pool = historyPoolState(account);
        const recoverySummary = `成功复活 ${account.recoveryCount} 次`;
        const diagnostics = [
            errorSummary ? `${escapeHTML(errorSummary)}${errorCode}${errorHTTPStatus}` : '',
            importError ? `Sub2：${escapeHTML(importError)}` : '',
            account.sub2Status.errorMessage ? `Sub2：${escapeHTML(account.sub2Status.errorMessage)}` : ''
        ].filter(Boolean);
        const recoveryHTML = renderHistoryRecovery(account);
        const { delivery, recovery, repair, recheck } = historyAccountTasks(account);
        const official = officialAccountStatus(account);
        const recentLoginFailure = stopped && !busy && ['error', 'failed'].includes(account.status) && account.lastError ? `最近登录失败：${account.lastError}` : '';
        const primaryProblem = discarded ? `废弃原因：${errorSummary}` : official ? `官方账号${lifecycleStatusName(official)}，已停止登录` : stopped
            ? [stopped.label, recentLoginFailure].filter(Boolean).join('；') : errorSummary || importError || account.sub2Status.errorMessage;
        const checkState = historyChecks.get(account.id)?.latest_task?.state;
        const workflow = official ? (importState === 'cleanup_pending' ? '后台清理 Sub2 中' : '已终止') : stopped?.code === 'free_local' ? 'Free · 仅本地' : stopped ? '自动处理已暂停' : [delivery, recovery, repair, recheck].some(task => task?.requires_action && !['completed', 'success', 'canceled'].includes(task.state)) ? '需处理'
            : ['queued', 'checking', 'recovery_queued', 'retry_wait'].includes(repair?.state) ? '资料更新处理中'
            : ['queued', 'running'].includes(checkState) ? (checkState === 'queued' ? '检测排队中' : '检测中')
            : ['running', 'pending'].includes(account.status) ? historyStatusName(account.status)
            : delivery?.state === 'retry_wait' ? '等待交付重试'
            : delivery && !['completed', 'canceled'].includes(delivery.state) ? '交付中'
            : recovery && historyRecoveryActive(recovery) ? historyRecoveryNames[recovery.state] || '恢复中'
            : historyRecoveryRetrying(recovery) ? '等待恢复重试'
            : recheck?.state === 'checking' ? '正在延迟复检'
            : ['pending', 'recovery_queued'].includes(recheck?.state) ? '等待延迟复检'
            : delivery?.state === 'completed' ? 'Sub2 交付完成' : importStateText;
        const automationHTML = official || discarded ? '' : renderHistoryAutomation(account);
        return `
            <article class="history-item history-account-card ${selected ? 'history-selected' : ''} ${deleted ? 'history-deleted' : ''}" data-history-card="${escapeHTML(account.id)}">
                <div class="history-select">
                    <input type="checkbox" data-history-select data-history-id="${escapeHTML(account.id)}" aria-label="选择 ${escapeHTML(historyAccountName(account))}" ${selected ? 'checked' : ''} ${historyActionsLocked() ? 'disabled' : ''}>
                </div>
                <div class="history-row-identity">
                    <div class="history-email">${escapeHTML(displayName)}</div>
                    ${displayName !== account.email && account.email ? `<div class="history-account-email">${escapeHTML(account.email)}</div>` : ''}
                    ${account.sub2Status.nameStale ? `<div class="history-name-source">${escapeHTML(({ cached_remote: '缓存名称', historical_import: '历史导入名称', local_delivery: '本地交付名称' }[account.sub2Status.nameSource] || '名称'))} · 待同步</div>` : ''}
                    <span class="history-account-number">AUTH #${escapeHTML(account.id)}${account.sub2Status.sub2AccountID ? ` · Sub2 #${escapeHTML(account.sub2Status.sub2AccountID)}` : ''} · ${escapeHTML(historyPlanLabel(account.planType))}</span>
                </div>
                <div class="history-row-auth"><span class="history-row-label">AUTH 登录</span><span class="history-status ${statusClass}">${escapeHTML(historyStatusName(account.status))}</span>${expiryBadge}</div>
                <div class="history-row-sub2"><span class="history-row-label">Sub2 调度</span>${renderSub2Status(account)}${pool.kind !== 'present' ? `<span class="history-account-number">${escapeHTML(pool.label)}</span>` : ''}</div>
                <div class="history-row-source"><span class="history-row-label">状态来源</span>${renderAccountStatusSource(account)}</div>
                <div class="history-row-quota"><span class="history-row-label">使用额度</span>${renderQuotaSummary(account)}${renderHistoryCost(account)}</div>
                <div class="history-row-workflow"><span class="history-row-label">交付 / 恢复</span><span class="history-workflow-state">${escapeHTML(workflow)}</span>
                    ${primaryProblem || recoveryHTML || automationHTML ? `<details class="history-diagnostics ${primaryProblem ? 'is-alert' : ''}" data-history-details="diagnostics-${escapeHTML(account.id)}"><summary><span>${escapeHTML(primaryProblem || '查看进度与记录')}</span></summary><div class="history-diagnostics-body">${diagnostics.map(line => `<p class="history-error">${line}</p>`).join('')}${automationHTML}${recoveryHTML}<p class="history-task-status">登录尝试 ${Number.isFinite(account.attemptCount) ? account.attemptCount : 0} 次 · 最近尝试 ${formatHistoryTime(account.lastAttemptAt || account.updatedAt)}</p></div></details>` : ''}
                    ${account.recoveryCount || account.recoveryAttemptCount ? `<button class="history-recovery-count" type="button" data-history-action="recovery-history" data-history-id="${escapeHTML(account.id)}" aria-expanded="false" aria-controls="historyRecoveryPopover" aria-haspopup="dialog" aria-label="${escapeHTML(historyAccountName(account))}，${escapeHTML(recoverySummary)}，查看复活记录" title="${account.recoveryAttemptCount} 次恢复尝试；悬停、聚焦或点击查看记录">↻ ${escapeHTML(recoverySummary)}</button>` : ''}
                </div>
                <div class="history-row-time"><span class="history-row-label">最近成功</span>${formatHistoryTime(account.lastSuccessAt)}</div>
                <div class="history-row-actions history-actions">
                    ${recoveryButton}
                    <button class="btn-secondary" type="button" data-history-action="details" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>查看详情</button>
                    ${official || discarded ? '' : `<details class="history-more" data-history-details="actions-${escapeHTML(account.id)}">
                        <summary>更多</summary>
                        <div class="history-more-menu">
                            ${copyRefreshButton}
                            ${stopped ? `<div class="history-more-status" role="status">${escapeHTML(stopped.label)}。${recentLoginFailure ? `${escapeHTML(recentLoginFailure)}${errorCode}${errorHTTPStatus}。` : ''}${official ? '官方终态账号不再提供登录或人工核对。' : stopped.code === 'free_local' ? '保留本地登录和凭据，不进入 Sub2。' : stopped.code === 'missing' ? '可仅重新登录 AUTH；也可明确选择“重新登录并导入 Sub2”重建。' : stopped.code === 'unmatched' ? '可更新资料、重新登录或手动首次导入。' : '可更新资料后手动重试；每次点击仅尝试一次，失败仍暂停。'}</div>` : ''}
                            ${stopped?.code === 'missing' ? '' : `<button class="btn-secondary" type="button" data-history-action="credentials" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy || account.status === 'running' ? 'disabled' : ''}>更新资料</button>`}
                            ${canReloginHistory(account) || busy ? `<button class="btn-secondary" type="button" data-history-action="relogin" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy || !canReloginHistory(account) ? 'disabled' : ''}>${busy ? '<span class="spinner"></span> 登录中' : stopped?.code === 'missing' ? '仅重新登录 AUTH' : historyReloginDelivers(account) ? '重新登录并交付' : '重新登录'}</button>` : ''}
                            ${canReimportRemoteMissing(account) ? `<button class="btn-secondary" type="button" data-history-action="reimport" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>重新登录并导入 Sub2</button>` : ''}
                            ${stopped && !canImportToSub2(account) ? '' : `<button class="btn-secondary" type="button" data-history-action="${importState === 'unknown' || importState === 'confirming' ? 'reconcile' : 'import'}" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy || !canImportToSub2(account) && !['unknown', 'confirming'].includes(importState) ? 'disabled' : ''}>${escapeHTML(importStateText)}</button>`}
                            <button class="btn-secondary" type="button" data-history-action="delete" data-history-id="${escapeHTML(account.id)}" ${historyActionsLocked() || busy ? 'disabled' : ''}>${deleted ? '清理记录' : '删除记录'}</button>
                        </div>
                    </details>`}
                </div>
            </article>`;
    }).join('');
    openDetails.forEach(key => {
        const details = historyGrid.querySelector(`[data-history-details="${CSS.escape(key)}"]`);
        if (details) details.open = true;
    });
    syncHistoryRecoveryPopover(pageAccounts);
    if (focusID && focusAction && focusWasInGrid) {
        const replacement = historyGrid.querySelector(`[data-history-action="${CSS.escape(focusAction)}"][data-history-id="${CSS.escape(focusID)}"]`);
        if (replacement && !replacement.disabled) replacement.focus({ preventScroll: true });
    }
    void loadHistoryCosts(pageAccounts);
}

async function checkHistoryNow() {
    if (historyActionsLocked() || !window.AccountChecks?.startMany) return;
    const visible = getFilteredHistoryAccounts();
    const source = historySelectedIDs.size
        ? historyAccounts.filter(account => historySelectedIDs.has(account.id))
        : visible;
    const ids = source.filter(canCheckHistoryAccount).map(account => account.id);
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
    const selected = historyAccounts.filter(account => ids.includes(account.id) && !terminalHistoryAccount(account));
    if (!selected.length) return;
    const pageIDs = new Set(getHistoryPageAccounts(getFilteredHistoryAccounts()).map(account => account.id));
    const hiddenCount = selected.filter(account => !pageIDs.has(account.id)).length;
    const subject = selected.length === 1 ? `账号 ${historyAccountName(selected[0])}` : `选中的 ${selected.length} 个账号`;
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
    clearHistoryCredentials();
    historyDetailBody.innerHTML = '';
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
    clearHistoryCredentials();
    historyDetailTrigger = trigger && typeof trigger.focus === 'function' ? trigger : null;
    historyDetailID = String(id);
    historyDetail.hidden = false;
    historyDetailPanel.focus?.({ preventScroll: true });
    historyDetailTitle.textContent = historyAccountName(account);
    historyDetailBody.innerHTML = '<div class="empty-state"><p>正在加载详情…</p></div>';
    const controller = new AbortController();
    let timedOut = false;
    const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 15000);
    historyDetailAbort = controller;
    const costPromise = loadHistoryCosts([account]);
    try {
        const response = await fetch(`/api/history/${encodeURIComponent(id)}`, {
            headers: { 'Accept': 'application/json' },
            signal: controller.signal
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.message || `详情加载失败（HTTP ${response.status}）`);
        await costPromise;
        if (historyDetailID !== String(id) || historyDetailAbort !== controller || controller.signal.aborted) return;
        renderHistoryDetail(payload.account || account, Array.isArray(payload.attempts) ? payload.attempts : [],
            Array.isArray(payload.recovery_failures) ? payload.recovery_failures : [],
            payload.recovery_failures_available !== false, payload.recovery_failures_truncated === true,
            Array.isArray(payload.lifecycle_events) ? payload.lifecycle_events : account.lifecycleEvents,
            payload.lifecycle_events_available !== false, payload.credentials_meta);
    } catch (error) {
        if (historyDetailID !== String(id)) return;
        if (error.name === 'AbortError' && !timedOut) return;
        const message = timedOut ? '详情读取超时，请重试' : (error.message || '详情加载失败');
        historyDetailBody.innerHTML = `<div class="empty-state"><div class="empty-icon">⚠️</div><p>${escapeHTML(message)}</p></div>`;
    } finally {
        clearTimeout(timeout);
    }
}

function renderRecoveryFailureEvents(events, available, truncated) {
    if (!available) return '';
    if (!events.length) return '';
    const rows = [...events].sort((left, right) => historyTimeValue(right.created_at) - historyTimeValue(left.created_at) || Number(right.id) - Number(left.id));
    const items = rows.map(event => {
        // The task ID is already shown in the row header; keep the body to
        // stage, fixed classification, HTTP status, and structured groups.
        const diagnostic = recoveryDiagnosticParts(event).filter(item => !item.startsWith('任务 #'));
        const taskID = Number(event.task_id || event.taskId || 0);
        const time = event.created_at ? formatHistoryTime(event.created_at) : '时间未知';
        return `<div class="history-attempt"><div class="history-attempt-header"><span>${escapeHTML(time)} · Sub2 检测失败</span><span>${taskID > 0 ? `任务 #${taskID}` : '恢复任务'}</span></div><div class="history-attempt-message">${escapeHTML(diagnostic.join(' · ') || '已记录脱敏失败分类')}</div></div>`;
    }).join('');
    return `<div class="history-detail-attempts"><p class="hint">Sub2 检测失败证据（仅保存固定分类、阶段和 HTTP 状态，不保存上游响应或凭据）${truncated ? ' · 仅显示最近 100 条' : ''}</p>${items}</div>`;
}

function clearHistoryCredentials() {
    historyDetailGeneration += 1;
    const fields = new Set([...historyCredentialValues.keys(), ...historyCredentialRequests.keys()]);
    historyCredentialRequests.forEach(controller => controller.abort());
    historyCredentialRequests.clear();
    historyCredentialValues.clear();
    for (const field of fields) {
        const value = historyDetailBody.querySelector(`[data-history-credential-value="${field}"]`);
        if (value) value.textContent = '••••••••';
        const button = historyDetailBody.querySelector(`[data-history-credential-field="${field}"][data-history-credential-action="show"]`);
        if (button) { button.textContent = '显示'; button.disabled = false; }
        const copy = historyDetailBody.querySelector(`[data-history-credential-field="${field}"][data-history-credential-action="copy"]`);
        if (copy) copy.disabled = false;
        const status = historyDetailBody.querySelector(`[data-history-credential-status="${field}"]`);
        if (status) status.textContent = '';
    }
}

function renderHistoryCredentials(account, meta) {
    if (terminalHistoryAccount(account)) return '';
    const fieldRow = (field, label) => {
        const state = meta?.[field]?.state;
        const available = state === 'saved';
        return `<div class="history-credential-row"><span>${label}</span><span class="history-credential-value" data-history-credential-value="${field}">${available ? '••••••••' : state === 'missing' ? '未保存/已清除' : '资料状态未提供'}</span><div class="history-credential-actions"><button class="btn-secondary" type="button" data-history-credential-field="${field}" data-history-credential-action="show" ${available ? '' : 'disabled'}>显示</button><button class="btn-secondary" type="button" data-history-credential-field="${field}" data-history-credential-action="copy" ${available ? '' : 'disabled'}>复制</button></div><span class="history-credential-status" data-history-credential-status="${field}" role="status" aria-live="polite"></span></div>`;
    };
    return `<section class="history-credentials" aria-label="登录资料"><h4>登录资料</h4><div class="history-credential-row"><span>邮箱</span><span class="history-credential-value">${escapeHTML(account.email)}</span><div class="history-credential-actions"><button class="btn-secondary" type="button" data-history-credential-field="email" data-history-credential-action="copy">复制</button></div></div>${fieldRow('password', '密码')}${fieldRow('totp_secret', '2FA（TOTP）密钥')}<p class="hint">2FA 显示已保存的密钥，不是当前六位验证码。</p><details class="history-credential-edit"><summary>修改登录资料</summary><p class="hint">保存资料不代表登录成功；Sub2 关联缺失时不会重新导入。</p><button class="btn-secondary" type="button" data-history-detail-edit ${sub2AutomationBlock(account)?.code === 'missing' || account.status === 'running' ? 'disabled' : ''}>修改资料</button></details></section>`;
}

async function handleHistoryCredentialAction(field, action, button) {
    const id = historyDetailID;
    if (!id || historyDetail.hidden || document.hidden || !['show', 'copy'].includes(action)) return;
    const account = historyAccounts.find(item => item.id === id);
    if (field === 'email' && action === 'copy') { if (account) await copySecret(account.email, '邮箱'); return; }
    if (!account || terminalHistoryAccount(account)) return;
    if (!['password', 'totp_secret'].includes(field)) return;
    const node = historyDetailBody.querySelector(`[data-history-credential-value="${field}"]`);
    const status = historyDetailBody.querySelector(`[data-history-credential-status="${field}"]`);
    if (action === 'show' && historyCredentialValues.has(field)) {
        historyCredentialRequests.get(field)?.abort();
        historyCredentialRequests.delete(field);
        historyCredentialValues.delete(field);
        if (node) node.textContent = '••••••••';
        button.textContent = '显示';
        if (status) status.textContent = '';
        return;
    }
    if (historyCredentialRequests.has(field)) return;
    const generation = historyDetailGeneration, controller = new AbortController();
    historyCredentialRequests.set(field, controller);
    const current = () => historyDetailID === id && historyDetailGeneration === generation && !historyDetail.hidden && !document.hidden && historyCredentialRequests.get(field) === controller && !controller.signal.aborted;
    button.disabled = true;
    if (status) status.textContent = '正在读取…';
    const timeout = setTimeout(() => controller.abort(), 15000);
    try {
        const response = await fetch(`/api/history/${encodeURIComponent(id)}/credentials/reveal`, {
            method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
            body: JSON.stringify({ field }), signal: controller.signal, cache: 'no-store'
        });
        const payload = await response.json().catch(() => ({}));
        if (!current()) return;
        if (!response.ok || payload.success !== true) {
            const messages = { credential_missing: '未保存/已清除', account_not_found: '账号不存在', credential_read_failed: '资料读取失败' };
            if (status) status.textContent = messages[payload.code] || '资料读取失败，请稍后重试';
            return;
        }
        if (String(payload.account_id) !== id || payload.field !== field || typeof payload.value !== 'string' || !payload.value) {
            if (status) status.textContent = '资料响应无效，请刷新详情';
            return;
        }
        if (action === 'copy') {
            const copied = await copySecret(payload.value, field === 'password' ? '密码' : 'TOTP 密钥');
            if (current() && status) status.textContent = copied ? '已复制' : '复制失败，请点击显示后手动复制';
        } else {
            historyCredentialValues.set(field, payload.value);
            if (node) node.textContent = payload.value;
            button.textContent = '隐藏';
            if (status) status.textContent = '';
        }
    } catch (_) {
        if (historyDetailID === id && historyDetailGeneration === generation && !historyDetail.hidden && !document.hidden && status) status.textContent = '资料读取失败，请稍后重试';
    } finally {
        clearTimeout(timeout);
        if (historyCredentialRequests.get(field) === controller) { historyCredentialRequests.delete(field); button.disabled = false; }
    }
}

function renderHistoryDetail(account, attempts, recoveryFailures = [], recoveryFailuresAvailable = true, recoveryFailuresTruncated = false, lifecycleEvents = [], lifecycleEventsAvailable = true, credentialsMeta = null) {
    const current = historyAccounts.find(item => item.id === String(account.id)) || normalizeHistoryAccount(account);
    const sub2 = current.sub2Status;
    const pool = historyPoolState(current);
    const runtimePause = sub2RuntimePause(sub2);
    const quota = sub2.quota;
    const quotaFiveHour = quotaWindowText(quota?.fiveHour, '5 小时');
    const quotaSevenDay = quotaWindowText(quota?.sevenDay, '7 天');
    const quotaSevenDaySonnet = quotaWindowText(quota?.sevenDaySonnet, '7 天 Sonnet');
    const quotaSevenDayFable = quotaWindowText(quota?.sevenDayFable, '7 天 Fable');
    const quotaThirtyDay = quotaWindowText(quota?.thirtyDay, '30 天');
    const quotaCredits = quota?.credits ? `Credits ${quota.credits.unlimited === true ? '无限' : quota.credits.balance || '未提供'}` : 'Credits 未提供';
    const sub2Notes = [sub2.errorMessage, runtimePause?.reason, sub2.error].filter(Boolean);
    const recoverySummary = renderHistoryRecovery(current);
    const delivery = historyDeliveries.get(current.id);
    const expiry = historyExpiryState({ expiresAt: Number(account.expires_at ?? account.expiresAt ?? 0) });
    const expiryText = expiry.label || '有效期未知';
    const plan = historyPlanLabel(account.plan_type || account.planType);
    const status = discardedHistoryAccount(current) ? 'discarded' : String(account.status || 'unknown').toLowerCase();
    const attemptHTML = attempts.length ? attempts.map(attempt => {
        const stage = stageNames[attempt.stage] || attempt.stage || '登录';
        const state = historyStatusName(String(attempt.status || 'unknown').toLowerCase());
        const rejected = attempt.code === 'invalid_username_or_password';
        const code = attempt.code && !rejected ? ` · ${escapeHTML(attempt.code)}` : '';
        const message = rejected ? '账密被拒绝' : loginBlockLabel(attempt.manual_reason || attempt.code) || attempt.message;
        const http = Number(attempt.http_status || 0) > 0 ? ` · HTTP ${Number(attempt.http_status)}` : '';
        const duration = Number(attempt.duration_ms || 0) > 0 ? `${(Number(attempt.duration_ms) / 1000).toFixed(1)}s` : '—';
        return `<div class="history-attempt">
            <div class="history-attempt-header"><span>${escapeHTML(formatHistoryTime(attempt.started_at))} · ${escapeHTML(state)}</span><span>${escapeHTML(stage)} · ${duration}</span></div>
            <div class="history-attempt-message">${attempt.retryable ? '可重试' : '不可重试'}${code}${http}${message ? ` · ${escapeHTML(message)}` : ''}</div>
        </div>`;
    }).join('') : '<div class="empty-state"><p>暂无登录尝试记录</p></div>';
    const failureHTML = renderRecoveryFailureEvents(recoveryFailures, recoveryFailuresAvailable, recoveryFailuresTruncated);
    const lifecycleHTML = renderLifecycleEvents(lifecycleEvents.length ? lifecycleEvents : current.lifecycleEvents, lifecycleEventsAvailable);
    clearHistoryCredentials();
    historyDetailBody.innerHTML = `${renderHistoryCredentials(current, credentialsMeta)}
        ${discardedHistoryAccount(current) ? `<p class="hint">废弃原因：${escapeHTML(discardReason(current))}</p>` : ''}
        <div class="history-detail-summary">
            <div class="history-detail-stat">AUTH 最近登录<br><strong>${escapeHTML(historyStatusName(status))}</strong></div>
            <div class="history-detail-stat">状态来源<br><strong>${renderAccountStatusSource(current)}</strong></div>
            <div class="history-detail-stat">Sub2 状态 / 调度<br><strong>${renderSub2Status(current)}</strong><br>${escapeHTML(pool.label)}</div>
            <div class="history-detail-stat">Sub2 最近同步<br><strong>${formatHistoryTime(sub2.checkedAt)}</strong></div>
            ${renderHistoryCost(current, true)}
            <div class="history-detail-stat">5 小时使用额度<br><strong>${escapeHTML(quotaFiveHour)}</strong></div>
            <div class="history-detail-stat">7 天使用额度<br><strong>${escapeHTML(quotaSevenDay)}</strong></div>
            <div class="history-detail-stat">7 天 Sonnet<br><strong>${escapeHTML(quotaSevenDaySonnet)}</strong></div>
            <div class="history-detail-stat">7 天 Fable<br><strong>${escapeHTML(quotaSevenDayFable)}</strong></div>
            <div class="history-detail-stat">30 天使用额度<br><strong>${escapeHTML(quotaThirtyDay)}</strong></div>
            <div class="history-detail-stat">Credits<br><strong>${escapeHTML(quotaCredits)}</strong></div>
            <div class="history-detail-stat">额度来源<br><strong>${escapeHTML(quota?.source || 'Sub2 未提供')}</strong>${quota?.updatedAt ? `<br>${escapeHTML(formatHistoryTime(quota.updatedAt))}` : ''}</div>
            <div class="history-detail-stat">方案<br><strong>${escapeHTML(String(plan))}</strong></div>
            <div class="history-detail-stat">访问令牌<br><strong>${escapeHTML(expiryText)}</strong></div>
            <div class="history-detail-stat">尝试次数<br><strong>${Number(account.attempt_count ?? account.attemptCount ?? 0)}</strong></div>
            <div class="history-detail-stat">最近成功<br><strong>${escapeHTML(formatHistoryTime(account.last_success_at || account.lastSuccessAt))}</strong></div>
            <div class="history-detail-stat">最近尝试<br><strong>${escapeHTML(formatHistoryTime(account.last_attempt_at || account.lastAttemptAt || account.updated_at || account.updatedAt))}</strong></div>
        </div>
        ${lifecycleHTML}${sub2Notes.length || delivery || recoverySummary ? `<div class="history-detail-attempts"><p class="hint">当前状态与处理进度</p>${sub2Notes.map(note => `<p class="history-task-status">${escapeHTML(note)}</p>`).join('')}${delivery ? `<p class="history-task-status">${escapeHTML(deliveryStatusText(delivery))}</p>` : ''}${recoverySummary}</div>` : ''}
        <div class="history-detail-attempts"><p class="hint">列表中的有效期来自 OAuth 访问令牌；访问令牌过期后，Refresh Token 是否可用不由此字段判断，通常仍可用于重新登录。最近 ${attempts.length} 次尝试</p>${attemptHTML}</div>${failureHTML}`;
}

async function performHistoryRelogin(id, { autoDeliver, deliveryOptions } = {}) {
    const account = historyAccounts.find(item => item.id === id);
    if (!account) return { success: false, error: '账号不存在' };
    if (!canReloginHistory(account)) return { success: false, error: '账号正在登录或官方已删除，请查看详情' };
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
            body: JSON.stringify({ account_id: Number(id), auto_deliver: autoDeliver ?? historyReloginDelivers(account), manual_attempt: true,
                ...(deliveryOptions ? { delivery_options: deliveryOptions } : {}) }),
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
        const latest = historyAccounts.find(item => item.id === id);
        if (!latest || terminalHistoryAccount(latest)) return { success: false, error: '账号已移除或已停止使用' };
        if (!finalData.success) {
            throw new Error(formatLoginError(finalData, { stage: '' }));
        }
        const result = finalData.Data || finalData.data || {};
        const refreshToken = getRefreshToken(result);
        if (refreshToken) historyRefreshTokens.set(id, refreshToken);
        const delivery = result.delivery || result.Delivery;
        account.reloginMessage = delivery?.state === 'queued' ? '登录成功，等待同步至 Sub2'
            : delivery?.state === 'canceled' ? `登录成功 · ${delivery.last_error || '已取消交付'}` : '登录成功，历史已更新';
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
        exportBtn.disabled = results.length === 0;
        refreshHistoryBtn.disabled = false;
        historySearchInput.disabled = false;
        renderHistory();
    }
}

async function reimportRemoteMissingHistory(id) {
    const key = String(id), account = historyAccounts.find(item => item.id === key);
    if (!account || historyActionsLocked() || historyBusyID || pendingDeliveryOptions || !canReimportRemoteMissing(account)) return;
    const importOperation = String(historyImports.get(key)?.operation_id || '');
    const initialCredentialVersion = Number(account.credentialVersion || 0);
    if (!importOperation) {
        historyBatchSummary = '缺少旧关联标识，请刷新核对后重新导入。';
        renderHistory();
        return;
    }
    const optionsRequest = chooseDeliveryOptions('将重新登录并创建新的 Sub2 账号。旧关联已标记不存在；只有确认后才提交。', true,
        historyDeliveries.get(key)?.delivery_options || null, false);
    deliveryOptionsApply.textContent = '重新登录并导入';
    const deliveryOptions = await optionsRequest;
    if (!deliveryOptions || historyActionsLocked() || !canReimportRemoteMissing(historyAccounts.find(item => item.id === key))
        || historyImports.get(key)?.operation_id !== importOperation) return;
    const frozenOptions = copyDeliveryOptions(deliveryOptions);
    processing = true;
    historyBatchRunning = true;
    historyBatchSummary = '正在重新登录并准备 Sub2 重建…';
    startBtn.disabled = true;
    startBtn.innerHTML = '<span class="spinner"></span><span>处理中</span>';
    exportBtn.disabled = true;
    refreshHistoryBtn.disabled = true;
    historySearchInput.disabled = true;
    document.body.classList.add('processing');
    renderHistory();
    try {
        const loginResult = await performHistoryRelogin(key, { autoDeliver: false, deliveryOptions: frozenOptions });
        if (!loginResult.success) {
            historyBatchSummary = loginResult.error || '重新登录失败，未提交 Sub2 重建';
            return;
        }
        await loadHistory();
        const freshAccount = historyAccounts.find(item => item.id === key);
        const credentialVersion = Number(freshAccount?.credentialVersion || 0);
        if (historyLoadError || !freshAccount || freshAccount.status !== 'active' || !Number.isSafeInteger(initialCredentialVersion) || initialCredentialVersion < 0
            || !Number.isSafeInteger(credentialVersion) || credentialVersion <= initialCredentialVersion
            || !canReimportRemoteMissing(freshAccount) || historyImports.get(key)?.operation_id !== importOperation) {
            historyBatchSummary = '账号状态已变化，未提交 Sub2 重建，请刷新历史核对。';
            return;
        }
        const response = await fetch('/api/sub2/import', {
            method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
            body: JSON.stringify({ account_ids: [Number(key)], delivery_options: frozenOptions, reimport: true,
                expected_credential_version: credentialVersion, expected_import_operation_id: importOperation }),
            signal: AbortSignal.timeout(30000)
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok || payload.success === false) throw new Error(payload.message || `Sub2 重建提交失败（HTTP ${response.status}）`);
        const accepted = Array.isArray(payload.imports) && payload.imports.some(item => String(item.account_id) === key);
        if (!accepted) throw new Error('Sub2 未受理重建请求，请刷新核对关联状态');
        historyBatchSummary = '已提交 Sub2 重建，正在等待结果…';
        const outcome = await waitForImportCompletion([key], 90000, { total: 1 });
        historyBatchSummary = outcome.pending ? `Sub2 重建仍在处理中，${outcome.message || '请稍后刷新核对'}`
            : outcome.success ? '重新登录并导入 Sub2 已完成。' : '登录已成功，Sub2 重建未完成，请查看账号状态与错误原因。';
    } catch (error) {
        historyBatchSummary = error.name === 'TimeoutError' || error instanceof TypeError ? 'Sub2 重建提交结果暂未确认，请刷新核对。' : (error.message || 'Sub2 重建提交失败，请刷新后核对');
    } finally {
        await loadHistory();
        processing = false;
        historyBatchRunning = false;
        startBtn.disabled = false;
        startBtn.innerHTML = '<span>▶️</span><span>开始处理</span>';
        document.body.classList.remove('processing');
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
    const proxyLabel = '优先使用各账号在 Sub2 的当前代理，不可达时尝试其备用节点；未交付账号沿用已有线路';
    const estimate = `默认最长约 ${ids.length * 3} 分钟`;
    const summary = `将按顺序复活 ${ids.length} 个账号，${proxyLabel}，${estimate}（按单账号 3 分钟上限）。${skipped > 0 ? `另有 ${skipped} 个选中账号当前不可复活，将跳过。` : ''}\n\n是否继续？`;
    if (!await confirmAction({ title: '批量复活账号', message: summary, confirmLabel: '开始复活' }) || historyActionsLocked()) return;

    historyBatchRunning = true;
    historyBatchSummary = '';
    historyBatchStats = { action: '批量复活', total: ids.length, done: 0, success: 0, failed: 0, skipped };
    processing = true;
    startBtn.disabled = true;
    startBtn.innerHTML = '<span class="spinner"></span><span>批量复活中</span>';
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
        exportBtn.disabled = results.length === 0;
        refreshHistoryBtn.disabled = false;
        historySearchInput.disabled = false;
        renderHistory();
    }
}

async function importHistoryAccounts(ids, { waitForCompletion = false, deliveryOptions = deliveryBatchOptions } = {}) {
    if (historyActionsLocked()) return;
    if (!deliveryOptions && !await prepareDeliveryBatch()) return;
    const frozenOptions = copyDeliveryOptions(deliveryOptions || deliveryBatchOptions);
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
                body: JSON.stringify({ account_ids: batchIDs.map(Number), delivery_options: frozenOptions }),
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
    const message = `将 ${eligible} 个账号导入 Sub2。${skipped ? `另有 ${skipped} 个选中账号不满足导入条件，将跳过并保留选中。` : ''}`;
    const options = await chooseDeliveryOptions(message, true);
    if (!options || historyActionsLocked()) return;
    await importHistoryAccounts(ids, { deliveryOptions: options });
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
        const message = `将 ${ids.length} 个批量登录成功账号导入 Sub2。${skipped ? `另有 ${skipped} 个结果已导入、不可用或未找到对应历史记录，将跳过。` : ''}`;
        const options = await chooseDeliveryOptions(message, true);
        if (!options) return;
        historyBatchRunning = false;
        renderHistory();
        await importHistoryAccounts(ids, { waitForCompletion: true, deliveryOptions: options });
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
    if (terminalHistoryAccount(historyAccounts.find(account => account.id === String(id)))) return;
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
    const autoDeliver = autoDeliveryConfigured === true && Boolean(autoDeliverToggle?.checked);
    // The initial settings read may still be resolving after a fast page load.
    // Use it when available so a selected Sub2 login route is not lost.
    if (!deliveryBatchOptions && deliverySettingsRequest) await deliverySettingsRequest;
    if (targetAccounts.some(account => (account.autoDeliver ?? autoDeliver) && !account.deliveryOptions) && !await prepareDeliveryBatch()) return;
    if (processing || historyBatchRunning) return;
    processing = true;
    const controller = new AbortController();
    loginBatchController = controller;
    const frozenOptions = deliveryBatchOptions ? copyDeliveryOptions(deliveryBatchOptions) : null;
    renderAutoDeliverySetting();
    startBtn.disabled = true;
    startBtn.innerHTML = '<span class="spinner"></span> 处理中';
    stopBtn.hidden = false;
    stopBtn.disabled = false;
    stopBtn.textContent = '停止';
    document.body.classList.add('processing');
    importSub2Btn.disabled = true;
    exportBtn.disabled = true;
    refreshHistoryBtn.disabled = true;
    const proxy = '';
    for (const account of targetAccounts) {
        // Retrying a login preserves the delivery choice frozen for that account's batch.
        if (typeof account.autoDeliver !== 'boolean') account.autoDeliver = autoDeliver;
        if (!account.deliveryOptions && frozenOptions) account.deliveryOptions = copyDeliveryOptions(frozenOptions);
        account.status = 'pending';
        account.message = '等待处理';
        account.duration = '';
        account.stage = '';
        account.stageStartedAt = null;
        account.timings = {};
        account.delivery = null;
    }
    renderHistory();
    renderStatus();
    updateStats();
    const elapsedTimer = setInterval(renderStatus, 1000);

    try {
        if (modeSelect.value === 'sequential') {
            await processSequential(targetAccounts, proxy, controller.signal);
        } else {
            await processParallel(targetAccounts, proxy, controller.signal);
        }
    } finally {
        clearInterval(elapsedTimer);
        loginBatchController = null;
        stopBtn.hidden = true;
        processing = false;
        renderAutoDeliverySetting();
        startBtn.disabled = false;
        startBtn.innerHTML = '<span>▶️</span><span>开始处理</span>';
        document.body.classList.remove('processing');
        exportBtn.disabled = results.length === 0;
        refreshHistoryBtn.disabled = false;
        await loadHistory();
        importSub2Btn.disabled = results.length === 0;
        renderStatus();
        updateStats();
    }
}

async function processSequential(targetAccounts, proxy, signal) {
    for (const account of targetAccounts) {
        if (signal.aborted) break;
        await processAccount(account, proxy, signal);
    }
}

async function processParallel(targetAccounts, proxy, signal) {
    const concurrency = Math.min(serverMaxConcurrent, 10, Math.max(1, parseInt(concurrencyInput.value) || 1));
    const queue = [...targetAccounts];
    const workers = [];

    for (let i = 0; i < concurrency; i++) {
        workers.push(async () => {
            while (queue.length > 0 && !signal.aborted) {
                const account = queue.shift();
                if (account) {
                    await processAccount(account, proxy, signal);
                }
            }
        });
    }

    await Promise.all(workers.map(w => w()));
}

async function processAccount(account, proxy, signal) {
    account.status = 'processing';
    account.message = '正在登录...';
    account.startTime = Date.now();
    account.historyAttemptCount = historyAccounts.find(item => item.email.toLowerCase() === account.email.toLowerCase())?.attemptCount;
    account.requestMayHaveStarted = false;
    renderStatus();
    updateStats();

    try {
        const data = await loginAccount(account, proxy, signal);
        account.timings = data.timings || {};

        if (data.success) {
            account.manualReason = '';
            account.requiresAction = false;
            account.retryable = null;
            account.status = 'success';
            account.message = '登录成功 ✨';
            account.result = data.data;
            account.historyID = String(data.data?.account_id || data.account_id || '');
            account.delivery = data.data?.delivery || data.delivery || null;
            if (account.autoDeliver) historyRefreshAt = Math.min(historyRefreshAt || Infinity, Date.now() + 5000);
            results.push({
                email: account.email,
                ...data.data
            });
        } else {
            account.manualReason = data.error?.manual_reason || '';
            account.lastErrorCode = data.error?.code || '';
            account.requiresAction = Boolean(account.manualReason);
            account.retryable = data.error?.retryable ?? null;
            throw new Error(formatLoginError(data, account));
        }
    } catch (error) {
        account.status = signal.aborted ? (account.requestMayHaveStarted ? 'checking' : 'stopped') : 'error';
        account.message = signal.aborted ? (account.requestMayHaveStarted
            ? '登录结果待核对，刷新账号历史后才能重试'
            : '未开始登录，已停止，可重试') : error.message;
    }

    account.endTime = Date.now();
    account.duration = ((account.endTime - account.startTime) / 1000).toFixed(1) + 's';
    renderStatus();
    updateStats();
}

function waitForLoginRetry(delay, signal) {
    if (signal.aborted) return Promise.reject(signal.reason);
    return new Promise((resolve, reject) => {
        const timer = setTimeout(() => { signal.removeEventListener('abort', stop); resolve(); }, delay);
        function stop() { clearTimeout(timer); reject(signal.reason); }
        signal.addEventListener('abort', stop, { once: true });
    });
}

async function loginAccount(account, proxy, signal) {
    let response;
    for (let attempt = 0; attempt < 4; attempt++) {
        if (signal.aborted) throw signal.reason;
        // Bound connection setup only: the server may use a custom login
        // deadline longer than the default while the SSE stream is active.
        const controller = new AbortController();
        const timeout = setTimeout(() => controller.abort(), 15000);
        const requestSignal = AbortSignal.any([signal, controller.signal]);
        try {
            account.requestMayHaveStarted = true;
            response = await fetch('/api/login/stream', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    email: account.email,
                    password: account.password,
                    totp_secret: account.totp_secret,
                    proxy,
                    auto_deliver: account.autoDeliver === true,
                    ...(account.deliveryOptions ? { delivery_options: account.deliveryOptions } : {})
                }),
                signal: requestSignal
            });
        } catch (error) {
            if (signal.aborted) throw signal.reason;
            if (controller.signal.aborted) throw new Error('登录连接超时，请刷新历史核对结果后再重试');
            throw error;
        } finally {
            clearTimeout(timeout);
        }
        const contentType = response.headers.get('content-type') || '';
        if (response.ok && contentType.includes('text/event-stream')) break;
        account.requestMayHaveStarted = false;
        const data = contentType.includes('json') ? await response.json() : {};
        if (response.status === 429 && data.code === 'platform_busy' && attempt < 3) {
            const delay = Number(data.retry_after || response.headers.get('retry-after'));
            if (Number.isFinite(delay) && delay > 0 && delay <= 600) {
                account.stage = 'queued';
                account.message = '服务并发已满，' + delay + ' 秒后继续';
                renderStatus();
                await waitForLoginRetry(delay * 1000, signal);
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
    if (detail.upstream_code !== detail.code && /^[a-z][a-z0-9_-]{1,63}$/.test(detail.upstream_code || '')) parts.push('上游 ' + detail.upstream_code);
    const blocked = loginBlockLabel(detail.manual_reason || detail.code);
    return parts.join(' · ') + '\n' + (blocked || detail.message || data.message || '登录失败');
}

function renderStatus() {
    if (accounts.length === 0) {
        if (statusGrid.dataset.signature !== 'empty') {
            statusGrid.innerHTML = '<div class="empty-state"><div class="empty-icon">📝</div><p>粘贴账号，点击「开始处理」</p></div>';
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
                    <div class="status-message" data-status-role="delivery" hidden></div>
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
                delivery: row.querySelector('[data-status-role="delivery"]'),
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
        // Creation confirmation is only a checkpoint. A persisted delivery must
        // finish verification/group binding/enabling before we call it success.
        const states = [...wanted].map(id => {
            const delivery = historyDeliveries.get(id);
            if (delivery) return ['requires_action', 'canceled'].includes(delivery.state) ? 'failed' : delivery.state === 'completed' ? 'completed' : 'pending';
            return String(historyImports.get(id)?.state || 'unknown').toLowerCase();
        });
        const terminal = states.filter(state => ['imported', 'completed', 'failed', 'error', 'rejected'].includes(state));
        const success = terminal.filter(state => ['imported', 'completed'].includes(state)).length;
        const failed = terminal.filter(state => ['failed', 'error', 'rejected'].includes(state)).length;
        const pending = states.length - terminal.length;
        last = { total, done: initialFailed + terminal.length, success, failed: initialFailed + failed,
            pending, running: pending > 0 };
        batchImportProgress = last;
        renderBatchImportStatus();
        if (!pending) return last;
        await new Promise(resolve => setTimeout(resolve, Math.min(2500, Math.max(1, timeoutMs - (Date.now() - started)))));
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
    const icons = { pending: '⏳', processing: '⚡', checking: '⌛', success: '✅', error: '❌', stopped: '⏹' };
    const status = icons[account.status] ? account.status : 'pending';
    state.icon.className = `status-icon ${status}`;
    state.icon.textContent = icons[status];
    state.email.textContent = account.email || '';
    state.message.textContent = account.message || '';
    if (state.delivery) {
        state.delivery.hidden = !(account.autoDeliver && account.status === 'success');
        state.delivery.textContent = state.delivery.hidden ? '' : `交付：${deliveryStatusText(account.delivery)}`;
    }

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

    const actionsKey = `${account.status}|${account.result ? 'result' : ''}|${processing ? 'processing' : 'idle'}|${canRetryLogin(account)}`;
    if (state.actionsKey !== actionsKey) {
        state.actions.innerHTML = `${account.status === 'checking' ? `
            <button class="btn-secondary" onclick="recheckStoppedAccount(${account.id})" ${processing ? 'disabled' : ''}>核对结果</button>
        ` : ''}${canRetryLogin(account) ? `
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
    stoppedCount.textContent = accounts.filter(a => a.status === 'stopped').length;
    checkingCount.textContent = accounts.filter(a => a.status === 'checking').length;
    retryFailedBtn.textContent = accounts.some(account => account.status === 'stopped') ? '重试失败和已停止' : '重试全部失败';
    retryFailedBtn.disabled = processing || historyBatchRunning || !accounts.some(canRetryLogin);
}

function getRefreshToken(result) {
    if (!result) return '';
    return String(result.RefreshToken || result.refresh_token || result.refreshToken || result.rt || '').trim();
}

async function copySecret(value, label) {
    if (!value) {
        window.alert(`没有可复制的 ${label}`);
        return false;
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
            try {
                textarea.select();
                if (!document.execCommand('copy')) throw new Error('clipboard unavailable');
            } finally {
                textarea.value = '';
                textarea.remove();
            }
        }
        window.alert(`✅ ${label} 已复制到剪贴板`);
        return true;
    } catch (_) {
        window.alert(`复制 ${label} 失败，请检查浏览器剪贴板权限`);
        return false;
    }
}

window.copyToken = function(id) {
    const account = accounts.find(a => a.id === id);
    if (account && account.result) {
        copySecret(getRefreshToken(account.result), 'Refresh Token');
    }
};

function copyHistoryRefreshToken(id) {
    if (terminalHistoryAccount(historyAccounts.find(account => account.id === String(id)))) return;
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
            name: r.Email,
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
                codex_fingerprint_mode: 'full',
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
