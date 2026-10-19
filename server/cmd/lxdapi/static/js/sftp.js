// SFTP client over the JSON-over-WebSocket protocol served by /ws/sftp.
// Every request carries a monotonically increasing id; replies echo that id
// so the caller can match them. Long-running operations (download chunks,
// upload progress) are delivered as push messages and are matched by action.

class SFTPClient {
  constructor(token) {
    this.token = token;
    this.ws = null;
    this.seq = 0;
    // id -> {resolve, reject, timer}
    this.pending = new Map();
    this.pushHandlers = [];
  }

  connect() {
    return new Promise((resolve, reject) => {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const ws = new WebSocket(`${protocol}//${window.location.host}/ws/sftp?token=${this.token}`);

      ws.onopen = () => {
        this.ws = ws;
        resolve();
      };
      ws.onerror = () => {
        this._closeWs();
        reject(new Error('WebSocket 连接失败'));
      };
      ws.onclose = () => this._onClose();
      ws.onmessage = (event) => this._onMessage(event.data);
    });
  }

  _closeWs() {
    if (this.ws) {
      try { this.ws.close(); } catch (e) {}
      this.ws = null;
    }
  }

  close() { this._closeWs(); }

  _onClose() {
    for (const p of this.pending.values()) {
      clearTimeout(p.timer);
      p.reject(new Error('连接已断开'));
    }
    this.pending.clear();
    this.ws = null;
  }

  _onMessage(data) {
    let msg;
    try { msg = JSON.parse(data); } catch (e) { return; }

    if (typeof msg.id === 'number') {
      const p = this.pending.get(msg.id);
      if (p) {
        clearTimeout(p.timer);
        this.pending.delete(msg.id);
        p.resolve(msg);
      }
      return;
    }

    for (const handler of this.pushHandlers.slice()) {
      try { handler(msg); } catch (e) {}
    }
  }

  request(action, payload, timeout = 30000) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('未连接到容器'));
    }
    const seq = ++this.seq;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(seq);
        reject(new Error('操作超时'));
      }, timeout);
      this.pending.set(seq, { resolve, reject, timer });
      this.ws.send(JSON.stringify({ id: seq, action, ...payload }));
    });
  }

  onPush(handler) {
    this.pushHandlers.push(handler);
    return () => {
      const i = this.pushHandlers.indexOf(handler);
      if (i >= 0) this.pushHandlers.splice(i, 1);
    };
  }

  async list(path) {
    const r = await this.request('list', { path: path || '/' });
    if (!r.ok) throw new Error(r.error || '列举失败');
    return r.files || [];
  }

  async stat(path) {
    const r = await this.request('stat', { path });
    if (!r.ok) throw new Error(r.error || '获取文件信息失败');
    return (r.files || [])[0];
  }

  async mkdir(path, perm) {
    const r = await this.request('mkdir', { path, perm });
    if (!r.ok) throw new Error(r.error || '创建目录失败');
  }

  async delete(path) {
    const r = await this.request('delete', { path });
    if (!r.ok) throw new Error(r.error || '删除失败');
  }

  async edit(path, content) {
    const r = await this.request('edit', { path, content, size: content.length });
    if (!r.ok) throw new Error(r.error || '保存失败');
  }

  // Uploads a File/Blob in 64 KiB base64 chunks. onProgress(percent) is called
  // after every chunk the server acknowledges.
  async upload(file, path, onProgress) {
    const size = file.size;
    const r = await this.request('upload', { path, size });
    if (!r.ok) throw new Error(r.error || '上传被拒绝');

    const chunkSize = 64 * 1024;
    let uploaded = 0;
    for (let offset = 0; offset < size; offset += chunkSize) {
      const slice = file.slice(offset, Math.min(offset + chunkSize, size));
      const data = await readBlobAsBase64(slice);
      await this.request('upload_data', { offset, data });
      uploaded += slice.size;
      if (onProgress) onProgress(Math.round(uploaded * 100 / size));
    }

    // The server closes the file and reports the final size once it sees the
    // terminator instead of another upload_data packet.
    const done = await this.request('upload_done', { path });
    if (!done.ok) throw new Error(done.error || '上传完成确认失败');
    return done.size || uploaded;
  }

  // Streams a remote file to the caller. onChunk(base64Data, offset) is called
  // for every chunk; the promise resolves with the total size once done.
  async download(path, onChunk) {
    const start = await this.request('download', { path });
    if (!start.ok) throw new Error(start.error || '下载失败');

    return new Promise((resolve, reject) => {
      const handler = (msg) => {
        if (msg.action === 'download_data') {
          if (onChunk) onChunk(msg.data, msg.offset);
        } else if (msg.action === 'download_done') {
          cleanup();
          resolve(msg.size);
        } else if (msg.action === 'download' && msg.error) {
          cleanup();
          reject(new Error(msg.error));
        }
      };
      const cleanup = () => {
        const i = this.pushHandlers.indexOf(handler);
        if (i >= 0) this.pushHandlers.splice(i, 1);
      };
      this.pushHandlers.push(handler);
    });
  }

  ping() {
    return this.request('ping', {}, 5000).catch(() => {});
  }
}

