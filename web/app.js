/* ====================================================
   ZPWU 云智能体 — 前端主逻辑
   GitHub-only storage · stateless server · Agent Loop
   ==================================================== */
'use strict';

// ─── 持久化 ───────────────────────────────────────────
const CFG_KEY = 'zpwu_config';
const PV_KEY  = 'zpwu_providers';

function loadCfg()   { try { return JSON.parse(localStorage.getItem(CFG_KEY) || '{}'); } catch(_) { return {}; } }
function saveCfg(p)  { const c={...loadCfg(),...p}; localStorage.setItem(CFG_KEY, JSON.stringify(c)); return c; }
function loadPVs()   { try { return JSON.parse(localStorage.getItem(PV_KEY) || '[]'); } catch(_) { return []; } }
function savePVs(l)  { localStorage.setItem(PV_KEY, JSON.stringify(l)); }

function activePV() {
  const id = loadCfg().activeProviderId;
  const list = loadPVs();
  return list.find(p => p.id === id) || list[0] || null;
}

// ─── DOM 辅助 ─────────────────────────────────────────
const $ = id => document.getElementById(id);
function setStatus(el, txt, type) {
  if (!el) return;
  el.textContent = txt;
  // support both .status-bar elements and .editor-status / .health-status elements
  const base = el.className.split(' ')[0] || 'status-bar';
  el.className = base + (type ? ' ' + type : '');
}
function esc(s) {
  return String(s)
    .replace(/&/g,'&amp;').replace(/</g,'&lt;')
    .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ─── API 请求 ─────────────────────────────────────────
function getAppToken() { return (loadCfg().appToken||'').trim(); }
function getGHToken()  { return (loadCfg().githubToken||'').trim(); }

async function api(path, opts={}) {
  const h = new Headers(opts.headers||{});
  const tok = getAppToken();
  if (tok) h.set('X-App-Token', tok);
  const res = await fetch(path, {...opts, headers: h});
  let d={};
  try { d = await res.json(); } catch(_) {}
  if (!res.ok) throw new Error(d?.error?.message || `HTTP ${res.status}`);
  return d;
}
async function ghApi(path, opts={}) {
  const h = new Headers(opts.headers||{});
  const t = getGHToken();
  if (t) h.set('X-GitHub-Token', t);
  return api(path, {...opts, headers: h});
}

// ─── 任务轮询 ─────────────────────────────────────────
async function pollTask(id, onTick, ms=120000) {
  const t0 = Date.now();
  while (Date.now()-t0 < ms) {
    const t = await api(`/api/tasks/${id}`);
    if (onTick) onTick(t);
    if (t.status==='completed'||t.status==='failed') return t;
    await new Promise(r=>setTimeout(r,1300));
  }
  throw new Error('任务超时');
}

/* ══════════════════════════════════════════════════════
   AUTH
══════════════════════════════════════════════════════ */
async function checkAuth() {
  const tok = getGHToken();
  if (!tok) { showLogin(); return; }
  try {
    const u = await ghApi('/api/auth/user');
    saveCfg({ githubLogin: u.login, githubAvatar: u.avatar_url, githubName: u.name });
    showApp(u);
  } catch(_) {
    saveCfg({ githubToken:'', githubLogin:'', githubAvatar:'' });
    showLogin();
  }
}

function showLogin() { $('loginPage').hidden=false; $('app').hidden=true; }
function showApp(u) {
  $('loginPage').hidden=true; $('app').hidden=false;
  renderUserInfo(u||{}); initApp();
}
function renderUserInfo(u) {
  const cfg=loadCfg();
  const login=u.login||cfg.githubLogin||'';
  const name=u.name||cfg.githubName||login;
  const avatar=u.avatar_url||cfg.githubAvatar||'';
  // 设置页面用户信息
  if ($('userName')) $('userName').textContent = name||'未知用户';
  if ($('userLogin')) $('userLogin').textContent = login ? '@'+login : '';
  if (avatar && $('userAvatar')) { $('userAvatar').src=avatar; $('userAvatar').alt=login; $('userAvatar').hidden=false; }
  // 顶部 header 头像 & 登录名
  if (avatar && $('headerAvatar')) { $('headerAvatar').src=avatar; $('headerAvatar').alt=login; $('headerAvatar').hidden=false; }
  if ($('headerLogin')) $('headerLogin').textContent = login ? '@'+login : '';
  if ($('appToken')) $('appToken').value = cfg.appToken||'';
}
$('logoutBtn').addEventListener('click',()=>{ saveCfg({githubToken:'',githubLogin:'',githubAvatar:'',githubName:''}); showLogin(); });
$('saveAppToken').addEventListener('click',()=>{ saveCfg({appToken:$('appToken').value.trim()}); $('saveAppToken').textContent='已保存 ✓'; setTimeout(()=>{$('saveAppToken').textContent='保存';},1500); });
$('checkHealth').addEventListener('click',async()=>{
  setStatus($('healthStatus'),'检测中…','');
  try { const d=await api('/api/health'); setStatus($('healthStatus'),`${d.status}  ${d.time}  OAuth:${d.checks?.github_oauth}`,'ok'); }
  catch(e) { setStatus($('healthStatus'),'失败: '+e.message,'err'); }
});

/* ══════════════════════════════════════════════════════
   TAB
══════════════════════════════════════════════════════ */
function switchTab(name) {
  document.querySelectorAll('.tab-btn').forEach(b=>{
    const a=b.dataset.tab===name;
    b.classList.toggle('active',a); b.setAttribute('aria-selected',String(a));
  });
  document.querySelectorAll('.tab-panel').forEach(p=>{
    const a=p.id==='tab-'+name;
    p.classList.toggle('active',a); p.hidden=!a;
  });
}
document.querySelectorAll('.tab-btn').forEach(b=>b.addEventListener('click',()=>switchTab(b.dataset.tab)));
document.querySelectorAll('[data-tab-goto]').forEach(b=>b.addEventListener('click',()=>switchTab(b.dataset.tabGoto)));

/* ══════════════════════════════════════════════════════
   PROVIDERS
══════════════════════════════════════════════════════ */
let editPvId = null;

function renderPVList() {
  const list=loadPVs(), cfg=loadCfg(), el=$('pvList');
  if (!list.length) { el.innerHTML='<p class="tree-hint">暂无提供商</p>'; updateTopBars(); return; }
  el.innerHTML='';
  list.forEach(p=>{
    const card=document.createElement('div');
    card.className='pv-card'+(p.id===cfg.activeProviderId?' pv-active':'');
    card.setAttribute('role','listitem');
    card.innerHTML=`
      <div class="pv-card-header">
        <span class="pv-name">${esc(p.name)}</span>
        <span class="pv-kind pv-kind-${p.kind}">${p.kind==='claude'?'Claude':'OpenAI'}</span>
      </div>
      <div class="pv-model">${esc(p.model||'—')}</div>
      <div class="pv-url">${esc(p.baseURL||(p.kind==='claude'?'api.anthropic.com':'—'))}</div>
      <div class="pv-actions">
        <button class="btn-secondary pv-activate" data-pid="${esc(p.id)}" ${p.id===cfg.activeProviderId?'disabled':''}>
          ${p.id===cfg.activeProviderId?'✓ 已激活':'激活'}
        </button>
        <button class="btn-secondary pv-edit" data-pid="${esc(p.id)}">编辑</button>
        <button class="btn-danger pv-del" data-pid="${esc(p.id)}">删除</button>
      </div>`;
    el.appendChild(card);
  });
  el.querySelectorAll('.pv-activate').forEach(b=>b.addEventListener('click',()=>{ saveCfg({activeProviderId:b.dataset.pid}); renderPVList(); }));
  el.querySelectorAll('.pv-edit').forEach(b=>b.addEventListener('click',()=>loadPVForm(b.dataset.pid)));
  el.querySelectorAll('.pv-del').forEach(b=>b.addEventListener('click',()=>{
    const l=loadPVs().filter(p=>p.id!==b.dataset.pid); savePVs(l);
    if (loadCfg().activeProviderId===b.dataset.pid) saveCfg({activeProviderId:l[0]?.id||''});
    renderPVList();
  }));
  updateTopBars();
}

function loadPVForm(id) {
  const p=loadPVs().find(x=>x.id===id); if(!p) return;
  editPvId=id;
  $('pvName').value=p.name||''; $('pvKind').value=p.kind||'openai';
  $('pvBaseURL').value=p.baseURL||''; $('pvModel').value=p.model||'';
  $('pvKey').value=p.apiKey||''; $('pvHeaders').value=p.headers?JSON.stringify(p.headers):'';
  $('pvSave').textContent='更新'; switchTab('providers'); $('pvName').focus();
}

$('pvSave').addEventListener('click',()=>{
  const name=$('pvName').value.trim(), kind=$('pvKind').value,
        baseURL=$('pvBaseURL').value.trim(), model=$('pvModel').value.trim(),
        apiKey=$('pvKey').value.trim();
  if (!name||!model||!apiKey) { alert('名称、模型、API Key 为必填项'); return; }
  let headers={};
  const rawH=$('pvHeaders').value.trim();
  if (rawH) { try { headers=JSON.parse(rawH); } catch(_) { alert('Headers 格式错误'); return; } }
  const list=loadPVs();
  if (editPvId) {
    const i=list.findIndex(x=>x.id===editPvId);
    if (i!==-1) list[i]={...list[i],name,kind,baseURL,model,apiKey,headers};
    savePVs(list); editPvId=null; $('pvSave').textContent='保存';
  } else {
    const nw={id:'pv-'+Date.now(),name,kind,baseURL,model,apiKey,headers};
    list.push(nw); savePVs(list);
    if (!loadCfg().activeProviderId) saveCfg({activeProviderId:nw.id});
  }
  clearPVForm(); renderPVList();
});
$('pvClear').addEventListener('click',()=>{ editPvId=null; $('pvSave').textContent='保存'; clearPVForm(); });
function clearPVForm() {
  ['pvName','pvBaseURL','pvModel','pvKey','pvHeaders'].forEach(id=>$(id).value='');
  $('pvKind').value='openai';
}

function updateTopBars() {
  const p=activePV();
  $('chatProviderName').textContent = p ? `${p.name} · ${p.model}` : '未选择 API — 去配置';
  const r=currentOwnerRepo;
  $('chatRepoName').textContent = r ? `${r.owner}/${r.repo}@${r.branch}` : '未选择仓库';
}

/* ══════════════════════════════════════════════════════
   FILE BROWSER
══════════════════════════════════════════════════════ */
let currentOwnerRepo    = null;
let currentDirPath      = '';
let currentFileCtx      = null; // {path, content}
let currentOriginalContent = ''; // raw content from GitHub before editing

async function loadRepoList() {
  const sel=$('repoSelect'); sel.innerHTML='<option>加载中…</option>';
  try {
    const d=await ghApi('/api/auth/repos'), repos=d.repos||[];
    sel.innerHTML='';
    repos.forEach(r=>{
      const o=document.createElement('option');
      o.value=r.full_name; o.dataset.branch=r.default_branch||'main';
      o.textContent=r.full_name+(r.private?' 🔒':'');
      sel.appendChild(o);
    });
    if (repos.length) $('repoBranchInput').value=repos[0].default_branch||'main';
  } catch(e) { sel.innerHTML='<option>加载失败</option>'; }
}

$('repoSelect').addEventListener('change',()=>{
  const o=$('repoSelect').selectedOptions[0];
  if (o) $('repoBranchInput').value=o.dataset.branch||'main';
});

$('loadRepoBtn').addEventListener('click',()=>{
  const fn=$('repoSelect').value; if (!fn||fn.indexOf('/')===-1) return;
  const [owner,repo]=fn.split('/');
  currentOwnerRepo={owner,repo,branch:$('repoBranchInput').value.trim()||'main'};
  currentDirPath=''; updateTopBars(); loadDir('');
});

async function loadDir(dirPath) {
  if (!currentOwnerRepo) return;
  currentDirPath=dirPath; $('fileTreePath').textContent='/'+dirPath;
  $('fileTree').innerHTML='<p class="tree-hint">加载中…</p>';
  const {owner,repo,branch}=currentOwnerRepo;
  try {
    const d=await ghApi('/api/git/files?'+new URLSearchParams({owner,repo,branch,path:dirPath}));
    renderTree(d.entries||[]);
  } catch(e) { $('fileTree').innerHTML=`<p class="tree-hint tree-err">失败: ${esc(e.message)}</p>`; }
}

function renderTree(entries) {
  const el=$('fileTree');
  if (!entries.length) { el.innerHTML='<p class="tree-hint">（空目录）</p>'; return; }
  const ul=document.createElement('ul'); ul.className='tree-list'; ul.setAttribute('role','group');
  if (currentDirPath) {
    const li=document.createElement('li'); li.className='tree-item tree-dir'; li.setAttribute('role','treeitem');
    const btn=document.createElement('button'); btn.className='tree-btn'; btn.textContent='⬆ ..';
    btn.addEventListener('click',()=>{ const p=currentDirPath.split('/').filter(Boolean); p.pop(); loadDir(p.join('/')); });
    li.appendChild(btn); ul.appendChild(li);
  }
  entries.forEach(e=>{
    const li=document.createElement('li'); li.className='tree-item '+(e.type==='dir'?'tree-dir':'tree-file'); li.setAttribute('role','treeitem');
    const btn=document.createElement('button'); btn.className='tree-btn';
    btn.textContent=(e.type==='dir'?'📁 ':'📄 ')+e.name;
    btn.addEventListener('click',()=>e.type==='dir'?loadDir(e.path):openFile(e.path));
    li.appendChild(btn); ul.appendChild(li);
  });
  el.innerHTML=''; el.appendChild(ul);
}

async function openFile(filePath) {
  if (!currentOwnerRepo) return;
  $('editorFilePath').textContent=filePath;
  $('fileEditor').value='加载中…'; $('fileEditor').disabled=true;
  if ($('saveDraft')) $('saveDraft').disabled=true;
  $('injectContext').disabled=true;
  currentOriginalContent='';
  setStatus($('editorStatus'),'','');
  const {owner,repo,branch}=currentOwnerRepo;
  try {
    const d=await ghApi('/api/git/file?'+new URLSearchParams({owner,repo,branch,path:filePath}));
    currentOriginalContent=d.file.content; // 缓存 GitHub 原始内容
    $('fileEditor').value=d.file.content; $('fileEditor').disabled=false;
    if ($('saveDraft')) $('saveDraft').disabled=false;
    $('injectContext').disabled=false;
    $('commitMessage').value='fix: update '+d.file.name;
    setStatus($('editorStatus'),`${d.file.size} 字节 · SHA ${d.file.sha.slice(0,7)}`,'ok');
  } catch(e) { $('fileEditor').value=''; $('fileEditor').disabled=false; setStatus($('editorStatus'),'加载失败: '+e.message,'err'); }
}

$('refreshFiles').addEventListener('click',()=>loadDir(currentDirPath));

// ── 存草稿（服务器暂存，不直接 push GitHub）────────────────
$('saveDraft').addEventListener('click',async()=>{
  if (!currentOwnerRepo) return;
  const fp=$('editorFilePath').textContent.trim(); if (!fp||fp==='未打开文件') return;
  const ght=getGHToken(); if (!ght) { setStatus($('editorStatus'),'请先登录 GitHub','err'); return; }
  const {owner,repo,branch}=currentOwnerRepo;
  $('saveDraft').disabled=true; setStatus($('editorStatus'),'存草稿中…','');
  try {
    await ghApi('/api/drafts',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({
        owner,repo,branch,file_path:fp,
        original_content:currentOriginalContent, // GitHub 原始内容
        content:$('fileEditor').value,
        commit_message:$('commitMessage').value.trim()||'update: '+fp
      })
    });
    setStatus($('editorStatus'),'✓ 已存为草稿，在「草稿箱」查看 diff 后推送','ok');
    loadDraftCount();
  } catch(e) { setStatus($('editorStatus'),'存草稿失败: '+e.message,'err'); }
  finally { $('saveDraft').disabled=false; }
});

