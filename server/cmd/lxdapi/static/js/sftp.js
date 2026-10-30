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
    // id -> {onData, resolve, reject, timer, touch}，下载流专用：
    // 服务端给 download_data / download_done 都回填了请求 id，且分片的 ok=false，
    // 不能走 pending 的"首帧即应答"匹配，必须按 id 逐片收集到 done 为止。
    this.streams = new Map();
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
    const why = this.fatalError || '连接已断开';
    for (const p of this.pending.values()) {
      clearTimeout(p.timer);
      p.reject(new Error(why));
    }
    this.pending.clear();
    for (const s of this.streams.values()) {
      clearTimeout(s.timer);
      s.reject(new Error(why));
    }
    this.streams.clear();
    this.ws = null;
  }

  _onMessage(data) {
    let msg;
    try { msg = JSON.parse(data); } catch (e) { return; }

    // 下载流的分片 / 结束 / 错误帧都带请求 id，先于 pending 匹配处理：
    // 否则第一个 download_data 分片会被误当成 download 请求的应答（ok=false）。
    if (typeof msg.id === 'number' && this.streams.has(msg.id)) {
      const s = this.streams.get(msg.id);
      if (msg.action === 'download_data') {
        s.touch();
        if (s.onData) s.onData(msg.data, msg.offset);
      } else if (msg.action === 'download_done') {
        this._endStream(msg.id);
        s.resolve(msg.size);
      } else if (msg.error) {
        this._endStream(msg.id);
        s.reject(new Error(msg.error));
      }
      return;
    }

    if (typeof msg.id === 'number') {
      const p = this.pending.get(msg.id);
      if (p) {
        clearTimeout(p.timer);
        this.pending.delete(msg.id);
        p.resolve(msg);
        return;
      }
    }

    // 服务端在请求到来之前就失败（例如容器缺 sftp-server）时发出的错误帧
    // 不对应任何请求 id。作为致命错误处理，让等待中的请求拿到真实原因，
    // 而不是在 WS 关闭后被统一盖成"连接已断开"。
    if (msg && msg.error) {
      this.fatalError = msg.error;
      for (const p of this.pending.values()) {
        clearTimeout(p.timer);
        p.reject(new Error(msg.error));
      }
      this.pending.clear();
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
    // 首次连接时服务端可能要在容器内自动安装 sftp-server，
    // 给 list 较长的超时，避免把安装时间误报为操作超时。
    const r = await this.request('list', { path: path || '/' }, 180000);
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

  async createFile(path) {
    const r = await this.request('create_file', { path });
    if (!r.ok) throw new Error(r.error || '新建文件失败');
  }

  async rename(oldPath, newPath) {
    const r = await this.request('rename', { path: oldPath, new_path: newPath });
    if (!r.ok) throw new Error(r.error || '重命名失败');
  }

  async chmod(path, perm) {
    const r = await this.request('chmod', { path, perm });
    if (!r.ok) throw new Error(r.error || '修改权限失败');
  }

  async chown(path, uid, gid) {
    const r = await this.request('chown', { path, uid, gid });
    if (!r.ok) throw new Error(r.error || '修改属主失败');
  }

  async copy(src, dst) {
    const r = await this.request('copy', { path: src, new_path: dst }, 300000);
    if (!r.ok) throw new Error(r.error || '复制失败');
  }

  async move(src, dst) {
    const r = await this.request('move', { path: src, new_path: dst }, 300000);
    if (!r.ok) throw new Error(r.error || '移动失败');
  }

  async archive(dir, names, format, name) {
    const r = await this.request('archive', { path: dir, names, format, name }, 300000);
    if (!r.ok) throw new Error(r.error || '打包失败');
    return r.path;
  }

  async extract(pkg, targetDir) {
    const r = await this.request('extract', { path: pkg, new_path: targetDir }, 300000);
    if (!r.ok) throw new Error(r.error || '解压失败');
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

  _endStream(id) {
    const s = this.streams.get(id);
    if (s) clearTimeout(s.timer);
    this.streams.delete(id);
  }

  // Fetches a whole remote file: sends one download request and collects
  // every download_data frame carrying this request's id until the server
  // sends download_done (or an error frame). onChunk(base64Data, offset) is
  // called per chunk; resolves with the total size. Deliberately bypasses
  // request()/pending: the server echoes the request id on every chunk and
  // chunk frames have ok=false, which pending matching would misread as the
  // (failed) reply. An inactivity timer (reset on each chunk) guards hangs,
  // including the empty-file case where only download_done ever arrives.
  fetch(path, onChunk) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('未连接到容器'));
    }
    const seq = ++this.seq;
    return new Promise((resolve, reject) => {
      const s = {
        onData: onChunk,
        resolve,
        reject,
        timer: null,
        touch: () => {
          clearTimeout(s.timer);
          s.timer = setTimeout(() => {
            this.streams.delete(seq);
            reject(new Error('下载超时'));
          }, 120000);
        },
      };
      s.touch();
      this.streams.set(seq, s);
      try {
        this.ws.send(JSON.stringify({ id: seq, action: 'download', path }));
      } catch (e) {
        this._endStream(seq);
        reject(e);
      }
    });
  }

  // Streams a remote file to the caller. onChunk(base64Data, offset) is called
  // for every chunk; the promise resolves with the total size once done.
  async download(path, onChunk) {
    return this.fetch(path, onChunk);
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
    document.getElementById('btn-newfile').addEventListener('click', () => this.showNewFile());
    document.getElementById('btn-archive').addEventListener('click', () => this.showArchiveSelected());
    const $dirInput = document.getElementById('dir-input');
    document.getElementById('btn-uploaddir').addEventListener('click', () => { if (this.connected) $dirInput.click(); else this.toast('请先连接容器', 'error'); });
    $dirInput.addEventListener('change', () => { this.uploadDirectory($dirInput.files); $dirInput.value = ''; });
    document.addEventListener('click', (e) => {
      const menu = document.getElementById('row-menu');
      if (menu && !menu.classList.contains('hidden') && !menu.contains(e.target)) menu.classList.add('hidden');
    });

    this._initModals();
    this._renderBreadcrumb();

    // 与 SSH 控制台页一致：打开页面自动连接，关闭窗口/离开页面自动断开，
    // 不再需要手动点“连接”。pagehide 与 beforeunload 都注册，
    // disconnect() 对未连接状态是安全的，两个事件先后触发也不会重复出错。
    window.addEventListener('pagehide', () => this.disconnect());
    window.addEventListener('beforeunload', () => this.disconnect());
    this.connect();
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
      // name 保持原始文件名：data-name/下载/路径拼接都用它；
      // 仅展示处用 escHtml(name) 转义，防止 HTML 注入。
      const name = f.name;
      const dispName = escHtml(f.name);
      const safePath = encodeURIComponent(f.name);

      const tr = document.createElement('tr');
      tr.className = 'file-row border-b border-gray-100';
      tr.innerHTML = `
        <td class="px-4 lg:px-6 py-2 lg:py-3">${isDir
          ? '<svg class="w-5 h-5 text-blue-500" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"></path></svg>'
          : '<svg class="w-5 h-5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"></path></svg>'}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3"><div class="flex items-center gap-2"><input type="checkbox" class="row-check w-4 h-4 flex-shrink-0" data-name="${encodeURIComponent(name)}">${isDir
          ? `<button class="flex items-center gap-2 text-left hover:text-blue-600 transition-colors" data-action="cd" data-name="${encodeURIComponent(name)}"><span class="font-medium">${dispName}</span></button>`
          : `<span class="font-medium">${dispName}</span>`}</div></td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 hidden lg:table-cell text-right tabular-nums">${isDir ? '-' : formatSize(f.size)}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 hidden lg:table-cell">${formatDate(f.modTime)}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 hidden lg:table-cell">${isDir ? '目录' : f.type === 'link' ? '链接' : '文件'}</td>
        <td class="px-4 lg:px-6 py-2 lg:py-3 text-right">
          <div class="flex justify-end gap-1">
            ${!isDir ? `<button class="action-btn text-gray-400 hover:text-blue-600 p-1 rounded" title="下载" data-action="download" data-name="${encodeURIComponent(name)}"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12"></path></svg></button>` : ''}
            ${!isDir ? `<button class="action-btn text-gray-400 hover:text-blue-600 p-1 rounded" title="编辑" data-action="edit" data-name="${encodeURIComponent(name)}"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v9a2 2 0 002 2h9a2 2 0 002-2v-5m-6 0v6m6-6H9m10-2a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg></button>` : ''}
            <button class="action-btn text-gray-400 hover:text-blue-600 p-1 rounded" title="更多操作" data-action="more" data-name="${encodeURIComponent(name)}"><svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 5v.01M12 12v.01M12 19v.01"></path></svg></button>
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
        else if (action === 'edit') { this.edit(target, name).catch((err) => this.toast('打开编辑失败: ' + err.message, 'error')); }
        else if (action === 'delete') { this.confirmDelete(target, name); }
        else if (action === 'more') { this.openRowMenu(btn, target, name, f); }
      });
      if (!isDir) {
        tr.addEventListener('dblclick', (e) => {
          if (e.target.closest('button') || e.target.closest('input')) return;
          this.edit(this._join(this.path, f.name), f.name).catch((err) => this.toast('打开编辑失败: ' + err.message, 'error'));
        });
      }
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

    const parts = [];
    await this.client.fetch(target, (data) => { parts.push(data); });

    // 每片单独 base64 解码成字节再拼接（服务端按 64KiB 独立编码每片，
    // 直接拼 base64 字符串会在接缝处出错），最后按 UTF-8 解码成文本；
    // 若用 atob() 的结果按 Latin-1 当文本，中文会乱码。
    const decoded = parts.map((b64) => {
      const bin = atob(b64);
      const u8 = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) u8[i] = bin.charCodeAt(i);
      return u8;
    });
    const bytes = new Uint8Array(decoded.reduce((n, u8) => n + u8.length, 0));
    let off = 0;
    for (const u8 of decoded) { bytes.set(u8, off); off += u8.length; }
    this._editState.contentEl.value = new TextDecoder('utf-8').decode(bytes);
    document.getElementById('edit-title').textContent = '编辑: ' + name;
    document.getElementById('edit-modal').classList.remove('hidden');
  }

  confirmDelete(target, name) {
    this._deleteState.setTarget(target);
    this._deleteState.msgEl.textContent = `确定要删除 "${name}" 吗？此操作不可恢复。`;
    document.getElementById('delete-modal').classList.remove('hidden');
  }

  // ---------- 通用表单弹窗 ----------
  openForm(title, fields, okLabel, onOk) {
    const modal = document.getElementById('form-modal');
    document.getElementById('form-title').textContent = title;
    const body = document.getElementById('form-fields');
    body.innerHTML = '';
    const inputs = {};
    for (const f of fields) {
      const wrap = document.createElement('div');
      const label = document.createElement('label');
      label.className = 'block text-sm font-medium text-gray-700 mb-1';
      label.textContent = f.label;
      wrap.appendChild(label);
      let input;
      if (f.type === 'select') {
        input = document.createElement('select');
        for (const opt of f.options) {
          const o = document.createElement('option');
          o.value = opt.value; o.textContent = opt.text;
          if (opt.value === f.value) o.selected = true;
          input.appendChild(o);
        }
      } else {
        input = document.createElement('input');
        input.type = f.type || 'text';
        if (f.placeholder) input.placeholder = f.placeholder;
        if (f.value !== undefined && f.value !== null) input.value = f.value;
      }
      input.className = 'w-full border border-gray-300 rounded-md px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500';
      if (f.hint) {
        const h = document.createElement('p');
        h.className = 'text-xs text-gray-500 mt-1';
        h.textContent = f.hint;
        wrap.appendChild(input); wrap.appendChild(h);
      } else {
        wrap.appendChild(input);
      }
      body.appendChild(wrap);
      inputs[f.id] = input;
    }
    const close = () => modal.classList.add('hidden');
    document.getElementById('form-close').onclick = close;
    document.getElementById('form-cancel').onclick = close;
    const okBtn = document.getElementById('form-ok');
    okBtn.textContent = okLabel || '确定';
    okBtn.onclick = async () => {
      const values = {};
      for (const k in inputs) values[k] = inputs[k].value;
      try {
        await onOk(values);
        close();
      } catch (err) {
        this.toast(err.message || '操作失败', 'error');
      }
    };
    modal.classList.remove('hidden');
  }

  // ---------- 行"更多"菜单 ----------
  openRowMenu(btn, target, name, file) {
    const menu = document.getElementById('row-menu');
    const isDir = file.type === 'directory';
    const lower = name.toLowerCase();
    const isArchive = lower.endsWith('.zip') || lower.endsWith('.tar.gz') || lower.endsWith('.tgz') || lower.endsWith('.tar');
    const items = [
      { label: '重命名', fn: () => this.showRename(target, name) },
      { label: '复制', fn: () => this.showCopyMove(target, name, false) },
      { label: '移动', fn: () => this.showCopyMove(target, name, true) },
      { label: '权限/属主', fn: () => this.showPerm(target, name, file) },
      { label: '打包为压缩包', fn: () => this.showArchive(target, name) },
    ];
    if (isArchive) items.push({ label: '解压到当前目录', fn: () => this.doExtract(target, this.path) });
    if (!isDir) {
      items.unshift({ label: '下载', fn: () => this.download(target, name) });
      items.unshift({ label: '编辑', fn: () => this.edit(target, name).catch((err) => this.toast('打开编辑失败: ' + err.message, 'error')) });
    }
    menu.innerHTML = '';
    for (const it of items) {
      const b = document.createElement('button');
      b.className = 'block w-full text-left px-3 py-2 hover:bg-blue-50 hover:text-blue-600 text-gray-700 transition-colors';
      b.textContent = it.label;
      b.onclick = (e) => { e.stopPropagation(); menu.classList.add('hidden'); it.fn(); };
      menu.appendChild(b);
    }
    const rect = btn.getBoundingClientRect();
    menu.style.top = Math.min(rect.bottom + 4, window.innerHeight - items.length * 36 - 16) + 'px';
    menu.style.left = Math.max(8, rect.right - 150) + 'px';
    menu.classList.remove('hidden');
    setTimeout(() => document.addEventListener('click', function close(e) {
      if (!menu.contains(e.target)) { menu.classList.add('hidden'); document.removeEventListener('click', close); }
    }), 0);
  }

  // ---------- 新建文件 ----------
  showNewFile() {
    if (!this.connected) { this.toast('请先连接容器', 'error'); return; }
    this.openForm('新建文件', [
      { id: 'name', label: '文件名', placeholder: '例如: config.json', hint: '在当前目录 ' + this.path + ' 下创建' },
    ], '创建', async (v) => {
      const n = (v.name || '').trim();
      if (!n || n.indexOf('/') >= 0) throw new Error('文件名不能为空且不能含 /');
      await this.client.createFile(this._join(this.path, n));
      this.toast('文件已创建', 'success');
      await this.refresh();
    });
  }

  // ---------- 重命名 ----------
  showRename(target, name) {
    this.openForm('重命名', [
      { id: 'name', label: '新名称', value: name },
    ], '保存', async (v) => {
      const n = (v.name || '').trim();
      if (!n || n === name) return;
      if (n.indexOf('/') >= 0) throw new Error('名称不能含 /');
      await this.client.rename(target, this._join(this.path, n));
      this.toast('已重命名', 'success');
      await this.refresh();
    });
  }

  // ---------- 复制 / 移动 ----------
  showCopyMove(target, name, isMove) {
    this.openForm(isMove ? '移动' : '复制', [
      { id: 'dst', label: '目标路径', value: target, hint: '填完整目标路径（可改名），如 ' + this.path + '/' + name + (isMove ? ' 移动后原文件消失' : ' 复制保留原文件') },
    ], isMove ? '移动' : '复制', async (v) => {
      const dst = (v.dst || '').trim();
      if (!dst) throw new Error('目标路径不能为空');
      if (!dst.startsWith('/')) throw new Error('目标必须是绝对路径');
      if (isMove) await this.client.move(target, dst);
      else await this.client.copy(target, dst);
      this.toast(isMove ? '已移动' : '已复制', 'success');
      await this.refresh();
    });
  }

  // ---------- 权限 / 属主 ----------
  showPerm(target, name, file) {
    const cur = file.permOctal || '';
    this.openForm('权限 / 属主 - ' + name, [
      { id: 'perm', label: '权限（八进制，如 644）', value: cur, placeholder: '644' },
      { id: 'uid', label: '用户 ID（留空不修改）', placeholder: '如 0' },
      { id: 'gid', label: '组 ID（留空不修改）', placeholder: '如 0' },
    ], '应用', async (v) => {
      const perm = (v.perm || '').trim();
      const uid = (v.uid || '').trim();
      const gid = (v.gid || '').trim();
      if (perm) {
        if (!/^[0-7]{3,4}$/.test(perm)) throw new Error('权限格式应为 3-4 位八进制');
        await this.client.chmod(target, perm);
      }
      if (uid !== '' || gid !== '') {
        const u = uid === '' ? -1 : parseInt(uid, 10);
        const g = gid === '' ? -1 : parseInt(gid, 10);
        await this.client.chown(target, u, g);
      }
      if (!perm && uid === '' && gid === '') throw new Error('没有可应用的修改');
      this.toast('已应用', 'success');
      await this.refresh();
    });
  }

  // ---------- 打包 ----------
  selectedNames() {
    const names = [];
    this.$tbody.querySelectorAll('.row-check:checked').forEach((c) => {
      names.push(decodeURIComponent(c.dataset.name));
    });
    return names;
  }

  showArchive(target, name) {
    this.showArchiveForm(this.path, [name]);
  }

  showArchiveSelected() {
    if (!this.connected) { this.toast('请先连接容器', 'error'); return; }
    const names = this.selectedNames();
    if (!names.length) { this.toast('请先勾选要打包的文件/目录', 'error'); return; }
    this.showArchiveForm(this.path, names);
  }

  showArchiveForm(dir, names) {
    const stamp = new Date().toISOString().slice(0, 16).replace(/[-:T]/g, '');
    this.openForm('打包压缩', [
      { id: 'name', label: '压缩包文件名', value: 'archive-' + stamp + '.tar.gz' },
      { id: 'format', label: '格式', type: 'select', value: 'tar.gz', options: [{ value: 'tar.gz', text: 'tar.gz（推荐）' }, { value: 'zip', text: 'zip（需容器内有 zip）' }] },
    ], '开始打包', async (v) => {
      let name = (v.name || '').trim();
      if (!name) throw new Error('文件名不能为空');
      if (name.indexOf('/') >= 0) throw new Error('文件名不能含 /');
      const format = v.format === 'zip' ? 'zip' : 'tar.gz';
      if (format === 'tar.gz' && !/\.tar\.gz$/i.test(name) && !/\.tgz$/i.test(name)) name += '.tar.gz';
      if (format === 'zip' && !/\.zip$/i.test(name)) name += '.zip';
      await this.client.archive(dir, names, format, name);
      this.toast('打包完成', 'success');
      await this.refresh();
    });
  }

  // ---------- 解压 ----------
  doExtract(target, dir) {
    (async () => {
      try {
        await this.client.extract(target, dir);
        this.toast('解压完成', 'success');
        await this.refresh();
      } catch (err) {
        this.toast('解压失败: ' + (err.message || ''), 'error');
      }
    })();
  }

  // ---------- 上传目录 ----------
  async uploadDirectory(files) {
    if (!this.connected) { this.toast('请先连接容器', 'error'); return; }
    if (!files || !files.length) return;
    let root = files[0].webkitRelativePath ? files[0].webkitRelativePath.split('/')[0] : '';
    if (!root) { this.toast('无法识别文件夹结构', 'error'); return; }
    this.toast('开始上传目录 ' + root + '（' + files.length + ' 个文件）', 'info');
    const made = new Set();
    let ok = 0, fail = 0;
    for (const f of files) {
      const rel = f.webkitRelativePath || f.name;
      const parts = rel.split('/');
      parts.shift(); // 去掉顶层目录名
      if (!parts.length || parts.some((p) => !p || p === '.' || p === '..')) { fail++; continue; }
      // 逐层确保目录存在（已存在则忽略报错）
      let acc = this._join(this.path, root);
      try { await this.client.mkdir(acc); } catch (e) {}
      for (let i = 0; i < parts.length - 1; i++) {
        acc = this._join(acc, parts[i]);
        if (!made.has(acc)) { try { await this.client.mkdir(acc); } catch (e) {} made.add(acc); }
      }
      const dest = this._join(acc, parts[parts.length - 1]);
      try {
        await this.client.upload(f, dest, () => {});
        ok++;
      } catch (e) { fail++; }
    }
    this.toast('目录上传完成: 成功 ' + ok + '，失败 ' + fail, fail ? 'error' : 'success');
    await this.refresh();
  }
}