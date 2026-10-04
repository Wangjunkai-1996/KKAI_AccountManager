// Offline browser-interaction fixture. No network, credentials, or dependencies.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
class Element {
    constructor(tag = 'div') { this.tagName = tag; this.children = []; this.dataset = {}; this.events = {}; this.attributes = {}; this.textContent = ''; this.value = ''; this.disabled = false; this.hidden = false; this.checked = false; this.open = false; this.replaceCount = 0; this.scrollTop = 0; }
    addEventListener(name, fn) { (this.events[name] ||= []).push(fn); }
    async emit(name, event = {}) { for (const fn of this.events[name] || []) await fn({ target: this, ...event }); }
    appendChild(child) { if (child.tagName === 'fragment') child.children.forEach(node => this.appendChild(node)); else this.children.push(child); return child; }
    append(...children) { children.forEach(child => this.appendChild(child)); }
    replaceChildren(...children) { this.children = []; this.replaceCount++; this.append(...children); }
    setAttribute(name, value) { this.attributes[name] = String(value); }
    set innerHTML(value) { this.children = [...value.matchAll(/data-check-role="([^"]+)"/g)].map(match => { const child = new Element(); child.dataset.checkRole = match[1]; return child; }); }
    querySelectorAll(selector) { return selector === '[data-check-role]' ? this.children : []; }
    querySelector(selector) { if (selector === 'option[value="2"]') return this.option2 ||= new Element('option'); return null; }
    focus() { document.activeElement = this; }
    scrollIntoView() {}
    showModal() { this.open = true; }
    close() { this.open = false; void this.emit('close'); }
}
const ids = [...fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8').matchAll(/id="([^"]+)"/g)].map(match => match[1]);
const elements = new Map(ids.map(id => [id, new Element()]));
const el = name => elements.get(`check${name}`);
for (const [name, value] of Object.entries({ Filter: 'all', Route: 'default', Concurrency: '2', PageSize: '20' })) el(name).value = value;
const document = new Element('document');
document.hidden = false; document.getElementById = id => elements.get(id); document.createElement = tag => new Element(tag); document.createDocumentFragment = () => new Element('fragment');
const window = new Element('window');
let activeTab = 'login';
window.AuthTabs = { select(name) {
    if (activeTab === name) return;
    activeTab = name; void document.emit('auth-tab-changed', { detail: { name } });
} };
let accounts = Array.from({ length: 125 }, (_, i) => ({ id: i + 1, email: `account${String(i + 1).padStart(3, '0')}@example.test`, status: 'active' }));
let checks = Object.fromEntries(accounts.map(a => [a.id, { account_id: a.id, eligible: true, latest_task: null, last_result: null, cooldown_remaining_seconds: 0 }]));
let available = true, active = null, recent = [], recoveries = {}, requests = [], stored = new Map(), timers = new Map(), timerID = 0;
let historyGate = null, activeGate = null, createFailure = false, recoveryFailure = '';
const capabilities = { available: true, model: 'gpt-5.6-luna', max_batch_size: 100, max_concurrency: 2, default_proxy_configured: true, default_proxy_supported: true };
const batches = new Map();
const context = vm.createContext({ console, document, window, location: { hash: '' }, crypto: require('node:crypto').webcrypto, AbortController,
    sessionStorage: { getItem: k => stored.get(k), setItem: (k, v) => stored.set(k, v), removeItem: k => stored.delete(k) },
    setTimeout: (fn, delay) => { timers.set(++timerID, { fn, delay }); return timerID; }, clearTimeout: id => timers.delete(id),
    fetch: async (url, options = {}) => {
        requests.push({ url, options });
        let body;
        if (url === '/api/history') {
            body = { success: true, data: structuredClone(accounts), imports: accounts.map(a => ({ account_id: a.id, sub2_account_id: a.id + 1000, state: 'imported' })), checks: structuredClone(checks), recoveries: structuredClone(recoveries), imports_available: available, checks_available: available };
            if (historyGate) { const gate = historyGate; historyGate = null; await gate.promise; }
        } else if (url.endsWith('?active=1')) {
            body = { success: true, active, capabilities };
            if (activeGate) { const gate = activeGate; activeGate = null; await gate.promise; }
        }
        else if (url.endsWith('?limit=20')) body = { success: true, data: recent };
        else if (url === '/api/account-recovery' && options.method === 'POST') {
            if (recoveryFailure === 'rejected') {
                recoveryFailure = '';
                return { ok: false, status: 409, json: async () => ({ success: false, code: 'recovery_rejected', message: '当前无法提交，请稍后再试' }) };
            }
            if (recoveryFailure === 'network') { recoveryFailure = ''; throw new Error('simulated recovery connection loss'); }
            const id = JSON.parse(options.body).account_id;
            body = { success: true, recovery: { ...recoveries[id], state: 'queued', resumable: false } };
        }
        else if (url === '/api/account-checks' && options.method === 'POST') {
            if (createFailure === 'server') { createFailure = false; return { ok: false, status: 503, json: async () => ({ success: false, code: 'checks_unavailable', message: '服务暂不可用' }) }; }
            if (createFailure) { createFailure = false; throw new Error('simulated connection loss'); }
            const req = JSON.parse(options.body), items = req.account_ids.map((id, i) => ({ id: 100 + i, account_id: id, state: 'queued' }));
            active = { id: 'batch-fixture', state: 'active', total: items.length, concurrency: req.concurrency, model: capabilities.model, route_label: '服务器默认代理', counts: { total: items.length, queued: items.length } };
            batches.set(active.id, { success: true, batch: active, items }); recent = [active]; body = { success: true, batch_id: active.id, ...active };
        } else if (url.endsWith('/cancel')) {
            const prior = batches.get('batch-fixture'); active = null;
            const batch = { ...prior.batch, state: 'stopped', counts: { total: prior.items.length, canceled: prior.items.length, settled: prior.items.length }, stop_reason: 'user_cancel' };
            batches.set(batch.id, { success: true, batch, items: prior.items.map(i => ({ ...i, state: 'canceled' })) }); recent = [batch]; body = { success: true, batch };
        } else if (url.startsWith('/api/account-checks/')) body = batches.get(url.split('/').pop());
        else if (url.startsWith('/api/history/')) body = { success: true, checks_available: true, checks: [checks[url.split('/').pop()]?.last_result].filter(Boolean) };
        else throw new Error(`Unexpected fixture URL ${url}`);
        return { ok: true, status: 200, json: async () => structuredClone(body) };
    }
});
vm.runInContext(fs.readFileSync(path.join(__dirname, 'account-checks.js'), 'utf8'), context);
const flush = async () => { for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve)); };
const row = id => el('Grid').children.find(node => node.dataset.checkId === String(id));
const part = (id, role) => row(id).children.find(node => node.dataset.checkRole === role);
const click = async name => { await el(name).emit('click'); await flush(); };
const change = async (name, value) => { if (typeof value === 'boolean') el(name).checked = value; else el(name).value = value; await el(name).emit(name === 'Search' ? 'input' : 'change'); await flush(); };
(async () => {
    await flush();
    const tabPanel = elements.get('panel-checks');
    assert.ok(tabPanel, 'checks tab must provide its actual scroll container');
    const requestCount = url => requests.filter(request => request.url === url).length;
    const submissions = () => requests.filter(request => request.url === '/api/account-checks' && request.options.method === 'POST');
    const initialHistoryReads = requestCount('/api/history'), initialRecoveries = requestCount('/api/account-checks?active=1');
    window.AuthTabs.select('checks'); await flush();
    assert.equal(context.location.hash, '', 'tab refresh must not rely on a hash');
    assert.equal(requestCount('/api/history'), initialHistoryReads + 1);
    assert.equal(requestCount('/api/account-checks?active=1'), initialRecoveries + 1);
    window.AuthTabs.select('history'); await flush();
    assert.equal(requestCount('/api/account-checks?active=1'), initialRecoveries + 1, 'other tabs must not recover checks');
    await part(1, 'details').emit('click'); await flush();
    assert.equal(el('Details').open, true);
    assert.equal(document.activeElement, el('DetailsClose'), 'opening detection details focuses the close control');
    await click('DetailsClose');
    assert.equal(document.activeElement, part(1, 'details'), 'closing detection details restores the opener');
    assert.equal(el('Grid').children.length, 20);
    await change('SelectAll', true); assert.match(el('Selection').textContent, /已选 20 个/);
    tabPanel.scrollTop = 900;
    await click('Next');
    assert.equal(tabPanel.scrollTop, 0, 'pagination must reset the panel, not the non-scrolling grid');
    for (let id = 21; id <= 25; id++) { part(id, 'select').checked = true; await part(id, 'select').emit('change'); }
    assert.match(el('Selection').textContent, /已选 25 个/);
    assert.equal(el('SelectAll').indeterminate, true);
    await change('SelectAll', false); assert.match(el('Selection').textContent, /已选 20 个/);
    tabPanel.scrollTop = 900;
    await change('Search', 'account001'); assert.match(el('Selection').textContent, /已选 0 个/);
    assert.equal(tabPanel.scrollTop, 0, 'search must reset the panel scroll');
    await change('Search', ''); await change('PageSize', '100');
    for (let id = 1; id <= 90; id++) { part(id, 'select').checked = true; await part(id, 'select').emit('change'); }
    await click('Next'); await change('SelectAll', true);
    assert.match(el('Selection').textContent, /已选 90 个/); assert.match(el('Notice').textContent, /本次选择未生效/);
    await click('Clear'); await click('Prev'); await change('PageSize', '20');
    const originalRow = row(1), replacements = el('Grid').replaceCount;
    tabPanel.scrollTop = 420;
    await document.emit('auth-history-changed'); await flush();
    assert.equal(row(1), originalRow, 'summary refresh must retain row DOM'); assert.equal(el('Grid').replaceCount, replacements);
    assert.equal(tabPanel.scrollTop, 420, 'background history refresh must preserve scroll');
    checks[1].last_result = { id: 1, account_id: 1, state: 'finished', outcome: 'ok', freshness: 'current', finished_at: Date.now(), duration_ms: 25, model: capabilities.model, route_label: '服务器默认代理' };
    checks[1].latest_task = { id: 2, state: 'canceled' };
    await document.emit('auth-history-changed'); await flush();
    assert.match(part(1, 'result').textContent, /模型调用正常/); assert.match(part(1, 'task').textContent, /已取消/);
    checks[1].last_result.freshness = 'stale';
    await document.emit('auth-history-changed'); await flush();
    assert.match(part(1, 'result').textContent, /旧凭据/); assert.equal(part(1, 'result').className, 'history-status');
    checks[1].last_result = { id: 3, account_id: 1, state: 'finished', outcome: 'unauthorized', freshness: 'current' };
    await document.emit('auth-history-changed'); await flush();
    await change('Filter', 'other');
    assert.ok(row(1), 'an unauthorized result without optional error fields must remain visible in other exceptions');
    await change('Filter', 'all');
    checks[1].last_result = { id: 1, account_id: 1, state: 'finished', outcome: 'ok', freshness: 'current', finished_at: Date.now(), duration_ms: 25, model: capabilities.model, route_label: '服务器默认代理' };
    await document.emit('auth-history-changed'); await flush();
    available = false; await document.emit('auth-history-changed'); await flush();
    assert.equal(el('SelectAll').disabled, true); assert.equal(el('Start').disabled, true); assert.equal(row(1), originalRow);
    assert.match(part(1, 'result').textContent, /未更新/);
    available = true; await click('Refresh');
    part(1, 'select').checked = true; await part(1, 'select').emit('change');
    capabilities.default_proxy_configured = false; await click('Refresh');
    assert.equal(el('Start').disabled, true, 'missing default proxy must block default mode');
    await change('Route', 'direct'); assert.equal(el('Start').disabled, false, 'explicit sys1 direct remains usable');
    capabilities.default_proxy_configured = true; await click('Refresh'); await change('Route', 'default');
    await part(1, 'details').emit('click'); await flush();
    checks[1].last_result.freshness = 'current';
    await document.emit('auth-history-changed'); await flush();
    const allText = node => [node.textContent, ...node.children.map(allText)].join(' ');
    assert.doesNotMatch(allText(el('DetailsBody')), /旧凭据/);
    checks[1].last_result.freshness = 'stale';
    await document.emit('auth-history-changed'); await flush();
    assert.match(allText(el('DetailsBody')), /旧凭据/, 'open details must refresh credential freshness');
    await click('DetailsClose');
    window.AuthTabs.select('checks'); await flush();
    await part(1, 'details').emit('click'); await flush();
    assert.equal(el('Details').open, true);
    window.AuthTabs.select('history'); await flush();
    assert.equal(el('Details').open, false, 'leaving checks must close its modal and release the browser top layer');
    let release; historyGate = { promise: new Promise(resolve => { release = resolve; }) };
    const slow = document.emit('auth-history-changed'); await flush();
    checks[1].last_result.freshness = 'current';
    const newer = document.emit('auth-history-changed'); release(); await Promise.all([slow, newer]); await flush();
    assert.doesNotMatch(part(1, 'result').textContent, /旧凭据/, 'late old summary must not overwrite newer snapshot');
    part(1, 'select').checked = true; await part(1, 'select').emit('change');
    createFailure = true; await click('Start');
    assert.match(el('Start').textContent, /重试提交/);
    const first = requests.filter(r => r.url === '/api/account-checks').at(-1);
    available = false; await document.emit('auth-history-changed'); await flush();
    assert.equal(el('Start').disabled, true, 'unconfirmed submission must still obey unavailable data guard');
    available = true; await click('Refresh');
    const beforeDifferentAccount = requests.filter(r => r.url === '/api/account-checks').length;
    await window.AccountChecks.startSingle(2); await flush();
    assert.equal(requests.filter(r => r.url === '/api/account-checks').length, beforeDifferentAccount, 'another single account must not silently submit old request');
    assert.match(el('Notice').textContent, /原任务/);
    await click('Start');
    const second = requests.filter(r => r.url === '/api/account-checks').at(-1);
    assert.equal(JSON.parse(first.options.body).request_key, JSON.parse(second.options.body).request_key, 'uncertain create must reuse key');
    assert.equal(stored.size, 0);
    await click('Refresh');
    assert.match(el('BatchTitle').textContent, /检测进行中/);
    assert.equal(requests.filter(r => r.url === '/api/account-checks').length, 2, 'task recovery must not create another task');
    await click('Stop');
    assert.match(el('BatchTitle').textContent, /已停止.*1\/1/);
    assert.match(el('BatchCounts').textContent, /已检测 0.*取消 1/);
    assert.equal(el('ProgressBar').value, 100);
    assert.match(part(1, 'result').textContent, /模型调用正常/, 'cancel must preserve previous completed outcome');
    assert.equal(requests.filter(r => r.options.method === 'POST' && r.url === '/api/account-checks').length, 2);
    part(2, 'select').checked = true; await part(2, 'select').emit('change');
    createFailure = 'server'; await click('Start');
    assert.match(el('Start').textContent, /重试提交/, '503 must preserve uncertain create key');
    const failed503 = JSON.parse(requests.filter(r => r.url === '/api/account-checks').at(-1).options.body);
    await click('Start');
    assert.equal(JSON.parse(requests.filter(r => r.url === '/api/account-checks').at(-1).options.body).request_key, failed503.request_key);
    await click('Stop');

    window.AuthTabs.select('history'); await flush();
    let releaseRecovery;
    activeGate = { promise: new Promise(resolve => { releaseRecovery = resolve; }) };
    const beforeSingle = submissions().length, beforeHistory = requestCount('/api/history'), beforeRecovery = requestCount('/api/account-checks?active=1');
    const single = window.AccountChecks.startSingle(3);
    const concurrentRefresh = el('Refresh').emit('click');
    await flush();
    assert.equal(activeTab, 'checks');
    assert.equal(submissions().length, beforeSingle, 'single check must await slow task recovery');
    assert.equal(requestCount('/api/history'), beforeHistory + 1, 'tab activation and single check must share their refresh');
    assert.equal(requestCount('/api/account-checks?active=1'), beforeRecovery + 1, 'concurrent refresh must share task recovery');
    assert.equal(el('Refresh').disabled, true);
    releaseRecovery(); await Promise.all([single, concurrentRefresh]); await flush();
    assert.equal(submissions().length, beforeSingle + 1);
    assert.deepEqual(JSON.parse(submissions().at(-1).options.body).account_ids, [3]);
    assert.match(el('Notice').textContent, /检测已开始/);
    await click('Stop');

    const remoteBatch = { id: 'remote-batch', state: 'active', total: 2, counts: { total: 2, queued: 2 } };
    active = remoteBatch; recent = [remoteBatch];
    batches.set(remoteBatch.id, { success: true, batch: remoteBatch, items: [{ id: 500, account_id: 4, state: 'queued' }, { id: 501, account_id: 5, state: 'queued' }] });
    window.AuthTabs.select('history'); await flush();
    activeGate = { promise: new Promise(resolve => { releaseRecovery = resolve; }) };
    const beforeRecovered = submissions().length;
    const recoveredSingle = window.AccountChecks.startSingle(4); await flush();
    assert.equal(submissions().length, beforeRecovered);
    releaseRecovery(); await recoveredSingle; await flush();
    assert.equal(submissions().length, beforeRecovered, 'recovering an external active batch must not submit another task');
    assert.match(el('BatchTitle').textContent, /检测进行中/);
    assert.match(el('Notice').textContent, /已有检测批次正在执行/);

    const remote = batches.get(remoteBatch.id);
    remote.items[0] = { ...remote.items[0], state: 'finished', outcome: 'forbidden', freshness: 'current' };
    remote.batch.counts = { total: 2, queued: 1, finished: 1, abnormal: 1, settled: 1 };
    const badgeHistoryReads = requestCount('/api/history');
    tabPanel.scrollTop = 420;
    await [...timers.values()].find(timer => timer.fn.name === 'poll').fn();
    assert.equal(requestCount('/api/history'), badgeHistoryReads, 'batch badge check must run before the periodic history refresh');
    assert.equal(elements.get('tabBadgeChecks').textContent, '1', 'a completed abnormal batch item must update the badge immediately');
    assert.equal(tabPanel.scrollTop, 420, 'batch polling must preserve scroll');
    active = { state: 'active' };
    await click('Refresh');
    assert.match(el('Notice').textContent, /任务编号无效/, 'malformed active task responses must fail clearly');

    // A completed recovery belongs to the detection result that created it.
    // A later 401 must be recoverable again, while the matching result stays disabled.
    active = null; available = true;
    checks[1].last_result = { id: 901, account_id: 1, state: 'finished', outcome: 'unauthorized', http_status: 401, freshness: 'current' };
    recoveries = { '1': { state: 'completed', check_id: 900, last_error: '旧结果已恢复' } };
    await document.emit('auth-history-changed'); await flush();
    assert.equal(part(1, 'recover').disabled, false, 'completed recovery for an older check must not block a new 401');
    assert.equal(part(1, 'recover').textContent, '恢复并开启调度');
    assert.match(part(1, 'recoveryStatus').textContent, /旧结果已恢复/, 'last_error must be surfaced in recovery status');
    recoveries = { '1': { state: 'completed', check_id: 901, last_error: '当前结果已恢复' } };
    await document.emit('auth-history-changed'); await flush();
    assert.equal(part(1, 'recover').disabled, true, 'matching completed recovery must remain disabled');
    assert.equal(part(1, 'recover').textContent, '已恢复');
    const recoverySubmissions = () => requests.filter(request => request.url === '/api/account-recovery' && request.options.method === 'POST');
    const beforeCompleted = recoverySubmissions().length;
    await window.AccountChecks.recoverAccount(1); await flush();
    assert.equal(recoverySubmissions().length, beforeCompleted, 'direct recovery entry must honor completed-check guard');
    recoveries[1] = { state: 'completed', credential_attempt_id: 44 };
    checks[1].last_result.credential_attempt_id = 44;
    await document.emit('auth-history-changed'); await flush();
    assert.equal(part(1, 'recover').disabled, false, 'credential version alone must not link completed recovery to a detection');

    for (const state of ['failed', 'unknown']) {
        for (const result of [{ outcome: 'unauthorized', http_status: 401, freshness: 'stale' }, { outcome: 'ok', freshness: 'current' }]) {
            checks[1].last_result = { id: 902, account_id: 1, state: 'finished', ...result };
            recoveries = { '1': { id: 10, account_id: 1, state, check_id: 901, resumable: true, last_error: '凭据已取得，待继续' } };
            await document.emit('auth-history-changed'); await flush();
            assert.equal(part(1, 'recover').hidden, false, `${state} checkpoint must remain visible for ${result.outcome}/${result.freshness}`);
            assert.equal(part(1, 'recover').disabled, false);
            assert.equal(part(1, 'recover').textContent, '继续恢复');
        }
    }
    const savedRecoveries = recoveries;
    recoveries = undefined;
    await document.emit('auth-history-changed'); await flush();
    assert.equal(part(1, 'recover').hidden, false, 'omitted recovery snapshot must preserve the resumable task even when detection is normal');
    assert.equal(part(1, 'recover').textContent, '继续恢复');
    recoveries = {};
    await document.emit('auth-history-changed'); await flush();
    assert.equal(part(1, 'recover').hidden, true, 'an explicit empty recovery snapshot clears the historical task');
    recoveries = savedRecoveries;
    await document.emit('auth-history-changed'); await flush();
    recoveryFailure = 'rejected';
    await part(1, 'recover').emit('click'); await flush();
    assert.deepEqual(JSON.parse(recoverySubmissions().at(-1).options.body), { account_id: 1 }, 'resume must submit through the existing recovery endpoint');
    assert.equal(part(1, 'recover').hidden, false, 'a rejected resume must retain checkpoint metadata');
    assert.equal(part(1, 'recover').disabled, false);
    assert.equal(part(1, 'recover').textContent, '继续恢复');
    assert.match(part(1, 'recoveryStatus').textContent, /当前无法提交/);

    recoveryFailure = 'network';
    await part(1, 'recover').emit('click'); await flush();
    assert.equal(part(1, 'recover').hidden, false, 'uncertain resume submission must keep task progress visible');
    assert.equal(part(1, 'recover').disabled, true);
    assert.equal(part(1, 'recover').textContent, '恢复中…');
    const beforePending = recoverySubmissions().length;
    await window.AccountChecks.recoverAccount(1); await flush();
    assert.equal(recoverySubmissions().length, beforePending, 'uncertain submission must not create a second request');
    await document.emit('auth-history-changed'); await flush();
    await part(1, 'recover').emit('click'); await flush();
    assert.equal(part(1, 'recover').hidden, false, 'queued resume must remain visible after a normal detection result');
    assert.equal(part(1, 'recover').disabled, true);
    assert.equal(part(1, 'recover').textContent, '恢复中…');

    recoveries[1] = { ...recoveries[1], state: 'failed', resumable: false };
    await document.emit('auth-history-changed'); await flush();
    assert.equal(part(1, 'recover').hidden, true, 'failed task without checkpoint cannot resume a normal detection result');
    checks[1].last_result = { id: 903, account_id: 1, state: 'finished', outcome: 'unauthorized', http_status: 401, freshness: 'current' };
    await document.emit('auth-history-changed'); await flush();
    assert.equal(part(1, 'recover').hidden, false, 'failed task without checkpoint can retry a current 401');
    assert.equal(part(1, 'recover').disabled, false);
    assert.equal(part(1, 'recover').textContent, '恢复并开启调度');
    available = false;
    await document.emit('auth-history-changed'); await flush();
    const beforeUnavailable = recoverySubmissions().length;
    await window.AccountChecks.recoverAccount(1); await flush();
    assert.equal(recoverySubmissions().length, beforeUnavailable, 'direct recovery entry must honor unavailable history');
    console.log('PASS: pagination, bounded selection, stable DOM, independent results, freshness, failed snapshots, proxy/direct, idempotent submit, recovery, cancel counts, tab entry, coalesced single-check recovery, panel scroll, live badge, modal cleanup, repeated recovery, checkpoint resume, uncertain resume submission');
})().catch(error => { console.error(error); process.exitCode = 1; });