$('injectContext').addEventListener('click',()=>{
  const fp=$('editorFilePath').textContent.trim(); if (!fp||fp==='未打开文件') return;
  currentFileCtx={path:fp,content:$('fileEditor').value};
  $('contextFileName').textContent=fp; $('contextBar').hidden=false;
  switchTab('chat');
});
$('clearContext').addEventListener('click',()=>{ currentFileCtx=null; $('contextBar').hidden=true; $('contextFileName').textContent=''; });

/* ══════════════════════════════════════════════════════
   DRAFTS — 草稿箱
══════════════════════════════════════════════════════ */
// ── Inline diff 引擎（LCS，带大文件保护）────────────────
const DIFF_LINE_LIMIT = 500; // 超过此行数改用简化模式，避免 O(m×n) OOM

function computeDiff(oldText, newText) {
  const oldLines = oldText.split('\n');
  const newLines = newText.split('\n');
  const m = oldLines.length, n = newLines.length;

  // 大文件保护：超过阈值时只比较头尾各 80 行
  if (m > DIFF_LINE_LIMIT || n > DIFF_LINE_LIMIT) {
    const HEAD = 80, TAIL = 80;
    // 确保 head 和 tail 不重叠
    const headEndOld = Math.min(HEAD, m), headEndNew = Math.min(HEAD, n);
    const tailStartOld = Math.max(headEndOld, m - TAIL);
    const tailStartNew = Math.max(headEndNew, n - TAIL);

    const ops = [];
    ops.push(...lcs(oldLines.slice(0, headEndOld), newLines.slice(0, headEndNew)));

    // 只有当尾部不与头部重叠时才插入省略行
    if (tailStartOld > headEndOld || tailStartNew > headEndNew) {
      ops.push({t:'ctx', v:`… 文件过大，中间部分已省略 …`});
      ops.push(...lcs(oldLines.slice(tailStartOld), newLines.slice(tailStartNew)));
    }
    return ops;
  }

  return lcs(oldLines, newLines);
}

