const healthEl = document.getElementById('health');
const providerResult = document.getElementById('providerResult');
const chatResult = document.getElementById('chatResult');
const syncResult = document.getElementById('syncResult');
const taskResult = document.getElementById('taskResult');
const activeProviderSelect = document.getElementById('activeProviderSelect');

async function apiRequest(url, options = {}) {
  const appToken = document.getElementById('appToken').value.trim();
  const headers = new Headers(options.headers || {});
  if (appToken) {
    headers.set('X-App-Token', appToken);
  }
  const res = await fetch(url, { ...options, headers });
  let data = {};
  try {
    data = await res.json();
  } catch (_) {}
  if (!res.ok) {
    const msg = data?.error?.message || `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return data;
}

async function refreshHealth() {
  healthEl.textContent = '检测中...';
  try {
    const data = await apiRequest('/api/health');
    healthEl.textContent = `${data.status} @ ${data.time}\n${JSON.stringify(data.checks, null, 2)}`;
  } catch (err) {
    healthEl.textContent = `失败: ${err.message}`;
  }
}

function parseHeaders() {
  const raw = document.getElementById('providerHeaders').value.trim();
  if (!raw) return {};
  const parsed = JSON.parse(raw);
  if (typeof parsed !== 'object' || Array.isArray(parsed) || parsed === null) {
    throw new Error('Headers 必须是 JSON 对象');
  }
  return parsed;
}

async function refreshProviders() {
  try {
    const data = await apiRequest('/api/providers');
    const providers = data.providers || [];
    activeProviderSelect.innerHTML = '';
    providers.forEach((p) => {
      const option = document.createElement('option');
      option.value = p.id;
      option.textContent = `${p.name} (${p.id})${p.active ? ' [active]' : ''}`;
      if (p.active) option.selected = true;
      activeProviderSelect.appendChild(option);
    });
    providerResult.textContent = JSON.stringify(data, null, 2);
  } catch (err) {
    providerResult.textContent = `失败: ${err.message}`;
  }
}

async function waitTask(taskId, outputEl) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < 120000) {
    const task = await apiRequest(`/api/tasks/${taskId}`);
    outputEl.textContent = JSON.stringify(task, null, 2);
    if (task.status === 'completed' || task.status === 'failed') return task;
    await new Promise((resolve) => setTimeout(resolve, 1200));
  }
  throw new Error('任务超时，请稍后在任务列表中查看状态');
}

document.getElementById('checkHealth').addEventListener('click', refreshHealth);

document.getElementById('saveProvider').addEventListener('click', async () => {
  providerResult.textContent = '保存中...';
  try {
    const payload = {
      id: document.getElementById('providerId').value.trim(),
      name: document.getElementById('providerName').value.trim(),
      base_url: document.getElementById('providerBaseUrl').value.trim(),
      model: document.getElementById('providerModel').value.trim(),
      api_key: document.getElementById('providerApiKey').value.trim(),
      headers: parseHeaders(),
      active: document.getElementById('providerActive').checked,
    };
    const data = await apiRequest('/api/providers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    providerResult.textContent = JSON.stringify(data, null, 2);
    document.getElementById('providerApiKey').value = '';
    await refreshProviders();
  } catch (err) {
    providerResult.textContent = `失败: ${err.message}`;
  }
});

document.getElementById('refreshProviders').addEventListener('click', refreshProviders);

document.getElementById('setActiveProvider').addEventListener('click', async () => {
  providerResult.textContent = '切换中...';
  try {
    const data = await apiRequest('/api/providers/active', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: activeProviderSelect.value }),
    });
    providerResult.textContent = JSON.stringify(data, null, 2);
    await refreshProviders();
  } catch (err) {
    providerResult.textContent = `失败: ${err.message}`;
  }
});

document.getElementById('sendChat').addEventListener('click', async () => {
  chatResult.textContent = '提交任务中...';
  try {
    const payload = {
      agent: document.getElementById('agent').value.trim(),
      provider_id: document.getElementById('chatProviderId').value.trim(),
      message: document.getElementById('message').value.trim(),
    };
    const task = await apiRequest('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    chatResult.textContent = `任务已提交: ${task.task_id}\n等待执行结果...`;
    await waitTask(task.task_id, chatResult);
    await refreshTasks();
  } catch (err) {
    chatResult.textContent = `失败: ${err.message}`;
  }
});

document.getElementById('syncGit').addEventListener('click', async () => {
  syncResult.textContent = '提交同步任务中...';
  try {
    const githubToken = document.getElementById('githubToken').value.trim();
    const payload = {
      owner: document.getElementById('repoOwner').value.trim(),
      repo: document.getElementById('repoName').value.trim(),
      branch: document.getElementById('repoBranch').value.trim(),
      file_path: document.getElementById('syncFilePath').value.trim(),
      content: document.getElementById('syncContent').value,
      commit_message: document.getElementById('syncCommitMessage').value.trim(),
    };
    const task = await apiRequest('/api/git/sync', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-GitHub-Token': githubToken,
      },
      body: JSON.stringify(payload),
    });
    syncResult.textContent = `任务已提交: ${task.task_id}\n等待执行结果...`;
    await waitTask(task.task_id, syncResult);
    await refreshTasks();
  } catch (err) {
    syncResult.textContent = `失败: ${err.message}`;
  }
});

async function refreshTasks() {
  try {
    const data = await apiRequest('/api/tasks');
    taskResult.textContent = JSON.stringify(data, null, 2);
  } catch (err) {
    taskResult.textContent = `失败: ${err.message}`;
  }
}

document.getElementById('refreshTasks').addEventListener('click', refreshTasks);

if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}

refreshHealth();
refreshProviders();
refreshTasks();
