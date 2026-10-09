// Offline interaction checks for the history selection and destructive actions.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
class Element {
    constructor() {
        this.events = {}; this.value = ''; this.textContent = ''; this.innerHTML = '';
        this.disabled = false; this.checked = false; this.hidden = false; this.dataset = {};
        this.attributes = {}; this.scrollTop = 0; this.isConnected = true; this.open = false;
        const classes = new Set();
        this.classList = { add: name => classes.add(name), remove: name => classes.delete(name),
            contains: name => classes.has(name), toggle(name, force = !classes.has(name)) { if (force) classes.add(name); else classes.delete(name); } };
    }
    addEventListener(name, handler) { (this.events[name] ||= []).push(handler); }
    get disabled() { return this._disabled; }
    set disabled(value) {
        this._disabled = value;
        if (value && document.activeElement === this) document.activeElement = document.body;
    }
    async emit(name, target = this, extra = {}) {
        const event = { target, preventDefault() { this.defaultPrevented = true; }, ...extra };
        for (const handler of this.events[name] || []) await handler(event);
        // Native dialog converts an uncancelled Escape key into a cancel event.
        if (name === 'keydown' && event.key === 'Escape' && this.open && !event.defaultPrevented) await this.emit('cancel');
    }
    dispatchEvent(event) { for (const handler of this.events[event.type] || []) handler(event); return true; }
    setAttribute(name, value) { this.attributes[name] = String(value); }
    focus() { document.activeElement = this; }
    showModal() {
        this.open = true;
        if (this.id === 'deliveryOptionsDialog') {
            confirmations.push(el('deliveryOptionsMessage').textContent);
            if (confirmResult !== null) setImmediate(async () => {
                for (let i = 0; i < 20 && el('deliveryOptionsApply').disabled; i++) await new Promise(resolve => setImmediate(resolve));
                await el(confirmResult ? 'deliveryOptionsForm' : 'deliveryOptionsCancel').emit(confirmResult ? 'submit' : 'click');
            });
        }
        if (this.id === 'actionConfirmDialog') {
            confirmations.push(el('actionConfirmMessage').textContent);
            if (confirmResult !== null) queueMicrotask(() => el(confirmResult ? 'actionConfirmAccept' : 'actionConfirmCancel').emit('click'));
        }
    }
    close() { this.open = false; queueMicrotask(() => this.emit('close')); }
    closest(selector) {
        if (selector === '[hidden]') for (let node = this; node; node = node.parentElement) if (node.hidden) return node;
        return null;
    }
    contains(node) { for (; node; node = node.parentElement) if (node === this) return true; return false; }
    querySelector() { return this.id === 'historyDetail' ? this : new Element(); }
    querySelectorAll() { return []; }
}
const indexHTML = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
assert.match(indexHTML, /<section[^>]*role="dialog"[^>]*aria-modal="true"[^>]*tabindex="-1"/,
    'history details dialog must be keyboard focusable and modal');
assert.match(indexHTML, /<dialog[^>]*id="actionConfirmDialog"[^>]*role="dialog"[^>]*aria-modal="true"[^>]*aria-labelledby="actionConfirmTitle"[^>]*aria-describedby="actionConfirmMessage"[^>]*tabindex="-1"/,
    'action confirmation must expose its title, message and modal semantics');
assert.match(indexHTML, /<section[^>]*id="historyRecoveryPopover"[^>]*role="dialog"[^>]*aria-labelledby="historyRecoveryPopoverHeading"/,
    'recovery history popover must expose dialog semantics');
const ids = [...indexHTML.matchAll(/id="([^"]+)"/g)].map(match => match[1]);
const elements = new Map(ids.map(id => { const element = new Element(); element.id = id; return [id, element]; }));
const el = id => elements.get(id);
const tabs = ['login', 'history', 'checks'].map(name => { const tab = el(`tab-${name}`); tab.dataset.tab = name; return tab; });
const panels = ['login', 'history', 'checks'].map(name => { const panel = el(`panel-${name}`); panel.dataset.tab = name; return panel; });
el('historySearchInput').parentElement = el('panel-history');
el('historySelectAll').parentElement = el('panel-history');
el('historyClearSelectionBtn').parentElement = el('historyActionBar');
for (const id of ['historyBatchReloginBtn', 'historyBatchImportBtn', 'historyBatchDeleteBtn']) el(id).parentElement = el('historyActionBar');
for (const id of ['actionConfirmCancel', 'actionConfirmAccept']) el(id).parentElement = el('actionConfirmDialog');
el('historyDetail').hidden = true;
for (const [id, value] of Object.entries({ historyStatusFilter: 'all', historySort: 'email_asc', historyPageSize: '20', modeSelect: 'sequential', concurrency: '1' })) el(id).value = value;
const document = new Element();
document.getElementById = el;
document.querySelectorAll = selector => selector.startsWith('.tab-btn') ? tabs : selector.startsWith('.tab-panel') ? panels : [];
document.querySelector = selector => selector === '.tab-btn[aria-selected="true"]' ? tabs.find(tab => tab.attributes['aria-selected'] === 'true') : null;
document.body = new Element();
const historyEvents = [];
document.dispatchEvent = event => { historyEvents.push(event.type); return Element.prototype.dispatchEvent.call(document, event); };
let rows = Array.from({ length: 25 }, (_, index) => ({ id: index + 1, email: `account${String(index + 1).padStart(3, '0')}@example.test`, status: ['active', 'error', 'running', 'deleted', 'interrupted'][index % 5], recovery_count: index === 0 ? 2 : 0, recovery_attempt_count: index === 0 ? 3 : 0 }));
let sub2Statuses = {
    1: { configured: true, imported: true, exists: true, status: 'active', schedulable: true, checked_at: '2026-10-05T10:00:00Z' },
    2: { configured: true, imported: true, exists: true, status: 'disabled', schedulable: false, checked_at: '2026-10-05T10:01:00Z' }
};
let recoverySettings = { auto_recovery_enabled: false, interval_seconds: 60 };
let checks = {}, recoveries = {}, recoveryCheckFailure = false, settingsFailure = false;
let deliveries = {}, rechecks = {}, repairs = {}, sub2Configured = true, loginStreamGate = null, loginFailures = 0;
let importSettings = { defaults: { group_ids: [], priority: 1, concurrency: 3 }, groups: [{id: 7, name: '稳定池'}, {id: 8, name: '<img unsafe>测试池'}], groups_available: true };
let settingsReadFailure = false, settingsReadGate = null, importSettingsSaveFailure = false, credentialsFailure = false;
const intervals = [];
let imports = [], historyFailure = false, historyGate = null, deleteGate = null;
let deletionFailures = new Map(), missingIDs = new Set(), acceptedImportIDs = null, importGate = null;
let confirmations = [], confirmResult = true, requests = [];
let detailGate = null, detailFailure = false, recoveryHistoryAvailable = true, recoveryHistoryTruncated = false;
let recoveryHistory = [{ id: 3, state: 'unknown', created_at: '2026-10-07T06:08:41Z', updated_at: '2026-10-07T06:08:59Z', last_error: '<img src=x onerror=alert(1)>身份待核对' },
    { id: 2, state: 'completed', created_at: '2026-10-06T01:00:00Z', updated_at: '2026-10-06T01:01:00Z' }];
const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => structuredClone(body) });
const location = { hash: '' };
const window = new Element();
const browserPreferences = new Map();
Object.assign(window, { alert: () => {},
    localStorage: { getItem: key => browserPreferences.get(key) ?? null, setItem: (key, value) => browserPreferences.set(key, value) },
    history: { state: null, pushState(_state, _title, hash) { location.hash = hash; } } });
