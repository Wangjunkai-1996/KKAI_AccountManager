// Local-only visual fixture: node cmd/server/history-ui-fixture.cjs [port]
// Every API response is synthetic; no production credentials or network calls.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const port = Number(process.argv[2] || 18089);
const now = Date.now();
const at = hours => new Date(now - hours * 3600000).toISOString();
const rows = [
    { id: 203, email: 'recovery.review@example.test', status: 'error', plan_type: 'self_serve_business_prolite', attempt_count: 4, recovery_count: 1, recovery_attempt_count: 3, last_error: '新登录账号身份与原账号不一致', last_error_code: 'identity_mismatch' },
    { id: 204, email: 'healthy.account@example.test', status: 'active', plan_type: 'plus', attempt_count: 7, recovery_count: 3, recovery_attempt_count: 3 },
    { id: 205, email: 'removed.from.pool@example.test', status: 'active', plan_type: 'team', attempt_count: 1, recovery_count: 0, recovery_attempt_count: 0 },
    { id: 206, email: 'unconfirmed.and.very.long.account.address.for.mobile@example.test', status: 'interrupted', plan_type: 'free', attempt_count: 2, recovery_count: 0, recovery_attempt_count: 1 },
    { id: 207, email: 'records.error@example.test', status: 'active', plan_type: 'plus', attempt_count: 2, recovery_count: 0, recovery_attempt_count: 1 },
    { id: 208, email: 'many.records@example.test', status: 'active', plan_type: 'business', attempt_count: 140, recovery_count: 85, recovery_attempt_count: 130 }
].map(row => ({ ...row, last_success_at: at(5), last_attempt_at: at(.1), updated_at: at(.1), expires_at: Math.floor(now / 1000) + 864000 }));
const sub2 = {
    203: { imported: true, exists: true, sub2_account_id: 16839, status: 'error', schedulable: false, checked_at: at(.05), error_message: 'Token revoked (401): Encountered invalidated oauth token for user, failing request' },
    204: { imported: true, exists: true, sub2_account_id: 16840, status: 'active', schedulable: true, checked_at: at(.05) },
    205: { imported: true, exists: false, status: 'deleted', checked_at: at(.05) },
    206: { imported: true, exists: true, unknown: true, status: 'unknown', error: 'Sub2 状态读取暂时失败', checked_at: at(.5) },
    207: { imported: true, exists: true, sub2_account_id: 16843, status: 'active', schedulable: true },
    208: { imported: true, exists: true, sub2_account_id: 16844, status: 'active', schedulable: true }
};
const history = {
    accounts: rows, imports: rows.filter(row => row.id !== 205).map(row => ({ account_id: row.id, state: 'imported' })),
    sub2_configured: true, imports_available: true, sub2_statuses: sub2, checks_available: true,
    checks: { 203: { eligible: true, last_result: { outcome: 'credential_revoked', http_status: 401, freshness: 'current', finished_at: at(.1) } } },
    recoveries: { 203: { id: 53, state: 'unknown', updated_at: at(.1), last_error: '账号绑定需要核对', requires_action: true, manual_action: '请核对账号绑定后继续恢复', resumable: true } },
    rechecks: { 204: { task_id: 53, account_id: 204, state: 'pending', round: 2, next_check_at: new Date(now + 600000).toISOString() } },
    deliveries: {
        204: { id: 70, account_id: 204, state: 'completed', updated_at: at(.1) },
        206: { id: 71, account_id: 206, state: 'requires_action', requires_action: true, manual_action: '请核对 Sub2 账号绑定后继续交付', updated_at: at(.1) },
        208: { id: 72, account_id: 208, state: 'retry_wait', retry_action: 'resume', next_retry_at: new Date(now + 60000).toISOString(), last_error: 'Sub2 暂时不可用，稍后自动继续', updated_at: at(.1) }
    }
};
http.createServer((req, res) => {
    const url = new URL(req.url, `http://127.0.0.1:${port}`);
    const send = (data, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' }); res.end(JSON.stringify(data)); };
    if (req.method !== 'GET') return send({ message: '本地视觉预览：操作不会提交，所有数据为演示数据' }, 409);
    const assets = { '/': 'index.html', '/api/client.js': 'client.js', '/api/account-checks.js': 'account-checks.js' };
    if (assets[url.pathname]) {
        res.writeHead(200, { 'Content-Type': url.pathname === '/' ? 'text/html; charset=utf-8' : 'text/javascript; charset=utf-8' });
        return res.end(fs.readFileSync(path.join(__dirname, assets[url.pathname])));
    }
    if (url.pathname === '/api/sub2/import-settings') return send({ success: true, defaults: { group_ids: [10], priority: 1, concurrency: 3 }, groups: [{ id: 10, name: '标准账号池' }, { id: 20, name: '高优先级账号池' }, { id: 30, name: '这个分组名称很长，用于检查手机上的换行效果' }], groups_available: true });
    if (url.pathname === '/health') return send({ max_concurrent: 10, sub2_configured: true });
    if (url.pathname === '/api/history') return send(history);
    if (url.pathname === '/api/account-recovery/settings') return send({ auto_recovery_enabled: true, running: true, interval_seconds: 60, last_scan_at: at(.02), next_scan_at: new Date(now + 60000).toISOString() });
    if (url.pathname === '/api/account-checks') return send({ batches: [] });
    if (url.pathname.startsWith('/api/history/')) {
        const id = Number(url.pathname.split('/').pop()), account = rows.find(row => row.id === id);
        if (!account) return send({ message: '账号不存在' }, 404);
        if (id === 207) return setTimeout(() => send({ message: '演示：复活记录读取失败，请重试' }, 503), 400);
        const count = id === 208 ? 100 : account.recovery_attempt_count;
        const records = Array.from({ length: count }, (_, index) => ({
            id: 130 - index, account_id: id, state: id === 203 && index === 0 ? 'unknown' : index % 3 === 0 ? 'failed' : 'completed',
            created_at: at(index * 24 + 1), updated_at: at(index * 24 + .5),
            last_error: index % 3 === 0 ? (index === 0 ? '认证恢复失败，Sub2 保持停止调度。' : '检测结果待核对：' + '较长的安全错误摘要，用来检查窄屏自动换行。'.repeat(6)) : ''
        }));
        return setTimeout(() => send({ account, attempts: [], recovery_history: records, recovery_history_available: true, recovery_history_truncated: id === 208 }), 650);
    }
    send({ message: 'fixture route not found' }, 404);
}).listen(port, '127.0.0.1', () => console.log(`History UI fixture: http://127.0.0.1:${port}/#panel-history`));
