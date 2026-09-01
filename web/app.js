const healthEl = document.getElementById('health');
const chatResult = document.getElementById('chatResult');
const syncResult = document.getElementById('syncResult');

document.getElementById('checkHealth').addEventListener('click', async () => {
  healthEl.textContent = '检测中...';
  try {
    const res = await fetch('/api/health');
    const data = await res.json();
    healthEl.textContent = `${data.status} @ ${data.time}`;
  } catch (err) {
    healthEl.textContent = `失败: ${err.message}`;
  }
});

document.getElementById('sendChat').addEventListener('click', async () => {
  const message = document.getElementById('message').value.trim();
  const apiKey = document.getElementById('apiKey').value.trim();
  const agent = document.getElementById('agent').value.trim();

  chatResult.textContent = '请求中...';
  try {
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': apiKey,
      },
      body: JSON.stringify({ message, agent }),
    });
    const data = await res.json();
    chatResult.textContent = JSON.stringify(data, null, 2);
  } catch (err) {
    chatResult.textContent = `失败: ${err.message}`;
  }
});

document.getElementById('syncGit').addEventListener('click', async () => {
  syncResult.textContent = '触发中...';
  try {
    const res = await fetch('/api/git/sync', { method: 'POST' });
    const data = await res.json();
    syncResult.textContent = JSON.stringify(data, null, 2);
  } catch (err) {
    syncResult.textContent = `失败: ${err.message}`;
  }
});

if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}
