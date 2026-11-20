// Offline interaction checks for the history selection and destructive actions.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const completionWatchdog = setTimeout(() => {
    console.error('client interaction checks did not complete within 10 seconds');
    process.exitCode = 1;
}, 10000);
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
assert.doesNotMatch(indexHTML, /id="(?:proxyInput|credentialsProxy|proxyCheckBtn)"/, 'proxy addresses are selected from Sub2 instead of typed into AUTH');
assert.match(indexHTML, /<section[^>]*role="dialog"[^>]*aria-modal="true"[^>]*tabindex="-1"/,
    'history details dialog must be keyboard focusable and modal');
assert.match(indexHTML, /<dialog[^>]*id="actionConfirmDialog"[^>]*role="dialog"[^>]*aria-modal="true"[^>]*aria-labelledby="actionConfirmTitle"[^>]*aria-describedby="actionConfirmMessage"[^>]*tabindex="-1"/,
    'action confirmation must expose its title, message and modal semantics');
assert.match(indexHTML, /<section[^>]*id="historyRecoveryPopover"[^>]*role="dialog"[^>]*aria-labelledby="historyRecoveryPopoverHeading"/,
    'recovery history popover must expose dialog semantics');
const ids = [...indexHTML.matchAll(/id="([^"]+)"/g)].map(match => match[1]);
const elements = new Map(ids.map(id => { const element = new Element(); element.id = id; return [id, element]; }));
const el = id => elements.get(id);
const tabs = ['login', 'history'].map(name => { const tab = el(`tab-${name}`); tab.dataset.tab = name; return tab; });
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
    1: { configured: true, imported: true, exists: true, status: 'active', schedulable: true, checked_at: '2026-10-05T10:00:00Z', quota: { five_hour: { used_percent: 42, reset_at: '2026-10-05T15:00:00Z' }, seven_day: { used_percent: 68, reset_after_seconds: 3600 }, credits: { balance: '12', has_credits: true, unlimited: false }, updated_at: '2026-10-05T10:00:00Z', source: 'Sub2 extra' } },
    2: { configured: true, imported: true, exists: true, status: 'disabled', schedulable: false, checked_at: '2026-10-05T10:01:00Z' }
};
let recoverySettings = { auto_recovery_enabled: false, interval_seconds: 60 };
let checks = {}, recoveries = {}, recoveryCheckFailure = false, settingsFailure = false;
let deliveries = {}, rechecks = {}, repairs = {}, sub2Configured = true, loginStreamGate = null, loginFailures = 0;
let importSettings = { defaults: { group_ids: [], priority: 1, concurrency: 3 }, groups: [{id: 7, name: '稳定池'}, {id: 8, name: '<img unsafe>测试池'}], groups_available: true };
let settingsReadFailure = false, settingsReadGate = null, importSettingsSaveFailure = false, credentialsFailure = false;
const intervals = [];
let imports = [], lifecycleEvents = {}, historyFailure = false, historyGate = null, deleteGate = null;
const costFixture = amount => ({ days: 90, total_cost: amount, total_standard_cost: amount * 2, total_requests: 18, total_tokens: 2400, checked_at: '2026-10-05T10:00:00Z' });
let costStats = { 1: costFixture(12.34), 2: costFixture(0), 4: costFixture(8.2), 21: costFixture(44), 1000: costFixture(56.78) };
let costsFailure = false, costsGate = null;
let deletionFailures = new Map(), missingIDs = new Set(), acceptedImportIDs = null, importGate = null;
let confirmations = [], confirmResult = true, requests = [];
let groupActions = [];
let detailGate = null, detailFailure = false, recoveryHistoryAvailable = true, recoveryHistoryTruncated = false;
let credentialsMeta = null, revealGate = null, revealReply = null;
const credentialCopies = [];
let recoveryHistory = [{ id: 3, state: 'unknown', created_at: '2026-10-07T06:08:41Z', updated_at: '2026-10-07T06:08:59Z', last_error: '<img src=x onerror=alert(1)>身份待核对' },
    { id: 2, state: 'completed', created_at: '2026-10-06T01:00:00Z', updated_at: '2026-10-06T01:01:00Z' }];
let recoveryFailures = [];
const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => structuredClone(body) });
const location = { hash: '' };
const window = new Element();
const browserPreferences = new Map();
Object.assign(window, { alert: () => {},
    localStorage: { getItem: key => browserPreferences.get(key) ?? null, setItem: (key, value) => browserPreferences.set(key, value) },
    history: { state: null, pushState(_state, _title, hash) { location.hash = hash; } } });