const context = vm.createContext({ document, window, location, console, Event, CustomEvent, AbortController, AbortSignal, TextDecoder, setTimeout, clearTimeout, clearInterval() {},
    setInterval: (callback, delay) => { intervals.push({ callback, delay }); return intervals.length; }, CSS: { escape: String },
    fetch: async (url, options = {}) => {
        requests.push({ url, options });
        if (url === '/api/sub2/import-settings') {
            if (!options.method && settingsReadGate) { const pending = settingsReadGate; settingsReadGate = null; await pending.promise; }
            if (!options.method) return settingsReadFailure ? response({ message: '读取设置失败' }, 503) : response(importSettings);
            if (importSettingsSaveFailure) return response({ message: '默认参数保存失败' }, 503);
            importSettings.defaults = JSON.parse(options.body);
            return response(importSettings);
        }
        if (url.endsWith('/credentials') && options.method === 'PATCH') return response(credentialsFailure ? { message: '账号正在处理，请稍后再试' } : { success: true }, credentialsFailure ? 409 : 200);
        if (url === '/health') return response({ max_concurrent: 10, sub2_configured: sub2Configured });
        if (url === '/api/login/stream') {
            if (loginStreamGate) { const pending = loginStreamGate; loginStreamGate = null; await pending.promise; }
            const input = JSON.parse(options.body);
            const succeeded = loginFailures-- <= 0;
            const id = input.email.includes('second') ? 9002 : 9001;
            const payload = { type: 'result', success: succeeded, message: succeeded ? '登录成功' : 'temporary login failure', data: succeeded ? { Email: input.email, account_id: id, delivery: input.auto_deliver ? { id, account_id: id, state: 'queued' } : null } : null };
            return { ok: true, headers: new Map([['content-type', 'text/event-stream']]), body: new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(payload)}\n\n`)); controller.close(); } }) };
        }
        if (url === '/api/history') {
            if (historyGate) { const gate = historyGate; historyGate = null; await gate.promise; }
            if (historyFailure) return response({ message: 'fixture refresh unavailable' }, 503);
            return response({ data: rows, imports, sub2_configured: sub2Configured, imports_available: true, sub2_statuses: sub2Statuses, checks, recoveries, deliveries, rechecks, repairs, checks_available: true });
        }
        if (url === '/api/account-recovery/settings' && !options.method) return response(recoverySettings);
        if (url === '/api/account-recovery/settings' && options.method === 'PUT') {
            if (settingsFailure) return response({ message: '设置未保存' }, 503);
            recoverySettings = { ...recoverySettings, auto_recovery_enabled: Boolean(JSON.parse(options.body).auto_recovery_enabled) };
            return response(recoverySettings);
        }
        if (url === '/api/account-recovery/check' && options.method === 'POST') {
            if (recoveryCheckFailure) return response({ success: false, message: '请稍后重试，检测冷却中' }, 409);
            const id = JSON.parse(options.body).account_id;
            checks[id] = { latest_task: { id: 10, account_id: id, state: 'queued' } };
            return response({ success: true, batch: { id: 'manual-recovery-check' } });
        }
        if (url === '/api/account-recovery' && options.method === 'POST') {
            const id = JSON.parse(options.body).account_id;
            recoveries[id] = { ...recoveries[id], state: 'queued', resumable: false };
            return response({ success: true, task: recoveries[id] });
        }
        if (url.startsWith('/api/history/') && !options.method) {
            if (detailGate) { const gate = detailGate; detailGate = null; await gate.promise; }
            if (detailFailure) return response({ message: 'fixture detail unavailable' }, 503);
            const id = Number(url.split('/').pop());
            const account = rows.find(row => row.id === id);
            return response({ account, attempts: [], recovery_history: recoveryHistory, recovery_history_available: recoveryHistoryAvailable, recovery_history_truncated: recoveryHistoryTruncated });
        }
        if (url.startsWith('/api/history/') && options.method === 'DELETE') {
            if (deleteGate) { const gate = deleteGate; deleteGate = null; await gate.promise; }
            const id = Number(url.split('/').pop());
            assert.ok(options.signal, 'deletes must have a request timeout');
            if (deletionFailures.get(id) instanceof Error) throw deletionFailures.get(id);
            if (deletionFailures.has(id)) return response(deletionFailures.get(id).body, deletionFailures.get(id).status);
            rows = rows.filter(row => row.id !== id);
            if (missingIDs.has(id)) return response({ code: 'account_not_found', message: '账号不存在' }, 404);
            return response({ success: true, id });
        }
        if (url === '/api/sub2/import') {
            if (importGate) { const gate = importGate; importGate = null; await gate.promise; }
            const ids = JSON.parse(options.body).account_ids;
            assert.ok(ids.length <= 100);
            const accepted = ids.filter(id => acceptedImportIDs === null || acceptedImportIDs.has(id));
            const tasks = accepted.map(id => ({ account_id: id, state: 'imported' }));
            imports.push(...tasks);
            return response({ success: true, imports: tasks });
        }
        throw new Error(`Unexpected fixture URL ${url}`);
    }
});
vm.runInContext(fs.readFileSync(path.join(__dirname, 'client.js'), 'utf8'), context);
const flush = async () => { for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve)); };
const run = script => vm.runInContext(script, context);
const selected = () => Array.from(run('[...historySelectedIDs]')).sort((a, b) => Number(a) - Number(b));
const click = async id => { await el(id).emit('click'); await flush(); };
const change = async (id, value) => { if (typeof value === 'boolean') el(id).checked = value; else el(id).value = value; await el(id).emit(id === 'historySearchInput' ? 'input' : 'change'); await flush(); };
const gate = () => { let release; const promise = new Promise(resolve => { release = resolve; }); return { promise, release }; };
const deletes = () => requests.filter(item => item.options.method === 'DELETE');
(async () => {
    await flush();
    assert.equal(el('panel-login').hidden, false);
    assert.equal(el('panel-checks').hidden, true);
    assert.equal(el('historyActionBar').hidden, true);
    await click('tab-history');
    assert.equal(location.hash, '#panel-history');
    // Pool membership is independent from credential health, and unknown reads stay unknown.
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status({imported: true, exists: true, status: 'error'}))).kind"), 'present');
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status(null))).kind"), 'unknown');
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status({exists: false}))).kind"), 'absent');
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status({exists: false, stale: true}))).kind"), 'unknown');
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status({association_status: 'conflict', exists: false}))).kind"), 'unknown');
    run('historyImportsAvailable = false');
    assert.equal(run("historyPoolState(historyAccounts[0]).kind"), 'unknown');
    run('historyImportsAvailable = true');
    assert.match(el('historyGrid').innerHTML, /成功复活 2 次/);
    const recoveryTrigger = new Element();
    recoveryTrigger.dataset = { historyId: '1', historyAction: 'recovery-history' };
    recoveryTrigger.closest = selector => selector.includes('recovery-history') || selector === '[data-history-action]' ? recoveryTrigger : null;
    context.recoveryTrigger = recoveryTrigger;
    const gridQuerySelector = el('historyGrid').querySelector;
    el('historyGrid').querySelector = selector => selector.includes('recovery-history') ? recoveryTrigger : gridQuerySelector.call(el('historyGrid'), selector);
    detailGate = gate(); const pendingRecords = detailGate;
    await el('historyGrid').emit('pointerover', recoveryTrigger, { pointerType: 'mouse' });
    assert.equal(el('historyRecoveryPopover').hidden, false);
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /正在加载复活记录/);
    pendingRecords.release(); await flush();
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /2 次完成/);
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /3 次尝试/);
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /恢复结果待核对/);
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /&lt;img/);
    assert.doesNotMatch(el('historyRecoveryPopoverBody').innerHTML, /<img/);
    const recoveryMarkup = el('historyRecoveryPopoverBody').innerHTML;
    el('historyRecoveryPopoverBody').scrollTop = 48;
    const detailRequestsBeforeRefresh = requests.filter(item => item.url === '/api/history/1').length;
    await run('loadHistory()');
    assert.equal(el('historyRecoveryPopover').hidden, false, 'polling preserves the open popover');
    assert.equal(el('historyRecoveryPopoverBody').innerHTML, recoveryMarkup);
    assert.equal(el('historyRecoveryPopoverBody').scrollTop, 48);
    assert.equal(requests.filter(item => item.url === '/api/history/1').length, detailRequestsBeforeRefresh, 'unchanged polling does not reload records');
    await el('historyGrid').emit('click', recoveryTrigger);
    assert.equal(run('historyRecoveryPopoverPinned'), true, 'click pins the hover popover for touch and reading');
    await document.emit('keydown', document, { key: 'Escape' });
    assert.equal(el('historyRecoveryPopover').hidden, true);
    assert.equal(document.activeElement, recoveryTrigger);
    let focusMovedToPopover = false;
    await el('historyGrid').emit('keydown', recoveryTrigger, { key: 'Tab', preventDefault() { focusMovedToPopover = true; } });
    assert.equal(focusMovedToPopover, true, 'Tab reopens the dismissed popover');
    assert.equal(el('historyRecoveryPopover').hidden, false);
    assert.equal(document.activeElement, el('historyRecoveryPopoverClose'));
    let focusReturnedToTrigger = false;
    await el('historyRecoveryPopover').emit('keydown', el('historyRecoveryPopoverClose'), { key: 'Tab', shiftKey: true, preventDefault() { focusReturnedToTrigger = true; } });
    assert.equal(focusReturnedToTrigger, true, 'Shift+Tab returns to the count');
    assert.equal(document.activeElement, recoveryTrigger);
    await document.emit('pointerdown', document.body);
    detailFailure = true;
    await el('historyGrid').emit('focusin', recoveryTrigger); await flush();
    focusMovedToPopover = false;
    await el('historyGrid').emit('keydown', recoveryTrigger, { key: 'Tab', preventDefault() { focusMovedToPopover = true; } });
    assert.equal(focusMovedToPopover, true, 'Tab from the count enters the open popover');
    assert.equal(document.activeElement, el('historyRecoveryPopoverClose'));
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /fixture detail unavailable/);
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /重新加载/);
    detailFailure = false; recoveryHistory = [];
    await run('loadHistoryRecoveryRecords()');
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /暂无复活记录/);
    recoveryHistoryAvailable = false;
    await run('loadHistoryRecoveryRecords()');
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /复活记录暂不可用/);
    recoveryHistoryAvailable = true; recoveryHistoryTruncated = true;
    await run('loadHistoryRecoveryRecords()');
    assert.match(el('historyRecoveryPopoverBody').innerHTML, /最近 100 条/);
    await document.emit('pointerdown', document.body);
    assert.equal(el('historyRecoveryPopover').hidden, true, 'outside press closes the popover');
    detailGate = gate(); const abandonedRecords = detailGate;
    run("openHistoryRecoveryPopover('1', recoveryTrigger); closeHistoryRecoveryPopover()");
    abandonedRecords.release(); await flush();
    assert.equal(el('historyRecoveryPopover').hidden, true, 'late responses cannot reopen a closed popover');
    recoveryHistoryTruncated = false;
    el('historyGrid').querySelector = gridQuerySelector;
    // One native modal owns each decision. Content stays text, and cancellation restores focus.
    confirmResult = null;
    el('historySelectAll').focus();
    const confirmation = run('confirmAction({ title: "删除账号", message: "<img src=x onerror=alert(1)>\\n第二行", confirmLabel: "确认删除", danger: true })');
    assert.equal(el('actionConfirmDialog').open, true);
    assert.equal(document.activeElement, el('actionConfirmCancel'), 'the safe action receives initial focus');
    assert.equal(el('actionConfirmMessage').textContent, '<img src=x onerror=alert(1)>\n第二行');
    assert.equal(el('actionConfirmMessage').innerHTML, '', 'untrusted account names must never become markup');
    assert.equal(el('actionConfirmAccept').classList.contains('btn-danger'), true);
    assert.equal(await run('confirmAction({ title: "duplicate", message: "must not replace the decision" })'), false);
    assert.equal(el('actionConfirmTitle').textContent, '删除账号');
    let confirmationTrapped = false;
    el('actionConfirmAccept').focus();
    await el('actionConfirmDialog').emit('keydown', el('actionConfirmAccept'), { key: 'Tab', preventDefault() { confirmationTrapped = true; } });
    assert.equal(confirmationTrapped, true);
    assert.equal(document.activeElement, el('actionConfirmCancel'));
    confirmationTrapped = false;
    await el('actionConfirmDialog').emit('keydown', el('actionConfirmCancel'), { key: 'Tab', shiftKey: true, preventDefault() { confirmationTrapped = true; } });
    assert.equal(confirmationTrapped, true);
    assert.equal(document.activeElement, el('actionConfirmAccept'));
    await el('actionConfirmDialog').emit('keydown', el('actionConfirmAccept'), { key: 'Escape' });
    assert.equal(await confirmation, false);
    assert.equal(el('actionConfirmDialog').open, false);
    assert.equal(document.activeElement, el('historySelectAll'));
    const acceptedConfirmation = run('confirmAction({ title: "导入", message: "ready" })');
    assert.equal(el('actionConfirmAccept').classList.contains('btn-danger'), false, 'danger styling resets between uses');
    await click('actionConfirmAccept');
    assert.equal(await acceptedConfirmation, true);
    const closedConfirmation = run('confirmAction({ title: "关闭", message: "ready" })');
    el('actionConfirmDialog').close();
    assert.equal(await closedConfirmation, false, 'external close cancels the pending decision');
    const firstDecision = run('confirmAction({ title: "first", message: "ready" })');
    run('closeActionConfirmation(true)');
    const nextDecision = run('confirmAction({ title: "next", message: "ready" })');
    await flush();
    assert.equal(el('actionConfirmDialog').open, true, 'a queued close event must not cancel a newly opened dialog');
    assert.equal(el('actionConfirmTitle').textContent, 'next');
    await click('actionConfirmCancel');
    assert.equal(await firstDecision, true);
    assert.equal(await nextDecision, false);
    const navigatedConfirmation = run('confirmAction({ title: "切页", message: "ready" })');
    await click('tab-login');
    assert.equal(await navigatedConfirmation, false, 'navigation cancels pending actions');
    assert.equal(document.activeElement, el('tab-login'), 'cancel must not restore focus into the hidden panel');
    await click('tab-history');
    confirmResult = true;
    assert.equal((el('historyGrid').innerHTML.match(/data-history-select /g) || []).length, 20, 'one general checkbox per row');
    assert.doesNotMatch(el('historyGrid').innerHTML, /data-history-import-select/);
    await change('historySelectAll', true);
    assert.equal(el('historyActionBar').hidden, false);
    assert.match(el('historyGrid').innerHTML, /Sub2 正常/);
    assert.match(el('historyGrid').innerHTML, /可调度/);
    assert.match(el('historyGrid').innerHTML, /Sub2 已禁用/);
    assert.equal(el('autoRecoveryToggle').checked, false);
    await change('autoRecoveryToggle', true);
    assert.equal(recoverySettings.auto_recovery_enabled, true);
    assert.equal(el('autoRecoveryToggle').checked, true);
    assert.equal(el('autoRecoveryStatus').textContent, '已开启 · 后台运行');
    assert.match(el('autoRecoveryDetail').textContent, /每 60 秒/);
    assert.match(el('autoRecoveryDetail').textContent, /关闭页面后仍继续/);
    assert.match(el('historyGrid').innerHTML, /最近登录成功/);
    assert.equal(intervals.length, 1, 'history has one background polling loop');
    assert.equal(intervals[0].delay, 5000);
    assert.equal(run('historyRefreshDelay()'), 60000, 'enabled idle polling is once per minute');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '1')).disabled"), true, 'healthy account cannot be recovered');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '1')).reason"), 'Sub2 正常，无需恢复');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).disabled"), true, 'disabled account cannot be recovered');
    sub2Statuses[2] = { ...sub2Statuses[2], sub2_account_id: 200, status: 'error', error_message: '401 credential expired', association_status: 'linked' };
    imports = [{ account_id: 2, sub2_account_id: 200, state: 'imported', operation_id: 'linked-200' }];
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /已关联 Sub2/);
    assert.match(el('historyGrid').innerHTML, /Sub2：401 credential expired/);
    assert.match(el('historyGrid').innerHTML, /data-history-action="recover" data-history-id="2"[^>]*>检测并恢复/);
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).disabled"), false);
    await run("checkAndRecoverHistory('2')");
    assert.equal(requests.filter(item => item.url === '/api/account-recovery/check').length, 1);
    assert.deepEqual(JSON.parse(requests.find(item => item.url === '/api/account-recovery/check').options.body), { account_id: 2 });
    assert.match(el('historyGrid').innerHTML, /检测已排队/);
    assert.doesNotMatch(el('historyGrid').innerHTML, /恢复完成/, 'queued checks must not claim successful recovery');
    assert.equal(run('historyRefreshDelay()'), 5000, 'active tasks are refreshed promptly');
    await run("checkAndRecoverHistory('2')");
    assert.equal(requests.filter(item => item.url === '/api/account-recovery/check').length, 1, 'duplicate clicks during checking do not submit again');
    checks[2] = { latest_task: { id: 10, state: 'finished' }, last_result: { id: 10, state: 'finished', outcome: 'unauthorized', http_status: 401, freshness: 'current' } };
    recoveries[2] = { state: 'logging_in', updated_at: '2026-10-05T11:00:00Z' };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /HTTP 401/);
    assert.match(el('historyGrid').innerHTML, /正在重新登录/);
    assert.match(el('historyGrid').innerHTML, /disabled>恢复中…/);
    sub2Statuses[2] = { ...sub2Statuses[2], status: 'active', schedulable: true, effective_schedulable: true };
    recoveries[2] = {};
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /Sub2 正常 · 本地凭据失效待处理/);
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).disabled"), false,
        'an AUTH 401 keeps recovery available when Sub2 is healthy');
    checks[2].last_result = { outcome: 'error', error_code: 'credential_missing', freshness: 'current' };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /本地凭据失效待处理/);
    checks[2].last_result = { outcome: 'unauthorized', http_status: 401, freshness: 'current' };
    sub2Statuses[2] = { ...sub2Statuses[2], rate_limit_reset_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(), effective_schedulable: false };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /Sub2 限流至/);
    assert.match(el('historyGrid').innerHTML, /暂不可调度/);
    assert.match(el('historyGrid').innerHTML, /本地凭据失效待处理/);
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).disabled"), false,
        'a confirmed AUTH 401 keeps immediate recovery available during a Sub2 cooldown');
    checks[2].eligible = true;
    run("historySelectedIDs.clear(); window.AccountChecks = { startMany: async ids => { globalThis.immediateCheckIDs = ids; return true; } }");
    rows = [{ id: 2, email: 'account002@example.test', status: 'active' }];
    imports = [{ account_id: 2, sub2_account_id: 200, state: 'imported', operation_id: 'linked-200' }];
    sub2Statuses[2] = { ...sub2Statuses[2], rate_limit_reset_at: '', effective_schedulable: true };
    await click('refreshHistoryBtn');
    await click('historyCheckNowBtn');
    assert.deepEqual(Array.from(run('immediateCheckIDs')), ['2'], 'history immediate check submits the visible linked account');
    assert.match(el('historyCheckNowStatus').textContent, /已提交 1 个/);
    rows = Array.from({ length: 25 }, (_, index) => ({ id: index + 1, email: `account${String(index + 1).padStart(3, '0')}@example.test`, status: ['active', 'error', 'running', 'deleted', 'interrupted'][index % 5] }));
    run('historySelectedIDs.clear()');
    await click('refreshHistoryBtn');
    run("historyAccounts.forEach((account, index) => { if (index < 20) historySelectedIDs.add(account.id); }); renderHistory()");
    recoveries[2] = { state: 'failed', last_error: '登录需要额外验证' };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /恢复失败：登录需要额外验证/);
    recoveryCheckFailure = true;
    await run("checkAndRecoverHistory('2')");
    assert.match(el('historyGrid').innerHTML, /请稍后重试，检测冷却中/);
    assert.doesNotMatch(el('historyGrid').innerHTML, /恢复完成/);
    recoveryCheckFailure = false;
    sub2Statuses[2].status = 'active'; sub2Statuses[2].schedulable = false;
    await click('refreshHistoryBtn');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).reason"), '账号已人工暂停，保持暂停状态');
    recoveries[2].resumable = true;
    await click('refreshHistoryBtn');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).disabled"), false, 'a checkpointed recovery can continue while scheduling is paused');
    assert.match(el('historyGrid').innerHTML, />继续恢复/);
    await run("checkAndRecoverHistory('2')");
    assert.deepEqual(JSON.parse(requests.find(item => item.url === '/api/account-recovery' && item.options.method === 'POST').options.body), { account_id: 2 });
    recoveries[2] = { state: 'failed' };
    checks[2] = { latest_task: { state: 'skipped', skip_reason: 'cooldown' }, last_result: { outcome: 'ok', freshness: 'stale' } };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /检测已跳过：检测冷却中/);
    assert.match(el('historyGrid').innerHTML, /上次凭据调用正常 · 旧凭据结果/);
    assert.doesNotMatch(el('historyGrid').innerHTML, /当前凭据调用正常/);
    sub2Statuses[2].exists = false;
    await click('refreshHistoryBtn');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).reason"), '该账号已从 Sub2 删除');
    for (const [association, label] of [['unmatched', '未找到关联'], ['ambiguous', '多条匹配待确认'], ['conflict', '身份不一致']]) {
        sub2Statuses[2] = { imported: false, exists: false, unknown: true, association_status: association, error: '<reason>' };
        await click('refreshHistoryBtn');
        assert.match(el('historyGrid').innerHTML, new RegExp(`Sub2 ${label}`));
        assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).disabled"), true);
    }
    sub2Statuses[2] = null;
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /Sub2 状态待核对/, 'missing Sub2 status must not imply confirmed absence');
    recoverySettings = { ...recoverySettings, scanning: true, last_scan_at: '2026-10-05T11:01:00Z', next_scan_at: '2026-10-05T11:02:00Z', last_error: '出口暂不可用', summary: 'Sub2 异常 2 个，提交检测 1 个，恢复 0 个，等待或冷却 1 个' };
    await run('loadRecoverySettings()');
    assert.equal(el('autoRecoveryStatus').textContent, '正在巡检…');
    assert.match(el('autoRecoveryDetail').textContent, /上次巡检/);
    assert.match(el('autoRecoveryDetail').textContent, /下次巡检/);
    assert.match(el('autoRecoveryDetail').textContent, /出口暂不可用/);
    assert.match(el('autoRecoveryDetail').textContent, /提交检测 1 个/);
    recoverySettings = { ...recoverySettings, running: false, blocked_reason: '恢复任务存储故障，已停止派发' };
    await run('loadRecoverySettings()');
    assert.equal(el('autoRecoveryToggle').checked, true, 'a blocked worker does not change the saved switch');
    assert.equal(el('autoRecoveryStatus').textContent, '已开启 · 暂停运行', 'blocked takes precedence over a stale scanning flag');
    assert.match(el('autoRecoveryDetail').textContent, /暂停原因：恢复任务存储故障，已停止派发/);
    assert.match(el('autoRecoveryDetail').textContent, /上次巡检/);
    assert.doesNotMatch(el('autoRecoveryDetail').textContent, /关闭页面后仍继续|下次巡检/);
    recoverySettings.auto_recovery_enabled = false;
    await run('loadRecoverySettings()');
    assert.equal(el('autoRecoveryStatus').textContent, '已关闭');
    assert.equal(el('autoRecoveryToggle').checked, false);
    assert.match(el('autoRecoveryDetail').textContent, /恢复任务存储故障，已停止派发/);
    assert.doesNotMatch(el('autoRecoveryDetail').textContent, /开启后立即巡检|关闭页面后仍继续|下次巡检/);
    recoverySettings = { ...recoverySettings, auto_recovery_enabled: true, running: true, scanning: false, blocked_reason: '' };
    await run('loadRecoverySettings()');
    assert.equal(el('autoRecoveryStatus').textContent, '已开启 · 后台运行', 'an ordinary scan warning does not mean the worker is blocked');
    assert.match(el('autoRecoveryDetail').textContent, /巡检提示：出口暂不可用/);
    assert.match(el('autoRecoveryDetail').textContent, /关闭页面后仍继续/);
    assert.match(el('autoRecoveryDetail').textContent, /下次巡检/);
    assert.doesNotMatch(el('autoRecoveryDetail').textContent, /暂停原因/);
    settingsFailure = true;
    await change('autoRecoveryToggle', false);
    assert.equal(el('autoRecoveryToggle').checked, true, 'unsaved changes roll back to server state');
    assert.equal(el('autoRecoveryStatus').textContent, '设置未保存');
    settingsFailure = false;
    recoverySettings = { auto_recovery_enabled: true, interval_seconds: 60 };
    checks = {}; recoveries = {}; imports = [];
    sub2Statuses[2] = { configured: true, imported: true, exists: true, status: 'disabled', schedulable: false };
    run('historyRecoveryNotices.clear()');
    await change('autoRecoveryToggle', false);
    assert.equal(run('historyRefreshDelay()'), 300000, 'disabled idle polling returns to five minutes');
    for (const state of ['queued', 'checking']) {
        repairs = { 2: { state } };
        await run('loadHistory()');
        assert.equal(run('historyRefreshDelay()'), 5000, `${state} credential repairs refresh promptly with automatic recovery off`);
    }
    repairs = { 2: { state: 'retry_wait', next_retry_at: new Date(Date.now() + 600000).toISOString() } };
    await run('loadHistory()');
    assert.equal(run('historyRefreshDelay()'), 60000, 'waiting credential repairs use the retry polling interval');
    repairs[2].requires_action = true;
    await run('loadHistory()');
    assert.equal(run('historyRefreshDelay()'), 300000, 'repairs needing user action do not keep retry polling active');
    repairs = { 2: { state: 'completed' } };
    await run('loadHistory()');
    assert.equal(run('historyRefreshDelay()'), 300000, 'completed repairs return to idle polling');
    repairs = {};
    await run('loadHistory()');
    recoverySettings.last_error = '手动恢复未能入队';
    await run('loadRecoverySettings()');
    assert.match(el('autoRecoveryDetail').textContent, /手动恢复未能入队/, 'manual failure remains visible with automatic recovery off');
    let releasePoll;
    historyGate = { promise: new Promise(resolve => { releasePoll = resolve; }) };
    run('historyRefreshAt = 0');
    const beforePoll = requests.filter(item => item.url === '/api/history').length;
    const firstPoll = run('pollHistory()');
    await flush();
    await run('pollHistory()');
    assert.equal(requests.filter(item => item.url === '/api/history').length, beforePoll + 1, 'poll ticks never overlap');
    releasePoll(); await firstPoll;
    await click('tab-login');
    assert.equal(el('historyActionBar').hidden, true, 'history actions must not appear in login');
    await click('tab-history');
    assert.equal(el('historyActionBar').hidden, false, 'returning to history restores retained selection');
    assert.equal(selected().length, 20, 'ordinary select-all includes every status');
    assert.equal(el('historySelectAll').checked, true);
    assert.equal(el('historyBatchStatus').hidden, true, 'no persistent time estimate clutter');
    el('panel-history').scrollTop = 1500;
    await click('historyNextPageBtn');
    assert.equal(el('panel-history').scrollTop, 0, 'pagination resets the actual scroll container');
    assert.match(el('historySelectionStatus').textContent, /20 个不在当前页/);
    await change('historySelectAll', true); assert.equal(selected().length, 25);
    await change('historySelectAll', false); assert.equal(selected().length, 20, 'unselect-all changes only current page');
    await change('historySearchInput', 'account001');
    assert.match(el('historySelectionStatus').textContent, /已选 20 个 · 19 个不在当前页/);
    const beforeCancel = deletes().length;
    confirmResult = false;
    await click('historyBatchDeleteBtn');
    assert.equal(deletes().length, beforeCancel, 'cancel must issue no delete requests');
    assert.match(confirmations.at(-1), /20 个账号/);
    assert.match(confirmations.at(-1), /19 个不在当前页/);
    assert.match(confirmations.at(-1), /不会删除 Sub2/);
    assert.equal(el('actionConfirmAccept').textContent, '确认删除');
    confirmResult = true;
    el('historyClearSelectionBtn').focus();
    await click('historyClearSelectionBtn'); assert.equal(selected().length, 0);
    assert.equal(el('historyActionBar').hidden, true);
    assert.equal(document.activeElement, el('historySelectAll'), 'clearing selection keeps keyboard focus in history');
    await change('historySearchInput', '');
    // Refresh keeps valid selections even if the account changes status.
    run("historySelectedIDs.add('1'); historySelectedIDs.add('2'); renderHistory()");
    rows[1].status = 'active';
    await click('refreshHistoryBtn'); assert.deepEqual(selected(), ['1', '2']);
    run("historyRefreshTokens.set('1', 'fixture-token'); historyDetailID = '1'; historyDetail.hidden = false");
    deletionFailures.set(2, { status: 409, body: { message: '账号正在登录，请稍后重试', code: 'account_busy' } });
    deleteGate = gate(); const activeDelete = deleteGate;
    const operation = run("deleteHistoryAccounts([...historySelectedIDs])");
    await flush();
    assert.equal(el('historySelectAll').disabled, true);
    assert.equal(el('startBtn').disabled, true);
    const requestsDuring = deletes().length;
    await run("deleteHistoryAccounts(['2'])");
    assert.equal(deletes().length, requestsDuring, 'duplicate delete is blocked');
    activeDelete.release(); await operation;
    assert.deepEqual(selected(), ['2'], 'only failure remains selected');
    assert.match(el('historyBatchStatus').textContent, /已删除 1 个，失败 1 个/);
    assert.match(el('historyBatchStatus').textContent, /记录 #2：账号正在登录/);
    assert.equal(run("historyRefreshTokens.has('1')"), false);
    assert.equal(el('historyDetail').hidden, true);
    assert.ok(historyEvents.includes('auth-history-changed'));
    // A stale missing ID is idempotent; unrelated 404s must remain errors.
    deletionFailures.delete(2); missingIDs.add(2);
    await click('historyBatchDeleteBtn'); assert.deepEqual(selected(), []);
    deletionFailures.set(3, { status: 404, body: { message: 'Route not found' } });
    await run("deleteHistory('3')"); assert.deepEqual(selected(), ['3']);
    assert.match(el('historyBatchStatus').textContent, /失败 1 个/);
    const timeout = new Error('fixture timeout'); timeout.name = 'TimeoutError';
    deletionFailures.set(3, timeout);
    await run("deleteHistoryAccounts(['3'])"); assert.deepEqual(selected(), ['3']);
    assert.match(el('historyBatchStatus').textContent, /请求超时，结果待确认/);
    assert.equal(el('historySelectAll').disabled, false, 'timeout releases action lock');
    // A refresh error after deletion does not resurrect successful selections.
    deletionFailures.delete(3); historyFailure = true;
    await run("deleteHistoryAccounts(['3'])");
    assert.deepEqual(selected(), []);
    assert.equal(el('historyBatchDeleteBtn').disabled, true);
    assert.match(el('historyRefreshStatus').textContent, /fixture refresh unavailable/);
    historyFailure = false; await click('refreshHistoryBtn');
    // Selection made before a refresh cannot start destructive work until it settles.
    run("historySelectedIDs.add('4'); renderHistory()");
    historyGate = gate(); const activeRefresh = historyGate;
    const refresh = run('loadHistory()'); await flush();
    const beforeBusy = deletes().length;
    await run("deleteHistoryAccounts(['4'])"); assert.equal(deletes().length, beforeBusy);
    activeRefresh.release(); await refresh;
    await click('historyClearSelectionBtn');
    // Import accepts eligible IDs only and preserves both skipped and unaccepted IDs.
    rows = [{ id: 101, email: 'a@example.test', status: 'active' }, { id: 102, email: 'b@example.test', status: 'active' }, { id: 103, email: 'c@example.test', status: 'error' }];
    imports = []; await click('refreshHistoryBtn'); await change('historySelectAll', true);
    const importsBeforeCancel = requests.filter(item => item.url === '/api/sub2/import').length;
    confirmResult = false;
    await run('batchImportHistory()');
    assert.equal(requests.filter(item => item.url === '/api/sub2/import').length, importsBeforeCancel, 'cancelled import issues no request');
    assert.equal(el('deliveryOptionsTitle').id, 'deliveryOptionsTitle');
    confirmResult = null;
    const lockedImport = run('batchImportHistory()'); await flush();
    run('historyLoading = true');
    await el('deliveryOptionsForm').emit('submit'); await lockedImport;
    assert.equal(requests.filter(item => item.url === '/api/sub2/import').length, importsBeforeCancel, 'a refresh that starts during confirmation blocks stale submission');
    run('historyLoading = false');
    acceptedImportIDs = new Set([101]); importGate = gate(); const activeImport = importGate;
    const importOperation = run('batchImportHistory()'); await flush();
    await run('batchImportHistory()');
    assert.equal(requests.filter(item => item.url === '/api/sub2/import').length, importsBeforeCancel, 'duplicate pending import cannot submit');
    run("historyAccounts.find(account => account.id === '103').status = 'active'");
    await el('deliveryOptionsForm').emit('submit'); await flush();
    assert.equal(el('historyBatchDeleteBtn').disabled, true);
    assert.match(confirmations.at(-1), /另有 1 个选中账号/);
    activeImport.release(); await importOperation;
    assert.deepEqual(selected(), ['102', '103']);
    assert.deepEqual(JSON.parse(requests.filter(item => item.url === '/api/sub2/import').at(-1).options.body).account_ids, [101, 102]);
    assert.match(el('historyBatchStatus').textContent, /1 个未受理/);
    // Successful re-login clears only successes, retaining ineligible and failed choices.
    rows = [{ id: 201, email: 'a@example.test', status: 'active' }, { id: 202, email: 'b@example.test', status: 'error' }, { id: 203, email: 'c@example.test', status: 'error' }];
    imports = []; await click('refreshHistoryBtn'); await change('historySelectAll', true);
    run("globalThis.reloginCalls = []; performHistoryRelogin = async id => { reloginCalls.push(id); return { success: id === '202' }; }");
    confirmResult = false;
    await run('batchReloginHistory()');
    assert.equal(run('reloginCalls.length'), 0, 'cancelled relogin must not start a login');
    assert.equal(el('actionConfirmTitle').textContent, '批量复活账号');
    confirmResult = true;
    await run('batchReloginHistory()');
    assert.deepEqual(Array.from(run('reloginCalls')), ['202', '203']);
    assert.deepEqual(selected(), ['201', '203']);
    assert.match(el('historyBatchStatus').textContent, /成功 1，失败 1，跳过 1/);
    confirmResult = null;
    const staleRelogin = run('batchReloginHistory()');
    run("historyAccounts.find(account => account.id === '203').status = 'active'");
    await click('actionConfirmAccept'); await staleRelogin;
    assert.equal(run('reloginCalls.length'), 2, 'accounts that become ineligible during confirmation must be skipped');
    confirmResult = true;
    // Cross-page import respects the endpoint's 100-ID request limit.
    rows = Array.from({ length: 101 }, (_, index) => ({ id: 1000 + index, email: `bulk${index}@example.test`, status: 'active' }));
    imports = []; acceptedImportIDs = null; await click('refreshHistoryBtn');
    run('historyAccounts.forEach(account => historySelectedIDs.add(account.id)); renderHistory()');
    const priorImports = requests.filter(item => item.url === '/api/sub2/import').length;
    await run('batchImportHistory()');
    const bulkRequests = requests.filter(item => item.url === '/api/sub2/import').slice(priorImports);
    assert.deepEqual(bulkRequests.map(item => JSON.parse(item.options.body).account_ids.length), [100, 1]);
    assert.deepEqual(selected(), []);
    // Batch login results map by email to the persisted history ID before importing.
    rows = [{ id: 2001, email: 'Login@Example.test', status: 'active' }, { id: 2002, email: 'skip@example.test', status: 'error' }];
    imports = []; acceptedImportIDs = null; await click('refreshHistoryBtn');
    run("results = [{ Email: 'login@example.test' }, { Email: 'missing@example.test' }]");
    confirmResult = true;
    const resultImportStart = requests.filter(item => item.url === '/api/sub2/import').length;
    await run('importLoginResultsToSub2()');
    const resultImports = requests.filter(item => item.url === '/api/sub2/import').slice(resultImportStart);
    assert.deepEqual(resultImports.map(item => JSON.parse(item.options.body).account_ids), [[2001]]);
    assert.match(el('batchImportStatus').textContent, /Sub2 导入完成：1 \/ 1，成功 1/);
    assert.match(confirmations.at(-1), /另有 1 个结果/);
    // A submission that accepts nothing must settle the visible login status.
    imports = [];
    acceptedImportIDs = new Set();
    await run('importLoginResultsToSub2()');
    assert.equal(run('batchImportProgress.running'), false, 'zero accepted imports must not remain running');
    assert.equal(el('batchImportStatus').hidden, false, 'import feedback remains visible on the login panel');
    assert.match(el('batchImportStatus').textContent, /没有账号被 Sub2 受理/);
    // Accepted and unaccepted IDs are both reflected in the terminal result.
    rows[1].status = 'active';
    imports = [];
    acceptedImportIDs = new Set([2001]);
    run("results = [{ Email: 'login@example.test' }, { Email: 'skip@example.test' }]");
    await run('importLoginResultsToSub2()');
    assert.equal(run('batchImportProgress.running'), false, 'partial acceptance must settle the import status');
    assert.match(el('batchImportStatus').textContent, /成功 1，失败 1/);
    // A history read failure must not reuse a previous terminal import state.
    acceptedImportIDs = null;
    historyFailure = true;
    await run('importLoginResultsToSub2()');
    assert.equal(run('batchImportProgress.running'), false, 'history read failure must settle the import status');
    assert.match(el('batchImportStatus').textContent, /账号历史读取失败/);
    historyFailure = false;
    // Polling timeout returns a non-running pending result for later refresh.
    imports = [{ account_id: 2001, state: 'queued' }];
    const timedOutImport = await run('waitForImportCompletion(["2001"], 0)');
    assert.equal(timedOutImport.running, false, 'poll timeout must clear running');
    assert.equal(timedOutImport.pending, 1);
    rows = [{ id: 1000, email: 'details@example.test', status: 'active' }];
    imports = []; await click('refreshHistoryBtn');
    // History details keep keyboard focus inside the dialog and return it to the opener.
    run("historySelectedIDs.add('1000'); renderHistory()");
    el('historyBatchDeleteBtn').disabled = false;
    el('historyBatchDeleteBtn').dataset.historyId = '1000';
    await run("openHistoryDetails('1000', historyBatchDeleteBtn)");
    assert.equal(el('historyDetail').hidden, false);
    assert.equal(document.activeElement, el('historyDetail'), 'opening details moves focus into the dialog');
    el('historyDetailClose').focus();
    let trapped = false;
    await document.emit('keydown', document, { key: 'Tab', preventDefault() { trapped = true; } });
    assert.equal(trapped, true, 'Tab at the dialog edge is trapped');
    trapped = false;
    await document.emit('keydown', document, { key: 'Tab', shiftKey: true, preventDefault() { trapped = true; } });
    assert.equal(trapped, true, 'Shift+Tab at the dialog edge is trapped');
    await document.emit('keydown', document, { key: 'Escape', preventDefault() {} });
    assert.equal(el('historyDetail').hidden, true);
    assert.equal(document.activeElement, el('historyBatchDeleteBtn'), 'closing details restores opener focus');
    await run("openHistoryDetails('1000', historyBatchDeleteBtn)");
    run("historyAccounts = historyAccounts.filter(account => account.id !== '1000'); closeHistoryDetails()");
    assert.equal(document.activeElement, el('historySelectAll'), 'removed opener falls back to history selection');
    // Navigation uses one selected/focusable tab and supports keyboard and history.
    run("historyDetailID = '123'; historyDetail.hidden = false");
    el('historySearchInput').focus();
    window.AuthTabs.select('checks');
    assert.equal(el('historyDetail').hidden, true, 'leaving history closes stale details');
    assert.equal(run('historyDetailID'), '');
    assert.equal(document.activeElement, el('tab-checks'), 'programmatic switch must move focus out of hidden panel');
    assert.equal(location.hash, '#accountChecksPanel');
    const transitions = historyEvents.filter(name => name === 'auth-tab-changed').length;
    window.AuthTabs.select('checks');
    assert.equal(historyEvents.filter(name => name === 'auth-tab-changed').length, transitions, 'same-tab selection must not trigger recovery again');
    let prevented = false;
    await el('tab-checks').emit('keydown', el('tab-checks'), { key: 'ArrowRight', preventDefault() { prevented = true; } });
    assert.equal(prevented, true); assert.equal(document.activeElement, el('tab-login'));
    await el('tab-login').emit('keydown', el('tab-login'), { key: 'End', preventDefault() {} });
    assert.equal(document.activeElement, el('tab-checks'));
    location.hash = '#panel-history'; await window.emit('popstate');
    assert.equal(el('panel-history').hidden, false);
    location.hash = '#accountChecksPanel'; await window.emit('hashchange');
    assert.equal(el('panel-checks').hidden, false);
    assert.deepEqual(tabs.map(tab => tab.tabIndex), [-1, -1, 0]);
    assert.deepEqual(tabs.map(tab => tab.attributes['aria-selected']), ['false', 'false', 'true']);
    // One parameter form serves login batches and manual imports; saving defaults is explicit.
    confirmResult = null;
    let selectedGroups = [7];
    el('deliveryGroups').querySelectorAll = () => selectedGroups.map(id => ({ dataset: { deliveryGroup: String(id) } }));
    const parameters = run('chooseDeliveryOptions()'); await flush();
    assert.match(el('deliveryGroups').innerHTML, /&lt;img unsafe&gt;/);
    assert.doesNotMatch(el('deliveryGroups').innerHTML, /<img/);
    el('deliveryPriority').value = '4'; el('deliveryConcurrency').value = '12'; el('deliverySaveDefault').checked = true;
    el('deliveryNamePrefix').value = '  主池🌙  ';
    importSettingsSaveFailure = true;
    await el('deliveryOptionsForm').emit('submit');
    assert.equal(el('deliveryOptionsDialog').open, true, 'failed default save retains the editable form');
    assert.match(el('deliveryOptionsStatus').textContent, /保存失败/);
    assert.equal(run('deliveryBatchOptions.priority'), 1, 'failed save does not silently apply the requested defaults');
    importSettingsSaveFailure = false;
    await el('deliveryOptionsForm').emit('submit'); await parameters;
    assert.deepEqual(importSettings.defaults, { group_ids: [7], priority: 4, concurrency: 12, name_prefix: '主池🌙' });
    assert.equal(el('accountNamePrefix').value, '主池🌙', 'saving dialog options synchronizes the login-page prefix');
    assert.match(el('deliveryOptionsSummary').textContent, /稳定池.*优先级 4.*账号并发 12/);
    const unavailable = run('chooseDeliveryOptions()'); settingsReadFailure = true; await flush();
    // The already-running read may have completed before this flag; a fresh open exercises failure.
    await click('deliveryOptionsCancel'); await unavailable;
    const unavailableRead = run('chooseDeliveryOptions()'); await flush();
    assert.equal(el('deliveryOptionsApply').disabled, true);
    assert.equal(el('deliveryNamePrefix').disabled, true, 'a failed settings read keeps the prefix disabled until a successful reload');
    assert.match(el('deliveryOptionsStatus').textContent, /读取设置失败/);
    await click('deliveryOptionsCancel'); await unavailableRead; settingsReadFailure = false;
    importSettings.groups_available = false;
    const unavailableGroups = run('chooseDeliveryOptions()'); await flush();
    assert.equal(el('deliveryOptionsApply').disabled, true, 'unavailable group catalog cannot submit a grouped batch');
    await click('deliveryOptionsCancel'); await unavailableGroups; importSettings.groups_available = true;
    await run('loadDeliverySettings()');
    // Automatic delivery is a persisted server task, distinct from login success.
    const manualImportsBeforeDelivery = requests.filter(item => item.url === '/api/sub2/import').length;
    assert.equal(el('autoDeliverToggle').checked, true, 'configured Sub2 defaults new batches to automatic delivery');
    await change('autoDeliverToggle', false);
    assert.equal(browserPreferences.get('kkai-auth:auto-deliver'), 'false');
    await run('loadServerCapabilities()');
    assert.equal(el('autoDeliverToggle').checked, false, 'a capabilities refresh preserves the local choice');
    sub2Configured = false;
    await run('loadServerCapabilities()');
    assert.equal(el('autoDeliverToggle').disabled, true);
    assert.equal(el('autoDeliverToggle').checked, false);
    assert.match(el('autoDeliverHint').textContent, /Sub2 未配置/);
    sub2Configured = true;
    await run('loadServerCapabilities()');
    await change('autoDeliverToggle', true);
    assert.equal(browserPreferences.get('kkai-auth:auto-deliver'), 'true');
    rows = [{ id: 9001, email: 'first@example.test', status: 'active' }, { id: 9002, email: 'second@example.test', status: 'active' }];
    imports = []; checks = {}; recoveries = {}; deliveries = {};
    run("accounts = [{ id: 0, email: 'first@example.test', password: 'synthetic-only', totp_secret: 'fixture' }, { id: 1, email: 'second@example.test', password: 'synthetic-only', totp_secret: 'fixture' }]; results = []");
    loginStreamGate = gate(); const firstLogin = loginStreamGate; loginFailures = 1;
    const loginRequestsBefore = requests.filter(item => item.url === '/api/login/stream').length;
    el('proxyInput').value = 'direct';
    const batch = run('runAccounts(accounts)'); await flush();
    assert.equal(el('autoDeliverToggle').disabled, true);
    await change('autoDeliverToggle', false); // Simulate a preference change while the first request is in flight.
    run('deliveryBatchOptions = {group_ids:[8],priority:20,concurrency:25}');
    el('accountNamePrefix').value = '下一批'; await el('accountNamePrefix').emit('input');
    firstLogin.release(); await batch;
    assert.deepEqual(requests.filter(item => item.url === '/api/login/stream').slice(loginRequestsBefore).map(item => JSON.parse(item.options.body).auto_deliver), [true, true], 'every account uses its frozen batch choice');
    assert.deepEqual(requests.filter(item => item.url === '/api/login/stream').slice(loginRequestsBefore).map(item => JSON.parse(item.options.body).delivery_options), [importSettings.defaults, importSettings.defaults], 'all accounts keep the original batch groups and scheduling parameters');
    assert.deepEqual(requests.filter(item => item.url === '/api/login/stream').slice(loginRequestsBefore).map(item => JSON.parse(item.options.body).proxy), ['direct', 'direct'], 'login batches preserve an explicit server IPv4 route');
    await window.retryAccount(0);
    assert.equal(JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body).auto_deliver, true, 'login retries retain the original delivery choice');
    assert.deepEqual(JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body).delivery_options, importSettings.defaults, 'retries retain original options after defaults change');
    assert.equal(run('accounts[0].status'), 'success');
    assert.equal(run('accounts[0].historyID'), '9001', 'the persisted account ID is read from SSE result.data');
    assert.match(run('deliveryStatusText(accounts[0].delivery)'), /交付状态暂未确认/, 'a missing task cannot become endless loading or a false completion');
    const statusState = Object.fromEntries(['icon', 'email', 'message', 'delivery', 'stage', 'progressContainer', 'progressBar', 'progressLabel', 'timings', 'time', 'actions'].map(key => [key, new Element()]));
    statusState.progressBar.style = {};
    context.statusState = statusState;
    run('updateStatusRow(accounts[0], statusState)');
    assert.match(statusState.message.textContent, /登录成功/);
    assert.match(statusState.delivery.textContent, /交付状态暂未确认/);
    const retryAt = new Date(Date.now() + 60000).toISOString();
    deliveries = { 9001: { id: 91, account_id: 9001, state: 'retry_wait', retry_action: 'resume', next_retry_at: retryAt, last_error: '网络超时' },
        9002: { id: 92, account_id: 9002, state: 'completed' } };
    await run('loadHistory()');
    assert.equal(run('accounts[1].delivery.state'), 'completed', 'history refresh follows the server delivery terminal state');
    assert.match(run('deliveryStatusText(accounts[0].delivery)'), /下次自动继续交付/);
    assert.equal(el('historyAttention').hidden, true, 'temporary delivery errors are not manual alerts');
    assert.match(el('historyGrid').innerHTML, /Sub2 交付完成/);
    assert.doesNotMatch(run("automaticTaskPolicy({state:'completed',next_retry_at:'2026-10-08T00:00:00Z',retry_action:'resume'})"), /重试|下次/);
    deliveries[9001] = { id: 91, account_id: 9001, state: 'requires_action', requires_action: true, manual_action: '<img src=x onerror=alert(1)>请核对绑定', last_error: '身份冲突' };
    recoveries[9001] = { id: 91, account_id: 9001, purpose: 'delivery', state: 'unknown', requires_action: true, manual_action: '同一交付的内部任务，不应重复' };
    rows.push({ id: 9003, email: 'transient@example.test', status: 'error', last_error_code: 'timeout', last_error: '临时超时' },
        { id: 9004, email: 'mfa@example.test', status: 'error', last_error_code: 'invalid_mfa' });
    await run('loadHistory()');
    assert.equal(el('historyAttentionSummary').textContent, '需要处理 2 个账号');
    assert.equal((el('historyAttentionList').innerHTML.match(/data-attention-account=/g) || []).length, 2);
    assert.match(el('historyAttentionList').innerHTML, /&lt;img/);
    assert.doesNotMatch(el('historyAttentionList').innerHTML, /<img|同一交付的内部任务|transient@example/);
    assert.doesNotMatch(el('historyGrid').innerHTML, /同一交付的内部任务/);
    await run('loadHistory()');
    assert.equal((el('historyAttentionList').innerHTML.match(/data-attention-account=/g) || []).length, 2, 'polling does not accumulate duplicate notices');
    deliveries[9001] = { id: 91, account_id: 9001, state: 'completed' }; recoveries = {};
    rows[3].status = 'active'; rows[3].last_error_code = '';
    await run('loadHistory()');
    assert.equal(el('historyAttention').hidden, true, 'resolved tasks leave the persistent attention list');
    assert.equal(requests.filter(item => item.url === '/api/sub2/import').length, manualImportsBeforeDelivery, 'automatic delivery never relies on a browser import request');
    rechecks = { 9001: { state: 'pending', round: 2, next_check_at: retryAt }, 9002: { state: 'requires_action', requires_action: true, manual_action: '请更新密码' } };
    await run('loadHistory()');
    assert.match(el('historyGrid').innerHTML, /第 2 次延迟复检/);
    assert.match(el('historyAttentionSummary').textContent, /需要处理 1 个账号/);
    assert.match(el('historyAttentionList').innerHTML, /data-attention-edit="9002"/);
    assert.match(el('historyGrid').innerHTML, /data-history-action="credentials"/);
    const credentialsBefore = requests.filter(item => item.url.endsWith('/credentials')).length;
    run("openCredentialsEditor('9002')");
    assert.equal(el('credentialsPassword').value, '');
    assert.equal(el('credentialsTotp').value, '');
    assert.equal(el('credentialsProxy').value, '', 'stored secrets are never read into the editor');
    await el('credentialsForm').emit('submit');
    assert.equal(requests.filter(item => item.url.endsWith('/credentials')).length, credentialsBefore, 'blank inputs preserve all values without a no-op request');
    el('credentialsPassword').value = ' synthetic replacement ';
    el('credentialsTotp').value = 'discarded fixture';
    el('credentialsClearTotp').checked = true;
    el('credentialsClearProxy').checked = true;
    credentialsFailure = true;
    await el('credentialsForm').emit('submit');
    assert.equal(el('credentialsDialog').open, true);
    assert.match(el('credentialsStatus').textContent, /正在处理/);
    credentialsFailure = false;
    const loginsBeforeSave = requests.filter(item => item.url === '/api/login/stream').length;
    await el('credentialsForm').emit('submit');
    assert.deepEqual(JSON.parse(requests.filter(item => item.url.endsWith('/credentials')).at(-1).options.body), { password: ' synthetic replacement ', clear_totp: true, clear_proxy: true });
    assert.equal(el('credentialsDialog').open, false);
    assert.equal(el('credentialsPassword').value, '', 'secrets are cleared immediately after a successful save');
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, loginsBeforeSave, 'resume is persisted by the server without a second browser login');
    run("openCredentialsEditor('9002')");
    el('credentialsProxy').value = 'direct';
    await el('credentialsForm').emit('submit');
    assert.deepEqual(JSON.parse(requests.filter(item => item.url.endsWith('/credentials')).at(-1).options.body), { proxy: 'direct' }, 'an explicit direct route must be saved, not treated as clearing or a malformed URL');
    const repairTime = new Date().toISOString();
    repairs = { 9002: { state: 'queued', updated_at: repairTime } };
    await run('loadHistory()');
    assert.equal(el('historyAttention').hidden, true, 'an accepted correction supersedes stale manual alerts');
    assert.match(el('historyGrid').innerHTML, /资料已保存，等待后台继续/);
    repairs[9002] = { state: 'requires_action', requires_action: true, manual_action: '更新后的 TOTP 仍需核对', updated_at: repairTime };
    await run('loadHistory()');
    assert.match(el('historyAttentionList').innerHTML, /更新后的 TOTP 仍需核对/);
    assert.doesNotMatch(el('historyAttentionList').innerHTML, /请更新密码/);
    imports = [{ account_id: 9001, state: 'imported' }];
    deliveries[9001] = { account_id: 9001, state: 'verifying' };
    const pendingDeliveryImport = await run("waitForImportCompletion(['9001'], 50)");
    assert.equal(pendingDeliveryImport.success, 0, 'remote creation alone does not count as completed delivery');
    assert.equal(pendingDeliveryImport.pending, 1);
    deliveries[9001].state = 'completed';
    const finishedDeliveryImport = await run("waitForImportCompletion(['9001'], 50)");
    assert.equal(finishedDeliveryImport.success, 1);
    deliveries[9001].state = 'requires_action';
    const manualDeliveryImport = await run("waitForImportCompletion(['9001'], 50)");
    assert.equal(manualDeliveryImport.failed, 1, 'manual-action delivery settles the batch instead of waiting indefinitely');
    assert.equal(run("namePrefixError('🌙'.repeat(32))"), '', 'prefix limits count Unicode code points rather than UTF-16 units');
    assert.match(run("namePrefixError('🌙'.repeat(33))"), /32/);
    assert.match(run("namePrefixError('a\\u0000b')"), /控制字符/);
    assert.match(run("namePrefixError('a\\u0085b')"), /控制字符/);
    assert.equal(run("normalizeNamePrefix('\\u0085主池\\u0085')"), '主池', 'Unicode whitespace normalization matches the server');
    assert.equal(run("normalizeNamePrefix('\\ufeff主池')"), '\ufeff主池', 'non-whitespace format characters are not silently removed');
    assert.equal(run("copyDeliveryOptions({group_ids:[],priority:1,concurrency:3,name_prefix:'  '}).name_prefix"), undefined, 'empty prefix preserves the existing omitempty contract');
    // A slow initial defaults response must not overwrite what the user typed.
    run('deliveryBatchOptions = null; deliverySettings = null; accountNamePrefixEdited = false');
    settingsReadGate = gate(); const delayedSettings = settingsReadGate;
    const prefixDefaults = run('loadDeliverySettings()');
    el('accountNamePrefix').value = '  <img src=x>  '; await el('accountNamePrefix').emit('input');
    delayedSettings.release(); await prefixDefaults;
    assert.equal(el('accountNamePrefix').value, '  <img src=x>  ');
    assert.equal(run('deliveryBatchOptions.name_prefix'), '<img src=x>');
    assert.match(el('deliveryOptionsSummary').textContent, /前缀 <img src=x>/, 'prefix is rendered as text, not HTML');
    settingsReadGate = gate(); const delayedDialogSettings = settingsReadGate;
    const prefixDialog = run('chooseDeliveryOptions()');
    assert.equal(el('deliveryNamePrefix').disabled, true, 'the modal prefix cannot be edited before its defaults arrive');
    delayedDialogSettings.release(); await flush();
    assert.equal(el('deliveryNamePrefix').disabled, false, 'a successful read restores prefix editing');
    assert.equal(el('deliveryNamePrefix').value, '<img src=x>');
    await click('deliveryOptionsCancel'); await prefixDialog;
    // AUTH-only batches do not depend on Sub2 settings, but preserve their prefix for a later import.
    await change('autoDeliverToggle', false);
    run("deliveryBatchOptions = null; deliverySettings = null; accounts = [{id:0,email:'first@example.test',password:'synthetic-only',totp_secret:'fixture'}]; results=[]");
    rows = [{ id: 9001, email: 'first@example.test', status: 'active' }];
    deliveries = {}; rechecks = {}; repairs = {}; imports = [];
    el('accountNamePrefix').value = '离线批次'; await el('accountNamePrefix').emit('input');
    const settingsBeforeOffline = requests.filter(item => item.url === '/api/sub2/import-settings').length;
    settingsReadFailure = true;
    await run('runAccounts(accounts)');
    assert.equal(run('accounts[0].namePrefix'), '离线批次');
    assert.equal(requests.filter(item => item.url === '/api/sub2/import-settings').length, settingsBeforeOffline, 'login-only batch never waits for unavailable delivery settings');
    assert.equal(JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body).delivery_options, undefined);
    settingsReadFailure = false;
    el('accountNamePrefix').value = '另一批'; await el('accountNamePrefix').emit('input');
    confirmResult = null;
    const offlineImport = run('importLoginResultsToSub2()'); await flush();
    assert.equal(el('deliveryNamePrefix').value, '离线批次', 'manual import starts with the original login batch prefix');
    el('deliveryNamePrefix').value = 'x'.repeat(33);
    const importsBeforeInvalidPrefix = requests.filter(item => item.url === '/api/sub2/import').length;
    await el('deliveryOptionsForm').emit('submit');
    assert.equal(el('deliveryOptionsDialog').open, true);
    assert.match(el('deliveryOptionsStatus').textContent, /32/);
    assert.equal(requests.filter(item => item.url === '/api/sub2/import').length, importsBeforeInvalidPrefix);
    el('deliveryNamePrefix').value = '手动覆盖';
    await el('deliveryOptionsForm').emit('submit'); await offlineImport;
    assert.equal(JSON.parse(requests.filter(item => item.url === '/api/sub2/import').at(-1).options.body).delivery_options.name_prefix, '手动覆盖', 'manual confirmation explicitly overrides the frozen prefix');
    assert.equal(run('accounts[0].namePrefix'), '离线批次', 'manual import does not mutate the login retry snapshot');
    // The Start handler owns the batch before its asynchronous settings read.
    await change('autoDeliverToggle', true);
    run('deliverySettings = null; deliveryBatchOptions = null');
    el('accountsInput').value = 'first@example.test----synthetic-password----';
    const startRequestsBefore = requests.filter(item => item.url === '/api/login/stream').length;
    settingsReadFailure = true;
    await click('startBtn');
    assert.equal(el('startBtn').disabled, false, 'failed preparation releases Start');
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, startRequestsBefore);
    settingsReadFailure = false;
    settingsReadGate = gate(); const startSettings = settingsReadGate;
    loginStreamGate = gate(); const startedLogin = loginStreamGate;
    const firstStart = el('startBtn').emit('click');
    const duplicateStart = el('startBtn').emit('click');
    await flush();
    assert.equal(el('startBtn').disabled, true, 'Start is disabled while delivery settings load');
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, startRequestsBefore);
    startSettings.release(); await flush();
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, startRequestsBefore + 1, 'double Start submits one login');
    assert.equal(run('accounts[0].status'), 'processing', 'duplicate Start does not replace the running row');
    startedLogin.release(); await Promise.all([firstStart, duplicateStart]);
    assert.equal(run('accounts[0].status'), 'success', 'the running row retains its successful result');
    assert.equal(run('results.length'), 1);
    assert.equal(el('startBtn').disabled, false, 'completion releases Start');
    const canceledStartOptions = run('chooseDeliveryOptions()'); await flush();
    await click('deliveryOptionsCancel'); await canceledStartOptions;
    await click('startBtn');
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, startRequestsBefore + 2, 'canceling delivery options leaves Start usable');
    assert.equal(run('accounts[0].status'), 'success');
    // A stalled connection settles without replaying the POST; an established
    // SSE stream keeps the server's configured login deadline.
    const savedFetch = context.fetch, savedSetTimeout = context.setTimeout, savedClearTimeout = context.clearTimeout;
    const connectionTimers = new Map();
    let connectionTimerID = 0, connectionRequests = 0;
    context.setTimeout = (callback, delay) => {
        const id = ++connectionTimerID;
        connectionTimers.set(id, { callback, delay });
        return id;
    };
    context.clearTimeout = id => connectionTimers.delete(id);
    run('platformPauseUntil = 0');
    try {
        context.fetch = async (_url, options) => {
            connectionRequests++;
            return new Promise((_resolve, reject) => {
                options.signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })), { once: true });
            });
        };
        const stalled = run('loginAccount({email:"timeout@example.test"}, "direct")');
        assert.equal(connectionTimers.size, 1);
        const connectionTimer = [...connectionTimers.values()][0];
        assert.equal(connectionTimer.delay, 15000);
        connectionTimer.callback();
        await assert.rejects(stalled, /登录连接超时.*刷新历史/);
        assert.equal(connectionRequests, 1, 'timeout must not automatically replay an uncertain login POST');
        assert.equal(connectionTimers.size, 0, 'timeout cleanup releases its timer');

        let streamController, streamSignal;
        context.fetch = async (_url, options) => {
            connectionRequests++;
            streamSignal = options.signal;
            return { ok: true, headers: new Map([['content-type', 'text/event-stream']]),
                body: new ReadableStream({ start(controller) { streamController = controller; } }) };
        };
        const streaming = run('loginAccount({email:"stream@example.test"}, "direct")');
        await flush();
        assert.equal(connectionTimers.size, 0, 'receiving SSE headers clears the connection deadline while login is still pending');
        assert.equal(streamSignal.aborted, false);
        streamController.enqueue(new TextEncoder().encode('data: {"type":"result","success":true}\n\n'));
        streamController.close();
        assert.equal((await streaming).success, true);
        assert.equal(connectionRequests, 2);

        context.fetch = async () => {
            connectionRequests++;
            throw new TypeError('fixture connection reset');
        };
        await assert.rejects(run('loginAccount({email:"reset@example.test"}, "direct")'), /fixture connection reset/);
        assert.equal(connectionRequests, 3, 'network failure must not automatically replay a login POST');
        assert.equal(connectionTimers.size, 0, 'network failure releases the connection timer');
    } finally {
        context.fetch = savedFetch;
        context.setTimeout = savedSetTimeout;
        context.clearTimeout = savedClearTimeout;
    }
    console.log('History checks passed: Sub2 status/settings, ordinary/cross-page select, cancellation, delete locking/409/404/timeouts/refresh failures, partial/chunked import, relogin retention.');
    console.log('Tab checks passed: selection isolation, navigation, keyboard, focus, hash/back navigation, panel scrolling.');
    console.log('Confirmation checks passed: plain text, cancellation/acceptance for all batch actions, modal focus/Tab/Escape/restore, repeat opens, navigation cancellation, stale-state guards.');
    console.log('Recovery popover checks passed: pool membership, counts, loading/error/empty/truncated records, HTML escaping, hover/focus/click, polling preservation, Escape/outside close, stale responses.');
    console.log('Automatic delivery checks passed: configuration/preference, frozen requests/retries, persisted history progress, distinct login/delivery states, retry timing, deduplicated manual attention and resolution.');
    console.log('Delivery parameter and correction checks passed: shared form/default save/failure, unavailable groups, frozen batch settings/retries, empty-secret preservation/explicit clears, delayed recheck notices.');
    console.log('Account prefix checks passed: Unicode/control validation, late defaults, safe summary, default save, automatic batch/retry freezing, login-only independence and manual-import override.');
    console.log('Regression checks passed: double Start during settings load, preparation failure/cancel release, and active/waiting/completed credential repair polling.');
    console.log('Login connection checks passed: bounded setup, uncertain POST is not replayed, active SSE respects the server deadline, and timers are released.');
})().catch(error => { console.error(error); process.exitCode = 1; });