function readBlobAsBase64(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const base64 = reader.result.split(',')[1];
      resolve(base64);
    };
    reader.onerror = () => reject(new Error('读取文件失败'));
    reader.readAsDataURL(blob);
  });
}

function formatSize(bytes) {
  if (bytes == null) return '-';
  if (bytes < 1024) return bytes + ' B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  let n = bytes;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(1) + ' ' + units[i];
}

function formatDate(iso) {
  if (!iso) return '-';
  try { return new Date(iso).toLocaleString(); } catch (e) { return iso; }
}

function escHtml(str) {
  return String(str).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function b64Encode(str) {
  return btoa(unescape(encodeURIComponent(str)));
}

// UI layer that wires the SFTP client to the page.
class SFTPUI {
  constructor(containerName, token) {
    this.containerName = containerName;
    this.token = token;
    this.client = null;
    this.path = '/';
    this.connected = false;

    this.$statusDot = document.getElementById('status-dot');
    this.$statusText = document.getElementById('status-text');
    this.$connectBtn = document.getElementById('connect-btn');
    this.$breadcrumb = document.getElementById('breadcrumb');
    this.$tbody = document.getElementById('file-tbody');
    this.$empty = document.getElementById('empty-state');
    this.$error = document.getElementById('error-state');
    this.$loading = document.getElementById('loading-state');
    this.$errorMsg = document.getElementById('error-msg');
    this.$toasts = document.getElementById('toasts');

    this.$connectBtn.addEventListener('click', () => this.toggleConnection());
    document.getElementById('btn-refresh').addEventListener('click', () => this.refresh());
    document.getElementById('btn-mkdir').addEventListener('click', () => this.showMkdir());
    document.getElementById('btn-upload').addEventListener('click', () => this.showUpload());

    this._initModals();
    this._renderBreadcrumb();
  }

  _setStatus(state, text) {
    this.connected = state === 'connected';
    this.$statusDot.className = 'status-dot ' + state;
    this.$statusText.textContent = text;
    this.$connectBtn.textContent = this.connected ? '断开' : '连接';
    this.$connectBtn.className = this.connected
      ? 'bg-red-500 hover:bg-red-600 text-white px-3 lg:px-4 py-1 lg:py-1.5 rounded-md text-xs lg:text-sm font-semibold transition-all'
      : 'bg-blue-500 hover:bg-blue-600 text-white px-3 lg:px-4 py-1 lg:py-1.5 rounded-md text-xs lg:text-sm font-semibold transition-all';
  }

  toggleConnection() {
    if (this.connected) { this.disconnect(); return; }
    this.connect();
  }

  async connect() {
    this._setStatus('connecting', '连接中...');
    this._hideAll();
    this.$loading.classList.remove('hidden');
    this.$loading.querySelector('p').textContent = '正在连接容器...';

    try {
      this.client = new SFTPClient(this.token);
      await this.client.connect();
      this._setStatus('connected', '已连接');
      this.toast('已连接到容器 ' + this.containerName, 'success');
      await this.refresh();
    } catch (err) {
      this._setStatus('', '未连接');
      this.showError('连接失败: ' + err.message);
      if (this.client) { this.client.close(); this.client = null; }
    }
  }

  disconnect() {
    if (this.client) { this.client.close(); this.client = null; }
    this._setStatus('', '已断开');
    this._showOverlay();
  }

  _showOverlay() {
    const overlay = document.getElementById('disconnect-overlay');
    overlay.classList.remove('hidden');
    overlay.classList.add('flex');
    document.getElementById('overlay-close').addEventListener('click', () => window.close(), { once: true });
  }

  _hideAll() {
    this.$empty.classList.add('hidden');
    this.$error.classList.add('hidden');
    this.$loading.classList.add('hidden');
    this.$tbody.innerHTML = '';
  }

  showError(message) {
    this._hideAll();
    this.$errorMsg.textContent = message;
    this.$error.classList.remove('hidden');
    document.getElementById('retry-link').onclick = () => this.refresh();
  }

  async refresh() {
    if (!this.connected) return;
    this._hideAll();
    this.$loading.classList.remove('hidden');
    this.$loading.querySelector('p').textContent = '正在列举文件...';
    try {
      const files = await this.client.list(this.path);
      this._render(files);
      this._renderBreadcrumb();
    } catch (err) {
      this.showError('列举文件失败: ' + err.message);
    }
  }

  _render(files) {
    this._hideAll();
    if (!files || files.length === 0) {
      this.$empty.classList.remove('hidden');
      return;
    }

    const frag = document.createDocumentFragment();
    for (const f of files) {
      const isDir = f.type === 'directory';
      const name = escHtml(f.name);
      const safePath = encodeURIComponent(f.name);

      const tr = document.createElement('tr');
      tr.className = 'file-row border-b border-gray-100';
      tr.innerHTML = `
        <td class="px-4 lg:px-6 py-2 lg:py-3">${isDir
          ? '<svg class="w-5 h-5 text-blue-500" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"></path></svg>'
          : '<svg class="w-5 h-5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"></path></svg>'}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3">${isDir
          ? `<button class="flex items-center gap-2 text-left hover:text-blue-600 transition-colors" data-action="cd" data-name="${encodeURIComponent(name)}"><span class="font-medium">${name}</span></button>`
          : `<div class="flex items-center gap-2"><span class="font-medium">${name}</span></div>`}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 hidden lg:table-cell text-right tabular-nums">${isDir ? '-' : formatSize(f.size)}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 hidden lg:table-cell">${formatDate(f.modTime)}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 hidden lg:table-cell">${isDir ? '目录' : f.type === 'link' ? '链接' : '文件'}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 text-right">
          <div class="flex justify-end gap-1">
            ${!isDir ? `<button class="action-btn text-gray-400 hover:text-blue-600 p-1 rounded" title="下载" data-action="download" data-name="${encodeURIComponent(name)}"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12"></path></svg></button>` : ''}
            ${!isDir ? `<button class="action-btn text-gray-400 hover:text-blue-600 p-1 rounded" title="编辑" data-action="edit" data-name="${encodeURIComponent(name)}"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v9a2 2 0 002 2h9a2 2 0 002-2v-5m-6 0v6m6-6H9m10-2a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg></button>` : ''}
            <button class="action-btn text-gray-400 hover:text-red-600 p-1 rounded" title="删除" data-action="delete" data-name="${encodeURIComponent(name)}"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v3m5-3m0 0m-5 3m5-3H4"></path></svg></button>
          </div>
        </td>`;

      tr.addEventListener('click', (e) => {
        const btn = e.target.closest('button[data-action]');
        if (!btn) return;
        e.stopPropagation();
        const target = this._join(this.path, decodeURIComponent(btn.dataset.name));
      const name = decodeURIComponent(btn.dataset.name);
      const action = btn.dataset.action;
        if (action === 'cd') { this.path = target; this.refresh(); }
        else if (action === 'download') { this.download(target, name); }
        else if (action === 'edit') { this.edit(target, name); }
        else if (action === 'delete') { this.confirmDelete(target, name); }
      });
      frag.appendChild(tr);
    }
    this.$tbody.appendChild(frag);
  }

  _renderBreadcrumb() {
    const parts = this.path.split('/').filter(Boolean);
    let html = `<button class="flex items-center gap-1 text-gray-500 hover:text-blue-600 transition-colors flex-shrink-0" data-path="/">
      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 00h3m3-11a1 1 0 011-1h3a1 1 0 011 1v3m-1 4l-1-1m-2 0L7 16m4 0l-4-4m4 4l1-1"></path></svg>
      根目录
    </button>`;
    let acc = '';
    for (const p of parts) {
      acc += '/' + p;
      html += `<span class="text-gray-400 flex-shrink-0">/</span>`;
      html += `<button class="text-gray-500 hover:text-blue-600 transition-colors flex-shrink-0" data-path="${escHtml(acc)}">${escHtml(p)}</button>`;
    }
    this.$breadcrumb.innerHTML = html;
  }

  _join(base, name) {
    if (base === '/') return '/' + name;
    return (base.endsWith('/') ? base : base + '/') + name;
  }

  toast(message, kind = 'info') {
    const colors = { success: 'bg-green-50 text-green-800 border-green-200', error: 'bg-red-50 text-red-800 border-red-200', info: 'bg-blue-50 text-blue-800 border-blue-200' };
    const el = document.createElement('div');
    el.className = (colors[kind] || colors.info) + ' border rounded-lg shadow-lg px-4 py-3 text-sm max-w-xs';
    el.textContent = message;
    this.$toasts.appendChild(el);
    setTimeout(() => { el.style.opacity = '0'; el.style.transition = 'opacity .3s'; setTimeout(() => el.remove(), 300); }, 4000);
  }

  _initModals() {
    // ---- breadcrumb delegation ----
    this.$breadcrumb.addEventListener('click', (e) => {
      const btn = e.target.closest('button[data-path]');
      if (btn) { this.path = btn.dataset.path; this.refresh(); }
    });

    // ---- upload modal ----
    const $uploadModal = document.getElementById('upload-modal');
    document.getElementById('upload-close').addEventListener('click', () => $uploadModal.classList.add('hidden'));
    document.getElementById('upload-cancel').addEventListener('click', () => $uploadModal.classList.add('hidden'));
    const $input = document.getElementById('upload-input');
    const $drop = document.getElementById('upload-drop');
    $drop.addEventListener('click', () => $input.click());
    $drop.addEventListener('dragover', (e) => { e.preventDefault(); $drop.classList.add('drag-over'); });
    $drop.addEventListener('dragleave', () => $drop.classList.remove('drag-over'));
    $drop.addEventListener('drop', (e) => {
      e.preventDefault();
      $drop.classList.remove('drag-over');
      if (e.dataTransfer.files.length) this._doUpload(e.dataTransfer.files);
    });
    $input.addEventListener('change', () => { if ($input.files.length) this._doUpload($input.files); });

    // ---- mkdir modal ----
    const $mkdirModal = document.getElementById('mkdir-modal');
    const $mkdirInput = document.getElementById('mkdir-input');
    document.getElementById('mkdir-close').addEventListener('click', () => $mkdirModal.classList.add('hidden'));
    document.getElementById('mkdir-cancel').addEventListener('click', () => $mkdirModal.classList.add('hidden'));
    document.getElementById('mkdir-ok').addEventListener('click', async () => {
      const name = $mkdirInput.value.trim();
      if (!name) { alert('请输入文件夹名称'); return; }
      try {
        await this.client.mkdir(this._join(this.path, name));
        $mkdirModal.classList.add('hidden');
        $mkdirInput.value = '';
        this.toast('目录创建成功', 'success');
        this.refresh();
      } catch (err) { alert('创建失败: ' + err.message); }
    });

    // ---- edit modal ----
    const $editModal = document.getElementById('edit-modal');
    const $editContent = document.getElementById('edit-content');
    const $editPath = document.getElementById('edit-path');
    const $sizeWarn = document.getElementById('edit-size-warn');
    document.getElementById('edit-close').addEventListener('click', () => $editModal.classList.add('hidden'));
    document.getElementById('edit-cancel').addEventListener('click', () => $editModal.classList.add('hidden'));
    document.getElementById('edit-save').addEventListener('click', async () => {
      const target = $editPath.dataset.path;
      if (!target) return;
      try {
        await this.client.edit(target, b64Encode($editContent.value));
        $editModal.classList.add('hidden');
        this.toast('文件已保存', 'success');
      } catch (err) { alert('保存失败: ' + err.message); }
    });
    this._editState = { pathEl: $editPath, contentEl: $editContent, sizeWarn: $sizeWarn, modal: $editModal };

    // ---- delete modal ----
    const $deleteModal = document.getElementById('delete-modal');
    const $deleteMsg = document.getElementById('delete-msg');
    let deletingTarget = null;
    document.getElementById('delete-cancel').addEventListener('click', () => $deleteModal.classList.add('hidden'));
    document.getElementById('delete-ok').addEventListener('click', async () => {
      if (!deletingTarget) return;
      try {
        await this.client.delete(deletingTarget);
        $deleteModal.classList.add('hidden');
        this.toast('已删除', 'success');
        this.refresh();
      } catch (err) { alert('删除失败: ' + err.message); }
    });
    this._deleteState = { msgEl: $deleteMsg, modal: $deleteModal, setTarget: (t) => { deletingTarget = t; } };
  }

  showMkdir() {
    if (!this.connected) { this.toast('请先连接容器'); return; }
    document.getElementById('mkdir-input').value = '';
    document.getElementById('mkdir-modal').classList.remove('hidden');
    document.getElementById('mkdir-input').focus();
  }

  showUpload() {
    if (!this.connected) { this.toast('请先连接容器'); return; }
    document.getElementById('upload-queue').innerHTML = '';
    document.getElementById('upload-modal').classList.remove('hidden');
  }

  async _doUpload(files) {
    if (!this.connected) { this.toast('请先连接容器'); return; }
    const queue = document.getElementById('upload-queue');
    queue.innerHTML = '';

    for (const file of files) {
      const target = this._join(this.path, file.name);
      const row = document.createElement('div');
      row.className = 'text-sm';
      row.innerHTML = `
        <div class="flex justify-between items-center gap-2">
          <span class="truncate font-medium">${escHtml(file.name)}</span>
          <span class="text-gray-500 text-xs">0%</span>
        </div>
        <div class="w-full bg-gray-200 rounded-full h-1.5 mt-1"><div class="bg-blue-500 h-1.5 rounded-full" style="width:0%"></div></div>`;
      queue.appendChild(row);

      const bar = row.querySelector('div > div');
      const pct = row.querySelector('span:last-child');
      try {
        await this.client.upload(file, target, (p) => {
          bar.style.width = p + '%';
          pct.textContent = p + '%';
        });
        row.innerHTML = `<div class="flex items-center gap-1 text-green-600"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg><span class="text-xs font-medium">${escHtml(file.name)} 上传成功</span></div>`;
      } catch (err) {
        row.innerHTML = `<div class="flex items-center gap-1 text-red-600"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path></svg><span class="text-xs font-medium">${escHtml(file.name)}: ${escHtml(err.message)}</span></div>`;
      }
    }
  }

  async download(target, name) {
    try {
      const chunks = [];
      await this.client.download(target, (data) => { chunks.push(data); });

      const binary = atob(chunks.join(''));
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);

      const blob = new Blob([bytes], { type: 'application/octet-stream' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      this.toast('下载开始', 'info');
    } catch (err) {
      alert('下载失败: ' + err.message);
    }
  }

  async edit(target, name) {
    const info = await this.client.stat(target);
    if (!info) throw new Error('文件不存在');
    this._editState.pathEl.textContent = target;
    this._editState.pathEl.dataset.path = target;
    this._editState.sizeWarn.classList.toggle('hidden', info.size <= 1024 * 1024);

    const chunks = [];
    let stop;
    const done = new Promise((resolve, reject) => {
      stop = this.client.onPush((msg) => {
        if (msg.action === 'download_data') chunks.push(msg.data);
        else if (msg.action === 'download_done') { stop(); resolve(); }
        else if (msg.action === 'download' && msg.error) { stop(); reject(new Error(msg.error)); }
      });
    });

    const resp = await this.client.request('download', { path: target });
    if (!resp.ok) {
      stop();
      throw new Error(resp.error || '读取失败');
    }
    await done;

    this._editState.contentEl.value = atob(chunks.join(''));
    document.getElementById('edit-title').textContent = '编辑: ' + name;
    document.getElementById('edit-modal').classList.remove('hidden');
  }

  confirmDelete(target, name) {
    this._deleteState.setTarget(target);
    this._deleteState.msgEl.textContent = `确定要删除 "${name}" 吗？此操作不可恢复。`;
    document.getElementById('delete-modal').classList.remove('hidden');
  }
}