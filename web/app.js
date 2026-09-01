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
let currentOwnerRepo = null;
let currentDirPath   = '';
let currentFileCtx   = null; // {path, content}

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
  $('saveFile').disabled=true; $('injectContext').disabled=true;
  setStatus($('editorStatus'),'','');
  const {owner,repo,branch}=currentOwnerRepo;
  try {
    const d=await ghApi('/api/git/file?'+new URLSearchParams({owner,repo,branch,path:filePath}));
    $('fileEditor').value=d.file.content; $('fileEditor').disabled=false;
    $('saveFile').disabled=false; $('injectContext').disabled=false;
    $('commitMessage').value='fix: update '+d.file.name;
    setStatus($('editorStatus'),`${d.file.size} 字节 · SHA ${d.file.sha.slice(0,7)}`,'ok');
  } catch(e) { $('fileEditor').value=''; $('fileEditor').disabled=false; setStatus($('editorStatus'),'加载失败: '+e.message,'err'); }
}

$('refreshFiles').addEventListener('click',()=>loadDir(currentDirPath));

$('saveFile').addEventListener('click',async()=>{
  if (!currentOwnerRepo) return;
  const fp=$('editorFilePath').textContent.trim(); if (!fp||fp==='未打开文件') return;
  const ght=getGHToken(); if (!ght) { setStatus($('editorStatus'),'请先登录 GitHub','err'); return; }
  const {owner,repo,branch}=currentOwnerRepo;
  $('saveFile').disabled=true; setStatus($('editorStatus'),'提交中…','');
  try {
    const task=await api('/api/git/sync',{
      method:'POST',
      headers:{'Content-Type':'application/json','X-GitHub-Token':ght},
      body:JSON.stringify({owner,repo,branch,file_path:fp,content:$('fileEditor').value,commit_message:$('commitMessage').value.trim()||'update: '+fp})
    });
    const r=await pollTask(task.task_id,t=>setStatus($('editorStatus'),t.status+'…',''));
    r.status==='completed'
      ? setStatus($('editorStatus'),`✓ commit ${r.result?.commit_sha?.slice(0,7)||''}`,'ok')
      : setStatus($('editorStatus'),'失败: '+(r.error?.message||''),'err');
  } catch(e) { setStatus($('editorStatus'),'失败: '+e.message,'err'); }
  finally { $('saveFile').disabled=false; }
});

$('injectContext').addEventListener('click',()=>{
  const fp=$('editorFilePath').textContent.trim(); if (!fp||fp==='未打开文件') return;
  currentFileCtx={path:fp,content:$('fileEditor').value};
  $('contextFileName').textContent=fp; $('contextBar').hidden=false;
  switchTab('chat');
});
$('clearContext').addEventListener('click',()=>{ currentFileCtx=null; $('contextBar').hidden=true; $('contextFileName').textContent=''; });

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
            // #fix1: key by call_id, not tool+'_'+round
            const card=appendToolCard(ev.tool, ev.input||'');
            toolCards.set(ev.call_id||ev.tool+'_'+ev.round, card);
            // #fix3: record for history
            pendingToolCalls.push({call_id:ev.call_id, tool_name:ev.tool, args:ev.input||''});
            break;
          }

          case 'tool_result': {
            // #fix1: lookup by call_id
            const card=toolCards.get(ev.call_id||ev.tool+'_'+ev.round);
            if (card) updateToolCard(card, ev.content, !ev.content?.startsWith('Tool execution error'));
            // #fix3: record result
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
}

if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(()=>{});
}

checkAuth();
