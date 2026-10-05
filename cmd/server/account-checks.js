(() => {
    'use strict';
    const names = ['Panel', 'Refresh', 'Search', 'Filter', 'Route', 'Concurrency', 'Model', 'SelectAll', 'Selection', 'Clear', 'Start', 'Notice', 'Progress', 'BatchTitle', 'Stop', 'Retry', 'ProgressBar', 'BatchCounts', 'BatchRoute', 'ListStatus', 'ApplyFilter', 'Grid', 'PageInfo', 'PageSize', 'Prev', 'Next', 'Details', 'DetailsTitle', 'DetailsClose', 'DetailsStatus', 'DetailsBody'];
    const ui = Object.fromEntries(names.map(name => [name, document.getElementById(name === 'Panel' ? 'accountChecksPanel' : `check${name}`)]));
    if (!ui.Panel) return;
    const tabPanel = document.getElementById('panel-checks'), tabBadge = document.getElementById('tabBadgeChecks');
    const outcomes = {
        ok: '模型调用正常', credential_missing: '缺少访问令牌', credential_incomplete: '凭据信息不完整',
        access_token_expired: '访问令牌已过期，待更新', unauthorized: '当前凭据鉴权失败', credential_revoked: '当前凭据已失效',
        account_disabled: '上游报告账号停用', account_deleted: '上游报告账号删除', account_unavailable: '上游报告账号不可用',
        upstream_challenge: '上游安全验证', forbidden: '请求被拒绝，原因待确认', region_restricted: '上游报告地区限制',
        rate_limited: '当前限流', quota_exhausted: '当前额度不足', model_unavailable: '模型不存在或无权限',
        request_rejected: '检测请求不兼容', upstream_error: '上游暂时异常', proxy_error: '代理错误', network_error: '网络错误',
        timeout: '检测超时', incomplete: '调用未完整完成', protocol_error: '响应协议异常'
    };
    const taskNames = { queued: '排队', running: '正在检测', finished: '检测结束', skipped: '已跳过', canceled: '已取消', interrupted: '上次检测中断' };
    const skipNames = { account_removed: '账号已删除', no_longer_imported: '不再符合导入条件', login_in_progress: '账号正在登录', cooldown: '检测冷却中' };
    const credentialOutcomes = ['credential_missing', 'credential_incomplete', 'access_token_expired', 'token_expired', 'token_revoked', 'credential_revoked', 'auth_failed', 'authentication_failed'];
    const networkOutcomes = ['network_error', 'proxy_error', 'timeout'];
    const permissionOutcomes = ['forbidden', 'region_restricted', 'model_unavailable'];
    const selected = new Set(), rows = new Map(), summaries = new Map(), recoveryStates = new Map();
    let accounts = new Map(), filteredIDs = [], page = 1, pageSize = 20, available = false;
    let capabilities = {}, batch = null, batchItems = [], activeID = '', submitting = false, stopping = false, recovering = false;
    let historyRequest = null, refreshRequest = null, historyGeneration = 0, historyAgain = false, historyReapply = false, summaryUpdatedAt = 0;
    let batchReadGeneration = 0, credentialGeneration = 0;
    let polling = false, timer = null, cooldownTimer = null, detailID = '', detailGeneration = 0, detailRecords = [], detailSignature = '', detailTrigger = null;
    const recoveryTimers = new Map();
    let pendingRequest = null;
    try { pendingRequest = JSON.parse(sessionStorage.getItem('auth-check-pending') || 'null'); } catch (_) { /* Storage may be unavailable. */ }
    if (!pendingRequest || !Array.isArray(pendingRequest.account_ids) || typeof pendingRequest.request_key !== 'string') pendingRequest = null;

    function put(node, value) { const text = String(value ?? ''); if (node.textContent !== text) node.textContent = text; }
    function notice(text) { put(ui.Notice, text); }
    function isActive(value = batch) { return value && ['active', 'stopping'].includes(value.state); }
    function maxBatch() { return Math.min(100, Number(capabilities.max_batch_size) || 100); }
    function time(value) {
        if (!value) return '暂无';
        const date = new Date(typeof value === 'number' ? value : /^\d+$/.test(value) ? Number(value) : value);
        return Number.isNaN(date.getTime()) ? '未知时间' : date.toLocaleString('zh-CN', { hour12: false });
    }
    function resultName(item) { return item ? outcomes[item.outcome] || '检测结果未知' : '尚无完成的检测'; }
    function freshnessName(item) {
        if (item?.freshness === 'account_removed') return '账号已删除';
        if (item?.freshness === 'stale') return item.credential_attempt_id === 0 || item.credential_attempt_id === null
            ? '凭据版本待确认，需重新检测' : '旧凭据，待重新检测';
        return '本次持有凭据的历史观察';
    }
    function remaining(summary) { return Math.max(0, Math.ceil(((summary?._cooldownUntil || 0) - Date.now()) / 1000)); }
    function eligible(account) {
        const summary = summaries.get(String(account?.id));
        return Boolean(account && summary?.eligible && account.status !== 'running' && !['queued', 'running'].includes(summary.latest_task?.state));
    }
    function canResumeRecovery(record) {
        return record?.resumable === true && ['failed', 'unknown'].includes(record.state);
    }
    function canRecover(account, result, recovery) {
        if (!account) return false;
        if (canResumeRecovery(recovery)) return true;
        if (!result || result.freshness === 'stale') return false;
        return Number(result.http_status) === 401 || credentialOutcomes.includes(result.outcome) ||
            result.outcome === 'unauthorized' || (result.stream_error_code &&
                ['unauthorized', 'access_token_expired', 'credential_revoked'].includes(result.outcome));
    }
    const recoveryNames = {
        queued: '等待恢复', validating: '正在核对检测结果', disabling_schedule: '正在暂停调度',
        logging_in: '正在重新登录', login_succeeded: '登录成功，正在写回',
        identity_verified: '身份已核对', refreshing_credentials: '正在刷新凭据', credentials_applied: '凭据已写回',
        applying_credentials: '正在写回凭据',
        enabling_schedule: '正在开启调度', running: '正在恢复', active: '正在恢复', pending: '等待恢复',
        in_progress: '正在恢复', completed: '恢复完成', success: '恢复完成', failed: '恢复失败',
        unknown: '待核对', canceled: '已取消'
    };
    const recoveryActiveStates = new Set(['queued', 'validating', 'logging_in', 'login_succeeded', 'identity_verified',
        'refreshing_credentials', 'credentials_applied', 'applying_credentials', 'disabling_schedule',
        'enabling_schedule', 'running', 'active', 'pending', 'in_progress']);
    const recoveryTerminalStates = new Set(['completed', 'success', 'failed', 'unknown', 'canceled']);
    function recoveryRecord(payload) {
        const record = payload?.recovery || payload?.task || payload?.data || payload || {};
        const state = String(record.state || record.status || payload?.state || '').toLowerCase();
        return { ...record, state: state || 'unknown', message: record.message || record.error || record.last_error || payload?.message || '' };
    }
    function recoveryLabel(record) {
        if (!record) return '';
        return record.message ? `${recoveryNames[record.state] || record.state}：${record.message}` : recoveryNames[record.state] || record.state;
    }
    function setRecovery(id, record) {
        recoveryStates.set(String(id), { ...record, updated_at: Date.now() });
    }
    function recoveryDone(id, record) {
        const state = String(record?.state || '').toLowerCase();
        return recoveryTerminalStates.has(state);
    }
    function recoveryMatchesResult(recovery, result) {
        if (!recovery || !result) return false;
        const checkID = Number(recovery.check_id);
        return Number.isFinite(checkID) && checkID > 0 && checkID === Number(result.id);
    }
    function canStartRecovery(account, result, recovery) {
        return available && canRecover(account, result, recovery) && !recoveryActiveStates.has(recovery?.state) &&
            !(['completed', 'success'].includes(recovery?.state) && recoveryMatchesResult(recovery, result));
    }
    function scheduleRecoveryPoll(id, delay = 1500) {
        const key = String(id);
        clearTimeout(recoveryTimers.get(key));
        recoveryTimers.set(key, setTimeout(() => { recoveryTimers.delete(key); void pollRecovery(key); }, delay));
    }
    async function pollRecovery(id) {
        const key = String(id), current = recoveryStates.get(key);
        if (!current || !recoveryActiveStates.has(String(current.state))) return;
        try {
            const payload = await api(`/api/account-recovery?account_id=${encodeURIComponent(key)}`);
            const record = recoveryRecord(payload); setRecovery(key, record); render();
            if (recoveryDone(key, record)) {
                if (['completed', 'success'].includes(record.state)) {
                    notice(`${accounts.get(key)?.email || '账号'} 恢复完成，正在刷新检测结果。`);
                    document.dispatchEvent(new CustomEvent('auth-history-changed'));
                    await refreshAccounts({ reapply: true, invalidate: true });
                } else notice(`${accounts.get(key)?.email || '账号'}：${recoveryLabel(record)}`);
                return;
            }
            scheduleRecoveryPoll(key);
        } catch (error) {
            setRecovery(key, { ...current, state: 'unknown', message: `状态查询失败：${error.message || '请刷新后核对'}` });
            render();
        }
    }
    async function startRecovery(id) {
        const key = String(id), account = accounts.get(key), result = summaries.get(key)?.last_result;
        const current = recoveryStates.get(key);
        if (!canStartRecovery(account, result, current)) return;
        setRecovery(key, { ...current, state: 'queued', message: '正在提交恢复任务…' }); render();
        try {
            const payload = await api('/api/account-recovery', { method: 'POST', body: JSON.stringify({ account_id: Number(key) }), timeoutMs: 120000 });
            const record = recoveryRecord(payload); setRecovery(key, record); render();
            if (recoveryDone(key, record)) {
                if (['completed', 'success'].includes(record.state)) {
                    document.dispatchEvent(new CustomEvent('auth-history-changed'));
                    await refreshAccounts({ reapply: true, invalidate: true });
                } else notice(`${account.email || '账号'}：${recoveryLabel(record)}`);
            } else scheduleRecoveryPoll(key, 500);
        } catch (error) {
            if (error.payload?.code === 'recovery_busy') {
                setRecovery(key, { ...current, state: 'pending', message: '已有恢复任务，正在查询结果…' });
                notice(`${account.email || '账号'}：已有恢复任务，正在查询。`); render(); scheduleRecoveryPoll(key, 500); return;
            }
            if (!error.definitive) {
                setRecovery(key, { ...current, state: 'pending', message: '提交结果未确认，正在查询恢复结果…' });
                notice(`${account.email || '账号'}：恢复结果暂未确认，正在查询。`); render(); scheduleRecoveryPoll(key, 1000); return;
            }
            setRecovery(key, { ...current, state: 'failed', message: error.message || '恢复任务提交失败' });
            notice(`${account.email || '账号'}：${error.message || '恢复任务提交失败'}`); render();
        }
    }
    function canSubmit() {
        return available && capabilities.available === true && !activeID && !submitting && !recovering &&
            (ui.Route.value === 'direct' || capabilities.default_proxy_configured === true && capabilities.default_proxy_supported === true);
    }
    function matches(account) {
        if (!String(account.email || '').toLowerCase().includes(ui.Search.value.trim().toLowerCase())) return false;
        const summary = summaries.get(String(account.id)), result = summary?.last_result, filter = ui.Filter.value;
        if (filter === 'all') return true;
        if (!available) return false;
        if (filter === 'never') return !result;
        if (!result) return false;
        if (filter === 'stale') return result.freshness === 'stale';
        if (filter === 'ok') return result.outcome === 'ok' && result.freshness !== 'stale';
        if (filter === '401') return Number(result.http_status) === 401;
        if (filter === 'stream_auth') return Boolean(result.stream_error_code) && ['unauthorized', 'access_token_expired', 'credential_revoked'].includes(result.outcome);
        if (filter === 'credential') return credentialOutcomes.includes(result.outcome);
        if (filter === 'limit') return ['rate_limited', 'quota_exhausted'].includes(result.outcome);
        if (filter === 'permission') return permissionOutcomes.includes(result.outcome);
        if (filter === 'network') return networkOutcomes.includes(result.outcome);
        return result.outcome !== 'ok' && !credentialOutcomes.includes(result.outcome) && !networkOutcomes.includes(result.outcome) &&
            !permissionOutcomes.includes(result.outcome) && !['rate_limited', 'quota_exhausted'].includes(result.outcome);
    }
    function pageIDs() { return filteredIDs.slice((page - 1) * pageSize, page * pageSize); }
    function savePending(request) {
        pendingRequest = request;
        try { if (request) sessionStorage.setItem('auth-check-pending', JSON.stringify(request)); else sessionStorage.removeItem('auth-check-pending'); } catch (_) { /* Server remains the task source. */ }
    }
    async function api(path, options = {}) {
        const { timeoutMs = 15000, ...requestOptions } = options;
        const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), timeoutMs);
        try {
            const response = await fetch(path, { ...requestOptions, cache: 'no-store', signal: controller.signal,
                headers: { Accept: 'application/json', ...(requestOptions.body ? { 'Content-Type': 'application/json' } : {}), ...requestOptions.headers } });
            const payload = await response.json().catch(() => ({}));
            if (!response.ok || payload.success === false) {
                const error = new Error(payload.message || `请求未完成（HTTP ${response.status}）`);
                error.payload = payload;
                // A gateway/server error may occur after the transaction commits.
                // Only an explicit client rejection can discard a submission key.
                error.definitive = response.status >= 400 && response.status < 500 && response.status !== 408 &&
                    payload.success === false && typeof payload.code === 'string';
                throw error;
            }
            if (!payload || typeof payload !== 'object' || payload.success !== true) throw new Error('检测服务响应格式异常');
            return payload;
        } finally { clearTimeout(timeout); }
    }
    function createRow(id) {
        const node = document.createElement('article'); node.className = 'history-item'; node.dataset.checkId = id;
        node.innerHTML = '<label class="history-select"><input type="checkbox" data-check-role="select"></label><div class="history-main"><div class="history-email" data-check-role="email"></div><div class="history-meta"><span data-check-role="login"></span><span class="history-status" data-check-role="result"></span></div><div class="history-meta" data-check-role="time"></div><div class="history-error" data-check-role="task"></div><div class="history-error" data-check-role="recoveryStatus"></div></div><div class="history-actions"><button class="btn-secondary" type="button" data-check-role="start">检测状态</button><button class="btn-secondary" type="button" data-check-role="details">检测详情</button><button class="btn-secondary" type="button" data-check-role="recover">恢复账号</button></div>';
        const parts = { node };
        node.querySelectorAll('[data-check-role]').forEach(child => { parts[child.dataset.checkRole] = child; });
        parts.select.addEventListener('change', () => changeSelection([id], parts.select.checked));
        parts.start.addEventListener('click', () => startSingle(id));
        parts.details.addEventListener('click', () => openDetails(id, parts.details));
        parts.recover.addEventListener('click', () => startRecovery(id));
        rows.set(id, parts); return parts;
    }
    function patchRow(id, parts) {
        const account = accounts.get(id), summary = summaries.get(id), result = summary?.last_result, task = summary?.latest_task;
        const recovery = recoveryStates.get(id), recoverable = canRecover(account, result, recovery);
        put(parts.email, account?.email || '账号已删除');
        put(parts.login, account ? `登录：${typeof historyStatusName === 'function' ? historyStatusName(account.status) : account.status || '暂无状态'}` : '本地账号已删除');
        put(parts.result, !available ? '检测记录未更新' : result ? `上次检测：${resultName(result)}${result.freshness === 'stale' ? `（${freshnessName(result)}）` : ''}` : '尚无完成的检测');
        parts.result.className = `history-status${available && result?.freshness === 'current' ? result.outcome === 'ok' ? ' success' : ' error' : ''}`;
        put(parts.time, result ? `${time(result.finished_at || result.checked_at)} · ${(Number(result.duration_ms || 0) / 1000).toFixed(1)} 秒 · ${result.route_label || '出口未记录'} · ${result.model || capabilities.model || ''}` : 'AUTH 记录已导入');
        const cooldown = remaining(summary);
        put(parts.task, task && task.state !== 'finished' ? `${taskNames[task.state] || task.state}${task.skip_reason ? `：${skipNames[task.skip_reason] || '当前不可执行'}` : ''}${cooldown ? ` · ${cooldown} 秒后可重测` : ''}` : cooldown ? `${cooldown} 秒后可重新检测` : '');
        put(parts.recoveryStatus, recovery ? recoveryLabel(recovery) : '');
        parts.recoveryStatus.hidden = !recovery;
        const recoveryState = String(recovery?.state || '').toLowerCase();
        const recoveryActive = recoveryActiveStates.has(recoveryState);
        parts.recover.hidden = !recoverable && !recoveryActive;
        const recoveryCurrent = ['completed', 'success'].includes(recoveryState) && recoveryMatchesResult(recovery, result);
        parts.recover.disabled = !canStartRecovery(account, result, recovery);
        put(parts.recover, recoveryActive ? '恢复中…' :
            recoveryCurrent ? '已恢复' :
                canResumeRecovery(recovery) ? '继续恢复' : '恢复并开启调度');
        parts.select.checked = selected.has(id);
        parts.select.disabled = !available || !eligible(account) || submitting || Boolean(pendingRequest);
        parts.select.setAttribute('aria-label', `选择检测 ${account?.email || '已删除账号'}`);
        put(parts.start, activeID ? '查看当前检测' : cooldown ? `${cooldown} 秒后可重测` : '检测状态');
        parts.start.disabled = !activeID && (!canSubmit() || !eligible(account) || cooldown > 0 || Boolean(pendingRequest));
        parts.details.disabled = !account;
    }
    function render(reapply = false) {
        if (reapply) {
            filteredIDs = [...accounts.values()].filter(matches).map(account => String(account.id));
            ui.ApplyFilter.hidden = true;
        }
        const count = Math.max(1, Math.ceil(filteredIDs.length / pageSize)); page = Math.min(Math.max(1, page), count);
        const ids = pageIDs(), signature = JSON.stringify(ids);
        if (ui.Grid.dataset.signature !== signature) {
            const fragment = document.createDocumentFragment();
            ids.forEach(id => fragment.appendChild((rows.get(id) || createRow(id)).node));
            if (!ids.length) { const empty = document.createElement('p'); empty.className = 'empty-state'; put(empty, available ? '没有符合条件的已导入账号' : '账号列表暂不可用，请刷新重试'); fragment.appendChild(empty); }
            ui.Grid.replaceChildren(fragment); ui.Grid.dataset.signature = signature;
        }
        ids.forEach(id => patchRow(id, rows.get(id)));
        const selectable = ids.filter(id => eligible(accounts.get(id))), onPage = selectable.filter(id => selected.has(id)).length;
        ui.SelectAll.checked = selectable.length > 0 && onPage === selectable.length;
        ui.SelectAll.indeterminate = onPage > 0 && onPage < selectable.length;
        ui.SelectAll.disabled = !available || selectable.length === 0 || submitting || Boolean(pendingRequest);
        put(ui.Selection, pendingRequest
            ? `待确认提交 ${pendingRequest.account_ids.length} 个 · ${pendingRequest.proxy_mode === 'direct' ? 'sys1 IPv4 直连' : '服务器默认代理'} · 并发 ${pendingRequest.concurrency}`
            : `已选 ${selected.size} 个，本页 ${ids.filter(id => selected.has(id)).length} 个，其他页 ${[...selected].filter(id => !ids.includes(id)).length} 个`);
        ui.Clear.disabled = selected.size === 0 || submitting || Boolean(pendingRequest);
        ui.Start.disabled = pendingRequest ? !available || capabilities.available !== true || submitting || recovering || Boolean(activeID) : !canSubmit() || selected.size === 0;
        put(ui.Start, submitting ? '正在提交…' : pendingRequest ? '重试提交（同一任务）' : `开始检测${selected.size ? ` ${selected.size} 个` : ''}`);
        ui.Route.disabled = submitting || Boolean(activeID) || Boolean(pendingRequest);
        ui.Concurrency.disabled = ui.Route.disabled;
        put(ui.PageInfo, `第 ${page} / ${count} 页 · 匹配 ${filteredIDs.length} / 共 ${accounts.size} 个账号`);
        ui.Prev.disabled = page <= 1; ui.Next.disabled = page >= count;
        put(ui.ListStatus, available ? `AUTH 记录已导入 ${accounts.size} 个账号` : '检测列表未更新，暂时禁止提交');
        if (!reapply && JSON.stringify([...accounts.values()].filter(matches).map(a => String(a.id))) !== JSON.stringify(filteredIDs)) ui.ApplyFilter.hidden = false;
        patchBatch();
        clearTimeout(cooldownTimer);
        if ([...summaries.values()].some(summary => remaining(summary) > 0)) cooldownTimer = setTimeout(() => render(), 1000);
    }
    function patchBatch() {
        ui.Progress.hidden = !batch;
        if (!batch) return;
        const c = batch.counts || {}, total = Number(c.total ?? batch.total ?? batchItems.length) || 0;
        const value = key => Number(c[key]) || 0;
        const settled = Number(c.settled ?? value('finished') + value('skipped') + value('canceled') + value('interrupted'));
        const title = { active: '检测进行中', stopping: '正在停止检测', stopped: '检测已停止', completed: '检测已结束' }[batch.state] || '检测批次';
        put(ui.BatchTitle, `${title} · 已结束 ${settled}/${total}`);
        ui.ProgressBar.value = total ? Math.min(100, settled / total * 100) : 0;
        put(ui.BatchCounts, `已检测 ${value('finished')}（正常 ${value('ok')}，异常 ${value('abnormal')}） · 本地预检 ${value('precheck')} · 已尝试请求 ${value('attempted')} · 运行 ${value('running')} · 排队 ${value('queued')} · 跳过 ${value('skipped')} · 取消 ${value('canceled')} · 中断 ${value('interrupted')}`);
        const reason = { user_cancel: '用户停止', shared_proxy_failure: '代理认证失败，剩余检测已停止', internal_error: '检测服务内部故障' }[batch.stop_reason] || '';
        put(ui.BatchRoute, `${batch.model || capabilities.model || ''} · ${batch.route_label || ''} · 并发 ${batch.concurrency || 1} · ${time(batch.created_at)}${reason ? ` · ${reason}` : ''}`);
        ui.Stop.hidden = !isActive(); ui.Stop.disabled = stopping || batch.state === 'stopping';
        put(ui.Stop, stopping || batch.state === 'stopping' ? '正在停止…' : '停止检测');
        ui.Retry.hidden = Boolean(activeID) || !batchItems.some(item => ['interrupted', 'canceled'].includes(item.state) && accounts.has(String(item.account_id)));
        ui.Retry.disabled = Boolean(pendingRequest) || submitting || recovering || !available;
    }
    function changeSelection(ids, checked) {
        if (!available || submitting || pendingRequest) return render();
        const next = new Set(selected);
        ids.filter(id => eligible(accounts.get(id))).forEach(id => checked ? next.add(id) : next.delete(id));
        if (next.size > maxBatch()) { notice(`每批最多 ${maxBatch()} 个账号，本次选择未生效。`); render(); return; }
        selected.clear(); next.forEach(id => selected.add(id)); render();
    }
    function updateBadge() {
        if (!tabBadge) return;
        let issues = 0;
        for (const id of accounts.keys()) {
            const result = summaries.get(id)?.last_result;
            if (result && result.outcome !== 'ok') issues++;
        }
        put(tabBadge, issues || '');
    }
    function applyHistory(payload) {
        if (payload.imports_available !== true || payload.checks_available !== true || !Array.isArray(payload.data) || !payload.checks || typeof payload.checks !== 'object') {
            throw new Error('账号、导入或检测记录暂不可用，请刷新重试');
        }
        const imported = new Set((payload.imports || []).filter(item => item.state === 'imported' && Number(item.sub2_account_id) > 0).map(item => String(item.account_id)));
        const previous = accounts;
        accounts = new Map(payload.data.filter(account => imported.has(String(account.id))).map(account => [String(account.id), account]));
        summaries.clear();
        for (const [id, summary] of Object.entries(payload.checks)) {
            summaries.set(id, { ...summary, _cooldownUntil: Date.now() + Math.max(0, Number(summary.cooldown_remaining_seconds) || 0) * 1000 });
        }
        if (payload.recoveries && typeof payload.recoveries === 'object') {
            const currentRecoveryIDs = new Set(Object.keys(payload.recoveries));
            for (const id of recoveryStates.keys()) if (!currentRecoveryIDs.has(id)) recoveryStates.delete(id);
            for (const [id, recovery] of Object.entries(payload.recoveries)) {
                const record = recoveryRecord(recovery);
                setRecovery(id, record);
                if (recoveryActiveStates.has(record.state)) scheduleRecoveryPoll(id, 500);
            }
        }
        let removed = 0;
        for (const id of selected) if (!accounts.has(id) || !summaries.get(id)?.eligible) { selected.delete(id); removed++; }
        if (removed) notice(`已移除 ${removed} 个删除或不再符合检测条件的选择。`);
        for (const id of previous.keys()) if (!accounts.has(id) && !pageIDs().includes(id)) rows.delete(id);
        available = true; summaryUpdatedAt = Date.now();
        updateBadge();
        if (detailID && ui.Details.open) void refreshDetails();
    }
    async function refreshAccounts({ reapply = false, invalidate = false } = {}) {
        if (historyRequest) {
            if (invalidate) { historyGeneration++; historyAgain = true; historyReapply ||= reapply; }
            return historyRequest;
        }
        const generation = ++historyGeneration;
        historyRequest = (async () => {
            try {
                const payload = await api('/api/history');
                if (generation !== historyGeneration) return;
                applyHistory(payload); render(reapply || !activeID);
            } catch (error) {
                if (generation !== historyGeneration) return;
                available = false; notice(error.message || '检测记录未更新，请刷新重试'); render();
            } finally {
                historyRequest = null;
                if (historyAgain) { const apply = historyReapply; historyAgain = false; historyReapply = false; await refreshAccounts({ reapply: apply }); }
            }
        })();
        return historyRequest;
    }
    function applyBatch(payload) {
        const next = payload.batch; if (!next) throw new Error('检测批次响应不完整');
        batch = next; batchItems = Array.isArray(payload.items) ? payload.items : batchItems;
        activeID = isActive(next) ? String(next.id || next.batch_id) : '';
        for (const item of batchItems) {
            const id = String(item.account_id), summary = summaries.get(id);
            if (!summary) continue;
            if (!summary.latest_task || Number(item.id) >= Number(summary.latest_task.id)) summary.latest_task = item;
            if (item.state === 'finished' && (!summary.last_result || Number(item.id) >= Number(summary.last_result.id))) summary.last_result = item;
        }
        updateBadge();
        if (detailID) {
            const updates = batchItems.filter(item => String(item.account_id) === detailID);
            const byID = new Map(detailRecords.map(item => [String(item.id), item]));
            updates.forEach(item => byID.set(String(item.id), item));
            detailRecords = [...byID.values()].sort((a, b) => Number(b.id) - Number(a.id)).slice(0, 20); renderDetails();
        }
        render(!activeID);
    }
    async function readBatch(id) {
        const generation = ++batchReadGeneration, credentialVersion = credentialGeneration;
        const payload = await api(`/api/account-checks/${encodeURIComponent(id)}`);
        if (generation !== batchReadGeneration || credentialVersion !== credentialGeneration) return;
        applyBatch(payload);
    }
    async function recover() {
        if (recovering || submitting) return;
        recovering = true; render();
        try {
            const payload = await api('/api/account-checks?active=1');
            capabilities = payload.capabilities || {};
            put(ui.Model, `模型：${capabilities.model || '暂不可用'} · 每批最多 ${maxBatch()} 个 · 单次最长 20 秒`);
            if (Number(capabilities.max_concurrency) === 1) ui.Concurrency.value = '1';
            ui.Concurrency.querySelector('option[value="2"]').disabled = Number(capabilities.max_concurrency) < 2;
            if (payload.active) {
                const recoveredID = payload.active.id || payload.active.batch_id;
                if (recoveredID == null || recoveredID === '') throw new Error('检测服务返回的任务编号无效，请刷新重试');
                activeID = String(recoveredID);
                await readBatch(activeID);
            } else {
                activeID = '';
                const recent = await api('/api/account-checks?limit=20');
                if (Array.isArray(recent.data) && recent.data.length) {
                    const latest = recent.data[0], latestID = latest?.id || latest?.batch_id;
                    if (latestID != null && latestID !== '') await readBatch(latestID);
                }
            }
        } finally { recovering = false; render(); schedulePoll(); }
    }
    function schedulePoll(delay = document.hidden ? 5000 : 1000) {
        clearTimeout(timer); timer = activeID ? setTimeout(poll, delay) : null;
    }
    async function poll() {
        if (polling || !activeID) return;
        polling = true;
        try {
            await readBatch(activeID);
            if (!activeID || Date.now() - summaryUpdatedAt >= 5000) await refreshAccounts({ reapply: !activeID });
            if (ui.Notice.textContent.startsWith('连接中断')) notice('连接已恢复。');
        } catch (_) { notice('连接中断，正在恢复；后台任务继续，请勿重复创建。'); }
        finally { polling = false; schedulePoll(); }
    }
    function requestKey() {
        if (crypto.randomUUID) return crypto.randomUUID();
        return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('');
    }
    async function start(ids) {
        if (activeID) { ui.Progress.scrollIntoView({ block: 'center', behavior: 'smooth' }); notice('已有检测批次正在执行，当前页面显示其进度。'); return; }
        if (!available || capabilities.available !== true || submitting || recovering || !pendingRequest && !canSubmit()) { notice('检测暂不可用，请刷新服务配置或检查所选出口。'); return; }
        if (!pendingRequest) {
            const unique = [...new Set(ids.map(String))];
            if (!unique.length || unique.length > maxBatch() || unique.some(id => !eligible(accounts.get(id)))) { notice('选择已变化或不符合检测条件，请刷新后重新选择。'); return; }
            savePending({ request_key: requestKey(), account_ids: unique.map(Number), concurrency: Number(ui.Concurrency.value), proxy_mode: ui.Route.value });
        }
        submitting = true; render(); notice('正在提交检测任务…');
        try {
            const payload = await api('/api/account-checks', { method: 'POST', body: JSON.stringify(pendingRequest) });
            const id = String(payload.batch_id || payload.id); savePending(null); selected.clear();
            activeID = ['active', 'stopping'].includes(payload.state) ? id : '';
            batch = { ...payload, id }; batchItems = [];
            await readBatch(id);
            notice(payload.reused ? '已恢复原检测任务，没有重复发送请求。' : '检测已开始。刷新或关闭页面不会终止后台任务。');
            if (!activeID) await refreshAccounts({ reapply: true, invalidate: true });
            return true;
        } catch (error) {
            if (error.definitive) savePending(null);
            if (error.payload?.code === 'batch_active' && error.payload.active_batch_id) {
                activeID = String(error.payload.active_batch_id);
                try { await readBatch(activeID); } catch (_) { /* Poll resumes safely. */ }
            }
            notice(pendingRequest ? '提交结果尚未确认。点击“重试提交”会使用同一任务编号，不会重复创建。' : error.message || '检测提交失败');
            return false;
        } finally { submitting = false; render(); schedulePoll(); }
    }
    async function startSingle(id) {
        return startMany([String(id)]);
    }
    async function startMany(ids) {
        window.AuthTabs?.select('checks');
        await refreshAll();
        if (pendingRequest) { notice('另有一次提交结果未确认，请先点击“重试提交”恢复原任务，再检测这个账号。'); return false; }
        if (activeID) { notice('已有检测批次正在执行，当前页面显示其进度。'); return false; }
        const unique = [...new Set((ids || []).map(String))];
        const cooldown = unique.find(id => remaining(summaries.get(id)));
        if (cooldown) { notice(`部分凭据刚刚检测过，请稍后重试；上次检测结果已保留。`); return false; }
        return start(unique);
    }
    async function stop() {
        if (!activeID || stopping) return;
        batchReadGeneration++;
        stopping = true; patchBatch();
        try {
            const payload = await api(`/api/account-checks/${encodeURIComponent(activeID)}/cancel`, { method: 'POST', body: '{}' });
            savePending(null);
            batch = payload.batch; activeID = isActive(batch) ? String(batch.id || batch.batch_id) : '';
            notice(activeID ? '正在停止。已完成结果保留，进行中的请求正在退出。' : '检测已停止，已完成结果保留。');
            if (!activeID) {
                await readBatch(batch.id || batch.batch_id);
                await refreshAccounts({ reapply: true, invalidate: true });
            }
        } catch (error) { notice(error.message || '停止尚未确认，正在继续查询任务状态。'); }
        finally { stopping = false; render(); schedulePoll(0); }
    }
    function renderDetails() {
        const signature = JSON.stringify(detailRecords);
        if (signature === detailSignature) return;
        detailSignature = signature;
        const fragment = document.createDocumentFragment();
        for (const item of detailRecords) {
            const section = document.createElement('section'); section.className = 'history-attempt';
            const title = document.createElement('p'); title.className = 'history-attempt-header';
            put(title, `${time(item.finished_at || item.started_at || item.created_at)} · ${taskNames[item.state] || item.state} · ${item.outcome ? resultName(item) : skipNames[item.skip_reason] || ''}`); section.appendChild(title);
            const list = document.createElement('dl');
            const fields = [ ['凭据结果', freshnessName(item)],
                ['模型 / 出口', `${item.model || '未记录'} / ${item.route_label || '未记录'}`], ['耗时', `${(Number(item.duration_ms || 0) / 1000).toFixed(1)} 秒`],
                ['尝试请求', item.request_attempted == null ? '未知，不能确认上次是否已发送' : item.request_attempted ? '是（不保证上游收到或未计费）' : '否'], ['目标 HTTP', item.http_status ?? '无目标 HTTP 响应'],
                ['代理 HTTP', item.proxy_http_status ?? '无'], ['流内状态 / 错误码', `${item.stream_error_status ?? '无'} / ${item.stream_error_code || '无'}`],
                ['错误码 / 阶段', `${item.error_code || '无'} / ${item.failure_stage || '无'}`], ['说明', item.message || item.error_message || '无'],
                ['建议等待', item.retry_after_seconds != null ? `${item.retry_after_seconds} 秒` : '无'] ];
            fields.forEach(([label, value]) => { const term = document.createElement('dt'), description = document.createElement('dd'); put(term, label); put(description, value); list.append(term, description); });
            section.appendChild(list); fragment.appendChild(section);
        }
        if (!detailRecords.length) { const empty = document.createElement('p'); put(empty, '暂无检测记录。'); fragment.appendChild(empty); }
        ui.DetailsBody.replaceChildren(fragment);
    }
    function restoreDetailsFocus() {
        const trigger = detailTrigger;
        detailTrigger = null;
        const visible = node => {
            if (!node || node.disabled || node.isConnected === false) return false;
            for (let current = node; current; current = current.parentElement) if (current.hidden) return false;
            return true;
        };
        if (visible(trigger)) trigger.focus?.({ preventScroll: true });
        else if (visible(ui.SelectAll)) ui.SelectAll.focus?.({ preventScroll: true });
        else document.getElementById('tab-checks')?.focus?.({ preventScroll: true });
    }
    async function openDetails(id, trigger = null) {
        detailID = String(id);
        detailTrigger = trigger && typeof trigger.focus === 'function' ? trigger : null;
        put(ui.DetailsTitle, `${accounts.get(detailID)?.email || '账号'} · 检测详情`);
        put(ui.DetailsStatus, '正在读取…'); detailRecords = []; detailSignature = ''; renderDetails();
        if (!ui.Details.open) ui.Details.showModal();
        ui.DetailsClose.focus?.({ preventScroll: true });
        await refreshDetails();
    }
    async function refreshDetails() {
        if (!detailID || !ui.Details.open) return;
        const generation = ++detailGeneration, credentialVersion = credentialGeneration;
        try {
            const payload = await api(`/api/history/${encodeURIComponent(detailID)}`);
            if (generation !== detailGeneration || credentialVersion !== credentialGeneration || !ui.Details.open) return;
            if (payload.checks_available === false || !Array.isArray(payload.checks)) throw new Error('检测记录暂不可用');
            detailRecords = payload.checks; renderDetails(); put(ui.DetailsStatus, `最近 ${detailRecords.length} 条检测记录`);
        } catch (error) { if (generation === detailGeneration) put(ui.DetailsStatus, `详情未更新，旧记录仅供参考：${error.message || '读取失败'}`); }
    }
    function refreshAll() {
        if (refreshRequest) return refreshRequest;
        ui.Refresh.disabled = true;
        refreshRequest = (async () => {
            try {
                const results = await Promise.allSettled([refreshAccounts({ reapply: true, invalidate: true }), recover()]);
                const failure = results.find(result => result.status === 'rejected');
                if (failure) throw failure.reason;
            }
            catch (error) { capabilities.available = false; notice(error.message || '检测服务暂不可用'); render(); }
            finally { ui.Refresh.disabled = false; refreshRequest = null; }
        })();
        return refreshRequest;
    }
    function resetScroll() {
        if (tabPanel) tabPanel.scrollTop = 0;
    }
    function resetFilter() {
        const hadSelection = selected.size > 0; selected.clear(); page = 1; render(true);
        resetScroll();
        if (hadSelection) notice('搜索或筛选已变化，已清空选择。');
    }
    ui.Search.addEventListener('input', resetFilter); ui.Filter.addEventListener('change', resetFilter);
    ui.SelectAll.addEventListener('change', () => changeSelection(pageIDs(), ui.SelectAll.checked));
    ui.Clear.addEventListener('click', () => { selected.clear(); render(); });
    ui.Start.addEventListener('click', () => start([...selected]));
    ui.Route.addEventListener('change', () => { notice(ui.Route.value === 'default' && (!capabilities.default_proxy_configured || !capabilities.default_proxy_supported) ? '服务器默认代理未配置或当前配置不支持，请先修复服务器配置。' : ''); render(); });
    ui.Refresh.addEventListener('click', refreshAll);
    ui.ApplyFilter.addEventListener('click', () => { selected.clear(); page = 1; render(true); resetScroll(); notice('已更新筛选结果并清空选择。'); });
    ui.Prev.addEventListener('click', () => { page--; render(); resetScroll(); });
    ui.Next.addEventListener('click', () => { page++; render(); resetScroll(); });
    ui.PageSize.addEventListener('change', () => { pageSize = Number(ui.PageSize.value); page = 1; render(); resetScroll(); });
    ui.Stop.addEventListener('click', stop);
    ui.Retry.addEventListener('click', () => {
        selected.clear();
        batchItems.filter(item => ['interrupted', 'canceled'].includes(item.state) && eligible(accounts.get(String(item.account_id)))).forEach(item => selected.add(String(item.account_id)));
        notice(`已选择 ${selected.size} 个未完成账号；将使用当前选择的出口创建新批次。`); render();
    });
    ui.DetailsClose.addEventListener('click', () => ui.Details.close());
    ui.Details.addEventListener('close', () => { detailID = ''; detailGeneration++; restoreDetailsFocus(); });
    document.addEventListener('auth-history-changed', event => {
        credentialGeneration++;
        if (!event.detail?.history) return refreshAccounts({ invalidate: true });
        // Reuse the history page's snapshot instead of querying every Sub2 account twice.
        historyGeneration++;
        historyAgain = false;
        try { applyHistory(event.detail.history); render(!activeID); }
        catch (error) { available = false; notice(error.message); render(); }
    });
    document.addEventListener('visibilitychange', () => { if (!document.hidden) void refreshAll(); else schedulePoll(); });
    document.addEventListener('auth-tab-changed', event => {
        if (event.detail.name === 'checks') void refreshAll();
        else if (ui.Details.open) ui.Details.close();
    });
    window.AccountChecks = { startSingle, startMany, recoverAccount: startRecovery };
    if (pendingRequest) notice('有一次提交结果未确认；重试提交会沿用原任务编号。');
    void refreshAll();
})();