function lcs(oldLines, newLines) {
  const m = oldLines.length, n = newLines.length;
  const dp = Array.from({length:m+1},()=>new Int32Array(n+1));
  for (let i=1;i<=m;i++) for (let j=1;j<=n;j++) {
    dp[i][j] = oldLines[i-1]===newLines[j-1] ? dp[i-1][j-1]+1 : Math.max(dp[i-1][j],dp[i][j-1]);
  }
  const ops=[];
  let i=m, j=n;
  while (i>0||j>0) {
    if (i>0&&j>0&&oldLines[i-1]===newLines[j-1]) { ops.push({t:'=',v:oldLines[i-1]}); i--;j--; }
    else if (j>0&&(i===0||dp[i][j-1]>=dp[i-1][j])) { ops.push({t:'+',v:newLines[j-1]}); j--; }
    else { ops.push({t:'-',v:oldLines[i-1]}); i--; }
  }
  return ops.reverse();
}

function renderDiffHTML(ops) {
  const CTX=3;
  let html='', changed=ops.map(o=>o.t!=='='&&o.t!=='ctx');
  for (let i=0;i<ops.length;i++) {
    const o=ops[i];
    if (o.t==='ctx') { html+=`<div class="diff-ctx">${esc(o.v)}</div>`; continue; }
    const near=changed.slice(Math.max(0,i-CTX),i+CTX+1).some(Boolean);
    if (!near) {
      if (i===0||ops[i-1].t==='=') html+=`<div class="diff-ctx">…</div>`;
      continue;
    }
    const cls=o.t==='+'?'diff-add':o.t==='-'?'diff-del':'diff-eq';
    const prefix=o.t==='+'?'+ ':o.t==='-'?'- ':'  ';
    html+=`<div class="${cls}">${esc(prefix+o.v)}</div>`;
  }
  return html||'<div class="diff-eq">（无变化）</div>';
}

