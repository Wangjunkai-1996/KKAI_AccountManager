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
    querySelector() { return this.id === 'historyDetail' ? this : { focus() {} }; }
}
const indexHTML = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
assert.match(indexHTML, /<section[^>]*role="dialog"[^>]*aria-modal="true"[^>]*tabindex="-1"/,
    'history details dialog must be keyboard focusable and modal');
assert.match(indexHTML, /<dialog[^>]*id="actionConfirmDialog"[^>]*role="dialog"[^>]*aria-modal="true"[^>]*aria-labelledby="actionConfirmTitle"[^>]*aria-describedby="actionConfirmMessage"[^>]*tabindex="-1"/,
    'action confirmation must expose its title, message and modal semantics');
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
let rows = Array.from({ length: 25 }, (_, index) => ({ id: index + 1, email: `account${String(index + 1).padStart(3, '0')}@example.test`, status: ['active', 'error', 'running', 'deleted', 'interrupted'][index % 5] }));
let sub2Statuses = {
    1: { configured: true, imported: true, exists: true, status: 'active', schedulable: true, checked_at: '2026-10-05T10:00:00Z' },
    2: { configured: true, imported: true, exists: true, status: 'disabled', schedulable: false, checked_at: '2026-10-05T10:01:00Z' }
};
let recoverySettings = { auto_recovery_enabled: false };
let imports = [], historyFailure = false, historyGate = null, deleteGate = null;
let deletionFailures = new Map(), missingIDs = new Set(), acceptedImportIDs = null, importGate = null;
let confirmations = [], confirmResult = true, requests = [];
const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => structuredClone(body) });
const location = { hash: '' };
const window = new Element();
Object.assign(window, { alert: () => {},
    history: { state: null, pushState(_state, _title, hash) { location.hash = hash; } } });
const context = vm.createContext({ document, window, location, console, Event, CustomEvent, AbortController, AbortSignal, setTimeout, clearTimeout, CSS: { escape: String },
    fetch: async (url, options = {}) => {
        requests.push({ url, options });
        if (url === '/health') return response({ max_concurrent: 10 });
        if (url === '/api/history') {
            if (historyGate) { const gate = historyGate; historyGate = null; await gate.promise; }
            if (historyFailure) return response({ message: 'fixture refresh unavailable' }, 503);
            return response({ data: rows, imports, sub2_configured: true, imports_available: true, sub2_statuses: sub2Statuses });
        }
        if (url === '/api/account-recovery/settings' && !options.method) return response(recoverySettings);
        if (url === '/api/account-recovery/settings' && options.method === 'PUT') {
            recoverySettings = { auto_recovery_enabled: Boolean(JSON.parse(options.body).auto_recovery_enabled) };
            return response(recoverySettings);
        }
        if (url.startsWith('/api/history/') && !options.method) {
            const id = Number(url.split('/').pop());
            const account = rows.find(row => row.id === id);
            return response({ account, attempts: [] });
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
            const tasks = accepted.map(id => ({ account_id: id, state: 'queued' }));
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
    assert.equal(el('autoRecoveryStatus').textContent, '已开启');
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
    assert.equal(el('actionConfirmTitle').textContent, '导入 Sub2');
    confirmResult = null;
    const lockedImport = run('batchImportHistory()');
    run('historyLoading = true');
    await click('actionConfirmAccept'); await lockedImport;
    assert.equal(requests.filter(item => item.url === '/api/sub2/import').length, importsBeforeCancel, 'a refresh that starts during confirmation blocks stale submission');
    run('historyLoading = false');
    acceptedImportIDs = new Set([101]); importGate = gate(); const activeImport = importGate;
    const importOperation = run('batchImportHistory()'); await flush();
    await run('batchImportHistory()');
    assert.equal(requests.filter(item => item.url === '/api/sub2/import').length, importsBeforeCancel, 'duplicate pending import cannot submit');
    run("historyAccounts.find(account => account.id === '103').status = 'active'");
    await click('actionConfirmAccept');
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
    assert.match(confirmations.at(-1), /另有 1 个结果/);
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
    console.log('History checks passed: Sub2 status/settings, ordinary/cross-page select, cancellation, delete locking/409/404/timeouts/refresh failures, partial/chunked import, relogin retention.');
    console.log('Tab checks passed: selection isolation, navigation, keyboard, focus, hash/back navigation, panel scrolling.');
    console.log('Confirmation checks passed: plain text, cancellation/acceptance for all batch actions, modal focus/Tab/Escape/restore, repeat opens, navigation cancellation, stale-state guards.');
})().catch(error => { console.error(error); process.exitCode = 1; });