const context = vm.createContext({ document, window, location, console, Event, CustomEvent, AbortController, AbortSignal, TextDecoder, setTimeout, clearTimeout, clearInterval() {},
    navigator: { clipboard: { writeText: async value => { credentialCopies.push(value); } } },
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
        if (/^\/api\/history\/\d+\/credentials\/reveal$/.test(url) && options.method === 'POST') {
            const id = Number(url.split('/')[3]), { field } = JSON.parse(options.body);
            assert.ok(['password', 'totp_secret'].includes(field));
            assert.equal(options.headers['Content-Type'], 'application/json');
            assert.equal(options.headers['X-Requested-With'], 'XMLHttpRequest');
            assert.equal(options.cache, 'no-store');
            assert.ok(options.signal);
            const reply = revealReply || response({ success: true, account_id: id, field, value: field === 'password' ? 'fixture-revealed-password' : 'fixture-revealed-totp' });
            if (revealGate) { const pending = revealGate; revealGate = null; await pending.promise; }
            return reply;
        }
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
            return response({ data: rows, imports, lifecycle_events: lifecycleEvents, lifecycle_events_available: true, sub2_configured: sub2Configured, imports_available: true, sub2_statuses: sub2Statuses, checks, recoveries, deliveries, rechecks, repairs, checks_available: true });
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
        if (/^\/api\/account-recovery\/\d+\/groups$/.test(url) && options.method === 'POST') {
            groupActions.push({ url, body: JSON.parse(options.body) });
            return response({ success: true, queued: true });
        }
        if (/^\/api\/account-recovery\/\d+\/recheck$/.test(url) && options.method === 'POST') {
            const taskID = Number(url.split('/')[3]);
            const task = Object.values(rechecks).find(item => item.task_id === taskID);
            if (!task || task.state !== 'requires_action') return response({ success: false }, 409);
            task.state = 'pending'; task.requires_action = false;
            return response({ success: true, task_id: taskID }, 202);
        }
        if (url.startsWith('/api/history/usage?') && !options.method) {
            const ids = url.split('account_ids=')[1].split(',').map(decodeURIComponent);
            const stats = Object.fromEntries(ids.map(id => [id, structuredClone(costStats[id] || { days: 90, unknown: true })]));
            assert.ok(ids.length <= 100, 'usage reads are bounded to the current page');
            assert.ok(options.signal, 'usage reads have a timeout');
            if (costsGate) { const pending = costsGate; costsGate = null; await pending.promise; }
            return costsFailure ? response({ message: 'usage unavailable' }, 503) : response({ success: true, sub2_configured: sub2Configured, stats });
        }
        if (url.startsWith('/api/history/') && !options.method) {
            if (detailGate) { const gate = detailGate; detailGate = null; await gate.promise; }
            if (detailFailure) return response({ message: 'fixture detail unavailable' }, 503);
            const id = Number(url.split('/').pop());
            const account = rows.find(row => row.id === id);
            return response({ account, attempts: [], credentials_meta: credentialsMeta, recovery_history: recoveryHistory, recovery_history_available: recoveryHistoryAvailable, recovery_history_truncated: recoveryHistoryTruncated, recovery_failures: recoveryFailures, recovery_failures_available: true, lifecycle_events: lifecycleEvents[id] || [], lifecycle_events_available: true });
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
            const request = JSON.parse(options.body);
            const ids = request.account_ids;
            assert.ok(ids.length <= 100);
            const accepted = ids.filter(id => acceptedImportIDs === null || acceptedImportIDs.has(id));
            const tasks = accepted.map(id => ({ account_id: id, state: 'imported' }));
            if (request.reimport) accepted.forEach(id => { deliveries[id] = { state: 'completed' }; });
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
    assert.match(indexHTML, /id="tab-checks"[^>]*hidden/, 'account checks tab is hidden');
    assert.match(indexHTML, /id="tab-history"[^>]*>账号表/, 'account table is the primary account tab');
    assert.equal(el('historyActionBar').hidden, true);
    await click('tab-history');
    assert.equal(location.hash, '#panel-history');
    const costRequests = () => requests.filter(item => item.url.startsWith('/api/history/usage?'));
    assert.equal(costRequests().length, 1, 'initial cost read is independent from repeated history rendering');
    assert.equal(costRequests()[0].url.split('account_ids=')[1].split(',').length, 20, 'costs are queried only for the visible page, not all 25 accounts');
    const costRow = id => el('historyGrid').innerHTML.match(new RegExp(`<article[^>]*data-history-card="${id}"[^>]*>.*?<\\/article>`, 's'))?.[0] || '';
    assert.match(costRow(1), /近90天 \$12\.34/);
    assert.match(costRow(2), /近90天 \$0\.00/, 'a verified zero is displayed');
    assert.match(costRow(3), /近90天 —.*待核对/);
    assert.doesNotMatch(costRow(3), /\$0\.00/, 'unknown usage must not look like zero spending');
    assert.match(costRow(4), /近90天 \$8\.20/, 'officially deleted accounts still show their recorded usage');
    run("historyRecentCosts.get('2').stats.total_cost = 0.001");
    assert.match(run("renderHistoryCost(historyAccounts.find(account => account.id === '2'))"), /&lt;\$0\.01/, 'small positive costs do not round down to a verified zero');
    run("historyRecentCosts.get('2').stats.total_cost = 0");
    run('renderHistory(); renderHistory()'); await flush();
    assert.equal(costRequests().length, 1, 'cached rendering does not trigger a request loop');
    costsFailure = true;
    run("historyRecentCosts.get('1').expiresAt = 0");
    await run("loadHistoryCosts([historyAccounts.find(account => account.id === '1')])");
    assert.match(costRow(1), /近90天 \$12\.34.*上次数据/, 'a failed refresh retains the last verified amount');
    assert.equal(run("historyRecentCosts.get('1').stats.checked_at"), '2026-10-05T10:00:00Z', 'a stale amount retains its original timestamp');
    costsFailure = false;
    costStats[1] = { days: 90, unknown: true };
    run("historyRecentCosts.get('1').expiresAt = 0");
    await run("loadHistoryCosts([historyAccounts.find(account => account.id === '1')])");
    assert.match(costRow(1), /近90天 \$12\.34.*上次数据/, 'an unverified read preserves prior data until the binding changes');
    costStats[1] = costFixture(12.34);
    run("historyRecentCosts.get('1').expiresAt = 0");
    await run("loadHistoryCosts([historyAccounts.find(account => account.id === '1')])");
    const delayedPage = gate(); costsGate = delayedPage;
    run('historyPage = 2; renderHistory()'); await flush();
    assert.equal(costRequests().at(-1).url.split('account_ids=')[1].split(',').length, 5, 'the second page requests only its five rows');
    run('historyPage = 1; renderHistory()');
    delayedPage.release(); await flush();
    assert.doesNotMatch(el('historyGrid').innerHTML, /data-history-card="21"/, 'late usage responses do not change the selected page');
    assert.match(costRow(1), /近90天 \$12\.34/);
    const delayedBinding = gate(); costsGate = delayedBinding;
    run("historyAccounts.find(account => account.id === '1').sub2Status.sub2AccountID = '601'; renderHistory()");
    await flush();
    costStats[1] = costFixture(99);
    run("historyAccounts.find(account => account.id === '1').sub2Status.sub2AccountID = '602'; renderHistory()");
    await flush();
    assert.match(costRow(1), /近90天 \$99\.00/, 'a changed binding invalidates cached costs');
    delayedBinding.release(); await flush();
    assert.match(costRow(1), /近90天 \$99\.00/, 'a late response for the prior binding cannot overwrite the new amount');
    costStats[1] = costFixture(12.34);
    run("historyAccounts.find(account => account.id === '1').sub2Status.sub2AccountID = ''; renderHistory()"); await flush();
    // Pool membership is independent from credential health, and unknown reads stay unknown.
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status({imported: true, exists: true, status: 'error'}))).kind"), 'present');
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status(null))).kind"), 'unknown');
    assert.equal(run("historyPoolState(normalizeHistoryAccount({id: 1}, normalizeSub2Status({exists: false}))).kind"), 'unknown', 'exists=false alone is not proof of a missing association');
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
    assert.doesNotMatch(el('historyGrid').innerHTML, /data-history-action="check"/, 'table rows do not link to the hidden checks page');
    const firstAccountRow = el('historyGrid').innerHTML.match(/<article[^>]*data-history-card="1"[^>]*>.*?<\/article>/s)?.[0];
    assert.ok(firstAccountRow, 'first account appears as a table row');
    for (const column of ['identity', 'auth', 'sub2', 'source', 'quota', 'workflow', 'time', 'actions']) {
        assert.match(firstAccountRow, new RegExp(`history-row-${column}`), `${column} has its own scannable column`);
    }
    assert.match(firstAccountRow, /Sub2 正常/);
    assert.match(firstAccountRow, /可调度/);
    assert.match(firstAccountRow, /5h 42%/);
    assert.match(firstAccountRow, /7d 68%/);
    assert.match(firstAccountRow, /未发现删除/);
    const officialDeletedRow = el('historyGrid').innerHTML.match(/<article[^>]*data-history-card="4"[^>]*>.*?<\/article>/s)?.[0];
    assert.match(officialDeletedRow || '', /官方删除/, 'AUTH deletion is shown as an official deletion source');
    assert.doesNotMatch(el('historyGrid').innerHTML, /成功复活 0 次/, 'accounts without recovery records do not show empty history buttons');
    assert.match(el('accountTableSummary').textContent, /全部 25 · 可调度 1 · 需处理 \d+ · 处理中 \d+/);
    await change('historyStatusFilter', 'schedulable');
    assert.match(el('historyGrid').innerHTML, /data-history-card="1"/);
    assert.doesNotMatch(el('historyGrid').innerHTML, /data-history-card="2"/, 'unschedulable accounts are excluded');
    await change('historyStatusFilter', 'attention');
    assert.match(el('historyGrid').innerHTML, /data-history-card="2"/, 'failed accounts remain visible in attention');
    await change('historyStatusFilter', 'working');
    assert.match(el('historyGrid').innerHTML, /data-history-card="3"/, 'active work remains visible');
    deliveries[1] = { state: 'queued' };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /data-history-card="1"/, 'queued delivery appears in working');
    await change('historyStatusFilter', 'schedulable');
    assert.match(el('historyGrid').innerHTML, /data-history-card="1"/, 'ongoing delivery does not hide a schedulable Sub2 account');
    deliveries = {};
    await click('refreshHistoryBtn');
    await change('historyStatusFilter', 'all');
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
    rechecks[2] = { task_id: 992, state: 'requires_action', requires_action: true, manual_action: '管理鉴权已修正' };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /data-history-action="recheck" data-history-id="2" data-recheck-task="992"[^>]*>重新复检/);
    const recheckPosts = () => requests.filter(item => /\/recheck$/.test(item.url));
    await run("retryHistoryRecheck('2', '991')");
    assert.equal(recheckPosts().length, 0, 'stale task buttons cannot retry a replacement task');
    const recheckClick = new Element();
    recheckClick.dataset = { historyAction: 'recheck', historyId: '2', recheckTask: '992' };
    recheckClick.closest = selector => selector === '[data-history-action]' ? recheckClick : null;
    await el('historyGrid').emit('click', recheckClick);
    await flush();
    assert.equal(recheckPosts().length, 1);
    assert.equal(recheckPosts()[0].url, '/api/account-recovery/992/recheck');
    assert.equal(recheckPosts()[0].options.method, 'POST');
    assert.equal(run('historyRefreshDelay()'), 5000, 'accepted follow-up stays visible while queued');
    assert.doesNotMatch(el('historyGrid').innerHTML, /data-recheck-task="992"/);
    await run("retryHistoryRecheck('2', '992')");
    assert.equal(recheckPosts().length, 1, 'pending rechecks cannot be submitted twice');
    rechecks[2].state = 'canceled'; rechecks[2].requires_action = true;
    await click('refreshHistoryBtn');
    await run("retryHistoryRecheck('2', '992')");
    assert.equal(recheckPosts().length, 1, 'canceled rechecks remain stopped');
    rechecks = {};
    await click('refreshHistoryBtn');
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
    assert.deepEqual(JSON.parse(requests.find(item => item.url === '/api/account-recovery/check').options.body), { account_id: 2, manual_attempt: true, expected_task_id: 0, expected_retry_count: 0 });
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
    assert.doesNotMatch(el('historyGrid').innerHTML, /data-history-action="recover" data-history-id="2"/, 'active recovery keeps progress visible without an unusable action button');
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
    rows = [{ id: 2, email: 'account002@example.test', status: 'active' }];
    imports = [{ account_id: 2, sub2_account_id: 200, state: 'imported', operation_id: 'linked-200' }];
    sub2Statuses[2] = { ...sub2Statuses[2], rate_limit_reset_at: '', effective_schedulable: true };
    await click('refreshHistoryBtn');
    assert.match(indexHTML, /id="historyCheckNowBtn"[^>]*hidden/, 'standalone manual checks are hidden from the account table');
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
    assert.deepEqual(JSON.parse(requests.find(item => item.url === '/api/account-recovery' && item.options.method === 'POST').options.body), { account_id: 2, manual_attempt: true, expected_task_id: 0, expected_retry_count: 0 });
    recoveries[2] = { state: 'failed' };
    checks[2] = { latest_task: { state: 'skipped', skip_reason: 'cooldown' }, last_result: { outcome: 'ok', freshness: 'stale' } };
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /检测已跳过：检测冷却中/);
    assert.match(el('historyGrid').innerHTML, /上次凭据调用正常 · 旧凭据结果/);
    assert.doesNotMatch(el('historyGrid').innerHTML, /当前凭据调用正常/);
    sub2Statuses[2].exists = false;
    await click('refreshHistoryBtn');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).reason"), 'Sub2 状态待核对');
    sub2Statuses[2].association_status = 'missing';
    await click('refreshHistoryBtn');
    assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).reason"), 'Sub2 关联账号不存在 · 已停止自动检测和恢复');
    const sub2DeletedRow = el('historyGrid').innerHTML.match(/<article[^>]*data-history-card="2"[^>]*>.*?<\/article>/s)?.[0];
    assert.match(sub2DeletedRow || '', /Sub2 关联账号不存在/, 'Sub2 absence is shown separately from official status');
    for (const [association, label] of [['unmatched', '未找到 Sub2 关联'], ['ambiguous', 'Sub2 多条匹配待确认'], ['conflict', 'Sub2 身份不一致']]) {
        sub2Statuses[2] = { imported: false, exists: false, unknown: true, association_status: association, error: '<reason>' };
        await click('refreshHistoryBtn');
        assert.match(el('historyGrid').innerHTML, new RegExp(label));
        assert.equal(run("historyRecoveryAction(historyAccounts.find(item => item.id === '2')).disabled"), true);
    }
    sub2Statuses[2] = null;
    const deletesBeforeUnknown = deletes().length;
    await click('refreshHistoryBtn');
    assert.match(el('historyGrid').innerHTML, /Sub2 状态待核对/, 'missing Sub2 status must not imply confirmed absence');
    assert.equal(run("historyAccountMatches(historyAccounts.find(account => account.id === '2'), 'unknown')"), true);
    assert.equal(deletes().length, deletesBeforeUnknown, 'an unknown Sub2 result never deletes an account');
    const unknownAccountRow = el('historyGrid').innerHTML.match(/<article[^>]*data-history-card="2"[^>]*>.*?<\/article>/s)?.[0];
    assert.ok(unknownAccountRow, 'unknown accounts remain visible');
    assert.doesNotMatch(unknownAccountRow, /清理记录/, 'unknown status is not represented as a deleted account');
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
    for (const state of ['queued', 'checking', 'recovery_queued']) {
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
    recoveries[1] = { state: 'unknown', requires_action: true, updated_at: '2026-10-08T01:00:00Z' };
    repairs = { 1: { state: 'completed', updated_at: '2026-10-08T02:00:00Z' } };
    await run('loadHistory()');
    assert.equal(run("historyAccountMatches(historyAccounts.find(account => account.id === '1'), 'attention')"), false,
        'a newer completed credential repair supersedes old recovery attention');
    const repairedAccountRow = el('historyGrid').innerHTML.match(/<article[^>]*data-history-card="1"[^>]*>.*?<\/article>/s)?.[0];
    assert.ok(repairedAccountRow);
    assert.doesNotMatch(repairedAccountRow, /history-workflow-state">需处理/, 'repaired account workflow no longer requests attention');
    repairs = {};
    for (const state of ['failed', 'unknown']) {
        recoveries[1] = { state, next_retry_at: new Date(Date.now() + 600000).toISOString() };
        await run('loadHistory()');
        assert.equal(run("historyAccountMatches(historyAccounts.find(account => account.id === '1'), 'working')"), true,
            `${state} recovery waiting for an automatic retry remains in working`);
    }
    recoveries = {};
    rechecks[1] = { state: 'checking' };
    await run('loadHistory()');
    assert.equal(run("historyAccountMatches(historyAccounts.find(account => account.id === '1'), 'working')"), true,
        'an active delayed recheck appears in working');
    rechecks = {};
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
    assert.match(confirmations.at(-1), /16 个账号/);
    assert.match(confirmations.at(-1), /15 个不在当前页/);
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
    el('deliveryMerchant').value = '测试号商';
    el('deliveryDeadline').value = '2026-12-31';
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
    const originalHistoryRelogin = run('performHistoryRelogin');
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
    rows = [{ id: 1000, email: 'details@example.test', status: 'active', password: 'fixture-secret', totp_secret: 'fixture-totp' }];
    sub2Statuses[1000] = { configured: true, imported: true, exists: true, status: 'active', schedulable: true, effective_schedulable: true, checked_at: '2026-10-05T10:00:00Z', quota: {
        five_hour: { used_percent: 42, resets_at: '2026-10-05T14:00:00Z', used_requests: 42, limit_requests: 100 },
        seven_day: { used_percent: 68 }, seven_day_sonnet: { used_percent: 21 }, seven_day_fable: { used_percent: 8 },
        thirty_day: { used_percent: 12 }, credits: { balance: '12', has_credits: true }, updated_at: '2026-10-05T10:01:00Z', source: 'cached'
    } };
    lifecycleEvents = { 1000: [{ id: 12, account_id: 1000, email: 'details@example.test', source: 'official', status: 'deactivated', reason: '官方账号已停用', code: 'account_deactivated', http_status: 403, observed_at: '2026-10-05T10:02:00Z' }] };
    imports = []; await click('refreshHistoryBtn');
    await change('historySearchInput', 'DETAILS@');
    assert.match(el('historyGrid').innerHTML, /data-history-card="1000"/, 'email searches are case insensitive');
    await change('historySearchInput', '1000');
    assert.match(el('historyGrid').innerHTML, /data-history-card="1000"/, 'account IDs are searchable independently of the email');
    await change('historySearchInput', 'not-a-matching-account');
    assert.doesNotMatch(el('historyGrid').innerHTML, /data-history-card="1000"/);
    await change('historySearchInput', '');
    recoveryFailures = [{ id: 7, task_id: 224, failure_stage: 'enabling_schedule', error_code: 'probe_failed', http_status: 503, retry_count: 2, created_at: '2026-10-08T01:02:03Z' }];
    // History details keep keyboard focus inside the dialog and return it to the opener.
    run("historySelectedIDs.add('1000'); renderHistory()");
    el('historyBatchDeleteBtn').disabled = false;
    el('historyBatchDeleteBtn').dataset.historyId = '1000';
    const detailCosts = gate(); costsGate = detailCosts;
    run("historyRecentCosts.delete('1000')");
    const openingCostDetail = run("openHistoryDetails('1000', historyBatchDeleteBtn)");
    await flush();
    assert.match(el('historyDetailBody').innerHTML, /正在加载详情/, 'details wait for the pending usage read');
    detailCosts.release();
    await openingCostDetail;
    assert.equal(el('historyDetail').hidden, false);
    assert.match(el('historyDetailBody').innerHTML, /Sub2 检测失败证据/);
    assert.match(el('historyDetailBody').innerHTML, /任务 #224/);
    assert.match(el('historyDetailBody').innerHTML, /HTTP 503/);
    assert.match(el('historyDetailBody').innerHTML, /Sub2 状态 \/ 调度/);
    assert.match(el('historyDetailBody').innerHTML, /状态来源/);
    assert.match(el('historyDetailBody').innerHTML, /Sub2 最近同步/);
    assert.match(el('historyDetailBody').innerHTML, /近90天账号费用/);
    assert.match(el('historyDetailBody').innerHTML, /\$56\.78/);
    assert.match(el('historyDetailBody').innerHTML, /18 次请求 · 2,400 Token/);
    assert.match(el('historyDetailBody').innerHTML, /Sub2 账号统计费用（USD）/);
    run("historyRecentCosts.get('1000').stats.checked_at = '<img src=x onerror=alert(1)>'");
    const escapedCost = run("renderHistoryCost(historyAccounts.find(account => account.id === '1000'), true)");
    assert.doesNotMatch(escapedCost, /<img/, 'usage metadata is escaped');
    assert.match(escapedCost, /&lt;img/);
    run("historyRecentCosts.get('1000').stats.checked_at = '2026-10-05T10:00:00Z'");
    assert.match(el('historyDetailBody').innerHTML, /7 天 Sonnet/);
    assert.match(el('historyDetailBody').innerHTML, /7 天 Fable/);
    assert.match(el('historyDetailBody').innerHTML, /30 天使用额度/);
    assert.match(el('historyDetailBody').innerHTML, /请求 42\/100/);
    assert.match(el('historyDetailBody').innerHTML, /Sub2 正常/);
    assert.match(el('historyDetailBody').innerHTML, /可调度/);
    assert.match(el('historyDetailBody').innerHTML, /账号状态历史/);
    assert.match(el('historyDetailBody').innerHTML, /details@example.test/);
    assert.match(el('historyDetailBody').innerHTML, /官方检测/);
    assert.match(el('historyDetailBody').innerHTML, /已停用/);
    assert.match(el('historyDetailBody').innerHTML, /HTTP 403/);
    assert.doesNotMatch(el('historyDetailBody').innerHTML, /fixture-secret|fixture-totp/, 'account details never reveal saved credentials');
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
    // Navigation uses the two visible tabs and redirects old checks links to the table.
    run("historyDetailID = '123'; historyDetail.hidden = false");
    el('historySearchInput').focus();
    window.AuthTabs.select('login');
    assert.equal(el('historyDetail').hidden, true, 'leaving history closes stale details');
    assert.equal(run('historyDetailID'), '');
    assert.equal(document.activeElement, el('tab-login'), 'programmatic switch must move focus out of hidden panel');
    assert.equal(location.hash, '#panel-login');
    const transitions = historyEvents.filter(name => name === 'auth-tab-changed').length;
    window.AuthTabs.select('login');
    assert.equal(historyEvents.filter(name => name === 'auth-tab-changed').length, transitions, 'same-tab selection must not trigger recovery again');
    window.AuthTabs.select('checks');
    assert.equal(el('panel-history').hidden, false, 'old checks actions open the account table');
    assert.equal(el('panel-checks').hidden, true);
    assert.equal(location.hash, '#panel-history');
    let prevented = false;
    await el('tab-login').emit('keydown', el('tab-login'), { key: 'ArrowRight', preventDefault() { prevented = true; } });
    assert.equal(prevented, true); assert.equal(document.activeElement, el('tab-history'));
    await el('tab-login').emit('keydown', el('tab-login'), { key: 'End', preventDefault() {} });
    assert.equal(document.activeElement, el('tab-history'));
    location.hash = '#panel-history'; await window.emit('popstate');
    assert.equal(el('panel-history').hidden, false);
    location.hash = '#accountChecksPanel'; await window.emit('hashchange');
    assert.equal(el('panel-history').hidden, false);
    assert.equal(el('panel-checks').hidden, true);
    location.hash = '#panel-checks'; await window.emit('hashchange');
    assert.equal(el('panel-history').hidden, false);
    assert.deepEqual(tabs.map(tab => tab.tabIndex), [-1, 0]);
    assert.deepEqual(tabs.map(tab => tab.attributes['aria-selected']), ['false', 'true']);
    // One parameter form serves login batches and manual imports; saving defaults is explicit.
    confirmResult = null;
    let selectedGroups = [7];
    el('deliveryGroups').querySelectorAll = () => selectedGroups.map(id => ({ dataset: { deliveryGroup: String(id) } }));
    const parameters = run('chooseDeliveryOptions()'); await flush();
    assert.match(el('deliveryGroups').innerHTML, /&lt;img unsafe&gt;/);
    assert.doesNotMatch(el('deliveryGroups').innerHTML, /<img/);
    el('deliveryPriority').value = '4'; el('deliveryConcurrency').value = '12'; el('deliverySaveDefault').checked = true;
    el('deliveryMerchant').value = '  主池🌙  ';
    el('deliveryDeadline').value = '  2026-12-31  ';
    importSettingsSaveFailure = true;
    await el('deliveryOptionsForm').emit('submit');
    assert.equal(el('deliveryOptionsDialog').open, true, 'failed default save retains the editable form');
    assert.match(el('deliveryOptionsStatus').textContent, /保存失败/);
    assert.equal(run('deliveryBatchOptions.priority'), 1, 'failed save does not silently apply the requested defaults');
    importSettingsSaveFailure = false;
    await el('deliveryOptionsForm').emit('submit'); await parameters;
    assert.deepEqual(importSettings.defaults, { group_ids: [7], priority: 4, concurrency: 12, merchant: '主池🌙', deadline: '2026-12-31' });
    assert.match(el('deliveryOptionsSummary').textContent, /稳定池.*优先级 4.*账号并发 12/);
    const unavailable = run('chooseDeliveryOptions()'); settingsReadFailure = true; await flush();
    // The already-running read may have completed before this flag; a fresh open exercises failure.
    await click('deliveryOptionsCancel'); await unavailable;
    const unavailableRead = run('chooseDeliveryOptions()'); await flush();
    assert.equal(el('deliveryOptionsApply').disabled, true);
    assert.equal(el('deliveryMerchant').disabled, true, 'a failed settings read keeps merchant fields disabled until a successful reload');
    assert.match(el('deliveryOptionsStatus').textContent, /读取设置失败/);
    await click('deliveryOptionsCancel'); await unavailableRead; settingsReadFailure = false;
    importSettings.groups_available = false;
    const unavailableGroups = run('chooseDeliveryOptions()'); await flush();
    assert.equal(el('deliveryOptionsApply').disabled, true, 'unavailable group catalog cannot submit a grouped batch');
    await click('deliveryOptionsCancel'); await unavailableGroups; importSettings.groups_available = true;
    await run('loadDeliverySettings()');
    const savedChooserBatch = run('JSON.stringify(deliveryBatchOptions)');
    run('deliveryBatchOptions = null');
    importSettings.groups_available = false;
    const isolatedOptions = { group_ids: [], priority: 6, concurrency: 2, merchant: '独立账号', deadline: '2026-12-31' };
    const isolatedChooser = run(`chooseDeliveryOptions('重新导入参数', true, ${JSON.stringify(isolatedOptions)}, false)`); await flush();
    assert.equal(el('deliveryOptionsApply').disabled, false, 'empty historical groups may submit even when the group catalog is unavailable');
    assert.equal(el('deliverySaveDefault').disabled, true, 'account reimport cannot change platform defaults');
    const defaultWrites = () => requests.filter(item => item.url === '/api/sub2/import-settings' && item.options.method === 'PUT').length;
    const defaultsBeforeIsolated = defaultWrites();
    el('deliverySaveDefault').checked = true;
    await el('deliveryOptionsForm').emit('submit');
    assert.deepEqual(JSON.parse(JSON.stringify(await isolatedChooser)), isolatedOptions, 'the submit fallback uses the dialog snapshot without a global batch');
    assert.equal(run('deliveryBatchOptions'), null, 'isolated options neither initialize nor replace the global batch');
    assert.equal(defaultWrites(), defaultsBeforeIsolated, 'even a stale checked value cannot save defaults in isolated mode');
    const unavailableHistoricalGroups = run(`chooseDeliveryOptions('重新导入参数', true, ${JSON.stringify({ ...isolatedOptions, group_ids: [8] })}, false)`); await flush();
    assert.equal(el('deliveryOptionsApply').disabled, true, 'unavailable historical groups cannot silently become ungrouped');
    await el('deliveryOptionsCancel').emit('click'); await unavailableHistoricalGroups;
    importSettings.groups_available = true;
    run(`deliveryBatchOptions = ${JSON.stringify({ ...isolatedOptions, priority: 99 })}`);
    const defaultsChooser = run("chooseDeliveryOptions('无历史参数', true, null, false)"); await flush();
    assert.equal(el('deliveryPriority').value, String(importSettings.defaults.priority), 'reimport with no account options uses platform defaults, not another login batch');
    await el('deliveryOptionsCancel').emit('click'); await defaultsChooser;
    run(`deliveryBatchOptions = ${savedChooserBatch}`);
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
    const batch = run('runAccounts(accounts)'); await flush();
    assert.equal(el('autoDeliverToggle').disabled, true);
    await change('autoDeliverToggle', false); // Simulate a preference change while the first request is in flight.
    run('deliveryBatchOptions = {group_ids:[8],priority:20,concurrency:25}');
    firstLogin.release(); await batch;
    assert.deepEqual(requests.filter(item => item.url === '/api/login/stream').slice(loginRequestsBefore).map(item => JSON.parse(item.options.body).auto_deliver), [true, true], 'every account uses its frozen batch choice');
    assert.deepEqual(requests.filter(item => item.url === '/api/login/stream').slice(loginRequestsBefore).map(item => JSON.parse(item.options.body).delivery_options), [importSettings.defaults, importSettings.defaults], 'all accounts keep the original batch groups and scheduling parameters');
    assert.deepEqual(requests.filter(item => item.url === '/api/login/stream').slice(loginRequestsBefore).map(item => JSON.parse(item.options.body).proxy), ['', ''], 'login batches leave route selection to Sub2');
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
    assert.match(run('deliveryStatusText(accounts[0].delivery)'), /网络超时/, 'retrying deliveries explain the safe server-reported reason');
    assert.ok(run('historyRefreshDelay()') > 5000 && run('historyRefreshDelay()') <= 60000, 'future retries keep the ordinary interval or wake when due');
    deliveries[9001].next_retry_at = new Date(Date.now() - 1000).toISOString();
    deliveries[9001].last_error = '<img src=x onerror=alert(1)>待核对';
    await run('loadHistory()');
    assert.equal(run('historyRefreshDelay()'), 5000, 'due retries refresh promptly with automatic recovery disabled');
    assert.match(run('deliveryStatusText(accounts[0].delivery)'), /已到重试时间，等待后台执行/);
    assert.doesNotMatch(run('deliveryStatusText(accounts[0].delivery)'), /下次自动/);
    assert.match(el('historyGrid').innerHTML, /&lt;img src=x onerror=alert\(1\)&gt;待核对/, 'retry explanations remain escaped in history markup');
    assert.doesNotMatch(el('historyGrid').innerHTML, /<img/);
    assert.equal(el('historyAttention').hidden, true, 'temporary delivery errors are not manual alerts');
    assert.match(el('historyGrid').innerHTML, /Sub2 交付完成/);
    assert.match(run("deliveryStatusText({state:'queued',blocked_reason:'后台服务已暂停，需要恢复服务后继续'})"), /自动交付已暂停/);
    assert.doesNotMatch(run("deliveryStatusText({state:'queued',blocked_reason:'后台服务已暂停，需要恢复服务后继续'})"), /等待自动交付/);
    assert.equal(run("deliveryStatusText({state:'completed',blocked_reason:'后台服务已暂停'})"), 'Sub2 交付完成');
    assert.doesNotMatch(run("automaticTaskPolicy({state:'completed',next_retry_at:'2026-10-08T00:00:00Z',retry_action:'resume'})"), /重试|下次/);
    // A batch finishing while an older history snapshot is in flight must
    // still fetch the delivery state that exists after the batch finished.
    const normalFetchBeforeDeliveryRace = context.fetch;
    const staleDeliverySnapshot = gate();
    const freshDeliverySnapshot = gate();
    let heldDeliverySnapshot = false;
    let deliverySnapshotRequests = 0;
    context.fetch = async (url, options) => {
        const reply = await normalFetchBeforeDeliveryRace(url, options);
        if (url === '/api/history') {
            deliverySnapshotRequests++;
            const snapshot = await reply.json();
            if (!heldDeliverySnapshot) {
                heldDeliverySnapshot = true;
                await staleDeliverySnapshot.promise;
            } else {
                await freshDeliverySnapshot.promise;
            }
            return response(snapshot);
        }
        return reply;
    };
    const oldDeliveryRefresh = run('loadHistory()');
    await flush();
    deliveries[9001] = { id: 91, account_id: 9001, state: 'completed' };
    let batchFinishedRefreshSettled = false;
    const batchFinishedRefresh = run('loadHistory()').then(() => { batchFinishedRefreshSettled = true; });
    const concurrentDeliveryRefreshes = Array.from({ length: 5 }, () => run('loadHistory()'));
    staleDeliverySnapshot.release();
    await flush();
    assert.equal(deliverySnapshotRequests, 2, 'overlapping refresh callers coalesce into one follow-up request');
    assert.equal(batchFinishedRefreshSettled, false, 'batch completion waits for the fresh snapshot instead of only the old request');
    freshDeliverySnapshot.release();
    await Promise.all([oldDeliveryRefresh, batchFinishedRefresh, ...concurrentDeliveryRefreshes]);
    context.fetch = normalFetchBeforeDeliveryRace;
    assert.equal(run('accounts[0].delivery.state'), 'completed', 'batch completion refresh must not reuse an older waiting delivery snapshot');
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
    await el('credentialsForm').emit('submit');
    assert.equal(requests.filter(item => item.url.endsWith('/credentials')).length, credentialsBefore, 'blank inputs preserve all values without a no-op request');
    el('credentialsPassword').value = ' synthetic replacement ';
    el('credentialsTotp').value = 'discarded fixture';
    el('credentialsClearTotp').checked = true;
    credentialsFailure = true;
    await el('credentialsForm').emit('submit');
    assert.equal(el('credentialsDialog').open, true);
    assert.match(el('credentialsStatus').textContent, /正在处理/);
    credentialsFailure = false;
    const loginsBeforeSave = requests.filter(item => item.url === '/api/login/stream').length;
    await el('credentialsForm').emit('submit');
    assert.deepEqual(JSON.parse(requests.filter(item => item.url.endsWith('/credentials')).at(-1).options.body), { password: ' synthetic replacement ', clear_totp: true });
    assert.equal(el('credentialsDialog').open, false);
    assert.equal(el('credentialsPassword').value, '', 'secrets are cleared immediately after a successful save');
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, loginsBeforeSave, 'resume is persisted by the server without a second browser login');
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
    assert.equal(run("deliveryValueError('🌙'.repeat(32), '号商', 32)"), '', 'merchant limits count Unicode code points rather than UTF-16 units');
    assert.match(run("deliveryValueError('🌙'.repeat(33), '号商', 32)"), /32/);
    assert.match(run("deliveryValueError('a\\u0000b', '号商', 32)"), /控制字符/);
    assert.match(run("deliveryValueError('a\\u0085b', '号商', 32)"), /控制字符/);
    assert.equal(run("normalizeDeliveryValue('\\u0085主池\\u0085')"), '主池', 'Unicode whitespace normalization matches delivery fields');
    assert.equal(run("normalizeDeliveryValue('\\ufeff主池')"), '\ufeff主池', 'non-whitespace format characters are not silently removed');
    assert.equal(run("copyDeliveryOptions({group_ids:[],priority:1,concurrency:3,merchant:'  '}).merchant"), undefined, 'empty merchant preserves the existing omitempty contract');
    // A slow initial defaults response must not create a stale identity snapshot.
    run('deliveryBatchOptions = null; deliverySettings = null');
    settingsReadGate = gate(); const delayedSettings = settingsReadGate;
    const deliveryDefaults = run('loadDeliverySettings()');
    delayedSettings.release(); await deliveryDefaults;
    assert.equal(run('deliveryBatchOptions.merchant'), '主池🌙');
    assert.match(el('deliveryOptionsSummary').textContent, /主池🌙/);
    settingsReadGate = gate(); const delayedDialogSettings = settingsReadGate;
    const identityDialog = run('chooseDeliveryOptions()');
    assert.equal(el('deliveryMerchant').disabled, true, 'the modal merchant is disabled before defaults arrive');
    delayedDialogSettings.release(); await flush();
    assert.equal(el('deliveryMerchant').disabled, false, 'a successful read restores merchant editing');
    assert.equal(el('deliveryMerchant').value, '主池🌙');
    await click('deliveryOptionsCancel'); await identityDialog;
    // Route selection keeps click order, and login-only requests use its first route.
    importSettings.egress_available = true;
    importSettings.egress_routes = [
        { id: 104, kind: 'proxy', name: 'RN-104', public_ip: '104.x.x.1', eligible: true },
        { id: 67, kind: 'proxy', name: '<img unsafe>RN-67', public_ip: '67.x.x.1', eligible: true },
        { id: 1, kind: 'direct', name: '本机直连', eligible: false }
    ];
    const routeDialog = run('chooseDeliveryOptions()'); await flush();
    assert.match(el('deliveryEgressRoutes').innerHTML, /&lt;img unsafe&gt;RN-67/);
    assert.doesNotMatch(el('deliveryEgressRoutes').innerHTML, /<img unsafe>/);
    assert.match(el('deliveryEgressRoutes').innerHTML, /data-delivery-route="1"[^>]*disabled/);
    await el('deliveryEgressRoutes').emit('change', { dataset: { deliveryRoute: '67' }, checked: true });
    await el('deliveryEgressRoutes').emit('change', { dataset: { deliveryRoute: '104' }, checked: true });
    assert.deepEqual(Array.from(run('selectedEgressRouteIDs')), [67, 104]);
    assert.match(el('deliveryEgressHint').textContent, /RN-67.*登录/);
    el('deliveryMerchant').value = '手动号商'; el('deliveryDeadline').value = '2026-12-31';
    await el('deliveryOptionsForm').emit('submit'); await routeDialog;
    assert.deepEqual(Array.from(run('deliveryBatchOptions.egress_route_ids')), [67, 104]);
    await change('autoDeliverToggle', false);
    run("accounts = [{id:0,email:'first@example.test',password:'synthetic-only',totp_secret:'fixture'}]; results=[]");
    await run('runAccounts(accounts)');
    let selectedLogin = JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body);
    assert.equal(selectedLogin.auto_deliver, false);
    assert.equal(selectedLogin.proxy, '');
    assert.deepEqual(selectedLogin.delivery_options.egress_route_ids, [67, 104]);
    const normalFetchBeforeRelogin = context.fetch;
    context.fetch = (url, options) => url === '/api/history/login'
        ? (requests.push({ url, options }), Promise.resolve({ ok: false, status: 400, headers: new Map([['content-type', 'application/json']]), json: async () => ({ message: 'synthetic stop' }) }))
        : normalFetchBeforeRelogin(url, options);
    context.performHistoryRelogin = originalHistoryRelogin;
    await run("performHistoryRelogin('9001')");
    context.fetch = normalFetchBeforeRelogin;
    const historyLogin = JSON.parse(requests.filter(item => item.url === '/api/history/login').at(-1).options.body);
    assert.equal(historyLogin.delivery_options, undefined, 'history revival uses its current Sub2 account route, not the next batch selection');
    assert.equal(historyLogin.proxy, undefined, 'history relogin uses the saved route when no new selection is provided');
    assert.equal(historyLogin.manual_attempt, true, 'an explicit history login permits one attempt');
    importSettings.egress_routes[1].eligible = false;
    const invalidRouteDialog = run('chooseDeliveryOptions()'); await flush();
    assert.equal(el('deliveryOptionsApply').disabled, true, 'a saved route that becomes unavailable blocks submission');
    assert.match(el('deliveryEgressRoutes').innerHTML, /data-delivery-route="67" checked(?! disabled)/, 'the unavailable selected route remains removable');
    await el('deliveryEgressRoutes').emit('change', { dataset: { deliveryRoute: '67' }, checked: false });
    assert.equal(el('deliveryOptionsApply').disabled, false, 'removing the unavailable route restores submission');
    await el('deliveryOptionsForm').emit('submit'); await invalidRouteDialog;
    importSettings.egress_routes[1].eligible = true;
    run('deliveryBatchOptions = {group_ids:[],priority:1,concurrency:3,egress_route_ids:[104]}');
    run("accounts[0].status = 'error'");
    await window.retryAccount(0);
    selectedLogin = JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body);
    assert.deepEqual(selectedLogin.delivery_options.egress_route_ids, [67, 104], 'retry keeps the original login route');
    // A login-only batch waits for the settings request already started by page initialization.
    const defaultsBeforeLoginRace = importSettings.defaults;
    importSettings.defaults = { ...defaultsBeforeLoginRace, egress_route_ids: [67, 104] };
    run("deliveryBatchOptions = null; deliverySettings = null; accounts = [{id:0,email:'first@example.test',password:'synthetic-only',totp_secret:'fixture'}]; results=[]");
    settingsReadGate = gate(); const pendingLoginDefaults = settingsReadGate;
    const loginDefaultsRead = run('loadDeliverySettings()');
    const settingsBeforeLoginRace = requests.filter(item => item.url === '/api/sub2/import-settings').length;
    const loginsBeforeLoginRace = requests.filter(item => item.url === '/api/login/stream').length;
    const delayedLoginBatch = run('runAccounts(accounts)');
    await flush();
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, loginsBeforeLoginRace, 'login waits before freezing unresolved default exits');
    assert.equal(requests.filter(item => item.url === '/api/sub2/import-settings').length, settingsBeforeLoginRace, 'login reuses the pending settings request');
    pendingLoginDefaults.release();
    await Promise.all([loginDefaultsRead, delayedLoginBatch]);
    selectedLogin = JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body);
    assert.equal(selectedLogin.auto_deliver, false);
    assert.deepEqual(selectedLogin.delivery_options.egress_route_ids, [67, 104], 'login-only batch retains defaults that arrive during initialization');
    importSettings.defaults = defaultsBeforeLoginRace;
    // An explicit empty exit selection must survive both the request and a later retry.
    run("deliveryBatchOptions = {group_ids:[],priority:1,concurrency:3}; accounts = [{id:0,email:'first@example.test',password:'synthetic-only',totp_secret:'fixture'}]; results=[]");
    loginFailures = 1;
    await run('runAccounts(accounts)');
    selectedLogin = JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body);
    assert.deepEqual(selectedLogin.delivery_options, { group_ids: [], priority: 1, concurrency: 3 }, 'an empty selection is sent explicitly instead of inheriting server defaults');
    run('deliveryBatchOptions = {group_ids:[],priority:1,concurrency:3,egress_route_ids:[104]}');
    await window.retryAccount(0);
    selectedLogin = JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body);
    assert.deepEqual(selectedLogin.delivery_options, { group_ids: [], priority: 1, concurrency: 3 }, 'retry keeps its empty selection after the next batch chooses an exit');
    // Login-only batches do not depend on Sub2 settings; import asks for identity fields later.
    await change('autoDeliverToggle', false);
    run("deliveryBatchOptions = null; deliverySettings = null; accounts = [{id:0,email:'first@example.test',password:'synthetic-only',totp_secret:'fixture'}]; results=[]");
    rows = [{ id: 9001, email: 'first@example.test', status: 'active' }];
    deliveries = {}; rechecks = {}; repairs = {}; imports = [];
    const settingsBeforeOffline = requests.filter(item => item.url === '/api/sub2/import-settings').length;
    settingsReadFailure = true;
    await run('runAccounts(accounts)');
    assert.equal(requests.filter(item => item.url === '/api/sub2/import-settings').length, settingsBeforeOffline, 'login-only batch never waits for unavailable delivery settings');
    assert.equal(JSON.parse(requests.filter(item => item.url === '/api/login/stream').at(-1).options.body).delivery_options, undefined);
    settingsReadFailure = false;
    confirmResult = null;
    const offlineImport = run('importLoginResultsToSub2()'); await flush();
    el('deliveryMerchant').value = '手动号商';
    el('deliveryDeadline').value = '2026-12-31';
    await el('deliveryOptionsForm').emit('submit'); await offlineImport;
    assert.deepEqual(JSON.parse(requests.filter(item => item.url === '/api/sub2/import').at(-1).options.body).delivery_options, {
        group_ids: [7], priority: 4, concurrency: 12, merchant: '手动号商', deadline: '2026-12-31'
    }, 'manual confirmation submits merchant and deadline');
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
    el('deliveryMerchant').value = '启动号商';
    el('deliveryDeadline').value = '2026-12-31';
    await el('deliveryOptionsForm').emit('submit'); await flush();
    assert.equal(requests.filter(item => item.url === '/api/login/stream').length, startRequestsBefore + 1, 'double Start submits one login');
    assert.equal(run('accounts[0].status'), 'processing', 'duplicate Start does not replace the running row');
    startedLogin.release(); await Promise.all([firstStart, duplicateStart]);
    assert.equal(run('accounts[0].status'), 'success', 'the running row retains its successful result');
    assert.equal(run('results.length'), 1);
    assert.equal(el('startBtn').disabled, false, 'completion releases Start');
    const canceledStartOptions = run('chooseDeliveryOptions()'); await flush();
    await click('deliveryOptionsCancel'); await canceledStartOptions;
    confirmResult = true;
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
    try {
        context.fetch = async (_url, options) => {
            connectionRequests++;
            return new Promise((_resolve, reject) => {
                options.signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })), { once: true });
            });
        };
        const stalled = run('loginAccount({email:"timeout@example.test"}, "direct", new AbortController().signal)');
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
        const streaming = run('loginAccount({email:"stream@example.test"}, "direct", new AbortController().signal)');
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
        await assert.rejects(run('loginAccount({email:"reset@example.test"}, "direct", new AbortController().signal)'), /fixture connection reset/);
        assert.equal(connectionRequests, 3, 'network failure must not automatically replay a login POST');
        assert.equal(connectionTimers.size, 0, 'network failure releases the connection timer');

        context.fetch = async () => {
            connectionRequests++;
            return { ...response({ code: 'upstream_rate_limit', message: '上游请求过多', retry_after: 120 }, 429),
                headers: new Map([['content-type', 'application/json']]) };
        };
        await assert.rejects(run('loginAccount({email:"limited@example.test"}, "direct", new AbortController().signal)'), /上游请求过多/);
        assert.equal(connectionRequests, 4, 'an upstream 429 remains an error without replaying the login POST');
        assert.equal(connectionTimers.size, 0, 'an upstream 429 does not schedule a platform-wide pause');
        context.fetch = savedFetch;
        assert.equal((await run('loginAccount({email:"next@example.test"}, "direct", new AbortController().signal)')).success, true,
            'the next account can start without a shared cooldown');
    } finally {
        context.fetch = savedFetch;
        context.setTimeout = savedSetTimeout;
        context.clearTimeout = savedClearTimeout;
    }
    // A stopped request stays non-retryable until its persisted result is known.
    await change('autoDeliverToggle', false);
    el('modeSelect').value = 'sequential';
    run("accounts = ['first', 'second', 'third'].map((name, id) => ({id, email:name + '@example.test', password:'synthetic-only', totp_secret:'fixture'})); results = []");
    const stopRequestsBefore = requests.filter(item => item.url === '/api/login/stream').length;
    const secondStarted = gate();
    let streamAborted = false;
    context.fetch = async (url, options = {}) => {
        if (url !== '/api/login/stream' || JSON.parse(options.body).email !== 'second@example.test') return savedFetch(url, options);
        requests.push({ url, options });
        const body = new ReadableStream({ start(controller) {
            options.signal.addEventListener('abort', () => {
                streamAborted = true;
                controller.error(new Error('stream aborted'));
            }, { once: true });
        } });
        secondStarted.release();
        return { ok: true, headers: new Map([['content-type', 'text/event-stream']]), body };
    };
    try {
        const stoppedBatch = run('runAccounts(accounts)');
        await secondStarted.promise; await flush();
        assert.equal(el('stopBtn').hidden, false, 'Stop is available during an active batch');
        await click('stopBtn');
        await stoppedBatch;
        assert.equal(streamAborted, true, 'Stop aborts the in-flight SSE request');
        assert.deepEqual(Array.from(run('accounts.map(account => account.status)')), ['success', 'checking', 'stopped']);
        assert.equal(run('results.length'), 1, 'completed results survive Stop');
        assert.equal(el('successCount').textContent, 1);
        assert.equal(el('stoppedCount').textContent, 1);
        assert.equal(el('checkingCount').textContent, 1);
        assert.equal(el('pendingCount').textContent, 0);
        assert.deepEqual(requests.filter(item => item.url === '/api/login/stream').slice(stopRequestsBefore).map(item => JSON.parse(item.options.body).email),
            ['first@example.test', 'second@example.test'], 'queued accounts never start after Stop');
        assert.equal(el('stopBtn').hidden, true, 'Stop hides after cleanup');
        const requestsBeforeUnsafeRetry = requests.filter(item => item.url === '/api/login/stream').length;
        await window.retryAccount(1);
        assert.equal(requests.filter(item => item.url === '/api/login/stream').length, requestsBeforeUnsafeRetry,
            'an unresolved in-flight request cannot be retried');
        rows.push({ id: 9002, email: 'second@example.test', status: 'active', attempt_count: 1,
            last_attempt_at: new Date(run('accounts[1].startTime')).toISOString(),
            last_success_at: new Date().toISOString() });
        await window.recheckStoppedAccount(1);
        assert.deepEqual(Array.from(run('accounts.map(account => account.status)')), ['success', 'success', 'stopped']);
        assert.match(run('accounts[1].message'), /登录结果已保存/);
        assert.equal(Boolean(run('accounts[1].result')), false, 'history reconciliation does not invent a returned token');
        assert.equal(run('accounts[1].historyID'), '9002');
        assert.equal(el('retryFailedBtn').disabled, false, 'only safely stopped queued accounts remain retryable');
    } finally {
        context.fetch = savedFetch;
    }
    // The server-busy retry delay must also yield to Stop without another POST.
    run("accounts = [{id:0,email:'busy@example.test',password:'synthetic-only',totp_secret:'fixture'}]; results = []");
    const busyStarted = gate();
    const busyRequestsBefore = requests.filter(item => item.url === '/api/login/stream').length;
    context.fetch = async (url, options = {}) => {
        if (url !== '/api/login/stream') return savedFetch(url, options);
        requests.push({ url, options });
        busyStarted.release();
        return { ...response({ code: 'platform_busy', retry_after: 1 }, 429), headers: new Map([['content-type', 'application/json']]) };
    };
    try {
        const busyBatch = run('runAccounts(accounts)');
        await busyStarted.promise; await flush();
        await click('stopBtn');
        await busyBatch;
        assert.equal(run('accounts[0].status'), 'stopped');
        assert.equal(requests.filter(item => item.url === '/api/login/stream').length, busyRequestsBefore + 1,
            'Stop ends the concurrency wait without replaying the login POST');
    } finally {
        context.fetch = savedFetch;
    }
    // A persisted failed attempt can be retried after a fresh history read.
    run("accounts = [{id:0,email:'failed-stop@example.test',password:'synthetic-only',totp_secret:'fixture'}]; results = []");
    const failedStarted = gate();
    context.fetch = async (url, options = {}) => {
        if (url !== '/api/login/stream') return savedFetch(url, options);
        requests.push({ url, options });
        const body = new ReadableStream({ start(controller) {
            options.signal.addEventListener('abort', () => controller.error(new Error('stream aborted')), { once: true });
        } });
        failedStarted.release();
        return { ok: true, headers: new Map([['content-type', 'text/event-stream']]), body };
    };
    try {
        const failedBatch = run('runAccounts(accounts)');
        await failedStarted.promise; await flush();
        await click('stopBtn');
        await failedBatch;
        assert.equal(run('accounts[0].status'), 'checking');
        rows.push({ id: 9011, email: 'failed-stop@example.test', status: 'failed', attempt_count: 1,
            last_attempt_at: new Date(run('accounts[0].startTime')).toISOString() });
        await window.recheckStoppedAccount(0);
        assert.equal(run('accounts[0].status'), 'stopped');
        assert.equal(el('retryFailedBtn').disabled, false);
    } finally {
        context.fetch = savedFetch;
    }
    // Parallel Stop cancels every active stream and never starts the queued item.
    el('modeSelect').value = 'parallel';
    el('concurrency').value = '2';
    run("accounts = ['parallel-a', 'parallel-b', 'parallel-c'].map((name, id) => ({id,email:name+'@example.test',password:'synthetic-only',totp_secret:'fixture'})); results = []");
    const parallelStarted = gate();
    let parallelRequests = 0, parallelAborts = 0;
    context.fetch = async (url, options = {}) => {
        if (url !== '/api/login/stream') return savedFetch(url, options);
        requests.push({ url, options });
        parallelRequests++;
        if (parallelRequests === 2) parallelStarted.release();
        const body = new ReadableStream({ start(controller) {
            options.signal.addEventListener('abort', () => {
                parallelAborts++;
                controller.error(new Error('stream aborted'));
            }, { once: true });
        } });
        return { ok: true, headers: new Map([['content-type', 'text/event-stream']]), body };
    };
    try {
        const parallelBatch = run('runAccounts(accounts)');
        await parallelStarted.promise; await flush();
        await click('stopBtn');
        await parallelBatch;
        assert.equal(parallelRequests, 2);
        assert.equal(parallelAborts, 2);
        assert.deepEqual(Array.from(run('accounts.map(account => account.status)')), ['checking', 'checking', 'stopped']);
    } finally {
        context.fetch = savedFetch;
        el('modeSelect').value = 'sequential';
    }
    // Group conflicts expose a safe, explicit choice with redacted task
    // evidence; the request carries the displayed IDs as a CAS snapshot.
    rows.push({ id: 9010, email: 'group-conflict@example.test', status: 'error', recovery_count: 0, recovery_attempt_count: 1 });
    deliveries[9010] = {
        id: 551, account_id: 9010, state: 'requires_action', requires_action: true,
        manual_action: '当前分组与本批目标分组不一致', error_code: 'group_conflict',
        failure_stage: 'applying_groups', http_status: 409,
        diagnostic: { current_group_ids: [13, 12], target_group_ids: [44, 12] }
    };
    await run('loadHistory()');
    assert.match(el('historyAttentionList').innerHTML, /当前分组：12、13；目标分组：12、44/);
    assert.match(el('historyAttentionList').innerHTML, /任务 #551/);
    assert.match(el('historyAttentionList').innerHTML, /保留当前分组/);
    assert.match(el('historyAttentionList').innerHTML, /按目标分组修正/);
    assert.match(el('historyAttentionList').innerHTML, /阶段：写入分组/);
    assert.match(el('historyAttentionList').innerHTML, /分类：group_conflict/);
    assert.match(el('historyAttentionList').innerHTML, /HTTP 409/);
    await run("resolveGroupConflict(historyDeliveries.get('9010'), 'keep_current')");
    assert.deepEqual(groupActions.at(-1), {
        url: '/api/account-recovery/551/groups',
        body: { action: 'keep_current', current_group_ids: [12, 13], target_group_ids: [12, 44] }
    });
    await run("resolveGroupConflict(historyDeliveries.get('9010'), 'apply_target')");
    assert.deepEqual(groupActions.at(-1).body, { action: 'apply_target', current_group_ids: [12, 13], target_group_ids: [12, 44] });
    // Account identity and plan filtering preserve selection and unfamiliar raw plans.
    rows = [
        { id: 9101, email: 'prefix@example.test', status: 'active', plan_type: 'plus' },
        { id: 9102, email: 'free@example.test', status: 'error', plan_type: 'free' },
        { id: 9103, email: 'unknown@example.test', status: 'active' },
        { id: 9104, email: 'new-plan@example.test', status: 'active', plan_type: 'self_serve_business_prolite' }
    ];
    sub2Statuses = { 9101: { exists: true, imported: true, association_status: 'linked', sub2_account_id: 5101, status: 'active', name: '自定义前缀 | 截止 | prefix@example.test', name_source: 'cached_remote', name_stale: true } };
    imports = []; recoveries = {}; deliveries = {}; repairs = {}; rechecks = {}; checks = {}; lifecycleEvents = {};
    run('historySelectedIDs.clear()');
    await change('historyStatusFilter', 'all'); await change('historySearchInput', ''); await run('loadHistory()');
    assert.match(el('historyGrid').innerHTML, /自定义前缀 \| 截止 \| prefix@example.test/);
    assert.match(el('historyGrid').innerHTML, /AUTH #9101 · Sub2 #5101 · Plus/);
    assert.match(el('historyGrid').innerHTML, /缓存名称 · 待同步/);
    assert.match(el('historyPlanFilter').innerHTML, /value="free".*Free/);
    assert.match(el('historyPlanFilter').innerHTML, /value="unknown".*未知（未识别）/);
    assert.match(el('historyPlanFilter').innerHTML, /value="self_serve_business_prolite"/);
    run("historySelectedIDs.add('9102'); historyPage = 2");
    await change('historySearchInput', '自定义前缀');
    await change('historyPlanFilter', 'plus');
    assert.equal(run('historyPage'), 1);
    assert.deepEqual(Array.from(run('getFilteredHistoryAccounts().map(item => item.id)')), ['9101']);
    assert.match(el('historySelectionStatus').textContent, /1 个不在当前页/);
    await change('historyStatusFilter', 'failed');
    assert.equal(run('getFilteredHistoryAccounts().length'), 0, 'search, plan and status use their intersection');
    await change('historyStatusFilter', 'all');
    rows = rows.filter(row => row.id !== 9101);
    await run('loadHistory()');
    assert.equal(el('historyPlanFilter').value, 'plus');
    assert.match(el('historyPlanFilter').innerHTML, /Plus（0 个匹配）/);
    assert.equal(run('getFilteredHistoryAccounts().length'), 0, 'refresh never silently expands a selected plan to all');
    await change('historySearchInput', ''); await change('historyPlanFilter', 'unknown');
    assert.deepEqual(Array.from(run('getFilteredHistoryAccounts().map(item => item.id)')), ['9103']);
    await change('historyPlanFilter', 'all');

    // Canonical association and login stop reasons take precedence over stale queued work.
    rows = [{ id: 9201, email: 'missing@example.test', status: 'error' }, { id: 9202, email: 'unmatched@example.test', status: 'error' },
        { id: 9203, email: 'email-challenge@example.test', status: 'error', requires_action: true, retryable: false, manual_reason: 'email_verification_required', last_error: 'old timeout', last_attempt_at: '2026-11-13T10:00:00Z' }];
    sub2Statuses = {
        9201: { association_status: 'missing', sub2_account_id: 5201, exists: false, name: '保留前缀 | missing@example.test', name_source: 'historical_import', name_stale: true },
        9202: { association_status: 'unmatched', exists: false },
        9203: { association_status: 'linked', exists: true, imported: true, status: 'error', sub2_account_id: 5203 }
    };
    imports = [{ account_id: 9201, state: 'remote_missing', sub2_account_id: 5201 }];
    checks = { 9201: { eligible: true }, 9202: { eligible: true }, 9203: { eligible: true } };
    deliveries = { 9201: { state: 'retry_wait', next_retry_at: '2020-01-01T00:00:00Z', delivery_options: { group_ids: [], priority: 1, concurrency: 3 } }, 9203: { state: 'retry_wait', next_retry_at: '2020-01-01T00:00:00Z' } };
    recoveries = { 9201: { state: 'unknown', resumable: true }, 9203: { id: 6301, retry_count: 2, state: 'unknown', resumable: true } };
    await run('loadHistory()');
    for (const id of ['9201', '9202', '9203']) {
        const card = costRow(id);
        assert.match(card, /自动处理已暂停/);
        assert.match(card, /history-more-status/, 'paused accounts explain automatic pause and available manual actions');
        assert.match(card, new RegExp(`data-history-action="details" data-history-id="${id}"`));
        assert.match(card, /data-history-action="relogin"/, 'automatic pause keeps explicit relogin available');
        assert.doesNotMatch(card, /data-history-action="(?:import|reconcile)"/);
        assert.doesNotMatch(card, /下次自动|等待后台执行/);
        assert.equal(run(`historyRecoveryAction(historyAccounts.find(item => item.id === '${id}')).disabled`), id !== '9203');
        assert.equal(run(`canBatchRelogin(historyAccounts.find(item => item.id === '${id}'))`), true);
        assert.equal(Boolean(run(`canCheckHistoryAccount(historyAccounts.find(item => item.id === '${id}'))`)), false, 'the hidden legacy check action keeps its automatic eligibility policy');
    }
    assert.match(costRow('9201'), /Sub2 关联账号不存在 · 已停止自动检测和恢复/);
    assert.match(costRow('9201'), /保留前缀/);
    assert.doesNotMatch(costRow('9201'), /官方删除/);
    assert.match(costRow('9201'), /仅重新登录 AUTH/);
    assert.doesNotMatch(costRow('9201'), /data-history-action="(?:credentials|recover|import|reconcile)"/);
    assert.match(costRow('9201'), /data-history-action="reimport"[^>]*>重新登录并导入 Sub2/);
    assert.match(costRow('9202'), /未找到 Sub2 关联 · 不参与自动恢复/);
    assert.match(costRow('9202'), /data-history-action="credentials"/);
    assert.match(run('renderHistoryCredentials(historyAccounts.find(a => a.id === "9202"), {})'), /data-history-detail-edit >修改资料/);
    rows[1].status = 'active';
    await run('loadHistory()');
    assert.equal(run('canImportToSub2(historyAccounts.find(a => a.id === "9202"))'), true, 'unmatched permits explicit first import after login success');
    assert.match(costRow('9202'), /data-history-action="import"/);
    assert.equal(run('historyRecoveryAction(historyAccounts.find(a => a.id === "9202")).visible'), false, 'unmatched remains excluded from automatic recovery');
    assert.doesNotMatch(costRow('9202'), /Sub2 已删除|官方删除/);
    assert.match(costRow('9203'), /需要邮箱验证码 · 已停止自动重试/);
    assert.match(costRow('9203'), /data-history-action="recover"/);
    assert.match(costRow('9203'), /data-history-action="credentials"/);
    assert.match(run("formatLoginError({error:{manual_reason:'password_rejected',code:'invalid_credentials',stage:'password',message:'generic'}},{})"), /密码错误 · 已停止自动重试/);
    assert.match(run("formatLoginError({error:{code:'verification_code_rejected',upstream_code:'incorrect_code',stage:'mfa'}},{})"), /上游 incorrect_code/);
    assert.doesNotMatch(run("formatLoginError({error:{code:'verification_code_rejected',upstream_code:'token=sensitive',stage:'mfa'}},{})"), /sensitive/);
    assert.doesNotMatch(run("formatLoginError({error:{code:'incorrect_code',stage:'mfa',message:'验证码错误，验证类型待确认'}},{})"), /TOTP 密钥错误/);
    assert.doesNotMatch(run("automaticTaskPolicy({state:'retry_wait',manual_reason:'totp_rejected',next_retry_at:'2020-01-01T00:00:00Z'})"), /下次自动|等待后台/);
    const stoppedRequests = requests.length;
    await run("checkAndRecoverHistory('9201')");
    assert.equal(requests.length, stoppedRequests, 'missing association cannot start recovery');

    // One parameter confirmation authorizes login followed by explicit reimport.
    // Ordinary import and background recovery remain blocked for this binding.
    const normalHistoryFetch = context.fetch;
    const reimportPosts = () => requests.filter(item => item.url === '/api/sub2/import' && JSON.parse(item.options.body).reimport);
    const historyLoginPosts = () => requests.filter(item => item.url === '/api/history/login');
    const priorHistoryLogins = historyLoginPosts().length;
    const savedMissingDelivery = structuredClone(deliveries[9201]);
    const savedMissingImports = structuredClone(imports);
    const savedAcceptedImports = acceptedImportIDs;
    const reimportOptions = { group_ids: [8], priority: 9, concurrency: 4, merchant: '旧号商', deadline: '2026-12-31' };
    deliveries[9201].delivery_options = reimportOptions;
    imports[0].operation_id = 'missing-operation-9201';
    await run('loadHistory()');
    const originalBatchOptions = run('JSON.stringify(deliveryBatchOptions)');
    const originalDefaults = structuredClone(importSettings.defaults);
    selectedGroups = [8];
    acceptedImportIDs = new Set([9201]);
    confirmResult = null;
    const canceledReimport = run("reimportRemoteMissingHistory('9201')"); await flush();
    assert.equal(el('deliveryOptionsDialog').open, true);
    assert.equal(el('deliveryOptionsApply').textContent, '重新登录并导入');
    assert.equal(el('deliveryMerchant').value, '旧号商', 'reimport starts from account delivery options');
    assert.equal(el('deliveryPriority').value, '9');
    assert.match(el('deliveryGroups').innerHTML, /data-delivery-group="8" checked/);
    await el('deliveryOptionsCancel').emit('click'); await canceledReimport;
    assert.equal(historyLoginPosts().length, priorHistoryLogins, 'canceling parameters does not log in');
    assert.equal(reimportPosts().length, 0, 'canceling parameters does not import');
    assert.equal(run('JSON.stringify(deliveryBatchOptions)'), originalBatchOptions);

    let reimportLoginSucceeded = false, afterReimportLogin = null, reimportLoginGate = null;
    context.fetch = async (url, options = {}) => {
        if (url !== '/api/history/login') return normalHistoryFetch(url, options);
        requests.push({ url, options });
        assert.equal(run('processing && historyBatchRunning'), true, 'the entire reimport operation owns the UI lock');
        assert.deepEqual(JSON.parse(options.body), { account_id: 9201, auto_deliver: false, manual_attempt: true, delivery_options: reimportOptions });
        if (reimportLoginGate) await reimportLoginGate.promise;
        if (!reimportLoginSucceeded) {
            rows[0].status = 'failed'; rows[0].last_error = '代理配置无效'; rows[0].last_error_code = 'invalid_configuration';
            return { ok: false, status: 400, headers: new Map([['content-type', 'application/json']]), json: async () => ({ message: '代理配置无效' }) };
        }
        rows[0].last_error = rows[0].last_error_code = '';
        rows[0].status = 'active'; rows[0].credential_version = (rows[0].credential_version || 122) + 1;
        afterReimportLogin?.();
        const payload = { type: 'result', success: true, data: { Email: 'missing@example.test', account_id: 9201 } };
        return { ok: true, headers: new Map([['content-type', 'text/event-stream']]), body: new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(payload)}\n\n`)); controller.close(); } }) };
    };
    confirmResult = true;
    await run("reimportRemoteMissingHistory('9201')");
    assert.equal(reimportPosts().length, 0, 'failed login does not import');
    assert.equal(run('processing || historyBatchRunning'), false, 'failure releases both locks');
    assert.match(el('historyBatchStatus').textContent, /代理配置无效/);
    assert.match(costRow('9201'), /<summary><span>Sub2 关联账号不存在 · 已停止自动检测和恢复；最近登录失败：代理配置无效<\/span>/, 'missing status must not hide the most recent login failure');
    assert.match(costRow('9201'), /class="history-more-status"[^>]*>[^<]*最近登录失败：代理配置无效 · invalid_configuration/, 'More shows the concrete login failure beside the missing association explanation');
    assert.match(costRow('9201'), /data-history-action="reimport" data-history-id="9201"\s*>/, 'failed reimport releases the per-account button after the final history refresh');
    assert.equal(run('historyBusyID'), '');
    assert.equal(run('historyActionsLocked()'), false);
    assert.equal(run('JSON.stringify(deliveryBatchOptions)'), originalBatchOptions, 'account options do not replace batch options');
    assert.deepEqual(importSettings.defaults, originalDefaults, 'account options do not silently save platform defaults');

    reimportLoginSucceeded = true;
    afterReimportLogin = () => { imports[0].operation_id = 'changed-operation'; };
    await run("reimportRemoteMissingHistory('9201')");
    assert.equal(reimportPosts().length, 0, 'changed binding after login cannot reuse the initial consent');
    imports[0].operation_id = 'missing-operation-9201';
    await run('loadHistory()');
    afterReimportLogin = () => { historyFailure = true; };
    await run("reimportRemoteMissingHistory('9201')");
    assert.equal(reimportPosts().length, 0, 'failed fresh history read cannot authorize reimport');
    historyFailure = false; afterReimportLogin = null;
    await run('loadHistory()');
    reimportLoginGate = gate();
    const successfulReimport = run("reimportRemoteMissingHistory('9201')"); await flush();
    const duringLogin = historyLoginPosts().length;
    await run("reimportRemoteMissingHistory('9201')");
    assert.equal(historyLoginPosts().length, duringLogin, 'duplicate clicks cannot start another login');
    reimportLoginGate.release(); await successfulReimport;
    assert.equal(reimportPosts().length, 1, 'successful confirmed flow sends exactly one reimport');
    assert.deepEqual(JSON.parse(reimportPosts()[0].options.body), { account_ids: [9201], delivery_options: reimportOptions, reimport: true,
        expected_credential_version: 125, expected_import_operation_id: 'missing-operation-9201' });
    assert.match(el('historyBatchStatus').textContent, /重新登录并导入 Sub2 已完成/);
    assert.equal(run('processing || historyBatchRunning'), false);
    assert.equal(run('JSON.stringify(deliveryBatchOptions)'), originalBatchOptions);
    assert.equal(el('actionConfirmDialog').open, false, 'reimport needs no second confirmation after login');
    imports = [{ ...savedMissingImports[0], operation_id: 'missing-operation-9201' }];
    deliveries[9201] = { ...savedMissingDelivery, delivery_options: reimportOptions };
    await run('loadHistory()');
    afterReimportLogin = () => { rows[0].credential_version--; };
    await run("reimportRemoteMissingHistory('9201')");
    assert.equal(reimportPosts().length, 1, 'SSE success without a newer stored credential version cannot authorize reimport');
    afterReimportLogin = null;
    const successFetch = context.fetch;
    context.fetch = async (url, options = {}) => {
        if (url === '/api/sub2/import') { requests.push({ url, options }); throw run('new TypeError("synthetic lost response")'); }
        return successFetch(url, options);
    };
    const historyReadsBeforeUnknown = requests.filter(item => item.url === '/api/history').length;
    await run("reimportRemoteMissingHistory('9201')");
    assert.equal(reimportPosts().length, 2, 'uncertain reimport sends one request without automatic replay');
    assert.match(el('historyBatchStatus').textContent, /提交结果暂未确认，请刷新核对/);
    assert.ok(requests.filter(item => item.url === '/api/history').length > historyReadsBeforeUnknown, 'uncertain submission refreshes persisted state');
    assert.equal(run('processing || historyBatchRunning'), false);
    context.fetch = normalHistoryFetch;
    selectedGroups = [7]; acceptedImportIDs = savedAcceptedImports;
    imports = savedMissingImports; deliveries[9201] = savedMissingDelivery;
    rows[0].status = 'error'; delete rows[0].credential_version;
    await run('loadHistory()');
    await run("performHistoryRelogin('9201')");
    assert.deepEqual(JSON.parse(requests.at(-1).options.body), { account_id: 9201, auto_deliver: false, manual_attempt: true }, 'missing association may only log in to AUTH without recreating Sub2');
    await run("checkAndRecoverHistory('9203')");
    assert.deepEqual(JSON.parse(requests.findLast(item => item.url === '/api/account-recovery').options.body), { account_id: 9203, manual_attempt: true, expected_task_id: 6301, expected_retry_count: 2 }, 'canonical failures permit an explicit single resume fenced to the displayed task');
    recoveries = {}; deliveries = {}; checks[9203] = { eligible: true };
    for (const code of ['auth_page_timeout_exhausted', 'totp_rejected', 'password_rejected', 'phone_verification_required']) {
        rows[2].manual_reason = code;
        await run('loadHistory()');
        assert.equal(run('historyRecoveryAction(historyAccounts.find(a => a.id === "9203")).disabled'), false, `${code} permits manual recovery`);
        assert.equal(run('canReloginHistory(historyAccounts.find(a => a.id === "9203"))'), true);
    }
    rows[2].status = 'deleted';
    await run('loadHistory()');
    const deletedRequests = requests.length;
    await run("performHistoryRelogin('9203'); checkAndRecoverHistory('9203')");
    assert.equal(requests.length, deletedRequests, 'official deletion blocks relogin and recovery');
    for (const status of ['deactivated', 'deleted_or_deactivated']) {
        rows[2].status = status;
        await run('loadHistory()');
        assert.equal(run('canReloginHistory(historyAccounts.find(a => a.id === "9203"))'), false, 'deactivation is terminal');
        assert.equal(run('historyRecoveryAction(historyAccounts.find(a => a.id === "9203")).disabled'), true);
        assert.equal(run('historyRefreshTokens.has("9203")'), false);
        assert.equal(run('historyAccountMatches(historyAccounts.find(a => a.id === "9203"), "attention")'), false);
    }
    rows[2].status = 'active';
    rows[2].sub2_policy = 'free_local';
    await run('loadHistory()');
    assert.equal(run('canReloginHistory(historyAccounts.find(a => a.id === "9203"))'), true);
    assert.equal(run('canImportToSub2(historyAccounts.find(a => a.id === "9203"))'), false);
    rows[2].status = 'discarded';
    rows[2].sub2_policy = 'discarded';
    rows[2].last_error_code = 'invalid_username_or_password';
    rows[2].last_error = 'raw upstream text must stay hidden';
    checks[9203] = { eligible: true, last_result: { freshness: 'current', outcome: 'account_deleted' } };
    run('historyRefreshTokens.set("9203", "fixture-token")');
    await run('loadHistory()');
    assert.match(costRow('9203'), /本地废弃/);
    assert.match(costRow('9203'), /废弃原因：账密被拒绝/);
    assert.doesNotMatch(costRow('9203'), /官方删除|raw upstream text|data-history-action="(?:relogin|credentials|recover)"/);
    assert.equal(run('historyRefreshTokens.has("9203")'), false);
    assert.equal(run('renderHistoryCredentials(historyAccounts.find(a => a.id === "9203"), {})'), '');
    run('renderHistoryDetail(historyAccounts.find(a => a.id === "9203"), [{ code: "invalid_username_or_password", message: "raw upstream text" }], [], true, false, [{ status: "discarded", code: "invalid_username_or_password", reason: "raw upstream text" }])');
    assert.match(el('historyDetailBody').innerHTML, /已废弃 · 账密被拒绝/);
    assert.doesNotMatch(el('historyDetailBody').innerHTML, /invalid_username_or_password|raw upstream text/);
    const discardedRequests = requests.length;
    await run("performHistoryRelogin('9203'); checkAndRecoverHistory('9203'); reconcileHistoryImport('9203')");
    assert.equal(requests.length, discardedRequests, 'discarded account blocks actions despite stale task data');
    const nativeDetailsQuery = el('historyGrid').querySelectorAll;
    el('historyGrid').querySelectorAll = () => ({ length: 0, [Symbol.iterator]: function* () {} });
    assert.doesNotThrow(() => run('renderHistory()'), 'real NodeList collections have no Array.map method');
    el('historyGrid').querySelectorAll = nativeDetailsQuery;
    rows = [{ id: 9204, email: 'fresh-success@example.test', status: 'active', credential_version: 102, updated_at: '2026-11-13T12:00:00Z' }];
    sub2Statuses = { 9204: { configured: true, imported: true, exists: true, status: 'active', schedulable: true, association_status: 'imported' } };
    imports = [{ account_id: 9204, state: 'imported', sub2_account_id: 123 }];
    recoveries = { 9204: { state: 'unknown', failure_stage: 'logging_in', retry_action: 'manual', requires_action: true, error_code: 'password_rejected', source_credential_attempt_id: 101, result_credential_attempt_id: 0, resumable: true, updated_at: '2026-11-13T12:00:00Z' } };
    deliveries = {}; repairs = {}; checks = {}; rechecks = {};
    await run('loadHistory()');
    assert.equal(run('automationStopped(historyAccounts[0])'), null, 'new credential version supersedes old deterministic failure even within the same second');
    assert.doesNotMatch(el('historyGrid').innerHTML, /自动处理已暂停|密码错误/);
    assert.equal(run('historyRecoveryAction(historyAccounts[0]).resume'), false, 'old failed task is never resumed after newer credentials');
    assert.equal(run('historyAccountMatches(historyAccounts[0], "attention")'), false);
    recoveries[9204].result_credential_attempt_id = 102;
    await run('loadHistory()');
    assert.match(run('automationStopped(historyAccounts[0]).label'), /密码错误/, 'failure against current result credentials still blocks');
    recoveries[9204].error_code = 'identity_mismatch'; recoveries[9204].result_credential_attempt_id = 0;
    await run('loadHistory()');
    assert.equal(run('historyAccountTasks(historyAccounts[0]).recovery.error_code'), 'identity_mismatch', 'new credential version does not dismiss identity or group work');
    assert.equal(run('loginTaskSuperseded(historyAccounts[0], {error_code:"password_rejected"})'), false, 'missing task versions cannot prove a superseded failure');
    const oldLoginFailure = { state: 'unknown', failure_stage: 'logging_in', retry_action: 'manual', error_code: 'auth_page_timeout_exhausted', source_credential_attempt_id: 101, result_credential_attempt_id: 0 };
    const isSuperseded = task => run(`loginTaskSuperseded(historyAccounts[0], ${JSON.stringify(task)})`);
    for (const error_code of ['email_verification_required', 'phone_verification_required', 'password_rejected', 'totp_rejected', 'verification_code_rejected', 'credentials_rejected', 'auth_page_timeout_exhausted']) {
        assert.equal(isSuperseded({ ...oldLoginFailure, error_code }), true, 'new success supersedes a known login-stage failure');
    }
    for (const change of [{ failure_stage: '' }, { failure_stage: 'identity_verified' }, { failure_stage: 'enabling_schedule' },
        { retry_action: 'relogin' }, { state: 'requires_action' }, { state: 'logging_in' }, { error_code: 'identity_mismatch' },
        { error_code: 'group_conflict' }, { error_code: 'manual_pause' }, { error_code: 'identity_mismatch', manual_reason: 'totp_rejected' },
        { source_credential_attempt_id: 102 }, { result_credential_attempt_id: 102 }]) {
        assert.equal(isSuperseded({ ...oldLoginFailure, ...change }), false, 'new success cannot bypass other stages, projected delivery states or current credential failures');
    }
    recoveries[9204] = { ...oldLoginFailure, id: 6302, retry_count: 3, resumable: true };
    await run('loadHistory()');
    assert.equal(run('automationStopped(historyAccounts[0])'), null, 'old login-stage timeout no longer masks a newer successful login');
    assert.equal(run('historyRecoveryAction(historyAccounts[0]).resume'), false);
    repairs = { 9204: { state: 'recovery_queued' } }; recoveries = {};
    rows[0].status = 'failed'; rows[0].last_error_code = 'password_rejected'; rows[0].requires_action = true;
    await run('loadHistory()');
    assert.match(el('historyGrid').innerHTML, /资料已保存，等待继续恢复/);
    assert.equal(run('historyAccountMatches(historyAccounts[0], "working")'), true);
    assert.equal(run('historyAccountMatches(historyAccounts[0], "attention")'), false, 'accepted repair handoff takes precedence over historical failed status');
    assert.equal(run('automationStopped(historyAccounts[0])'), null);
    repairs = {}; rows[0].status = 'running';
    await run('loadHistory()');
    assert.equal(run('automationStopped(historyAccounts[0])'), null, 'an in-progress new attempt is not paused by historical last_error_code');

    // Single-field reveal is isolated from polling and discarded on close/switch/hide.
    rows = [{ id: 9301, email: 'secret-a@example.test', status: 'active' }, { id: 9302, email: 'secret-b@example.test', status: 'active' }];
    sub2Statuses = {}; imports = []; checks = {}; recoveries = {}; deliveries = {};
    credentialsMeta = { password: { state: 'saved' }, totp_secret: { state: 'saved' } };
    await run('loadHistory()');
    const secretNodes = new Map();
    const detailQuery = el('historyDetailBody').querySelector;
    el('historyDetailBody').querySelector = selector => {
        if (!selector.startsWith('[data-history-credential-')) return detailQuery.call(el('historyDetailBody'), selector);
        if (!secretNodes.has(selector)) secretNodes.set(selector, new Element());
        return secretNodes.get(selector);
    };
    const secretValue = field => el('historyDetailBody').querySelector(`[data-history-credential-value="${field}"]`);
    const secretStatus = field => el('historyDetailBody').querySelector(`[data-history-credential-status="${field}"]`);
    const secretButton = (field, action) => el('historyDetailBody').querySelector(`[data-history-credential-field="${field}"][data-history-credential-action="${action}"]`);
    context.secretButton = secretButton;
    const revealRequests = () => requests.filter(item => item.url.endsWith('/credentials/reveal'));
    await run("openHistoryDetails('9301')");
    assert.match(el('historyDetailBody').innerHTML, /登录资料/);
    assert.equal(revealRequests().length, 0, 'opening details only fetches metadata');
    await run("handleHistoryCredentialAction('password', 'show', secretButton('password','show'))");
    assert.equal(secretValue('password').textContent, 'fixture-revealed-password');
    assert.equal(secretButton('password', 'show').textContent, '隐藏');
    assert.equal(revealRequests().at(-1).url, '/api/history/9301/credentials/reveal');
    assert.deepEqual(JSON.parse(revealRequests().at(-1).options.body), { field: 'password' });
    assert.doesNotMatch(el('historyDetailBody').innerHTML + el('historyGrid').innerHTML + run('JSON.stringify(historyAccounts)'), /fixture-revealed/, 'secrets only enter field text nodes');
    await run('loadHistory()');
    assert.equal(secretValue('password').textContent, 'fixture-revealed-password', 'list polling leaves an explicitly revealed detail untouched');
    await run("handleHistoryCredentialAction('password', 'show', secretButton('password','show'))");
    assert.equal(secretValue('password').textContent, '••••••••');
    assert.equal(run('historyCredentialValues.size'), 0);
    assert.equal(revealRequests().length, 1, 'hide never sends a second reveal');
    await run("handleHistoryCredentialAction('totp_secret', 'copy', secretButton('totp_secret','copy'))");
    assert.equal(credentialCopies.at(-1), 'fixture-revealed-totp');
    assert.equal(run('historyCredentialValues.size'), 0, 'copy does not cache the value');
    const delayedReveal = gate(); revealGate = delayedReveal;
    const pendingReveal = run("handleHistoryCredentialAction('password', 'show', secretButton('password','show'))");
    await flush();
    const pendingSignal = revealRequests().at(-1).options.signal;
    await run("openHistoryDetails('9302')");
    assert.equal(pendingSignal.aborted, true);
    delayedReveal.release(); await pendingReveal;
    assert.equal(secretValue('password').textContent, '••••••••');
    assert.equal(run('historyCredentialValues.size'), 0, 'account A cannot populate account B');
    const delayedCopy = gate(); revealGate = delayedCopy;
    const pendingCopy = run("handleHistoryCredentialAction('password', 'copy', secretButton('password','copy'))");
    await flush(); const copyCount = credentialCopies.length;
    run('closeHistoryDetails()'); delayedCopy.release(); await pendingCopy;
    assert.equal(credentialCopies.length, copyCount, 'a response after close must not copy');
    assert.equal(el('historyDetailBody').innerHTML, '');
    await run("openHistoryDetails('9301')");
    revealReply = response({ success: true, account_id: 9302, field: 'password', value: 'wrong-account-secret' });
    await run("handleHistoryCredentialAction('password', 'show', secretButton('password','show'))");
    assert.equal(secretStatus('password').textContent, '资料响应无效，请刷新详情');
    assert.notEqual(secretValue('password').textContent, 'wrong-account-secret');
    revealReply = response({ code: 'credential_read_failed', message: 'do-not-render-raw-secret' }, 500);
    await run("handleHistoryCredentialAction('password', 'show', secretButton('password','show'))");
    assert.equal(secretStatus('password').textContent, '资料读取失败');
    revealReply = null;
    await run("handleHistoryCredentialAction('password', 'show', secretButton('password','show'))");
    document.hidden = true; await document.emit('visibilitychange'); document.hidden = false;
    assert.equal(secretValue('password').textContent, '••••••••');
    assert.equal(run('historyCredentialValues.size'), 0);
    run('closeHistoryDetails()');
    el('historyDetailBody').querySelector = detailQuery;
    assert.equal(browserPreferences.has('fixture-revealed-password'), false);

    const clipboard = context.navigator.clipboard, createElement = document.createElement, appendChild = document.body.appendChild;
    let fallbackField;
    context.navigator.clipboard = null;
    document.createElement = () => fallbackField = { value: '', style: {}, setAttribute() {}, select() {}, remove() { this.removed = true; } };
    document.body.appendChild = () => {};
    document.execCommand = () => { throw new Error('clipboard blocked'); };
    assert.equal(await run('copySecret("fixture-private-password", "密码")'), false);
    assert.equal(fallbackField.value, '', 'failed legacy copy clears the temporary field');
    assert.equal(fallbackField.removed, true, 'failed legacy copy never leaves a hidden secret in the DOM');
    context.navigator.clipboard = clipboard; document.createElement = createElement; document.body.appendChild = appendChild;
    delete document.execCommand;

    let exportedBlob, downloadClicks = 0;
    context.Blob = Blob;
    context.URL = { createObjectURL(blob) { exportedBlob = blob; return 'blob:fixture'; }, revokeObjectURL() {} };
    document.createElement = () => ({ click() { downloadClicks++; } });
    run("results = [{Email:'export@example.test', AccessToken:'synthetic-access', RefreshToken:'synthetic-refresh'}]");
    await click('exportBtn');
    const exported = JSON.parse(await exportedBlob.text());
    assert.equal(downloadClicks, 1);
    assert.equal(exported.accounts[0].extra.codex_fingerprint_mode, 'full', 'exported accounts default to full fingerprint convergence');
    assert.equal(Object.hasOwn(exported.accounts[0].extra, 'codex_fingerprint_seed'), false, 'Sub2 generates each account fingerprint seed');
    console.log('Export checks passed: default full fingerprint convergence without a shared seed.');
    console.log('Account management checks passed: trusted names, plan intersections/zero refresh, canonical Sub2 absence, deterministic stop actions, single-field reveal, metadata-only defaults and close/switch/hide races.');
    console.log('Recent cost checks passed: page-only reads, cache deduplication, zero/unknown, deleted account history, stale values, binding changes, late responses and details.');
    console.log('Group conflict checks passed: redacted task diagnostics, explicit choices, confirmation and snapshot-bound requests.');
    console.log('History checks passed: Sub2 status/settings, ordinary/cross-page select, cancellation, delete locking/409/404/timeouts/refresh failures, partial/chunked import, relogin retention.');
    console.log('Tab checks passed: selection isolation, navigation, keyboard, focus, hash/back navigation, panel scrolling.');
    console.log('Confirmation checks passed: plain text, cancellation/acceptance for all batch actions, modal focus/Tab/Escape/restore, repeat opens, navigation cancellation, stale-state guards.');
    console.log('Recovery popover checks passed: pool membership, counts, loading/error/empty/truncated records, HTML escaping, hover/focus/click, polling preservation, Escape/outside close, stale responses.');
    console.log('Automatic delivery checks passed: configuration/preference, frozen requests/retries, persisted history progress, distinct login/delivery states, retry timing, deduplicated manual attention and resolution.');
    console.log('Delivery parameter and correction checks passed: shared form/default save/failure, unavailable groups, frozen batch settings/retries, empty-secret preservation/explicit clears, delayed recheck notices.');
    console.log('Delivery identity checks passed: Unicode/control validation, late defaults, safe summary, default save, automatic batch/retry freezing, login-only independence and manual-import fields.');
    console.log('Regression checks passed: double Start during settings load, preparation failure/cancel release, and active/waiting/completed credential repair polling.');
    console.log('Login connection checks passed: bounded setup, uncertain POST is not replayed, active SSE respects the server deadline, and timers are released.');
    console.log('Batch Stop checks passed: sequential/parallel SSE cancellation, queued work, persisted success/failure reconciliation, retry gating and concurrency wait.');
    globalThis.__clientTestComplete = true;
})().then(() => {
    assert.equal(globalThis.__clientTestComplete, true, 'client interaction checks did not reach completion');
    clearTimeout(completionWatchdog);
}).catch(error => { clearTimeout(completionWatchdog); console.error(error); process.exitCode = 1; });