async function loadDraftList() {
  const el=$('draftList'); if (!el) return;
  el.innerHTML='<p class="empty-hint">加载中…</p>';
  try {
    const d=await ghApi('/api/drafts');
    renderDraftList(d.drafts||[]);
    updateDraftBadge(d.drafts?.length||0);
  } catch(e) {
    el.innerHTML=`<p class="empty-hint tree-err">加载失败: ${esc(e.message)}</p>`;
  }
}

async function loadDraftCount() {
  try {
    const d=await ghApi('/api/drafts');
    updateDraftBadge(d.drafts?.length||0);
  } catch(_) {}
}

function updateDraftBadge(n) {
  const badge=$('draftBadge'); if (!badge) return;
  if (n>0) { badge.textContent=n; badge.hidden=false; }
  else { badge.hidden=true; }
}

function renderDraftList(drafts) {
  const el=$('draftList'); if (!el) return;
  if (!drafts.length) { el.innerHTML='<p class="empty-hint">暂无草稿。在「文件」面板编辑后点「存草稿」。</p>'; return; }
  el.innerHTML='';
  drafts.forEach(d=>{
    const card=document.createElement('div');
    card.className='draft-card'; card.setAttribute('role','listitem');
    const ts=new Date(d.updated_at||d.created_at).toLocaleString('zh-CN',{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'});

    // 计算 diff
    const origText = d.original_content||'';
    const newText  = d.content||'';
    const ops = computeDiff(origText, newText);
    const addCount = ops.filter(o=>o.t==='+').length;
    const delCount = ops.filter(o=>o.t==='-').length;
    const diffHTML = renderDiffHTML(ops);
    const hasDiff  = addCount>0||delCount>0;

    card.innerHTML=`
      <div class="draft-card-header">
        <span class="draft-repo">${esc(d.owner)}/${esc(d.repo)}</span>
        <span class="draft-branch">@${esc(d.branch)}</span>
        <span class="draft-ts">${ts}</span>
      </div>
      <div class="draft-path">${esc(d.file_path)}</div>
      ${hasDiff
        ? `<div class="diff-stat"><span class="diff-stat-add">+${addCount}</span> <span class="diff-stat-del">-${delCount}</span> 行变更</div>`
        : `<div class="diff-stat diff-stat-none">无变更</div>`
      }
      <details class="draft-diff-wrap" open>
        <summary class="draft-preview-toggle">查看 Diff</summary>
        <div class="diff-view">${diffHTML}</div>
      </details>
      <div class="draft-commit-row">
        <label class="commit-label" style="white-space:nowrap">提交信息</label>
        <input class="draft-commit-input commit-input" value="${esc(d.commit_message)}" placeholder="提交信息" aria-label="提交信息" data-did="${esc(d.id)}" />
      </div>
      <div class="draft-actions">
        <button class="btn-primary draft-push" data-did="${esc(d.id)}" ${!hasDiff?'disabled':''}>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true"><polyline points="16 16 12 12 8 16"/><line x1="12" y1="12" x2="12" y2="21"/><path d="M20.39 18.39A5 5 0 0 0 18 9h-1.26A8 8 0 1 0 3 16.3"/></svg>
          ✔ 授权推送 GitHub
        </button>
        <button class="btn-ghost draft-reject" data-did="${esc(d.id)}">✕ 拒绝此草稿</button>
      </div>
      <div class="draft-status" id="dst-${esc(d.id)}" role="status" aria-live="polite"></div>`;
    el.appendChild(card);
  });

  // ── 授权推送（必须用户主动点击确认）────────────────────
  el.querySelectorAll('.draft-push').forEach(btn=>btn.addEventListener('click',async()=>{
    const did=btn.dataset.did;
    const card=btn.closest('.draft-card');
    const msgInput=card.querySelector('.draft-commit-input');
    const fp=card.querySelector('.draft-path')?.textContent||'';
    const repoLabel=card.querySelector('.draft-repo')?.textContent||'';
    const branchLabel=card.querySelector('.draft-branch')?.textContent||'';
    const statusEl=$('dst-'+did);
    const commitMsg=msgInput?.value.trim()||'';

    // ── 用户授权确认 ──
    const ok=confirm(
      `⚠️ 授权推送到 GitHub\n\n` +
      `仓库：${repoLabel} ${branchLabel}\n` +
      `文件：${fp}\n` +
      `提交：${commitMsg||'（空）'}\n\n` +
      `确认将把此草稿的变更提交到远程仓库。`
    );
    if (!ok) return;

    btn.disabled=true; setStatus(statusEl,'推送中…','');
    try {
      const task=await ghApi('/api/drafts/push',{
        method:'POST',
        headers:{'Content-Type':'application/json'},
        body:JSON.stringify({id:did,commit_message:commitMsg})
      });
      const r=await pollTask(task.task_id,t=>setStatus(statusEl,t.status+'…',''));
      if (r.status==='completed') {
        setStatus(statusEl,`✓ commit ${r.result?.commit_sha?.slice(0,7)||''}  已推送`,'ok');
        setTimeout(()=>loadDraftList(),1200);
      } else {
        setStatus(statusEl,'失败: '+(r.error?.message||''),'err');
        btn.disabled=false;
      }
    } catch(e) { setStatus(statusEl,'失败: '+e.message,'err'); btn.disabled=false; }
  }));

  // ── 拒绝草稿（删除）────────────────────────────────────
  el.querySelectorAll('.draft-reject').forEach(btn=>btn.addEventListener('click',async()=>{
    const did=btn.dataset.did;
    const statusEl=$('dst-'+did);
    if (!confirm('拒绝并删除此草稿？')) return;
    btn.disabled=true; setStatus(statusEl,'删除中…','');
    try {
      await ghApi('/api/drafts?id='+encodeURIComponent(did),{method:'DELETE'});
      await loadDraftList();
    } catch(e) { setStatus(statusEl,'删除失败: '+e.message,'err'); btn.disabled=false; }
  }));
}

$('refreshDrafts')?.addEventListener('click',()=>loadDraftList());
$('clearAllDrafts')?.addEventListener('click',async()=>{
  if (!confirm('确认清空所有草稿？此操作不可恢复。')) return;
  try {
    const d=await ghApi('/api/drafts?all=1',{method:'DELETE'});
    await loadDraftList();
  } catch(e) { alert('清空失败: '+e.message); }
});

// Tab 切换到草稿箱时自动刷新
document.querySelectorAll('.tab-btn').forEach(b=>b.addEventListener('click',()=>{
  if (b.dataset.tab==='drafts') loadDraftList();
}));

/* ══════════════════════════════════════════════════════
   CHAT — 普通对话 + Agent Loop（SSE）
══════════════════════════════════════════════════════ */
const chatMsgs = $('chatMessages');
// conversation history for multi-turn (simple mode only)
let chatHistory = [];

function appendMsg(role, text) {
  const wrap=document.createElement('div'); wrap.className='chat-msg chat-msg-'+role; wrap.setAttribute('role','article');
  const bubble=document.createElement('div'); bubble.className='chat-bubble'; bubble.textContent=text;
  wrap.appendChild(bubble); chatMsgs.appendChild(wrap); chatMsgs.scrollTop=chatMsgs.scrollHeight;
  return wrap;
}
function appendSys(text, isErr) {
  const d=document.createElement('div'); d.className='chat-sys'+(isErr?' chat-sys-err':'');
  d.setAttribute('role','status'); d.textContent=text;
  chatMsgs.appendChild(d); chatMsgs.scrollTop=chatMsgs.scrollHeight; return d;
}

// ── Agent 事件气泡 ────────────────────────────────────
function appendAgentThinking(text, round) {
  const d=document.createElement('div'); d.className='agent-event agent-thinking';
  d.innerHTML=`<span class="agent-icon">🧠</span><span class="agent-text">${esc(text)}</span>`;
  if (round) d.dataset.round=round;
  chatMsgs.appendChild(d); chatMsgs.scrollTop=chatMsgs.scrollHeight; return d;
}

function appendToolCard(name, input) {
  const d=document.createElement('div'); d.className='agent-event agent-tool';
  d.innerHTML=`
    <div class="tool-header">
      <span class="tool-icon">🔧</span>
      <span class="tool-name">${esc(name)}</span>
      <span class="tool-status running">执行中…</span>
    </div>
    <pre class="tool-input">${esc(input)}</pre>
    <div class="tool-result-area"></div>`;
  chatMsgs.appendChild(d); chatMsgs.scrollTop=chatMsgs.scrollHeight; return d;
}

// ── 授权卡片（write_file 调用前展示给用户）─────────────────
function appendApprovalCard(callId, filePath, content, commitMsg) {
  const d=document.createElement('div'); d.className='agent-event agent-approval';
  // 计算预览（前 400 字符）
  const preview=content.slice(0,400)+(content.length>400?'\n…（已截断）':'');
  d.innerHTML=`
    <div class="approval-header">
      <span class="approval-icon">✍️</span>
      <span class="approval-title">AI 请求写入文件</span>
      <span class="approval-badge pending">等待授权</span>
    </div>
    <div class="approval-path">${esc(filePath)}</div>
    <div class="approval-commit">提交：${esc(commitMsg||'（无提交信息）')}</div>
    <details class="approval-preview-wrap">
      <summary class="draft-preview-toggle">预览内容</summary>
      <pre class="draft-content">${esc(preview)}</pre>
    </details>
    <div class="approval-actions">
      <button class="btn-primary approval-allow" data-cid="${esc(callId)}">
        ✔ 允许写入
      </button>
      <button class="btn-danger approval-deny" data-cid="${esc(callId)}">
        ✕ 拒绝
      </button>
    </div>
    <div class="approval-status" id="aps-${esc(callId)}" role="status" aria-live="polite"></div>`;
  chatMsgs.appendChild(d); chatMsgs.scrollTop=chatMsgs.scrollHeight;

  // ── 允许 ──
  d.querySelector('.approval-allow').addEventListener('click', async()=>{
    const btn=d.querySelector('.approval-allow');
    btn.disabled=true; d.querySelector('.approval-deny').disabled=true;
    try {
      await ghApi('/api/agent/approve',{
        method:'POST',
        headers:{'Content-Type':'application/json'},
        body:JSON.stringify({call_id:callId,approved:true})
      });
      const badge=d.querySelector('.approval-badge');
      if (badge) { badge.textContent='✓ 已授权'; badge.className='approval-badge approved'; }
      d.querySelector('.approval-actions').hidden=true;
    } catch(e) {
      const s=d.querySelector('.approval-status'); if(s) s.textContent='失败: '+e.message;
      btn.disabled=false; d.querySelector('.approval-deny').disabled=false;
    }
  });

  // ── 拒绝 ──
  d.querySelector('.approval-deny').addEventListener('click', async()=>{
    const btn=d.querySelector('.approval-deny');
    btn.disabled=true; d.querySelector('.approval-allow').disabled=true;
    try {
      await ghApi('/api/agent/approve',{
        method:'POST',
        headers:{'Content-Type':'application/json'},
        body:JSON.stringify({call_id:callId,approved:false,reason:'用户拒绝'})
      });
      const badge=d.querySelector('.approval-badge');
      if (badge) { badge.textContent='✗ 已拒绝'; badge.className='approval-badge rejected'; }
      d.querySelector('.approval-actions').hidden=true;
    } catch(e) {
      const s=d.querySelector('.approval-status'); if(s) s.textContent='失败: '+e.message;
      btn.disabled=false; d.querySelector('.approval-allow').disabled=false;
    }
  });

  return d;
}

function updateToolCard(card, resultText, ok) {
  const statusEl=card.querySelector('.tool-status');
  if (statusEl) { statusEl.textContent=ok?'✓ 完成':'✗ 失败'; statusEl.className='tool-status '+(ok?'done':'fail'); }
  const area=card.querySelector('.tool-result-area');
  if (area) { area.innerHTML=`<pre class="tool-result">${esc(resultText)}</pre>`; }
}

// ── 发送 ──────────────────────────────────────────────
async function sendChat() {
  const msg=$('chatMessage').value.trim(); if (!msg) return;
  const pv=activePV();
  if (!pv) { appendSys('⚠ 请先在「API」面板配置并激活一个提供商', true); return; }
  const agentMode=$('agentMode').checked;

  appendMsg('user', msg);
  $('chatMessage').value=''; $('sendChat').disabled=true;

  if (agentMode) {
    await runAgentMode(msg, pv);
  } else {
    await runSimpleMode(msg, pv);
  }
  $('sendChat').disabled=false;
}

// ── 普通模式（单轮/多轮历史，非 agent）──────────────────
async function runSimpleMode(msg, pv) {
  chatHistory.push({role:'user', content:msg});
  const loading=appendSys('思考中…');
  try {
    const task=await api('/api/chat',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({
        message:msg, system:$('chatSystem').value.trim(),
        context: currentFileCtx?currentFileCtx.content:'',
        agent:'mobile-agent',
        provider:{name:pv.name,base_url:pv.baseURL||'',model:pv.model,api_key:pv.apiKey,kind:pv.kind||'openai',headers:pv.headers||{}},
      }),
    });
    loading.textContent=`任务 ${task.task_id} 运行中…`;
    const r=await pollTask(task.task_id,t=>{ loading.textContent=`状态: ${t.status}…`; });
    loading.remove();
    if (r.status==='completed') {
      const reply=r.result?.reply||'（无回复）';
      chatHistory.push({role:'assistant',content:reply});
      appendMsg('ai', reply);
    } else {
      appendSys('失败: '+(r.error?.message||''), true);
    }
  } catch(e) { loading.remove(); appendSys('失败: '+e.message, true); }
}

// ── Agent 模式（SSE + Tool Calling）─────────────────────
// #fix1: tool cards keyed by call_id (unique per invocation, not tool+round)
// #fix3: tool call/result messages merged into chatHistory for multi-turn continuity
// #fix7: AbortController cancels SSE when user navigates away or re-sends
let agentAbortCtrl = null;

async function runAgentMode(msg, pv) {
  if (!currentOwnerRepo) {
    $('agentRepoHint').hidden=false;
    setTimeout(()=>{ $('agentRepoHint').hidden=true; }, 3000);
    return;
  }
  const ght=getGHToken();
  if (!ght) { appendSys('⚠ 请先登录 GitHub', true); $('sendChat').disabled=false; return; }

  // #fix7: cancel any in-flight agent request
  if (agentAbortCtrl) { agentAbortCtrl.abort(); }
  agentAbortCtrl = new AbortController();
  const signal = agentAbortCtrl.signal;

  const thinkEl=appendAgentThinking('Agent 启动中…', 0);
  // #fix1: key by call_id (unique tool call ID from server)
  const toolCards=new Map();

  // #fix3: track tool calls/results for history (keyed by call_id)
  const pendingToolCalls=[]; // {call_id, tool_name, args}
  const toolResults=new Map(); // call_id → result string

  const body=JSON.stringify({
    message: msg,
    system:  $('chatSystem').value.trim(),
    history: chatHistory,
    provider:{kind:pv.kind||'openai',base_url:pv.baseURL||'',model:pv.model,api_key:pv.apiKey,headers:pv.headers||{},max_rounds:10},
    owner:   currentOwnerRepo.owner,
    repo:    currentOwnerRepo.repo,
    branch:  currentOwnerRepo.branch,
  });

  const headers={'Content-Type':'application/json'};
  if (ght) headers['X-GitHub-Token']=ght;
  const appTok=getAppToken(); if (appTok) headers['X-App-Token']=appTok;

  try {
    const res=await fetch('/api/agent/run',{method:'POST',headers,body,signal});
    if (!res.ok) {
      let errMsg='HTTP '+res.status;
      try { const d=await res.json(); errMsg=d?.error?.message||errMsg; } catch(_) {}
      thinkEl.querySelector('.agent-text').textContent='失败: '+errMsg;
      thinkEl.className='agent-event agent-thinking agent-err';
      return;
    }

    const reader=res.body.getReader();
    const decoder=new TextDecoder();
    let buf='';
    let finalReply='';

    while (true) {
      const {done,value}=await reader.read();
      if (done) break;
      buf+=decoder.decode(value,{stream:true});
      const lines=buf.split('\n');
      buf=lines.pop();

      for (const line of lines) {
        if (!line.startsWith('data: ')) continue;
        let ev;
        try { ev=JSON.parse(line.slice(6)); } catch(_) { continue; }

        switch (ev.type) {
          case 'thinking':
            thinkEl.querySelector('.agent-text').textContent=ev.content;
            break;

          case 'tool_call': {
            const card=appendToolCard(ev.tool, ev.input||'');
            toolCards.set(ev.call_id||ev.tool+'_'+ev.round, card);
            pendingToolCalls.push({call_id:ev.call_id, tool_name:ev.tool, args:ev.input||''});
            break;
          }

          case 'tool_approval': {
            // Agent wants to write_file — render authorisation card
            const approvalCard=appendApprovalCard(ev.call_id, ev.file_path||'', ev.content||'', ev.commit_message||'');
            toolCards.set(ev.call_id, approvalCard);
            // Record in pendingToolCalls so history is complete regardless of decision.
            // args must be the original JSON arguments string (path/content/commit_message),
            // NOT the raw file content — that would pollute the AI context window.
            const approvalArgs=JSON.stringify({
              path: ev.file_path||'',
              content: ev.content||'',
              commit_message: ev.commit_message||''
            });
            pendingToolCalls.push({call_id:ev.call_id, tool_name:'write_file', args:approvalArgs});
            break;
          }

          case 'tool_approved': {
            const card=toolCards.get(ev.call_id);
            if (card) {
              const badge=card.querySelector('.approval-badge');
              if (badge) { badge.textContent='✓ 已授权执行'; badge.className='approval-badge approved'; }
              const actions=card.querySelector('.approval-actions');
              if (actions) actions.hidden=true;
            }
            break;
          }

          case 'tool_rejected': {
            const card=toolCards.get(ev.call_id);
            if (card) {
              const badge=card.querySelector('.approval-badge');
              if (badge) { badge.textContent='✗ 已拒绝'; badge.className='approval-badge rejected'; }
              const actions=card.querySelector('.approval-actions');
              if (actions) actions.hidden=true;
            }
            if (ev.call_id) toolResults.set(ev.call_id, ev.content||'已拒绝');
            break;
          }

          case 'tool_result': {
            const card=toolCards.get(ev.call_id||ev.tool+'_'+ev.round);
            if (card) updateToolCard(card, ev.content, !ev.content?.startsWith('Tool execution error'));
            if (ev.call_id) toolResults.set(ev.call_id, ev.content||'');
            break;
          }

          case 'text':
            finalReply=ev.content;
            break;

          case 'done':
            if (finalReply) {
              thinkEl.remove();
              appendMsg('ai', finalReply);
              // #fix3: build complete history entry including tool calls
              chatHistory.push({role:'user',content:msg});
              // assistant turn with tool_calls array (if any)
              const assistantEntry={role:'assistant',content:finalReply,tool_calls:[]};
              for (const tc of pendingToolCalls) {
                assistantEntry.tool_calls.push({id:tc.call_id,type:'function',function:{name:tc.tool_name,arguments:tc.args}});
              }
              chatHistory.push(assistantEntry);
              // tool result turns
              for (const tc of pendingToolCalls) {
                const result=toolResults.get(tc.call_id)||'';
                chatHistory.push({role:'tool',content:result,tool_call_id:tc.call_id});
              }
              // keep history bounded
              if (chatHistory.length>60) chatHistory=chatHistory.slice(-60);
            } else {
              thinkEl.querySelector('.agent-text').textContent='✓ '+ev.content;
            }
            break;

          case 'error':
            thinkEl.querySelector('.agent-text').textContent='✗ '+ev.content;
            thinkEl.className='agent-event agent-thinking agent-err';
            break;
        }
        chatMsgs.scrollTop=chatMsgs.scrollHeight;
      }
    }
  } catch(e) {
    if (e.name==='AbortError') return; // user cancelled, silently ignore
    thinkEl.querySelector('.agent-text').textContent='连接失败: '+e.message;
    thinkEl.className='agent-event agent-thinking agent-err';
  } finally {
    agentAbortCtrl=null;
  }
}

// agent mode toggle hint
$('agentMode').addEventListener('change',()=>{
  if ($('agentMode').checked && !currentOwnerRepo) {
    $('agentRepoHint').hidden=false;
  } else {
    $('agentRepoHint').hidden=true;
  }
});

$('sendChat').addEventListener('click', sendChat);
$('chatMessage').addEventListener('keydown',e=>{
  if (e.key==='Enter'&&(e.ctrlKey||e.metaKey)) { e.preventDefault(); sendChat(); }
});

/* ══════════════════════════════════════════════════════
   INIT
══════════════════════════════════════════════════════ */
function initApp() {
  renderPVList();
  loadRepoList();
  $('appToken').value = loadCfg().appToken||'';
  loadDraftCount();
}

if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(()=>{});
}

checkAuth();
